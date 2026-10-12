package pan123open

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathOfflineDownload         = "/api/v1/offline/download"
	pathOfflineDownloadProcess  = "/api/v1/offline/download/process"
	offlineTaskMissingThreshold = 3
)

type offlineDownloadCreateResp struct {
	TaskID jsonNumber `json:"taskID"`
}

type offlineDownloadProcessResp struct {
	Process float64 `json:"process"`
	Status  int     `json:"status"`
}

// jsonNumber 同时兼容 123 偶尔返回的数字或数字字符串。
type jsonNumber string

func (n *jsonNumber) UnmarshalJSON(data []byte) error {
	*n = jsonNumber(strings.Trim(string(data), `"`))
	return nil
}

// webNumber 是网页接口的数值字段。
//
// ⚠️ 刻意宽松，因为 **123 在同一份响应里混着两种形状**：字节数是小数
// （`276445467.0`）、百分比是小数（`17.47`），而任务号与状态是整数，
// 偶尔还包成字符串。
//
// 早先把 progress 声明成 `int`，整包解析当场失败；而失败被公共层当成
// 「刷新出错」→ 任务永远停在 pending → 「转存记录」里一直显示「处理中」，
// 网盘那边其实早在下了（真机踩到过：17%→34%，列表一动不动）。
// 教训是**任何一处形状变化都不该让整次刷新失败** —— 那会把「一个字段变了」
// 放大成「所有任务都失去状态」，所以这里数字 / 数字字符串 / null 一律收下。
type webNumber float64

func (n *webNumber) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*n = 0
		return nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		raw = strings.TrimSpace(text)
		if raw == "" {
			*n = 0
			return nil
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return err
	}
	*n = webNumber(value)
	return nil
}

func (n webNumber) int() int         { return int(n) }
func (n webNumber) int64() int64     { return int64(n) }
func (n webNumber) float64() float64 { return float64(n) }

func (d *Driver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs:      true,
		SupportsBatchURLs: false,
		SupportsTorrent:   false,
		// 磁力走**网页接口**（开放平台那份 /api/v1/offline/download 只收直链，
		// 而网页版的 v2/offline_download/task/{resolve,submit} 实测收磁力）。
		// 所以这里把 magnet 也列上 —— 但账号没配网页凭据时它其实用不了，
		// 那一刻由 SelectOfflineProvider 降级到内置下载器。
		URLSchemes:        []string{"http", "https", "magnet"},
		RootTargetAllowed: d.rootID() != "0",
		RemoteDelete:      false,
	}
}

func (d *Driver) AddOfflineURLs(ctx context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	if len(req.URLs) != 1 {
		return nil, domain.Errorf(domain.CodeValidation, "123 云盘一次只能提交一个离线下载链接")
	}
	rawURL := strings.TrimSpace(req.URLs[0])
	if rawURL == "" {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接不能为空")
	}
	// 磁力只能走网页接口（见 OfflineDownloadCapabilities 的说明）。
	if isMagnet(rawURL) {
		return d.addWebMagnet(ctx, rawURL, req)
	}
	body := map[string]any{"url": rawURL}
	if fileName := strings.TrimSpace(req.FileName); fileName != "" {
		body["fileName"] = fileName
	}
	parentID := d.normalizeParent(req.ParentID)
	if parentID != "0" {
		id, err := strconv.ParseInt(parentID, 10, 64)
		if err != nil || id <= 0 {
			return nil, domain.Errorf(domain.CodeValidation, "123 离线下载目标目录 ID 不正确")
		}
		body["dirID"] = id
	}
	var result offlineDownloadCreateResp
	if err := d.apiCall(ctx, http.MethodPost, pathOfflineDownload, nil, body, &result); err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(string(result.TaskID))
	if taskID == "" || taskID == "0" {
		return nil, domain.Errorf(domain.CodeDriverError, "123 离线下载响应缺少 taskID")
	}
	return []driver.OfflineAddResult{{
		Source:         rawURL,
		ProviderTaskID: taskID,
		Name:           strings.TrimSpace(req.FileName),
		Success:        true,
		Message:        "已提交到 123 云盘",
	}}, nil
}

// ——————————————————— 磁力（网页接口） ———————————————————

// webMagnetFile 是 resolve 返回的资源里的一个文件。
type webMagnetFile struct {
	ID   webNumber `json:"id"`
	Name string    `json:"name"`
	Size webNumber `json:"size"`
}

type webMagnetResource struct {
	ID     webNumber       `json:"id"`
	Hash   string          `json:"hash"`
	Name   string          `json:"name"`
	Size   webNumber       `json:"size"`
	Result int             `json:"result"`
	Files  []webMagnetFile `json:"files"`
}

type webMagnetResolveData struct {
	List []webMagnetResource `json:"list"`
}

type webMagnetSubmitData struct {
	TaskList []struct {
		TaskID   webNumber `json:"task_id"`
		InfoHash string    `json:"info_hash"`
		Result   int       `json:"result"`
	} `json:"task_list"`
}

type webOfflineTaskListData struct {
	List []struct {
		// ⚠️ 全部用 webNumber 收：123 在同一份响应里混着**小数**（progress 17.47、
		// downloaded 276445467.0）与整数，偶尔还包成字符串。任何一处形状变化
		// 都不该让整包解析失败 —— 那会把「一个字段变了」放大成「所有任务都失去
		// 状态」，表现就是「转存记录里一直处理中」（见 webNumber 的说明）。
		TaskID     webNumber `json:"task_id"`
		Name       string    `json:"name"`
		Status     int       `json:"status"`
		Size       webNumber `json:"size"`
		Downloaded webNumber `json:"downloaded"`
		Progress   webNumber `json:"progress"`
		FileID     string    `json:"file_id"`
	} `json:"list"`
}

// webTaskPrefix 是网页磁力任务号的前缀。
//
// ⚠️ 必须有它：直链那条路走开放平台的 taskID，磁力走网页版的 task_id，
// **两套 id 空间互不相干，还都是纯数字**。刷新时拿到一串数字无从判断该问哪套接口，
// 送错接口只会得到「任务不存在」→ 被判失败。加上前缀之后判据是自明的，
// 而且它跟着 ProviderTaskID 一起落库，重启也不会丢。
const webTaskPrefix = "web:"

func isWebTaskID(id string) bool { return strings.HasPrefix(id, webTaskPrefix) }

// webTaskStatus 把网页版的任务状态翻成公共层的五态。
//
// 数值来自 p123client 的 offline_task_list 文档与实测：
// 0 进行中 / 1 下载失败 / 2 下载成功 / 3 重试中 / 4 等待中。
func webTaskStatus(status int) string {
	switch status {
	case 0:
		return driver.OfflineStatusRunning
	case 1:
		return driver.OfflineStatusFailed
	case 2:
		return driver.OfflineStatusSuccess
	case 3:
		return driver.OfflineStatusRetrying
	case 4:
		return driver.OfflineStatusPending
	default:
		return driver.OfflineStatusPending
	}
}

func webTaskMessage(status int) string {
	switch status {
	case 0:
		return "正在由 123 云盘离线下载"
	case 1:
		return "离线下载失败"
	case 2:
		return "离线下载完成"
	case 3:
		return "123 云盘正在重试离线下载"
	default:
		return "等待 123 云盘处理"
	}
}

// addWebMagnet 用网页接口提交一条磁力：先 resolve 拿文件清单，再 submit。
//
// 两步是接口要求：submit 必须带上 resolve 给的 resource_id 与要下哪几个文件，
// 只给磁力串它不认（实测回「资源文件无效」）。
func (d *Driver) addWebMagnet(ctx context.Context, rawURL string, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	var resolved webMagnetResolveData
	if err := d.webRequest(ctx, http.MethodPost, pathWebTaskRes, nil,
		map[string]any{"urls": rawURL}, &resolved); err != nil {
		return nil, err
	}
	if len(resolved.List) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "123 没能解析这条磁力（可能没有资源或链接不完整）")
	}
	res := resolved.List[0]
	if len(res.Files) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "123 解析出这条磁力，但里面没有可下载的文件")
	}
	fileIDs := make([]int64, 0, len(res.Files))
	for _, f := range res.Files {
		fileIDs = append(fileIDs, f.ID.int64())
	}

	// ⚠️ 网页接口的 upload_dir 是**整数目录 id**，不是开放平台那串字符串。
	var dirID int64
	if parentID := d.normalizeParent(req.ParentID); parentID != "0" {
		id, err := strconv.ParseInt(parentID, 10, 64)
		if err != nil || id <= 0 {
			return nil, domain.Errorf(domain.CodeValidation, "123 离线下载目标目录 ID 不正确")
		}
		dirID = id
	}

	var submitted webMagnetSubmitData
	if err := d.webRequest(ctx, http.MethodPost, pathWebTaskSub, nil, map[string]any{
		"resource_list": []map[string]any{
			{"resource_id": res.ID.int64(), "select_file_id": fileIDs},
		},
		"upload_dir": dirID,
	}, &submitted); err != nil {
		return nil, err
	}
	if len(submitted.TaskList) == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "123 没有返回磁力下载任务")
	}
	task := submitted.TaskList[0]
	if task.TaskID.int64() == 0 {
		return nil, domain.Errorf(domain.CodeDriverError, "123 磁力任务缺少 task_id")
	}

	name := strings.TrimSpace(req.FileName)
	if name == "" {
		name = strings.TrimSpace(res.Name)
	}
	return []driver.OfflineAddResult{{
		Source:         rawURL,
		ProviderTaskID: webTaskPrefix + strconv.FormatInt(task.TaskID.int64(), 10),
		InfoHash:       strings.TrimSpace(task.InfoHash),
		Name:           name,
		Success:        true,
		Message:        "已提交到 123 云盘（网页离线下载）",
	}}, nil
}

// refreshWebMagnetTasks 用网页接口刷磁力任务。
//
// ⚠️ 与直链那条路**用的不是同一套接口**：直链走开放平台的 process、
// 磁力走网页版的 task/list（按 task_id 匹配）。两套 id 空间不同，
// 所以必须分开刷，不能混在一起。
func (d *Driver) refreshWebMagnetTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, bool, error) {
	byID := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if id := strings.TrimSpace(ref.ProviderTaskID); id != "" {
			byID[id] = struct{}{}
		}
	}
	if len(byID) == 0 {
		return nil, true, nil
	}
	var listed webOfflineTaskListData
	if err := d.webRequest(ctx, http.MethodPost, pathWebTaskList, nil, map[string]any{
		"current_page": 1,
		"page_size":    webPageLimit,
		"status_arr":   []int{0, 1, 2, 3, 4},
	}, &listed); err != nil {
		return nil, false, err
	}

	updates := make([]driver.OfflineTaskUpdate, 0, len(refs))
	for _, item := range listed.List {
		key := webTaskPrefix + strconv.FormatInt(item.TaskID.int64(), 10)
		if _, want := byID[key]; !want {
			continue
		}
		updates = append(updates, driver.OfflineTaskUpdate{
			ProviderTaskID: key,
			Status:         webTaskStatus(item.Status),
			Progress:       clampPercent(item.Progress.float64()),
			Size:           item.Size.int64(),
			Name:           strings.TrimSpace(item.Name),
			FileID:         strings.TrimSpace(item.FileID),
			Message:        webTaskMessage(item.Status),
		})
		delete(byID, key)
	}
	// 列表里找不到的（123 只保留最近若干条）判失败，与直链那条路
	// 「任务已在 123 云盘侧移除」同一个处理。
	for key := range byID {
		updates = append(updates, driver.OfflineTaskUpdate{
			ProviderTaskID: key,
			Status:         driver.OfflineStatusFailed,
			Message:        "任务已不在 123 的离线任务列表里（可能已完成后被清理）",
			Error:          "123 磁力任务已不存在",
		})
	}
	return updates, true, nil
}

// clampPercent 把 0~100 的小数进度收成整数百分比。
//
// 取的是**小数进度**（123 回 17.47 这种），不是字节数 —— 所以直接四舍五入即可，
// 别拿 downloaded/size 去算（那两样在任务刚开始时可能都还是 0）。
func clampPercent(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(v + 0.5)
}

func isMagnet(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:?")
}

func (d *Driver) RefreshOfflineTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	updates := make([]driver.OfflineTaskUpdate, 0, len(refs))

	// 先按前缀把网页磁力任务摘出来，交给另一套接口 —— 见 webTaskPrefix 的说明。
	webRefs := make([]driver.OfflineTaskRef, 0, len(refs))
	openRefs := make([]driver.OfflineTaskRef, 0, len(refs))
	for _, ref := range refs {
		if isWebTaskID(strings.TrimSpace(ref.ProviderTaskID)) {
			webRefs = append(webRefs, ref)
			continue
		}
		openRefs = append(openRefs, ref)
	}
	if len(webRefs) > 0 {
		webUpdates, ok, err := d.refreshWebMagnetTasks(ctx, webRefs)
		if err != nil {
			return nil, err
		}
		// ok=false 表示没配网页凭据（或登录失败）：保留原状态，不判失败 ——
		// 那是配置问题，不该把一条其实正常下载的任务钉成失败。
		if ok {
			updates = append(updates, webUpdates...)
		} else {
			for _, ref := range webRefs {
				updates = append(updates, driver.OfflineTaskUpdate{
					ProviderTaskID: strings.TrimSpace(ref.ProviderTaskID),
					Status:         driver.OfflineStatusPending,
					Message:        "等待 123 网页凭据就绪后继续查询",
				})
			}
		}
	}

	for _, ref := range openRefs {
		taskID := strings.TrimSpace(ref.ProviderTaskID)
		if taskID == "" {
			continue
		}
		params := url.Values{"taskID": []string{taskID}}
		var result offlineDownloadProcessResp
		if err := d.apiCall(ctx, http.MethodGet, pathOfflineDownloadProcess, params, nil, &result); err != nil {
			if appErr, ok := domain.AsAppError(err); ok && appErr.Code == domain.CodeNotFound {
				if d.bumpOfflineMissing(taskID) < offlineTaskMissingThreshold {
					updates = append(updates, driver.OfflineTaskUpdate{
						ProviderTaskID: taskID,
						Status:         driver.OfflineStatusPending,
						Message:        "等待 123 云盘同步任务状态",
					})
					continue
				}
				d.clearOfflineMissing(taskID)
				updates = append(updates, driver.OfflineTaskUpdate{
					ProviderTaskID: taskID,
					Status:         driver.OfflineStatusFailed,
					Progress:       0,
					Message:        "任务已在 123 云盘侧移除",
					Error:          "123 离线任务已不存在",
				})
				continue
			}
			return nil, err
		}
		d.clearOfflineMissing(taskID)
		update := driver.OfflineTaskUpdate{
			ProviderTaskID: taskID,
			Progress:       int(result.Process + 0.5),
		}
		switch result.Status {
		case 0:
			update.Status = driver.OfflineStatusRunning
			update.Message = "正在由 123 云盘离线下载"
		case 1:
			update.Status = driver.OfflineStatusFailed
			update.Progress = 0
			update.Message = "离线下载失败"
			update.Error = "123 离线下载失败"
		case 2:
			update.Status = driver.OfflineStatusSuccess
			update.Progress = 100
			update.Message = "离线下载完成"
		case 3:
			update.Status = driver.OfflineStatusRetrying
			update.Message = "123 云盘正在重试离线下载"
		default:
			continue
		}
		updates = append(updates, update)
	}
	return updates, nil
}

func (d *Driver) bumpOfflineMissing(taskID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offlineMissing == nil {
		d.offlineMissing = make(map[string]int)
	}
	d.offlineMissing[taskID]++
	return d.offlineMissing[taskID]
}

func (d *Driver) clearOfflineMissing(taskID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offlineMissing == nil {
		return
	}
	delete(d.offlineMissing, taskID)
}
