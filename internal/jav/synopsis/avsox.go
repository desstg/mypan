package synopsis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// avsox：**无码**来源（avsox.click，avmoo 同族的 Java/JavBus 系模板站）。
//
// # 为什么值得单独写它（这是目前字段最全的一家）
//
// 它有一个**JSON 接口**，一个番号能拿回来的东西比 JAVDB 还全（2026-10-15 实测）：
//
//	description_ja   完整日文剧情（+ description_cn / _tw 中文版）
//	title_ja/cn/tw   三种语言的标题
//	length           时长（分钟）
//	star[]           演员，**带名字与头像**
//	genre[]          标签，带中文名（genreName_cn）
//	series / studio  系列 / 片商
//	sampleLarge[]    剧照
//	posterLarge      封面
//	releaseDate      发行日期
//
// 用户库的真机缺口：无码那一档 2323 部里 **2148 部缺简介（92%）、100% 缺导演**。
// 而这一家能给的正好是「简介 + 演员 + 标签 + 时长 + 系列 + 片商 + 剧照」。
//
// # 入口（**两个 JSON 接口，都要 CSRF**）
//
//	GET  /cn                                  → 页面 meta 里的 csrf-token
//	POST /javu/data/api/search   [ {search,lang}, 60, 1 ]  → [{movieId, movieFanHao, …}]
//	POST /javu/data/api/getMovie [ movieId, "cn" ]         → 上面那一整份
//
// ⚠️ **不带 `x-csrf-token` 的话搜索恒返回 `data:[]`**（2026-10-15 实测：
// 同一个号带 token 有结果、不带就空）。它会**静默**表现成「这站没有这一部」——
// 所以 token 是这家能不能用的前提，不是可选项。
//
// ⚠️ 它的**详情 URL 不能从番号推**（`/cn/movies/{随机码}`，如 `nxzyeap`），
// 所以必须走「搜索拿 movieId → 取详情」两步。
//
// # 它**只认无码号**
//
// 有码的 `SSIS-001` 搜出来是空 —— 那是**对的**（avsox 是无码站，不是漏）。
// 所以这一档的路由把有码号送进来时它会安静地返回空，交给后面的源。
type Avsox struct {
	baseURL string
	c       *conn
	// csrfMu 保护下面那个缓存的 CSRF token（`/cn` 页面里那个）。
	csrfMu    sync.Mutex
	csrfToken string
}

type AvsoxOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare 挑战 / 年龄墙）。空 = 不启用。
	// 见 flaresolverr.go —— 它慢（10~60 秒），只在直连与代理都过不去时才用。
	FlareSolverrURL string
}

func NewAvsox(opts AvsoxOptions) *Avsox {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://avsox.click"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL, FlareSolverrURL: opts.FlareSolverrURL})
	c.thr = throttler{gap: gap}
	return &Avsox{baseURL: base, c: c}
}

func (s *Avsox) Name() string { return "avsox" }

// Fetch 实现 Source：只取简介（日文原文优先，有中文就用中文）。
func (s *Avsox) Fetch(ctx context.Context, number string) (string, error) {
	p, err := s.Enrich(ctx, number)
	if err != nil {
		return "", err
	}
	return p.Summary, nil
}

// Enrich 实现 Enricher。
func (s *Avsox) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	key := avsoxKey(number)
	if key == "" {
		return FieldPatch{}, nil
	}
	// ⚠️ 搜索要传**原样番号**（站点认 `092926-001` 这个带连字符的形态），
	// 而比对用归一化后的 key。拿 key 去搜会让 `092926-001` 搜不到
	// （实测：传 `092926001` 恒空，传 `092926-001` 有结果）—— 2026-10-15 踩过。
	movieID, err := s.search(ctx, strings.TrimSpace(number), key)
	if err != nil {
		return FieldPatch{}, err
	}
	if movieID == "" {
		return FieldPatch{}, nil // 这站没有这一部（有码号在这里必然为空）
	}
	detail, err := s.getMovie(ctx, movieID)
	if err != nil {
		return FieldPatch{}, err
	}
	return ParseAvsoxMovie(detail, key), nil
}

// ————————————————————— 取数 —————————————————————

// csrf 取（并缓存）页面的 csrf token。
func (s *Avsox) csrf(ctx context.Context) (string, error) {
	s.csrfMu.Lock()
	defer s.csrfMu.Unlock()
	if s.csrfToken != "" {
		return s.csrfToken, nil
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/cn", s.baseURL+"/", "", nil)
	if err != nil {
		return "", fmt.Errorf("avsox 取 csrf token 失败: %w", err)
	}
	tok := avsoxCSRFRe.FindStringSubmatch(page)
	if len(tok) < 2 || strings.TrimSpace(tok[1]) == "" {
		return "", fmt.Errorf("avsox 页面里没有 csrf-token（站点结构可能变了）")
	}
	s.csrfToken = strings.TrimSpace(tok[1])
	return s.csrfToken, nil
}

var avsoxCSRFRe = regexp.MustCompile(`name="csrf-token"\s+content="([^"]+)"`)

// search 按番号搜，返回 movieId（找不到返回空串）。
//
// `query` 是**传给站点的原样番号**，`key` 是归一化后的比对键 —— 两者刻意分开：
// 站点只认它自己那套写法（`092926-001`），而我们库里 `_` / `-` 两种都有。
func (s *Avsox) search(ctx context.Context, query, key string) (string, error) {
	body, err := s.postJSON(ctx, "/javu/data/api/search",
		[]any{map[string]any{"search": query, "lang": "cn"}, 60, 1})
	if err != nil {
		return "", err
	}
	var res struct {
		Code int `json:"code"`
		Data []struct {
			MovieID     string `json:"movieId"`
			MovieFanHao string `json:"movieFanHao"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("avsox 搜索响应解析失败: %w", err)
	}
	// ⚠️ **命中判据**：搜索结果可能夹附近的号，必须挑 movieFanHao 对得上那个。
	for _, m := range res.Data {
		if avsoxKey(m.MovieFanHao) == key {
			return m.MovieID, nil
		}
	}
	return "", nil
}

// getMovie 取详情（原样返回那份 JSON）。
func (s *Avsox) getMovie(ctx context.Context, movieID string) (avsoxMovie, error) {
	body, err := s.postJSON(ctx, "/javu/data/api/getMovie", []any{movieID, "cn"})
	if err != nil {
		return avsoxMovie{}, err
	}
	var res struct {
		Code int        `json:"code"`
		Data avsoxMovie `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return avsoxMovie{}, fmt.Errorf("avsox 详情响应解析失败: %w", err)
	}
	return res.Data, nil
}

// postJSON 发一个带 CSRF 头的 JSON POST，返回响应体。
//
// 走 conn.do —— 那样才有「先直连、失败才走代理」那条回落（这批站直连几乎全不通）。
func (s *Avsox) postJSON(ctx context.Context, path string, payload any) ([]byte, error) {
	token, err := s.csrf(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := s.c.thr.wait(ctx); err != nil {
		return nil, err
	}
	url := s.baseURL + path
	resp, err := s.c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		setCommonHeaders(req, s.baseURL+"/cn", nil, langDefault)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		// ⚠️ 这两个头是**必须的**：少了 csrf 那个搜索恒返回空（见文件头）。
		req.Header.Set("X-CSRF-Token", token)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("avsox 接口请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// token 过期时清掉缓存，下一轮重取。
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		s.csrfMu.Lock()
		s.csrfToken = ""
		s.csrfMu.Unlock()
		return nil, fmt.Errorf("avsox 接口 HTTP %d（csrf token 可能过期）", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avsox 接口 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// ————————————————————— 纯解析 —————————————————————

// avsoxKey 归一化番号用于比对：只留字母数字、转大写。
//
//	092926-001 → 092926001
//	092926_001 → 092926001     ← 库里两种写法都有（站上只用 `-`）
//	HEYZO-3950 → HEYZO3950
func avsoxKey(number string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(number)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// avsoxMovie 是 getMovie 的 `data`（只声明我们真用的字段）。
type avsoxMovie struct {
	MovieFanHao string `json:"movieFanHao"`
	TitleJA     string `json:"title_ja"`
	TitleCN     string `json:"title_cn"`
	TitleTW     string `json:"title_tw"`
	DescJA      string `json:"description_ja"`
	DescCN      string `json:"description_cn"`
	DescTW      string `json:"description_tw"`
	ReleaseDate string `json:"releaseDate"`
	Length      int    `json:"length"`
	PosterLarge string `json:"posterLarge"`
	PosterSmall string `json:"posterSmall"`
	Studio      struct {
		NameJA string `json:"studioName_ja"`
		NameCN string `json:"studioName_cn"`
	} `json:"studio"`
	Series struct {
		NameJA string `json:"seriesName_ja"`
		NameCN string `json:"seriesName_cn"`
	} `json:"series"`
	Genre []struct {
		NameJA string `json:"genreName_ja"`
		NameCN string `json:"genreName_cn"`
	} `json:"genre"`
	Star []struct {
		NameJA string `json:"starName_ja"`
		NameCN string `json:"starName_cn"`
	} `json:"star"`
}

// ParseAvsoxMovie 把 getMovie 的 data 转成补丁（纯函数，便于钉用例）。
//
// `key` 是归一后的请求番号，用于**命中判据** —— 对不上返回空补丁。
func ParseAvsoxMovie(m avsoxMovie, key string) FieldPatch {
	if avsoxKey(m.MovieFanHao) != key {
		return FieldPatch{}
	}
	var p FieldPatch

	// 简介：**中文优先**（`description_cn`），没有就用日文原文。
	// 两者都是真剧情（实测那段日文是完整的剧情梗概），不是站点标语。
	if s := firstNonEmpty(m.DescCN, m.DescTW, m.DescJA); s != "" {
		p.Summary = s
		p.Filled = append(p.Filled, "summary")
	}
	// 中文标题：只认**真中文**那一列（`title_cn` / `title_tw`）。
	// 不拿 `title_ja` 顶替 —— 往 `title_zh` 里塞日文会让界面显示成「有中文标题」。
	if s := firstNonEmpty(m.TitleCN, m.TitleTW); s != "" {
		p.TitleZH = s
		p.Filled = append(p.Filled, "title_zh")
	}
	if s := strings.TrimSpace(m.ReleaseDate); s != "" {
		p.ReleaseDate = avsoxDate(s)
		if p.ReleaseDate != "" {
			p.Filled = append(p.Filled, "release_date")
		}
	}
	if m.Length > 0 {
		p.DurationMin = m.Length
		p.Filled = append(p.Filled, "duration")
	}
	if s := firstNonEmpty(m.Studio.NameCN, m.Studio.NameJA); s != "" {
		p.Maker = s
		p.Filled = append(p.Filled, "maker")
	}
	if tags := avsoxNames(m.Genre, func(g struct {
		NameJA string `json:"genreName_ja"`
		NameCN string `json:"genreName_cn"`
	}) (string, string) {
		return g.NameCN, g.NameJA
	}); len(tags) > 0 {
		p.Tags = tags
		p.Filled = append(p.Filled, "tags")
	}
	if actors := avsoxNames(m.Star, func(x struct {
		NameJA string `json:"starName_ja"`
		NameCN string `json:"starName_cn"`
	}) (string, string) {
		return x.NameCN, x.NameJA
	}); len(actors) > 0 {
		p.Actors = actors
		p.Filled = append(p.Filled, "actors")
	}
	if s := firstNonEmpty(m.PosterLarge, m.PosterSmall); s != "" {
		p.CoverURL = s
		p.Filled = append(p.Filled, "cover")
	}
	if len(p.Filled) > 0 {
		p.Source = "avsox"
	}
	return p
}

// avsoxNames 从一组「有 ja / cn 两个名字」的条目里取出名字（中文优先、去重、去空）。
//
// 用泛型 + 取值函数：genre 与 star 是**两个不同的匿名结构体**（字段名不同），
// 但「中文优先、去重、剔空」的判据是同一套。各写一遍迟早分家。
func avsoxNames[T any](items []T, names func(T) (cn, ja string)) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		cn, ja := names(it)
		name := firstNonEmpty(cn, ja)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// avsoxDate 把日期收敛成 `YYYY-MM-DD`（实测已经是这个形态，留一道保险）。
func avsoxDate(s string) string {
	s = strings.TrimSpace(s)
	if m := dateOnlyRe.FindString(s); m != "" {
		return m
	}
	return ""
}

// firstNonEmpty 返回第一个非空（去空白后）的字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
