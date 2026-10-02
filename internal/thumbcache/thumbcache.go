// Package thumbcache 给海报墙这类「一屏几十张图」的页面按需缩放图片，并把结果落到磁盘缓存。
//
// # 为什么需要它
//
// 番号海报墙一页 50 张卡，图直接从磁盘原样吐出去：实测一次首屏要 30 个请求、
// 2.1 MB，按 0.6~1.1 秒一张算，最后一张要 2.7~4.5 秒才画完。而卡片上的图其实
// 只有 173~259 CSS 像素宽（宽屏缩略图视图还不到 140），原图 376×538 足足大了一倍多。
//
// 缩到实际显示尺寸再编码成 JPEG，体积能掉七成多，而且**视觉上看不出差别**
// （缩放在服务端做，浏览器解码的像素也少了）。
//
// # 两条边界
//
//  1. **不放大**。目标宽度 >= 原图宽度时直接返回原字节 —— 放大只会糊，
//     而且让缓存里多出一份比原图还大的文件。
//  2. **失败就回原图**。解码不了、编码失败、磁盘写不进去，一律返回原字节。
//     缩略图是「锦上添花」，绝不能因为缓存目录没权限就让整面墙的图全挂掉。
//     这条与 emby.BuildPoster 的 best-effort 是同一个取舍。
//
// # 缓存怎么失效
//
// 键里带上源文件的 **大小 + mtime**：海报被「重刮」或用户保存裁剪后文件会变，
// 键随之改变，旧的那份自然不再被命中（不必主动清理，也绝不会读到过期图）。
// 同样的键永远不会产生两张不同的图，所以命中即正确。
package thumbcache

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/draw"
)

// 缩放后的 JPEG 质量。
//
// 88 是量出来的折中：卡片图最终显示不到 300 px 宽，88 与 92 在这种尺寸上肉眼
// 分不出，但体积差近两成。比海报生成用的 90 略低是有意的 —— 那是要落进媒体库
// 给 Emby 读的成品，这是给网页滚动看的中间产物。
const jpegQuality = 88

// 磁盘缓存里最多保留多少个子目录（每张图一个子目录，见 cachePath）。
//
// 上限存在的意义只是「别无限长」：一次全量重刮会换掉所有键，旧的那些再也不会
// 被读到。清理是**机会式**的（写入时顺手看一眼），没有后台定时器 —— 这个量级
// （每张几十 KB）不值得为它起一个 goroutine。
const maxEntries = 4000

// Cache 是一个磁盘缩略图缓存。零值不可用，用 New 构造。
type Cache struct {
	dir string
	log func(string, ...any)

	mu      sync.Mutex
	entries int
}

// New 在 dataDir 下开一个缓存目录。dataDir 为空时缓存降级为「只缩不存」——
// 仍然省流量，只是每次都重算一遍。
func New(dataDir string, logf func(string, ...any)) *Cache {
	c := &Cache{log: logf}
	if dataDir == "" {
		return c
	}
	c.dir = filepath.Join(dataDir, "thumbcache")
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		// 建不出来就退化成不落盘：不影响任何正确性，只是每次都重算。
		if c.log != nil {
			c.log("缩略图缓存目录创建失败，本次降级为不落盘", "dir", c.dir, "err", err)
		}
		c.dir = ""
		return c
	}
	if n, err := countSubdirs(c.dir); err == nil {
		c.entries = n
	}
	return c
}

// Get 返回可以被直接写进 HTTP 响应的 JPEG 字节。
//
// srcPath 是磁盘上的原图；width 是「想要多宽」（调用方按卡片实际显示尺寸给）。
// 任何一步出问题都回落到原图字节 —— 调用方不需要处理错误分支，它总能拿到一张图。
func (c *Cache) Get(srcPath string, width int) ([]byte, string) {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, ""
	}
	ext := filepath.Ext(srcPath)
	if width <= 0 {
		return raw, contentType(ext)
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return raw, contentType(ext)
	}

	key := cacheKey(srcPath, info.Size(), info.ModTime().UnixNano(), width)
	if hit, ok := c.read(key); ok {
		return hit, "image/jpeg"
	}

	scaled, ok := downscaleJPEG(raw, width)
	if !ok {
		// 解不开图（不是 jpeg/png、或是坏文件）→ 原样给出去。
		// 浏览器至少还能试一次，比返回错误强。
		return raw, contentType(ext)
	}
	c.write(key, scaled)
	return scaled, "image/jpeg"
}

// read 取磁盘缓存；未命中返回 false。
func (c *Cache) read(key string) ([]byte, bool) {
	if c.dir == "" {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(c.dir, key, "thumb.jpg"))
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return b, true
}

// write 落盘缓存。失败只记日志 —— 缓存写不进去不该影响这次响应。
func (c *Cache) write(key string, data []byte) {
	if c.dir == "" {
		return
	}
	sub := filepath.Join(c.dir, key)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		c.warn("缩略图缓存写入失败", err)
		return
	}
	// 先写临时文件再改名：并发请求同一张图时，读到的要么是完整文件、要么没有，
	// 不会读到写了一半的 JPEG（那种图会在浏览器里显示成下半截灰）。
	tmp := filepath.Join(sub, fmt.Sprintf("thumb-%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		c.warn("缩略图缓存写入失败", err)
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, filepath.Join(sub, "thumb.jpg")); err != nil {
		c.warn("缩略图缓存改名失败", err)
		_ = os.Remove(tmp)
		return
	}
	c.mu.Lock()
	c.entries++
	over := c.entries > maxEntries
	c.mu.Unlock()
	if over {
		c.prune()
	}
}

// prune 机会式清理：超过上限时把整个目录清空重来。
//
// 「全清」而不是「按时间淘汰最旧的一批」：键是按源文件内容算的，缓存几乎全是
// 「当前这批图的缩放结果」，而一次全量重刮就会把整批键换掉 —— 逐个判新旧要
// 读几千个文件的时间戳，不如直接清掉让下次重建。这个量级（几千张 × 几十 KB、
// 重建只要几十毫秒一张）不值得更精细的策略。
func (c *Cache) prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(c.dir, e.Name()))
	}
	c.mu.Lock()
	c.entries = 0
	c.mu.Unlock()
}

func (c *Cache) warn(msg string, err error) {
	if c.log != nil {
		c.log(msg, "err", err)
	}
}

// downscaleJPEG 把 raw 缩到不超过 width 宽（保持比例），编码成 JPEG。
// 返回 false 表示解不开或编不出来，调用方应回落原图。
func downscaleJPEG(raw []byte, width int) ([]byte, bool) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, false
	}
	if b.Dx() <= width {
		// 原图本来就比目标窄：不放大（见包注释）。
		return nil, false
	}
	height := b.Dy() * width / b.Dx()
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	// CatmullRom 而不是双线性：海报上的字（番号、角标）在缩小时最容易糊，
	// CatmullRom 的锐度明显更好，而这点尺寸上的耗时差可以忽略（每张几毫秒）。
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// cacheKey 由「源路径 + 大小 + mtime + 目标宽度」算出。
//
// 带上大小与 mtime 是为了**自动失效**：海报被重刮或用户保存裁剪后文件变了，
// 键就变了，旧缓存不会再被命中。这比「存一条映射再逐个判断有没有过期」简单得多，
// 而且永远不会读到过期图（同样的键一定对应同样的内容）。
func cacheKey(path string, size, mtime int64, width int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", path, size, mtime, width)))
	return hex.EncodeToString(sum[:])
}

// contentType 给原图字节判类型。缓存命中时一律是 image/jpeg（我们自己编的），
// 这条路只在回落原图时用到。
func contentType(ext string) string {
	switch filepath.Ext(ext) {
	case ".png", ".PNG":
		return "image/png"
	case ".webp", ".WEBP":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// countSubdirs 数缓存目录下现有的子目录数（进程重启后接着算）。
func countSubdirs(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n, nil
}
