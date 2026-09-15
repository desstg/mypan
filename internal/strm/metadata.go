package strm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/playback"
)

const (
	metadataCDNConcurrency = 3
	metadataHTTPAttempts   = 3
	metadataResolveRounds  = 3
	metadataClientTimeout  = 5 * time.Minute
	metadataFallbackUA     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

type metadataResolver interface {
	Resolve(ctx context.Context, accountID int64, fileID, ua string, refresh, playback bool) (playback.Resolved, error)
}

type metadataSyncer struct {
	playback   metadataResolver
	failures   *FailureCollector
	client     *http.Client
	onProgress ScanProgressReporter
	resolveMu  sync.Mutex
}

type metadataItem struct {
	fileID        string
	fileName      string
	relDirs       []string
	relPath       string
	legacyRelPath string
	direct        bool
}

type metadataGroup struct {
	fileID string
	items  []metadataItem
}

type metadataDownloadJob struct {
	group    metadataGroup
	resolved playback.Resolved
}

func (m *metadataSyncer) syncFiles(ctx context.Context, accountID int64, root string, items []metadataItem) (int64, error) {
	if m == nil || m.playback == nil || len(items) == 0 {
		return 0, nil
	}
	pending := pendingMetadataItems(root, items, m.failures)
	if len(pending) == 0 {
		return 0, nil
	}
	total := len(pending)
	reportMetadataProgress(m.onProgress, 0, total, "")
	client := m.client
	if client == nil {
		client = &http.Client{Timeout: metadataClientTimeout}
	}

	var stateMu sync.Mutex
	var created int64
	done := 0
	finish := func(item metadataItem, made bool) {
		stateMu.Lock()
		if made {
			created++
		}
		done++
		reportMetadataProgress(m.onProgress, done, total, metadataProgressLabel(item.relPath))
		stateMu.Unlock()
	}
	createdCount := func() int64 {
		stateMu.Lock()
		defer stateMu.Unlock()
		return created
	}

	groups := make([]metadataGroup, 0, len(pending))
	groupIndex := make(map[string]int, len(pending))
	for _, item := range pending {
		if err := ctx.Err(); err != nil {
			return createdCount(), err
		}
		migrated, err := migrateLegacyMetadata(root, item)
		if err != nil {
			m.recordFailure(item.relPath, err.Error())
			finish(item, false)
			continue
		}
		if migrated {
			finish(item, true)
			continue
		}
		key := item.fileID
		if key == "" {
			key = "\x00" + item.relPath
		}
		if index, ok := groupIndex[key]; ok {
			groups[index].items = append(groups[index].items, item)
			continue
		}
		groupIndex[key] = len(groups)
		groups = append(groups, metadataGroup{fileID: item.fileID, items: []metadataItem{item}})
	}

	jobs := make(chan metadataDownloadJob, metadataCDNConcurrency*2)
	var workers sync.WaitGroup
	workers.Add(metadataCDNConcurrency)
	for range metadataCDNConcurrency {
		go func() {
			defer workers.Done()
			for job := range jobs {
				body, err := m.downloadResolvedWithRetry(ctx, client, accountID, job.group.fileID, job.resolved)
				if err != nil {
					if ctx.Err() == nil {
						for _, item := range job.group.items {
							m.recordFailure(item.relPath, err.Error())
							finish(item, false)
						}
					}
					continue
				}
				for _, item := range job.group.items {
					if ctx.Err() != nil {
						break
					}
					written, writeErr := writeMetadataFile(root, item.relPath, body)
					if writeErr != nil {
						m.recordFailure(item.relPath, writeErr.Error())
						finish(item, false)
						continue
					}
					finish(item, written)
				}
			}
		}()
	}

	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			break
		}
		resolved, err := m.resolve(ctx, accountID, group.fileID, false)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			for _, item := range group.items {
				m.recordFailure(item.relPath, err.Error())
				finish(item, false)
			}
			continue
		}
		select {
		case jobs <- metadataDownloadJob{group: group, resolved: resolved}:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return createdCount(), err
	}
	return createdCount(), nil
}

func filterPendingMetadataItems(root string, items []metadataItem) []metadataItem {
	return pendingMetadataItems(root, items, nil)
}

func pendingMetadataItems(root string, items []metadataItem, failures *FailureCollector) []metadataItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]metadataItem, 0, len(items))
	for _, item := range items {
		dest := filepath.Join(root, item.relPath)
		if pathHasOversizedComponent(dest) {
			addOversizedPathFailure(failures, ScanFailureMetadata, item.relPath, false)
			continue
		}
		if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 0 {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (m *metadataSyncer) syncOne(ctx context.Context, client *http.Client, accountID int64, root string, item metadataItem) (created bool, err error) {
	dest := filepath.Join(root, item.relPath)
	if pathHasOversizedComponent(dest) {
		addOversizedPathFailure(m.failures, ScanFailureMetadata, item.relPath, false)
		return false, nil
	}
	if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 0 {
		return false, nil
	}
	if migrated, migrateErr := migrateLegacyMetadata(root, item); migrateErr != nil {
		m.recordFailure(item.relPath, migrateErr.Error())
		return false, nil
	} else if migrated {
		return true, nil
	}
	body, dlErr := m.downloadWithRetry(ctx, client, accountID, item.fileID, 0)
	if dlErr != nil {
		m.recordFailure(item.relPath, dlErr.Error())
		return false, nil
	}
	written, writeErr := writeMetadataFile(root, item.relPath, body)
	if writeErr != nil {
		m.recordFailure(item.relPath, writeErr.Error())
		return false, nil
	}
	return written, nil
}

func migrateLegacyMetadata(root string, item metadataItem) (bool, error) {
	if item.legacyRelPath == "" {
		return false, nil
	}
	legacy := filepath.Join(root, item.legacyRelPath)
	if info, err := os.Stat(legacy); err != nil || info.Size() <= 0 {
		return false, nil
	}
	dest := filepath.Join(root, item.relPath)
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	if err := os.Link(legacy, dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(legacy); err != nil {
		return false, err
	}
	return true, nil
}

func writeMetadataFile(root, relPath string, body []byte) (bool, error) {
	dest := filepath.Join(root, relPath)
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmpPath, dest); err == nil {
		return true, nil
	} else if errors.Is(err, fs.ErrExist) {
		return false, nil
	} else {
		return false, err
	}
}

func (m *metadataSyncer) downloadResolvedWithRetry(
	ctx context.Context,
	client *http.Client,
	accountID int64,
	fileID string,
	resolved playback.Resolved,
) ([]byte, error) {
	var lastErr error
	res := resolved
	for round := 0; round < metadataResolveRounds; round++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if round > 0 {
			refreshed, err := m.resolve(ctx, accountID, fileID, true)
			if err != nil {
				lastErr = err
				continue
			}
			res = refreshed
		}
		size := res.File.Size
		if size <= 0 && res.Link.Size > 0 {
			size = res.Link.Size
		}
		if localPath := strings.TrimSpace(res.Link.LocalPath); localPath != "" {
			body, err := readMetadataLocalFile(ctx, localPath, size)
			if err != nil {
				return nil, fmt.Errorf("读取本地元数据失败: %w", err)
			}
			return body, nil
		}
		if res.Link.URL == "" {
			lastErr = fmt.Errorf("无下载地址")
			continue
		}
		body, err := fetchMetadataURLWithRetry(ctx, client, res.Link.URL, res.Link.Headers, size)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("元数据下载失败")
	}
	return nil, lastErr
}

func readMetadataLocalFile(ctx context.Context, localPath string, expectedSize int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(localPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expectedSize > 0 && int64(len(body)) != expectedSize {
		return nil, fmt.Errorf("文件大小不一致: expected=%d, got=%d", expectedSize, len(body))
	}
	return body, nil
}

func (m *metadataSyncer) downloadWithRetry(ctx context.Context, client *http.Client, accountID int64, fileID string, expectedSize int64) ([]byte, error) {
	res, err := m.resolve(ctx, accountID, fileID, false)
	if err != nil {
		return nil, err
	}
	if expectedSize > 0 {
		res.File.Size = expectedSize
		res.Link.Size = expectedSize
	}
	return m.downloadResolvedWithRetry(ctx, client, accountID, fileID, res)
}

func (m *metadataSyncer) resolve(ctx context.Context, accountID int64, fileID string, refresh bool) (playback.Resolved, error) {
	m.resolveMu.Lock()
	defer m.resolveMu.Unlock()
	return m.playback.Resolve(ctx, accountID, fileID, "", refresh, false)
}

func (m *metadataSyncer) recordFailure(path, reason string) {
	if m.failures != nil {
		m.failures.Add(ScanFailureMetadata, path, reason)
	}
}

func fetchMetadataURLWithRetry(ctx context.Context, client *http.Client, downloadURL string, headers http.Header, expectedSize int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < metadataHTTPAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := fetchMetadataURLOnce(ctx, client, downloadURL, headers, expectedSize)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if isIntegrityMetadataErr(err) {
			return nil, err
		}
		if !isTransientMetadataErr(err) || attempt >= metadataHTTPAttempts-1 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("元数据下载失败")
	}
	return nil, lastErr
}

func prepareMetadataFetchHeaders(src http.Header) http.Header {
	h := src.Clone()
	if h == nil {
		h = make(http.Header)
	}
	if h.Get("Accept") == "" {
		h.Set("Accept", "*/*")
	}
	if h.Get("User-Agent") == "" {
		h.Set("User-Agent", metadataFallbackUA)
	}
	h.Set("Accept-Encoding", "identity")
	h.Set("Connection", "close")
	h.Del("Cache-Control")
	h.Del("Range")
	return h
}

func fetchMetadataURLOnce(ctx context.Context, client *http.Client, downloadURL string, headers http.Header, expectedSize int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range prepareMetadataFetchHeaders(headers) {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 && resp.StatusCode <= 504 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if cl := strings.TrimSpace(resp.Header.Get("Content-Length")); cl != "" {
		if want, convErr := strconv.Atoi(cl); convErr == nil && len(data) != want {
			return nil, fmt.Errorf("Content-Length不一致: expected=%d, got=%d", want, len(data))
		}
	}
	if expectedSize > 0 && int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("文件大小不一致: expected=%d, got=%d", expectedSize, len(data))
	}
	return data, nil
}

func isTransientMetadataErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") {
		return true
	}
	if strings.HasPrefix(msg, "http 5") {
		return true
	}
	return false
}

func isIntegrityMetadataErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "不一致")
}

func metadataRelPath(outputFolder string, relDirs []string, fileName string) string {
	parts := make([]string, 0, len(relDirs)+len(SafeDirSegments(outputFolder))+1)
	parts = append(parts, SafeDirSegments(outputFolder)...)
	for _, dir := range relDirs {
		parts = append(parts, SafeName(dir))
	}
	parts = append(parts, SafeName(fileName))
	return filepath.Join(parts...)
}

func newMetadataItem(fileID, fileName, outputFolder string, relDirs []string) metadataItem {
	return metadataItem{
		fileID:   fileID,
		fileName: fileName,
		relDirs:  append([]string{}, relDirs...),
		relPath:  metadataRelPath(outputFolder, relDirs, fileName),
		direct:   true,
	}
}

func alignMetadataItems(outputFolder string, media []mediaCandidate, items []metadataItem, isoFilenameEnabled bool) []metadataItem {
	if len(items) == 0 || !isoFilenameEnabled {
		return items
	}
	isoStems := make(map[string][]string)
	for _, item := range media {
		if !isISOFileName(item.fileName) {
			continue
		}
		key := dirKey(item.relDirs)
		isoStems[key] = append(isoStems[key], MediaStem(item.fileName))
	}
	if len(isoStems) == 0 {
		return items
	}
	out := make([]metadataItem, len(items))
	for key := range isoStems {
		sort.SliceStable(isoStems[key], func(i, j int) bool {
			return len(isoStems[key][i]) > len(isoStems[key][j])
		})
	}
	for i, item := range items {
		out[i] = item
		for _, stem := range isoStems[dirKey(item.relDirs)] {
			alignedName, changed := alignISOMetadataName(item.fileName, stem)
			if !changed {
				continue
			}
			out[i].legacyRelPath = item.relPath
			out[i].relPath = metadataRelPath(outputFolder, item.relDirs, alignedName)
			out[i].direct = false
			break
		}
	}
	return out
}

func alignISOMetadataName(name, stem string) (string, bool) {
	if len(name) <= len(stem) || !strings.EqualFold(name[:len(stem)], stem) {
		return name, false
	}
	suffix := name[len(stem):]
	lowerSuffix := strings.ToLower(suffix)
	if strings.HasPrefix(lowerSuffix, ".iso.") || strings.HasPrefix(lowerSuffix, ".iso-") {
		return name, false
	}
	if strings.HasPrefix(suffix, ".") {
		return name[:len(stem)] + ".iso" + suffix, true
	}
	for _, prefix := range []string{
		"-poster.", "-cover.", "-default.", "-movie.",
		"-clearart.", "-banner.", "-disc.", "-cdart.",
		"-clearlogo.", "-logo.", "-thumb.", "-landscape.",
	} {
		if strings.HasPrefix(lowerSuffix, prefix) {
			return name[:len(stem)] + ".iso" + suffix, true
		}
	}
	return name, false
}
