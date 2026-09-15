package embyproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
)

const (
	embyMediaInfoPageSize    = 200
	embyMediaInfoConcurrency = 2
	embyMediaInfoRecheckWait = time.Second
	embyMediaInfoDispatchGap = time.Second
)

type CompleteMediaInfoRequest struct {
	ConfigID  string `json:"config_id"`
	Mode      string `json:"mode"`
	LibraryID string `json:"library_id"`
}

type CompleteMediaInfoResult struct {
	ConfigID    string   `json:"config_id"`
	ConfigName  string   `json:"config_name"`
	Mode        string   `json:"mode"`
	LibraryID   string   `json:"library_id,omitempty"`
	LibraryName string   `json:"library_name,omitempty"`
	Scanned     int      `json:"scanned"`
	Missing     int      `json:"missing"`
	Completed   int      `json:"completed"`
	TimedOut    int      `json:"timed_out"`
	Failed      int      `json:"failed"`
	Unchanged   int      `json:"unchanged"`
	FailedItems []string `json:"failed_items,omitempty"`
}

type embyMediaStream struct {
	Type string `json:"Type"`
}

type embyMediaInfoItem struct {
	ID           string            `json:"Id"`
	Name         string            `json:"Name"`
	Path         string            `json:"Path"`
	MediaStreams []embyMediaStream `json:"MediaStreams"`
	MediaSources []struct {
		Path         string            `json:"Path"`
		MediaStreams []embyMediaStream `json:"MediaStreams"`
	} `json:"MediaSources"`
}

type embyMediaInfoPage struct {
	Items            []embyMediaInfoItem `json:"Items"`
	TotalRecordCount int                 `json:"TotalRecordCount"`
}

func (s *Service) CompleteMediaInfo(ctx context.Context, req CompleteMediaInfoRequest) (CompleteMediaInfoResult, error) {
	cfg, err := s.resolveConfig(req.ConfigID)
	if err != nil {
		return CompleteMediaInfoResult{}, err
	}
	if strings.TrimSpace(cfg.EmbyURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		return CompleteMediaInfoResult{}, domain.Errorf(domain.CodeValidation, "请先完善 Emby 地址和 API Key")
	}
	result := CompleteMediaInfoResult{ConfigID: cfg.ID, ConfigName: cfg.Name, Mode: strings.TrimSpace(req.Mode)}
	if result.Mode == "" {
		result.Mode = "global"
	}
	if result.Mode != "global" && result.Mode != "library" {
		return CompleteMediaInfoResult{}, domain.Errorf(domain.CodeValidation, "Emby 执行范围无效")
	}
	if result.Mode == "library" {
		result.LibraryID = strings.TrimSpace(req.LibraryID)
		if result.LibraryID == "" {
			return CompleteMediaInfoResult{}, domain.Errorf(domain.CodeValidation, "请选择 Emby 媒体库")
		}
		libraries, listErr := s.listLibraries(ctx, cfg)
		if listErr != nil {
			return CompleteMediaInfoResult{}, listErr
		}
		for _, library := range libraries {
			if library.ID == result.LibraryID {
				result.LibraryName = library.Name
				break
			}
		}
		if result.LibraryName == "" {
			return CompleteMediaInfoResult{}, domain.Errorf(domain.CodeValidation, "所选 Emby 媒体库不存在")
		}
	}

	base := strings.TrimRight(cfg.EmbyURL, "/")
	recheckItems := make([]embyMediaInfoItem, 0)
	timedOutIDs := make(map[string]bool)
	for start := 0; ; start += embyMediaInfoPageSize {
		page, listErr := s.listMediaInfoItems(ctx, base, cfg.APIKey, result.LibraryID, start)
		if listErr != nil {
			return CompleteMediaInfoResult{}, listErr
		}
		missingItems := make([]embyMediaInfoItem, 0)
		for _, item := range page.Items {
			result.Scanned++
			if mediaInfoComplete(item) {
				continue
			}
			if !mediaInfoProbeSupported(item) {
				continue
			}
			result.Missing++
			missingItems = append(missingItems, item)
		}
		for probe := range s.probeMediaInfoItems(ctx, base, cfg.APIKey, missingItems) {
			if probe.err == nil {
				recheckItems = append(recheckItems, probe.item)
				continue
			}
			if probe.timedOut {
				result.TimedOut++
				timedOutIDs[probe.item.ID] = true
				recheckItems = append(recheckItems, probe.item)
				continue
			}
			result.Failed++
			result.FailedItems = append(result.FailedItems, probe.item.Name)
			s.log.Warn("Emby 补全媒体信息失败", "config_id", cfg.ID, "item_id", probe.item.ID, "item_name", probe.item.Name, "error", probe.err)
		}
		if len(page.Items) < embyMediaInfoPageSize || (page.TotalRecordCount > 0 && start+len(page.Items) >= page.TotalRecordCount) {
			break
		}
	}
	if len(recheckItems) > 0 {
		timer := time.NewTimer(embyMediaInfoRecheckWait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
			completedIDs, recheckErr := s.recheckMediaInfoItems(ctx, base, cfg.APIKey, recheckItems)
			if recheckErr != nil {
				return CompleteMediaInfoResult{}, recheckErr
			} else {
				for _, item := range recheckItems {
					if completedIDs[item.ID] {
						if timedOutIDs[item.ID] {
							result.TimedOut--
						}
						result.Completed++
						continue
					}
					if timedOutIDs[item.ID] {
						s.log.Info("Emby 媒体信息提取仍在处理", "config_id", cfg.ID, "item_id", item.ID, "item_name", item.Name)
					} else {
						result.Unchanged++
						s.log.Info("Emby 未写入媒体信息", "config_id", cfg.ID, "item_id", item.ID, "item_name", item.Name)
					}
				}
			}
		}
	}
	s.log.Info("Emby 补全媒体信息完成", "config_id", cfg.ID, "library_id", result.LibraryID, "scanned", result.Scanned, "missing", result.Missing, "completed", result.Completed, "timed_out", result.TimedOut, "unchanged", result.Unchanged, "failed", result.Failed)
	return result, nil
}

func (s *Service) recheckMediaInfoItems(ctx context.Context, base, apiKey string, items []embyMediaInfoItem) (map[string]bool, error) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
		}
	}
	completed := make(map[string]bool, len(ids))
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch, err := s.recheckMediaInfoBatch(ctx, base, apiKey, ids[start:end])
		if err != nil {
			return nil, err
		}
		for id, ok := range batch {
			completed[id] = ok
		}
	}
	return completed, nil
}

func (s *Service) recheckMediaInfoBatch(ctx context.Context, base, apiKey string, ids []string) (map[string]bool, error) {
	query := url.Values{"api_key": {apiKey}, "Ids": {strings.Join(ids, ",")}, "Fields": {"MediaStreams,MediaSources,Path"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/Items?"+query.Encode(), nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, embyTestHTTPError(resp.StatusCode)
	}
	var page embyMediaInfoPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	completed := make(map[string]bool, len(page.Items))
	for _, item := range page.Items {
		completed[item.ID] = mediaInfoComplete(item)
	}
	return completed, nil
}

func (s *Service) listMediaInfoItems(ctx context.Context, base, apiKey, libraryID string, start int) (embyMediaInfoPage, error) {
	query := url.Values{
		"api_key": {apiKey}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Video,Episode"},
		"Fields": {"MediaStreams,MediaSources,Path"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(embyMediaInfoPageSize)},
	}
	if libraryID != "" {
		query.Set("ParentId", libraryID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/Items?"+query.Encode(), nil)
	if err != nil {
		return embyMediaInfoPage{}, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return embyMediaInfoPage{}, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return embyMediaInfoPage{}, embyTestHTTPError(resp.StatusCode)
	}
	var page embyMediaInfoPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return embyMediaInfoPage{}, domain.Wrap(domain.CodeInternal, err)
	}
	return page, nil
}

func mediaInfoComplete(item embyMediaInfoItem) bool {
	if countNonSubtitleStreams(item.MediaStreams) >= 2 {
		return true
	}
	for _, source := range item.MediaSources {
		if countNonSubtitleStreams(source.MediaStreams) >= 2 {
			return true
		}
	}
	return false
}

func countNonSubtitleStreams(streams []embyMediaStream) int {
	nonSubtitleStreams := 0
	for _, stream := range streams {
		if !strings.EqualFold(strings.TrimSpace(stream.Type), "Subtitle") {
			nonSubtitleStreams++
		}
	}
	return nonSubtitleStreams
}

func mediaInfoProbeSupported(item embyMediaInfoItem) bool {
	paths := []string{item.Path}
	for _, source := range item.MediaSources {
		paths = append(paths, source.Path)
	}
	for _, rawPath := range paths {
		pathLower := strings.ToLower(strings.TrimSpace(rawPath))
		if parsed, err := url.Parse(pathLower); err == nil && parsed.Path != "" {
			pathLower = parsed.Path
		}
		pathLower = strings.TrimSuffix(pathLower, ".strm")
		if strings.HasSuffix(pathLower, ".iso") || strings.Contains(pathLower, "/bdmv/") || strings.Contains(pathLower, "/video_ts/") {
			return false
		}
	}
	return true
}

type mediaInfoProbeResult struct {
	item     embyMediaInfoItem
	timedOut bool
	err      error
}

func (s *Service) probeMediaInfoItems(ctx context.Context, base, apiKey string, items []embyMediaInfoItem) <-chan mediaInfoProbeResult {
	results := make(chan mediaInfoProbeResult, len(items))
	if len(items) == 0 {
		close(results)
		return results
	}
	jobs := make(chan embyMediaInfoItem, len(items))
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	workers := min(embyMediaInfoConcurrency, len(items))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			first := true
			for item := range jobs {
				if !first {
					timer := time.NewTimer(embyMediaInfoDispatchGap)
					select {
					case <-ctx.Done():
						timer.Stop()
					case <-timer.C:
					}
				}
				first = false
				timedOut, err := s.probeMediaInfo(ctx, base, apiKey, item.ID)
				results <- mediaInfoProbeResult{item: item, timedOut: timedOut, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

func (s *Service) probeMediaInfo(ctx context.Context, base, apiKey, itemID string) (bool, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return false, domain.Errorf(domain.CodeValidation, "Emby 条目 ID 为空")
	}
	query := url.Values{"api_key": {apiKey}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/Items/"+url.PathEscape(itemID)+"/PlaybackInfo?"+query, nil)
	if err != nil {
		return false, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return true, fmt.Errorf("Emby 媒体信息提取等待超时")
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return true, fmt.Errorf("Emby 媒体信息提取等待超时")
		}
		return false, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("Emby 返回 HTTP %d", resp.StatusCode)
	}
	return false, nil
}
