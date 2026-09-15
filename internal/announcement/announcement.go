// Package announcement 拉取并缓存后台公告，远端不可用时静默降级。
package announcement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultURL 是公告文件地址，指向本项目仓库里的 announcement.json。
//
// 注意用 raw.githubusercontent.com 而不是 github.com/.../blob/...：后者返回的是
// 网页 HTML，解析会被拒掉、表现为「没有公告」且不报错。
// 用户可在「系统设置 → 其他设置 → 后台公告」里覆盖成自己的地址。
const DefaultURL = "https://raw.githubusercontent.com/desstg/mypan/main/announcement.json"

// Section 是公告正文的一个小节。
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Announcement 是解析后的公告内容。
type Announcement struct {
	// Version 用于公告判重，缺失时使用内容哈希。
	Version   string    `json:"notice_version"`
	Badge     string    `json:"badge"`
	Title     string    `json:"dialog_title"`
	Banner    string    `json:"banner"`
	Special   string    `json:"special"`
	Lead      string    `json:"lead"`
	Sections  []Section `json:"issues"`
	Footnote  string    `json:"footnote"`
	FetchedAt time.Time `json:"fetched_at"`
}

const (
	fetchTimeout = 10 * time.Second
	cacheTTL     = 10 * time.Minute
	// failCooldown 避免失败后频繁请求远端。
	failCooldown = 5 * time.Minute
	// maxBodyBytes 远端文件大小上限，防止异常大文件拖垮请求。
	maxBodyBytes = 512 * 1024
)

// Service 拉取并缓存公告。
type Service struct {
	resolve func() string
	client  *http.Client

	mu       sync.Mutex
	cached   *Announcement
	cachedAt time.Time
	failedAt time.Time
	// activeURL 是上次实际使用的地址。地址一变，上面的缓存与失败冷却都属于上一个地址，
	// 必须一起作废，否则换了地址还要等满 cacheTTL 才生效，表现为「改了没用」。
	activeURL string
}

// New 构造固定地址的公告服务。
func New(url string) *Service {
	trimmed := strings.TrimSpace(url)
	return NewDynamic(func() string { return trimmed })
}

// NewDynamic 构造地址可变的公告服务：每次拉取时调 resolve 取当前地址，
// 供后台的「公告地址」设置项热更新使用。
func NewDynamic(resolve func() string) *Service {
	return &Service{
		resolve: resolve,
		client:  &http.Client{Timeout: fetchTimeout},
	}
}

// currentURL 返回当前生效的公告地址；未配置时为空串。
func (s *Service) currentURL() string {
	if s.resolve == nil {
		return ""
	}
	return strings.TrimSpace(s.resolve())
}

// Enabled 返回公告服务是否配置了远端文件。
func (s *Service) Enabled() bool {
	return s.currentURL() != ""
}

// Fetch 返回当前公告；拉取失败时静默返回旧缓存。
func (s *Service) Fetch(ctx context.Context) (*Announcement, error) {
	url := s.currentURL()
	if url == "" {
		return nil, nil
	}
	s.mu.Lock()
	now := time.Now()
	// 地址换过了：上一个地址的缓存和失败冷却都不该继续影响新地址。
	if url != s.activeURL {
		s.activeURL = url
		s.cached = nil
		s.cachedAt = time.Time{}
		s.failedAt = time.Time{}
	}
	if s.cached != nil && now.Sub(s.cachedAt) < cacheTTL {
		item := *s.cached
		s.mu.Unlock()
		return &item, nil
	}
	if !s.failedAt.IsZero() && now.Sub(s.failedAt) < failCooldown {
		item := s.cached
		s.mu.Unlock()
		return item, nil
	}
	s.mu.Unlock()

	body, err := s.fetchBody(ctx, url)
	if err != nil {
		s.mu.Lock()
		s.failedAt = time.Now()
		item := s.cached
		s.mu.Unlock()
		return item, nil
	}

	item := parse(body)
	if item == nil {
		s.mu.Lock()
		s.failedAt = time.Now()
		cached := s.cached
		s.mu.Unlock()
		return cached, nil
	}
	item.FetchedAt = time.Now()
	s.mu.Lock()
	s.cached = item
	s.cachedAt = item.FetchedAt
	s.failedAt = time.Time{}
	s.mu.Unlock()
	return item, nil
}

func (s *Service) fetchBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LitePan/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("announcement file exceeds %d bytes", maxBodyBytes)
	}
	return body, nil
}

// parse 只接受文档站约定的有效 JSON。格式异常、非 JSON 或没有可展示正文时返回 nil，
// 调用方静默沿用旧缓存或返回暂无公告，避免把错误页和损坏内容展示给用户。
func parse(raw []byte) *Announcement {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	hash := contentHash(text)
	if a, ok := parseJSON(raw, hash); ok {
		return &a
	}
	return nil
}

type jsonAnnouncement struct {
	Version  string    `json:"notice_version"`
	Badge    string    `json:"badge"`
	Title    string    `json:"dialog_title"`
	Banner   string    `json:"banner"`
	Special  string    `json:"special"`
	Lead     string    `json:"lead"`
	Issues   []Section `json:"issues"`
	Footnote string    `json:"footnote"`
}

func parseJSON(raw []byte, hash string) (Announcement, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return Announcement{}, false
	}
	var ja jsonAnnouncement
	if err := json.Unmarshal(raw, &ja); err != nil {
		return Announcement{}, false
	}
	if strings.TrimSpace(ja.Title) == "" && strings.TrimSpace(ja.Lead) == "" && len(ja.Issues) == 0 {
		return Announcement{}, false
	}
	version := strings.TrimSpace(ja.Version)
	if version == "" {
		version = hash
	}
	sections := make([]Section, 0, len(ja.Issues))
	for _, s := range ja.Issues {
		s.Title = strings.TrimSpace(s.Title)
		s.Body = strings.TrimSpace(s.Body)
		if s.Title == "" && s.Body == "" {
			continue
		}
		sections = append(sections, s)
	}
	title := strings.TrimSpace(ja.Title)
	if title == "" {
		title = "公告"
	}
	return Announcement{
		Version:  version,
		Badge:    normalizeVisible(ja.Badge),
		Title:    title,
		Banner:   normalizeVisible(ja.Banner),
		Special:  normalizeVisible(ja.Special),
		Lead:     normalizeVisible(ja.Lead),
		Sections: sections,
		Footnote: strings.TrimSpace(ja.Footnote),
	}, true
}

// normalizeVisible 归一化可选文本区（badge/banner/special/lead）：
// 空值、none、false（不区分大小写）一律视为不显示。
func normalizeVisible(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none", "false":
		return ""
	}
	return strings.TrimSpace(v)
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
