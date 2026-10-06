package strmscrape

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/proxybase"
)

// 海报墙的「可播放文件」查询。
//
// # 为什么要有它
//
// 墙上每张卡片背后都对应一个真实的 `.strm`，正文就是播放地址 —— 但卡片的 payload
// 里只有 rel_dir / strm_name / stem，**拿不到账号与 file_id**（见 types.go 的 Item
// 与 javwall.go 的 JavWallItem，索引表里也没有）。要「点一下就能播」，就得有人在
// 服务端把 .strm 读出来。
//
// # 为什么不让前端读
//
// `.strm` 正文里那一行带着**播放令牌**（`/t/{token}`）。它是全站共享的静态令牌
// （见 internal/strm 的 ensureToken），一旦进了浏览器就等于交给了每一个能打开
// 后台的人 —— 而这一步本来就只需要一个「能不能播」的答案，不值得把令牌摊开。
//
// # 为什么现算、不落进索引
//
// 可播性会随磁盘变（用户删了文件、刚重刮过）。落进索引就是第三份状态，
// 而 `data/strmscrape/{id}.sqlite` 那张表本来就是**可重建的缓存**。
// 一次墙刷新返回几十上百张卡，逐张读盘也会把列表拖慢。
// 点播放时才查，天然是「所见即所得」。

// bomPrefix 是 UTF-8 BOM。外部工具写过的 `.strm` 偶尔带它。
const bomPrefix = "\uFEFF"

// SubtitleFile 是作品目录里的一份字幕（`.srt` / `.vtt` / `.sup`）。
type SubtitleFile struct {
	// Name 是磁盘上的文件名（含语言后缀，如 `CEMD-851-U.zh-CN.srt`）。
	Name string `json:"name"`
	// Label 是给用户看的语言名，由文件名后缀推断（「简体中文」等）。
	// 推不出来时回落成后缀本身的大写形式，与前端原有的 subtitleLabel 同一口径。
	Label string `json:"label"`
	// Format 是 srt / vtt / sup。sup 是图形字幕（走 libbitsub 渲染），另两种是文本。
	Format string `json:"format"`
	// URL 是取这一个文件内容的站内地址。
	//
	// 关键点：**字幕在本地磁盘上**（STRM 任务同步下来的元数据小文件），
	// 不在网盘上。所以它不需要任何网盘账号或 file_id，直接读本地文件即可 ——
	// 早先以为要走一次 `files/list` 拿网盘 file id，那是想多了。
	URL string `json:"url"`
}

// PlayableFile 是某个作品目录下的一个可播放文件。
type PlayableFile struct {
	// Name 是磁盘上的 `.strm` 文件名（含后缀），用来当列表的 key 与显示名。
	Name string `json:"name"`
	// Path 是 `.strm` 正文里那一行，**原样**返回。
	//
	// 刻意**不**重新拼：那一行已经是唯一的真相源 —— 签名开关、base URL、
	// 播放令牌的变化都由它自己体现。重拼要在这里再实现一遍 strm.BuildPlayPath，
	// 那会多出一个会漂的真相源。
	Path string `json:"path"`
	// Subtitles 是同目录里配得上这个视频的字幕，按文件名排序。
	Subtitles []SubtitleFile `json:"subtitles,omitempty"`
}

// TMDBPlayable 取 TMDB 影片墙上某张卡的可播放文件。
//
// strmName 非空（单文件作品）时只读那一个；为空（多集作品，见 item.go 的
// `item.StrmName` 只在「平铺」或「只有一个条目」时才被填）则列整个目录。
func (s *Service) TMDBPlayable(ctx context.Context, taskID int64, relDir, strmName string) ([]PlayableFile, error) {
	return s.playableIn(ctx, taskID, relDir, strmName)
}

// JavPlayable 取番号墙上某张卡的可播放文件。
//
// stem 非空时用 jav 那套锚点定位（`<absDir>/<stem>.strm` 必须存在），
// 拿到的就是那一个；为空则列目录（`flat` 布局的作品会有 `-cd1` / `-cd2` 多个）。
func (s *Service) JavPlayable(ctx context.Context, taskID int64, relDir, stem string) ([]PlayableFile, error) {
	name := ""
	if strings.TrimSpace(stem) != "" {
		ref, err := s.resolveJavItem(ctx, taskID, relDir, stem)
		if err != nil {
			// 番号那套 resolveJavItem 的报错已经分好了类（非法路径 / 目录不存在 /
			// 没有对应的 .strm），直接透上去比在这里吞掉强。
			return nil, err
		}
		name = ref.StrmName
		relDir = ref.RelDir
	}
	return s.playableIn(ctx, taskID, relDir, name)
}

// playableIn 是两条路共用的实现。
//
// 入参 relDir / name 都来自客户端，所以三道闸门一道不能少（判据抄
// resolveJavItem，别另写一份）：拒绝 `..` 与分隔符 → 拼出的绝对路径必须
// `isInside(root, …)` → 只认 `.strm` 扩展名。
func (s *Service) playableIn(ctx context.Context, taskID int64, relDir, name string) ([]PlayableFile, error) {
	// resolveTask 返回的 root 已经做过 Abs（见它的实现），直接用它当基准。
	_, root, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	rel := normalizeJavRelDir(relDir)
	if rel == "" && strings.TrimSpace(relDir) != "" {
		return nil, errBadJavPath
	}
	absDir := root
	if rel != "" {
		absDir = filepath.Join(root, filepath.FromSlash(rel))
	}
	if !isInside(root, absDir) {
		return nil, errBadJavPath
	}

	// 已知文件名：只读那一个，不列目录（多集作品才需要列）。
	if name = strings.TrimSpace(name); name != "" {
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, errBadJavPath
		}
		if !strings.EqualFold(filepath.Ext(name), ".strm") {
			return nil, errBadJavPath
		}
		items := s.readPlayableFiles(taskID, absDir, rel, []string{name})
		return items, nil
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, domain.Errorf(domain.CodeNotFound, "目录不存在：%s", rel)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			continue
		}
		names = append(names, e.Name())
	}
	// 自然序：`-cd2` 要排在 `-cd10` 前面，纯字典序会反过来。
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	return s.readPlayableFiles(taskID, absDir, rel, names), nil
}

// readPlayableFiles 逐个读取 `.strm` 正文，只留下**本应用的播放地址**。
//
// 判不过的一律 `logWarn` 丢掉、**不报错**：`.strm` 是纯文本，用户可能手改过、
// 也可能指向别的播放器。那种文件对「能不能播」这个问题的答案就是「不能」，
// 而整条请求因为其中一个文件坏了就 500，会把「这一部不能播」说成「这个接口坏了」。
func (s *Service) readPlayableFiles(taskID int64, absDir, relDir string, names []string) []PlayableFile {
	// 字幕只列一次，给这个目录下每一集都带上。
	subs := listSubtitleFiles(taskID, absDir, relDir)
	out := make([]PlayableFile, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(absDir, name))
		if err != nil {
			s.log.Warn("strm wall read playable file failed", "name", name, "err", err)
			continue
		}
		// 正文只有一行，但容忍尾随空行与 BOM。
		line := strings.TrimSpace(strings.TrimPrefix(string(raw), bomPrefix))
		if line == "" {
			continue
		}
		if _, _, ok := proxybase.ParseLitePanSTRMURL(line); !ok {
			s.log.Warn("strm wall skip non-litepan play url", "name", name)
			continue
		}
		out = append(out, PlayableFile{Name: name, Path: line, Subtitles: subs})
	}
	return out
}

// listSubtitleFiles 列同目录里的字幕。
//
// **字幕在本地磁盘上**（STRM 任务同步下来的元数据小文件，见 taskMetaExtensions），
// 不涉及任何网盘账号，所以这里直接 `os.ReadDir` 就够了 —— 不需要为它多打一次
// 网盘列目录请求。
//
// 只认 srt / vtt / sup 三种扩展名，与前端 VideoPreview 的字幕候选白名单一致。
// 大小写不敏感（Windows 上用户可能手改成 `.SRT`）。
func listSubtitleFiles(taskID int64, absDir, relDir string) []SubtitleFile {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}
	out := make([]SubtitleFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".srt" && ext != ".vtt" && ext != ".sup" {
			continue
		}
		out = append(out, SubtitleFile{
			Name:   name,
			Label:  subtitleLabelFromName(name),
			Format: strings.TrimPrefix(ext, "."),
			URL:    subtitleURLFromRel(taskID, joinRel(relDir, name)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].Name, out[j].Name) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// subtitleURLFromRel 拼字幕的取文件地址。
//
// 复用海报那条 `/poster` 端点（它的 ResolvePosterFile 已经把路径安全、`isInside`、
// 扩展名白名单都做完了），只是后缀白名单扩到字幕。接口路径名沿用 poster 不动 ——
// 它已经是一个「按 rel 取任务输出目录里的本地文件」的通用出口，
// 为了名字好看去动它会把所有现存海报 URL 一起改掉。
func subtitleURLFromRel(taskID int64, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	return fmt.Sprintf("/api/admin/strm-scrape/poster?strm_task_id=%d&rel=%s", taskID, pathEscape(rel))
}

// subtitleLabelFromName 从文件名后缀推语言名。
//
// 与前端 VideoPreview.subtitleLabel **同一套口径**（那边是按视频主干切后缀再判），
// 这里退化成「看整个文件名的后缀」——因为服务端不知道视频主干是哪个；实际命名
// （`CEMD-851-U.zh-CN.srt`）两种判法得到同一个结果。
func subtitleLabelFromName(name string) string {
	lower := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	// 取最后一段带分隔符的后缀，如 `xxx.zh-cn` 里的 `zh-cn`。
	suffix := lower
	for _, sep := range []string{".", "_", "-", " "} {
		if i := strings.LastIndex(lower, sep); i >= 0 && i+1 < len(lower) {
			if s := lower[i+1:]; s != "" {
				// 只在该段确实像语言标记时才采用，否则保留完整主干去匹配中文词。
				if isLanguageToken(s) {
					suffix = s
					break
				}
			}
		}
	}
	switch {
	case strings.HasPrefix(suffix, "zh-cn"), strings.HasPrefix(suffix, "chs"),
		strings.HasPrefix(suffix, "sc"), strings.Contains(lower, "简"), strings.Contains(lower, "简体"):
		return "简体中文"
	case strings.HasPrefix(suffix, "zh-tw"), strings.HasPrefix(suffix, "cht"),
		strings.HasPrefix(suffix, "tc"), strings.Contains(lower, "繁"), strings.Contains(lower, "繁體"):
		return "繁體中文"
	case strings.HasPrefix(suffix, "zh"), strings.Contains(lower, "中文"), strings.Contains(lower, "中字"):
		return "中文"
	case strings.HasPrefix(suffix, "en"), strings.Contains(lower, "english"):
		return "English"
	case strings.HasPrefix(suffix, "ja"), strings.HasPrefix(suffix, "jp"), strings.Contains(lower, "日本"):
		return "日本語"
	case strings.HasPrefix(suffix, "ko"), strings.Contains(lower, "한국"):
		return "한국어"
	}
	if isLanguageToken(suffix) {
		return strings.ToUpper(suffix)
	}
	return "默认字幕"
}

// isLanguageToken 判断一段后缀像不像语言标记（避免把 `CEMD` 这种误当成语言）。
func isLanguageToken(s string) bool {
	switch s {
	case "zh", "zh-cn", "zh-tw", "zh-hans", "zh-hant", "chs", "cht", "sc", "tc",
		"en", "eng", "english", "ja", "jp", "jpn", "japanese",
		"ko", "kor", "kr", "korean":
		return true
	}
	return false
}

// naturalLess 是「数字当数字比」的字符串比较，与前端 episodes 的
// `localeCompare(..., { numeric: true })` 同一意图 —— 两边的顺序要是反的，
// 用户选「第 2 集」会播到第 10 集。
func naturalLess(a, b string) bool {
	al, bl := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(al) && j < len(bl) {
		ca, cb := al[i], bl[j]
		da, db := ca >= '0' && ca <= '9', cb >= '0' && cb <= '9'
		if da && db {
			ia, jb := i, j
			for ia < len(al) && al[ia] >= '0' && al[ia] <= '9' {
				ia++
			}
			for jb < len(bl) && bl[jb] >= '0' && bl[jb] <= '9' {
				jb++
			}
			na := strings.TrimLeft(al[i:ia], "0")
			nb := strings.TrimLeft(bl[j:jb], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			i, j = ia, jb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(al)-i < len(bl)-j
}
