package strm

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newCapturingLogger 把日志收进内存，用来断言「该记的那条记了没有」。
//
// 那几个「静默失败」的坑（队列没启动、去重吃掉了作业）之所以难查，正是因为
// 它们不留下任何痕迹 —— 所以这里的断言对象就是**日志本身**。
func newCapturingLogger(out *[]string) *slog.Logger {
	var mu sync.Mutex
	return slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		*out = append(*out, string(p))
		mu.Unlock()
		return len(p), nil
	}), nil))
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

var _ io.Writer = writerFunc(nil)

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

// TestJavPosterQueueForgetsAfterProcess 去重记录**必须在一张处理完之后撤掉**。
//
// 这一条是真机踩出来的：`seen` 原来只增不减，而键是**绝对路径** ——
// 于是删掉任务、用同一个输出目录重建之后，新任务算出来的路径与旧的一模一样，
// **每一张都被当成重复静默丢掉**。症状是「扫描跑完一张 poster 都没有，
// 日志里也一条都不见」，而跑一次全量又能出来（全量走 Overwrite，绕过去重）。
//
// 判据：处理完一张之后，`seen` 里不该再留着它。
func TestJavPosterQueueForgetsAfterProcess(t *testing.T) {
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
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})
	waitFor(t, poster)

	// 等去重记录被撤掉（处理是异步的，落盘与 forget 之间没有可见的同步点）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		_, still := q.seen[poster]
		q.mu.Unlock()
		if !still {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("处理完之后去重记录还留着 —— 删任务重建后这一张会被当成重复丢掉")
}

// TestJavPosterQueueWarnsWhenNotStarted 队列没启动时要**记 warn**。
//
// 以前这里是静默丢弃（只记 Debug），于是「一张 poster 都没生成」与
// 「队列满了」在日志里长得一模一样 —— 群晖那次排查的第二个盲点。
func TestJavPosterQueueWarnsWhenNotStarted(t *testing.T) {
	dir := t.TempDir()
	var logs []string
	q := newJavPosterQueue(newCapturingLogger(&logs))
	// **不调 Start**
	q.SchedulePoster(javPosterJob{
		ThumbPath:  filepath.Join(dir, "thumb.jpg"),
		PosterPath: filepath.Join(dir, "poster.jpg"),
		Censored:   true,
	})
	if !strings.Contains(strings.Join(logs, "\n"), "海报队列未启动") {
		t.Errorf("队列没启动时必须留下一条 warn，got %v", logs)
	}
	if artifactExists(filepath.Join(dir, "poster.jpg")) {
		t.Error("队列没启动时不该写出任何东西")
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

// TestJavPosterQueueSurvivesTaskRecreate 复刻群晖那个真机场景：
// **删掉任务（连同输出目录）→ 用同一个输出目录重建任务 → 扫描**。
//
// 修复前：`seen` 只增不减且键是绝对路径，重建后每张都命中旧记录被静默丢掉 ——
// 扫描跑完一张 poster 都没有、日志里也一条都不见，而全量扫描（走 Overwrite
// 绕过去重）又能出来。修复后：第一轮普通作业处理完就撤掉记录，第二轮照常写。
func TestJavPosterQueueSurvivesTaskRecreate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "有码", "ABF-387")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	thumb := filepath.Join(dir, "thumb.jpg")
	poster := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(thumb, sampleJPEG(840, 568), 0o644); err != nil {
		t.Fatal(err)
	}

	q := newJavPosterQueue(testLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	// 第一轮（旧任务）：正常出图
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})
	waitFor(t, poster)

	// 删任务（连输出目录一起删）→ 用同一个输出目录重建
	if err := os.RemoveAll(filepath.Join(root, "有码")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(thumb, sampleJPEG(840, 568), 0o644); err != nil {
		t.Fatal(err)
	}

	// 第二轮（新任务）：**普通**作业，必须照样出图
	q.SchedulePoster(javPosterJob{ThumbPath: thumb, PosterPath: poster, Censored: true})
	waitFor(t, poster)
}
