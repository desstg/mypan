package pan115open

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathOfflineAddURLs    = "/open/offline/add_task_urls"
	pathOfflineDelete     = "/open/offline/del_task"
	pathOfflineTaskList   = "/open/offline/get_task_list"
	pathOfflineTorrent    = "/open/offline/torrent"
	pathOfflineAddBT      = "/open/offline/add_task_bt"
	offlineSeedRootName   = "云下载"
	offlineSeedFolderName = "种子文件"
)

type offlineAddURLItem struct {
	State    bool   `json:"state"`
	Code     int64  `json:"code"`
	Message  string `json:"message"`
	InfoHash string `json:"info_hash"`
	URL      string `json:"url"`
}

type offlineTaskPage struct {
	Page      int           `json:"page"`
	PageCount int           `json:"page_count"`
	Count     int           `json:"count"`
	Tasks     []offlineTask `json:"tasks"`
}

type offlineTask struct {
	InfoHash    string           `json:"info_hash"`
	PercentDone flexibleProgress `json:"percentDone"`
	Size        int64            `json:"size"`
	Name        string           `json:"name"`
	FileID      string           `json:"file_id"`
	Status      int              `json:"status"`
	URL         string           `json:"url"`
}

type flexibleProgress int

func (p *flexibleProgress) UnmarshalJSON(data []byte) error {
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		*p = flexibleProgress(int(number))
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		*p = 0
		return nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	*p = flexibleProgress(int(number))
	return nil
}

type offlineTorrentFile struct {
	Size   int64  `json:"size"`
	Path   string `json:"path"`
	Wanted int    `json:"wanted"`
}

type offlineTorrentInfo struct {
	FileSize        int64                `json:"file_size"`
	TorrentName     string               `json:"torrent_name"`
	FileCount       int                  `json:"file_count"`
	InfoHash        string               `json:"info_hash"`
	TorrentFileList []offlineTorrentFile `json:"torrent_filelist"`
}

func (d *Driver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs:      true,
		SupportsBatchURLs: true,
		SupportsTorrent:   true,
		URLSchemes:        []string{"http", "https", "ftp", "magnet", "ed2k"},
		RootTargetAllowed: true,
		RemoteDelete:      true,
	}
}

func (d *Driver) AddOfflineURLs(ctx context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	urls := make([]string, 0, len(req.URLs))
	for _, raw := range req.URLs {
		if value := strings.TrimSpace(raw); value != "" {
			urls = append(urls, value)
		}
	}
	if len(urls) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接不能为空")
	}
	form := urlValues(map[string]string{
		"urls":       strings.Join(urls, "\n"),
		"wp_path_id": d.normalizeParent(req.ParentID),
	})
	var items []offlineAddURLItem
	if err := d.apiCall(ctx, http.MethodPost, pathOfflineAddURLs, nil, form, &items); err != nil {
		return nil, err
	}
	results := make([]driver.OfflineAddResult, 0, len(items))
	for _, item := range items {
		results = append(results, driver.OfflineAddResult{
			Source:   strings.TrimSpace(item.URL),
			InfoHash: strings.TrimSpace(item.InfoHash),
			Success:  item.State && strings.TrimSpace(item.InfoHash) != "",
			Message:  strings.TrimSpace(item.Message),
		})
	}
	d.recoverExistingOfflineHashes(ctx, results)
	return results, nil
}

// 恢复历史 hash / pick_code 兜底。
func (d *Driver) recoverExistingOfflineHashes(ctx context.Context, results []driver.OfflineAddResult) {
	wanted := make(map[string][]int)
	for i, result := range results {
		url := strings.TrimSpace(result.Source)
		if result.Success || strings.TrimSpace(result.InfoHash) != "" || url == "" {
			continue
		}
		wanted[url] = append(wanted[url], i)
	}
	if len(wanted) == 0 {
		return
	}

	for page := 1; ; page++ {
		var result offlineTaskPage
		query := urlValues(map[string]string{"page": strconv.Itoa(page)})
		if err := d.apiCall(ctx, http.MethodGet, pathOfflineTaskList, query, nil, &result); err != nil {
			return
		}
		for _, task := range result.Tasks {
			url := strings.TrimSpace(task.URL)
			indexes := wanted[url]
			hash := strings.TrimSpace(task.InfoHash)
			if len(indexes) == 0 || hash == "" {
				continue
			}
			for _, index := range indexes {
				results[index].InfoHash = hash
			}
			delete(wanted, url)
		}
		if len(wanted) == 0 || result.PageCount <= page || len(result.Tasks) == 0 {
			return
		}
	}
}

func (d *Driver) RefreshOfflineTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	wanted := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if hash := strings.ToLower(strings.TrimSpace(ref.InfoHash)); hash != "" {
			wanted[hash] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	updates := make([]driver.OfflineTaskUpdate, 0, len(wanted))
	page := 1
	for {
		query := urlValues(map[string]string{"page": strconv.Itoa(page)})
		var result offlineTaskPage
		if err := d.apiCall(ctx, http.MethodGet, pathOfflineTaskList, query, nil, &result); err != nil {
			return nil, err
		}
		for _, task := range result.Tasks {
			hash := strings.ToLower(strings.TrimSpace(task.InfoHash))
			if _, ok := wanted[hash]; !ok {
				continue
			}
			update := driver.OfflineTaskUpdate{
				InfoHash: task.InfoHash,
				Progress: int(task.PercentDone),
				Size:     task.Size,
				Name:     task.Name,
				FileID:   task.FileID,
			}
			switch task.Status {
			case -1:
				update.Status = driver.OfflineStatusFailed
				update.Message = "离线下载失败"
				update.Error = "115 离线下载失败"
			case 0:
				update.Status = driver.OfflineStatusPending
				update.Message = "正在分配离线下载资源"
			case 1:
				update.Status = driver.OfflineStatusRunning
				update.Message = "正在由 115 网盘离线下载"
			case 2:
				update.Status = driver.OfflineStatusSuccess
				update.Progress = 100
				update.Message = "离线下载完成"
			default:
				continue
			}
			updates = append(updates, update)
			delete(wanted, hash)
		}
		if len(wanted) == 0 || result.PageCount <= page || len(result.Tasks) == 0 {
			break
		}
		page++
	}
	return updates, nil
}

func (d *Driver) DeleteOfflineTask(ctx context.Context, ref driver.OfflineTaskRef, deleteSourceFile bool) error {
	hash := strings.TrimSpace(ref.InfoHash)
	if hash == "" {
		return domain.Errorf(domain.CodeValidation, "115 离线任务缺少 info_hash")
	}
	delSource := "0"
	if deleteSourceFile {
		delSource = "1"
	}
	return d.apiCall(ctx, http.MethodPost, pathOfflineDelete, nil, urlValues(map[string]string{
		"info_hash":       hash,
		"del_source_file": delSource,
	}), nil)
}

func (d *Driver) PrepareOfflineTorrent(ctx context.Context, localPath, fileName string) (*driver.OfflineTorrentPreparation, error) {
	seedParent, err := d.ensureOfflineSeedFolder(ctx)
	if err != nil {
		return nil, err
	}
	uploaded, err := d.UploadLocalFile(ctx, driver.LocalUploadRequest{
		LocalPath:      localPath,
		FileName:       fileName,
		ParentID:       seedParent,
		ConflictPolicy: "keep_both",
	})
	if err != nil {
		return nil, err
	}
	if uploaded == nil || strings.TrimSpace(uploaded.FileID) == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "BT 种子上传后缺少文件 ID")
	}
	fileSHA1, err := hashFileSHA1(ctx, localPath)
	if err != nil {
		return nil, err
	}
	// 上传后优先查详情补 pick_code，详情暂不可用时回退目录列表。
	_, _ = d.GetFileInfo(ctx, uploaded.FileID)
	pickCode := d.cachedPickCode(uploaded.FileID)
	if pickCode == "" {
		if _, err := d.ListFiles(ctx, seedParent); err != nil {
			return nil, err
		}
		pickCode = d.cachedPickCode(uploaded.FileID)
	}
	if pickCode == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "BT 种子上传后未取得 pick_code")
	}
	var parsed offlineTorrentInfo
	if err := d.apiCall(ctx, http.MethodPost, pathOfflineTorrent, nil, urlValues(map[string]string{
		"torrent_sha1": fileSHA1,
		"pick_code":    pickCode,
	}), &parsed); err != nil {
		return nil, err
	}
	return &driver.OfflineTorrentPreparation{
		TorrentName: parsed.TorrentName,
		TotalSize:   parsed.FileSize,
		InfoHash:    parsed.InfoHash,
		TorrentSHA1: fileSHA1,
		PickCode:    pickCode,
		SeedFileID:  uploaded.FileID,
		Files:       mapOfflineTorrentFiles(parsed.TorrentFileList),
	}, nil
}

func mapOfflineTorrentFiles(items []offlineTorrentFile) []driver.OfflineTorrentFile {
	files := make([]driver.OfflineTorrentFile, 0, len(items))
	for index, item := range items {
		files = append(files, driver.OfflineTorrentFile{
			Index:  index,
			Path:   item.Path,
			Size:   item.Size,
			Wanted: item.Wanted != 0,
		})
	}
	return files
}

func (d *Driver) AddOfflineTorrent(ctx context.Context, req driver.OfflineTorrentRequest) (*driver.OfflineAddResult, error) {
	if len(req.Wanted) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请至少选择一个 BT 文件")
	}
	wanted := make([]string, 0, len(req.Wanted))
	for _, index := range req.Wanted {
		wanted = append(wanted, strconv.Itoa(index))
	}
	savePath := strings.TrimSpace(req.SavePath)
	if savePath == "" {
		savePath = strings.TrimSpace(req.Preparation.TorrentName)
		if strings.HasSuffix(strings.ToLower(savePath), ".torrent") {
			savePath = savePath[:len(savePath)-len(".torrent")]
		}
		if savePath == "" {
			savePath = "离线下载"
		}
	}
	err := d.apiCall(ctx, http.MethodPost, pathOfflineAddBT, nil, urlValues(map[string]string{
		"info_hash":    req.Preparation.InfoHash,
		"wanted":       strings.Join(wanted, ","),
		"save_path":    savePath,
		"torrent_sha1": req.Preparation.TorrentSHA1,
		"pick_code":    req.Preparation.PickCode,
		"wp_path_id":   d.normalizeParent(req.ParentID),
	}), nil)
	if err != nil {
		return nil, err
	}
	return &driver.OfflineAddResult{
		InfoHash: req.Preparation.InfoHash,
		Name:     req.Preparation.TorrentName,
		Success:  true,
		Message:  "BT 任务已提交到 115 网盘",
	}, nil
}

func (d *Driver) ensureOfflineSeedFolder(ctx context.Context) (string, error) {
	rootID := d.normalizeParent("0")
	cloudID, err := d.findOrCreateFolder(ctx, rootID, offlineSeedRootName)
	if err != nil {
		return "", err
	}
	return d.findOrCreateFolder(ctx, cloudID, offlineSeedFolderName)
}

func (d *Driver) findOrCreateFolder(ctx context.Context, parentID, name string) (string, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.IsDir && strings.EqualFold(strings.TrimSpace(item.Name), name) {
			return item.ID, nil
		}
	}
	item, err := d.CreateFolder(ctx, parentID, name)
	if err != nil {
		return "", err
	}
	return item.ID, nil
}
