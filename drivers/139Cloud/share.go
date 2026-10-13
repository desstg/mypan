package cloud139

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// 移动云盘（139）的**分享链转存**。
//
// 与 115/123/夸克/天翼不同，这条通道**不需要任何额外凭据** —— 它用的就是
// 本驱动本来就有的 `Authorization`（Basic 令牌，扫码登录时拿到的），
// 与光鸭同一档。所以 `ShareReceiveCapabilities` 只看令牌在不在。
//
// 三步：
//
//  1. `POST {share-kd-njs}/yun-share/richlifeApp/devapp/IOutLink/getOutLinkInfoV6`
//     —— 列分享目录（`caLst` 文件夹 / `coLst` 文件）；
//  2. `POST .../IBatchOprTask/createOuterLinkBatchOprTask` —— 提交转存任务，
//     收的是**分享侧的路径**（`path`）而不是 id；
//  3. `POST {personal-kd-njs}/hcy/task/get` —— 轮询任务直到落定。
//
// ⚠️ 第 1、2 步走的是 `share-kd-njs.yun.139.com`，**不带 mcloud-sign**；
// 第 3 步走 `personal-kd-njs.yun.139.com`，**要带 mcloud-sign**。两套签名规则
// 不一样（见 shareHeaders / personalSignHeaders），混用会回 9101。

// ShareKinds 报告这个驱动能转存移动云盘自己的分享链。
func (d *Driver) ShareKinds() string { return driver.ShareKind139 }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 判据是「有没有登录令牌 + 能不能解析出手机号」：转存请求体里的 msisdn 就是手机号，
// 少了它服务端会回一句与真实原因无关的错误。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentAuthorization()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该移动云盘账号还没登录（没有 Authorization 令牌），无法转存分享；" +
				"到账号配置里扫码登录即可",
		}
	}
	if strings.TrimSpace(d.currentAccount()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该移动云盘账号的 Authorization 里解析不出手机号（msisdn），无法转存分享；" +
				"请重新扫码登录，或确认填的是网页版完整的 Authorization",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份移动云盘分享整份转存进目标目录。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	linkID := strings.TrimSpace(req.ShareCode)
	if linkID == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	target := strings.TrimSpace(req.TargetCID)
	if target == "" {
		target = "/"
	}
	if strings.TrimSpace(d.currentAccount()) == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation,
			"移动云盘转存需要手机号（msisdn），但当前 Authorization 里解析不出来；请重新扫码登录")
	}

	// ① 目标目录核对。必须在转存**之前** —— 写错目录比写不进去严重得多。
	if err := d.verifyTargetDir(ctx, target, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② 列分享根目录。
	entries, err := d.shareListRoot(ctx, linkID, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(entries) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "移动云盘分享是空的，或已失效")
	}

	// ③ 提交转存任务并等它落定。
	taskID, err := d.shareCreateTask(ctx, linkID, strings.TrimSpace(req.ReceiveCode), entries, target)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if err := d.waitShareTask(ctx, taskID); err != nil {
		return driver.ShareReceiveResult{}, err
	}
	return driver.ShareReceiveResult{Count: len(entries)}, nil
}

// PeekShare 看一眼分享里有什么（不写入任何东西）。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	linkID := strings.TrimSpace(req.ShareCode)
	if linkID == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	entries, err := d.shareListRoot(ctx, linkID, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	res := driver.SharePeekResult{FileCount: len(entries)}
	if len(entries) > 0 {
		res.Title = entries[0].Name
		res.FileName = entries[0].Name
		for _, e := range entries {
			res.TotalSize += e.Size
		}
	}
	return res, nil
}

//——————————————————— 分享侧接口 ———————————————————

// shareEntry 是分享里的一个条目。
//
// ⚠️ `path` 是**转存时要交上去的东西**（见 shareCreateTask 的说明）：
// 139 的转存接口收的不是 id 而是分享内的路径，所以列目录时就得把它带出来。
type shareEntry struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
}

// shareOutLinkReq 是 getOutLinkInfoV6 的请求体。
type shareOutLinkReq struct {
	Account string `json:"account"`
	LinkID  string `json:"linkID"`
	Passwd  string `json:"passwd"`
	PCaID   string `json:"pCaID"`
	CaSrt   int    `json:"caSrt"`
	CoSrt   int    `json:"coSrt"`
	SrtDr   int    `json:"srtDr"`
	BNum    int    `json:"bNum"`
	ENum    int    `json:"eNum"`
}

// shareListRoot 列分享根目录，返回每个条目的**分享内路径**。
//
// parentID 固定 "root"：拿分享侧的子目录 id 去要下一层，139 会返回根那一层
// （与光鸭踩过的坑同族），所以只列根，靠 path 让服务端自己带子树。
func (d *Driver) shareListRoot(ctx context.Context, linkID, passwd string) ([]shareEntry, error) {
	body := map[string]any{
		"getOutLinkInfoReq": shareOutLinkReq{
			Account: d.currentAccount(),
			LinkID:  linkID,
			Passwd:  passwd,
			PCaID:   "root",
			CaSrt:   0,
			CoSrt:   0,
			SrtDr:   1,
			BNum:    1,
			ENum:    sharePageSize,
		},
	}
	var data shareOutLinkData
	if err := d.shareRequest(ctx, pathShareOutLink, body, &data); err != nil {
		return nil, mapShareError(err)
	}

	entries := make([]shareEntry, 0, len(data.CaLst)+len(data.CoLst))
	for _, folder := range data.CaLst {
		name := firstNonEmpty139(folder.CatalogName, folder.CaName)
		path := strings.TrimSpace(folder.Path)
		if strings.TrimSpace(name) == "" || path == "" {
			continue
		}
		entries = append(entries, shareEntry{Name: name, Path: path, IsDir: true})
	}
	for _, file := range data.CoLst {
		name := firstNonEmpty139(file.ContentName, file.CoName)
		path := strings.TrimSpace(file.Path)
		if strings.TrimSpace(name) == "" || path == "" {
			continue
		}
		size, _ := file.CoSize.Int64()
		if size == 0 {
			size, _ = file.Size.Int64()
		}
		entries = append(entries, shareEntry{Name: name, Path: path, Size: size})
	}
	// 备选字段名那一套（有的响应用 catalogList / contentList）。
	for _, folder := range data.CatalogList {
		name := strings.TrimSpace(folder.CatalogName)
		path := strings.TrimSpace(folder.Path)
		if name == "" || path == "" {
			continue
		}
		entries = append(entries, shareEntry{Name: name, Path: path, IsDir: true})
	}
	for _, file := range data.ContentList {
		name := strings.TrimSpace(file.ContentName)
		path := strings.TrimSpace(file.Path)
		if name == "" || path == "" {
			continue
		}
		size, _ := file.Size.Int64()
		entries = append(entries, shareEntry{Name: name, Path: path, Size: size})
	}
	return entries, nil
}

// shareCreateTask 提交转存任务，返回任务号。
func (d *Driver) shareCreateTask(ctx context.Context, linkID, passwd string, entries []shareEntry, targetCatalogID string) (string, error) {
	coPaths := make([]string, 0, len(entries))
	caPaths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			caPaths = append(caPaths, e.Path)
			continue
		}
		coPaths = append(coPaths, e.Path)
	}
	needPassword := strings.TrimSpace(passwd) != ""
	req := map[string]any{
		"createOuterLinkBatchOprTaskReq": map[string]any{
			"msisdn":       d.currentAccount(),
			"ownerAccount": "",
			"taskType":     1,
			"linkID":       linkID,
			"needPassword": needPassword,
			"taskInfo": map[string]any{
				"linkID":          linkID,
				"needPassword":    needPassword,
				"contentInfoList": coPaths,
				"catalogInfoList": caPaths,
				"newCatalogID":    targetCatalogID,
			},
		},
	}
	var data shareCreateTaskData
	if err := d.shareRequest(ctx, pathShareCreateTask, req, &data); err != nil {
		return "", mapShareError(err)
	}
	taskID := firstNonEmpty139(data.TaskID, data.TaskIDAlt)
	if taskID == "" {
		return "", domain.Errorf(domain.CodeDriverError, "移动云盘没有返回转存任务号")
	}
	return taskID, nil
}

// waitShareTask 轮询转存任务直到落定。
//
// ⚠️ 走的是**个人云**接口（要 mcloud-sign），与分享侧那套签名不同 ——
// 这也正是把两步拆成两个方法的理由。
func (d *Driver) waitShareTask(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	deadline := time.Now().Add(shareTaskTimeout)
	for {
		var data taskGetData
		err := d.personalShareRequest(ctx, pathTaskGet, map[string]any{"taskId": taskID}, &data)
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(data.status())) {
		case "success", "2", "finished", "completed":
			return nil
		case "failed", "3", "error":
			return domain.Errorf(domain.CodeDriverError, "移动云盘转存失败：%s",
				firstNonEmpty139(data.Message, "任务失败"))
		}
		if time.Now().After(deadline) {
			return domain.Errorf(domain.CodeDriverError,
				"移动云盘转存任务超时未完成（taskId=%s），请稍后到网盘里确认文件是否已到", taskID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(shareTaskPollInterval):
		}
	}
}

//——————————————————— 网页通道 ———————————————————

// shareRequest 打一次**分享侧**接口（`share-kd-njs`，不带 mcloud-sign）。
func (d *Driver) shareRequest(ctx context.Context, path string, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	return d.doShareJSON(ctx, shareHostBase+path, shareHeaders(d.currentAuthorization()), body, out)
}

// personalShareRequest 打一次**个人云**接口（要 mcloud-sign）。
//
// 与 transport.go 的 signedRequest 同一套签名，但宿主不同（personal-kd-njs）
// 而且不经过 route policy —— 139 的转存任务查询就落在这台上。
func (d *Driver) personalShareRequest(ctx context.Context, path string, body, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	host := d.personalShareHost()
	return d.doShareJSON(ctx, host+path, personalSignHeaders(d.currentAuthorization()), body, out)
}

// personalShareHost 是 personal-kd-njs 的宿主。单独拎成变量只为让测试指向 httptest。
var personalShareHostURL = "https://personal-kd-njs.yun.139.com"

func (d *Driver) personalShareHost() string { return personalShareHostURL }

// shareHostBase 是分享侧接口的宿主。同样只为测试可替换。
var shareHostBase = "https://share-kd-njs.yun.139.com"

// doShareJSON 发一个 JSON POST，并解 139 的统一外壳。
func (d *Driver) doShareJSON(ctx context.Context, rawURL string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(payload)))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errorf(domain.CodeAuthExpired, "移动云盘认证已过期，请重新登录")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.Errf(domain.CodeRateLimited)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return domain.Errorf(domain.CodeDriverError, "移动云盘 API HTTP %d: %s", resp.StatusCode, truncate139(data, 300))
	}
	var env apiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Success != nil && !*env.Success {
		return mapAPIError(env.Code.String(), env.Message)
	}
	if out != nil {
		if len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return domain.Wrap(domain.CodeDriverError, err)
			}
			return nil
		}
		// 有的响应根本没有 data 包装（分享侧就有），退回整包解。
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

// shareHeaders 是分享侧接口的固定头（不含 mcloud-sign）。
func shareHeaders(authorization string) map[string]string {
	return map[string]string{
		"Accept":                 "application/json, text/plain, */*",
		"Authorization":          "Basic " + normalizeAuthorization(authorization),
		"caller":                 "web",
		"x-m4c-caller":           "PC",
		"x-m4c-src":              "10002",
		"x-inner-ntwk":           "2",
		"mcloud-route":           "001",
		"mcloud-version":         "7.17.2",
		"mcloud-channel":         "1000101",
		"mcloud-client":          "10701",
		"INNER-HCY-ROUTER-HTTPS": "1",
		"Origin":                 webOrigin,
		"Referer":                webOrigin + "/",
		"User-Agent":             userAgent,
	}
}

// personalSignHeaders 是个人云接口的固定头（含 mcloud-sign）。
func personalSignHeaders(authorization string) map[string]string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	randomValue, err := randomString(16)
	if err != nil {
		randomValue = "0000000000000000"
	}
	headers := map[string]string{
		"Accept":                 "application/json, text/plain, */*",
		"Authorization":          "Basic " + normalizeAuthorization(authorization),
		"CMS-DEVICE":             "default",
		"Inner-Hcy-Router-Https": "1",
		"Caller":                 "web",
		"mcloud-channel":         "1000101",
		"mcloud-client":          "10701",
		"mcloud-route":           "001",
		"mcloud-sign":            ts + "," + randomValue + "," + calcSign("", ts, randomValue),
		"mcloud-version":         "7.14.0",
		"Origin":                 webOrigin,
		"Referer":                webOrigin + "/w/",
		"User-Agent":             userAgent,
		"x-SvcType":              "1",
		"x-yun-api-version":      "v1",
	}
	return headers
}

//——————————————————— 目标目录核对 ———————————————————

// verifyTargetDir 确认目录 id 真的指向 expectPath。
//
// expectPath 为空时跳过（调用方没要求核对）。与另几家同一条原则：
// 转存会真往用户网盘里写东西，**写错目录比写不进去严重得多**。
func (d *Driver) verifyTargetDir(ctx context.Context, dirID, expectPath string) error {
	expectPath = strings.TrimSpace(expectPath)
	root := d.rootID()
	if expectPath == "" || dirID == root || dirID == "0" || dirID == "/" {
		return nil
	}
	actual, err := d.dirFullPath(ctx, dirID)
	if err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在移动云盘里读不出来：%v；请重新选择目标目录", dirID, err)
	}
	if !sameDirPath(actual, expectPath) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：移动云盘里 %s 是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			dirID, actual, expectPath)
	}
	return nil
}

// dirFullPath 从目录 id 往上拼出完整路径（`/a/b`），根目录返回 `/`。
//
// ⚠️ 139 个人云的目录接口**不返回父目录 id**（参考实现里也只有 family/group
// 那两套带路径），所以这里靠 `ListFiles` 逐层往下找：从根开始，看名字在哪一层
// 命中，拼出它所在的路径。找不到时返回错误 —— 宁可不转存，也不能把文件
// 塞进一个没核对过的目录。
func (d *Driver) dirFullPath(ctx context.Context, dirID string) (string, error) {
	target := d.normalizeParent(dirID)
	root := d.rootID()
	type frame struct {
		id   string
		path string
	}
	queue := []frame{{id: root, path: ""}}
	seen := map[string]struct{}{root: {}}
	for depth := 0; len(queue) > 0 && depth < max139PathDepth; depth++ {
		next := make([]frame, 0, len(queue))
		for _, cur := range queue {
			items, err := d.ListFiles(ctx, cur.id)
			if err != nil {
				return "", err
			}
			for _, item := range items {
				if !item.IsDir {
					continue
				}
				path := cur.path + "/" + item.Name
				if item.ID == target {
					return normalizeDirPath(path), nil
				}
				if _, ok := seen[item.ID]; ok {
					continue
				}
				seen[item.ID] = struct{}{}
				next = append(next, frame{id: item.ID, path: path})
			}
		}
		queue = next
	}
	return "", domain.Errorf(domain.CodeNotFound, "在移动云盘里找不到目录 %s（可能已被删除或移动）", dirID)
}

//——————————————————— 数据形状 ———————————————————

// shareOutLinkData 是 getOutLinkInfoV6 的 data。
//
// ⚠️ 字段名在参考实现里有过两套（`caLst/coLst` 与 `catalogList/contentList`），
// 所以每个字段都收两个候选名 —— 一套对不上时另一套还能用。
type shareOutLinkData struct {
	CaLst []struct {
		CatalogID   string `json:"catalogID"`
		CaID        string `json:"caID"`
		CatalogName string `json:"catalogName"`
		CaName      string `json:"caName"`
		Path        string `json:"path"`
	} `json:"caLst"`
	CoLst []struct {
		ContentID   string      `json:"contentID"`
		CoID        string      `json:"coID"`
		ContentName string      `json:"contentName"`
		CoName      string      `json:"coName"`
		Path        string      `json:"path"`
		CoSize      json.Number `json:"coSize"`
		Size        json.Number `json:"size"`
	} `json:"coLst"`
	CatalogList []struct {
		CatalogID   string `json:"catalogID"`
		CatalogName string `json:"catalogName"`
		Path        string `json:"path"`
	} `json:"catalogList"`
	ContentList []struct {
		ContentID   string      `json:"contentID"`
		ContentName string      `json:"contentName"`
		Path        string      `json:"path"`
		Size        json.Number `json:"size"`
	} `json:"contentList"`
}

// shareCreateTaskData 是 createOuterLinkBatchOprTask 的 data。
type shareCreateTaskData struct {
	TaskID    string `json:"taskID"`
	TaskIDAlt string `json:"taskId"`
}

// taskGetData 是 hcy/task/get 的 data。
type taskGetData struct {
	TaskID    string `json:"taskId"`
	Status    string `json:"status"`
	StatusAlt string `json:"taskStatus"`
	Message   string `json:"message"`
}

func (t taskGetData) status() string { return firstNonEmpty139(t.Status, t.StatusAlt) }

//——————————————————— 小工具 ———————————————————

// mapShareError 把分享这条路上的错误翻成人话。
func mapShareError(err error) error {
	if err == nil {
		return nil
	}
	ae, ok := domain.AsAppError(err)
	if !ok {
		return err
	}
	msg := ae.Message
	switch {
	case strings.Contains(msg, "分享") && (strings.Contains(msg, "不存在") ||
		strings.Contains(msg, "失效") || strings.Contains(msg, "取消") || strings.Contains(msg, "过期")):
		return domain.Errorf(domain.CodeNotFound, "移动云盘分享不存在、已失效或被取消：%s", msg)
	case strings.Contains(msg, "密码") || strings.Contains(msg, "口令") || strings.Contains(msg, "提取码"):
		return domain.Errorf(domain.CodeValidation, "移动云盘分享提取码不正确：%s", msg)
	}
	return err
}

// truncate139 截断一段响应体用于报错，避免把整页 HTML 塞进错误信息。
func truncate139(data []byte, n int) string { return httpx.Truncate(data, n) }

func firstNonEmpty139(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sameDirPath 比两条目录路径是否指向同一个地方（忽略首尾斜杠与重复斜杠）。
func sameDirPath(a, b string) bool { return normalizeDirPath(a) == normalizeDirPath(b) }

func normalizeDirPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	parts := make([]string, 0, 4)
	for _, seg := range strings.Split(p, "/") {
		if seg = strings.TrimSpace(seg); seg != "" {
			parts = append(parts, seg)
		}
	}
	return "/" + strings.Join(parts, "/")
}

// 分享转存这条路上的常量。
const (
	// sharePageSize 是分享目录一次列的条目数。
	sharePageSize = 200
	// shareTaskPollInterval / shareTaskTimeout 是转存任务的轮询节奏与上限。
	shareTaskPollInterval = time.Second
	shareTaskTimeout      = 40 * time.Second
	// max139PathDepth 是往下找目录时的深度上限，防环。
	max139PathDepth = 16

	// 分享侧接口路径。
	pathShareOutLink    = "/yun-share/richlifeApp/devapp/IOutLink/getOutLinkInfoV6"
	pathShareCreateTask = "/yun-share/richlifeApp/devapp/IBatchOprTask/createOuterLinkBatchOprTask"
	// 个人云接口路径（转存任务查询）。
	pathTaskGet = "/hcy/task/get"
)
