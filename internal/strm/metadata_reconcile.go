package strm

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	MetadataSyncCloudPrimary  = "cloud_primary"
	MetadataSyncLocalPrimary  = "local_primary"
	MetadataSyncBidirectional = "bidirectional"
)

type metadataFileService interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	UploadLocal(ctx context.Context, accountID int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error)
}

type metadataDirectory struct {
	parentID string
	relDirs  []string
}

type localMetadataItem struct {
	parentID string
	fileName string
	relPath  string
	absPath  string
	size     int64
	modTime  time.Time
}

type metadataSyncPlan struct {
	downloads []metadataItem
	uploads   []localMetadataItem
	deletes   []localMetadataItem
}

type metadataSyncRequest struct {
	AccountID    int64
	Root         string
	OutputFolder string
	Mode         string
	Extensions   map[string]struct{}
	MaxSizeBytes int64
	RemoteItems  []metadataItem
	Directories  map[string]metadataDirectory
	Files        metadataFileService
	Playback     metadataResolver
	Failures     *FailureCollector
	OnProgress   ScanProgressReporter
}

type metadataSyncResult struct {
	Downloaded int64
	Uploaded   int64
	Deleted    int64
}

type metadataSyncMutationKey struct{}

func withMetadataSyncMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, metadataSyncMutationKey{}, true)
}

func isMetadataSyncMutation(ctx context.Context) bool {
	marked, _ := ctx.Value(metadataSyncMutationKey{}).(bool)
	return marked
}

func normalizeMetadataSyncMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case MetadataSyncCloudPrimary:
		return MetadataSyncCloudPrimary
	case MetadataSyncBidirectional:
		return MetadataSyncBidirectional
	default:
		return MetadataSyncLocalPrimary
	}
}

func recordMetadataDirectory(dirs map[string]metadataDirectory, parentID string, relDirs []string) {
	if dirs == nil {
		return
	}
	key := dirKey(relDirs)
	dirs[key] = metadataDirectory{
		parentID: strings.TrimSpace(parentID),
		relDirs:  append([]string{}, relDirs...),
	}
}

func filterMetadataDirectories(
	dirs map[string]metadataDirectory,
	dirHasMedia, subtreeHasMedia map[string]bool,
	parentEnabled bool,
) map[string]metadataDirectory {
	if len(dirs) == 0 {
		return nil
	}
	out := make(map[string]metadataDirectory)
	for key, dir := range dirs {
		if dirHasMedia[key] || (parentEnabled && subtreeHasMedia[key]) {
			out[key] = dir
		}
	}
	return out
}

func syncMetadata(ctx context.Context, req metadataSyncRequest) (metadataSyncResult, error) {
	var result metadataSyncResult
	if len(req.Directories) == 0 || len(req.Extensions) == 0 {
		return result, nil
	}
	reportMetadataActionProgress(req.OnProgress, ScanPhaseMetadataCompare, 0, 0, "")
	plan, err := buildMetadataSyncPlan(ctx, req)
	if err != nil {
		return result, err
	}

	if len(plan.downloads) > 0 && req.Playback != nil {
		syncer := &metadataSyncer{
			playback:   req.Playback,
			failures:   req.Failures,
			onProgress: req.OnProgress,
		}
		result.Downloaded, err = syncer.syncFiles(ctx, req.AccountID, req.Root, plan.downloads)
		if err != nil {
			return result, err
		}
	}

	switch normalizeMetadataSyncMode(req.Mode) {
	case MetadataSyncCloudPrimary:
		if len(pendingMetadataItems(req.Root, plan.downloads, nil)) > 0 {
			return result, nil
		}
		result.Deleted, err = deleteLocalMetadata(ctx, plan.deletes, req.Failures, req.OnProgress)
	case MetadataSyncBidirectional:
		result.Uploaded, err = uploadLocalMetadata(ctx, req.AccountID, plan.uploads, req.Files, req.Failures, req.OnProgress)
	}
	return result, err
}

func buildMetadataSyncPlan(ctx context.Context, req metadataSyncRequest) (metadataSyncPlan, error) {
	var plan metadataSyncPlan
	remotePaths := make(map[string]struct{}, len(req.RemoteItems)*2)
	for _, item := range req.RemoteItems {
		remotePaths[metadataPathKey(item.relPath)] = struct{}{}
		if item.legacyRelPath != "" {
			remotePaths[metadataPathKey(item.legacyRelPath)] = struct{}{}
		}
	}
	plan.downloads = pendingMetadataItems(req.Root, req.RemoteItems, req.Failures)

	keys := make([]string, 0, len(req.Directories))
	for key := range req.Directories {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		dir := req.Directories[key]
		localDir := localTaskDir(req.Root, req.OutputFolder, dir.relDirs)
		entries, err := os.ReadDir(localDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return plan, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !isMetadataExtension(entry.Name(), req.Extensions) {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if req.MaxSizeBytes > 0 && info.Size() > req.MaxSizeBytes {
				continue
			}
			absPath := filepath.Join(localDir, entry.Name())
			relPath, err := filepath.Rel(req.Root, absPath)
			if err != nil || relPath == "." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				continue
			}
			if pathHasOversizedComponent(absPath) {
				continue
			}
			if _, exists := remotePaths[metadataPathKey(relPath)]; exists {
				continue
			}
			item := localMetadataItem{
				parentID: dir.parentID,
				fileName: entry.Name(),
				relPath:  relPath,
				absPath:  absPath,
				size:     info.Size(),
				modTime:  info.ModTime(),
			}
			switch normalizeMetadataSyncMode(req.Mode) {
			case MetadataSyncCloudPrimary:
				plan.deletes = append(plan.deletes, item)
			case MetadataSyncBidirectional:
				plan.uploads = append(plan.uploads, item)
			}
		}
	}
	return plan, nil
}

func metadataPathKey(path string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))))
}

func localMetadataUnchanged(item localMetadataItem) bool {
	info, err := os.Stat(item.absPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return info.Size() == item.size && info.ModTime().Equal(item.modTime)
}

func deleteLocalMetadata(
	ctx context.Context,
	items []localMetadataItem,
	failures *FailureCollector,
	progress ScanProgressReporter,
) (int64, error) {
	if len(items) == 0 {
		return 0, nil
	}
	reportMetadataActionProgress(progress, ScanPhaseMetadataCleanup, 0, len(items), "")
	var deleted int64
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !localMetadataUnchanged(item) {
			recordMetadataFailure(failures, item.relPath, "本地文件在同步期间发生变化，已跳过清理")
			reportMetadataActionProgress(progress, ScanPhaseMetadataCleanup, i+1, len(items), metadataProgressLabel(item.relPath))
			continue
		}
		if err := os.Remove(item.absPath); err != nil {
			if !os.IsNotExist(err) {
				recordMetadataFailure(failures, item.relPath, "清理本地元数据："+err.Error())
			}
		} else {
			deleted++
		}
		reportMetadataActionProgress(progress, ScanPhaseMetadataCleanup, i+1, len(items), metadataProgressLabel(item.relPath))
	}
	return deleted, nil
}

func uploadLocalMetadata(
	ctx context.Context,
	accountID int64,
	items []localMetadataItem,
	files metadataFileService,
	failures *FailureCollector,
	progress ScanProgressReporter,
) (int64, error) {
	if len(items) == 0 {
		return 0, nil
	}
	reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, 0, len(items), "")
	if files == nil {
		for i, item := range items {
			recordMetadataFailure(failures, item.relPath, "当前账号不支持从本地上传元数据")
			reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, i+1, len(items), metadataProgressLabel(item.relPath))
		}
		return 0, nil
	}

	grouped := make(map[string][]localMetadataItem)
	order := make([]string, 0)
	for _, item := range items {
		if _, ok := grouped[item.parentID]; !ok {
			order = append(order, item.parentID)
		}
		grouped[item.parentID] = append(grouped[item.parentID], item)
	}

	var uploaded int64
	done := 0
	for _, parentID := range order {
		if err := ctx.Err(); err != nil {
			return uploaded, err
		}
		remote, err := files.List(ctx, accountID, parentID, true)
		if err != nil {
			for _, item := range grouped[parentID] {
				recordMetadataFailure(failures, item.relPath, "上传前检查云端目录："+err.Error())
				done++
				reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, done, len(items), metadataProgressLabel(item.relPath))
			}
			continue
		}
		existing := make(map[string]struct{}, len(remote))
		for _, item := range remote {
			existing[strings.ToLower(strings.TrimSpace(item.Name))] = struct{}{}
		}
		for _, item := range grouped[parentID] {
			if err := ctx.Err(); err != nil {
				return uploaded, err
			}
			label := metadataProgressLabel(item.relPath)
			if _, ok := existing[strings.ToLower(item.fileName)]; ok {
				done++
				reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, done, len(items), label)
				continue
			}
			if !localMetadataUnchanged(item) {
				recordMetadataFailure(failures, item.relPath, "本地文件在同步期间发生变化，已跳过上传")
				done++
				reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, done, len(items), label)
				continue
			}
			modTime := item.modTime
			got, uploadErr := files.UploadLocal(withMetadataSyncMutation(ctx), accountID, driver.LocalUploadRequest{
				LocalPath:      item.absPath,
				FileName:       item.fileName,
				ParentID:       parentID,
				ConflictPolicy: "skip",
				ModTime:        &modTime,
			})
			if uploadErr != nil {
				recordMetadataFailure(failures, item.relPath, "上传元数据："+uploadErr.Error())
			} else if got != nil && !got.Skipped {
				uploaded++
				existing[strings.ToLower(item.fileName)] = struct{}{}
			}
			done++
			reportMetadataActionProgress(progress, ScanPhaseMetadataUpload, done, len(items), label)
		}
	}
	return uploaded, nil
}

func recordMetadataFailure(failures *FailureCollector, path, reason string) {
	if failures != nil {
		failures.Add(ScanFailureMetadata, filepath.ToSlash(path), strings.TrimSpace(reason))
	}
}
