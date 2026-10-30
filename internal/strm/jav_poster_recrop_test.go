package strm

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
)

// noisyJPEG 造一张**每一列的灰度都不一样**的横图（800x538，与 JAVDB 真实封面同尺寸）。
//
// 为什么不能用 sampleJPEG 那种 `x%256` 渐变，也不能用「左右两半图案不同」：
// 反推窗口（MatchPosterRect）靠逐列灰度找最像的那一段。渐变图里每一段都长得差不多；
// 而「右半是横条纹」更糟 —— 那半边的**每一列灰度完全相同**，反推会给出一个
// 差 20 多像素的答案（实测），用例看起来红、其实是测法不成立。
//
// 所以这里让**列均值随列号变化**（`x*13%200`），行方向只加一点点抖动。
func noisyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := (x*13)%200 + 20
			// 行方向的 ±2 抖动：让图不像「纯竖直色带」，但不影响列均值。
			if (y/xorShift(x))%2 == 0 {
				v += 2
			}
			img.Set(x, y, color.RGBA{uint8(v), uint8(v), uint8(v), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// xorShift 给每一列一个伪随机数（避免 v%2 在列方向算出规律条纹）。
func xorShift(x int) int {
	s := x*2654435761 + 1
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	if s < 0 {
		s = -s
	}
	return s%7 + 1
}

// TestPosterNeedsRecrop 钉住「已经在那儿的 poster 什么时候该重裁」。
//
// 这是 2026-10-30 用户报的那条：「好多张，裁剪的 poster 图也不是 thumb 图的右边，
// 本来应该是影片是有码分类的话，poster 图是裁剪 thumb 的右边」。
//
// 根因是**存量侧车当年没写 `type`**，海报按人脸档裁过一版；补上字段之后扫描那条路
// 的判据仍然是「poster 在就跳过」—— 永远不看第二眼。修法是「刚补上 type 那一刻」
// 核一次：按新属性算的窗口 ≠ 反推出来的窗口才重裁。
func TestPosterNeedsRecrop(t *testing.T) {
	thumbBytes := noisyJPEG(t, 800, 538)
	dir := t.TempDir()
	thumb := filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(thumb, thumbBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// ① 按「无码/人脸」那一档裁出来的海报（存量坏状态），现在属性是有码 → 该重裁。
	faceRect, err := emby.DefaultPosterRect(thumbBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	censoredRect, err := emby.DefaultPosterRect(thumbBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if faceRect.X == censoredRect.X {
		t.Fatalf("这张测试图两档窗口一样（X=%d），用例验不出东西", faceRect.X)
	}
	facePoster, err := emby.CropPoster(thumbBytes, faceRect, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	poster := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(poster, facePoster, 0o644); err != nil {
		t.Fatal(err)
	}
	job := javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true, EnforceCrop: true}
	if !posterNeedsRecrop(job) {
		t.Errorf("人脸档的旧海报在有码片下应当被判为「要重裁」（face X=%d, want X=%d）",
			faceRect.X, censoredRect.X)
	}

	// ② 换成按有码档裁的那一张（正确状态）→ 一个字节都别动。
	goodPoster, err := emby.CropPoster(thumbBytes, censoredRect, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, goodPoster, 0o644); err != nil {
		t.Fatal(err)
	}
	if posterNeedsRecrop(job) {
		got, _ := emby.MatchPosterRect(thumbBytes, goodPoster)
		t.Errorf("已经是右边那一版了，不该重裁（反推 %+v、期望 %+v）—— 手调过的框全靠这条判据保命",
			got, censoredRect)
	}

	// ③ 反推不出来（poster 与 thumb 完全无关）→ **不动**。
	// 这一条是刻意的：那种海报多半与当前 thumb 不是同一代（上游换过封面），
	// 重裁等于用今天的图盖掉用户正看着的那张。
	if err := os.WriteFile(poster, noisyJPEG(t, 300, 300), 0o644); err != nil {
		t.Fatal(err)
	}
	if posterNeedsRecrop(job) {
		t.Error("反推不出窗口时应当保守不动")
	}

	// ④ 没有 thumb → 不动（没得算）。
	if posterNeedsRecrop(javPosterJob{ThumbPath: filepath.Join(dir, "nope.jpg"), PosterPath: poster, Censored: true}) {
		t.Error("没有 thumb 时不该判「要重裁」")
	}
}

// TestSyncSidecarsFromRepoRecropsPosterOnTypeFill 是这条修复的**端到端**那一条：
// 回写补上 `type` 之后，磁盘上那张按人脸档裁错的海报要被换成「thumb 的右边」。
//
// 用户的原始描述（2026-10-30）：「有好多张，裁剪的 poster 图也不是 thumb 图的右边，
// 本来应该是影片是有码分类的话，poster 图是裁剪 thumb 的右边」。
//
// 三段拼起来才成立，缺一段都不行：
//
//	① 侧车回写要把 `type` 补进去（jav.sidecarFieldsFor，上一版已修）
//	② 补进去那一次要报出来（writeFieldsIntoFileReport 的 typeFilled）
//	③ 报了之后要真的核一次海报（enqueuePosterRecropCheck → 队列的 EnforceCrop）
func TestSyncSidecarsFromRepoRecropsPosterOnTypeFill(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "TASK")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const stem = "SSIS-001"
	thumbBytes := noisyJPEG(t, 800, 538)
	if err := os.WriteFile(filepath.Join(taskDir, stem+"-thumb.jpg"), thumbBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// 摆一张**按无码档裁错**的海报（就是线上那 17 份的样子）。
	wrongRect, err := emby.DefaultPosterRect(thumbBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	wrongPoster, err := emby.CropPoster(thumbBytes, wrongRect, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	posterPath := filepath.Join(taskDir, stem+"-poster.jpg")
	if err := os.WriteFile(posterPath, wrongPoster, 0o644); err != nil {
		t.Fatal(err)
	}
	// 侧车：**没有 type**（老侧车），同层只有一个载体 → 平铺布局中的「独占目录」？
	// 这里刻意只放 .strm + 侧车，`countMedia` 算出 1 → 非平铺命名（poster.jpg）。
	if err := os.WriteFile(filepath.Join(taskDir, stem+".strm"), []byte("http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(posterPath, filepath.Join(taskDir, "poster.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(taskDir, stem+"-thumb.jpg"), filepath.Join(taskDir, "thumb.jpg")); err != nil {
		t.Fatal(err)
	}
	sidecar := `{"schema":"litepan.jav.sidecar/1","number":"` + stem + `","title":"t"}`
	if err := os.WriteFile(filepath.Join(taskDir, stem+".json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &Service{
		strmDir: root,
		repo: stubStrmTaskRepo{tasks: []*domain.StrmTask{
			{ID: 1, MediaKind: domain.StrmMediaKindJav, OutputFolder: "TASK"},
		}},
		log:        testLogger(t),
		javPosters: newJavPosterQueue(testLogger(t)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.javPosters.Start(ctx)

	if _, err := svc.SyncSidecarsFromRepo(ctx, func(number string) (map[string]any, bool) {
		return map[string]any{"type": "0", "type_label": "有码"}, true
	}); err != nil {
		t.Fatalf("SyncSidecarsFromRepo: %v", err)
	}
	if !waitForPending(t, svc.javPosters, 5*time.Second) {
		t.Fatal("海报队列没跑完（作业被丢了？）")
	}

	poster, err := os.ReadFile(filepath.Join(taskDir, "poster.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := emby.MatchPosterRect(thumbBytes, poster)
	if !ok {
		t.Fatal("重裁之后反推不出窗口 —— 海报没被换成新版？")
	}
	want, err := emby.DefaultPosterRect(thumbBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != want.X {
		t.Errorf("补上 type 后海报仍然不是右边那一版：got X=%d，期望 %d", got.X, want.X)
	}
}

// waitForPending 等队列里的作业全部处理完。
//
// 不能只看文件 mtime：队列是异步的、而且写入可能**根本没发生**（判据说「本来就对」）。
// 等到 PendingForTest 归零才算这一轮结束。
func waitForPending(t *testing.T, q *javPosterQueue, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if q.PendingForTest() == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return q.PendingForTest() == 0
}

//
// 这是海报重裁的**触发信号**：只有在「刚补上」那一刻核海报，例行回写才可能
// 反复把用户手调过的框换掉。
func TestWriteFieldsIntoFileDetectsTypeFill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "A-1.json")
	base := `{"schema":"` + emby.SupportedSchema + `","number":"A-1","title":"t"}`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	// 第一次：type 从空 → 有值，必须报 true。
	changed, typeFilled, err := writeFieldsIntoFileReport(path, map[string]any{
		"type": "0", "type_label": "有码",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !typeFilled {
		t.Fatalf("首次补 type 应当 changed=true typeFilled=true，got %v %v", changed, typeFilled)
	}

	// 第二次（同一批字段再跑一遍）：什么都不改，也**不能再报 typeFilled** ——
	// 否则每轮回写都会去核海报、把用户手调过的框换掉。
	changed, typeFilled, err = writeFieldsIntoFileReport(path, map[string]any{
		"type": "0", "type_label": "有码",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed || typeFilled {
		t.Fatalf("重复补同一份字段不该再报改动，got changed=%v typeFilled=%v", changed, typeFilled)
	}

	// 侧车里已经有别的值（比如用户手改成 1）时：**只补空**，不覆盖、也不报 typeFilled。
	if err := os.WriteFile(path, []byte(`{"schema":"`+emby.SupportedSchema+`","number":"A-1","type":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, typeFilled, err = writeFieldsIntoFileReport(path, map[string]any{"type": "0"})
	if err != nil {
		t.Fatal(err)
	}
	if changed || typeFilled {
		t.Fatalf("侧车已有 type 时不该动它，got changed=%v typeFilled=%v", changed, typeFilled)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"type":"1"`)) && !bytes.Contains(raw, []byte(`"type": "1"`)) {
		t.Errorf("侧车里原有的 type=1 被冲掉了：%s", raw)
	}
}
