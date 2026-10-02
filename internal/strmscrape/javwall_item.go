package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/strm"
)

// 番号海报墙的**单品**：定位（路径安全）、读（编辑器要的数据）、写（nfo / poster）。

// javMediaKind 与 domain.StrmMediaKindJav 同值；这里再写一次是为了让本包不依赖
// domain 的那个常量名（拼错的代价由参数校验那条用例兜着）。
const javMediaKind = "jav"

var (
	errNotJavTask = domain.Errorf(domain.CodeValidation, "该任务不是番号影片任务，请在海报墙上切换任务")
	errBadJavPath = domain.Errorf(domain.CodeValidation, "非法路径")
)

// javTitleEntry 是 nfo 的内存缓存条目（键 = nfo 绝对路径）。
//
// 名字还叫 title，但装的其实是「列卡片 + 排序」要用的**整组**字段（见 JavNFOInfo）——
// 改名的收益抵不上满仓 diff，函数注释里点明即可。
//
// 日期必须在**这一层**缓存：排序发生在读标题之前（listJavWall 里 sortJavWallRows
// 先跑），那时 row 上就得有值。nfo 变过（mod/size 不一致）时整条作废重解，
// 所以用户手改 nfo 里的日期也会跟着更新。
type javTitleEntry struct {
	mod  time.Time
	size int64
	info emby.JavNFOInfo
}

// javItemRef 是把 (rel_dir, stem) 收敛成的一组绝对路径。
type javItemRef struct {
	Task     *domain.StrmTask
	Root     string
	AbsDir   string
	RelDir   string // 规范化后的相对目录（"/" 分隔，可为空）
	Stem     string // **磁盘上的**原样主干
	StrmName string // 磁盘上的 .strm 文件名
	Flat     bool   // 现算，绝不采信客户端
	Names    emby.Names
}

// resolveJavItem 定位一部片。三层闸门，缺一不可：
//
//  1. relDir / stem 不接受绝对路径、分隔符、`..`（stem 允许首尾空格 —— 生成器的
//     SafeStem 刻意保留它）；
//  2. `isInside(root, absDir)` —— 与 ResolvePosterFile 同一条判据；
//  3. **锚点**：`<absDir>/<主干>.strm` 必须真的存在。没有这一条，stem 就是任意文件名；
//     有了它，能寻址的文件永不出现在「番号生成器会写的那一套」之外。
//
// ⚠️ 返回的 Stem 是**磁盘上的那一份**（忽略大小写比对后取磁盘名）：Windows 上
// `ssis-001` 能配到 `SSIS-001.strm`，照请求写就会多出一个 `ssis-001.nfo`；
// Linux 部署上那就是两份文件。
func (s *Service) resolveJavItem(ctx context.Context, taskID int64, relDir, stem string) (javItemRef, error) {
	var ref javItemRef
	task, root, err := s.resolveTask(ctx, taskID)
	if err != nil {
		return ref, err
	}
	if task.MediaKind != javMediaKind {
		return ref, errNotJavTask
	}
	rel := normalizeJavRelDir(relDir)
	if rel == "" && strings.TrimSpace(relDir) != "" {
		return ref, errBadJavPath
	}
	stem = strings.TrimSpace(stem)
	if stem == "" || strings.ContainsAny(stem, `/\`) || stem == "." || stem == ".." {
		return ref, errBadJavPath
	}

	absDir := root
	if rel != "" {
		absDir = filepath.Join(root, filepath.FromSlash(rel))
	}
	if !isInside(root, absDir) {
		return ref, errBadJavPath
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return ref, domain.Errorf(domain.CodeNotFound, "目录不存在：%s", rel)
	}

	// 锚点 + 取磁盘上的真实主干。
	strmName := ""
	flat := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			continue
		}
		flat++
		if strings.EqualFold(strm.MediaStem(e.Name()), stem) {
			strmName = e.Name()
		}
	}
	if strmName == "" {
		return ref, domain.Errorf(domain.CodeNotFound, "这一层没有名为 %s 的 .strm", stem)
	}
	ref = javItemRef{
		Task:     task,
		Root:     root,
		AbsDir:   absDir,
		RelDir:   rel,
		Stem:     strm.MediaStem(strmName),
		StrmName: strmName,
		Flat:     flat > 1,
	}
	ref.Names = emby.TargetNames(ref.Stem, ref.Flat)
	return ref, nil
}

// normalizeJavRelDir 归一化相对目录：统一成 `/`、拒绝 `..` 与绝对路径。
// 返回空串表示"根目录"；返回空串但原文非空表示非法。
func normalizeJavRelDir(relDir string) string {
	rel := strings.TrimSpace(strings.ReplaceAll(relDir, `\`, "/"))
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return ""
	}
	if strings.HasPrefix(rel, ":") {
		return ""
	}
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return ""
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// javWallImageURL 拼图片地址（走既有的 /poster 端点：它已经有路径安全与扩展名白名单）。
//
// **末尾那个 `&v=<mtime>` 是必须的**：那个响应带 `Cache-Control: private, max-age=3600`，
// 而保存裁剪**只改内容、不改路径** —— 不换 URL 的话浏览器会一直端出缓存里那张旧图，
// 用户看到的是「提示保存成功了、图还是旧的」，像极了没生效。实测撞见过。
//
// **`&w=<宽度>` 是给服务端按需缩放的**（见 internal/thumbcache）。海报墙上一张卡
// 实际只有 140~260 CSS 像素宽，而磁盘上的 poster/thumb 是 376×538 起步 ——
// 原样发出去实测首屏 30 个请求 2.1 MB、最后一张要 2.7~4.5 秒才画完。
//
// 宽度按**视图**给两档，而不是让前端自己算：那个值随视口变化，会让同一张图在
// 不同窗口宽度下各存一份缓存；两档固定值既够清晰，缓存条目数也可预测。
//
//   - 海报视图（2:3）卡片 141~250 px 宽 → 取 400，2 倍屏够用；
//   - 缩略图视图（3:2）5 列、宽屏一张约 300 px → 取 600。
//
// ⚠️ **两档都刻意大于卡片宽度，不是浪费**：取「刚好等于卡片」的宽度时，
// 磁盘上那些 197×282 / 275×394 的小图会因为 `w >= 原图宽` 走 thumbcache 里
// 「不放大」那条路，结果是**一张都没缩**（实测本地样本 100% 命中这条路）。
// 取得比原图宽之后，服务端看到的是「原图 > 目标宽」→ 真的缩，省下的是
// 「原图尺寸 vs 卡片尺寸」那部分，与卡片样式变不变无关。
func javWallImageURL(taskID int64, relDir, fileName, rev string, view string) string {
	// 没有文件名就没有图。生产路径上调用方已经用 pickJavArtifact 判过一次，
	// 但这里再兜一道：少了它，joinRel 会把「目录 + 斜杠」拼成一个看着像路径、
	// 实际指不到任何文件的地址，请求回来是 404 —— 而卡片上表现为一块占位图，
	// 看不出是拼错了还是真没图。
	if strings.TrimSpace(fileName) == "" {
		return ""
	}
	url := posterURLFromRel(taskID, joinRel(relDir, fileName))
	if url == "" {
		return ""
	}
	url += "&w=" + strconv.Itoa(javWallImageWidth(view))
	if rev != "" {
		url += "&v=" + rev
	}
	return url
}

// javWallImageWidth 是两种视图各自的请求宽度。
func javWallImageWidth(view string) int {
	if view == "thumb" {
		return 600
	}
	return 400
}

// cachedJavTitle 只在缓存命中且文件没变时返回。
func (s *Service) cachedJavTitle(absPath string) (javTitleEntry, bool) {
	info, err := os.Stat(absPath)
	if err != nil {
		return javTitleEntry{}, false
	}
	s.javTitleMu.Lock()
	defer s.javTitleMu.Unlock()
	e, ok := s.javTitleCache[absPath]
	if !ok || !e.mod.Equal(info.ModTime()) || e.size != info.Size() {
		return javTitleEntry{}, false
	}
	return e, true
}

// javNFOInfoOf 读一份 nfo 的「标题 / 番号 / 两个日期」（带缓存）。
//
// 读不到（文件不在、不是 <movie> 结构）返回零值 —— 调用方按「没有」处理，
// 卡片上回落到主干、排序键为空。
func (s *Service) javNFOInfoOf(absPath string) emby.JavNFOInfo {
	info, err := os.Stat(absPath)
	if err != nil {
		return emby.JavNFOInfo{}
	}
	s.javTitleMu.Lock()
	if e, ok := s.javTitleCache[absPath]; ok && e.mod.Equal(info.ModTime()) && e.size == info.Size() {
		s.javTitleMu.Unlock()
		return e.info
	}
	s.javTitleMu.Unlock()

	data, err := os.ReadFile(absPath)
	if err != nil {
		return emby.JavNFOInfo{}
	}
	parsed, err := emby.TitleNumberDates(data)
	if err != nil {
		return emby.JavNFOInfo{}
	}
	s.javTitleMu.Lock()
	s.javTitleCache[absPath] = javTitleEntry{mod: info.ModTime(), size: info.Size(), info: parsed}
	s.javTitleMu.Unlock()
	return parsed
}

// javTitleLookup 是给列表用的「读一份 nfo」函数（带缓存 + 并发）。
type javTitleLookup func(row javWallRow) emby.JavNFOInfo

// newJavTitleLookup 返回一个带并发预热的 nfo 读取器：先把这一页要用的都读出来
// （8 个 worker），再逐行取。冷启动几千份 nfo 时差别明显。
//
// nfo 名与生成器同源（`emby.TargetNames`）：**不要**在这里另拼一个名字，
// 平铺/独占翻转时那会指向一个不存在的文件（卡片上的标题全变成主干）。
func (s *Service) newJavTitleLookup(rows []javWallRow) javTitleLookup {
	var (
		mu     sync.Mutex
		loaded = map[string]emby.JavNFOInfo{}
		wg     sync.WaitGroup
	)
	sem := make(chan struct{}, 8)
	pathOf := func(row javWallRow) string {
		return filepath.Join(row.absDir, emby.TargetNames(row.item.Stem, row.item.Flat).NFO)
	}
	for _, row := range rows {
		path := pathOf(row)
		if _, ok := s.cachedJavTitle(path); ok {
			continue // 缓存里已有，不必占一个 worker
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			info := s.javNFOInfoOf(p)
			mu.Lock()
			loaded[p] = info
			mu.Unlock()
		}(path)
	}
	wg.Wait()
	return func(row javWallRow) emby.JavNFOInfo {
		path := pathOf(row)
		mu.Lock()
		if v, ok := loaded[path]; ok {
			mu.Unlock()
			return v
		}
		mu.Unlock()
		return s.javNFOInfoOf(path)
	}
}

// stripJavNumberPrefix 与 emby 的读侧同一套：只剥「番号 + 空格」那个前缀。
func stripJavNumberPrefix(number, title string) string {
	number = strings.TrimSpace(number)
	title = strings.TrimSpace(title)
	if number == "" {
		return title
	}
	prefix := number + " "
	if len(title) > len(prefix) && strings.EqualFold(title[:len(prefix)], prefix) {
		return title[len(prefix):]
	}
	return title
}

func strconvI64(v int64) string { return strconv.FormatInt(v, 10) }
