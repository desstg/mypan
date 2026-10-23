package strm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/mediaorganize/rules"
)

// 「一个作品」在磁盘上的载体有两种：
//
//   - 本应用生成的 `.strm`（正文是一行播放地址），STRM 任务那面的全部产物；
//   - 用户**别的工具产出的真实视频文件**（mp4 / mkv …）—— 海报墙挂上来的「本地目录」
//     里绝大多数就是这种，以前整套番号逻辑只认 `.strm`，于是那些库里一部片都认不出来。
//
// 这两个函数就是这个二选一的**唯一判据**。凡是要问「这一层有几个作品」「这个文件
// 算不算一部片」的地方都走这里，别在各处自己 `EqualFold(ext, ".strm")` ——
// 那种写法散在五六个文件里，加载体时会漏掉其中两处，而漏掉的表现是
// 「墙上有卡、刮削扫不到」或者反过来的静默不一致。

// videoExtensions 是「算一个作品」的视频扩展名（小写、不带点）。
//
// **不另写一份列表**：直接取 mediaorganize/rules 的默认媒体扩展名 —— 那是「哪些文件
// 参与媒体整理」的同一批后缀，两处各写一份的话，用户改了那一项设置就会出现
// 「整理认得、墙上不认得」。
var videoExtensions = rules.ParseExtensionSet(rules.DefaultMediaExtensions)

// IsStrmFileName 报告这个文件名是不是本应用生成的 `.strm`。
func IsStrmFileName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".strm")
}

// IsVideoFileName 报告这个文件名是不是一个真实视频文件。
//
// 只按扩展名判，**不 stat**：这个判据在扫盘的热路径上（每个目录一次 ReadDir 之后
// 逐名判定），多一次 stat 在 SMB / 网盘路径上就是多一次网络往返。
func IsVideoFileName(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "" {
		return false
	}
	_, ok := videoExtensions[ext]
	return ok
}

// IsMediaFileName 报告这个文件是不是「一个作品的载体」（`.strm` 或真实视频）。
func IsMediaFileName(name string) bool {
	return IsStrmFileName(name) || IsVideoFileName(name)
}

// PickMediaNames 从一次 ReadDir 的文件名里挑出**这一层的作品载体**，一个主干留一个。
//
// 三条规矩：
//
//  1. 非媒体文件（海报 / nfo / 侧车 / 字幕 / 广告 mht …）一律不算；
//  2. 同一主干的多个载体**收成一个**：`NIMA-086.strm` 与 `NIMA-086.mp4` 是同一部片
//     （生成 `.strm` 时源文件还在网盘上，本地不该同时有；但用户可能自己放了一份）。
//     不收的话两张卡的 ID 一样、产物名也一样，墙上会重复，删一部会连累另一部；
//  3. 优先留 `.strm`（那是带播放地址、四个产物都按它命名的那一份），没有才用视频本身。
//
// 主干比较**忽略大小写**：Windows 上 `A-1.mp4` 与 `a-1.strm` 是同一部，Linux 上
// 名字不同但同样只该占一张卡。返回的文件名保持**磁盘上的原样**（大小写不能动，
// 见 mediaStemKey 的注释），按字典序排列（同一棵目录两次扫描的可观测行为必须一致）。
func PickMediaNames(names []string) []string {
	type picked struct{ strm, video string }
	byStem := make(map[string]*picked, len(names))
	order := make([]string, 0, len(names))
	for _, name := range names {
		if !IsMediaFileName(name) {
			continue
		}
		key := strings.ToLower(MediaStem(name))
		p, ok := byStem[key]
		if !ok {
			p = &picked{}
			byStem[key] = p
			order = append(order, key)
		}
		if IsStrmFileName(name) {
			if p.strm == "" || name < p.strm {
				p.strm = name
			}
			continue
		}
		if p.video == "" || name < p.video {
			p.video = name
		}
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		p := byStem[key]
		if p.strm != "" {
			out = append(out, p.strm)
			continue
		}
		out = append(out, p.video)
	}
	sort.Strings(out)
	return out
}

// PickMediaNamesOfEntries 是 PickMediaNames 的目录版（只取文件名，不碰子目录）。
func PickMediaNamesOfEntries(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return PickMediaNames(names)
}

// mediaNameForStem 在一个目录里找出**主干等于 stem**的那个载体文件名（找不到返回 ""）。
//
// 用在「手上只有主干、要拿它去问别的判据」的地方（比如 `FindLocalSidecar` 要的是
// 载体名，而不是主干）。以前那些调用点直接拼 `stem+".strm"` —— 在纯视频库里
// 那个名字根本不存在，于是配对判据静默返回「没有」，而调用方把它当成「还没有」。
//
// 同一主干有多个载体时按 `PickMediaNames` 的规矩（`.strm` 优先）。主干比较忽略大小写，
// 返回值保持磁盘上的原样。
func mediaNameForStem(absDir, stem string) string {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(stem))
	if key == "" {
		return ""
	}
	for _, name := range PickMediaNamesOfEntries(entries) {
		if strings.ToLower(MediaStem(name)) == key {
			return name
		}
	}
	return ""
}
