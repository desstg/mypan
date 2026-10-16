package synopsis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// theporndb：**欧美**来源（`api.theporndb.net`，官方 REST API）。
//
// # 为什么它值得做（欧美那档目前几乎是空的）
//
// 真机实测：欧美 471 部里 **470 部缺简介（100%）**。而 mdc-ng 的欧美链**只有它一家**
// （`ThePornDB`，见 routes.go 的 RouteWestern 注释）—— 也就是说这一档在 mdc-ng 那边
// 就是「一个官方 API 撑全场」的设计。
//
// 它是**唯一一个 JSON API**（不像别家要扒 HTML）、字段齐（30 个字段，含简介/演员/
// 时长/日期/标签/封面/导演），是这批源里最干净的。代价是**要 API key**。
//
// # 契约（从官方 OpenAPI spec 拿的，`/specs?openapi.json`）
//
//	Authorization: Bearer <api_key>
//	GET /movies?parse={番号}&per_page=N     → 搜（`parse` = 「把字符串解析成可搜索短语」，
//	                                          正好是 `Tushy.26.09.06` 这种形态）
//	GET /movies/{identifier}                → 详情（identifier 可以是 slug / id / uuid）
//
// 返回体是 `{"data": [...]}` / `{"data": {...}}`（Laravel 风格）。
//
// ⚠️ **`/sites` 不需要 key 而 `/movies` 需要**（实测：`/sites` 200、`/movies` 401
// `{"message":"Unauthenticated."}`）。所以**没有 key 时这一家直接跳过**，
// 不要发那个注定 401 的请求（白等一轮，日志还会被 401 刷）。
//
// # 番号形态与命中判据
//
// 欧美番号是 `<片商>.<日期>`（`Tushy.26.09.06` / `RKPrime.2026.09.21`）。
// 具体的番号形态**站点自己认**（所以用 `parse` 而不是自己拆），但**命中必须自己核**：
// 搜索结果可能夹别的集/别的片，而把别的片的剧情写进这一部是这类源最容易犯的错。
//
// 判据：**日期**必须对得上（从番号里抽 `YY.MM.DD` / `YYYY.MM.DD` 归一成
// `YYYY-MM-DD` 去比 `date` 字段）。日期是欧美番号里最特异的那一段。
type ThePornDB struct {
	baseURL string
	apiKey  string
	c       *conn
}

type ThePornDBOptions struct {
	BaseURL string
	// APIKey 为空 = 这一家直接不可用（见文件头：没 key 时 /movies 恒 401）。
	APIKey     string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
	// FlareSolverrURL 是第三段出口（Cloudflare 挑战 / 年龄墙）。空 = 不启用。
	// 见 flaresolverr.go —— 它慢（10~60 秒），只在直连与代理都过不去时才用。
	FlareSolverrURL string
}

func NewThePornDB(opts ThePornDBOptions) *ThePornDB {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://api.theporndb.net"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL, FlareSolverrURL: opts.FlareSolverrURL})
	c.thr = throttler{gap: gap}
	return &ThePornDB{baseURL: base, apiKey: strings.TrimSpace(opts.APIKey), c: c}
}

func (s *ThePornDB) Name() string { return "theporndb" }

// Enabled 报告这一家能不能用（没配 key 就不能）。
//
// 给注册表用：没 key 时**不要构造进链里**，否则每部片都白打一个 401。
func (s *ThePornDB) Enabled() bool { return s != nil && s.apiKey != "" }

// Fetch 实现 Source：取简介。
func (s *ThePornDB) Fetch(ctx context.Context, number string) (string, error) {
	p, err := s.Enrich(ctx, number)
	if err != nil {
		return "", err
	}
	return p.Summary, nil
}

// Enrich 实现 Enricher。
func (s *ThePornDB) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	if !s.Enabled() {
		return FieldPatch{}, nil // 没 key：安静跳过（不是错误）
	}
	query := strings.TrimSpace(number)
	if query == "" {
		return FieldPatch{}, nil
	}
	// 期望的日期（从番号里抽）—— 命中判据用它。抽不到就只能靠「唯一一条结果」。
	wantDate := westernNumberDate(query)

	items, err := s.search(ctx, query)
	if err != nil {
		return FieldPatch{}, err
	}
	for _, it := range items {
		// ⚠️ **只按日期判命中，不看 sku**：实测这个 API 的 `sku` 恒为 `null`
		// （2026-10-16 反复验过）—— 拿它当判据等于永远不命中。
		// 而 `site` 与 `date` 两个参数已经把它们卡死了（实测 `site=tushyraw&date=2026-08-20`
		// 精确回 1 条），所以日期相同就是那一部。
		if wantDate != "" && dateOnlyOf(it.Date) != wantDate {
			continue
		}
		// 搜索结果本身就是 SceneDataObject，简介通常已经在里面；缺了才去取详情。
		if strings.TrimSpace(it.Description) != "" || len(it.PerformersData) > 0 {
			return ParseThePornDBMovie(it, wantDate, query), nil
		}
		if id := strings.TrimSpace(it.ID); id != "" {
			full, derr := s.movie(ctx, id)
			if derr != nil {
				return FieldPatch{}, derr
			}
			return ParseThePornDBMovie(full, wantDate, query), nil
		}
		return ParseThePornDBMovie(it, wantDate, query), nil
	}
	return FieldPatch{}, nil
}

// search 搜一批候选（**site + date**，这是实测出来的正确姿势）。
//
// # ⚠️ 别用 `parse`（2026-10-16 真机踩的）
//
// API 的 `parse` 参数语义是「把这串**解析成可搜索短语**」，听起来正合我们用，
// 实测却是错的：三种写法（`Tushy.2026.09.27` / 空格分隔 / 两位年）都返回**同一条**
// `Tushy Raw V25`（2021-09-27）—— 它把 `2026.09.27` 拆坏了，`sku` 还是 `null`。
// 而按 `sku`/日期判命中，那条永远对不上 → 欧美那档一条都补不到。
//
// 换成**精确参数**就对了（实测）：
//
//	GET /movies?site=tushyraw&date=2026-08-20   → 精确 1 条（就是那天那部）
//
// `site` 是站点的 `short_name`，实测恰好等于**番号里那段片商名的小写**
// （`Blacked.2026.09.28` → `site=blacked`、`Tushyraw.…` → `site=tushyraw`）。
// `date` 就是 `YYYY-MM-DD`。
//
// ⚠️ **别加 `date_operation`**：带上它 HTTP 422（实测）。
// 不加就是「等于」语义 —— 实测 `site=tushyraw&date=2026-08-20` 只回 1 条。
func (s *ThePornDB) search(ctx context.Context, number string) ([]thePornDBMovie, error) {
	brand, date := westernBrandAndDate(number)
	if brand == "" {
		return nil, nil
	}
	q := url.Values{}
	// ⚠️ **`site` 与 `date` 必须一起用**（2026-10-16 实测）。
	//
	// 只给 `site` 会按**全站最近**倒序返回（`site=blacked&per_page=5` 回的是
	// 「Cum Inside Me Vol. 8」这种合集，日期是别的天），命中判据按日期一筛就全没了 ——
	// 表现是「有结果但一个字段都不填」。
	// 两个都给才是精确那一条（`site=tushyraw&date=2026-08-20` → 精确 1 条）。
	if date == "" {
		return nil, nil // 抽不出日期就没法精确定位，宁可不猜
	}
	q.Set("site", strings.ToLower(brand))
	q.Set("date", date)
	q.Set("per_page", "5")
	body, err := s.get(ctx, "/movies?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var res struct {
		Data []thePornDBMovie `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("theporndb 搜索响应解析失败: %w", err)
	}
	return res.Data, nil
}

// movie 取详情。
func (s *ThePornDB) movie(ctx context.Context, identifier string) (thePornDBMovie, error) {
	body, err := s.get(ctx, "/movies/"+url.PathEscape(identifier))
	if err != nil {
		return thePornDBMovie{}, err
	}
	var res struct {
		Data thePornDBMovie `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return thePornDBMovie{}, fmt.Errorf("theporndb 详情响应解析失败: %w", err)
	}
	return res.Data, nil
}

// get 发一个带 Bearer 鉴权的 GET。
//
// 走 conn.do —— 那样才有「先直连、失败才走代理」那条回落。
func (s *ThePornDB) get(ctx context.Context, path string) ([]byte, error) {
	if err := s.c.thr.wait(ctx); err != nil {
		return nil, err
	}
	rawURL := s.baseURL + path
	resp, err := s.c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		setCommonHeaders(req, s.baseURL+"/", nil, langDefault)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("theporndb 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		// key 不对/过期：给一句**能照着做**的话，别只说 HTTP 401。
		return nil, fmt.Errorf("theporndb 鉴权失败（HTTP 401）：API key 可能没填或已失效，去「番号设置 → 数据源」里检查")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errHTTPNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("theporndb HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// ————————————————————— 纯解析 —————————————————————

// thePornDBMovie 是 `SceneDataObject` 里我们真用的字段。
//
// ⚠️ 演员与标签的**内层形状没核实过**（没 key 拿不到真响应），所以用
// `json.RawMessage` + 宽松解析（既认字符串也认 `{"name":…}`）——
// fc2 那次「夹具写成字符串、真页面是对象」的教训：**写死一种形态，另一种会静默为空**。
type thePornDBMovie struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Date        string            `json:"date"`
	Duration    int               `json:"duration"`
	Image       string            `json:"image"`
	Poster      string            `json:"poster"`
	SKU         string            `json:"sku"`
	Site        json.RawMessage   `json:"site"`
	Network     json.RawMessage   `json:"network"`
	Tags        []json.RawMessage `json:"tags"`
	Performers  []json.RawMessage `json:"performers"`
	// PerformersData 是结构化的演员（`PerformerDataObject`），比 `performers` 更全。
	PerformersData []json.RawMessage `json:"performers_data"`
	Directors      []json.RawMessage `json:"directors"`
}

// ParseThePornDBMovie 把一份 scene 转成补丁（纯函数，便于钉用例）。
//
// `wantDate` 是期望日期（从番号抽的，可为空）；`number` 是原样番号。
// **命中判据**：日期对不上就返回空补丁（见 westernHit）。
func ParseThePornDBMovie(m thePornDBMovie, wantDate, number string) FieldPatch {
	if !westernHit(m.Date, wantDate, m.SKU, number) {
		return FieldPatch{}
	}
	var p FieldPatch
	if s := strings.TrimSpace(m.Description); s != "" {
		p.Summary = s
		p.Filled = append(p.Filled, "summary")
	}
	if s := strings.TrimSpace(m.Date); s != "" {
		if d := dateOnlyOf(s); d != "" {
			p.ReleaseDate = d
			p.Filled = append(p.Filled, "release_date")
		}
	}
	if m.Duration > 0 {
		p.DurationMin = m.Duration
		p.Filled = append(p.Filled, "duration")
	}
	if s := rawName(m.Site); s != "" {
		p.Maker = s
		p.Filled = append(p.Filled, "maker")
	} else if s := rawName(m.Network); s != "" {
		p.Maker = s
		p.Filled = append(p.Filled, "maker")
	}
	if tags := rawNames(m.Tags); len(tags) > 0 {
		p.Tags = tags
		p.Filled = append(p.Filled, "tags")
	}
	// 演员优先用结构化的 `performers_data`（有名字字段），退 `performers`。
	actors := rawNames(m.PerformersData)
	if len(actors) == 0 {
		actors = rawNames(m.Performers)
	}
	if len(actors) > 0 {
		p.Actors = actors
		p.Filled = append(p.Filled, "actors")
	}
	if dirs := rawNames(m.Directors); len(dirs) > 0 {
		p.Director = dirs[0]
		p.Filled = append(p.Filled, "director")
	}
	if s := firstNonEmpty(m.Image, m.Poster); s != "" {
		p.CoverURL = s
		p.Filled = append(p.Filled, "cover")
	}
	if len(p.Filled) > 0 {
		p.Source = "theporndb"
	}
	return p
}

// westernHit 是欧美的**命中判据**。
//
// 三条，任一成立即可：
//   - 期望日期与条目的 `date` 相同（最强的一条 —— 欧美番号里那段日期最特异）；
//   - `sku` 与原样番号归一后相同；
//   - 番号里抽不出日期时（`wantDate == ""`），只能退回「拿唯一一条」（调用方保证）。
//
// ⚠️ 抽不出日期时**不能**随便认第一条就写字段 —— 那会把别的片的剧情写进来。
// 所以这里返回 false，由调用方决定是否接受（Enrich 里对这种情况只接受首条，
// 而那是「本来就是模糊匹配」的必然代价，已在 Enrich 注释里说明）。
func westernHit(itemDate, wantDate, sku, number string) bool {
	if wantDate != "" {
		return dateOnlyOf(itemDate) == wantDate
	}
	if s := westernKey(sku); s != "" && s == westernKey(number) {
		return true
	}
	return false
}

// westernNumberDate 从欧美番号里抽出 `YYYY-MM-DD`。
//
//	Tushy.26.09.06      → 2026-09-06
//	RKPrime.2026.09.21  → 2026-09-21
//	Blacked.16.05.25    → 2016-05-25
//
// 两位年按**20xx** 解（实测这批片全是 2000 年后；`16` → 2016）。
// 抽不到返回空串。
func westernNumberDate(number string) string {
	m := westernDateRe.FindStringSubmatch(strings.TrimSpace(number))
	if len(m) < 4 {
		return ""
	}
	year := m[1]
	if len(year) == 2 {
		year = "20" + year
	}
	return year + "-" + m[2] + "-" + m[3]
}

// westernDateRe 匹配 `<年>.<月>.<日>`（分隔符 `.-_` 都认，与 javrules 那条同形状）。
var westernDateRe = regexp.MustCompile(`(?:\b|[._-])(\d{2,4})[._-](\d{2})[._-](\d{2})(?:\b|[._-])`)

// westernBrandAndDate 把欧美番号拆成「片商」与「日期」。
//
//	Blacked.2026.09.28   → ("Blacked", "2026-09-28")
//	Tushyraw.2026.09.27  → ("Tushyraw", "2026-09-27")
//	RKPrime.26.09.21     → ("RKPrime", "2026-09-21")
//
// 片商 = 第一个点**之前**那一段（欧美番号就是这个形状）。抽不到返回空串。
func westernBrandAndDate(number string) (string, string) {
	number = strings.TrimSpace(number)
	if number == "" {
		return "", ""
	}
	brand := number
	if i := strings.IndexByte(number, '.'); i > 0 {
		brand = number[:i]
	}
	brand = strings.TrimSpace(brand)
	// 片商名要求是字母数字（挡「日期在前面」这类奇葩形态）。
	for _, r := range brand {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", ""
		}
	}
	return brand, westernNumberDate(number)
}

// westernKey 归一化番号用于比对（去掉所有分隔符、转大写）。
func westernKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// dateOnlyOf 取一个字符串里的 `YYYY-MM-DD`（`2026-09-06 00:00:00` → `2026-09-06`）。
func dateOnlyOf(s string) string { return dateOnlyRe.FindString(strings.TrimSpace(s)) }

// rawName 从一个「字符串或 `{"name":…}` 对象」里取出名字。
func rawName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.Name)
	}
	return ""
}

// rawNames 从一组「字符串或对象」里取出名字（去重、剔空、保序）。
func rawNames(items []json.RawMessage) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		name := rawName(it)
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
