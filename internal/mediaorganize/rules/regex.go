package rules

import "regexp"

var (
	dottedYearRe      = regexp.MustCompile(`[.\s]((18|19|20)\d{2})[.\s]`)
	identityYearRe    = regexp.MustCompile(`(?is)^\s*(.+?)\s*[\(（]\s*((18|19|20)\d{2})(?:[.\s,，、·/_-][^\)）]*)?[\)）]`)
	dottedTitleYearRe = regexp.MustCompile(`(?is)^(.+?)[.\s]((18|19|20)\d{2})`)
	yearOnlyTitleRe   = regexp.MustCompile(`^\s*(18|19|20)\d{2}\s*$`)

	knownIDTagRe                = regexp.MustCompile(`(?i)[\{\[]\s*(?:tmdb|tmdbid|imdb|imdbid|tvdb|tvdbid|douban|doubanid|bangumi|anidb)\s*[=\-:]\s*[^\}\]]+[\}\]]`)
	releaseSiteLeadingBracketRe = regexp.MustCompile(`(?is)^\s*[【\[][^】\]]*(?:发布|www\.|https?://|(?:\.com|\.net|\.org|\.cc|\.tv|\.me|\.io)\b|影视之家|高清影视|资源网|论坛|社区|家园|站点|网站)[^】\]]*[】\]]\s*`)

	cnQualityTagRe    = regexp.MustCompile(`(?:蓝光原盘|4K原盘|2K原盘|UHD原盘|杜比视界|杜比全景声|杜比音效|内封(?:简[繁日英中]?|繁[简日英]?|日[英简繁]?|中[简繁日英]?|英[简繁日中]?|特效|双语|多语|官方|官)?字幕|外挂(?:简[繁日英中]?|繁[简日英]?|双语)?字幕|特效字幕|内嵌字幕|压制字幕|简繁[中日英]*内封|国语配音|国语原声|国[日英粤台韩]双语?(?:音|配)?|国粤[日英]?双?语?|国英台?双?语?|多音轨|多声道|无损音轨|高码率|压制版|压制组|中英字幕|官方字幕|加长版|导演剪辑版?|未删减版|数码修复版?|特典映像|特典)`)
	cnShortTagRe      = regexp.MustCompile(`(^|[\s._\-+\[\(【（])(?:蓝光|原盘|双语|中字|国配|台配|港配|官中|压制|高清|超清|无损|HQ)(?:[\s._\-+\]\)】）]|$)`)
	enQualityTagRe    = regexp.MustCompile(`(?i)(^|[\s._\-+\[\(【（])(?:2160p|1080p|720p|480p|4K|2K|8K|UHD|FHD|FullHD|WEB[-. ]?DL|WEB[-. ]?Rip|BluRay|BDRip|BDMV|BD25|BD50|HDTV|HDTVrip|DVDRip|DVD[-. ]?9|DVD[-. ]?5|REMUX|HDR\d*\+?|Dolby[. ]Vision|DoVi|SDR|HLG|10[. ]?bit|8[. ]?bit|H\.?264|H\.?265|HEVC|AVC|x264|x265|VP9|AV1|DTS[-.]?HD[. ]?MA|DTS[-.]?HD|DTS[-.]?X|DTS|DDP|DD\+|DD|AC3|EAC3|TrueHD|Atmos|FLAC|AAC|OPUS|MP3|PCM|\d\.\d|\d{2,3}fps|Subs?|MultiSubs?|MultiAudio|Multi[. ]?Lang)([\s._\-+\]\)】）]|$)`)
	enEditionSuffixRe = regexp.MustCompile(`(?i)\s+(?:Repack|Proper|Extended|Director'?s Cut|Theatrical|Uncut)\s*$`)
	noiseSpaceRe      = regexp.MustCompile(`[\s._\-+\[\]【】（）()]+`)

	releaseGroupTailRe    = regexp.MustCompile(`^(.*?)[\s._\-]+([A-Za-z0-9]{2,12})\s*$`)
	releaseGroupDashRe    = regexp.MustCompile(`^(.*?)\s*-\s*([A-Za-z0-9]{2,12})\s*$`)
	releaseGroupGenericRe = regexp.MustCompile(`(?i)^(.+?)[\-_.]([\w\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]{1,12})\s*$`)
	seTokenRe             = regexp.MustCompile(`(?i)^S\d+E\d+$`)

	cnSeasonSuffixRe = regexp.MustCompile(`(?i)^(?P<title>.+?)[\s._\-]*第\s*(?P<num>[0-9]+|[零〇一二两三四五六七八九十百]+)\s*[季部]\s*$`)
	enSeasonSuffixRe = regexp.MustCompile(`(?i)^(?P<title>.+?)[\s._\-]+(?:Season|Series)\s*0*(?P<num>\d{1,3})\s*$`)
	trailingNumberRe = regexp.MustCompile(`(?P<title>.+?)[\s._\-]*(?P<num>\d{1,2})\s*$`)

	// cnBracketTagRe 匹配含中文关键词的方括号/中文括号标签（[全10集] [内封简英字幕] 【广告】），
	// 这类标签不参与标题识别；纯数字/字母的方括号（如 [2160p] [01]）不匹配、保留。
	cnBracketTagRe = regexp.MustCompile(`(?i)[\[【][^\]】]*?(?:全\s*[0-9零一二三四五六七八九十百]+\s*[集话]|字幕|内封|外挂|特效|内嵌|压制|配音|音轨|国语|粤语|台配|港配|国配|双语|多音轨|原盘|杜比|特典|花絮|加长版|未删减|导演剪辑|高码率|合集|中英|简繁|广告)[^\]】]*?[\]】]`)

	chineseSubTagRe          = regexp.MustCompile(`(?i)[\[【][^\]】]*?(?:字幕组|压制组|压制|字幕社|动漫国|fansub|sub|raws?|喵萌|霜庭云花|爱恋|猎户|动音漫影|花园字幕组|风之圣殿|澄空学园|轻之国度|肉肉|纪伊宫|银光字幕组|北宇治|漫猫|樱都|桜都|萌樱|悠哈璃羽|云歌|氢气烤肉架|拨雪寻春|沸羊羊|极影|织梦|枫叶|猪猪|幻之|曙光|恶魔岛|爱恋字幕社|Lilith[-\s]?Raws|ANi|VCB[- ]?Studio|DBD|DKB|SweetSub|LoliHouse|Nekomoe|MCE|HYSUB|KTXP|MingY|NC[-\s]?Raws|喵萌奶茶屋|动漫之家)[^\]】]*?[\]】]`)
	animeBracketSquareRe     = regexp.MustCompile(`\[[^\]]+\]`)
	animeBracketCornerRe     = regexp.MustCompile(`【[^】]+】`)
	animeEpisodeRe           = regexp.MustCompile(`\s+-\s*(\d{1,4})\s*(?:[\(（]|\[|【|$|[\s.])`)
	animeQualityRe           = regexp.MustCompile(`(?i)(?:1080p|720p|2160p|480p|4K|HDR|x264|x265|HEVC|AVC|WEB[- ]?DL|WEBRip|BluRay|BDRip|BDMV|HDTV|AAC|FLAC|OPUS|GB|MB|REMUX|Atmos|TrueHD|DTS|杜比视界|杜比全景声|原盘)`)
	animeSizeTagRe           = regexp.MustCompile(`(?i)^\d+(?:\.\d+)?\s*[KMGT]B$`)
	animeTitleYearRe         = regexp.MustCompile(`\s*[\(（]?\s*((19|20)\d{2})\s*[\)）]?\s*$`)
	animeTrailingEpRe        = regexp.MustCompile(`(?:^|\s|-)\s*(\d{1,3})(?:v\d+)?\s*$`)
	bareEpisodeWithQualityRe = regexp.MustCompile(`(?i)^(\d{1,4})(?:v\d+)?(?:\s+(?:4k|8k|2160p|1080p|720p|480p|uhd|hd))*$`)
	bracketEpisodeOnlyRe     = regexp.MustCompile(`[\[【](\d{1,4})(?:v\d+)?[\]】]`)

	sceneMovieReleaseRe = regexp.MustCompile(`(?i)(?:\.(?:19|20)\d{2}\.(?:2160|1080|720|480)p|(?:2160|1080|720|480)p[\s._\-]*(?:WEB[- ]?DL|BluRay|REMUX|HDTV|WEBRip)|WEB[- ]?DL[\s._\-]*H\.?26[45]|(?:H\.?265|HEVC|x265)[\s._\-]*(?:HDR|HQ|DTS)|60fps|DTS\d|高码)`)
	seInNameRe          = regexp.MustCompile(`(?i)[\s._\-\[]S\d{1,2}E\d{1,4}(?:$|[^0-9A-Za-z])`)

	absLeadingEpRe  = regexp.MustCompile(`^(\d{3,4})(?:[.\s_\-]|$)`)
	tmdbTagPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\[tmdbid[=\-](\d+)\]`),
		regexp.MustCompile(`(?i)\[tmdb[=\-](\d+)\]`),
		regexp.MustCompile(`(?i)\{tmdbid[=\-](\d+)\}`),
		regexp.MustCompile(`(?i)\{tmdb[=\-](\d+)\}`),
		regexp.MustCompile(`(?i)\{\[\s*tmdbid\s*=\s*(\d+)\s*`),
	}

	organizedStructureRe = regexp.MustCompile(`(?i)^.+?\s\((?:19|20)\d{2}\)(?:\s+S\d{1,3}E\d{1,4})?(?:\s+\[[^\]]*\])?\.[^.]+$`)
	titleNoiseRe         = regexp.MustCompile(`(?i)www\.|https?://|(?:\.com|\.net|\.org|\.cc|\.tv|\.me|\.io)\b|发布|影视之家|资源网|论坛|社区`)
	resolutionInTitleRe  = regexp.MustCompile(`(?i)\d{3,4}p`)
	sceneKeywordsRe      = regexp.MustCompile(`(?i)\b(?:WEB[- ]?DL|BluRay|REMUX|HDTV|HEVC|x265|DTS)\b`)
	pureChineseTitleRe   = regexp.MustCompile(`^[\p{Han}]{2,16}$`)
	chineseTitleCoreRe   = regexp.MustCompile(`^([\p{Han}]+(?:[·・][\p{Han}]+)*)`)
	enWordRe             = regexp.MustCompile(`[a-z0-9]{3,}`)
	// seasonOnlyPatterns 识别季-only（无集号）：S01 / Season 1 / 第1季，如 S01.2160p.WEB-DL...
	seasonOnlyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:^|[\s._\-])(?:S|Season)\s*0*(\d{1,3})(?:$|[\s._\-])`),
		regexp.MustCompile(`(?:^|[\s._\-])第\s*(\d{1,3})\s*季(?:$|[\s._\-])`),
	}
	// codecEpisodeTokenRe 匹配视频编码 token：H.265 的 265 / x265 / HEVC / AVC
	codecEpisodeTokenRe = regexp.MustCompile(`(?i)h[\s._\-]?26[45]|x[\s._\-]?26[45]|hevc|avc`)
	seasonDirPatterns   = []struct {
		re      *regexp.Regexp
		extract func([]string) *int
	}{
		{regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:season|series)\s*0*(\d{1,3})\b`), func(m []string) *int { n, _ := parseInt(m[1]); return intPtr(n) }},
		{regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s0*(\d{1,3})\b`), func(m []string) *int { n, _ := parseInt(m[1]); return intPtr(n) }},
		{regexp.MustCompile(`(?:^|[^0-9])第\s*(\d{1,3})\s*[季部](?:$|[\s._\-()（）【】\[\]])`), func(m []string) *int { n, _ := parseInt(m[1]); return intPtr(n) }},
		{regexp.MustCompile(`(?:^|[^零〇一二两三四五六七八九十百])第([零〇一二两三四五六七八九十百]+)\s*[季部](?:$|[\s._\-()（）【】\[\]])`), func(m []string) *int { return ChineseNumberToInt(m[1]) }},
	}

	numberPattern = `(\d{1,4}|[零〇一二两三四五六七八九十百]{1,6})`
)

func compileEpisodePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:第\s*` + numberPattern + `\s*[季部])\s*第\s*` + numberPattern + `\s*[集话話回]`),
		regexp.MustCompile(`(?i)(?:第\s*` + numberPattern + `\s*[季部])\s*[Ee][Pp]?\s*` + numberPattern),
		regexp.MustCompile(`(?i)(?:Season|Series)\s*` + numberPattern + `\s*(?:Episode|Ep|E)\s*` + numberPattern),
		regexp.MustCompile(`(?i)[Ss]\s*` + numberPattern + `\s*[Ee]\s*` + numberPattern),
		regexp.MustCompile(`(?i)` + numberPattern + `\s*[xX]\s*` + numberPattern),
	}
}

func compileEpisodeOnlyPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:第\s*` + numberPattern + `\s*[集话話回期])`),
		regexp.MustCompile(`(?i)(?:^|[\s._\-\[])(?:EP|Ep|ep|Episode|episode|E)\s*` + numberPattern + `(?:$|[\s._\-\]])`),
		regexp.MustCompile(`(?i)(?:^|[\s._\-\[])` + numberPattern + `\s*(?:集|话|話|回|期)(?:$|[\s._\-\]:：])`),
	}
}
