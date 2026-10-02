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
	"net/url"
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
	// resolveProxy 返回当前该用的代理地址（空串 = 直连）。与 resolve 同款：
	// 每次拉取时现取，供后台设置热更新。注入成回调而不是直接吃 settings，
	// 是因为本包是叶子包 —— 让它 import settings 会把依赖方向倒过来。
	resolveProxy func() string

	transport *http.Transport
	// client 是按当前地址与代理现算出来的。两者任一变化都要重建，
	// 见 applyTransport。
	client *http.Client

	mu       sync.Mutex
	cached   *Announcement
	cachedAt time.Time
	failedAt time.Time
	// activeURL 是上次实际使用的地址。地址一变，上面的缓存与失败冷却都属于上一个地址，
	// 必须一起作废，否则换了地址还要等满 cacheTTL 才生效，表现为「改了没用」。
	activeURL string
	// activeProxy 同理：代理换了，记在旧代理上的失败冷却不该继续挡着新代理。
	activeProxy string
}

// New 构造固定地址的公告服务。
func New(url string) *Service {
	trimmed := strings.TrimSpace(url)
	return NewDynamic(func() string { return trimmed })
}

// NewDynamic 构造地址可变的公告服务：每次拉取时调 resolve 取当前地址，
// 供后台的「公告地址」设置项热更新使用。
func NewDynamic(resolve func() string) *Service {
	return NewDynamicWithProxy(resolve, nil)
}

// NewDynamicWithProxy 在 NewDynamic 之上再接一个「当前代理地址」的取值回调。
//
// resolveProxy 为 nil 或返回空串时直连 —— 不引入开关，**填了代理就走代理**。
// 这条规则对「后台小功能」才成立，见 fetchBody 里的失败回落：代理填错时
// 会自动退回直连再试一次，所以宁可多试一条路，也不要静默地什么都拉不到。
func NewDynamicWithProxy(resolve func() string, resolveProxy func() string) *Service {
	transport := &http.Transport{}
	return &Service{
		resolve:      resolve,
		resolveProxy: resolveProxy,
		transport:    transport,
		// **必须显式挂上 transport**：只建 `&http.Client{Timeout: …}` 的话它用的是
		// http.DefaultTransport，applyTransport 往 s.transport 上装的代理根本不会生效
		// —— 而且是静默的（照常直连、照常失败），这条路踩过一次。
		client: &http.Client{Timeout: fetchTimeout, Transport: transport},
	}
}

// currentURL 返回当前生效的公告地址；未配置时为空串。
func (s *Service) currentURL() string {
	if s.resolve == nil {
		return ""
	}
	return strings.TrimSpace(s.resolve())
}

// currentProxy 返回当前生效的代理地址；没配时为空串（直连）。
func (s *Service) currentProxy() string {
	if s.resolveProxy == nil {
		return ""
	}
	return strings.TrimSpace(s.resolveProxy())
}

// applyTransport 按代理地址（重新）装配 transport，代理没变时什么都不做。
//
// **必须每次调用**，不能只在构造时配一次：用户在设置页填上代理之后，
// 下一次拉取就该走上它，而不是等到重启。
//
// 直接给 http.ProxyURL 而不是拼 Transport 的其它字段：ProxyURL 会原样返回
// 已经解析好的 URL，也支持带账号密码的形式（settings 那边已经把凭据拼进去了）。
func (s *Service) applyTransport(proxy string) {
	if proxy == s.activeProxy {
		return
	}
	s.activeProxy = proxy
	if proxy == "" {
		s.transport.Proxy = nil
		return
	}
	parsed, err := url.Parse(proxy)
	if err != nil || parsed.Host == "" {
		// 地址写错时按直连走，而不是把 client 弄成一个必然失败的形态 ——
		// fetchBody 的失败回落会再兜一次底，但能在这里就判掉更好。
		s.transport.Proxy = nil
		return
	}
	s.transport.Proxy = http.ProxyURL(parsed)
}

// URL 返回当前生效的公告地址（未配置时为空串）。
//
// 与 Enabled 一样是「问一句现在是什么」，供调用方在拉不到内容时把地址记进日志 ——
// 失败原因基本都在这个地址上，光记「拉取失败」等于什么都没说。
func (s *Service) URL() string { return s.currentURL() }

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
	// 代理换过了同理：旧代理上的失败不该挡住新代理。同样要按当前值现装，
	// 用户刚填上代理时下一次拉取就得生效。
	if proxy := s.currentProxy(); proxy != s.activeProxy {
		s.applyTransport(proxy)
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

// fetchBody 取远端公告正文。**代理失败会自动回落直连** —— 见 fetchOnce 的说明。
func (s *Service) fetchBody(ctx context.Context, url string) ([]byte, error) {
	body, err := s.fetchOnce(ctx, url)
	if err == nil {
		return body, nil
	}
	// 回落：只有「这次确实走了代理」才值得再试一条路。
	//
	// 为什么必须有这条：规则是「填了代理就走代理、不看开关」，而用户填的代理
	// 完全可能只对 TMDB 有效（那种按域名分流的代理很常见）。没有回落的话，
	// 一个对 GitHub 无效的代理会把公告整个打死 —— 而失败是**静默**的
	// （见包注释的降级设计），表现就是「公告莫名其妙不弹了」，
	// 比原来「没代理所以拉不到」更难查。公告是个几 KB 的小文件，
	// 多试一次直连的代价可以忽略。
	if s.currentProxy() == "" {
		return nil, err
	}
	direct, directErr := s.fetchDirect(ctx, url)
	if directErr != nil {
		return nil, err // 报代理那一次的错误：它才是用户该看到的原因
	}
	return direct, nil
}

// fetchOnce 用**当前** transport（可能带代理）请求一次。
func (s *Service) fetchOnce(ctx context.Context, url string) ([]byte, error) {
	return s.do(ctx, s.client, url)
}

// fetchDirect 明确绕开代理请求一次，供代理失败时回落。
//
// 用一次性 client（Proxy 显式置 nil）而不是把共享 transport 的代理改掉：
// 后者会与正在并发进行的其它拉取打架。
func (s *Service) fetchDirect(ctx context.Context, url string) ([]byte, error) {
	return s.do(ctx, &http.Client{
		Timeout:   fetchTimeout,
		Transport: &http.Transport{Proxy: nil},
	}, url)
}

func (s *Service) do(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LitePan/1.0")
	resp, err := client.Do(req)
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
