package strm

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

type CurrentDirectoryEntry struct {
	ID    string
	Name  string
	Size  int64
	IsDir bool
}

type CurrentDirectoryResult struct {
	MatchedTaskID      int64
	Created            int64
	Updated            int64
	SkippedExisting    int64
	SkippedConflict    int64
	SkippedPathTooLong int64
	Deleted            int64
	MediaCount         int64
	MetadataCreated    int64
	MetadataUploaded   int64
	MetadataDeleted    int64
}

type CurrentDirectoryStatus struct {
	MatchedTaskID   int64
	PendingStrm     int64
	PendingMetadata int64
}

type currentDirWork struct {
	task            *domain.StrmTask
	parentID        string
	relDirs         []string
	outputFolder    string
	root            string
	scanCfg         ScanSettings
	selected        []mediaCandidate
	metadataItems   []metadataItem
	remoteDirNames  map[string]struct{}
	skippedConflict int64
}

func (s *Service) CheckCurrentDirectoryStatus(ctx context.Context, accountID int64, parentID, currentPath string, items []CurrentDirectoryEntry) (CurrentDirectoryStatus, error) {
	var status CurrentDirectoryStatus
	work, err := s.prepareCurrentDirectoryWork(ctx, accountID, parentID, currentPath, items)
	if err != nil {
		return status, err
	}
	if work == nil {
		return status, nil
	}
	status.MatchedTaskID = work.task.ID

	token, err := s.ensureToken(ctx)
	if err != nil {
		return status, err
	}
	baseURL := s.scanBaseURL()
	for _, item := range work.selected {
		relPath := LocalRelPath(work.outputFolder, item.relDirs, item.fileName, work.scanCfg.ISOFilenameEnabled)
		localAbs := filepath.Join(work.root, relPath)
		if pathHasOversizedComponent(localAbs) {
			continue
		}
		url := BuildPlayURL(baseURL, work.task.AccountID, item.fileID, item.fileName, token, s.SignatureEnabled(), s.secret)
		if strmFilePending(work.root, relPath, url, work.task.ScanMode) {
			status.PendingStrm++
		}
	}
	if work.task.SyncMetadata {
		filtered := filterMetadataItems(work.metadataItems, nil, nil, false)
		plan, planErr := buildMetadataSyncPlan(ctx, metadataSyncRequest{
			Root:         work.root,
			OutputFolder: work.outputFolder,
			Mode:         work.scanCfg.MetadataSyncMode,
			Extensions:   parseExtensions(work.scanCfg.MetadataExtensions),
			MaxSizeBytes: metadataMaxBytes(work.scanCfg.MetadataMaxSizeMB),
			RemoteItems:  filtered,
			Directories: map[string]metadataDirectory{
				dirKey(work.relDirs): {parentID: work.parentID, relDirs: work.relDirs},
			},
		})
		if planErr != nil {
			return status, planErr
		}
		status.PendingMetadata = int64(len(plan.downloads) + len(plan.uploads) + len(plan.deletes))
	}
	return status, nil
}

func (s *Service) GenerateCurrentDirectory(ctx context.Context, accountID int64, parentID, currentPath string, items []CurrentDirectoryEntry) (CurrentDirectoryResult, error) {
	var out CurrentDirectoryResult
	if s == nil || s.repo == nil {
		return out, domain.Errf(domain.CodeNotImplement)
	}
	work, err := s.prepareCurrentDirectoryWork(ctx, accountID, parentID, currentPath, items)
	if err != nil {
		return out, err
	}
	if work == nil {
		return out, nil
	}
	releaseFiles, ok := s.TryBeginTaskFileOperation(work.task.ID)
	if !ok {
		return out, domain.Errorf(domain.CodeValidation, "该任务正在运行或刮削元数据，请稍后再试")
	}
	defer releaseFiles()
	out.MatchedTaskID = work.task.ID
	out.MediaCount = int64(len(work.selected))
	out.SkippedConflict = work.skippedConflict

	token, err := s.ensureToken(ctx)
	if err != nil {
		return out, err
	}
	baseURL := s.scanBaseURL()
	ctx = driver.WithExtraAPIDelay(ctx, work.task.ApiInterval)

	seen := make(map[string]struct{}, len(work.selected))
	for _, item := range work.selected {
		relPath := LocalRelPath(work.outputFolder, item.relDirs, item.fileName, work.scanCfg.ISOFilenameEnabled)
		relSlash := filepath.ToSlash(relPath)
		localAbs := filepath.Join(work.root, relPath)
		if pathHasOversizedComponent(localAbs) {
			out.SkippedPathTooLong++
			continue
		}
		seen[relSlash] = struct{}{}
		if _, migrateErr := MigrateLegacyISOStrmFile(work.root, work.outputFolder, item.relDirs, item.fileName, item.fileID, work.scanCfg.ISOFilenameEnabled); migrateErr != nil {
			if s.log != nil {
				s.log.Warn("strm current dir legacy ISO migration failed", "path", relSlash, "err", migrateErr)
			}
			continue
		}

		if work.task.ScanMode == domain.StrmScanModeIncrementalMissing {
			if _, statErr := os.Stat(filepath.Join(work.root, relPath)); statErr == nil {
				out.SkippedExisting++
				continue
			}
		}
		url := BuildPlayURL(baseURL, work.task.AccountID, item.fileID, item.fileName, token, s.SignatureEnabled(), s.secret)
		created, updated, writeErr := writeStrmFile(work.root, relPath, url, work.task.ScanMode)
		if writeErr != nil {
			if s.log != nil {
				s.log.Warn("strm current dir write failed", "path", relSlash, "err", writeErr)
			}
			continue
		}
		switch {
		case created:
			out.Created++
		case updated:
			out.Updated++
		default:
			out.SkippedExisting++
		}
	}

	if work.task.ScanMode == domain.StrmScanModeIncrementalUpdate || work.task.ScanMode == domain.StrmScanModeFullSync {
		removed, cleanErr := cleanupCurrentDirectoryStrm(work.root, work.outputFolder, work.relDirs, seen, work.remoteDirNames)
		if cleanErr != nil {
			return out, cleanErr
		}
		out.Deleted = removed
	}

	if work.task.SyncMetadata {
		filtered := filterMetadataItems(work.metadataItems, nil, nil, false)
		syncResult, syncErr := syncMetadata(ctx, metadataSyncRequest{
			AccountID:    work.task.AccountID,
			Root:         work.root,
			OutputFolder: work.outputFolder,
			Mode:         work.scanCfg.MetadataSyncMode,
			Extensions:   parseExtensions(work.scanCfg.MetadataExtensions),
			MaxSizeBytes: metadataMaxBytes(work.scanCfg.MetadataMaxSizeMB),
			RemoteItems:  filtered,
			Directories: map[string]metadataDirectory{
				dirKey(work.relDirs): {parentID: work.parentID, relDirs: work.relDirs},
			},
			Files:    s.files,
			Playback: s.playback,
		})
		if syncErr != nil {
			return out, syncErr
		}
		out.MetadataCreated = syncResult.Downloaded
		out.MetadataUploaded = syncResult.Uploaded
		out.MetadataDeleted = syncResult.Deleted
	}

	return out, nil
}

func (s *Service) prepareCurrentDirectoryWork(ctx context.Context, accountID int64, parentID, currentPath string, items []CurrentDirectoryEntry) (*currentDirWork, error) {
	if s == nil || s.repo == nil {
		return nil, domain.Errf(domain.CodeNotImplement)
	}
	tasks, err := s.repo.ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	task, relDirs := matchTaskForDisplayPath(tasks, currentPath)
	if task == nil {
		return nil, nil
	}
	outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
	if outputFolder == "" {
		outputFolder = task.Name
	}
	scanCfg := s.scanSettings()
	exts := parseExtensions(task.Extensions)
	if len(exts) == 0 {
		exts = parseExtensions(scanCfg.DefaultExtensions)
	}
	if len(exts) == 0 {
		exts = parseExtensions(defaultExtensions)
	}
	metaExts := parseExtensions(scanCfg.MetadataExtensions)
	minMediaBytes := int64(scanCfg.MinFileSizeMB) * 1024 * 1024
	metaMaxBytes := int64(scanCfg.MetadataMaxSizeMB) * 1024 * 1024
	if scanCfg.MetadataMaxSizeMB <= 0 {
		metaMaxBytes = 0
	}

	var candidates []mediaCandidate
	var metadataItems []metadataItem
	remoteDirNames := make(map[string]struct{})

	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if item.IsDir {
			remoteDirNames[SafeName(name)] = struct{}{}
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		if _, ok := exts[ext]; ok {
			if minMediaBytes > 0 && item.Size < minMediaBytes {
				continue
			}
			if strings.TrimSpace(item.ID) == "" {
				continue
			}
			candidates = append(candidates, mediaCandidate{
				fileID: item.ID, fileName: name, size: item.Size, relDirs: append([]string{}, relDirs...),
			})
			continue
		}
		if task.SyncMetadata && len(metaExts) > 0 {
			if _, ok := metaExts[ext]; ok {
				if metaMaxBytes > 0 && item.Size > metaMaxBytes {
					continue
				}
				if strings.TrimSpace(item.ID) == "" {
					continue
				}
				metadataItems = append(metadataItems, newMetadataItem(item.ID, name, outputFolder, relDirs))
			}
		}
	}

	selected, skippedConflict := selectConflictWinners(candidates, scanCfg.ConflictPolicy)
	metadataItems = alignMetadataItems(outputFolder, selected, metadataItems, scanCfg.ISOFilenameEnabled)

	return &currentDirWork{
		task:            task,
		parentID:        parentID,
		relDirs:         relDirs,
		outputFolder:    outputFolder,
		root:            s.strmDir,
		scanCfg:         scanCfg,
		selected:        selected,
		metadataItems:   metadataItems,
		remoteDirNames:  remoteDirNames,
		skippedConflict: skippedConflict,
	}, nil
}

func metadataMaxBytes(maxMB int) int64 {
	if maxMB <= 0 {
		return 0
	}
	return int64(maxMB) * 1024 * 1024
}

func strmFilePending(root, relPath, url, scanMode string) bool {
	full := filepath.Join(root, relPath)
	if pathHasOversizedComponent(full) {
		return false
	}
	switch scanMode {
	case domain.StrmScanModeIncrementalMissing:
		_, err := os.Stat(full)
		return os.IsNotExist(err)
	default:
		data, err := os.ReadFile(full)
		if err != nil {
			return true
		}
		return strings.TrimSpace(string(data)) != strings.TrimSpace(url)
	}
}

func normalizeDisplayPath(path string) string {
	value := "/" + strings.Trim(strings.TrimSpace(path), "/")
	if value == "/" {
		return "/"
	}
	return strings.TrimRight(value, "/")
}

func relativeDisplayDirs(taskPath, currentPath string) ([]string, bool) {
	taskNorm := normalizeDisplayPath(taskPath)
	currentNorm := normalizeDisplayPath(currentPath)
	if currentNorm == taskNorm {
		return nil, true
	}
	prefix := strings.TrimRight(taskNorm, "/") + "/"
	if !strings.HasPrefix(currentNorm, prefix) {
		return nil, false
	}
	rel := strings.Trim(strings.TrimPrefix(currentNorm, prefix), "/")
	if rel == "" {
		return nil, true
	}
	return strings.Split(rel, "/"), true
}

func matchTaskForDisplayPath(tasks []*domain.StrmTask, currentPath string) (*domain.StrmTask, []string) {
	currentNorm := normalizeDisplayPath(currentPath)
	var best *domain.StrmTask
	var bestRel []string
	bestLen := -1
	for _, task := range tasks {
		if task == nil {
			continue
		}
		rel, ok := relativeDisplayDirs(task.Path, currentNorm)
		if !ok {
			continue
		}
		taskLen := len(normalizeDisplayPath(task.Path))
		if taskLen > bestLen {
			bestLen = taskLen
			best = task
			bestRel = rel
		}
	}
	return best, bestRel
}

func cleanupCurrentDirectoryStrm(root, outputFolder string, relDirs []string, seen map[string]struct{}, remoteDirNames map[string]struct{}) (int64, error) {
	currentLocalDir := localTaskDir(root, outputFolder, relDirs)
	if pathHasOversizedComponent(currentLocalDir) {
		return 0, nil
	}
	if _, err := os.Stat(currentLocalDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	entries, err := os.ReadDir(currentLocalDir)
	if err != nil {
		return 0, err
	}
	var removed int64
	for _, e := range entries {
		if e.IsDir() {
			if _, ok := remoteDirNames[SafeName(e.Name())]; ok {
				continue
			}
			childPath := filepath.Join(currentLocalDir, e.Name())
			n := countStrmFiles(childPath)
			if err := os.RemoveAll(childPath); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed += n
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".strm") {
			continue
		}
		full := filepath.Join(currentLocalDir, e.Name())
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return removed, relErr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := seen[rel]; ok {
			continue
		}
		if err := removeStaleStrmAndSameStemSidecars(full); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
