package pan115open

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
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
		// ⚠️ 「这颗种子已经在任务列表里」时 115 是**整条响应失败**
		// （`{"state":false,"code":10008,"message":"任务已存在...","data":[]}`），
		// 不是数组里某一项的 state=false —— 所以错误在**信封层**就返回了，
		// 下面那个「按位置回填」的循环根本走不到。
		//
		// 拿提交的 URL 去任务列表里比对一次：认得出就按「已在网盘」处理，
		// 认不出再把原错误抛回去。不补这一步，用户在匹配历史里看到的是
		// 「推送失败：任务已存在」，而文件其实好好地在网盘上。
		if isDuplicateTaskError(err) {
			results := make([]driver.OfflineAddResult, 0, len(urls))
			for _, u := range urls {
				results = append(results, driver.OfflineAddResult{
					Source:  u,
					Message: "任务已存在，请勿输入重复的链接地址",
				})
			}
			d.recoverExistingOfflineHashes(ctx, results)
			for _, r := range results {
				if r.AlreadyExists {
					return results, nil
				}
			}
		}
		return nil, err
	}
	results := make([]driver.OfflineAddResult, 0, len(items))
	for i, item := range items {
		// ⚠️ 115 在**拒收**时（10008 就是这一种）常常**不回 url 字段**，
		// 只给 code 与 message。而下面那段「这颗种子是不是已经在任务列表里」
		// 的兜底要靠 URL/btih 去比对 —— 空的 Source 会让它一条都对不上，
		// 兜底在最需要它的场景静默失效（真机踩到：记录里写着「推送失败：10008」，
		// 而那颗种子好好地在网盘任务列表第 2 页上）。
		//
		// 按**位置**回填：批量提交的响应数组与请求的 urls 一一对应。
		source := strings.TrimSpace(item.URL)
		if source == "" && i < len(urls) {
			source = urls[i]
		}
		results = append(results, driver.OfflineAddResult{
			Source:   source,
			InfoHash: strings.TrimSpace(item.InfoHash),
			Success:  item.State && strings.TrimSpace(item.InfoHash) != "",
			Message:  strings.TrimSpace(item.Message),
		})
	}
	d.recoverExistingOfflineHashes(ctx, results)
	return results, nil
}

// recoverExistingOfflineHashes 把「网盘说这条链接已经有了」的那批结果补上 info hash。
//
// 115 对重复提交回的是 `10008 任务已存在，请勿输入重复的链接地址`，**且不带 hash**。
// 公共层拿到 Success=false 就按失败记账，于是「这颗种子其实早就在网盘上下着/下好了」
// 被记成一次推送失败 —— 真机踩到过：一颗已经在任务列表里 success 的种子，
// 记录上写着 `推送失败：10008`，而文件其实好好地在网盘上。
//
// 补法：拿**任务列表**里的 URL 反查。这里做两次比较：
//
//  1. URL **全等**（原有逻辑）。115 回显的 url 与提交时基本一致，这条最稳。
//  2. URL 匹配不上时，再用 **btih 比对**（新增）。
//
// 为什么必须加第 2 条：115 对同一个种子会回显**不同的 URL 形态** ——
// 真机实测同一个 hash 有两个任务，一个的 url 是裸磁力、
// 另一个带 `&dn=...&xl=...` 一串参数，全等比较直接漏掉。
// 而 10008 恰恰就是「同一颗种子已存在」，漏掉它等于这条兜底在最需要它的场景失效。
//
// 比对用 btih 而不是「整条 URL 归一化」：tracker 参数、dn 顺序、大小写都可能变，
// 而 btih 是种子的真身份。
func (d *Driver) recoverExistingOfflineHashes(ctx context.Context, results []driver.OfflineAddResult) {
	byURL := make(map[string][]int)
	byHash := make(map[string][]int)
	for i, result := range results {
		if result.Success || strings.TrimSpace(result.InfoHash) != "" {
			continue
		}
		source := strings.TrimSpace(result.Source)
		if source == "" {
			continue
		}
		byURL[source] = append(byURL[source], i)
		if hash := btihOfMagnet(source); hash != "" {
			byHash[hash] = append(byHash[hash], i)
		}
		// URL 全等那条也补一个**解码后**的变体：库里存的是 `%3A` 形态，
		// 而 115 回显的是裸 `:`，两者字面不等。不补的话全等比较也会漏。
		if decoded, err := neturl.QueryUnescape(source); err == nil && decoded != source {
			byURL[decoded] = append(byURL[decoded], i)
		}
	}
	if len(byURL) == 0 && len(byHash) == 0 {
		return
	}

	for page := 1; ; page++ {
		var result offlineTaskPage
		query := urlValues(map[string]string{"page": strconv.Itoa(page)})
		if err := d.apiCall(ctx, http.MethodGet, pathOfflineTaskList, query, nil, &result); err != nil {
			// 兜底路径出错不该让整次提交失败（调用方拿到的是「没补上 hash」，
			// 那只是少了一条优化）。驱动没有 logger 注入点，所以这里用
			// fmt 打一行 —— 静默返回过，排查时完全看不出「为什么这颗种子的
			// hash 没补回来」。
			fmt.Fprintf(os.Stderr, "[115] 离线任务列表读取失败，跳过重复种子的 hash 兜底: page=%d err=%v\n", page, err)
			return
		}
		for _, task := range result.Tasks {
			hash := strings.TrimSpace(task.InfoHash)
			if hash == "" {
				continue
			}
			// ① URL 全等
			if url := strings.TrimSpace(task.URL); url != "" {
				for _, index := range byURL[url] {
					results[index].InfoHash = hash
					// 这条兜底**只在**「网盘拒了、但任务列表里找得到同一颗种子」时
					// 生效 —— 正是 10008「任务已存在」的场景。打上标记让公共层
					// 别把它记成一次推送失败（东西本来就在网盘上）。
					results[index].AlreadyExists = true
				}
				delete(byURL, url)
			}
			// ② btih 比对（URL 形态变了也能对上）
			lowerHash := strings.ToLower(hash)
			for _, index := range byHash[lowerHash] {
				results[index].InfoHash = hash
				results[index].AlreadyExists = true
			}
			delete(byHash, lowerHash)
		}
		if len(byURL) == 0 && len(byHash) == 0 {
			return
		}
		if result.PageCount <= page || len(result.Tasks) == 0 {
			return
		}
	}
}

// isDuplicateTaskError 判断一次提交失败是不是「这颗种子已经在任务列表里」。
//
// 115 用 10008 表示它，文案是「任务已存在，请勿输入重复的链接地址」。
// 同时看 code 与文案：code 是契约，文案是兜底 —— 115 改过 code 的话
// 至少文案还能认出来（这一条兜底的收益远大于误判风险：它只影响
// 「要不要去任务列表比对一次」）。
func isDuplicateTaskError(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := domain.AsAppError(err)
	if !ok {
		return false
	}
	if ae.Code != domain.CodeDriverError {
		return false
	}
	msg := ae.Message
	return strings.Contains(msg, "10008") || strings.Contains(msg, "任务已存在")
}

// btihOfMagnet 从磁力链里取小写 hex 的 btih，取不到返回空串。
//
// ⚠️ **必须先还原 URL 编码**。频道里的磁力链在抽取阶段被规范化过，
// 库里与传给驱动的是 `magnet:?xt=urn%3Abtih%3A...` 这种**百分号编码**形态 ——
// 直接找字面量 `urn:btih:` 会永远找不到，兜底静默失效（真机踩到：
// 记录写着「推送失败：10008」，而那颗种子就在网盘任务列表第 2 页上）。
//
// 只认 hex(40) 一种：网盘任务列表里的 info_hash 就是 hex，base32 写法在这里
// 只会引入一次额外的转换与出错面。
func btihOfMagnet(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	// %3a 是 `:` 的百分号编码；有的链会把整段编码、有的只编 `:`，两种都要认。
	lower = strings.ReplaceAll(lower, "%3a", ":")
	idx := strings.Index(lower, "urn:btih:")
	if idx < 0 {
		return ""
	}
	rest := lower[idx+len("urn:btih:"):]
	// 掐到下一个分隔符：`&` `#` 或空白。
	if end := strings.IndexAny(rest, "&# \t\r\n"); end >= 0 {
		rest = rest[:end]
	}
	if len(rest) < 40 {
		return ""
	}
	cand := rest[:40]
	for _, r := range cand {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return cand
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
