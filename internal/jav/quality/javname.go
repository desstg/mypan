package quality

import (
	"strconv"
	"strings"
)

// 番号资源的标准文件命名。
//
// 这套命名是**两处共用**的契约，所以放在这个纯算法包里：
//
//   - 写侧车时（internal/jav/sidecar.go）用它拼出 `<番号>-UC-4K.json`；
//   - 目录整理时（internal/mediaorganize/javplanner）从**文件名**把它拆回来，
//     不必再去读侧车的内容。
//
// 为什么整理那边靠文件名而不是读 JSON：一次计划要处理几十个作品，逐个把 json
// 从网盘读下来是几十次「解析直链 + 下载」，全压在生成预览这一步上，而且每次
// 读取都是一个会失败的环节（读不到就整目录回落到旧命名）。名字里写着的东西，
// List 一次就拿到了。
//
// 也正因为要拆回来，**算法只能有一份**：两边各写一份的话，将来一边改了
// （加个 -HD 之类），另一边不认，就成了「文件明明在那儿但认不出来」的静默失效。

// Marks 是一颗资源的质量标记，决定名字里的字母段与 4K 段。
//
// 刻意只有这三样：档位再细（UHD/HD）对媒体库没有意义，而它们是用户实际
// 在文件名里区分版本的那几个 —— 同一部片的高清版与破解 4K 版要靠它对上号。
type Marks struct {
	// Uncensored 是「破解/无码」，字母段里的 U。
	Uncensored bool
	// Subtitle 是「中字」，字母段里的 C。
	Subtitle bool
	// FourK 是**真 4K**（角标那一档），单独一段 -4K。
	//
	// 不是四档里的第四档（4K 仍属超清档，见 Tags.FourK 的注释），
	// 只是文件名里要单独标出来。
	FourK bool
}

// Suffix 返回标记对应的后缀，**含前导连字符**；一个标记都没有时返回空串。
//
// 顺序固定：字母段（U 在前、C 在后）→ `-4K`。所以四种组合是
// `-U` / `-C` / `-UC`，带 4K 再加 `-4K`；只带 4K 就是 `-4K` 本身 ——
// 「只带 4K」也得有个名字可叫，不能因为字母段为空就把它吞掉。
func (m Marks) Suffix() string {
	var b strings.Builder
	letters := ""
	if m.Uncensored {
		letters += "U"
	}
	if m.Subtitle {
		letters += "C"
	}
	if letters != "" {
		b.WriteString("-")
		b.WriteString(letters)
	}
	if m.FourK {
		b.WriteString("-4K")
	}
	return b.String()
}

// BuildJavFileName 组装标准番号文件名。
//
//	<番号>[-U|-C|-UC][-4K][-cdN]<ext>
//
//	SSIS-444.mp4          什么标记都没有
//	SSIS-444-C.mp4        中字
//	SSIS-444-U.mp4        破解
//	SSIS-444-UC.mp4       破解 + 中字
//	SSIS-444-4K.mp4       只有 4K
//	SSIS-444-UC-4K.mp4    破解 + 中字 + 4K
//	SSIS-444-UC-4K-cd2.mp4  第二个分片
//
// ext 原样带上（**不改大小写**，调用方自己决定带不带点）：媒体服务器按扩展名
// 识别类型，把 .mp4 写成 .MP4 没有好处，却可能在大小写敏感的环境里出问题。
// cd <= 0 表示不编号 —— 只有一个视频时加个 -cd1 是噪声。
// 番号为空返回空串，调用方据此知道「拼不出标准名」。
func BuildJavFileName(number string, m Marks, cd int, ext string) string {
	number = strings.TrimSpace(number)
	if number == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(number)
	b.WriteString(m.Suffix())
	if cd > 0 {
		b.WriteString("-cd")
		b.WriteString(strconv.Itoa(cd))
	}
	b.WriteString(ext)
	return b.String()
}

// ParseJavFileName 从文件名把它拆回番号与标记。
//
// 只拆**主名**（扩展名由调用方剥掉），从右往左吃掉认识的后缀：
// `-cdN` → `-4K` → 字母段（`-UC` / `-U` / `-C`），剩下的是番号。
//
// **本函数只做机械拆分，不判断拆出来的是不是真的番号。** 那是调用方的事：
// `internal/mediaorganize/javplanner` 会拿 `javrules.HasCode` 再兜一道 ——
// 那是这个项目里唯一权威的番号识别。不在这里重复一套判据，是因为两套判据
// 迟早会分家，而分家的表现是「同一份文件在设置页试跑里认得出、整理时不认」。
//
// 所以 `ParseJavFileName("4K")` 会老老实实返回 `4K`：它确实是「一个没有标记的
// 主名」。要不要把它当番号，由调用方决定。
//
// 只有「拆完什么都不剩」才算认不出。
func ParseJavFileName(stem string) (number string, m Marks, ok bool) {
	stem = strings.TrimSpace(stem)
	if stem == "" {
		return "", Marks{}, false
	}

	// 分片编号排在最外层，先吃掉。侧车自己不带 cd，但视频名带 —— 同一套拆法
	// 两边都能用，省得分出「拆侧车」和「拆视频」两个几乎一样的函数。
	if cut, ok := trimTrailingCD(stem); ok {
		stem = cut
	}
	// 4K 在字母段之外，顺序固定，所以先于字母段吃。
	if cut, ok := trimSuffixFold(stem, "-4K"); ok {
		m.FourK = true
		stem = cut
	}
	// 字母段。长的先试，否则 `-UC` 会被 `-U` 匹配掉、剩一个 `C` 挂在番号尾巴上。
	for _, cand := range []struct {
		token    string
		uncensor bool
		subtitle bool
	}{
		{"-UC", true, true},
		{"-U", true, false},
		{"-C", false, true},
	} {
		if cut, hit := trimSuffixFold(stem, cand.token); hit {
			m.Uncensored, m.Subtitle = cand.uncensor, cand.subtitle
			stem = cut
			break
		}
	}

	stem = strings.TrimSpace(stem)
	if stem == "" {
		return "", Marks{}, false
	}
	return stem, m, true
}

// trimTrailingCD 吃掉结尾的 `-cdN`（N 是 1~2 位数字），返回去掉后的串。
func trimTrailingCD(stem string) (string, bool) {
	lower := strings.ToLower(stem)
	idx := strings.LastIndex(lower, "-cd")
	if idx < 0 {
		return stem, false
	}
	digits := stem[idx+3:]
	if digits == "" || len(digits) > 2 {
		return stem, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return stem, false
		}
	}
	return stem[:idx], true
}

// trimSuffixFold 按**忽略大小写**吃掉结尾的 token，返回去掉后的串。
//
// 忽略大小写是因为文件名经过各种工具的手，`-uc` / `-4k` 都出现过；
// 而番号本体的大小写我们不在这里动（那是调用方的事）。
func trimSuffixFold(stem, token string) (string, bool) {
	if len(stem) < len(token) {
		return stem, false
	}
	cut := stem[:len(stem)-len(token)]
	if !strings.EqualFold(stem[len(stem)-len(token):], token) {
		return stem, false
	}
	return cut, true
}
