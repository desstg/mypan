package synopsis

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// caribbeancom：无码 / 素人那批的简介来源。
//
// 片站自己的页面，简介最权威。形如：
//
//	GET https://www.caribbeancom.com/moviepages/092526-001/index.html
//
// ⚠️ **页面是 euc-jp 编码**（`<meta charset="euc-jp">`）。当 UTF-8 读不会报错，
// 只会得到一堆乱码 —— 那种错最难查，所以转码这一步有专门的用例。
//
// 简介的位置：正文里那个够长的 `<p>`（实测是第三段，前后是「DXLIVE 广告」与
// 「需要登录」这两段固定文案）。取「最长的那段且不像固定文案」最稳。
type Caribbeancom struct {
	baseURL string
	c       *conn
}

type CaribbeancomOptions struct {
	BaseURL    string
	Timeout    time.Duration
	RequestGap time.Duration
	// ProxyURL 是兜底代理：直连不通时才走它（见 conn）。空串 = 只直连。
	ProxyURL string
}

// NewCaribbeancom 构造 caribbeancom 来源。
func NewCaribbeancom(opts CaribbeancomOptions) *Caribbeancom {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://www.caribbeancom.com"
	}
	gap := opts.RequestGap
	if gap == 0 {
		gap = defaultGap
	}
	c := newConn(httpOptions{Timeout: opts.Timeout, ProxyURL: opts.ProxyURL})
	c.thr = throttler{gap: gap}
	return &Caribbeancom{baseURL: base, c: c}
}

func (s *Caribbeancom) Name() string { return "caribbeancom" }

// Fetch 按番号取简介。番号形如 `092526-001`（**原样带连字符**）。
func (s *Caribbeancom) Fetch(ctx context.Context, number string) (string, error) {
	id := normalizeCaribID(number)
	if id == "" {
		return "", nil
	}
	pageURL := fmt.Sprintf("%s/moviepages/%s/index.html", s.baseURL, id)
	page, err := s.c.fetch(ctx, pageURL, s.baseURL+"/", "euc-jp", nil)
	if err != nil {
		if err == errHTTPNotFound {
			return "", nil
		}
		return "", fmt.Errorf("caribbeancom 抓取失败: %w", err)
	}
	return ParseCaribbeancomIntro(page), nil
}

// normalizeCaribID 把番号归成 caribbeancom 的目录名。
//
//	092526-001  → 092526-001   （原样）
//	092526_001  → 092526-001   （下划线换成连字符）
//	SSIS-001    → ""           （不是这一档的番号，直接放弃，别去打无关的 URL）
func normalizeCaribID(number string) string {
	id := strings.TrimSpace(strings.ReplaceAll(number, "_", "-"))
	if !caribIDRe.MatchString(id) {
		return ""
	}
	return id
}

const (
	// maxIntroRunes 是能接受的简介上限。
	//
	// 取「最长的那段」这条在极少数页面上会失手（整页正文被包在一个 <p> 里），
	// 而一段 1500 字的"简介"塞进 nfo 的 <plot> 只会把播放器的详情页撑爆。
	// 超过这个长度就**不要**了：宁可空着，也不要一段明显不对的东西。
	maxIntroRunes = 800
)

// caribIDRe 是日期序号型：6 位日期 + 连字符 + 2~3 位序号。
var caribIDRe = regexp.MustCompile(`^\d{6}-\d{2,4}$`)

var (
	caribPRe = regexp.MustCompile(`(?is)<p[^>]*>(.{20,900}?)</p>`)
	// 固定文案（广告 / 提示），不是简介。
	caribJunkRe = regexp.MustCompile(`DXLIVE|ご利用いただくため|ログイン|会員登録|お済みでない方|JavaScript`)
)

// ParseCaribbeancomIntro 从详情页 HTML 里取简介（纯函数）。
//
// 取「最长的那一段且不像固定文案」—— 页面上的段落排序不稳定（广告位会变），
// 按位置取迟早踩空。
func ParseCaribbeancomIntro(page string) string {
	best := ""
	for _, m := range caribPRe.FindAllStringSubmatch(page, -1) {
		text := cleanIntro(stripTags(m[1]))
		if text == "" || caribJunkRe.MatchString(text) {
			continue
		}
		if len([]rune(text)) > maxIntroRunes {
			continue
		}
		if len([]rune(text)) > len([]rune(best)) {
			best = text
		}
	}
	return best
}

// Enrich 实现 Enricher：caribbeancom 能给的只有简介（片站页面没有片商/类别那套）。
func (s *Caribbeancom) Enrich(ctx context.Context, number string) (FieldPatch, error) {
	text, err := s.Fetch(ctx, number)
	if err != nil {
		return FieldPatch{}, err
	}
	if text == "" {
		return FieldPatch{}, nil
	}
	return FieldPatch{Summary: text, Filled: []string{"summary"}}, nil
}
