package synopsis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// javbus：**没有简介**，但补得上别的字段。
//
// 实测它同一页（`/{番号}`）上有：識別碼 / 發行日期 / 長度 / 導演 / 製作商 / 發行商
// 六项信息表，外加类别（`/genre/xxx` 链接）与演员（`/star/xxx` 链接）。
// 磁链那条路已经在抓这一页了（`internal/jav/javbus`），但只解析了 gid/uc/img ——
// 信息表整块丢掉。这里把它捡回来。
//
// 放在 synopsis 包里而不是改 javbus 客户端：那个包是「取磁链」的专职叶子，
// 而这里要的是「补字段」。两件事的调用时机、失败容忍、记账方式都不同，
// 塞在一起只会让磁链那条路多背一份责任。
type Javbus struct {
	baseURL string
	c       *conn
}

type JavbusOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
}

func NewJavbus(opts JavbusOptions) *Javbus {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.javbus.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL})
	c.thr = throttler{gap: gap}
	return &Javbus{baseURL: base, c: c}
}

func (s *Javbus) Name() string { return "javbus" }

// Fetch 实现 Source，但 javbus 没有简介 —— 恒返回空。
//
// 保留它（而不是从 Source 列表里去掉）是为了让它在 Enrich 那条路上被用到；
// 如果把它也放进简介的查找链，只会白白多打一次请求。
func (s *Javbus) Fetch(context.Context, string) (string, error) { return "", nil }

// Detail 取一页并解析出能补的字段。
func (s *Javbus) Detail(ctx context.Context, number string) (JavbusDetail, error) {
	code := strings.TrimSpace(number)
	if code == "" {
		return JavbusDetail{}, nil
	}
	page, err := s.c.fetch(ctx, s.baseURL+"/"+url.PathEscape(code), s.baseURL+"/", "utf-8",
		[]*http.Cookie{{Name: "existmag", Value: "all"}})
	if err != nil {
		if err == errHTTPNotFound {
			return JavbusDetail{}, nil // 这站没有：无码/欧美/FC2 本来就常 404
		}
		return JavbusDetail{}, fmt.Errorf("javbus 抓取失败: %w", err)
	}
	return ParseJavbusDetail(page), nil
}

// JavbusDetail 是能从 javbus 补到的字段。**没有简介**（那是刻意的：它确实没有）。
type JavbusDetail struct {
	ReleaseDate string
	DurationMin int
	Director    string
	Maker       string
	Label       string
	Tags        []string
	Actors      []string
}

// Empty 报告这份详情什么都没取到。
func (d JavbusDetail) Empty() bool {
	return d.ReleaseDate == "" && d.DurationMin == 0 && d.Director == "" &&
		d.Maker == "" && len(d.Tags) == 0 && len(d.Actors) == 0
}

var (
	// ⚠️ Go 的 RE2 **不支持 lookahead / lookbehind**（`(?=…)`、`(?!…)` 会让 init 直接
	// panic），所以「取到下一个 header 之前」这种写法**不能**用正则表达。
	// 改成「按 header 切分」：Split 之后每段就是「某字段的值 + 后面的杂项」。
	javbusHeaderSplit = regexp.MustCompile(`(?i)<span class="header">`)
	javbusSplitLabel  = regexp.MustCompile(`^\s*([^<]{1,12})</span>`)
	javbusLinkRe      = regexp.MustCompile(`href="[^"]*/(genre|star|studio|label|series)/([^"/]+)"[^>]*>([^<]{1,24})</a>`)
	javbusNumRe       = regexp.MustCompile(`[^\d]`)
)

// ParseJavbusDetail 从详情页 HTML 里取可补的字段（纯函数）。
func ParseJavbusDetail(page string) JavbusDetail {
	var d JavbusDetail
	for _, seg := range javbusHeaderSplit.Split(page, -1)[1:] {
		lm := javbusSplitLabel.FindStringSubmatch(seg)
		if lm == nil {
			continue
		}
		label := strings.TrimSpace(lm[1])
		// 值到本段结束（= 下一个 header 之前）为止 —— Split 已经切好了，
		// 所以这里只把剩下的标签清掉、并按行取第一行。
		value := strings.TrimSpace(stripTags(seg[len(lm[0]):]))
		if i := strings.IndexByte(value, 10); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		switch label {
		case "發行日期:", "发行日期:":
			d.ReleaseDate = normalizeDate(value)
		case "長度:", "长度:":
			d.DurationMin = parseMinutes(value)
		case "導演:", "导演:":
			d.Director = value
		case "製作商:", "制作商:":
			d.Maker = value
		case "發行商:", "发行商:":
			d.Label = value
		}
	}
	// 类别与演员是链接，且只在「信息表之后」的那一段里 —— 页面上还有导航栏的
	// 「有碼類別 / 無碼類別」两个链接，直接全局抓会把它们也算成类别。
	if i := strings.Index(page, `<span class="header">`); i >= 0 {
		body := page[i:]
		for _, m := range javbusLinkRe.FindAllStringSubmatch(body, -1) {
			name := strings.TrimSpace(m[3])
			switch m[1] {
			case "genre":
				if name != "" && !containsStr(d.Tags, name) {
					d.Tags = append(d.Tags, name)
				}
			case "star":
				if name != "" && !containsStr(d.Actors, name) {
					d.Actors = append(d.Actors, name)
				}
			case "studio":
				if d.Maker == "" {
					d.Maker = name
				}
			case "label":
				if d.Label == "" {
					d.Label = name
				}
			}
		}
	}
	return d
}

// normalizeDate 把 `2026-09-25` / `2026/09/25` 统一成 `2026-09-25`。
func normalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 10 {
		return ""
	}
	v = strings.ReplaceAll(v[:10], "/", "-")
	if !dateRe.MatchString(v) {
		return ""
	}
	return v
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// parseMinutes 从 `120分鐘` / `120分` / `120` 里取分钟数。
func parseMinutes(v string) int {
	digits := javbusNumRe.ReplaceAllString(strings.SplitN(v, "分", 2)[0], "")
	if digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 || n > 24*60 {
		return 0
	}
	return n
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Enrich 实现 Enricher：javbus 补的是**别的字段**（它没有简介）。
//
// 它在链路上排最后：简介那两家都没有时轮到它也没用，而导演/时长/类别这些
// 前面两家根本不提供 —— 所以它每次都值得问一次。
func (s *Javbus) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	d, err := s.Detail(ctx, number)
	if err != nil {
		return FieldPatch{}, err
	}
	if d.Empty() {
		return FieldPatch{}, nil
	}
	p := FieldPatch{
		ReleaseDate: d.ReleaseDate,
		DurationMin: d.DurationMin,
		Director:    d.Director,
		Maker:       d.Maker,
		Tags:        d.Tags,
		Actors:      d.Actors,
	}
	if d.ReleaseDate != "" {
		p.Filled = append(p.Filled, "release_date")
	}
	if d.DurationMin > 0 {
		p.Filled = append(p.Filled, "duration")
	}
	if d.Director != "" {
		p.Filled = append(p.Filled, "director")
	}
	if d.Maker != "" {
		p.Filled = append(p.Filled, "maker")
	}
	if len(d.Tags) > 0 {
		p.Filled = append(p.Filled, "tags")
	}
	if len(d.Actors) > 0 {
		p.Filled = append(p.Filled, "actors")
	}
	return p, nil
}
