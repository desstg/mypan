package thumbcache

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeJPEG 造一张 w×h 的纯色 JPEG。纯色压缩率极高，所以这里不拿它断言「体积变小」，
// 只用来断言尺寸与格式；体积那件事按「解码后的像素尺寸」判，与压缩率无关。
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 加点渐变，免得整张纯色被压成一个字节，看不出编码是否真的跑了。
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatalf("造图失败: %v", err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	return p
}

func decodeSize(t *testing.T, raw []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("解不出尺寸: %v", err)
	}
	return cfg.Width, cfg.Height
}

// 缩放真的发生了：输出宽度等于请求宽度，比例保持不变。
func TestGetScalesDownKeepingRatio(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "poster.jpg", makeJPEG(t, 376, 538))

	c := New(t.TempDir(), nil)
	got, ctype := c.Get(src, 200)
	if ctype != "image/jpeg" {
		t.Fatalf("content-type = %q", ctype)
	}
	w, h := decodeSize(t, got)
	if w != 200 {
		t.Fatalf("宽度 = %d，want 200", w)
	}
	// 538 * 200 / 376 = 286.2 → 286（整数除）
	if h != 286 {
		t.Fatalf("高度 = %d，want 286（比例要保持）", h)
	}
}

// **不放大**：目标宽度 >= 原图宽度时原样返回。
//
// 这条不只是省事 —— 放大既糊又会往缓存里塞一份比原图还大的文件。
func TestGetDoesNotUpscale(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "poster.jpg", makeJPEG(t, 200, 300))

	c := New(t.TempDir(), nil)
	got, _ := c.Get(src, 500)
	w, h := decodeSize(t, got)
	if w != 200 || h != 300 {
		t.Fatalf("不该放大，得到 %dx%d", w, h)
	}
}

// 同一张图第二次取走磁盘缓存，且内容与第一次逐字节相同。
func TestGetCachesOnDisk(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "poster.jpg", makeJPEG(t, 376, 538))
	cacheDir := t.TempDir()

	c := New(cacheDir, nil)
	first, _ := c.Get(src, 180)
	second, _ := c.Get(src, 180)
	if !bytes.Equal(first, second) {
		t.Fatal("两次结果不一致（缓存命中的必须是同一个字节串）")
	}
	// 缓存目录下应当有且只有一个条目。**注意数的是 <cacheDir>/thumbcache 里面**，
	// 不是 cacheDir 本身 —— 后者只会数到 thumbcache 这一个目录名，
	// 于是「一个条目都没有」也会通过（这个假通过我踩过一次）。
	sub := countCacheEntries(t, cacheDir)
	if sub != 1 {
		t.Fatalf("缓存条目数 = %d，want 1", sub)
	}
}

// countCacheEntries 数缓存目录下真正存了多少张图。
func countCacheEntries(t *testing.T, cacheDir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cacheDir, "thumbcache"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// 源文件变了（重刮 / 用户保存了裁剪），键要跟着变 —— 绝不能读到过期图。
func TestCacheInvalidatesWhenSourceChanges(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "poster.jpg", makeJPEG(t, 376, 538))
	c := New(t.TempDir(), nil)

	before, _ := c.Get(src, 180)

	// 换一张内容与尺寸都不同的（同时改大小与 mtime，模拟真实的重写）。
	writeFile(t, dir, "poster.jpg", makeJPEG(t, 600, 900))
	after, _ := c.Get(src, 180)

	if bytes.Equal(before, after) {
		t.Fatal("源文件变了却命中了旧缓存")
	}
	w, h := decodeSize(t, after)
	if w != 180 || h != 270 {
		t.Fatalf("新图缩放结果 = %dx%d，want 180x270", w, h)
	}
}

// 解不开的图**原样返回**，不报错、不产生缓存条目。
//
// 这条是「失败就回原图」那条边界的钉子：海报墙上哪怕混进一张坏图，
// 也只是那一张显示不出来，不会把整面墙拖垮。
func TestGetFallsBackToOriginalOnUndecodable(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("这不是一张图")
	src := writeFile(t, dir, "broken.jpg", raw)
	cacheDir := t.TempDir()

	c := New(cacheDir, nil)
	got, ctype := c.Get(src, 100)
	if !bytes.Equal(got, raw) {
		t.Fatal("解不开的图应当原样返回")
	}
	if ctype != "image/jpeg" {
		t.Fatalf("content-type = %q", ctype)
	}
	if entries := countCacheEntries(t, cacheDir); entries != 0 {
		t.Fatalf("坏图不该产生缓存条目，实得 %d 个", entries)
	}
}

// PNG 也要能缩（海报来源不保证都是 jpg），且命中的是 JPEG 缓存。
func TestGetScalesPNG(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: 200, B: uint8(y % 255), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	src := writeFile(t, dir, "poster.png", buf.Bytes())

	c := New(t.TempDir(), nil)
	got, ctype := c.Get(src, 200)
	if ctype != "image/jpeg" {
		t.Fatalf("缩放结果应当是 JPEG，得到 %q", ctype)
	}
	if w, h := decodeSize(t, got); w != 200 || h != 300 {
		t.Fatalf("得到 %dx%d，want 200x300", w, h)
	}
}

// dataDir 为空（或建不出来）时降级成「只缩不存」，功能不受影响。
func TestCacheWorksWithoutDataDir(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "poster.jpg", makeJPEG(t, 376, 538))

	c := New("", nil)
	got, _ := c.Get(src, 150)
	if w, _ := decodeSize(t, got); w != 150 {
		t.Fatalf("无缓存目录时仍应缩放，得到宽度 %d", w)
	}
}
