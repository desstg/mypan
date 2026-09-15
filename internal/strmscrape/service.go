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
	"litepan/internal/settings"
	"litepan/internal/strm"
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
}

type Service struct {
	strm     *strm.Service
	settings *settings.Service
	bus      *eventbus.Bus
	dataDir  string
	strmDir  string
	log      *slog.Logger

	mu          sync.Mutex
	operationMu sync.Mutex
	progress    Progress
	cancel      context.CancelFunc
	indexLocks  sync.Map // taskID -> *sync.Mutex
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
		strm:     opts.Strm,
		settings: opts.Settings,
		bus:      opts.Bus,
		dataDir:  opts.DataDir,
		strmDir:  strmDir,
		log:      log,
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

func (s *Service) ListItems(ctx context.Context, strmTaskID int64, query ItemListQuery) (ItemListResult, error) {
	query = normalizeItemListQuery(query)
	_, root, err := s.resolveTask(ctx, strmTaskID)
	if err != nil {
		return ItemListResult{}, err
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	var out ItemListResult
	err = s.withTaskIndexLock(strmTaskID, func() error {
		if err := s.ensureIndexLocked(ctx, strmTaskID, root); err != nil {
			return err
		}
		items, err := s.listIndexItems(strmTaskID, query)
		if err != nil {
			return err
		}
		out = items
		return nil
	})
	return out, err
}

// RefreshIndex 扫盘重建索引并返回列表（海报墙刷新按钮）。
func (s *Service) RefreshIndex(ctx context.Context, strmTaskID int64, query ItemListQuery) (ItemListResult, error) {
	query = normalizeItemListQuery(query)
	if err := s.RebuildIndex(ctx, strmTaskID); err != nil {
		return ItemListResult{}, err
	}
	return s.listIndexItems(strmTaskID, query)
}

func (s *Service) RunAsync(ctx context.Context, req RunRequest) error {
	if req.StrmTaskID <= 0 {
		return domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	_ = ctx // 后台任务不随启动请求结束
	return s.startAsyncOperation(req.StrmTaskID, 0, "准备刮削", "刮削完成", "strm scrape failed", func(runCtx context.Context) error {
		return s.run(runCtx, req)
	})
}

func (s *Service) startAsyncOperation(taskID int64, total int, message, doneMessage, logMessage string, run func(context.Context) error) error {
	if !s.operationMu.TryLock() {
		return domain.Errorf(domain.CodeValidation, "刮削任务进行中")
	}
	releaseFiles := func() {}
	if s.strm != nil {
		var ok bool
		releaseFiles, ok = s.strm.TryBeginTaskFileOperation(taskID)
		if !ok {
			s.operationMu.Unlock()
			return domain.Errorf(domain.CodeValidation, "该 STRM 任务正在运行，请稍后再刮削")
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
		TaskID:    taskID,
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
			s.log.Warn(logMessage, "task_id", taskID, "err", err)
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

// Rematch 对同 ID 沿用写入策略，换 ID 时强制覆盖并统一走 writeMatchedOpts。
func (s *Service) Rematch(ctx context.Context, req RematchRequest) (*Item, bool, error) {
	if req.StrmTaskID <= 0 || strings.TrimSpace(req.ItemID) == "" || strings.TrimSpace(req.TMDBID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "参数不完整")
	}
	_, root, err := s.resolveTask(ctx, req.StrmTaskID)
	if err != nil {
		return nil, false, err
	}
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
	current := buildItem(req.StrmTaskID, root, g)
	err = s.startAsyncOperation(req.StrmTaskID, 1, "正在刮削："+display, "已重新匹配："+display, "strm rematch failed", func(runCtx context.Context) error {
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = req.ItemID
		})
		updated, applyErr := s.applyRematch(runCtx, req, root, g, mediaType, overwrite)
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

func (s *Service) applyRematch(ctx context.Context, req RematchRequest, root string, g workGroup, mediaType string, overwrite bool) (*Item, error) {
	client := s.newTMDBClient()
	if client == nil {
		return nil, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	scrapeCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	raw, err := client.Lookup(scrapeCtx, req.TMDBID, mediaType)
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
	_, err = s.writeMatchedOpts(scrapeCtx, client, g, info, overwrite, true)
	if err != nil {
		return nil, err
	}
	clearManualComplete(g)
	s.upsertIndexItem(scrapeCtx, req.StrmTaskID, root, g)
	item := buildItem(req.StrmTaskID, root, g)
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
	_, root, err := s.resolveTask(ctx, req.StrmTaskID)
	if err != nil {
		return nil, err
	}
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
		s.upsertIndexItem(ctx, req.StrmTaskID, root, g)
		item := buildItem(req.StrmTaskID, root, g)
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
	s.upsertIndexItem(ctx, req.StrmTaskID, root, g)
	item := buildItem(req.StrmTaskID, root, g)
	return &item, nil
}

// Rescrape：沿用原 TMDB ID，走与「开始刮削」相同的 writeMatchedOpts（含正片季/finale 集数与 pending）。
func (s *Service) Rescrape(ctx context.Context, req RescrapeRequest) (*Item, bool, error) {
	if req.StrmTaskID <= 0 || strings.TrimSpace(req.ItemID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "参数不完整")
	}
	_, root, err := s.resolveTask(ctx, req.StrmTaskID)
	if err != nil {
		return nil, false, err
	}
	g, err := findWorkByID(root, req.ItemID)
	if err != nil {
		return nil, false, err
	}
	mediaType := resolveWorkMediaType(g)
	meta, ok := readWorkNFOMeta(g, mediaType)
	if _, manual := readManualComplete(g); manual && (!ok || strings.TrimSpace(meta.TMDBID) == "") {
		clearManualComplete(g)
		s.upsertIndexItem(ctx, req.StrmTaskID, root, g)
		item := buildItem(req.StrmTaskID, root, g)
		return &item, false, nil
	}
	if !ok || strings.TrimSpace(meta.TMDBID) == "" {
		return nil, false, domain.Errorf(domain.CodeValidation, "缺少 TMDB ID，请先重新匹配")
	}
	display := strings.TrimSpace(meta.Title)
	if display == "" {
		display = workDisplayName(g)
	}
	current := buildItem(req.StrmTaskID, root, g)
	overwrite := s.overwriteForMatch(true)
	if s.newTMDBClient() == nil {
		return nil, false, domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	err = s.startAsyncOperation(req.StrmTaskID, 1, "正在重新刮削："+display, "已重新刮削："+display, "strm rescrape failed", func(runCtx context.Context) error {
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = req.ItemID
		})
		updated, applyErr := s.applyRematch(runCtx, RematchRequest{
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

func (s *Service) ResolvePosterFile(ctx context.Context, strmTaskID int64, rel string) (string, error) {
	_, root, err := s.resolveTask(ctx, strmTaskID)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean("/" + strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "/")
	full := filepath.Join(root, rel)
	if !isInside(root, full) {
		return "", domain.Errorf(domain.CodeValidation, "非法路径")
	}
	base := strings.ToLower(filepath.Base(full))
	if !strings.HasSuffix(base, ".jpg") && !strings.HasSuffix(base, ".png") && !strings.HasSuffix(base, ".webp") {
		return "", domain.Errorf(domain.CodeValidation, "仅允许图片文件")
	}
	if !fileExists(full) {
		return "", domain.Errorf(domain.CodeNotFound, "海报不存在")
	}
	return full, nil
}

func (s *Service) run(ctx context.Context, req RunRequest) error {
	task, root, err := s.resolveTask(ctx, req.StrmTaskID)
	if err != nil {
		return err
	}
	failures := make([]ScrapeFailure, 0)
	defer func() {
		s.notifyScrapeFailures(task, failures)
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
	works = filterWorksByScope(works, s.GetScope(req.StrmTaskID).ExcludedDirs)
	if len(works) == 0 {
		s.setProgress(func(p *Progress) {
			p.Total = 0
			p.Done = 0
			p.Skipped = 0
			p.Failed = 0
			p.CurrentItemID = ""
			p.Message = "当前刮削范围内没有 .strm 文件"
			p.Error = ""
		})
		return domain.Errorf(domain.CodeValidation, "当前刮削范围内没有 .strm 文件：%s", root)
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

	for i, g := range works {
		if err := ctx.Err(); err != nil {
			return err
		}
		displayName := workDisplayName(g)
		item := buildItem(req.StrmTaskID, root, g)
		need := mode == WriteModeOverwrite || workNeedsScrape(g, item.MediaType)
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
			failures = append(failures, s.logScrapeFailure(task, g, displayName, ScrapeFailureStageMatch, reason))
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
			failures = append(failures, s.logScrapeFailure(task, g, title, ScrapeFailureStageWrite, err.Error()))
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.Failed++
				p.CurrentItemID = ""
				p.Message = "写入失败：" + title
			})
			time.Sleep(interval)
			continue
		}
		s.upsertIndexItem(ctx, req.StrmTaskID, root, g)
		updated := buildItem(req.StrmTaskID, root, g)
		s.setProgress(func(p *Progress) {
			p.Done = i + 1
			p.CurrentItemID = ""
			p.ItemRevision++
			p.UpdatedItem = &updated
			p.Message = "已刮削：" + title
		})
		time.Sleep(interval)
	}
	// 全量对账一次，去掉已删除作品
	_ = s.RebuildIndex(ctx, req.StrmTaskID)
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
