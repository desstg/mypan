package strmscrape

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
)

// 这一组用例钉住「用户自己上传的演员头像」这条链的关键约束。
//
// 设计上的三条硬要求（任何一条退回去都会变成用户能看见的故障）：
//  1. **落盘在服务端**、与原文件 / 原设备无关 —— 用户换手机、清空相册之后还要能看见；
//  2. **解不开的字节绝不落盘** —— 否则界面上是一张裂图，而用户以为传成功了；
//  3. **没有自传头像时行为完全不变** —— 这条链不能污染侧车 / 资料层那条老路。

func newAvatarService(t *testing.T) *Service {
	t.Helper()
	return &Service{dataDir: t.TempDir()}
}

// solidPNG 造一张指定尺寸的纯色 PNG。
func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 120, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("造 PNG 失败：%v", err)
	}
	return buf.Bytes()
}

// 1. 存一张大图 → 落盘是 JPEG、尺寸被压到上限内、路径命中、URL 带 v。
func TestSaveJavActorAvatarDownscalesAndStores(t *testing.T) {
	svc := newAvatarService(t)
	key := "z0GW"

	if got := svc.actorAvatarURL(key); got != "" {
		t.Fatalf("上传之前不该有自定义头像，实际 %q", got)
	}
	out, err := svc.SaveJavActorAvatar(context.Background(), key, solidPNG(t, 1000, 1500))
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if out == nil || out.Key != key {
		t.Fatalf("返回不对：%+v", out)
	}
	if !strings.HasPrefix(out.AvatarURL, "/api/admin/strm-scrape/jav-wall/actor/avatar?") {
		t.Fatalf("返回的地址必须是服务端地址，实际 %q", out.AvatarURL)
	}
	if !strings.Contains(out.AvatarURL, "v=") {
		t.Fatalf("地址里必须带 mtime（换图后不吃旧缓存），实际 %q", out.AvatarURL)
	}

	path, ok := svc.actorAvatarPath(key)
	if !ok {
		t.Fatal("落盘之后 actorAvatarPath 应该命中")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回落盘文件失败：%v", err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("落盘内容不是可解码的图片：%v", err)
	}
	if format != "jpeg" {
		t.Fatalf("落盘格式应为 jpeg，实际 %q", format)
	}
	b := img.Bounds()
	// 1000×1500 等比缩到宽 480 → 高 720（正好两档都到上限）。
	if b.Dx() > javActorAvatarMaxW || b.Dy() > javActorAvatarMaxH {
		t.Fatalf("尺寸没压到上限内：%dx%d", b.Dx(), b.Dy())
	}
	// 保持比例（1000:1500 = 2:3）。
	if got, want := b.Dx()*3, b.Dy()*2; got < want-3 || got > want+3 {
		t.Fatalf("比例被改坏了：%dx%d", b.Dx(), b.Dy())
	}
}

// 小图**不放大**（放大只会糊，且文件更大）。
func TestSaveJavActorAvatarNeverUpscales(t *testing.T) {
	svc := newAvatarService(t)
	key := "small"
	if _, err := svc.SaveJavActorAvatar(context.Background(), key, solidPNG(t, 90, 122)); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	path, _ := svc.actorAvatarPath(key)
	data, _ := os.ReadFile(path)
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if b := img.Bounds(); b.Dx() != 90 || b.Dy() != 122 {
		t.Fatalf("小图不该被放大，实际 %dx%d", b.Dx(), b.Dy())
	}
}

// 2. 删掉之后路径不再命中、URL 回空（前端据此回到侧车 / 资料层那张）。
// 删除是幂等的：本来就没有也不报错。
func TestDeleteJavActorAvatar(t *testing.T) {
	svc := newAvatarService(t)
	key := "K4gKv"
	if _, err := svc.SaveJavActorAvatar(context.Background(), key, solidPNG(t, 300, 400)); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if _, ok := svc.actorAvatarPath(key); !ok {
		t.Fatal("刚存完应该命中")
	}
	out := svc.DeleteJavActorAvatar(key)
	if out == nil || out.AvatarURL != "" {
		t.Fatalf("删除后不该返回地址，实际 %+v", out)
	}
	if _, ok := svc.actorAvatarPath(key); ok {
		t.Fatal("删除之后不该再命中")
	}
	if got := svc.actorAvatarURL(key); got != "" {
		t.Fatalf("删除之后 URL 应为空，实际 %q", got)
	}
	// 幂等：再删一次不 panic 也不报错。
	if out := svc.DeleteJavActorAvatar(key); out == nil {
		t.Fatal("重复删除也要返回结果")
	}
}

// 3. 非图片字节 → VALIDATION，**且目录里不留文件**（半成品不能落盘）。
func TestSaveJavActorAvatarRejectsNonImage(t *testing.T) {
	svc := newAvatarService(t)
	key := "bad"
	_, err := svc.SaveJavActorAvatar(context.Background(), key, []byte("this is not an image, just a text file"))
	if err == nil {
		t.Fatal("非图片字节应被拒绝")
	}
	var de *domain.AppError
	if !errors.As(err, &de) || de.Code != domain.CodeValidation {
		t.Fatalf("应返回 VALIDATION，实际 %v", err)
	}
	if _, ok := svc.actorAvatarPath(key); ok {
		t.Fatal("校验失败时不该留下任何文件")
	}
	// 目录可能被建出来了，但里面必须是空的（不能有半张图 / 临时文件残留）。
	entries, _ := os.ReadDir(svc.javActorAvatarDir())
	if len(entries) != 0 {
		t.Fatalf("校验失败后目录应为空，实际 %d 项", len(entries))
	}
}

// 4. key 里带 `../` 与 `/` 也不能逃出 data 目录。
func TestJavActorAvatarPathStaysInDataDir(t *testing.T) {
	svc := newAvatarService(t)
	dir := svc.javActorAvatarDir()
	for _, key := range []string{
		"name:../../etc/passwd",
		"a/b/c",
		"..",
		"name:楓ふうあ",
		"../../../../Windows/System32/cmd",
	} {
		path, ok := javActorAvatarFile(dir, key)
		if !ok {
			t.Fatalf("key=%q 应该能算出路径", key)
		}
		clean := filepath.Clean(path)
		if !strings.HasPrefix(clean, filepath.Clean(dir)+string(os.PathSeparator)) {
			t.Fatalf("key=%q 逃出了数据目录：%s", key, clean)
		}
		if strings.ContainsAny(filepath.Base(clean), `\/`) {
			t.Fatalf("key=%q 算出的文件名里还有分隔符：%s", key, clean)
		}
	}
	// 真的存一次那个最凶的 key，断言落点仍在目录里。
	if _, err := svc.SaveJavActorAvatar(context.Background(), "name:../../etc/passwd", solidPNG(t, 100, 100)); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	path, ok := svc.actorAvatarPath("name:../../etc/passwd")
	if !ok {
		t.Fatal("带诡异 key 的头像应能读回")
	}
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(os.PathSeparator)) {
		t.Fatalf("落点逃出数据目录：%s", path)
	}
}

// 5. 超过上限的输入被拒（这条闸在 API 层还有一道，这里只管服务层）。
func TestSaveJavActorAvatarRejectsOversize(t *testing.T) {
	svc := newAvatarService(t)
	oversize := make([]byte, JavActorAvatarMaxBytes+1)
	_, err := svc.SaveJavActorAvatar(context.Background(), "big", oversize)
	if err == nil {
		t.Fatal("超限的输入应被拒绝")
	}
	var de *domain.AppError
	if !errors.As(err, &de) || de.Code != domain.CodeValidation {
		t.Fatalf("应返回 VALIDATION，实际 %v", err)
	}
	if _, ok := svc.actorAvatarPath("big"); ok {
		t.Fatal("超限时不该落盘")
	}
}

// 6. 没有自传头像时覆盖是**空操作** —— 老那条路（侧车 / 索引表拼的图）行为不变。
func TestApplyCustomAvatarsIsNoopWithoutUpload(t *testing.T) {
	svc := newAvatarService(t)
	cards := []JavWallActorCard{
		{Key: "z0GW", Name: "A", AvatarURL: "https://c0.jdbstatic.com/avatars/z0/z0GW.jpg"},
		{Key: "name:某人", Name: "B"},
	}
	svc.applyCustomAvatars(cards)
	if cards[0].AvatarURL != "https://c0.jdbstatic.com/avatars/z0/z0GW.jpg" {
		t.Fatalf("没有自传头像时不该改地址，实际 %q", cards[0].AvatarURL)
	}
	if cards[0].CustomAvatar || cards[1].CustomAvatar {
		t.Fatal("没有自传头像时不该打 CustomAvatar 标记")
	}

	// 存一张之后，只有那一位被覆盖，其他人原样。
	if _, err := svc.SaveJavActorAvatar(context.Background(), "z0GW", solidPNG(t, 200, 200)); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	svc.applyCustomAvatars(cards)
	if !cards[0].CustomAvatar || !strings.HasPrefix(cards[0].AvatarURL, "/api/") {
		t.Fatalf("有自传头像的那位应被换成服务端地址，实际 %+v", cards[0])
	}
	if cards[1].AvatarURL != "" || cards[1].CustomAvatar {
		t.Fatalf("没有自传头像的那位应原样，实际 %+v", cards[1])
	}
}

// 7. **跨实例持久性**：头像在磁盘上，不是进程内状态。
//
// 这是用户那条要求的直接对照：「上传后不依赖电脑、手机上的这个原文件」——
// 服务重启（= 新建一个 Service 实例）之后必须还在。
func TestCustomAvatarSurvivesServiceRestart(t *testing.T) {
	dataDir := t.TempDir()
	first := &Service{dataDir: dataDir}
	key := "restart"
	out, err := first.SaveJavActorAvatar(context.Background(), key, solidPNG(t, 400, 600))
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if out.AvatarURL == "" {
		t.Fatal("保存后应有地址")
	}

	// 「重启」：同一个 DataDir，全新的 Service。
	second := &Service{dataDir: dataDir}
	path, ok := second.actorAvatarPath(key)
	if !ok {
		t.Fatal("新建实例后应仍能命中（说明是落盘的，不是进程内状态）")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件应该真的在磁盘上：%v", err)
	}
	if got := second.actorAvatarURL(key); got == "" {
		t.Fatal("新建实例后应仍能拼出地址")
	}
}

// 换一张图之后地址必须变（`v` 带 mtime），否则浏览器会给旧缓存。
func TestActorAvatarURLChangesAfterReplace(t *testing.T) {
	svc := newAvatarService(t)
	key := "swap"
	if _, err := svc.SaveJavActorAvatar(context.Background(), key, solidPNG(t, 200, 200)); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	first := svc.actorAvatarURL(key)
	// 把 mtime 明确往前推一天，避开「同一秒内两次写入 mtime 相同」的抖动。
	path, _ := svc.actorAvatarPath(key)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 失败：%v", err)
	}
	later := info.ModTime().Add(24 * time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("改 mtime 失败：%v", err)
	}
	second := svc.actorAvatarURL(key)
	if first == second {
		t.Fatalf("mtime 变了地址也该变（否则会读到旧缓存）：%q", first)
	}
}

// 落盘的就是规范尺寸：一张 4:3 的图进来，两个方向都不超上限。
func TestFitActorAvatarSize(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{1000, 1500, 480, 720}, // 两档都到上限
		{4000, 3000, 480, 360}, // 宽先到上限
		{600, 2000, 216, 720},  // 高先到上限
		{90, 122, 90, 122},     // 不放大
		{10, 10, 10, 10},       // 不动
		{0, 0, 0, 0},           // 无效
	}
	for _, c := range cases {
		got := fitActorAvatarSize(c.w, c.h)
		if got.Dx() != c.wantW || got.Dy() != c.wantH {
			t.Fatalf("%dx%d 应缩成 %dx%d，实际 %dx%d", c.w, c.h, c.wantW, c.wantH, got.Dx(), got.Dy())
		}
	}
}

// jpeg 的输出确实是 JPEG（防止有人把编码器换成 png 却不改断言）。
func TestNormalizeActorAvatarEmitsJPEG(t *testing.T) {
	data, err := normalizeActorAvatar(solidPNG(t, 300, 400))
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("产出不是 JPEG：%v", err)
	}
}
