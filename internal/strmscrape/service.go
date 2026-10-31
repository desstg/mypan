package strmscrape

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/jav"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/internal/thumbcache"
)

const (
	defaultItemListLimit = 120
	maxItemListLimit     = 200
)

type Options struct {
	Strm     *strm.Service
	Settings *settings.Service
	Bus      *eventbus.Bus
	DataDir  string
	StrmDir  string
	Log      *slog.Logger
	// FileInfo 按 (account_id, file_id) 查网盘源文件的信息（详情抽屉的「源媒体信息」
	// 要显示**源文件**的大小与类型，而 `.strm` 自己只有一百多字节）。
	//
	// 由 wire 注入（`*file.Service`）而不是直接持有它：strmscrape 不需要认识文件服务
	// 那一整套（列表 / 删除 / 上传），只要这一个查询。形状与 strm 那边的 ScrapeTrigger
	// 一样 —— 用一个小接口把依赖面收窄。
	//
	// **可以为 nil**（测试、或某个装配路径没接上）：那时源媒体信息退化成只显示文件名
	// 与路径，不报错。
	FileInfo FileInfoLookup
	// AI 是 AI 辅助识别（海报墙刮削的「补认」那一档，见 scrape_ai.go）。
	//
	// **可以为 nil**：nil 时 `run()` 的 AI 分支整段不进入，刮削与改造前逐字相同。
	// 用户没开 AI 也是同一条路（`Available()` 为假，见 run() 里 `useAI` 的算法）。
	AI AIEnhancer
}

// FileInfoLookup 是「按 file_id 查源文件信息」的最小接口。
//
// 存在的理由见 Options.FileInfo。`*file.Service` 的 Info 方法天然满足它。
type FileInfoLookup interface {
	Info(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error)
}

type Service struct {
	strm     *strm.Service
	settings *settings.Service
	bus      *eventbus.Bus
	dataDir  string
	strmDir  string
	log      *slog.Logger
	files    FileInfoLookup

	// jav 是番号服务（按番号打上游、写侧车、补 nfo）。
	//
	// **必须是 strmscrape → jav 这个方向**：jav 那边不 import 本包，本包 import 它
	// 不成环。而「按任务反查输出目录」那条依赖走的是 strm.Service，与本字段无关。
	//
	// 可以为 nil（测试、或某个装配路径没接上）：那时在线刮削报「番号服务未装配」。
	jav *jav.Service
	// javOnline 是在线刮削的进度（与 TMDB 那套 Progress 分开，见 javwall_online.go）。
	javOnlineMu sync.Mutex
	javOnline   *JavOnlineScrapeProgress
	// ai 是 AI 辅助识别（可为 nil，见 Options.AI）。
	ai AIEnhancer

	mu          sync.Mutex
	operationMu sync.Mutex
	progress    Progress
	cancel      context.CancelFunc
	indexLocks  sync.Map // taskID -> *sync.Mutex

	// javWallCache 是番号海报墙的扫盘快照（`wallSource.ScopeKey()` → 快照）。
	// 与 indexLocks 一样是"每个数据源一份"，但这里只是缓存：丢了重建即可。
	//
	// ⚠️ 键是**字符串**（不是 taskID）：墙的数据源现在有两种（STRM 任务 / 本地目录），
	// 任务那侧沿用裸十进制（"5"）以免与已有状态分家，本地那侧是 `local-<id>`。
	javWallMu    sync.Mutex
	javWallCache map[string]*javWallSnapshot
	// javTitleCache 是 nfo 标题的内存缓存（绝对路径 → 标题/番号/mtime/size）。
	// 冷启动要读几千份 nfo，命中就免一次读盘 + 解析。**不落盘**。
	javTitleMu    sync.Mutex
	javTitleCache map[string]javTitleEntry

	// javActorIdx 是演员墙索引（所有番号墙聚合出来的那一份）。
	//
	// **只在后台建**：建它要把每一面墙的侧车 / nfo 全读一遍，SMB 上实测一分钟以上，
	// 挂在请求路径上就是反代 502（2026-10-24 用户报的现象）。判据与预算见
	// javwall_actor_index.go。
	javActorIdxMu sync.Mutex
	javActorIdx   *javActorIndexCache
	// javActorIdxBuilding 非 nil 表示有一轮在跑（关闭 = 跑完）。
	javActorIdxBuilding chan struct{}
	// javActorIdxTriedAt 是上一次尝试重建的时间（失败退避用；成功时清空）。
	javActorIdxTriedAt time.Time

	// javActorTMDB 是演员详情里那段 TMDB 补充档案的缓存（键是查过的名字）。
	// 只存内存、24h 过期 —— 见 javwall_actor_tmdb.go 顶部那段。
	javActorTMDBMu sync.Mutex
	javActorTMDB   map[string]javActorTMDBEntry

	// javActorProfile 是演员**资料层**的缓存（生日 / 出身 / 三围 / 头像）。
	// 与 TMDB 那层分开：那个是补充档案，这个是番号圈自己的源（avbase / JAVDB），
	// 命中率与字段完全不同，混一张表会让两套 TTL 互相污染。
	// 见 javwall_actor_profile.go。
	javActorProfileMu sync.Mutex
	javActorProfile   map[string]javActorProfileEntry

	// wallSchedule 是详情抽屉「集数进度」要的 TMDB 排期（键 = TMDB id）。
	// 只存内存、6h 过期 —— 见 wall_episodes.go。
	wallScheduleMu sync.Mutex
	wallSchedule   map[string]wallScheduleEntry

	// thumbs 是按需缩放的图片磁盘缓存（见 internal/thumbcache）。
	// 海报墙上那些图只有一百多像素宽，原图直接发是五六倍的浪费。
	thumbs *thumbcache.Cache
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	strmDir := strings.TrimSpace(opts.StrmDir)
	if strmDir == "" {
		strmDir = filepath.Join(filepath.Dir(filepath.Clean(opts.DataDir)), "strm")
	}
	return &Service{
		strm:          opts.Strm,
		settings:      opts.Settings,
		bus:           opts.Bus,
		dataDir:       opts.DataDir,
		strmDir:       strmDir,
		log:           log,
		files:         opts.FileInfo,
		ai:            opts.AI,
		javWallCache:  map[string]*javWallSnapshot{},
		javTitleCache: map[string]javTitleEntry{},
		thumbs: thumbcache.New(opts.DataDir, func(msg string, args ...any) {
			log.Warn(msg, args...)
		}),
	}
}

func (s *Service) GetProgress() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.progress
}

func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func normalizeItemListQuery(in ItemListQuery) ItemListQuery {
	out := in
	if out.Offset < 0 {
		out.Offset = 0
	}
	switch {
	case out.Limit <= 0:
		out.Limit = defaultItemListLimit
	case out.Limit > maxItemListLimit:
		out.Limit = maxItemListLimit
	}
	out.Keyword = strings.TrimSpace(out.Keyword)
	out.Status = strings.TrimSpace(out.Status)
	out.MediaType = strings.TrimSpace(out.MediaType)
	out.TVState = strings.TrimSpace(out.TVState)
	switch out.Sort {
	case ItemListSortTitleAsc, ItemListSortYearDesc, ItemListSortYearAsc, ItemListSortAddedAsc, ItemListSortAddedDesc:
	default:
		out.Sort = ItemListSortAddedDesc
	}
	if out.Status != "" && out.Status != ItemStatusOK && out.Status != ItemStatusMiss && out.Status != ItemStatusDoubt {
		out.Status = ""
	}
	if out.MediaType != "" && out.MediaType != MediaTypeMovie && out.MediaType != MediaTypeTV {
		out.MediaType = ""
	}
	if out.TVState != "" && out.TVState != TVStateEnded && out.TVState != TVStateUpdating {
		out.TVState = ""
	}
	return out
}

// ListItems 是「按任务 id 列」的老入口（自动刮削 / 老前端）。
//
// 内部转调 ListItemsByRef：本地目录源与任务源因此**必然同源** —— 两边各写一份
// 迟早会漂成「任务能列、本地目录不能」。
func (s *Service) ListItems(ctx context.Context, strmTaskID int64, query ItemListQuery) (ItemListResult, error) {
	return s.ListItemsByRef(ctx, WallSourceRef{StrmTaskID: strmTaskID}, query)
}

// ListItemsByRef 按请求侧的选择器列（`local_dir_id` 优先，见 wall_source.go）。
func (s *Service) ListItemsByRef(ctx context.Context, ref WallSourceRef, query ItemListQuery) (ItemListResult, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return ItemListResult{}, err
	}
	return s.ListItemsBySource(ctx, src, query)
}

// ListItemsBySource 是 ListItems 的本体（数据源已经在手上时走这条）。
func (s *Service) ListItemsBySource(ctx context.Context, src *wallSrc, query ItemListQuery) (ItemListResult, error) {
	query = normalizeItemListQuery(query)
	if err := requireTmdbSource(src); err != nil {
		return ItemListResult{}, err
	}
	root := src.Root
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	var out ItemListResult
	err := s.withTaskIndexLock(src.ScopeKey(), func() error {
		if err := s.ensureIndexLocked(ctx, src, root); err != nil {
			return err
		}
		items, err := s.listIndexItems(src.ScopeKey(), src.PosterRef(), query)
		if err != nil {
			return err
		}
		out = items
		return nil
	})
	return out, err
}

// RefreshIndex 扫盘重建索引并返回列表（海报墙刷新按钮）。
//
// 与 ListItems 同一条规矩：老入口转调源版，两种数据源走同一份实现。
func (s *Service) RefreshIndex(ctx context.Context, strmTaskID int64, query ItemListQuery) (ItemListResult, error) {
	return s.RefreshIndexByRef(ctx, WallSourceRef{StrmTaskID: strmTaskID}, query)
}

// RefreshIndexByRef 按请求侧的选择器重建索引并返回列表。
func (s *Service) RefreshIndexByRef(ctx context.Context, ref WallSourceRef, query ItemListQuery) (ItemListResult, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return ItemListResult{}, err
	}
	query = normalizeItemListQuery(query)
	if err := s.RebuildIndex(ctx, src); err != nil {
		return ItemListResult{}, err
	}
	return s.listIndexItems(src.ScopeKey(), src.PosterRef(), query)
}

func (s *Service) RunAsync(ctx context.Context, req RunRequest) error {
	// 守卫要放在**起后台任务之前**：进 startAsyncOperation 之后就是异步的了，
	// 错误只能落在 progress.Error 里，调用方拿不到（见 errNotTmdbTask 的注释）。
	src, err := s.resolveSource(ctx, WallSourceRef{StrmTaskID: req.StrmTaskID, LocalDirID: req.LocalDirID})
	if err != nil {
		return err
	}
	if err := requireTmdbSource(src); err != nil {
		return err
	}
	_ = ctx // 后台任务不随启动请求结束
	return s.startAsyncOperation(src, 0, "准备刮削", "刮削完成", "strm scrape failed", func(runCtx context.Context) error {
		return s.run(runCtx, src, req)
	})
}

// taskSource 用一个 taskID 现查一个数据源。
//
// 给**还没迁移到本地源**的那几条路用（TMDB 的详情 / 重匹配 / 重刮 / 补抓）：
// 它们只需要把 taskID 还原成统一的数据源，好复用同一套下游。
func (s *Service) taskSource(ctx context.Context, taskID int64) (*wallSrc, error) {
	return s.resolveSource(ctx, WallSourceRef{StrmTaskID: taskID})
}

func (s *Service) startAsyncOperation(src *wallSrc, total int, message, doneMessage, logMessage string, run func(context.Context) error) error {
	if !s.operationMu.TryLock() {
		return domain.Errorf(domain.CodeValidation, "刮削任务进行中")
	}
	releaseFiles := func() {}
	if s.strm != nil {
		var ok bool
		if src.isLocal() {
			// 本地目录源没有任务 id，拿 TaskID 去取锁会拿到一把**键为 0 的**、
			// 谁都能进的锁。改用按根目录取的锁 —— 它与任务那把是**同一个 registry**
			// 里的不同键，所以「同一个目录既是某任务的输出、又被挂成本地源」这条
			// 重合仍然互斥（注册期也另外拦了一道，见 rejectTaskOutputRoot）。
			releaseFiles, ok = s.strm.TryBeginRootFileOperation(src.Root)
		} else {
			releaseFiles, ok = s.strm.TryBeginTaskFileOperation(src.TaskID)
		}
		if !ok {
			s.operationMu.Unlock()
			return domain.Errorf(domain.CodeValidation, "该数据源正在运行，请稍后再刮削")
		}
	}
	s.mu.Lock()
	if s.progress.Running {
		s.mu.Unlock()
		releaseFiles()
		s.operationMu.Unlock()
		return domain.Errorf(domain.CodeValidation, "刮削任务进行中")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.progress = Progress{
		Running:   true,
		TaskID:    src.TaskID,
		SourceKey: src.ScopeKey(),
		Total:     total,
		Message:   message,
		StartedAt: time.Now().Format(time.RFC3339),
	}
	s.mu.Unlock()

	go func() {
		defer releaseFiles()
		defer s.operationMu.Unlock()
		defer cancel()
		err := run(runCtx)
		s.mu.Lock()
		s.progress.Running = false
		s.progress.CurrentItemID = ""
		s.cancel = nil
		if err != nil {
			s.progress.Error = err.Error()
			if s.progress.Message == "" {
				s.progress.Message = "刮削失败"
			}
			s.log.Warn(logMessage, "source", src.ScopeKey(), "err", err)
		} else if s.progress.Message == "" {
			s.progress.Message = doneMessage
		}
		s.mu.Unlock()
	}()
	return nil
}

// overwriteForMatch：换 TMDB ID 一律覆盖；同 ID 跟随全局写入策略。
func (s *Service) overwriteForMatch(sameID bool) bool {
	if !sameID {
		return true
	}
	return normalizeWriteMode(s.GetSettings().WriteMode) == WriteModeOverwrite
}

// requireTmdbTask 挡住「拿 TMDB 那套去刮番号任务」（见 errNotTmdbTask）。
//
// 取 task 而不是只比 taskID：resolveTask 已经查过一次库，这里再查一次是为了让
// 调用点读起来是一句话 —— 这几条路的开销都在后面的扫盘/网络，多一次主键查询无所谓。
func (s *Service) requireTmdbTask(ctx context.Context, strmTaskID int64) error {
	src, err := s.taskSource(ctx, strmTaskID)
	if err != nil {
		return err
	}
	return requireTmdbSource(src)
}

// Rematch 对同 ID 沿用写入策略，换 ID 时强制覆盖并统一走 writeMatchedOpts。
func (s *Service) Rematch(ctx context.Context, req RematchRequest) (*Item, bool, error) {
	src, err := s.resolveSource(ctx, WallSourceRef{StrmTaskID: req.StrmTaskID, LocalDirID: req.LocalDirID})
	if err != nil {
		return nil, false, err
	}
	if err := requireTmdbSource(src); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(req.ItemID) == "" || strings.TrimSpace(req.TMDBID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "参数不完整")
	}
	root := src.Root
	g, err := findWorkByID(root, req.ItemID)
	if err != nil {
		return nil, false, err
	}
	mediaType := strings.ToLower(strings.TrimSpace(req.MediaType))
	if mediaType == "" {
		mediaType = resolveWorkMediaType(g)
	}
	sameID := workTMDBIDMatches(g, mediaType, req.TMDBID)
	overwrite := s.overwriteForMatch(sameID)
	if s.newTMDBClient() == nil {
		return nil, false, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	display := strings.TrimSpace(req.Title)
	if display == "" {
		display = workDisplayName(g)
	}
	current := buildItem(src.PosterRef(), root, g)
	err = s.startAsyncOperation(src, 1, "正在刮削："+display, "已重新匹配："+display, "strm rematch failed", func(runCtx context.Context) error {
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = req.ItemID
		})
		updated, applyErr := s.applyRematch(runCtx, src, req, root, g, mediaType, overwrite)
		if applyErr != nil {
			s.setProgress(func(p *Progress) {
				p.Done = 1
				p.Failed = 1
				p.CurrentItemID = ""
				p.Message = "重新匹配失败：" + display
			})
			return applyErr
		}
		s.setProgress(func(p *Progress) {
			p.Done = 1
			p.CurrentItemID = ""
			p.ItemRevision++
			p.UpdatedItem = updated
			p.Message = "已重新匹配：" + display
		})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &current, true, nil
}

func (s *Service) applyRematch(ctx context.Context, src *wallSrc, req RematchRequest, root string, g workGroup, mediaType string, overwrite bool) (*Item, error) {
	client := s.newTMDBClient()
	if client == nil {
		return nil, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	scrapeCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	// 用 LookupFull 而不是 Lookup：这条路（重新匹配 / 重新刮削，`overwrite` 常为真）
	// 要写的就是完整的那一份。用简版的话，用户手动选定之后**已有的**演员/剧照/
	// 背景图会被一份薄 nfo 盖掉 —— 而且这一部往往正是从「薄」的那一版重新刮的。
	// 附加块拉不回来时 LookupFull 自己降级回简版（见 tmdb 客户端），不会整部失败。
	raw, _, err := client.LookupFull(scrapeCtx, req.TMDBID, mediaType)
	if err != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "TMDB 查询失败：%v", err)
	}
	info, err := decodeTMDBInfo(raw, mediaType)
	if err != nil {
		return nil, err
	}
	if title := strings.TrimSpace(req.Title); title != "" {
		info.Title = title
	}
	if req.Year != nil {
		info.Year = req.Year
	}
	info.Doubt = false // 用户手动选定，不再存疑
	_, err = s.writeMatchedOpts(scrapeCtx, client, g, info, overwrite, true, false)
	if err != nil {
		return nil, err
	}
	clearManualComplete(g)
	s.upsertIndexItem(scrapeCtx, src, root, g)
	item := buildItem(src.PosterRef(), root, g)
	return &item, nil
}

func workTMDBIDMatches(g workGroup, mediaType, tmdbID string) bool {
	meta, ok := readWorkNFOMeta(g, mediaType)
	return ok && strings.TrimSpace(meta.TMDBID) == strings.TrimSpace(tmdbID)
}

func confirmExistingMatch(g workGroup, mediaType string) {
	if pending, ok := readPendingState(g); ok && pending.Status == PendingDoubt {
		finalizeAfterScrape(g, mediaType, pending.EpTMDB, false)
	}
}

func (s *Service) MarkNormal(ctx context.Context, req MarkNormalRequest) (*Item, error) {
	if req.StrmTaskID <= 0 || strings.TrimSpace(req.ItemID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "参数不完整")
	}
	if !s.operationMu.TryLock() {
		return nil, domain.Errorf(domain.CodeValidation, "刮削任务进行中")
	}
	defer s.operationMu.Unlock()
	src, err := s.resolveSource(ctx, WallSourceRef{StrmTaskID: req.StrmTaskID, LocalDirID: req.LocalDirID})
	if err != nil {
		return nil, err
	}
	if err := requireTmdbSource(src); err != nil {
		return nil, err
	}
	root := src.Root
	g, err := findWorkByID(root, req.ItemID)
	if err != nil {
		return nil, err
	}
	mediaType := resolveWorkMediaType(g)
	if requested := strings.ToLower(strings.TrimSpace(req.MediaType)); requested == MediaTypeTV || requested == MediaTypeMovie {
		mediaType = requested
	}
	if req.ClearMatch {
		if err := clearScrapedMetadata(g); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "清理错误匹配元数据：%v", err)
		}
		if err := writeManualComplete(g, mediaType); err != nil {
			return nil, domain.Errorf(domain.CodeDriverError, "保存手动完成状态：%v", err)
		}
		s.upsertIndexItem(ctx, src, root, g)
		item := buildItem(src.PosterRef(), root, g)
		return &item, nil
	}
	if pending, ok := readPendingState(g); ok && pending.Status == PendingDoubt {
		if !workHasNFO(g, mediaType) || !workHasPoster(g, mediaType) {
			return nil, domain.Errorf(domain.CodeValidation, "%v", errRootMetaIncomplete)
		}
		confirmExistingMatch(g, mediaType)
	} else if workHasNFO(g, mediaType) && workHasPoster(g, mediaType) {
		if err := markWorkNormal(g, mediaType); err != nil {
			return nil, domain.Errorf(domain.CodeValidation, "%v", err)
		}
	} else if err := writeManualComplete(g, mediaType); err != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "保存手动完成状态：%v", err)
	}
	s.upsertIndexItem(ctx, src, root, g)
	item := buildItem(src.PosterRef(), root, g)
	return &item, nil
}

// Rescrape：沿用原 TMDB ID，走与「开始刮削」相同的 writeMatchedOpts（含正片季/finale 集数与 pending）。
func (s *Service) Rescrape(ctx context.Context, req RescrapeRequest) (*Item, bool, error) {
	if strings.TrimSpace(req.ItemID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "参数不完整")
	}
	src, err := s.resolveSource(ctx, WallSourceRef{StrmTaskID: req.StrmTaskID, LocalDirID: req.LocalDirID})
	if err != nil {
		return nil, false, err
	}
	if err := requireTmdbSource(src); err != nil {
		return nil, false, err
	}
	root := src.Root
	g, err := findWorkByID(root, req.ItemID)
	if err != nil {
		return nil, false, err
	}
	mediaType := resolveWorkMediaType(g)
	meta, ok := readWorkNFOMeta(g, mediaType)
	if _, manual := readManualComplete(g); manual && (!ok || strings.TrimSpace(meta.TMDBID) == "") {
		clearManualComplete(g)
		s.upsertIndexItem(ctx, src, root, g)
		item := buildItem(src.PosterRef(), root, g)
		return &item, false, nil
	}
	if !ok || strings.TrimSpace(meta.TMDBID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "缺少 TMDB ID，请先重新匹配")
	}
	display := strings.TrimSpace(meta.Title)
	if display == "" {
		display = workDisplayName(g)
	}
	current := buildItem(src.PosterRef(), root, g)
	overwrite := s.overwriteForMatch(true)
	if s.newTMDBClient() == nil {
		return nil, false, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	err = s.startAsyncOperation(src, 1, "正在重新刮削："+display, "已重新刮削："+display, "strm rescrape failed", func(runCtx context.Context) error {
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = req.ItemID
		})
		updated, applyErr := s.applyRematch(runCtx, src, RematchRequest{
			StrmTaskID: req.StrmTaskID,
			ItemID:     req.ItemID,
			TMDBID:     meta.TMDBID,
			MediaType:  mediaType,
			Title:      meta.Title,
			Year:       meta.Year,
		}, root, g, mediaType, overwrite)
		if applyErr != nil {
			s.setProgress(func(p *Progress) {
				p.Done = 1
				p.Failed = 1
				p.CurrentItemID = ""
				p.Message = "重新刮削失败：" + display
			})
			return applyErr
		}
		s.setProgress(func(p *Progress) {
			p.Done = 1
			p.CurrentItemID = ""
			p.ItemRevision++
			p.UpdatedItem = updated
			p.Message = "已重新刮削：" + display
		})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &current, true, nil
}

func (s *Service) ResolvePosterFile(ctx context.Context, srcRef WallSourceRef, rel string) (string, error) {
	src, err := s.resolveSource(ctx, srcRef)
	if err != nil {
		return "", err
	}
	root := src.Root
	rel = filepath.Clean("/" + strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "/")
	full := filepath.Join(root, rel)
	if !isInside(root, full) {
		return "", domain.Errorf(domain.CodeValidation, "非法路径")
	}
	// 扩展名白名单（图片 + 字幕）。
	//
	// 路径**允许子目录**（`extrafanart/fanart3.jpg` 就是剧照）—— 安全性由上面那条
	// `isInside(root, full)` 兜着：它挡住 `..` 逃逸，而根目录下的任何文件本来就
	// 是这个任务自己的输出，没有「读到系统文件」的余地。
	base := strings.ToLower(filepath.Base(full))
	if !strings.HasSuffix(base, ".jpg") && !strings.HasSuffix(base, ".jpeg") && !strings.HasSuffix(base, ".png") &&
		!strings.HasSuffix(base, ".webp") &&
		!strings.HasSuffix(base, ".srt") && !strings.HasSuffix(base, ".vtt") && !strings.HasSuffix(base, ".sup") {
		return "", domain.Errorf(domain.CodeValidation, "仅允许图片与字幕文件")
	}
	if !fileExists(full) {
		return "", domain.Errorf(domain.CodeNotFound, "文件不存在")
	}
	return full, nil
}

// PosterThumb 返回 path 这张图缩到不超过 width 宽的 JPEG 字节（走磁盘缓存）。
//
// 出错或缩不动时返回原图字节 —— 调用方总能拿到一张图，失败路径不必分支
// （见 thumbcache 包注释里那条「失败就回原图」的边界）。
func (s *Service) PosterThumb(path string, width int) ([]byte, string) {
	if s == nil || s.thumbs == nil || strings.TrimSpace(path) == "" || width <= 0 {
		return nil, ""
	}
	return s.thumbs.Get(path, width)
}

// emptyWorksMessage 是「这一轮一部都没扫到」时给用户的话。
//
// 分两种：点名刮（勾了几部）扫不到那几部，跟整根扫发现库里是空的，原因完全不同 ——
// 前者多半是目录刚被动过、或勾选集是上一次留下的；后者是「这个目录本来就没有 .strm」。
// 文案混用会让用户按错的方向去查。
func emptyWorksMessage(picked bool) string {
	if picked {
		return "选中的作品在当前刮削范围内不存在（可能已被移动或删除）"
	}
	return "当前刮削范围内没有 .strm 文件"
}

func (s *Service) run(ctx context.Context, src *wallSrc, req RunRequest) error {
	root := src.Root
	failures := make([]ScrapeFailure, 0)
	defer func() {
		s.notifyScrapeFailures(src, failures)
	}()
	mode := normalizeWriteMode(s.GetSettings().WriteMode)
	if strings.TrimSpace(req.WriteMode) != "" {
		mode = normalizeWriteMode(req.WriteMode)
	}
	client := s.newTMDBClient()
	if client == nil {
		return domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key，请先在设置中填写")
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
		if serr != nil && os.IsNotExist(serr) {
			return domain.Errorf(domain.CodeValidation, "STRM 输出目录不存在：%s", root)
		}
		return domain.Errorf(domain.CodeValidation, "STRM 输出目录无效：%s", root)
	}
	works, err := scanWorks(root)
	if err != nil {
		return err
	}
	works = filterWorksByScope(works, s.GetScope(src.ScopeKey()).ExcludedDirs)
	picked := len(req.ItemIDs) > 0
	works = filterWorksByIDs(works, req.ItemIDs)
	if len(works) == 0 {
		s.setProgress(func(p *Progress) {
			p.Total = 0
			p.Done = 0
			p.Skipped = 0
			p.Failed = 0
			p.CurrentItemID = ""
			p.Message = emptyWorksMessage(picked)
			p.Error = ""
		})
		return domain.Errorf(domain.CodeValidation, "%s：%s", emptyWorksMessage(picked), root)
	}
	s.setProgress(func(p *Progress) {
		p.Total = len(works)
		p.Done = 0
		p.Skipped = 0
		p.Failed = 0
		p.Message = "扫描完成，按作品开始匹配"
		p.Error = ""
	})

	interval := time.Duration(s.GetSettings().TmdbRequestIntervalMS) * time.Millisecond
	if interval < 200*time.Millisecond {
		interval = 300 * time.Millisecond
	}

	// AI 补认只在「设置里开着 + 源是本地目录 + AI 真的配好了」三条同时成立时开启
	// （原来是「用户点了那颗 AI 按钮」，按钮合并进「开始刮削」之后这个开关挪到了设置里）。
	// 关着时下面的循环**一个分支都不多走** —— 进度、失败计数、日志与改造前逐字相同。
	useAI := s.aiEnabledFor(src)
	// pending 只装「规则没认出来、留给 AI 再试一次」的作品（见 scrape_ai.go）。
	pending := make([]aiPendingWork, 0)

	for i, g := range works {
		if err := ctx.Err(); err != nil {
			return err
		}
		displayName := workDisplayName(g)
		item := buildItem(src.PosterRef(), root, g)
		need := mode == WriteModeOverwrite || workNeedsScrape(g, item.MediaType)
		// 点名刮削（批量选择勾中的那几部）时，勾了却「已经刮过、只是缺剧照/演员/背景图」
		// 的作品改走**补齐**那条路：只补缺、不覆盖已有 nfo 正文与图。
		//
		// 起因：页头那颗「补齐剧照与演员」与这颗按钮管的是同一件事（给存量作品补新抓的
		// 那批数据），两颗并排只会让人猜「该点哪颗」。用户 2026-10-31 拍板合并，页头腾一颗。
		// 并进来的只有**点名**这一档：整根扫照旧只挑没刮过的（那是每次跑全库的路径，
		// 不该悄悄多干活），「覆盖」策略照旧覆盖（`mode != WriteModeOverwrite` 把这条挡掉，
		// 这一条是防御 —— 覆盖模式下 `need` 恒真，本来就到不了这里）。
		//
		// 只认「有 nfo 但缺演员/背景图/剧照」的那批（`backfillNeeded`）。勾中却连 nfo 都
		// 没有的作品不在这儿开工：多半是点名刮扫了一遍没认出来的漏网，混进来会让
		// 「勾了几部只刮几部」的语义变得说不清，照旧跳过。
		if !need && picked && mode != WriteModeOverwrite && backfillNeeded(g, item.MediaType) {
			s.setProgress(func(p *Progress) {
				p.CurrentItemID = item.ID
				p.Message = "正在补齐：" + displayName
			})
			if err := s.backfillOne(ctx, client, root, g, item.MediaType); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				failures = append(failures, s.logScrapeFailure(src, g, displayName, ScrapeFailureStageWrite, err.Error()))
				s.setProgress(func(p *Progress) {
					p.Done = i + 1
					p.Failed++
					p.CurrentItemID = ""
					p.Message = "补齐失败：" + displayName
				})
				time.Sleep(interval)
				continue
			}
			s.upsertIndexItem(ctx, src, root, g)
			updated := buildItem(src.PosterRef(), root, g)
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.CurrentItemID = ""
				p.ItemRevision++
				p.UpdatedItem = &updated
				p.Message = "已补齐：" + displayName
			})
			time.Sleep(interval)
			continue
		}
		if !need {
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.Skipped++
				p.CurrentItemID = ""
			})
			continue
		}
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = item.ID
			p.Message = "正在刮削：" + displayName
		})
		info, matchErr := s.matchWork(ctx, client, g)
		if err := ctx.Err(); err != nil {
			return err
		}
		if matchErr != nil || info == nil || info.TMDBID == "" {
			reason := "未返回有效的 TMDB 匹配结果"
			if matchErr != nil {
				reason = matchErr.Error()
			}
			if useAI {
				// 先记下来，统一问 AI；失败原因原样留着 —— AI 也救不回来时按这条收尾。
				// Done 照常前进（这一部已经处理过了），Failed 等 AI 结果出来再定。
				pending = append(pending, aiPendingWork{
					idx:     i,
					g:       g,
					display: displayName,
					itemID:  item.ID,
					reason:  reason,
				})
				s.setProgress(func(p *Progress) {
					p.Done = i + 1
					p.CurrentItemID = ""
					p.Message = "待 AI 识别：" + displayName
				})
				time.Sleep(interval)
				continue
			}
			failures = append(failures, s.logScrapeFailure(src, g, displayName, ScrapeFailureStageMatch, reason))
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.Failed++
				p.CurrentItemID = ""
				p.Message = "匹配失败：" + displayName
			})
			time.Sleep(interval)
			continue
		}
		title := strings.TrimSpace(info.Title)
		if title == "" {
			title = displayName
		}
		s.setProgress(func(p *Progress) {
			p.Message = "正在刮削：" + title
		})
		if err := s.writeMatched(ctx, client, g, *info, mode == WriteModeOverwrite); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			failures = append(failures, s.logScrapeFailure(src, g, title, ScrapeFailureStageWrite, err.Error()))
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.Failed++
				p.CurrentItemID = ""
				p.Message = "写入失败：" + title
			})
			time.Sleep(interval)
			continue
		}
		s.upsertIndexItem(ctx, src, root, g)
		updated := buildItem(src.PosterRef(), root, g)
		s.setProgress(func(p *Progress) {
			p.Done = i + 1
			p.CurrentItemID = ""
			p.ItemRevision++
			p.UpdatedItem = &updated
			p.Message = "已刮削：" + title
		})
		time.Sleep(interval)
	}
	// 规则没认出来的那批：一次性问 AI，认出来就接着搜 TMDB 并写入（见 scrape_ai.go）。
	// 认不出来 / 搜不到 / AI 整批失败的，按原来那条匹配失败路径收尾。
	s.flushAIPending(ctx, client, src, root, pending, &failures, interval, mode == WriteModeOverwrite)
	// 全量对账一次，去掉已删除作品
	_ = s.RebuildIndex(ctx, src)
	s.setProgress(func(p *Progress) {
		p.CurrentItemID = ""
		p.Message = fmt.Sprintf("完成：成功 %d，跳过 %d，失败 %d", p.Done-p.Skipped-p.Failed, p.Skipped, p.Failed)
	})
	return nil
}

func (s *Service) resolveTask(ctx context.Context, id int64) (*domain.StrmTask, string, error) {
	if s.strm == nil {
		return nil, "", domain.Errorf(domain.CodeInternal, "strm 服务未装配")
	}
	task, err := s.strm.GetTask(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if task == nil {
		return nil, "", domain.Errorf(domain.CodeNotFound, "STRM 任务不存在")
	}
	root := strm.TaskOutputDir(s.strmDir, strm.TaskRelDir(task.GroupDir, task.OutputFolder))
	if root == "" {
		return nil, "", domain.Errorf(domain.CodeValidation, "输出目录无效")
	}
	// 统一成绝对路径，避免 upsert 时 Abs(root) 与相对扫盘的 g.absDir 拼出错误 poster_rel。
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return task, root, nil
}

func (s *Service) setProgress(fn func(*Progress)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.progress)
}
