package tgsubscribe

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"litepan/internal/mediaorganize/rules"
)

// ReleaseName 是从发布名里解析出来的结构化信息。
//
// 解析复用 internal/mediaorganize/rules（与目录整理 / STRM 刮削同一套），
// 这里只补订阅场景需要的东西：片名候选、季集的真实性校验、整季包判定、画质字段归一化。
type ReleaseName struct {
	Raw string
	// TitleCandidates 是归一化后的片名候选，按可信度降序，已去重。
	// 中英混排的名字会额外拆出纯中文段和纯英文段 —— 两边都可能命中 TMDB 的别名。
	TitleCandidates []string
	Year            *int
	Season          *int
	Episode         *int
	// EpisodeEnd 非空表示这是集数区间（E01-E12）。
	EpisodeEnd   *int
	IsBatch      bool
	Resolution   string
	VideoCodec   string
	Source       string
	AudioCodec   string
	ReleaseGroup string
	// SizeBytes 来自磁力链的 xl= 参数。它由调用方（抽链阶段）填进来，
	// 发布名本身解不出体积。频道里乱填的情况很多，所以只用于体积门槛和同分取大，
	// 不作为硬过滤依据（用户没配 max_size_gb 时完全忽略）。
	SizeBytes int64
	// NameSource 说明这个发布名是从哪来的：dn 参数最可信，正文首行兜底的可信度低一档。
	// 由抽链阶段填，用于给打分微调。
	NameSource string
}

// ParseReleaseName 解析一条发布名。
func ParseReleaseName(raw string) ReleaseName {
	out := ReleaseName{Raw: strings.TrimSpace(raw)}
	if out.Raw == "" {
		return out
	}

	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(out.Raw))

	out.Year = parsed.Year
	out.Season, out.Episode = trustSeasonEpisode(out.Raw, parsed.Season, parsed.Episode)
	out.Resolution = strutilFirstNonEmpty(canonicalResolution(parsed.ScreenSize), resolutionFromRaw(out.Raw))
	out.VideoCodec = strutilFirstNonEmpty(canonicalVideoCodec(parsed.VideoCodec), codecFromRaw(out.Raw))
	out.Source = pickSource(canonicalSource(parsed.Source), sourceFromRaw(out.Raw))
	out.AudioCodec = strings.TrimSpace(parsed.AudioCodec)
	out.ReleaseGroup = strings.TrimSpace(parsed.ReleaseGroup)

	out.EpisodeEnd, out.IsBatch = detectBatch(out.Raw, out.Season, out.Episode)
	if out.Episode == nil {
		if ep := animeBracketEpisode(out.Raw); ep != nil {
			out.Episode = ep
			out.IsBatch = false
		}
	}
	out.TitleCandidates = buildTitleCandidates(strings.TrimSpace(parsed.Title), out.Raw)
	return out
}

// animeBracketEpisode 处理动漫的方括号集号写法：[SweetSub][葬送的芙莉莲][12][1080p]。
//
// 只在确认是动漫发布、且没有其它显式季集标记时才启用 —— 普通影视的方括号里
// 放的是发布组和画质标签，裸数字往里套会误判。
func animeBracketEpisode(raw string) *int {
	if !rules.IsAnime(raw) || explicitEpisodeRe.MatchString(raw) || explicitSeasonRe.MatchString(raw) {
		return nil
	}
	for _, segment := range bracketSegments(raw) {
		seg := strings.TrimSpace(segment)
		if seg == "" || len(seg) > 4 {
			continue
		}
		v, err := strconv.Atoi(seg)
		if err != nil || v <= 0 || v > 999 {
			continue
		}
		if isYearToken(seg) {
			continue
		}
		return &v
	}
	return nil
}

// LooksLikeRelease 判断这条消息是否值得进匹配流程。
//
// 频道里大量消息是公告、闲聊、频道推广 —— 不设门槛的话匹配历史会被刷屏。
// 判据：解得出片名，并且带年份或分辨率，有其一才像一条影视资源。
func (r ReleaseName) LooksLikeRelease() bool {
	if len(r.TitleCandidates) == 0 {
		return false
	}
	return r.Year != nil || r.Resolution != ""
}

// trustSeasonEpisode 只在原文里真的有显式季/集标记时才信任解析器的结果。
//
// 解析器会从 "DTS-HD.MA.5.1" 里猜出 S01E05、从 "John Wick 4" 里猜出 S01E04。
// 一旦带上集号：电影订阅会被「遇到 SxxEyy」的 -40 分误杀，剧集订阅会去查一个
// 不存在的集号从而被降级成 ambiguous。所以宁可把季集清空，也不能留假的。
func trustSeasonEpisode(raw string, season, episode *int) (*int, *int) {
	if season != nil && !explicitSeasonRe.MatchString(raw) && !cnSeasonRe.MatchString(raw) && !tvSeasonWordRe.MatchString(raw) {
		// 原文没有季标记，但可能有个「全 30 集」式的集数，保留季号没意义。
		season = nil
	}
	if episode != nil && !explicitEpisodeRe.MatchString(raw) && !cnEpisodeRe.MatchString(raw) {
		episode = nil
	}
	// 解析器常常只给出季或只给出集，另一侧从原文补齐。
	if season == nil {
		if m := explicitSeasonRe.FindStringSubmatch(raw); len(m) >= 2 {
			season = parseSmallInt(m[1])
		}
		if season == nil {
			if m := cnSeasonRe.FindStringSubmatch(raw); len(m) >= 2 {
				season = parseCNNumeral(m[1])
			}
		}
		if season == nil {
			if m := tvSeasonWordRe.FindStringSubmatch(raw); len(m) >= 1 {
				if v := trailingInt(m[0]); v != nil {
					season = v
				}
			}
		}
	}
	if episode != nil && season == nil {
		if m := explicitEpisodeRe.FindStringSubmatch(raw); len(m) >= 2 && m[1] != "" {
			season = parseSmallInt(m[1])
		}
	}
	if season != nil && (*season < 0 || *season > rules.MaxReasonableSeason) {
		season = nil
	}
	if episode != nil && (*episode <= 0 || *episode > 9999) {
		episode = nil
	}
	return season, episode
}

// trailingInt 取出字符串末尾的连续数字（"Season 2" → 2）。
func trailingInt(s string) *int {
	end := len(s)
	start := end
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	if start == end {
		return nil
	}
	return parseSmallInt(s[start:end])
}

var (
	// S01E01 / S01.E01 / E01 / EP01 —— 第 1 个捕获组是季号（可能为空）。
	explicitEpisodeRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:(s\d{1,2}))?[ ._-]*e(?:p)?[ ._-]*(\d{1,3})(?:[^0-9]|$)`)
	// S01 / Season 1 / SEASON.01
	explicitSeasonRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(?:eason)?[ ._-]*(\d{1,2})(?:[^0-9]|$)`)
	// 第1季 / 第一季 / 第2部
	cnSeasonRe = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百]+)\s*[季部]`)
	// 第01集 / 第3话
	cnEpisodeRe = regexp.MustCompile(`第\s*(\d{1,4})\s*[集话話]`)
	// 全30集 / 共12集 —— 整季包的常见写法
	cnTotalEpisodeRe = regexp.MustCompile(`[全共]\s*(\d{1,4})\s*[集话話]`)
	// Season 1 / Series 1
	tvSeasonWordRe = regexp.MustCompile(`(?i)\b(?:season|series|saison|staffel)[ ._-]*\d{1,2}\b`)
)

// 集数区间：S01E01-E12 / E01-12 / EP01~12 / 第01-12集。
var (
	episodeRangeRe   = regexp.MustCompile(`(?i)(?:s\d{1,2})?[ ._-]*e(?:p)?[ ._-]*(\d{1,3})\s*[-~～–—]\s*(?:e|ep)?[ ._-]*(\d{1,3})`)
	cnEpisodeRangeRe = regexp.MustCompile(`第\s*(\d{1,3})\s*[-~～至到]\s*(\d{1,3})\s*[集话話]`)
)

// batchKeywords 是整季包 / 合集的关键词。中文不能用 \b，所以做子串判断。
var batchKeywords = []string{
	"complete", "season pack", "batch", "全集", "合集", "全季", "全剧", "完结",
}

// detectBatch 判定这条资源是整季包还是单集。
//
// 整季包与单集混发是频道常态，两者在选优阶段权重完全不同（用户多数时候
// 只想要新出的那一集），所以必须在解析阶段就把这个事实标出来。
func detectBatch(raw string, season, episode *int) (*int, bool) {
	if m := episodeRangeRe.FindStringSubmatch(raw); len(m) == 3 {
		if end := parseSmallInt(m[2]); end != nil {
			return end, true
		}
	}
	if m := cnEpisodeRangeRe.FindStringSubmatch(raw); len(m) == 3 {
		if end := parseSmallInt(m[2]); end != nil {
			return end, true
		}
	}
	if m := cnTotalEpisodeRe.FindStringSubmatch(raw); len(m) == 2 {
		if end := parseSmallInt(m[1]); end != nil {
			return end, true
		}
	}
	// 有季号但没有集号 → 整季包。
	if season != nil && episode == nil {
		return nil, true
	}
	lower := strings.ToLower(raw)
	for _, kw := range batchKeywords {
		if strings.Contains(lower, kw) {
			return nil, true
		}
	}
	return nil, false
}

func parseSmallInt(s string) *int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 || v > 9999 {
		return nil
	}
	return &v
}

// parseCNNumeral 把 一/二/…/十/十二/二十/三十 转成数字。
func parseCNNumeral(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if v, err := strconv.Atoi(s); err == nil {
		if v <= 0 {
			return nil
		}
		return &v
	}
	digits := map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	total := 0
	cur := 0
	seen := false
	for _, r := range s {
		switch {
		case r == '十':
			if cur == 0 {
				cur = 1
			}
			total += cur * 10
			cur = 0
			seen = true
		case r == '百':
			if cur == 0 {
				cur = 1
			}
			total += cur * 100
			cur = 0
			seen = true
		default:
			d, ok := digits[r]
			if !ok {
				return nil
			}
			cur = d
			seen = true
		}
	}
	total += cur
	if !seen || total <= 0 || total > rules.MaxReasonableSeason {
		return nil
	}
	return &total
}

// ————————————————————— 片名候选 —————————————————————

// buildTitleCandidates 生成片名候选。
//
// 顺序即优先级：
//  1. 解析器给出的完整片名（最可信）
//  2. 中英混排时拆出的纯中文段 / 纯英文段
//  3. 方括号里的内容（动漫常见 [SweetSub][葬送的芙莉莲][12]）
//  4. 解析器给不出片名时的兜底（1917.2019.1080p... 这类）
//
// 长度 < 2 的、纯数字的、纯噪声词的候选一律丢掉 —— 它们只会制造误匹配。
func buildTitleCandidates(title, raw string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	add := func(candidate string, allowNumeric bool) {
		normalized := NormalizeName(stripTitleNoise(candidate))
		if !isUsableTitle(normalized, allowNumeric) {
			return
		}
		if _, dup := seen[normalized]; dup {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	add(title, false)

	han, ascii := splitByScript(title)
	if han != "" && ascii != "" {
		add(han, false)
		add(ascii, false)
	}

	for _, segment := range bracketSegments(raw) {
		if isNoiseBracketSegment(segment) {
			continue
		}
		add(segment, false)
	}

	if len(out) == 0 {
		// 兜底候选放宽「纯数字」限制 —— `1917` / `2012` 这类纯数字片名本来就是合法的，
		// 它们出现在年份 token 左侧，位置本身就是足够的证据。
		add(fallbackTitle(raw), true)
	}
	return out
}

// isUsableTitle 过滤掉不可能作为片名的候选。
//
// allowNumeric 用于兜底路径：`1917` 与 `2012` 是合法片名，不能因为「纯数字」被否掉，
// 但常规路径下必须拒掉 `2021`、`1080` 这类纯粹是年份/分辨率的候选。
func isUsableTitle(s string, allowNumeric bool) bool {
	if len([]rune(s)) < 2 {
		return false
	}
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter && !allowNumeric {
		return false
	}
	// 全是画质/编码/噪声词的候选也不是片名。
	informative := 0
	for _, tok := range strings.Fields(s) {
		if !isQualityToken(tok) {
			informative++
		}
	}
	return informative > 0
}

// isNoiseBracketSegment 判断方括号片段是不是发布组 / 画质标签这类噪声。
func isNoiseBracketSegment(segment string) bool {
	s := strings.TrimSpace(segment)
	if s == "" {
		return true
	}
	for _, suffix := range []string{"字幕组", "压制组", "发布组", "资源组", "字幕社"} {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	hasHan := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			break
		}
	}
	if hasHan {
		return false
	}
	// 纯 ASCII 片段：单 token 的（SweetSub、1080p）一律当发布组/标签；
	// 多 token 的只有在全是标签词时才丢。
	tokens := strings.Fields(NormalizeName(s))
	if len(tokens) <= 1 {
		return true
	}
	for _, tok := range tokens {
		if !isQualityToken(tok) {
			return false
		}
	}
	return true
}

// 片名里要剥掉的形态词。
var (
	titleSeasonNoiseRe = regexp.MustCompile(`第\s*[0-9一二三四五六七八九十百]+\s*[季部]`)
	titleBatchNoiseRe  = regexp.MustCompile(`(?:全集|合集|全季|全剧|完结|[全共]\s*\d{1,4}\s*[集话話])`)
)

// stripTitleNoise 从片名里去掉季号 / 全集这类形态词：`权力的游戏 第一季 全集` → `权力的游戏`。
func stripTitleNoise(s string) string {
	s = titleSeasonNoiseRe.ReplaceAllString(s, " ")
	s = titleBatchNoiseRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// fallbackTitle 在解析器给不出片名时，从发布名里兜一个出来。
//
// 典型场景是纯数字片名：1917.2019.1080p.BluRay.x264.DTS-HD.MA.5.1 和
// 2012.2009.1080p.BluRay.x264 —— 解析器会把开头的数字当成画质或季集吃掉。
//
// 做法：先剔除画质/编码/音轨/发布组 token，再取「最后一个 4 位年份 token」左边
// 的部分作为片名。这样 `1917 2019` 取到 `1917`，`2012 2009` 取到 `2012`。
func fallbackTitle(raw string) string {
	tokens := strings.Fields(stripKnownTagsForTitle(raw))

	lastYear := -1
	for i, tok := range tokens {
		if isYearToken(tok) {
			lastYear = i
		}
	}
	if lastYear > 0 {
		return strings.Join(tokens[:lastYear], " ")
	}
	if lastYear == 0 {
		// 整个名字就是「年份 + 画质...」，没有片名可提取。
		return ""
	}
	return strings.Join(tokens, " ")
}

// stripKnownTagsForTitle 把发布名拆成 token 并丢掉所有能识别的标签。
func stripKnownTagsForTitle(raw string) string {
	fields := strings.Fields(NormalizeName(stripReleaseSitePrefix(raw)))
	out := make([]string, 0, len(fields))
	for _, tok := range fields {
		if isQualityToken(tok) {
			continue
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

// stripReleaseSitePrefix 剥掉发布站前缀（hhd800.com@ / www.xxx.com-）。
func stripReleaseSitePrefix(raw string) string {
	s := strings.TrimSpace(raw)
	if at := strings.IndexByte(s, '@'); at > 0 && at < 64 && strings.Contains(s[:at], ".") {
		s = s[at+1:]
	}
	return strings.TrimSpace(s)
}

func isYearToken(tok string) bool {
	if len(tok) != 4 {
		return false
	}
	v, err := strconv.Atoi(tok)
	return err == nil && v >= 1900 && v <= 2099
}

// splitByScript 把中英混排的名字切成纯中文段与纯英文段。
//
// 紧跟在汉字后面的数字算中文名的一部分（沙丘2）—— 否则会被拆成 `沙丘` + `2`，
// 而 TMDB 上的中文标题恰恰是 `沙丘2`。
func splitByScript(s string) (han, ascii string) {
	const (
		clsOther = iota
		clsHan
		clsDigit
		clsLetter
	)
	classOf := func(r rune) int {
		switch {
		case unicode.Is(unicode.Han, r):
			return clsHan
		case r >= '0' && r <= '9':
			return clsDigit
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			return clsLetter
		}
		return clsOther
	}

	var hanParts, asciiParts []string
	var cur []rune
	curCls := clsOther

	flush := func() {
		if len(cur) > 0 {
			tok := strings.TrimSpace(string(cur))
			cur = cur[:0]
			if tok != "" {
				switch curCls {
				case clsHan, clsDigit:
					hanParts = append(hanParts, tok)
				case clsLetter:
					asciiParts = append(asciiParts, tok)
				}
			}
		}
		curCls = clsOther
	}

	for _, r := range s {
		cls := classOf(r)
		if cls == clsOther {
			flush()
			continue
		}
		// 数字紧跟在汉字后面时延续当前 run，不另起一段。
		if cls != curCls && !(cls == clsDigit && curCls == clsHan) {
			flush()
		}
		if curCls == clsOther {
			curCls = cls
		}
		cur = append(cur, r)
	}
	flush()
	return strings.Join(hanParts, " "), strings.Join(asciiParts, " ")
}

// bracketSegments 取方括号 / 圆括号里的片段。
func bracketSegments(raw string) []string {
	out := make([]string, 0, 2)
	for _, pair := range [][2]rune{{'[', ']'}, {'【', '】'}, {'(', ')'}, {'（', '）'}} {
		depth := 0
		start := -1
		for i, r := range raw {
			switch r {
			case pair[0]:
				if depth == 0 {
					start = i + len(string(r))
				}
				depth++
			case pair[1]:
				if depth > 0 {
					depth--
					if depth == 0 && start >= 0 && start <= i {
						if seg := strings.TrimSpace(raw[start:i]); seg != "" {
							out = append(out, seg)
						}
						start = -1
					}
				}
			}
		}
	}
	return out
}

// ————————————————————— 归一化与词表 —————————————————————

// noiseWords 是绝不可能成为片名的词。
var noiseWords = map[string]struct{}{
	"movie": {}, "tv": {}, "complete": {}, "batch": {}, "repack": {}, "proper": {},
	"internal": {}, "multi": {}, "chs": {}, "cht": {}, "gb": {}, "big5": {},
	"合集": {}, "全集": {}, "全季": {}, "全剧": {}, "完结": {},
	"电影": {}, "影片": {}, "剧集": {}, "高清": {}, "超清": {}, "蓝光": {},
	"更新": {}, "更新了": {}, "已更新": {}, "推荐": {}, "资源": {},
}

var (
	resolutionTokenRe = regexp.MustCompile(`^\d{3,4}[pi]$`)
	audioChannelRe    = regexp.MustCompile(`^\d\.\d(?:\.\d)?$`)
	hdrRe             = regexp.MustCompile(`^(?:hdr\d*\+?|sdr|dv|dolbyvision|dovi)$`)
)

var codecTokens = map[string]struct{}{
	"hevc": {}, "h265": {}, "h264": {}, "x265": {}, "x264": {}, "avc": {}, "av1": {},
	"vp9": {}, "vp8": {}, "mpeg2": {}, "xvid": {}, "divx": {}, "vc1": {},
}

var audioTokens = map[string]struct{}{
	"aac": {}, "ac3": {}, "eac3": {}, "ddp": {}, "dd": {}, "dts": {}, "dtshd": {},
	"dtsma": {}, "truehd": {}, "atmos": {}, "flac": {}, "opus": {}, "mp3": {},
	"lpcm": {}, "pcm": {}, "thd": {}, "ma": {}, "hra": {},
}

var sourceTokens = map[string]struct{}{
	"remux": {}, "bluray": {}, "blu": {}, "bdrip": {}, "brrip": {}, "bd": {},
	"webdl": {}, "webrip": {}, "web": {}, "hdtv": {}, "pdtv": {}, "dvdrip": {},
	"dvd": {}, "cam": {}, "ts": {}, "tc": {}, "scr": {}, "r5": {}, "uhd": {},
	"4k": {}, "8k": {}, "hd": {}, "sd": {}, "fhd": {}, "qhd": {}, "dl": {},
}

// isQualityToken 判断一个 token 是不是画质 / 编码 / 音轨 / 片源标签。
func isQualityToken(tok string) bool {
	s := strings.ToLower(strings.TrimSpace(tok))
	if s == "" {
		return true
	}
	if _, ok := noiseWords[s]; ok {
		return true
	}
	if resolutionTokenRe.MatchString(s) || audioChannelRe.MatchString(s) || hdrRe.MatchString(s) {
		return true
	}
	for _, set := range []map[string]struct{}{codecTokens, audioTokens, sourceTokens} {
		if _, ok := set[s]; ok {
			return true
		}
	}
	// 10bit / 8bit
	if base, found := strings.CutSuffix(s, "bit"); found {
		if _, err := strconv.Atoi(base); err == nil {
			return true
		}
	}
	// ddp5 / dts5 / dtshd7 这类被空格切开的音轨写法
	for _, prefix := range []string{"ddp", "dd", "dts", "aac"} {
		if base, found := strings.CutPrefix(s, prefix); found && base != "" {
			if _, err := strconv.Atoi(base); err == nil {
				return true
			}
		}
	}
	return false
}

// NormalizeName 把片名 / 别名 / 发布名归一成可比较的字符串。
//
// 顺序不能乱：先全角转半角（否则全角标点不会被后面的分隔符规则处理），
// 再统一分隔符，最后压缩空白。
//
// 繁简**不做**转换：TMDB 的 translations 里繁简都有，订阅的别名集合天然覆盖
// 两种写法，繁体发布名归一化后会与繁体别名精确相等，不需要引 OpenCC。
func NormalizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	var folded strings.Builder
	folded.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '　': // 全角空格
			folded.WriteRune(' ')
		case r >= '！' && r <= '～': // 全角 ASCII
			folded.WriteRune(r - 0xFEE0)
		default:
			folded.WriteRune(r)
		}
	}

	var out strings.Builder
	out.Grow(folded.Len())
	for _, r := range folded.String() {
		switch r {
		case '.', '_', '-', '·', '：', ':', '/', '\\', '|', '+', '~', '　':
			out.WriteRune(' ')
		default:
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// tokenize 把候选串切成词元，用于部分覆盖度打分。
func tokenize(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if isQualityToken(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// ————————————— 从原文兜底识别画质字段 —————————————
//
// rules 的解析器用的是 guessit，遇到中文标签夹在中间（`1080p.中英双字.WEB-DL`）
// 或纯符号分隔（`H.265-FLUX`）时会漏掉片源/编码。这些字段直接决定画质选优，
// 漏了用户设的优先级就形同虚设，所以按原文再兜一次。

// tokenizeForScan 把发布名压成「无分隔符小写串」，便于做子串匹配。
func tokenizeForScan(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case '.', '_', '-', '·', '：', ':', '/', '\\', '|', '+', '~', '　', ' ', '\t':
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// sourceFromRaw 按优先级从原文里认片源。越靠前的越具体，必须先匹配。
func sourceFromRaw(raw string) string {
	s := tokenizeForScan(raw)
	candidates := []struct {
		needle  string
		display string
	}{
		{"uhdremux", "Remux"}, {"bdremux", "Remux"}, {"blurayremux", "Remux"}, {"remux", "Remux"},
		{"bluray", "BluRay"}, {"bluraydisc", "BluRay"}, {"bdrip", "BluRay"}, {"brrip", "BluRay"},
		{"webdl", "WEB-DL"}, {"webrip", "WEBRip"},
		{"hdtv", "HDTV"}, {"pdtv", "HDTV"},
		{"dvdrip", "DVD"}, {"dvd", "DVD"},
		{"hdcam", "CAM"}, {"cam", "CAM"},
		{"hdts", "TS"}, {"telesync", "TS"},
		{"telecine", "TC"}, {"dvdscr", "SCR"}, {"bdscr", "SCR"}, {"screener", "SCR"},
	}
	for _, c := range candidates {
		if strings.Contains(s, c.needle) {
			return c.display
		}
	}
	return ""
}

// codecFromRaw 从原文里认视频编码。
func codecFromRaw(raw string) string {
	s := tokenizeForScan(raw)
	switch {
	case strings.Contains(s, "av1"):
		return "AV1"
	case strings.Contains(s, "h265"), strings.Contains(s, "x265"), strings.Contains(s, "hevc"):
		return "H.265"
	case strings.Contains(s, "h264"), strings.Contains(s, "x264"), strings.Contains(s, "avc"):
		return "H.264"
	case strings.Contains(s, "vp9"):
		return "VP9"
	case strings.Contains(s, "mpeg2"):
		return "MPEG-2"
	case strings.Contains(s, "xvid"):
		return "XviD"
	case strings.Contains(s, "divx"):
		return "DivX"
	}
	return ""
}

// resolutionFromRaw 从原文里认分辨率。先看带 p 的写法，再看 4K/8K。
func resolutionFromRaw(raw string) string {
	s := tokenizeForScan(raw)
	for _, probe := range []struct {
		needle  string
		display string
	}{
		{"2160p", "2160p"}, {"4320p", "4320p"}, {"1440p", "1440p"}, {"1080p", "1080p"},
		{"720p", "720p"}, {"576p", "576p"}, {"540p", "540p"}, {"480p", "480p"}, {"360p", "360p"},
		{"8k", "4320p"}, {"4k", "2160p"}, {"2k", "1440p"},
	} {
		if strings.Contains(s, probe.needle) {
			return probe.display
		}
	}
	return ""
}

// pickSource 在解析器的结论和原文直读的结论里挑一个。
//
// 唯一让原文覆盖解析器的情形是 Remux：guessit 遇到 `2160p.UHD.BluRay.Remux`
// 会给 BluRay，把整条链里最高那一档丢掉，而片源直接决定选优排序。
// 只放行这一个词是有意的 —— 原文直读是子串匹配，范围放开会误判
// （比如发布组名里带 "BluRay" 而实际是 WEB-DL）。
func pickSource(fromParser, fromRaw string) string {
	if fromParser == "" {
		return fromRaw
	}
	if fromRaw == "Remux" && fromParser != "Remux" {
		return "Remux"
	}
	return fromParser
}

func strutilFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ————————————————————— 画质字段归一化 —————————————————————

// 解析器会给出 "4K" / "2160" / "FHD" 等多种分辨率写法，统一成画质方案里的
// 规范值，否则用户配的优先级对不上。
func canonicalResolution(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return ""
	case "4k", "uhd", "2160", "2160p":
		return "2160p"
	case "8k", "4320", "4320p":
		return "4320p"
	case "2k", "1440", "1440p":
		return "1440p"
	case "1080", "1080p", "fhd":
		return "1080p"
	case "720", "720p", "hd":
		return "720p"
	case "576", "576p":
		return "576p"
	case "540", "540p":
		return "540p"
	case "480", "480p", "sd":
		return "480p"
	case "360", "360p":
		return "360p"
	}
	return s
}

func canonicalVideoCodec(raw string) string {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "_", "", ".", "").Replace(strings.TrimSpace(raw)))
	switch s {
	case "":
		return ""
	case "HEVC", "H265", "X265":
		return "H.265"
	case "AVC", "H264", "X264":
		return "H.264"
	case "AV1":
		return "AV1"
	case "VP9":
		return "VP9"
	case "MPEG2":
		return "MPEG-2"
	case "XVID":
		return "XviD"
	case "DIVX":
		return "DivX"
	}
	return s
}

func canonicalSource(raw string) string {
	s := strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.TrimSpace(raw)))
	switch s {
	case "":
		return ""
	case "remux", "bdremux", "uhdremux", "blurayremux":
		return "Remux"
	case "bluray", "bdrip", "brrip", "bd", "bluraydisc":
		return "BluRay"
	case "webdl", "web", "webdlrip", "amzn", "nf", "dsnp", "hmax", "atvp", "itunes":
		return "WEB-DL"
	case "webrip", "webcap":
		return "WEBRip"
	case "hdtv", "hdtvrip", "pdtv", "dsr":
		return "HDTV"
	case "dvdrip", "dvd", "dvd5", "dvd9":
		return "DVD"
	case "hdcam", "cam", "camrip":
		return "CAM"
	case "ts", "telesync", "hdts":
		return "TS"
	case "tc", "telecine":
		return "TC"
	case "screener", "dvdscr", "bdscr", "scr":
		return "SCR"
	case "r5":
		return "R5"
	}
	return strings.TrimSpace(raw)
}
