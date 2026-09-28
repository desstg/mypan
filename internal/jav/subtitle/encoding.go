// Package subtitle 从迅雷看看的字幕接口里挑一份「最优」字幕并落成 Emby 认的文件。
//
// # 为什么是迅雷看看
//
// 它是 XL_center（另一套媒体中心）用的源，源自开源插件 MeiamSubtitles：**私有接口、
// 无鉴权、无 API key、无 cookie**，只要一个 User-Agent。代价是没有任何契约文档 ——
// 字段名是抓来的，上游改了就静默返回空列表（见 Search 的处理）。
//
// # 本包不 import internal/jav 根包
//
// 与 internal/jav/emby 同一条规矩：避免成环，也让本包能被独立测试。设置、图片抓取、
// 库句柄一律由调用方从外面递进来。
//
// # 「最优」是两层判据，不是一个分数
//
// 先按**搜索词**兜底（番号搜不到就用标题，见 Client.BestSubtitle），再在结果里按
// 匹配档 → 语言 → 时长 → 上游 score 排序（见 PickBest）。这两层解决的是不同问题：
// 第一层解决「搜不到」，第二层解决「搜到了但挑错」。
package subtitle

import (
	"bytes"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// BOM 常量。三种都要认：迅雷返回的 srt 实测是 UTF-8 with BOM（已实测确认），
// 而 UTF-16 的两种 BOM 是从别处收来的字幕里常见的。
var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// NormalizeEncoding 把一份字幕字节归一成**不带 BOM 的 UTF-8**。
//
// # 为什么必须做这件事
//
// XL_center 的原实现是 `writeFileSync(out, content, 'utf-8')` —— 把响应字节按 UTF-8
// 解。中文字幕有相当一部分是 GBK，那一解就是满屏 U+FFFD，而且是**静默**的：文件写
// 出去了、播放器也加载了，只是字全坏了。用户看到的是「这字幕是乱码」，不会想到是编码。
//
// # 判据顺序
//
//  1. 有 BOM 就信 BOM（UTF-8 去掉即可，UTF-16 转换）。BOM 是**显式声明**，比任何
//     猜测都可靠，所以放第一位。
//  2. 没有 BOM 时先 `utf8.Valid` 校验。这一条覆盖了绝大多数情况 —— UTF-8 中文字幕
//     不带 BOM 也是合法的 UTF-8，不该被当成 GBK 再转一次。
//  3. 校验失败才按 **GB18030** 转。用 GB18030 而不是 GBK：它是 GBK 的超集，
//     能多认下四字节的罕用字，而认错的代价只是「本来也解不出来的那几个字」。
//
// 转换失败（字节序列在两种编码下都不合法）时**原样返回**：一份坏字幕总比一份空文件
// 好，而且调用方后面还有内容校验兜着。
func NormalizeEncoding(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	switch {
	case bytes.HasPrefix(raw, bomUTF8):
		return raw[len(bomUTF8):]
	case bytes.HasPrefix(raw, bomUTF16LE), bytes.HasPrefix(raw, bomUTF16BE):
		// unicode.BOMOverride 按 BOM 自己选 UTF-16 的字节序（所以不必分 LE/BE 两次）。
		if out, _, err := transform.Bytes(unicode.BOMOverride(unicode.UTF8.NewDecoder()), raw); err == nil {
			return out
		}
		return raw
	}
	if utf8.Valid(raw) {
		return raw
	}
	if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
		return out
	}
	return raw
}
