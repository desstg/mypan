package telegram

import (
	"encoding/base32"
	"encoding/hex"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
)

// 资源类型。本期只产出 magnet；其它类型是后续迭代的扩展口子。
const (
	ResourceKindMagnet = "magnet"
)

// 资源来源，用于排查「为什么这条没抽到」。
const (
	SourceText        = "text"
	SourceCaption     = "caption"
	SourceEntity      = "entity"
	SourceButton      = "button"
	SourceCopyButton  = "copy_button"
	SourceTorrentFile = "torrent_file"
)

// ResourceRef 是从一条频道消息里抽出的可下载资源。
//
// Kind 是本功能的核心扩展点：本期 Registry 只注册 magnet 抽取器，
// 将来接 115/夸克分享转存时新增 Kind 与对应 Extractor 即可，下游不用改。
type ResourceRef struct {
	Kind        string
	Raw         string // 规范化后的磁力链接（参数重新编码，保证可被网盘接受）
	InfoHash    string // 统一成 40 位小写 hex；v2 单独用 "btmh:" 前缀
	DisplayName string // magnet 的 dn= 参数，可能为空
	SizeBytes   int64  // magnet 的 xl= 参数，不可信，仅作参考
	Source      string
}

// Extractor 从一条消息里抽出某一类资源。
type Extractor interface {
	Kind() string
	Extract(msg *Message) []ResourceRef
}

// Registry 按注册顺序调用各抽取器，并按 info hash 去重。
//
// 顺序即优先级：同一资源被多处引用时，保留先出现的那个（Source 更可信）。
type Registry struct {
	extractors []Extractor
	seen       map[string]struct{}
}

func NewRegistry(extractors ...Extractor) *Registry {
	return &Registry{extractors: extractors, seen: make(map[string]struct{}, 2)}
}

// Extract 返回消息里的全部资源，已按 info hash 去重。
func (r *Registry) Extract(msg *Message) []ResourceRef {
	if r == nil || msg == nil {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	for _, ex := range r.extractors {
		if ex == nil {
			continue
		}
		for _, ref := range ex.Extract(msg) {
			if ref.InfoHash == "" {
				continue
			}
			if _, dup := r.seen[ref.InfoHash]; dup {
				continue
			}
			r.seen[ref.InfoHash] = struct{}{}
			out = append(out, ref)
		}
	}
	// 清掉去重表，让 Registry 可复用（调用方每次处理一条消息，串行调用）。
	clear(r.seen)
	return out
}

// MagnetExtractor 从 magnet 链接里抽资源。
//
// 磁力链可能出现在四个地方，缺一个就会漏掉一批资源：
//  1. text / caption 正文
//  2. entities / caption_entities（text_link 的 url、url、以及放在代码块里的链）
//  3. 内联按钮的 url
//  4. 内联按钮的 copy_text（Bot API 7.11+，很多资源频道用它让用户「点击复制磁力」）
type MagnetExtractor struct{}

func (MagnetExtractor) Kind() string { return ResourceKindMagnet }

func (e MagnetExtractor) Extract(msg *Message) []ResourceRef {
	if msg == nil {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	out = append(out, scanTextForMagnets(msg.Text, SourceText)...)
	out = append(out, scanTextForMagnets(msg.Caption, SourceCaption)...)
	out = append(out, scanEntitiesForMagnets(msg.Text, msg.Entities)...)
	out = append(out, scanEntitiesForMagnets(msg.Caption, msg.CaptionEntities)...)
	out = append(out, scanButtonsForMagnets(msg.ReplyMarkup)...)
	return out
}

func scanEntitiesForMagnets(text string, entities []MessageEntity) []ResourceRef {
	if len(entities) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	for _, ent := range entities {
		// text_link 的磁链在 url 字段里，正文只显示一段自定义文案。
		if ent.Type == "text_link" && strings.TrimSpace(ent.URL) != "" {
			out = append(out, scanTextForMagnets(ent.URL, SourceEntity)...)
		}
		// url / code / pre 这几种，磁链本身就在正文切片里。
		if s := entityText(text, ent); s != "" {
			out = append(out, scanTextForMagnets(s, SourceEntity)...)
		}
	}
	return out
}

func scanButtonsForMagnets(markup *InlineKeyboardMarkup) []ResourceRef {
	if markup == nil {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if strings.TrimSpace(btn.URL) != "" {
				out = append(out, scanTextForMagnets(btn.URL, SourceButton)...)
			}
			if btn.CopyText != nil && strings.TrimSpace(btn.CopyText.Text) != "" {
				out = append(out, scanTextForMagnets(btn.CopyText.Text, SourceCopyButton)...)
			}
		}
	}
	return out
}

// entityText 按实体区间取正文切片。
//
// ⚠️ Bot API 的 offset/length 单位是 UTF-16 码元，不是 byte 也不是 rune。
// 含中文或 emoji 的正文里按 rune 切会错位甚至越界 —— 必须走 UTF-16。
func entityText(text string, ent MessageEntity) string {
	if text == "" || ent.Length <= 0 || ent.Offset < 0 {
		return ""
	}
	units := utf16.Encode([]rune(text))
	if ent.Offset >= len(units) || ent.Offset+ent.Length > len(units) {
		return ""
	}
	return string(utf16.Decode(units[ent.Offset : ent.Offset+ent.Length]))
}

// scanTextForMagnets 扫描一段文本里的全部磁力链。
func scanTextForMagnets(text, source string) []ResourceRef {
	candidates := collectMagnetCandidates(text)
	if len(candidates) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, len(candidates))
	for _, cand := range candidates {
		if ref, ok := normalizeMagnet(cand, source); ok {
			out = append(out, ref)
		}
	}
	return out
}

// collectMagnetCandidates 从一段文本里收集所有磁力链候选串。
//
// 同一行可能有多个磁链，所以按出现位置切分；跨行断开的链会尝试与下一行拼接一次。
// 每个出现位置都会产出一个「严格」候选（到空白/引号/全角标点为止）和一个「宽松」候选
// （只到引号/括号/全角标点为止）—— 前者干净，后者能救回 dn= 里带未编码空格、
// 导致 xt= 被甩在空格后面的链。normalizeMagnet 负责从中挑出能解出 info hash 的那个。
func collectMagnetCandidates(text string) []string {
	if text == "" {
		return nil
	}
	text = html.UnescapeString(text)
	lines := strings.Split(text, "\n")

	out := make([]string, 0, 2)
	for li := 0; li < len(lines); li++ {
		line := lines[li]
		starts := magnetStarts(line)
		for i, start := range starts {
			end := len(line)
			if i+1 < len(starts) {
				end = starts[i+1]
			}
			segment := line[start:end]

			for _, cand := range []string{cutStrict(segment), cutLoose(segment)} {
				if cand == "" {
					continue
				}
				// 两种情况下与下一行拼接：
				//   · 这一行还没解出 xt —— 链被硬折行了；
				//   · 下一行以 & 开头 —— 参数被折到了下一行。
				// 其它情况不拼，避免吞掉下一行不相干的正文。
				if li+1 < len(lines) {
					next := strings.TrimSpace(lines[li+1])
					if next != "" && (strings.HasPrefix(next, "&") || !hasInfoHashParam(cand)) {
						if joined := cand + next; hasInfoHashParam(joined) {
							cand = joined
						}
					}
				}
				out = append(out, cand)
			}
		}
	}
	return out
}

// magnetStarts 返回一行里所有 "magnet:" 的起始下标（大小写不敏感）。
func magnetStarts(line string) []int {
	lower := strings.ToLower(line)
	out := make([]int, 0, 1)
	for from := 0; from < len(lower); {
		idx := strings.Index(lower[from:], "magnet:")
		if idx < 0 {
			break
		}
		abs := from + idx
		// 只认 "magnet:?" 开头的，避免命中 "magnet:" 这种无参数写法。
		if strings.HasPrefix(lower[abs:], "magnet:?") {
			out = append(out, abs)
		}
		from = abs + len("magnet:")
	}
	return out
}

// magnetTailStops 是硬停止字符：引号、尖括号、各类括号。出现在这些字符之后的
// 内容一定不属于磁力链。
func cutAtStopChars(s string, stopAtSpace bool) string {
	end := len(s)
	for i, r := range s {
		if stopAtSpace && (r == ' ' || r == '\t' || r == '\r') {
			end = i
			break
		}
		if isMagnetStopChar(r) {
			end = i
			break
		}
	}
	return strings.TrimSpace(trimTrailingPunct(s[:end]))
}

// cutStrict 到空白或硬停止字符为止。
func cutStrict(s string) string { return cutAtStopChars(s, true) }

// cutLoose 只在硬停止字符处截断（允许值里出现未编码的空格）。
func cutLoose(s string) string { return cutAtStopChars(s, false) }

func isMagnetStopChar(r rune) bool {
	switch r {
	case '"', '\'', '<', '>', '“', '”', '‘', '’',
		'(', ')', '（', '）', '[', ']', '【', '】',
		'《', '》', '〔', '〕', '「', '」', '『', '』':
		return true
	// 中文标点。频道里「下载地址 magnet:?...，提取码 1234」这种写法极常见，
	// 不在这里截断的话，"提取码" 会被拼进 xt= 的值里，整条链就解析不出来了。
	case '，', '。', '；', '：', '！', '？', '、', '…', '·':
		return true
	}
	return false
}

func trimTrailingPunct(s string) string {
	return strings.TrimRight(s, ".,;:!?，。；：！？、·…")
}

// hasInfoHashParam 判断候选串里是否带可识别的 xt=。
func hasInfoHashParam(s string) bool {
	params, _ := parseMagnetParams(s)
	for _, xt := range params["xt"] {
		if _, ok := parseBTIH(xt); ok {
			return true
		}
	}
	return false
}

// normalizeMagnet 把一条候选串规范成 ResourceRef。
//
// 输出是**重建**过的磁力链：参数重新做过 URL 编码。这样做是因为频道里
// dn= 带未编码空格、结尾粘着中文标点的情况非常普遍，原样透传给网盘离线下载
// 接口会被拒。重建后只保留能正确解析的参数，保证链一定可用。
func normalizeMagnet(candidate, source string) (ResourceRef, bool) {
	candidate = strings.TrimSpace(candidate)
	if len(candidate) < len("magnet:?xt=") {
		return ResourceRef{}, false
	}
	params, order := parseMagnetParams(candidate)

	var hash string
	var xtRaw string
	for _, xt := range params["xt"] {
		if h, ok := parseBTIH(xt); ok {
			hash, xtRaw = h, xt
			break
		}
	}
	if hash == "" {
		return ResourceRef{}, false
	}

	ref := ResourceRef{
		Kind:     ResourceKindMagnet,
		InfoHash: hash,
		Source:   source,
	}
	if dn := firstNonEmpty(params["dn"]); dn != "" {
		ref.DisplayName = strings.TrimSpace(dn)
	}
	if xl := firstNonEmpty(params["xl"]); xl != "" {
		ref.SizeBytes = parseSizeParam(xl)
	}
	// 用规范化后的 xt 重建，其余参数原样保留（tr / ws 等对下载有实际意义）。
	params["xt"] = []string{xtRaw}
	ref.Raw = buildMagnet(params, order)
	return ref, true
}

// parseMagnetParams 解析 "magnet:?..." 的查询参数。
//
// 与 url.Query() 的区别：没有 '=' 的分段会被当成上一个值的续写。
// 发布者常把 dn=My Movie 2021 里的空格原样写出来，直接按 & 切会把
// "Movie" 当成一个独立分段丢掉，这里把它拼回去。
func parseMagnetParams(candidate string) (map[string][]string, []string) {
	q := candidate
	if idx := strings.IndexByte(q, '?'); idx >= 0 {
		q = q[idx+1:]
	}
	// 已经在 cutAtStopChars 里处理过硬停止字符，这里只需去掉尾部标点。
	q = strings.TrimSpace(trimTrailingPunct(q))

	params := make(map[string][]string, 8)
	order := make([]string, 0, 8)
	for _, seg := range strings.Split(q, "&") {
		eq := strings.IndexByte(seg, '=')
		if eq <= 0 {
			// 续写分段：拼到上一个参数的值后面。
			if len(order) == 0 {
				continue
			}
			last := order[len(order)-1]
			vals := params[last]
			if len(vals) > 0 {
				vals[len(vals)-1] += " " + strings.TrimSpace(seg)
				params[last] = vals
			}
			continue
		}
		key := strings.ToLower(strings.TrimSpace(seg[:eq]))
		if key == "" {
			continue
		}
		val := decodeParamValue(seg[eq+1:])
		if _, seen := params[key]; !seen {
			order = append(order, key)
		}
		params[key] = append(params[key], val)
	}
	return params, order
}

// decodeParamValue 做一次宽松的 URL 解码。失败就原样返回 —— 频道里的链
// 编码质量参差，不能因为一个 % 就整条丢掉。
func decodeParamValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if decoded, err := url.QueryUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func buildMagnet(params map[string][]string, order []string) string {
	var b strings.Builder
	b.WriteString("magnet:?")
	first := true
	for _, key := range order {
		for _, val := range params[key] {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(url.QueryEscape(key))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(val))
		}
	}
	return b.String()
}

// parseBTIH 把 xt 归一成统一的 info hash 表示。
//
// v1 的 btih 有 hex(40) 和 base32(32) 两种写法，不统一的话同一个种子
// 会被当成两个不同资源，去重直接失效。v2 的 btmh 无法与 v1 互转，单独命名空间。
func parseBTIH(xt string) (string, bool) {
	xt = strings.TrimSpace(xt)
	if xt == "" {
		return "", false
	}
	lower := strings.ToLower(xt)
	switch {
	case strings.HasPrefix(lower, "urn:btih:"):
		raw := strings.TrimSpace(xt[len("urn:btih:"):])
		switch len(raw) {
		case 40:
			if !isHexString(raw) {
				return "", false
			}
			return strings.ToLower(raw), true
		case 32:
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(raw))
			if err != nil || len(decoded) != 20 {
				return "", false
			}
			return hex.EncodeToString(decoded), true
		}
		return "", false
	case strings.HasPrefix(lower, "urn:btmh:"):
		raw := strings.ToLower(strings.TrimSpace(xt[len("urn:btmh:"):]))
		if raw == "" {
			return "", false
		}
		return "btmh:" + raw, true
	}
	return "", false
}

func isHexString(s string) bool {
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return len(s) > 0
}

// parseSizeParam 解析 xl= 参数。频道里乱填的很多，所以只做尽力而为的解析，
// 解析不出来就返回 0（调用方不拿它做硬过滤）。
func parseSizeParam(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
		return v
	}
	// 也见 "1.5 GB" 这类写法。
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return 0
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || val <= 0 {
		return 0
	}
	mult := float64(1)
	switch strings.ToUpper(fields[1]) {
	case "KB", "KIB":
		mult = 1 << 10
	case "MB", "MIB":
		mult = 1 << 20
	case "GB", "GIB":
		mult = 1 << 30
	case "TB", "TIB":
		mult = 1 << 40
	default:
		return 0
	}
	return int64(val * mult)
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
