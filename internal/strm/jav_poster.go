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

// SchedulePoster 入队。队列满 / 未启动时静默丢弃（记 Debug）。
//
// 去重按 PosterPath：同一张海报在一轮扫描里可能被两个入口算出同一个路径
// （扫描 + 手动「生成当前目录」），做两遍纯属浪费。
func (q *javPosterQueue) SchedulePoster(job javPosterJob) {
	if q == nil || job.ThumbPath == "" || job.PosterPath == "" {
		return
	}
	q.mu.Lock()
	if _, dup := q.seen[job.PosterPath]; dup {
		q.mu.Unlock()
		return
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
	if artifactExists(job.PosterPath) {
		return
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
		return
	}
	if _, err := writeMetadataFile(filepath.Dir(job.PosterPath), filepath.Base(job.PosterPath), poster); err != nil {
		q.log.Warn("番号元数据：海报写入失败", "poster", job.PosterPath, "err", err)
	}
}
