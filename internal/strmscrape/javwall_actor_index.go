package strmscrape

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

// 演员墙索引的**缓存与后台重建**。
//
// # 为什么要这一层（2026-10-24 用户报「直接点演员按键经常 502，先开一次海报墙就能开」）
//
// `ListJavWallActors` 原来是**同步**聚合：把每一面番号墙的快照扫出来、再把每一部片的
// 侧车 / nfo 读一遍。本地盘上那是内存活，但这套库的墙常常挂在 SMB / 网盘上 ——
// 实测一台 NAS 上 1200 个影片目录：扫目录 55s、读侧车 55s、读 nfo 23s，
// 合起来两分钟出头。群晖那层反代的读超时等不了这么久，于是**用户先看到 502、
// 后端那条 goroutine 还在跑**（本项目老毛病，见 internal/api/media_organize.go:193）。
//
// 用户观察到的「先开一下海报墙，再点演员就正常」正是这个机制：海报墙的列表会先把
// 那个数据源的快照建好（同一次扫盘），演员这一趟就只剩读侧车那一段，勉强赶在超时前；
// 而直接点演员时两段叠在一起，必然超。第二句「能打开也要等好久」是同一件事的轻症。
//
// # 因此
//
//   - 索引**只在后台建**，请求路径上绝不扫盘；
//   - 建好的索引缓存一段时间（见 javActorIndexMaxAge），过期了也先给旧的那份；
//   - 一份都没有时立刻回一页「正在整理」（`building: true`），前端据此显示进度并轮询；
//   - 失效判据是**墙的内容**（见 javWallSnapshotSig），不是「快照对象换过」：
//     快照每 60s 到期重扫一次，但那多半只是重扫了一遍同一个库 —— 按对象判会让
//     每次开海报墙都白建一次索引。
const (
	// javActorIndexMaxAge 是索引的最长保鲜期。
	//
	// 比快照那 60s 长得多：这一份是在快照之上又读了一遍侧车，重建一次一两分钟，
	// 按快照的节奏来等于「每分钟把墙重读一遍」。墙真的变了由内容签名立刻反映，
	// 这个上限只兜住「片名一个没变、但侧车里的演员被改写」那种情况。
	javActorIndexMaxAge = 10 * time.Minute
	// javActorIndexBuildBudget 是一次后台重建最多能花多久。
	//
	// 必须有自己的预算而不是跟着请求走：触发它的那个请求早就断了（用户关页面 /
	// 反代先超时），而扫描按 ctx 取消 —— 拿请求的 ctx 去建，等于建一半作废。
	javActorIndexBuildBudget = 10 * time.Minute
	// javActorIndexRetryDelay 是两次重建之间的最短间隔（失败之后别把共享 / 外站打爆）。
	javActorIndexRetryDelay = 30 * time.Second
	// javActorDetailIndexWait 是「详情类请求等索引」的上限。
	//
	// 列表请求**从不等待**（它要的是立刻给出一页「正在整理」）；详情 / TMDB / 资料
	// 这三条是用户点了一张卡之后才发的，那时索引多半已经在建或建好了 ——
	// 等一会儿比直接报错好，但不能无限等（否则又回到 502）。
	javActorDetailIndexWait = 15 * time.Second
	// javActorIndexWarmDelay 是启动后多久开始预建第一份索引。
	//
	// 启动那几秒各子系统都在初始化（认证、缓存、扫描任务），别在这时候去抢 SMB；
	// 但也不能不建 —— 第一次打开演员墙才有东西可给。
	javActorIndexWarmDelay = 90 * time.Second
)

// javActorSourceMissingSig 是「这一面墙**没扫上**」的哨兵签名。
//
// 一份索引只要少了一面墙就永远不算新鲜（见 javActorIdxSourcesFresh），下一次请求
// 会再试一遍 —— 但受 javActorIndexRetryDelay 兜着，不会变成每秒扫一遍。
// 用哨兵而不是「干脆不记这个键」：不记的话，这条判据自己就发现不了「少了一面」。
const javActorSourceMissingSig = "\x00missing"

// javActorIndexCache 是一份建好的索引 + 建它时看到的墙内容。
type javActorIndexCache struct {
	sig     string
	idx     *javActorIndex
	sources map[string]string // 数据源键 → 当时那份快照的内容签名
	builtAt time.Time
}

// javWallSnapshotSig 是**一份扫盘快照的内容签名**。
//
// 只由「墙上有哪些卡」决定（数据源 + 根 + 每一行的 rel_dir/stem），不含构建时间：
// TTL 到期重扫同一个库、根目录 mtime 动了但内容没变，签名都一样 —— 演员索引因此
// 不必跟着重建。卡片变了（加片 / 删片 / 改名 / 换数据源）就会变。
func javWallSnapshotSig(scopeKey, root string, snap *javWallSnapshot) string {
	if snap == nil {
		return ""
	}
	var b strings.Builder
	b.Grow(32 + len(snap.rows)*24)
	b.WriteString(scopeKey)
	b.WriteByte(0)
	b.WriteString(root)
	b.WriteByte(0)
	b.WriteString(strconv.Itoa(len(snap.rows)))
	for _, row := range snap.rows {
		b.WriteByte(0)
		b.WriteString(row.item.RelDir)
		b.WriteByte(0)
		b.WriteString(row.item.Stem)
	}
	return b.String()
}

// javActorSourcesSig 把「这次要用哪些数据源」折成一个可比较的签名。
//
// 必须带上 root：同一个任务被改指向别的目录时 ScopeKey 不变，不带 root 就会把
// 旧索引当成新的（「改了输出目录，演员还是旧那批」）。
func javActorSourcesSig(sources []*wallSrc, hiddenKey string) string {
	parts := make([]string, 0, len(sources))
	for _, src := range sources {
		if src == nil {
			continue
		}
		parts = append(parts, src.ScopeKey()+"\x00"+src.Root)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01") + "\x02" + hiddenKey
}

// javActorIndexSnapshot 取当前可用的索引；`building` 表示后台正在建。
//
// 缓存过期但还有旧的那一份时**照旧返回**（stale-while-revalidate）：演员墙是一面
// 浏览页，给一份几分钟前的名单远比给一句「正在整理」好。
func (s *Service) javActorIndexSnapshot(ctx context.Context) (idx *javActorIndex, building bool) {
	hiddenKey := javWallHiddenKey(javWallHiddenDirs(s.settings))
	sources := s.javWallActorSources(ctx)
	sig := javActorSourcesSig(sources, hiddenKey)

	s.javActorIdxMu.Lock()
	cached := s.javActorIdx
	s.javActorIdxMu.Unlock()

	// ⚠️ 判「还新不新」放在锁**外面**：`javActorIdxSourcesFresh` 要读 `javWallMu`，
	// 两把锁套在一起，将来任何一处反过来拿（先 javWallMu 再 javActorIdxMu）就会咬死。
	if cached != nil && cached.idx != nil && cached.sig == sig &&
		time.Since(cached.builtAt) <= javActorIndexMaxAge &&
		s.javActorIdxSourcesFresh(cached.sources) {
		return cached.idx, false
	}

	s.javActorIdxMu.Lock()
	// 没有在跑、而且离上次尝试够久了，才起一轮。失败之后立刻再来会变成死循环
	// （整个共享不通时每一秒起一轮扫描），所以留一段退避。
	if s.javActorIdxBuilding == nil && time.Since(s.javActorIdxTriedAt) >= javActorIndexRetryDelay {
		s.javActorIdxTriedAt = time.Now()
		s.javActorIdxBuilding = make(chan struct{})
		go s.rebuildJavActorIndex(s.javActorIdxBuilding)
	}
	building = s.javActorIdxBuilding != nil
	s.javActorIdxMu.Unlock()

	if cached != nil && cached.idx != nil {
		return cached.idx, false
	}
	return nil, building
}

// javActorIdxSourcesFresh 判断「建索引时看到的那些墙，现在还是不是那样」。
//
// 只看**内存里的快照**（不读盘）：快照被作废（保存 / 删除 / 改隐藏 / 换目录）时
// `javWallCache` 里那一格就没了，这里立刻判成变了。
func (s *Service) javActorIdxSourcesFresh(used map[string]string) bool {
	s.javWallMu.Lock()
	defer s.javWallMu.Unlock()
	for key, want := range used {
		snap := s.javWallCache[key]
		if snap == nil {
			return false
		}
		// 快照上就带着 root，签名不必再靠数据源去拼（那条路要查库 / 解析路径）。
		if javWallSnapshotSig(key, snap.root, snap) != want {
			return false
		}
	}
	return true
}

// rebuildJavActorIndex 后台建一轮索引，建好就换上（`done` 关掉时醒等待者）。
func (s *Service) rebuildJavActorIndex(done chan struct{}) {
	started := time.Now()
	idx := s.warmJavActorIndexNow()

	s.javActorIdxMu.Lock()
	if idx == nil {
		// 失败：退避从**结束**这一刻算起。从「开始」算的话，一轮两分钟的失败
		// 跑完就立刻又能起下一轮 —— 共享整个不通时等于连续扫、永远扫不完。
		s.javActorIdxTriedAt = time.Now()
	}
	s.javActorIdxBuilding = nil
	close(done)
	s.javActorIdxMu.Unlock()

	if s.log == nil {
		return
	}
	if idx == nil {
		s.log.Warn("jav actor index build failed", "elapsed_ms", time.Since(started).Milliseconds())
		return
	}
	s.log.Info("jav actor index ready",
		"actors", len(idx.order), "movies", idx.total, "sources", idx.sources,
		"elapsed_ms", time.Since(started).Milliseconds())
}

// warmJavActorIndexNow **同步**建一轮索引并换上（带自己的预算与取消）。
//
// 请求路径上不要调它（那就是 502 的来源）。它给三个地方用：后台重建、启动预热、
// 以及测试（测试要确定性地拿到一份建好的索引，不能去等 goroutine）。
func (s *Service) warmJavActorIndexNow() *javActorIndex {
	ctx, cancel := context.WithTimeout(context.Background(), javActorIndexBuildBudget)
	defer cancel()

	hiddenKey := javWallHiddenKey(javWallHiddenDirs(s.settings))
	sources := s.javWallActorSources(ctx)
	idx, used := s.buildJavActorIndexFrom(ctx, sources)
	if idx == nil {
		return nil
	}
	// 少了一面墙的索引仍然是「能用」的（先把有的那几面给人看），但它**永远不算新鲜**
	// （见 javActorIdxSourcesFresh），下一次请求还会再来一遍。
	incomplete := false
	for _, sig := range used {
		if sig == javActorSourceMissingSig {
			incomplete = true
			break
		}
	}
	s.javActorIdxMu.Lock()
	s.javActorIdx = &javActorIndexCache{
		sig:     javActorSourcesSig(sources, hiddenKey),
		idx:     idx,
		sources: used,
		builtAt: time.Now(),
	}
	// `javActorIdxTriedAt` 是给失败兜底的退避（共享不通时别每秒起一轮扫描）。
	//   - 全都扫上了：清空 —— 否则墙一变还要干等 30 秒，那 30 秒里端出去的是
	//     **删掉那部片**的旧名单；
	//   - 有一面没扫上：记成「刚试过」—— 它每次都判不新鲜，不清的话就成了
	//     「每来一个请求就整面墙扫一遍」。
	if incomplete {
		s.javActorIdxTriedAt = time.Now()
	} else {
		s.javActorIdxTriedAt = time.Time{}
	}
	s.javActorIdxMu.Unlock()
	return idx
}

// waitJavActorIndex 等一轮正在跑的构建（最多 `timeout`）。
//
// 返回 nil 表示「没等到」或「压根没在跑」—— 调用方自己决定是报错还是用缓存。
func (s *Service) waitJavActorIndex(ctx context.Context, timeout time.Duration) *javActorIndex {
	s.javActorIdxMu.Lock()
	ch := s.javActorIdxBuilding
	s.javActorIdxMu.Unlock()
	if ch == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-ctx.Done():
		return nil
	case <-timer.C:
		return nil
	}
	s.javActorIdxMu.Lock()
	defer s.javActorIdxMu.Unlock()
	if s.javActorIdx != nil {
		return s.javActorIdx.idx
	}
	return nil
}

// errJavActorIndexBuilding 是「索引还在建」时详情类接口给的错。
//
// 特意不用 CodeNotFound：那会被前端说成「没有这位演员」，而人明明在墙上 ——
// 这是本项目反复踩的「静默变空」，所以这里必须说清是**还在整理**。
func errJavActorIndexBuilding() error {
	return domain.Errorf(domain.CodeInternal,
		"演员资料正在整理（第一次约需一两分钟），稍后再点一次")
}

// StartJavActorIndexWarmup 在后台预建一次演员索引（启动时由 app 调，可以并发）。
//
// 好处很实在：用户第一次打开演员墙就不用面对「正在整理」。**必须在后台**——
// 冷启动时那面墙可能要读几十秒，卡在启动流程里就是「服务起不来」。
func (s *Service) StartJavActorIndexWarmup(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(javActorIndexWarmDelay):
		}
		s.javActorIndexSnapshot(context.Background())
	}()
}
