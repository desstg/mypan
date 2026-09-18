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

// 资源类型。
//
// 常量声明在这个子包而不是父包 tgsubscribe：抽取器都住在这里，子包无法 import 父包。
// 父包 resource.go 用别名转发，字符串值是两边的契约，改名要同时改。
//
// 每新增一种，配一个 Extractor；若要能投递，再配一个 Supports(kind) 返回 true 的 Deliverer。
const (
	ResourceKindMagnet     = "magnet"
	ResourceKindED2K       = "ed2k"
	ResourceKindShare115   = "share_115"
	ResourceKindShareQuark = "share_quark"
	ResourceKindHTTP       = "http"
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
// Kind 是扩展点：Registry 按注册顺序调用各抽取器，新增类型只需实现 Extractor。
type ResourceRef struct {
	Kind string
	Raw  string // 规范化后的链接（参数重新编码，保证可被网盘接受）
	// InfoHash 是**资源指纹**，按 Kind 加前缀，用作跨类型的去重键
	// （落在 DB 的 magnet_hash 列，见 store/migrations/0023 的两个唯一索引）。
	//
	//	magnet     40 位小写 hex（btih，v1 base32 也归一到这个形式）
	//	magnet v2  "btmh:..."（无法与 v1 互转，单独命名空间）
	//	ed2k       "ed2k:<32 位小写 hex>"（MD4）
	//	http 直链  "http:<sha1(规范化 URL) 40 位 hex>"
	//	115 分享    "115:<share code>"
	//	夸克分享    "quark:<share code>"
	//
	// 各类型的值域两两不相交，所以能共用一个唯一索引而不会互相撞键。
	//
	// ⚠️ magnet 的指纹**必须保持裸 hex，不能加 "magnet:" 前缀** —— 加了之后
	// 升级前的历史行与升级后新抽的值不再相等，去重索引直接失效，同一个磁力会被
	// 重复推送/重复下载。
	InfoHash    string
	DisplayName string // 链接自带的名字：magnet 的 dn= / ed2k 的 |file| 名，可能为空
	SizeBytes   int64  // magnet 的 xl= 不可信；ed2k 的 size 可信
	Source      string
}

// Extractor 从一条消息里抽出某几类资源。
//
// Kinds 是自描述（一个抽取器可以产出多种 Kind，例如分享链抽取器同时管 115 与夸克），
// 仅供测试与排查「哪些类型有实现」用 —— Registry 的派发只看 Extract。
type Extractor interface {
	Kinds() []string
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

func (MagnetExtractor) Kinds() []string { return []string{ResourceKindMagnet} }

func (MagnetExtractor) Extract(msg *Message) []ResourceRef {
	return forEachSource(msg, scanTextForMagnets)
}

// forEachSource 按固定顺序遍历一条消息里所有可能藏资源的来源，把每一段交给 pick。
//
// 顺序即优先级（见 Registry.Extract 的去重）：正文 → 配文 → 正文实体 → 配文实体 →
// 内联按钮。所有抽取器共用它，使「同一资源出现在多处时保留来源更可信的那个」
// 这条规则对所有资源类型都一致。
func forEachSource(msg *Message, pick func(text, source string) []ResourceRef) []ResourceRef {
	if msg == nil {
		return nil
	}
	out := make([]ResourceRef, 0, 4)
	out = append(out, pick(msg.Text, SourceText)...)
	out = append(out, pick(msg.Caption, SourceCaption)...)
	out = append(out, scanEntitySources(msg.Text, msg.Entities, pick)...)
	out = append(out, scanEntitySources(msg.Caption, msg.CaptionEntities, pick)...)
	out = append(out, scanButtonSources(msg.ReplyMarkup, pick)...)
	return out
}

// scanEntitySources 把实体区间与 text_link 的 url 交给 pick。
func scanEntitySources(text string, entities []MessageEntity, pick func(text, source string) []ResourceRef) []ResourceRef {
	if len(entities) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	for _, ent := range entities {
		// text_link 的链在 url 字段里，正文只显示一段自定义文案
		// —— 实测频道里「点击跳转」这类按钮式正文链接走的就是这条。
		if ent.Type == "text_link" && strings.TrimSpace(ent.URL) != "" {
			out = append(out, pick(ent.URL, SourceEntity)...)
		}
		// url / code / pre 这几种，链本身就在正文切片里。
		if s := entityText(text, ent); s != "" {
			out = append(out, pick(s, SourceEntity)...)
		}
	}
	return out
}

// scanButtonSources 把内联按钮的 url 与 copy_text 交给 pick。
func scanButtonSources(markup *InlineKeyboardMarkup, pick func(text, source string) []ResourceRef) []ResourceRef {
	if markup == nil {
		return nil
	}
	out := make([]ResourceRef, 0, 2)
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if strings.TrimSpace(btn.URL) != "" {
				out = append(out, pick(btn.URL, SourceButton)...)
			}
			if btn.CopyText != nil && strings.TrimSpace(btn.CopyText.Text) != "" {
				out = append(out, pick(btn.CopyText.Text, SourceCopyButton)...)
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
