package synopsis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// jav321：有码那批的简介来源。
//
// 流程是**两步**，不能省：
//
//	① GET  /            拿 cookie（不带 cookie 直接 POST 搜索会被 301 走）
//	② POST /search      表单 sn=<番号>，结果页里取第一个 /video/<slug>
//	③ GET  /video/<slug> 简介在 `col-md-12` 的那个正文块里
//
// 为什么不能直接拼详情页 URL：它的 slug 是「番号去连字符小写 + 数字补零」
// （`SSIS-001` → `ssis00001`，`NIMA-086` → `nima00086`），补零位数按站上实际条目来，
// 拼错了就是 404 —— 实测 `start00154` 这种形态也存在。
//
// ⚠️ **搜索结果不保证是那部片**：实测搜 `NIMA-086` 回来的是 `1start00154`。
// 所以拿到 slug 后要在结果页里核对该条目的标题里带着番号（忽略连字符），
// 核对不上就当作「这站没有」——宁可空着，也不能把别的片的剧情写进这部。
type Jav321 struct {
	baseURL string
	c       *conn

	mu      sync.Mutex
	cookies []*http.Cookie
}

type Jav321Options struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
}

// NewJav321 构造 jav321 来源。
func NewJav321(opts Jav321Options) *Jav321 {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.jav321.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL})
	c.thr = throttler{gap: gap}
	// 保留每次请求的 cookie（① 拿到的那个），与 javbus 那边用 AddCookie 不同：
	// 这里的 cookie 是服务端发的，得让 http.Client 自己记。**两条出口各一份 jar** ——
	// 共用一份会把直连拿到的会话 cookie 带到代理那条路上（换出口时那套 cookie 本来就作废）。
	c.direct.Jar = newSimpleJar()
	if c.viaProxy != c.direct {
		c.viaProxy.Jar = newSimpleJar()
	}
	return &Jav321{baseURL: base, c: c}
}

func (s *Jav321) Name() string { return "jav321" }

// Fetch 按番号取简介。
func (s *Jav321) Fetch(ctx context.Context, number string) (string, error) {
	code := normalizeJav321Code(number)
	if code == "" {
		return "", nil
	}
	if err := s.ensureSession(ctx); err != nil {
		return "", err
	}

	// ② 搜索
	searchURL := s.baseURL + "/search"
	body, err := s.c.postForm(ctx, searchURL, url.Values{"sn": {number}}, s.baseURL+"/", s.cookies)
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil
		}
		return "", fmt.Errorf("jav321 搜索失败: %w", err)
	}

	slug, ok := pickJav321Hit(body, code)
	if !ok {
		// 搜索没给出**对得上**的那一条时走兜底：详情页 URL 是「番号去连字符小写」，
		// 而且**两种形态都要试**（`ssis001` 与补零的 `ssis00001` 都存在，实测同一部片
		// 两种都能打开）。不试的话「搜索结果里恰好没有它」就会白白漏掉一部。
		slug, ok = s.probeJav321Detail(ctx, code, searchURL)
		if !ok {
			return "", nil // 这站没有这部
		}
	}

	// ③ 详情页
	page, err := s.c.fetch(ctx, s.baseURL+"/video/"+slug, searchURL, "", s.cookies)
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil
		}
		return "", fmt.Errorf("jav321 详情页抓取失败: %w", err)
	}
	return ParseJav321Intro(page), nil
}

// probeJav321Detail 在搜索没给出对口结果时，按两种 slug 形态各试一次详情页。
//
// 判据是「页面里真的出现了这个番号」（标题或正文）—— 只是 200 不算数：
// jav321 对不存在的 slug 也会给页面（带一堆推荐影片），不核对就会把别人的简介抓回来。
func (s *Jav321) probeJav321Detail(ctx context.Context, code, referer string) (string, bool) {
	for _, slug := range jav321SlugCandidates(code) {
		page, err := s.c.fetch(ctx, s.baseURL+"/video/"+slug, referer, "", s.cookies)
		if err != nil {
			continue
		}
		if strings.Contains(foldKey(stripTags(page)), code) && ParseJav321Intro(page) != "" {
			return slug, true
		}
	}
	return "", false
}

// jav321SlugCandidates 给出番号的两种 slug 形态（去连字符小写）。
//
//	ssis001 → ssis001, ssis00001     （数字位数不定：站上两种都有）
//	nima086 → nima086, nima00086
func jav321SlugCandidates(code string) []string {
	out := []string{code}
	// 把末尾的数字补零到 5 位（`086` → `00086`），再与不补的那个并列。
	i := len(code)
	for i > 0 && code[i-1] >= '0' && code[i-1] <= '9' {
		i--
	}
	if i < len(code) {
		digits := code[i:]
		if len(digits) < 5 {
			padded := strings.Repeat("0", 5-len(digits)) + digits
			if cand := code[:i] + padded; cand != code {
				out = append(out, cand)
			}
		}
	}
	return out
}

// ensureSession 先 GET 一次首页（拿 cookie）。只做一次。
func (s *Jav321) ensureSession(ctx context.Context) error {
	s.mu.Lock()
	done := len(s.cookies) > 0
	s.mu.Unlock()
	if done {
		return nil
	}
	if _, err := s.c.fetch(ctx, s.baseURL+"/", "", "", nil); err != nil {
		return fmt.Errorf("jav321 首页不可达: %w", err)
	}
	s.mu.Lock()
	s.cookies = nil // 真正要的是 client.Jar 里的那份，这里只是标记已初始化
	s.mu.Unlock()
	// 标记：往 cookies 里塞一个哨兵（fetch 时会带上，服务端不认也无害）。
	s.cookies = []*http.Cookie{{Name: "_synopsis_session", Value: "1"}}
	return nil
}

// simpleJar 是一个只按域名存 cookie 的最小实现（不引第三方）。
// 只需要在同一个来源的几次请求之间保持会话，不需要过期/路径的精细语义。
func newSimpleJar() http.CookieJar { return &simpleJar{m: map[string][]*http.Cookie{}} }

type simpleJar struct {
	mu sync.Mutex
	m  map[string][]*http.Cookie
}

func (j *simpleJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.m["default"] = append(j.m["default"], cookies...)
}

func (j *simpleJar) Cookies(_ *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.m["default"]
}

// pickJav321Hit 从搜索结果页里挑出**确实属于这个番号**的详情页 slug。
//
// 判据：结果页里 `href="/video/<slug>"` 的条目，其标题（同一个 <a> 的文本）
// 去掉连字符/空格并转小写之后**包含**目标番号。搜索结果经常夹着同片商的其他片
// （实测搜 NIMA-086 第一条是 1start00154），不核对就会把别人的剧情写进这部。
func pickJav321Hit(page, code string) (string, bool) {
	for _, m := range jav321HitRe.FindAllStringSubmatch(page, -1) {
		slug, label := m[1], stripTags(m[2])
		if jav321LabelMatches(label, code) {
			return slug, true
		}
	}
	return "", false
}

// jav321HitRe 抓 `<a href="/video/xxx" ...>标题</a>`。
var jav321HitRe = regexp.MustCompile(`(?is)href="/video/([a-z0-9]+)"[^>]*>(.{0,200}?)</a>`)

// jav321LabelMatches 报告标题里是不是真的有这个番号（忽略连字符、下划线、空格与大小写）。
func jav321LabelMatches(label, code string) bool {
	return strings.Contains(foldKey(label), foldKey(code))
}

func foldKey(s string) string {
	s = strings.ToLower(s)
	r := strings.NewReplacer("-", "", "_", "", " ", "", "　", "")
	return r.Replace(s)
}

// normalizeJav321Code 把番号归到可比较的形态（同样忽略连字符）。
func normalizeJav321Code(number string) string {
	code := foldKey(strings.TrimSpace(number))
	if code == "" {
		return ""
	}
	return code
}

// jav321IntroRe 抓详情页里那段简介。
//
// 位置实测：`<div class="row"><div class="col-md-12">` 里、紧跟 iframe 之后的那段正文。
// 兜底再退一步：随便找第一个够长的 `col-md-12` 块。
var (
	jav321IntroAfterIframeRe = regexp.MustCompile(`(?is)</iframe>\s*<div class="row">\s*<div class="col-md-12">(.{40,900}?)</div>`)
	jav321IntroFallbackRe    = regexp.MustCompile(`(?is)<div class="col-md-12">\s*([^<]{60,900}?)\s*</div>`)
)

// ParseJav321Intro 从详情页 HTML 里取简介文本（纯函数，夹具用例覆盖）。
func ParseJav321Intro(page string) string {
	if m := jav321IntroAfterIframeRe.FindStringSubmatch(page); m != nil {
		if text := cleanIntro(stripTags(m[1])); text != "" {
			return text
		}
	}
	if m := jav321IntroFallbackRe.FindStringSubmatch(page); m != nil {
		return cleanIntro(stripTags(m[1]))
	}
	return ""
}

// cleanIntro 清掉简介里常见的噪声（页脚链接文案、广告）。
func cleanIntro(text string) string {
	text = strings.TrimSpace(text)
	for _, junk := range []string{
		"カリビアンコムを最適な環境で", "DXLIVEの無料チャット", "動画本編の視聴およびダウンロードには",
	} {
		if strings.HasPrefix(text, junk) {
			return ""
		}
	}
	if len([]rune(text)) < 30 {
		return "" // 太短的多半是标题或导航，不是简介
	}
	return text
}

// Enrich 实现 Enricher：从 jav321 详情页补字段。
//
// 页面上能拿到的：简介（正文那段）、出演者、メーカー（片商）、配信開始日（发行日期）、
// 封面（`pics.dmm.co.jp/digital/video/<slug>/<slug>pl.jpg`，**干净直连**，
// 不像 JAVDB 那张是异或混淆过的）。
func (s *Jav321) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	code := normalizeJav321Code(number)
	if code == "" {
		return FieldPatch{}, nil
	}
	if err := s.ensureSession(ctx); err != nil {
		return FieldPatch{}, err
	}
	searchURL := s.baseURL + "/search"
	body, err := s.c.postForm(ctx, searchURL, url.Values{"sn": {number}}, s.baseURL+"/", s.cookies)
	if err != nil && err != errHTTPNotFound {
		return FieldPatch{}, fmt.Errorf("jav321 搜索失败: %w", err)
	}
	slug, ok := pickJav321Hit(body, code)
	referer := searchURL
	if !ok {
		slug, ok = s.probeJav321Detail(ctx, code, searchURL)
		if !ok {
			return FieldPatch{}, nil
		}
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/video/"+slug, referer, "", s.cookies)
	if err != nil {
		if err == errHTTPNotFound {
			return FieldPatch{}, nil
		}
		return FieldPatch{}, fmt.Errorf("jav321 详情页抓取失败: %w", err)
	}
	patch := ParseJav321Detail(page)
	if patch.Summary != "" {
		patch.Filled = append(patch.Filled, "summary")
	}
	return patch, nil
}

// ParseJav321Detail 从详情页解析可补字段（纯函数）。
func ParseJav321Detail(page string) FieldPatch {
	var p FieldPatch
	p.Summary = ParseJav321Intro(page)
	// 信息块：`<b>出演者</b>: <a …>名</a>` / `<b>メーカー</b>: …` / `<b>品番</b>: …`
	for _, m := range jav321LabelRe.FindAllStringSubmatch(page, -1) {
		label := strings.TrimSpace(m[1])
		value := cleanFieldValue(stripTags(m[2]))
		switch label {
		case "出演者":
			if value != "" && !containsStr(p.Actors, value) {
				p.Actors = append(p.Actors, value)
			}
		case "メーカー", "メーカ":
			if p.Maker == "" {
				p.Maker = value
			}
		case "配信開始日", "発売日":
			if p.ReleaseDate == "" {
				p.ReleaseDate = normalizeDate(value)
			}
		}
	}
	// 封面：详情页里那张大图的地址（`pics.dmm.co.jp`，直连可取）
	if m := jav321CoverRe.FindStringSubmatch(page); m != nil {
		p.CoverURL = strings.TrimSpace(m[1])
	}
	return p
}

var (
	jav321LabelRe = regexp.MustCompile(`(?is)<b>\s*([^<]{2,10})\s*</b>\s*:?\s*(.{0,120}?)(?:<br|</div>|<b>)`)
	jav321CoverRe = regexp.MustCompile(`(?is)poster="(https?://[^"]*pics\.dmm\.co\.jp/[^"]+pl\.jpg)"`)
)

// cleanFieldValue 把「多个演员用 &nbsp; 隔开」这类取值收干净。
func cleanFieldValue(v string) string {
	v = strings.ReplaceAll(v, "\u00a0", " ")
	v = strings.Trim(v, " :：,，")
	return strings.Join(strings.Fields(v), " ")
}
