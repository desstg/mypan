package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBodyBytes 限制响应读取量。一页 500 个条目的 JSON 约几百 KB，
// 给 16MB 足够余量，同时防止上游异常返回超大体时把内存吃满。
const maxBodyBytes = 16 << 20

// pageSize 是分页拉取每页的条目数。500 是 Emby/Jellyfin 都接受的常见取值，
// 调大会被服务端截断，调小只是多跑几轮。
const pageSize = 500

// Client 是一台 Emby / Jellyfin 的客户端。
type Client struct {
	baseURL string
	apiKey  string
	name    string
	kind    string
	http    *http.Client
}

// Options 构造客户端。
type Options struct {
	URL      string
	APIKey   string
	Name     string
	Type     string // emby / jellyfin
	Timeout  time.Duration
	ProxyURL string
}

// New 构造客户端。URL 为空或 API Key 为空都算配置不全。
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.URL), "/")
	if base == "" {
		return nil, fmt.Errorf("服务器地址为空")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("服务器地址必须以 http:// 或 https:// 开头")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	// Emby/Jellyfin 在局域网上，**不跟随系统代理** —— 把它塞进公网代理会直接连不上。
	// 只有用户显式配了代理才走。
	if proxy := strings.TrimSpace(opts.ProxyURL); proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效: %w", err)
		}
		transport.Proxy = http.ProxyURL(pu)
	}

	kind := strings.ToLower(strings.TrimSpace(opts.Type))
	if kind != "jellyfin" {
		kind = "emby"
	}

	return &Client{
		baseURL: base,
		apiKey:  strings.TrimSpace(opts.APIKey),
		name:    strings.TrimSpace(opts.Name),
		kind:    kind,
		http:    &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// ServerInfo 是 /System/Info 的关键字段。
type ServerInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
}

// Ping 探活。返回的字符串是给用户看的「连上了谁」。
func (c *Client) Ping(ctx context.Context) (string, error) {
	var info ServerInfo
	if err := c.getJSON(ctx, "/System/Info", nil, &info); err != nil {
		return "", err
	}
	name := info.ServerName
	if name == "" {
		name = info.Version
	}
	if name == "" {
		name = "在线"
	}
	return name, nil
}

// Item 是媒体库里的一条。
type Item struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
	Path string `json:"Path"`
	Type string `json:"Type"`
}

type itemsResp struct {
	Items            []Item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
}

// FetchAllMovies 拉全库条目，自动翻页。
//
// 只取 Movie 类型：Series / Episode / BoxSet 都会混进同一个库，
// 它们的 Name 往往是剧名而不是番号，一起收进来会把「已入库」判得一塌糊涂。
func (c *Client) FetchAllMovies(ctx context.Context) ([]Item, error) {
	out := make([]Item, 0, pageSize)
	start := 0
	for {
		params := url.Values{
			"Recursive":        {"true"},
			"IncludeItemTypes": {"Movie"},
			"Fields":           {"Path"},
			"StartIndex":       {fmt.Sprint(start)},
			"Limit":            {fmt.Sprint(pageSize)},
		}
		var page itemsResp
		if err := c.getJSON(ctx, "/Items", params, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)

		// 三种停止条件，缺一个都可能死循环：
		// 本轮不足一页（自然结束）、累计已够总数、上游没给总数时靠前两者兜底。
		if len(page.Items) < pageSize {
			break
		}
		start += len(page.Items)
		if page.TotalRecordCount > 0 && start >= page.TotalRecordCount {
			break
		}
	}
	return out, nil
}

// itemDetail 是 /Items/{id} 的响应里我们用得上的部分。
type itemDetail struct {
	Item
	MediaSources []mediaSource `json:"MediaSources"`
}

type mediaSource struct {
	Size         int64         `json:"Size"`
	Height       int           `json:"Height"`
	MediaStreams []mediaStream `json:"MediaStreams"`
}

type mediaStream struct {
	Type   string `json:"Type"`
	Height int    `json:"Height"`
}

// ItemMedia 读单条条目的画质信息。
//
// 这是质检回填的入口：列表接口给不出分辨率，要逐条查详情。
// 所以调用方必须限速 —— 一个几千部的库全量回填会对服务器造成持续压力。
func (c *Client) ItemMedia(ctx context.Context, itemID string) (resolution int, sizeBytes int64, err error) {
	var detail itemDetail
	params := url.Values{"Fields": {"MediaSources"}}
	if err := c.getJSON(ctx, "/Items/"+url.PathEscape(itemID), params, &detail); err != nil {
		return 0, 0, err
	}

	heights := make([]int, 0, len(detail.MediaSources))
	sizes := make([]int64, 0, len(detail.MediaSources))
	for _, src := range detail.MediaSources {
		sizes = append(sizes, src.Size)
		if src.Height > 0 {
			heights = append(heights, src.Height)
		}
		// Height 有时只挂在媒体流上（尤其是转码过的库）。
		for _, stream := range src.MediaStreams {
			if strings.EqualFold(stream.Type, "Video") && stream.Height > 0 {
				heights = append(heights, stream.Height)
			}
		}
	}
	resolution, sizeBytes = ItemQuality(heights, sizes)
	return resolution, sizeBytes, nil
}

// Search 按关键字在服务器上搜一条，用于「这个番号到底入库没有」的实时核对。
func (c *Client) Search(ctx context.Context, keyword string) ([]Item, error) {
	params := url.Values{
		"SearchTerm":       {keyword},
		"Recursive":        {"true"},
		"IncludeItemTypes": {"Movie"},
		"Limit":            {"3"},
	}
	var page itemsResp
	if err := c.getJSON(ctx, "/Items", params, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// getJSON 发一次带 api_key 的 GET 并解 JSON。
func (c *Client) getJSON(ctx context.Context, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LitePan")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("地址错误或无法连接: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		// 单独认 401：这是唯一一个「用户自己能修」的错误，
		// 混进通用的 HTTP 错误里会让人以为是网络问题去查网络。
		return fmt.Errorf("API Key 错误（401）")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("连接失败（HTTP %d）", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("响应不是预期的 JSON（可能不是 Emby/Jellyfin 地址）: %w", err)
	}
	return nil
}
