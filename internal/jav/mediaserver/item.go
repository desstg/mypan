// Package mediaserver 是 Emby / Jellyfin 的客户端，只做一件事：
// 把媒体库里的条目读出来，并从中提取番号。
//
// 提取结果决定卡片上「已入库 / 未入库」的角标，以及洗版判定里「库里已经有更好的了吗」。
// 所以这里宁可**少提取**也不能**提取错** —— 一个错的番号会让完全无关的影片
// 显示成已入库，而用户没有办法察觉。
package mediaserver

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// 标准番号形态，两种都常见：
	//   SSIS-001 / ABC_123  —— 字母 + 分隔符 + 数字
	//   FC2PPV1234567        —— 字母直接接数字（FC2 系）
	//
	// 开头那个 \b 不能省：没有它，"FC2PPV1234567" 里 "PPV123456" 会被当成
	// 一个番号（正则引擎从位置 3 起匹到了它）。加上后这类词内匹配全部失效，
	// 整串会落到兜底路径拿到正确结果。
	reStandard = regexp.MustCompile(`(?i)\b([A-Z0-9]{2,12}[-_][A-Z0-9]{2,10}|[A-Z]{2,10}\d{3,6})`)

	// 兜底：取第一个词。
	reFirstWord = regexp.MustCompile(`(?i)^([a-z0-9_-]{3,25})`)

	// 这些词长得像番号（都是 2-6 个大写字母 + 数字），但其实是片名里的常见词。
	// 兜底路径命中它们时会提取出一个完全错误的「番号」。
	excludeWords = map[string]struct{}{
		"THE": {}, "THIS": {}, "WHAT": {}, "WITH": {},
	}
)

// ExtractCode 从一段文本（文件名或路径）里提取番号。
//
// 与源码 mediaserver.extract_code 同构：先找标准形态，找不到再退化成第一个词。
// 顺序很重要 —— 反过来会把 "SSIS-001" 这样的字符串切成 "SSIS"。
//
// 比源码多两道闸，都是实测出来的误判：
//
//  1. 标准形态匹配到的结果**必须含数字**。源码的正则只要求「字母数字 + 分隔符」，
//     于是 "this-file.mkv" 会提出一个 "THIS-FILE" 的假番号。真番号没有不含数字的，
//     用「含数字」这条把它挡住，比往黑名单里逐个加英文单词可靠得多。
//  2. 兜底路径按**首段**判黑名单，而不是整词。源码拿 "this-file" 去比对
//     THE/THIS/WHAT/WITH，整词对不上，照样漏过去。
func ExtractCode(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	// 只看文件名部分：路径里的目录名常常是发行商名，容易被误当成番号。
	if base := filepath.Base(filepath.FromSlash(s)); base != "" && base != "." {
		s = base
	}

	// 取**第一个含数字**的匹配，而不是第一个匹配。
	// "HD-SSIS-001.mkv" 的第一个匹配是 "HD-SSIS"（无数字、是画质标记），
	// 只要第一个匹配就返回的话，真番号 SSIS-001 会被它顶掉。
	for _, m := range reStandard.FindAllStringSubmatch(s, -1) {
		if code := strings.ToUpper(m[1]); strings.ContainsAny(code, "0123456789") {
			return code
		}
	}
	if m := reFirstWord.FindStringSubmatch(s); m != nil {
		word := strings.ToUpper(m[1])
		if _, bad := excludeWords[firstSegment(word)]; !bad {
			return word
		}
	}
	return ""
}

// firstSegment 取 "THIS-FILE" 里的 "THIS"。
func firstSegment(word string) string {
	if i := strings.IndexAny(word, "-_"); i > 0 {
		return word[:i]
	}
	return word
}

// ItemQuality 从一个条目的媒体信息里读出分辨率档位与体积。
//
// 分辨率：height >= 2160 → 2（超清），>= 720 → 1（高清），否则 0。
// 体积取所有媒体源里最大的那个 —— 一个条目可能同时挂着 direct 与 hls 两个源，
// 体积是以源为单位的，取最大才代表这份文件真实有多大。
func ItemQuality(heights []int, sizes []int64) (resolution int, sizeBytes int64) {
	for _, h := range heights {
		switch {
		case h >= 2160:
			if resolution < 2 {
				resolution = 2
			}
		case h >= 720:
			if resolution < 1 {
				resolution = 1
			}
		}
	}
	for _, s := range sizes {
		if s > sizeBytes {
			sizeBytes = s
		}
	}
	return resolution, sizeBytes
}
