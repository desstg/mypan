package baiduopen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 百度网盘的**原生离线下载**（云下载）。
//
// 走网页接口 `/rest/2.0/services/cloud_dl`（见 web_transport.go 的说明）：
//
//	add_task   提交任务（form: source_url / save_path / task_from / selected_idx）
//	query_task 精确查状态（task_ids 逗号分隔，最多 100 个）
//	list_task  列全部任务
//	delete_task / cancel_task 删 / 取消
//
// ⚠️ 这条通道**只认网页 Cookie**，开放平台令牌不认 —— 所以 `WebLoginReady()`
// 报的就是「配没配 Cookie」，没配时公共层会把磁力/直链降级到内置下载器。

// OfflineDownloadCapabilities 声明百度能收哪些离线链接。
func (d *Driver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	return driver.OfflineDownloadCapabilities{
		SupportsURLs: true,
		// cloud_dl 的 add_task 一次只收一个 source_url；批量由公共层拆开逐条提交。
		SupportsBatchURLs: false,
		// 不收 .torrent 文件：`query_sinfo` 那条路要的是「已经在网盘里的种子文件」，
		// 而本驱动没有「先传种子再提交」的那条轨（那是 115 的 PrepareOfflineTorrent）。
		// 声明 true 会在界面上放出一个点了必炸的按钮。
		SupportsTorrent: false,
		// 百度收直链、磁力、ed2k、ftp 与迅雷链。
		URLSchemes:        []string{"http", "https", "ftp", "magnet", "ed2k", "thunder"},
		RootTargetAllowed: true,
		// delete_task / cancel_task 都有。
		RemoteDelete: true,
	}
}

// WebLoginReady 报告「需要网页登录态的那部分能力」（离线下载与分享转存）当下能不能用。
//
// 廉价、不发请求：只看配置里有没有网页 Cookie。
// ⚠️ 它影响的不只是磁力 —— 百度的**整条离线通道**都要 bdstoken，所以
// `internal/offlinedownload/types.go` 的 NativeSchemeUsable 里对 baidu_open
// 是「没配 Cookie 就整条不可用」，而不是像 123 那样只挡磁力。
func (d *Driver) WebLoginReady() bool {
	return strings.TrimSpace(d.currentCookie()) != ""
}

// AddOfflineURLs 提交离线下载任务（一次一个链接）。
func (d *Driver) AddOfflineURLs(ctx context.Context, req driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	urls := normalizeOfflineURLs(req.URLs)
	if len(urls) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接不能为空")
	}
	if len(urls) > 1 {
		// 与 OfflineDownloadCapabilities.SupportsBatchURLs=false 一致：
		// 公共层会按这个声明拆批，真走到这里说明调用方绕过了它。
		return nil, domain.Errorf(domain.CodeValidation, "百度网盘一次只能提交一个离线下载链接")
	}

	savePath := d.normalizePath(req.ParentID)
	bdstoken, err := d.panBdstoken(ctx)
	if err != nil {
		return nil, err
	}

	raw := urls[0]
	results := make([]driver.OfflineAddResult, 0, 1)

	// 磁力要先 resolve 出文件列表，再带着 selected_idx 提交 —— 直接 add_task
	// 一颗磁力会被百度拒（它需要知道要下哪些文件）。
	if isMagnetLink(raw) {
		files, err := d.magnetResolve(ctx, bdstoken, raw)
		if err != nil {
			return []driver.OfflineAddResult{{Source: raw, Message: err.Error()}}, nil
		}
		idx := make([]string, 0, len(files))
		for i := range files {
			// ⚠️ selected_idx 是 **1-based** 的。
			idx = append(idx, strconv.Itoa(i+1))
		}
		taskID, err := d.cloudDlAdd(ctx, bdstoken, raw, savePath, map[string]string{
			"type":         "4",
			"t":            strconv.FormatInt(nowMillis(), 10),
			"file_sha1":    "",
			"selected_idx": strings.Join(idx, ","),
			"task_from":    "1",
		})
		if err != nil {
			return []driver.OfflineAddResult{{Source: raw, Name: magnetDisplayName(raw), Message: err.Error()}}, nil
		}
		results = append(results, driver.OfflineAddResult{
			Source:         raw,
			ProviderTaskID: taskID,
			Name:           magnetDisplayName(raw),
			Success:        true,
			Message:        "已提交到百度网盘离线下载",
		})
		return results, nil
	}

	taskID, err := d.cloudDlAdd(ctx, bdstoken, raw, savePath, nil)
	if err != nil {
		return []driver.OfflineAddResult{{Source: raw, Message: err.Error()}}, nil
	}
	results = append(results, driver.OfflineAddResult{
		Source:         raw,
		ProviderTaskID: taskID,
		Success:        true,
		Message:        "已提交到百度网盘离线下载",
	})
	return results, nil
}

// cloudDlAdd 提交一个离线任务，返回 task_id。
func (d *Driver) cloudDlAdd(ctx context.Context, bdstoken, sourceURL, savePath string, extra map[string]string) (string, error) {
	query := url.Values{}
	query.Set("method", "add_task")
	query.Set("app_id", panAppID)
	query.Set("bdstoken", bdstoken)
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("clienttype", "0")

	form := url.Values{}
	form.Set("source_url", sourceURL)
	form.Set("save_path", savePath)
	form.Set("task_from", "0")
	form.Set("selected_idx", "1")
	for k, v := range extra {
		form.Set(k, v)
	}

	var resp struct {
		Errno  json.Number `json:"errno"`
		ErrMsg string      `json:"errmsg"`
		TaskID json.Number `json:"task_id"`
	}
	if err := d.panJSON(ctx, http.MethodPost, pcsBaseURLWeb+"/rest/2.0/services/cloud_dl?"+query.Encode(), form, &resp); err != nil {
		return "", err
	}
	if code, _ := resp.Errno.Int64(); code != 0 {
		return "", mapCloudDlErrno(code, resp.ErrMsg)
	}
	taskID := strings.TrimSpace(resp.TaskID.String())
	if taskID == "" || taskID == "0" {
		return "", domain.Errorf(domain.CodeDriverError, "百度没有返回离线下载任务号")
	}
	return taskID, nil
}

// RefreshOfflineTasks 批量查任务状态。
func (d *Driver) RefreshOfflineTasks(ctx context.Context, refs []driver.OfflineTaskRef) ([]driver.OfflineTaskUpdate, error) {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if id := strings.TrimSpace(ref.ProviderTaskID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > cloudDlQueryLimit {
		ids = ids[:cloudDlQueryLimit]
	}
	bdstoken, err := d.panBdstoken(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("method", "query_task")
	query.Set("app_id", panAppID)
	query.Set("op_type", "1")
	query.Set("task_ids", strings.Join(ids, ","))
	query.Set("bdstoken", bdstoken)
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("clienttype", "0")

	// ⚠️ query_task 的 task_info 是**对象**（id → 任务），而 list_task 的是**数组**。
	// 两者形状不同，别用同一个结构体收 —— 收错只会得到「一个任务都没有」。
	var resp struct {
		Errno    json.Number                  `json:"errno"`
		ErrMsg   string                       `json:"errmsg"`
		TaskInfo map[string]cloudDlTaskRecord `json:"task_info"`
	}
	if err := d.panRequest(ctx, http.MethodGet, pcsBaseURLWeb+"/rest/2.0/services/cloud_dl?"+query.Encode(), nil, &resp); err != nil {
		return nil, err
	}

	updates := make([]driver.OfflineTaskUpdate, 0, len(resp.TaskInfo))
	for id, rec := range resp.TaskInfo {
		updates = append(updates, rec.update(id))
	}
	return updates, nil
}

// DeleteOfflineTask 删除一条离线任务（`delete_task`）。
func (d *Driver) DeleteOfflineTask(ctx context.Context, ref driver.OfflineTaskRef, _ bool) error {
	taskID := strings.TrimSpace(ref.ProviderTaskID)
	if taskID == "" {
		return domain.Errorf(domain.CodeValidation, "缺少离线任务号，无法删除")
	}
	bdstoken, err := d.panBdstoken(ctx)
	if err != nil {
		return err
	}
	query := url.Values{}
	query.Set("method", "delete_task")
	query.Set("app_id", panAppID)
	query.Set("task_id", taskID)
	query.Set("bdstoken", bdstoken)
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("clienttype", "0")

	var resp struct {
		Errno  json.Number `json:"errno"`
		ErrMsg string      `json:"errmsg"`
	}
	if err := d.panRequest(ctx, http.MethodPost, pcsBaseURLWeb+"/rest/2.0/services/cloud_dl?"+query.Encode(), nil, &resp); err != nil {
		return err
	}
	return nil
}

//——————————————————— 磁力 resolve ———————————————————

// cloudDlMagnetFile 是 resolve 出来的一个文件。
type cloudDlMagnetFile struct {
	FileName string      `json:"file_name"`
	FileSize json.Number `json:"file_size"`
	Idx      json.Number `json:"idx"`
}

// magnetResolve 用 `query_magnetinfo` 拿磁力里的文件列表。
func (d *Driver) magnetResolve(ctx context.Context, bdstoken, magnet string) ([]cloudDlMagnetFile, error) {
	query := url.Values{}
	query.Set("method", "query_magnetinfo")
	query.Set("app_id", panAppID)
	query.Set("bdstoken", bdstoken)
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("clienttype", "0")

	form := url.Values{}
	form.Set("source_url", magnet)
	form.Set("type", "4")
	form.Set("save_path", "")

	var resp struct {
		Errno          json.Number         `json:"errno"`
		ErrMsg         string              `json:"errmsg"`
		MagnetInfo     []cloudDlMagnetFile `json:"magnet_info"`
		FileList       []cloudDlMagnetFile `json:"file_list"`
		ServerFileName string              `json:"server_filename"`
	}
	if err := d.panJSON(ctx, http.MethodPost, pcsBaseURLWeb+"/rest/2.0/services/cloud_dl?"+query.Encode(), form, &resp); err != nil {
		return nil, err
	}
	if code, _ := resp.Errno.Int64(); code != 0 {
		return nil, mapCloudDlErrno(code, resp.ErrMsg)
	}
	files := resp.MagnetInfo
	if len(files) == 0 {
		files = resp.FileList
	}
	if len(files) == 0 {
		return nil, domain.Errorf(domain.CodeNotFound,
			"百度没解析出这个磁力里的文件（种子可能没有可用节点，或链接不完整）")
	}
	return files, nil
}

//——————————————————— 任务状态 ———————————————————

// cloudDlTaskRecord 是一条离线任务的原始记录。
//
// ⚠️ 全部用 json.Number 收：百度在同一个字段上用过数字与字符串两种形态
// （`finished_size` 就变过），用 int64 收会让**整包解析失败** ——
// 表现是「所有任务都失去状态」，比一个字段读不出来糟得多。
type cloudDlTaskRecord struct {
	Status       json.Number `json:"status"`
	FileSize     json.Number `json:"file_size"`
	FinishedSize json.Number `json:"finished_size"`
	TaskName     string      `json:"task_name"`
	SavePath     string      `json:"save_path"`
	SourceURL    string      `json:"source_url"`
	FileList     []struct {
		FileName string      `json:"file_name"`
		FileSize json.Number `json:"file_size"`
	} `json:"file_list"`
}

// update 把一条原始记录翻成公共层的任务状态。
func (r cloudDlTaskRecord) update(taskID string) driver.OfflineTaskUpdate {
	status, _ := r.Status.Int64()
	finished := numberToInt64(r.FinishedSize)
	total := numberToInt64(r.FileSize)
	name := strings.TrimSpace(r.TaskName)
	if name == "" && len(r.FileList) > 0 {
		name = strings.TrimSpace(r.FileList[0].FileName)
	}
	return driver.OfflineTaskUpdate{
		ProviderTaskID: taskID,
		Status:         cloudDlStatus(status),
		Progress:       cloudDlProgress(finished, total),
		Size:           total,
		Name:           name,
		Message:        cloudDlStatusText(status),
	}
}

// cloudDlStatus 把百度的 0~7 状态码翻成公共层的五态。
//
// 台账来自 BaiduPCS-Go 的 CloudDlTaskInfo 注释（实测确认）：
//
//	0 下载成功 / 1 下载进行中 / 2 系统错误 / 3 资源不存在
//	4 下载超时 / 5 资源存在但下载失败 / 6 存储空间不足 / 7 任务取消
func cloudDlStatus(status int64) string {
	switch status {
	case 0:
		return driver.OfflineStatusSuccess
	case 1:
		return driver.OfflineStatusRunning
	default:
		return driver.OfflineStatusFailed
	}
}

func cloudDlStatusText(status int64) string {
	switch status {
	case 0:
		return "离线下载完成"
	case 1:
		return "离线下载中"
	case 2:
		return "系统错误"
	case 3:
		return "资源不存在"
	case 4:
		return "下载超时"
	case 5:
		return "资源存在但下载失败"
	case 6:
		return "存储空间不足"
	case 7:
		return "任务已取消"
	}
	return "未知状态（" + strconv.FormatInt(status, 10) + "）"
}

// cloudDlProgress 算进度百分比；总大小拿不到时返回 0。
func cloudDlProgress(finished, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(finished * 100 / total)
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// mapCloudDlErrno 把 cloud_dl 这条路上的错误码翻成人话。
func mapCloudDlErrno(code int64, msg string) error {
	msg = strings.TrimSpace(msg)
	switch code {
	case -6, 31045:
		return domain.Errorf(domain.CodeAuthExpired, "百度认证失败(%d)：%s", code, firstNonEmptyString(msg, "Cookie 可能已失效"))
	case 6, 20012, 31034:
		return domain.Errorf(domain.CodeRateLimited, "百度接口限流(%d)：%s", code, firstNonEmptyString(msg, "请稍后再试"))
	case 1:
		return domain.Errorf(domain.CodeValidation, "百度离线下载参数不正确：%s", firstNonEmptyString(msg, "请检查链接与目标目录"))
	case 2:
		return domain.Errorf(domain.CodeValidation, "百度离线下载暂不可用：%s", firstNonEmptyString(msg, "该账号可能没有离线下载权益"))
	}
	if msg == "" {
		msg = "无错误描述"
	}
	return domain.Errorf(domain.CodeDriverError, "百度离线下载接口错误(%d)：%s", code, msg)
}

//——————————————————— 小工具 ———————————————————

func normalizeOfflineURLs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

func isMagnetLink(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:?")
}

func magnetDisplayName(raw string) string {
	if parsed, err := url.Parse(strings.TrimSpace(raw)); err == nil {
		if name := strings.TrimSpace(parsed.Query().Get("dn")); name != "" {
			return name
		}
	}
	return "磁力链接任务"
}

func numberToInt64(n json.Number) int64 {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return 0
	}
	if v, err := n.Int64(); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(v)
	}
	return 0
}

// nowMillis 是 add_task 的 `t` 参数要的毫秒时间戳。
func nowMillis() int64 { return time.Now().UnixMilli() }

// 离线下载这条路上的常量。
const (
	// cloudDlQueryLimit 是 query_task 一次能查的任务数上限（百度文档与参考实现一致）。
	cloudDlQueryLimit = 100
)
