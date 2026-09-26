package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 海报裁切队列。生成器那一半用「就地执行」测过了（PosterQueue 为 nil），
// 这里测真正跑在生产上的那条路：入队 → 后台裁 → 落盘。
//
// 它是全链路里唯一异步的一段，也最容易出「看起来对了其实没跑」的问题
// （队列没起、作业被去重吃掉、写盘失败只留一条 warn）。

func TestJavPosterQueueWritesPoster(t *testing.T) {
	dir := t.TempDir()
	thumb := filepath.Join(dir, "thumb.jpg")
	poster := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(thumb, sampleJPEG(800, 538), 0o644); err != nil {
		t.Fatal(err)
	}

	q := newJavPosterQueue(testLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)
	q.Start(ctx) // 重复 Start 是安全的（第二次直接返回，不会起第二个工作者）

	// 同一个 PosterPath 排两次：第二次应当被去重丢掉（扫描 + 手动生成可能同时入队）
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})

	waitFor(t, poster)
	img := decodeJPEGFile(t, poster)
	h := img.Bounds().Dy()
	if h != 538 {
		t.Errorf("海报高度应当与封面同高（538），got %d", h)
	}

	// 已经存在时再排一次：执行前的存在检查会让它什么都不做（也不会覆盖）
	before, _ := os.ReadFile(poster)
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})
	time.Sleep(80 * time.Millisecond)
	after, _ := os.ReadFile(poster)
	if string(before) != string(after) {
		t.Error("已存在的海报不该被重写")
	}
}

// TestJavPosterQueueSkipsUnreadableThumb 缩略图不见了（这一轮被删了）时静默跳过，
// 不 panic、不写半截文件 —— 下一轮入队时会重算。
func TestJavPosterQueueSkipsUnreadableThumb(t *testing.T) {
	dir := t.TempDir()
	poster := filepath.Join(dir, "poster.jpg")

	q := newJavPosterQueue(testLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)
	q.SchedulePoster(javPosterJob{ThumbPath: filepath.Join(dir, "missing.jpg"), PosterPath: poster, Censored: true})

	time.Sleep(80 * time.Millisecond)
	if artifactExists(poster) {
		t.Error("读不到缩略图时不该写出海报")
	}
}

// waitFor 等一个文件出现（队列是异步的，超时上界给足）。
func waitFor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if artifactExists(path) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等不到 %s", filepath.Base(path))
}
