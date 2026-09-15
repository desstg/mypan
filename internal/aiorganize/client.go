package aiorganize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

const recognitionSystemPrompt = `你是媒体文件识别助手。输入是内置规则无法稳定识别的多个作品组。
只能根据输入中已有的 work_id、目录名、文件名和候选信息判断，不得虚构文件。
只返回 JSON 对象，格式为：
{"items":[{"work_id":"work_1","recognized":true,"title":"中文或常用标题","original_title":"可选原名","year":2024,"media_type":"movie|tv","season":1,"files":[{"source_id":"source_1","episode":1,"kind":"episode|movie|extra"}]}]}
每个 work_id 最多返回一次。无法稳定判断时返回 recognized=false，不要猜。不要返回目标目录、TMDB ID、置信度、文件新名或任何操作。`

const recognitionRepairPrompt = `将下面内容修正为严格 JSON。只返回一个对象，顶层只有 items 数组。items 中只允许 work_id、recognized、title、original_title、year、media_type、season、files；files 中只允许 source_id、episode、kind。不要解释，不要 Markdown 代码块。`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type modelProtocol uint8

const (
	protocolOpenAI modelProtocol = iota + 1
	protocolAnthropic
)

type anthropicRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []chatMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (s *Service) chat(ctx context.Context, cfg Config, messages []chatMessage) (string, error) {
	protocols := s.protocolCandidates(cfg)
	var lastErr error
	for index, protocol := range protocols {
		content, status, body, err := s.chatWithProtocol(ctx, cfg, messages, protocol)
		if err == nil {
			s.rememberProtocol(cfg, protocol)
			return content, nil
		}
		lastErr = err
		if index == len(protocols)-1 || !isProtocolMismatch(status, body) {
			return "", err
		}
	}
	return "", lastErr
}

func (s *Service) protocolCandidates(cfg Config) []modelProtocol {
	key := protocolCacheKey(cfg)
	s.mu.Lock()
	remembered := s.protocols[key]
	s.mu.Unlock()
	if remembered != 0 {
		return []modelProtocol{remembered, otherProtocol(remembered)}
	}
	preferred := protocolOpenAI
	hint := strings.ToLower(cfg.BaseURL + " " + cfg.Model)
	if strings.Contains(hint, "anthropic") || strings.Contains(hint, "claude") || strings.Contains(hint, "/messages") {
		preferred = protocolAnthropic
	}
	return []modelProtocol{preferred, otherProtocol(preferred)}
}

func (s *Service) rememberProtocol(cfg Config, protocol modelProtocol) {
	s.mu.Lock()
	s.protocols[protocolCacheKey(cfg)] = protocol
	s.mu.Unlock()
}

func protocolCacheKey(cfg Config) string {
	return strings.ToLower(strings.TrimSpace(cfg.BaseURL)) + "\x00" + strings.ToLower(strings.TrimSpace(cfg.Model))
}

func otherProtocol(protocol modelProtocol) modelProtocol {
	if protocol == protocolAnthropic {
		return protocolOpenAI
	}
	return protocolAnthropic
}

func (s *Service) chatWithProtocol(
	ctx context.Context,
	cfg Config,
	messages []chatMessage,
	protocol modelProtocol,
) (string, int, []byte, error) {
	if protocol == protocolAnthropic {
		return s.doAnthropicChat(ctx, cfg, messages)
	}
	content, status, body, err := s.doOpenAIChat(ctx, cfg, messages, true)
	if status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "response_format") {
		return s.doOpenAIChat(ctx, cfg, messages, false)
	}
	return content, status, body, err
}

func (s *Service) doOpenAIChat(
	ctx context.Context,
	cfg Config,
	messages []chatMessage,
	jsonMode bool,
) (string, int, []byte, error) {
	endpoint, err := openAIEndpoint(cfg.BaseURL)
	if err != nil {
		return "", 0, nil, err
	}
	payload := chatRequest{Model: cfg.Model, Messages: messages}
	if jsonMode {
		payload.ResponseFormat = map[string]any{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, nil, err
	}
	status, data, err := s.executeModelRequest(ctx, endpoint, body, map[string]string{
		"Authorization": "Bearer " + cfg.APIKey,
	})
	if err != nil {
		return "", status, data, err
	}
	var decoded chatResponse
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded.Choices) == 0 {
		return "", status, data, domain.Errorf(domain.CodeDriverError, "模型服务返回了无法识别的内容")
	}
	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if content == "" {
		return "", status, data, domain.Errorf(domain.CodeDriverError, "模型没有返回识别结果")
	}
	return content, status, data, nil
}

func (s *Service) doAnthropicChat(
	ctx context.Context,
	cfg Config,
	messages []chatMessage,
) (string, int, []byte, error) {
	endpoint, err := anthropicEndpoint(cfg.BaseURL)
	if err != nil {
		return "", 0, nil, err
	}
	system, input := splitAnthropicMessages(messages)
	body, err := json.Marshal(anthropicRequest{
		Model:     cfg.Model,
		MaxTokens: 4096,
		System:    system,
		Messages:  input,
	})
	if err != nil {
		return "", 0, nil, err
	}
	status, data, err := s.executeModelRequest(ctx, endpoint, body, map[string]string{
		"x-api-key":         cfg.APIKey,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return "", status, data, err
	}
	var decoded anthropicResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", status, data, domain.Errorf(domain.CodeDriverError, "模型服务返回了无法识别的内容")
	}
	parts := make([]string, 0, len(decoded.Content))
	for _, block := range decoded.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	content := strings.Join(parts, "\n")
	if content == "" {
		return "", status, data, domain.Errorf(domain.CodeDriverError, "模型没有返回识别结果")
	}
	return content, status, data, nil
}

func splitAnthropicMessages(messages []chatMessage) (string, []chatMessage) {
	system := make([]string, 0, 1)
	input := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			system = append(system, message.Content)
			continue
		}
		input = append(input, message)
	}
	return strings.Join(system, "\n\n"), input
}

func (s *Service) executeModelRequest(
	ctx context.Context,
	endpoint string,
	body []byte,
	headers map[string]string,
) (int, []byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, data, err := httpx.Execute(s.http, req, 4<<20)
		if err != nil {
			if attempt == 0 && waitContext(ctx, 350*time.Millisecond) {
				continue
			}
			return 0, nil, domain.Errorf(domain.CodeDriverError, "连接模型服务失败")
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt == 0 && waitContext(ctx, 500*time.Millisecond) {
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp.StatusCode, data, modelHTTPError(resp.StatusCode)
		}
		return resp.StatusCode, data, nil
	}
	return 0, nil, fmt.Errorf("model request failed")
}

func openAIEndpoint(baseURL string) (string, error) {
	u, err := parseModelURL(baseURL)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), "/chat/completions") {
		u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	}
	return u.String(), nil
}

func anthropicEndpoint(baseURL string) (string, error) {
	u, err := parseModelURL(baseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(strings.ToLower(path), "/messages"):
	case strings.HasSuffix(strings.ToLower(path), "/v1"):
		path += "/messages"
	default:
		path += "/v1/messages"
	}
	u.Path = path
	return u.String(), nil
}

func parseModelURL(baseURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, domain.Errorf(domain.CodeValidation, "API 地址无效")
	}
	return u, nil
}

func isProtocolMismatch(status int, body []byte) bool {
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusOK {
		return true
	}
	if status != http.StatusBadRequest && status != http.StatusUnauthorized &&
		status != http.StatusForbidden && status != http.StatusUnprocessableEntity {
		return false
	}
	message := strings.ToLower(string(body))
	for _, hint := range []string{"chat/completions", "/v1/messages", "x-api-key", "anthropic-version", "unknown endpoint"} {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

func modelHTTPError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return domain.Errorf(domain.CodePermissionDenied, "API Key 无效或没有模型访问权限")
	case http.StatusNotFound:
		return domain.Errorf(domain.CodeNotFound, "API 地址或模型名称不正确")
	case http.StatusTooManyRequests:
		return domain.Errorf(domain.CodeRateLimited, "模型服务请求过于频繁，请稍后重试")
	default:
		return domain.Errorf(domain.CodeDriverError, "模型服务请求失败（HTTP %d）", status)
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
