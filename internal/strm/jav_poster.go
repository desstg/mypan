package strm

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"litepan/internal/jav/emby"
)

// 海报裁切的低优先级队列。
//
// # 为什么要队列
//
// 裁切是这条链路里最贵的一步：解码整张封面 + 跑一遍人脸级联。放在扫描里会让每一部片
// 多等几百毫秒，而扫描是有超时与并发闸的。用户的原话是「这一步可以在其它事完成之后，
// 在后面慢慢执行」—— 这个队列就是那句话的实现，不是顺手异步。
//
// 挂在 `strm.Service` 上（**全局一个**）：所有任务共用一个单并发工作者，
// 多个 STRM 任务并行时海报天然排队 —— 这就是「低优先级」的实现方式。
//
// 作业只带**本地路径**：不带网盘句柄、不带扫描的 ctx（那个 ctx 在任务结束时就被取消了）。

type javPosterJob struct {
	ThumbPath  string
	PosterPath string
	Censored   bool
	// Overwrite 为真时这张海报是**要重算**的（全量恢复 / 手动重刮）：绕过去重、
	// 也绕过「已存在就跳过」。
	Overwrite bool
	// EnforceCrop 为真时：海报**已经存在**也核对一次「它是不是按本片属性该有的那一版」，
	// 对不上才重裁。命中时一个字节都不写。
	//
	// 用途只有一个：**给老侧车补上 `type` 的那一轮**（2026-10-30 用户报的
	// 「好多张有码片的 poster 不是 thumb 的右边」）。那 17 份侧车当年没写 `type`，
	// 于是 `IsCensored()` 判 false、海报走了人脸档；补上字段之后**存量那张图不会自己变**，
	// 而默认的「存在即跳过」永远不会再看它一眼。
	//
	// ⚠️ 与 `Overwrite` 的区别必须守住：`Overwrite` 是无条件重裁（用户手调的框会被换掉），
	// 这一条是**核对后再决定**，只有「明显不是右边的裁法」才动手。
	EnforceCrop bool
	// WatermarkIDs 是要贴上去的水印 id（空 = 不贴）。由生成侧按侧车属性算好传进来 ——
	// 队列只负责"贴"，不负责"该不该贴"（那是业务判据，不属于队列）。
	WatermarkIDs []string
	// WatermarkScale / WatermarkMargin 是百分数（18 / 6），与编辑页那两个偏好同源。
	WatermarkScale  int
	WatermarkMargin int
	// watermarkDir 是用户放图标的目录（空 = 用内置那套）。随作业带进来 ——
	// 队列是个孤立的工作者，不去反查 Service 的字段。
	watermarkDir string
}

const (
	// javPosterQueueSize 是队列容量。满了就丢（记 Debug）：下一轮扫描发现 poster
	// 还不在会重新入队，所以丢不丢都不影响最终一致。
	javPosterQueueSize = 4096
	// javPosterGap 是每个作业之间的停顿。300ms 与番号库回填那个循环同量级 ——
	// 这是一步纯 CPU 活，间隔只为把负载摊平，不让它在扫描刚结束时抢满一个核。
	javPosterGap = 300 * time.Millisecond
	// javPosterTimeout 是单个作业的上限（读图 + 裁切 + 落盘，正常几十毫秒）。
	javPosterTimeout = 30 * time.Second
)

type javPosterQueue struct {
	log *slog.Logger
	ch  chan javPosterJob

	mu   sync.Mutex
	seen map[string]struct{} // 去重键 = PosterPath
	ctx  context.Context
}

func newJavPosterQueue(log *slog.Logger) *javPosterQueue {
	if log == nil {
		log = slog.Default()
	}
	return &javPosterQueue{
		log:  log,
		ch:   make(chan javPosterJob, javPosterQueueSize),
		seen: map[string]struct{}{},
	}
}

// PendingForTest 报告队列里还没处理完的任务数（长度 + 正在处理的那一个）。
//
// 存在的理由是**测试与排查**：海报裁切是异步的，跑完一遍「重刮」之后
// `poster.jpg` 还没落盘是常态，不等到 0 就去看文件会得出「水印没贴上」的错结论。
func (q *javPosterQueue) PendingForTest() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	inflight := 0
	if q.seen != nil {
		inflight = len(q.seen)
	}
	q.mu.Unlock()
	return len(q.ch) + inflight
}

// Start 起工作者，随 appCtx 结束。重复调用是安全的（第二次直接返回）。
func (q *javPosterQueue) Start(ctx context.Context) {
	if q == nil || ctx == nil {
		return
	}
	q.mu.Lock()
	if q.ctx != nil {
		q.mu.Unlock()
		return
	}
	q.ctx = ctx
	q.mu.Unlock()
	go q.run(ctx)
}

// SchedulePoster 入队。队列没启动时记 warn 丢弃；队列满时记 Debug 留到下一轮。
//
// # 去重是**一轮之内**的事，不是永久的
//
// 用途只有一个：同一张海报在一轮扫描里可能被两个入口算出同一个路径
// （扫描 + 手动「生成当前目录」），做两遍纯属浪费。
//
// ⚠️ **记录必须在处理完之后删掉**（见 process）。这一条踩过真机：
// 原来 `seen` 只增不减，而键是**绝对路径** —— 于是删掉任务、用同一个输出目录
// 重建之后，新任务算出来的路径与旧的一模一样，**每一张都被当成重复静默丢掉**。
// 症状是「扫描跑完一张 poster 都没有，日志里也一条都不见」（去重这条路不记日志），
// 而跑一次**全量**扫描又能出来 —— 因为全量走 `Overwrite`，绕过了去重。
// 群晖上就是这么表现的，查了很久。
//
// 只在一轮内去重是安全的：真正的「这张海报已经写好了没有」由 process 里的
// artifactExists 再判一次，那才是最后一道、也是唯一可靠的闸门。
func (q *javPosterQueue) SchedulePoster(job javPosterJob) {
	if q == nil || job.ThumbPath == "" || job.PosterPath == "" {
		return
	}
	// ⚠️ 队列**没启动**时必须记 warn：那时 job 会被静默丢掉（下面那个 select 的
	// default），而「一张 poster 都没生成」与「队列满了」在日志里长得一模一样。
	// 这是 2026-10-04 排查群晖那个问题时的第二个盲点。
	if !q.started() {
		q.log.Warn("番号元数据：海报队列未启动，这一张被丢弃",
			"poster", job.PosterPath, "hint", "strm.Service.Start 没跑到？")
		return
	}
	// 去重只在**非强制**时生效：强制作业（全量恢复 / 手动重刮）本来就要重算，
	// 走去重会让第二次「全量恢复」命中第一次留下的记录、被静默丢掉。
	q.mu.Lock()
	if !job.Overwrite {
		if _, dup := q.seen[job.PosterPath]; dup {
			q.mu.Unlock()
			return
		}
	}
	q.seen[job.PosterPath] = struct{}{}
	q.mu.Unlock()

	select {
	case q.ch <- job:
	default:
		// 满了：把去重记录撤掉，下一轮还能再试
		q.mu.Lock()
		delete(q.seen, job.PosterPath)
		q.mu.Unlock()
		q.log.Debug("番号元数据：海报队列已满，这一张留到下一轮", "poster", job.PosterPath)
	}
}

// started 报告工作者起来了没有（Start 被调用过）。
func (q *javPosterQueue) started() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.ctx != nil
}

// forget 撤掉一条去重记录（一个作业处理完之后）。
func (q *javPosterQueue) forget(posterPath string) {
	q.mu.Lock()
	delete(q.seen, posterPath)
	q.mu.Unlock()
}

func (q *javPosterQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-q.ch:
			q.process(ctx, job)
			select {
			case <-ctx.Done():
				return
			case <-time.After(javPosterGap):
			}
		}
	}
}

// process 执行一个作业。**再查一次**「poster 在不在」：
//
// 入队到执行之间隔着队列，而且生成侧与执行侧可能隔着一次进程重启。作业只带本地路径，
// 所以执行侧这一次检查是最后一道、也是唯一可靠的闸门。
func (q *javPosterQueue) process(parent context.Context, job javPosterJob) {
	ctx, cancel := context.WithTimeout(parent, javPosterTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return
	}
	// 处理完就撤掉去重记录：它只用来挡「同一轮里的重复入队」，
	// 留着会让删任务重建之后**每一张都被当成重复丢掉**（见 SchedulePoster 的注释）。
	defer q.forget(job.PosterPath)

	if !job.Overwrite && artifactExists(job.PosterPath) {
		// EnforceCrop：海报**已经在了**，也要判一次「它是不是旧判据留下的那一版」。
		// 不是（或认不出）就一个字节都不写。
		if !job.EnforceCrop || !posterNeedsRecrop(job) {
			return
		}
	}
	thumb, err := os.ReadFile(job.ThumbPath)
	if err != nil {
		// thumb 可能还没写完（或这轮被删了）—— 下一轮入队时会重算。
		q.log.Debug("番号元数据：海报裁切读不到缩略图，跳过", "thumb", job.ThumbPath, "err", err)
		return
	}
	poster, err := emby.BuildPoster(thumb, job.Censored)
	if err != nil {
		// BuildPoster 已经把「原样复制」的结果带回来了，只把原因记下来。
		q.log.Warn("番号元数据：海报裁切降级为原图", "poster", job.PosterPath, "err", err)
	}
	if len(poster) == 0 {
		// 走到这里说明 BuildPoster 连原图都没带回来 —— 那不该发生，
		// 而它以前是**静默返回**的（队列里唯一一处无声的出口）。
		q.log.Warn("番号元数据：海报裁切产出空字节，这一张没有写", "poster", job.PosterPath)
		return
	}
	// 水印：best-effort（贴不上就按无水印写出去），与上面裁切失败的处理一致。
	if len(job.WatermarkIDs) > 0 {
		marks, loadErr := emby.LoadWatermarks(job.watermarkDir, job.WatermarkIDs)
		if loadErr != nil {
			q.log.Warn("番号元数据：水印图标装不上", "ids", job.WatermarkIDs, "err", loadErr)
		} else if marked, wmErr := emby.ComposePosterWithMargin(poster, marks,
			float64(job.WatermarkScale)/100, float64(job.WatermarkMargin)/100); wmErr != nil {
			q.log.Warn("番号元数据：水印没贴上，海报按无水印写", "poster", job.PosterPath, "err", wmErr)
		} else {
			poster = marked
		}
	}
	if _, err := writeMetadataFileForced(filepath.Dir(job.PosterPath), filepath.Base(job.PosterPath), poster, true); err != nil {
		q.log.Warn("番号元数据：海报写入失败", "poster", job.PosterPath, "err", err)
	}
}

// posterNeedsRecrop 报告「磁盘上这张海报是不是该按新判据重裁一次」。
//
// # 为什么需要它
//
// 队列的默认判据是「poster 在就跳过」—— 那对（手调过的框不能被自动流程覆盖），
// 但它同时意味着**存量那张图永远不会被重新看一眼**。真机后果（2026-10-30）：
// 17 份老侧车当年没写 `type`，海报是按「无码/人脸」那一档裁的；补上 `type` 之后
// 侧车对了，海报却还是旧的那张 —— 用户看到的就是「好多张有码片的 poster
// 不是 thumb 的右边」。
//
// # 判据：反推当前窗口，与新属性算出来的窗口比
//
// 用 MatchPosterRect 反推这张海报当初是从 thumb 的哪一块裁的，再和
// DefaultPosterRect（按**当前**属性算、生成器会给的那一个）比。一致就不动 ——
// EnforceCrop 只在「侧车刚被补上 `type`」时置位，所以用户手调过的框不会因为
// 例行扫描被换掉。
//
// ⚠️ **反推不出来时返回 false（不动）**。反推失败意味着这张 poster 与当前 thumb
// 不是同一代（上游换过封面、旧版缩放图 —— 见 MatchPosterRect 的注释），那时重裁
// 等于用今天的 thumb 盖掉用户正看着的那张图。宁可留着旧图，等他点「裁剪」时自然对齐。
func posterNeedsRecrop(job javPosterJob) bool {
	thumb, err := os.ReadFile(job.ThumbPath)
	if err != nil {
		return false // 连 thumb 都没有：这一轮没得算
	}
	poster, err := os.ReadFile(job.PosterPath)
	if err != nil {
		return false // 说了「在」却读不出来：让调用方下一轮按缺失重入队
	}
	want, err := emby.DefaultPosterRect(thumb, job.Censored)
	if err != nil {
		return false // thumb 解不开：裁也裁不出东西
	}
	got, ok := emby.MatchPosterRect(thumb, poster)
	if !ok {
		return false
	}
	// 容差 2 像素：反推是逐列灰度找最小差，整数窗口重编码后会偏一两个像素。
	const tol = 2
	return absInt(got.X-want.X) > tol || absInt(got.Y-want.Y) > tol ||
		absInt(got.W-want.W) > tol || absInt(got.H-want.H) > tol
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
