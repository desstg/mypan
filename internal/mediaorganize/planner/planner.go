package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/classification"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/recognition"
	"litepan/internal/mediaorganize/rules"
)

type Planner struct {
	ctx       context.Context
	files     FileService
	accountID int64
	cfg       TaskConfig
	settings  Settings
	taskID    string
	tmdb      TMDBClient
	log       LogFunc
	progress  ProgressFunc
	stopFn    StopFunc

	actionSeq         int
	actions           []moplan.PlanAction
	skippedItems      []map[string]any
	needsMatch        []map[string]any
	diagnostics       map[string]any
	scannedDirs       int
	scannedDirNames   map[string]string
	scannedDirParents map[string]string
	scannedFiles      int
	processedBatches  int
	currentDir        string
	maxWorksPerRun    int
	plannedWorkCount  int
	quotaReached      bool

	mediaExts         map[string]struct{}
	metaExts          map[string]struct{}
	mediaTagOrder     []string
	alignMediaTags    bool
	actionType        string
	marker            string
	taskMediaType     string
	recursive         bool
	parentID          string
	targetRootID      string
	useTMDB           bool
	overwriteExisting bool
	tmdbLang          string
	tmdbInterval      time.Duration
	tmdbAvailable     bool
	seasonFolderTpl   string
	tvSeasonsCache    map[string][]map[string]any
	recognition       recognition.Enhancer
	classification    classification.Enhancer
	deferred          []deferredGroup
	applyingAI        bool
}

func (p *Planner) SetRecognitionEnhancer(enhancer recognition.Enhancer) {
	p.recognition = enhancer
}

func (p *Planner) SetClassificationEnhancer(enhancer classification.Enhancer) {
	p.classification = enhancer
}

func New(
	ctx context.Context,
	files FileService,
	accountID int64,
	cfg TaskConfig,
	settings Settings,
	taskID string,
	tmdb TMDBClient,
	log LogFunc,
	progress ProgressFunc,
	checkStop StopFunc,
) *Planner {
	if settings == nil {
		settings = Settings{}
	}
	if log == nil {
		log = func(string) {}
	}
	if checkStop == nil {
		checkStop = func() error { return nil }
	}
	p := &Planner{
		ctx:               ctx,
		files:             files,
		accountID:         accountID,
		cfg:               cfg,
		settings:          settings,
		taskID:            taskID,
		tmdb:              tmdb,
		log:               log,
		progress:          progress,
		stopFn:            checkStop,
		actions:           make([]moplan.PlanAction, 0),
		skippedItems:      make([]map[string]any, 0),
		diagnostics:       map[string]any{"groups": []map[string]any{}},
		scannedDirNames:   map[string]string{},
		scannedDirParents: map[string]string{},
		tvSeasonsCache:    map[string][]map[string]any{},
	}
	p.loadSettings()
	return p
}

func (p *Planner) loadSettings() {
	extText := extensionSetting(p.settings, "mo_file_extensions", p.cfg.FileExtensions, rules.DefaultMediaExtensions)
	metaText := extensionSetting(p.settings, "mo_metadata_extensions", p.cfg.MetadataExtensions, rules.DefaultMetadataExtensions)
	p.mediaExts = rules.ParseExtensionSet(extText)
	p.metaExts = rules.ParseExtensionSet(metaText)

	tagOrderRaw := strSetting(p.settings, "mo_media_tag_order", "")
	tagOrderConfigured := false
	if tagOrderRaw != "" {
		var order []string
		if err := json.Unmarshal([]byte(tagOrderRaw), &order); err == nil {
			p.mediaTagOrder = order
			tagOrderConfigured = true
		}
	}
	if !tagOrderConfigured {
		p.mediaTagOrder = rules.DefaultMediaTagOrder
	}
	p.alignMediaTags = rules.SettingBool(p.settings["mo_align_media_tags"], false)

	p.actionType = strings.ToLower(strings.TrimSpace(p.cfg.ActionType))
	if p.actionType == "" {
		p.actionType = "move"
	}
	p.marker = ""
	if p.actionType == "rename" {
		p.marker = strings.TrimSpace(p.cfg.RenameMarker)
	}
	p.taskMediaType = strings.ToLower(strings.TrimSpace(p.cfg.MediaType))
	if p.taskMediaType == "" {
		p.taskMediaType = "auto"
	}
	p.recursive = p.cfg.Recursive
	if !p.cfg.Recursive && p.cfg.TargetDirectoryID != "" {
		p.recursive = true
	}
	p.parentID = strings.TrimSpace(p.cfg.TargetDirectoryID)
	p.targetRootID = strings.TrimSpace(p.cfg.TargetRootID)
	if p.actionType != "move" {
		p.targetRootID = ""
	}
	p.useTMDB = p.cfg.UseTMDB
	p.overwriteExisting = p.cfg.OverwriteExisting || rules.SettingBool(p.settings["mo_overwrite_existing"], false)
	p.tmdbLang = strSetting(p.settings, "mo_tmdb_language", "zh-CN")
	if ms, ok := p.settings["mo_tmdb_request_interval_ms"]; ok {
		if n, err := strconv.Atoi(fmt.Sprint(ms)); err == nil && n > 0 {
			p.tmdbInterval = time.Duration(n) * time.Millisecond
		}
	}
	if p.tmdbInterval <= 0 {
		p.tmdbInterval = 250 * time.Millisecond
	}
	if n, err := strconv.Atoi(fmt.Sprint(p.settings["mo_max_works_per_run"])); err == nil {
		p.maxWorksPerRun = n
	}
	p.seasonFolderTpl = strings.TrimSpace(p.cfg.SeasonFolderTemplate)
	if p.seasonFolderTpl == "" {
		p.seasonFolderTpl = "Season {season:02d}"
	}
}

func extensionSetting(settings Settings, key, taskValue, fallback string) string {
	if raw, ok := settings[key]; ok {
		if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" {
			return value
		}
		return fallback
	}
	if value := strings.TrimSpace(taskValue); value != "" {
		return value
	}
	return fallback
}

func strSetting(settings Settings, key, fallback string) string {
	if v, ok := settings[key]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			return s
		}
	}
	return fallback
}

func (p *Planner) Build() (*moplan.Plan, error) {
	if err := p.validateTMDB(); err != nil {
		return nil, err
	}
	if err := p.checkStop(); err != nil {
		return nil, err
	}
	if p.parentID == "" {
		return p.finalize(), nil
	}
	if err := p.scanAndPlan(p.parentID); err != nil {
		return nil, err
	}
	if err := p.runRecognitionEnhancement(); err != nil {
		return nil, err
	}
	p.tryWholeDirMoveOptimization()
	p.detectSameWorkDirConflicts()
	p.detectTargetNameConflicts()
	p.planEmptyDirCleanup()
	return p.finalize(), nil
}

func (p *Planner) validateTMDB() error {
	if !p.useTMDB {
		p.log("[计划] 任务未启用 TMDB 匹配，仅使用文件名识别")
		p.diagnostics["tmdb_status"] = "disabled_task"
		p.diagnostics["tmdb_api_key_configured"] = false
		return nil
	}
	apiKey := p.tmdbAPIKey()
	p.diagnostics["tmdb_api_key_configured"] = apiKey != ""
	if apiKey == "" {
		p.log("[计划] 未配置 TMDB API Key，仅使用文件名识别")
		p.diagnostics["tmdb_status"] = "no_api_key"
		return nil
	}
	if p.tmdb == nil {
		p.diagnostics["tmdb_status"] = "unreachable"
		return nil
	}
	p.log("[计划] 验证 TMDB 连通性...")
	ok := p.tmdb.ValidateConnection(p.ctx)
	if err := p.checkStop(); err != nil {
		return err
	}
	p.tmdbAvailable = ok
	if ok {
		p.diagnostics["tmdb_status"] = "available"
		p.log("[计划] TMDB 连通正常")
	} else {
		p.diagnostics["tmdb_status"] = "unreachable"
		p.log("[计划] TMDB 无法连通，将跳过 TMDB 匹配（请检查 API Key、网络或代理）")
	}
	return nil
}

func (p *Planner) tmdbAPIKey() string {
	if key := strSetting(p.settings, "mo_tmdb_api_key", ""); key != "" {
		return key
	}
	return strSetting(p.settings, "tmdb_api_key", "")
}

func (p *Planner) finalize() *moplan.Plan {
	if len(p.needsMatch) > 0 {
		p.diagnostics["needs_match"] = append([]map[string]any(nil), p.needsMatch...)
	} else if p.diagnostics != nil {
		p.diagnostics["needs_match"] = []map[string]any{}
	}
	return &moplan.Plan{
		TaskID:         p.taskID,
		CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
		TargetRootID:   p.targetRootID,
		TargetParentID: p.parentID,
		Actions:        append([]moplan.PlanAction(nil), p.actions...),
		Skipped:        append([]map[string]any(nil), p.skippedItems...),
		Diagnostics:    p.diagnostics,
	}
}

func (p *Planner) emitProgress() {
	if p.progress == nil {
		return
	}
	groups, _ := p.diagnostics["groups"].([]map[string]any)
	p.progress(Progress{
		Stage:        "planning",
		ScannedDirs:  p.scannedDirs,
		ScannedFiles: p.scannedFiles,
		Groups:       len(groups),
		Actions:      len(p.actions),
		Skipped:      len(p.skippedItems),
		CurrentDir:   p.currentDir,
		PlannedWorks: p.plannedWorkCount,
		MaxWorks:     p.maxWorksPerRun,
		QuotaReached: p.quotaReached,
	})
}

func (p *Planner) nextID() string {
	p.actionSeq++
	return fmt.Sprintf("a%d", p.actionSeq)
}

func (p *Planner) add(action moplan.PlanAction) moplan.PlanAction {
	p.actions = append(p.actions, action)
	return action
}

func (p *Planner) skip(item domain.FileItem, reason string) {
	p.skippedItems = append(p.skippedItems, map[string]any{
		"file_id":   item.ID,
		"file_name": item.Name,
		"reason":    reason,
	})
}

func (p *Planner) isMedia(item domain.FileItem) bool {
	if item.IsDir || !strings.Contains(item.Name, ".") {
		return false
	}
	_, ok := p.mediaExts[rules.FileExtension(item.Name)]
	return ok
}

func (p *Planner) isCategoryDir(name string, items []domain.FileItem) bool {
	if rules.IsGenericMediaDir(name) {
		return true
	}
	directMedia := 0
	childDirs := 0
	seasonDirs := 0
	rangeDirs := 0
	workDirs := 0
	for _, item := range items {
		if p.isMedia(item) {
			directMedia++
		}
		if item.IsDir {
			childDirs++
			if rules.IsSeasonDirName(item.Name) {
				seasonDirs++
			}
			if rules.IsEpisodeRangeDirName(item.Name) {
				rangeDirs++
			}
			if rules.LooksLikeWorkDirName(item.Name) {
				workDirs++
			}
		}
	}
	if directMedia > 0 {
		return false
	}
	if childDirs < 2 {
		return false
	}
	if seasonDirs > 0 || rangeDirs > 0 {
		return false
	}
	return workDirs >= 2 && float64(workDirs)/float64(childDirs) >= 0.5
}

func (p *Planner) listWithRetry(dirID string) ([]domain.FileItem, error) {
	if err := p.checkStop(); err != nil {
		return nil, err
	}
	items, err := p.files.List(p.ctx, p.accountID, dirID, false)
	if err != nil {
		p.log(fmt.Sprintf("[计划] 目录扫描失败: %s - %v", dirID, err))
		return nil, nil
	}
	p.scannedDirs++
	if p.scannedDirs%5 == 0 {
		p.emitProgress()
	}
	return items, nil
}

func (p *Planner) scanAndPlan(rootID string) error {
	items, err := p.listWithRetry(rootID)
	if err != nil {
		return err
	}
	rootEntries := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			rootEntries = append(rootEntries, batchEntry{item: item})
		}
	}
	if len(rootEntries) > 0 {
		if err := p.planBatch(rootEntries, "根目录文件"); err != nil {
			return err
		}
	}
	if p.quotaReached || !p.recursive {
		return nil
	}
	for _, item := range items {
		if err := p.checkStop(); err != nil {
			return err
		}
		if p.quotaReached {
			return nil
		}
		if item.IsDir {
			if err := p.walkForBatches(item.ID, []rules.Ancestor{{ID: item.ID, Name: item.Name}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Planner) walkForBatches(dirID string, ancestors []rules.Ancestor) error {
	if p.quotaReached {
		return nil
	}
	items, err := p.listWithRetry(dirID)
	if err != nil {
		return err
	}
	if err := p.checkStop(); err != nil {
		return err
	}
	dirName := ""
	if len(ancestors) > 0 {
		dirName = ancestors[len(ancestors)-1].Name
	}
	p.recordDirMeta(ancestors)
	if p.isCategoryDir(dirName, items) {
		scatter := make([]batchEntry, 0)
		for _, item := range items {
			if p.isMedia(item) {
				scatter = append(scatter, batchEntry{item: item, ancestors: cloneAncestors(ancestors)})
			}
		}
		if len(scatter) > 0 {
			label := dirName
			if label == "" {
				label = "分类目录"
			}
			if err := p.planBatch(scatter, label+" 散落文件"); err != nil {
				return err
			}
		}
		for _, child := range items {
			if err := p.checkStop(); err != nil {
				return err
			}
			if p.quotaReached {
				return nil
			}
			if child.IsDir {
				next := append(cloneAncestors(ancestors), rules.Ancestor{ID: child.ID, Name: child.Name})
				if err := p.walkForBatches(child.ID, next); err != nil {
					return err
				}
			}
		}
		return nil
	}
	batch := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			batch = append(batch, batchEntry{item: item, ancestors: cloneAncestors(ancestors)})
		}
	}
	for _, child := range items {
		if !child.IsDir {
			continue
		}
		next := append(cloneAncestors(ancestors), rules.Ancestor{ID: child.ID, Name: child.Name})
		if err := p.collectDescendants(child.ID, next, &batch); err != nil {
			return err
		}
	}
	if len(batch) > 0 {
		return p.planBatch(batch, dirName)
	}
	return nil
}

func (p *Planner) collectDescendants(dirID string, ancestors []rules.Ancestor, out *[]batchEntry) error {
	items, err := p.listWithRetry(dirID)
	if err != nil {
		return err
	}
	p.recordDirMeta(ancestors)
	for _, item := range items {
		if err := p.checkStop(); err != nil {
			return err
		}
		if item.IsDir {
			next := append(cloneAncestors(ancestors), rules.Ancestor{ID: item.ID, Name: item.Name})
			if err := p.collectDescendants(item.ID, next, out); err != nil {
				return err
			}
		} else if p.isMedia(item) {
			*out = append(*out, batchEntry{item: item, ancestors: cloneAncestors(ancestors)})
		}
	}
	return nil
}

func (p *Planner) recordDirMeta(ancestors []rules.Ancestor) {
	if len(ancestors) == 0 {
		return
	}
	cur := ancestors[len(ancestors)-1]
	p.scannedDirNames[cur.ID] = cur.Name
	if len(ancestors) >= 2 {
		p.scannedDirParents[cur.ID] = ancestors[len(ancestors)-2].ID
	} else {
		p.scannedDirParents[cur.ID] = p.parentID
	}
}

func cloneAncestors(in []rules.Ancestor) []rules.Ancestor {
	return append([]rules.Ancestor(nil), in...)
}

var shortTitleRe = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,6}$`)

func (p *Planner) isLowConfidenceGroup(key groupKey, items []batchEntry) bool {
	title := strings.TrimSpace(key.title)
	if title == "" {
		return true
	}
	if key.hasYear {
		return false
	}
	if key.hasSeason || key.hasEpisode {
		return false
	}
	for _, entry := range items {
		if entry.fileParsed.Season != nil || entry.fileParsed.Episode != nil {
			return false
		}
		if entry.fileParsed.Year != nil {
			return false
		}
	}
	if p.findExistingTMDBIDInGroup(items) != "" {
		return false
	}
	if shortTitleRe.MatchString(title) {
		return true
	}
	switch strings.ToLower(title) {
	case "video", "movie", "new folder", "未命名", "未分类", "新建文件夹":
		return true
	}
	return false
}

// groupUIDOf 生成组的稳定标识，供手动匹配绑定使用。
func groupUIDOf(key groupKey) string {
	return fmt.Sprintf("%s|%s|%s|%s", key.mediaKind, key.dirID, key.dirName, key.title)
}
