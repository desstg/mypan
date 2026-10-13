package cloud189

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// 天翼云盘（189）的**分享链转存**。
//
// ⚠️ 这条通道与驱动平时的签名接口（`api.cloud.189.cn` + SessionKey/SessionSecret 签名）
// **是两套完全不同的凭据与协议**：
//
//   - 平时的文件管理走开放平台 OAuth 换来的 SessionKey 签名（见 transport.go）；
//   - 分享转存走**网页 Cookie** —— 先用 Cookie 换一个 AccessToken
//     （`open/oauth2/ssoH5.action`），再拿它调 `open/share/*` 与 `open/batch/*`。
//
// 两套 ID 空间是同一个账号的同一份文件树（网页接口的 fileId 与开放平台的 folderId
// 是同一套数字 id），但**凭据不通用**：只配了开放平台令牌的账号类型上支持、实际转不了。
// 所以这里全程只认 `d.currentCookie()`，缺了就给一句可执行的话。
//
// 参考实现：`115-media-hub` 的 tianyi provider 与 `TgtoDrive` 的 tgto189。
// 接口都不带签名，是普通的表单 POST，成功判据是响应体里的 `res_code == 0`。

// ShareKinds 报告这个驱动能转存天翼自己的分享链。
//
// ⚠️ 必须实现：internal/driver/share.go 的契约是「不实现 ShareKindReporter 就按
// 只认 115 处理」，不实现的话天翼分享会被当成 115 的分享码去打 115 的接口。
func (d *Driver) ShareKinds() string { return driver.ShareKind189 }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 判据只有「配没配网页 Cookie」；Cookie 过期会在实际请求里以 res_code 报出来。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该天翼账号还没配「网页 Cookie」，无法转存分享；" +
				"到账号配置里粘贴一份 cloud.189.cn 的浏览器 Cookie 即可" +
				"（分享转存走网页接口，开放平台令牌做不了）",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份天翼分享整份转存进目标目录。
//
// 三步：换 AccessToken → 列分享根目录 → 提交 SHARE_SAVE 批量任务。
// 与 115/123/夸克/光鸭同一套骨架，也同一道闸：**先核对目标目录**。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	target := strings.TrimSpace(req.TargetCID)
	if target == "" {
		target = d.rootID()
	}

	// ① 目标目录核对。必须在转存**之前** —— 写错目录比写不进去严重得多。
	if err := d.verifyShareTargetDir(ctx, target, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	token, err := d.shareAccessToken(ctx)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② 分享元信息：shareId/shareMode/根 fileId 都从这一步拿。
	meta, err := d.shareInfoByCode(ctx, code, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ③ 列分享根目录，拿到要转存的条目（目录与文件都要，服务端会带整棵子树）。
	items, err := d.shareListDir(ctx, token, meta)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(items) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "天翼分享是空的，或已失效")
	}
	if len(items) > shareSaveBatchLimit {
		// 单次转存条目数有上限，超了服务端会整批拒。截断而不是失败 ——
		// 用户至少能拿到前面这批，剩下的可以再点一次。
		items = items[:shareSaveBatchLimit]
	}

	// ④ 提交 SHARE_SAVE 批量任务并等它收敛。
	if err := d.shareSaveFiles(ctx, meta, items, target); err != nil {
		return driver.ShareReceiveResult{}, err
	}
	return driver.ShareReceiveResult{Count: len(items)}, nil
}

// PeekShare 看一眼分享里有什么（不写入任何东西）。
//
// 与 115 的 `share/snap` 不同，天翼的分享接口**要凭据**，所以没配 Cookie 时这一步也会失败。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	meta, err := d.shareInfoByCode(ctx, code, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	res := driver.SharePeekResult{
		Title:     strings.TrimSpace(meta.FileName),
		FileName:  strings.TrimSpace(meta.FileName),
		OwnerID:   strings.TrimSpace(meta.ShareID),
		FileCount: 1,
	}
	return res, nil
}

//——————————————————— 网页凭据 ———————————————————

// shareWebBase 是网页开放接口的宿主。单独拎成变量只为让测试指向 httptest；
// 生产代码里不要改它。
var shareWebBase = apiURL

// shareTokenTTL 是 AccessToken 的缓存时长。
//
// ssoH5 换来的令牌实测有效期是小时级，但网页 Cookie 一旦失效它就立刻不能用。
// 取一个保守的短 TTL（10 分钟）：既不至于每次转存都多打一次 ssoH5，
// 又不会在 Cookie 换了之后长期用着旧令牌。
const shareTokenTTL = 10 * time.Minute

// currentCookie 读网页 Cookie。
//
// ⚠️ 与 115 同一条路：值来自 account_auth_states（经 SetAuthCredentials 注入到
// d.cookie），而不是 Addition.Cookie 字段 —— 那个字段只是写入/展示用的入口。
// 读不到时回落到 Addition，方便「直接在配置里填」的场景。
func (d *Driver) currentCookie() string {
	d.mu.Lock()
	cookie := strings.TrimSpace(d.cookie)
	d.mu.Unlock()
	if cookie != "" {
		return cookie
	}
	return strings.TrimSpace(d.add.Cookie)
}

// shareAccessToken 用网页 Cookie 换一个网页接口用的 AccessToken，带进程内缓存。
func (d *Driver) shareAccessToken(ctx context.Context) (string, error) {
	cookie := d.currentCookie()
	if cookie == "" {
		return "", domain.Errorf(domain.CodeValidation,
			"该天翼账号还没配「网页 Cookie」，无法转存分享；请到账号配置里粘贴一份 cloud.189.cn 的浏览器 Cookie")
	}

	d.shareMu.Lock()
	if d.shareToken != "" && cookie == d.shareTokenCookie && time.Now().Before(d.shareTokenExpiry) {
		token := d.shareToken
		d.shareMu.Unlock()
		return token, nil
	}
	d.shareMu.Unlock()

	endpoint := shareWebBase + "/open/oauth2/ssoH5.action"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", domain.Wrap(domain.CodeInternal, err)
	}
	set189WebHeaders(req, cookie)
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return "", domain.Wrap(domain.CodeDriverError, err)
	}
	token := extract189AccessToken(resp, data)
	if token == "" {
		if resp.StatusCode == http.StatusOK {
			return "", domain.Errorf(domain.CodeAuthExpired,
				"天翼 AccessToken 换取失败，网页 Cookie 可能已失效或不完整（请重新登录 cloud.189.cn 后完整复制 Cookie）")
		}
		return "", domain.Errorf(domain.CodeDriverError,
			"天翼网页接口 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	d.shareMu.Lock()
	d.shareToken = token
	d.shareTokenCookie = cookie
	d.shareTokenExpiry = time.Now().Add(shareTokenTTL)
	d.shareMu.Unlock()
	return token, nil
}

//——————————————————— 分享元信息与目录 ———————————————————

// shareMeta 是一次分享转存路上反复要用到的几个标识。
type shareMeta struct {
	ShareID   string
	ShareMode string
	RootID    string
	FileName  string
}

type shareInfoResp struct {
	ResCode    json.RawMessage `json:"res_code"`
	ResMessage string          `json:"res_message"`
	ShareID    string          `json:"shareId"`
	FileID     string          `json:"fileId"`
	ShareMode  string          `json:"shareMode"`
	FileName   string          `json:"fileName"`
	IsFolder   bool            `json:"isFolder"`
}

// shareInfoByCode 用分享码换分享元信息。
//
// 接口是 `getShareInfoByCodeV2.action`：**不需要登录态**也能读（分享本身是公开的），
// 但带上有提取码的分享时提取码要一起带（走 checkAccessCode）。
func (d *Driver) shareInfoByCode(ctx context.Context, code, accessCode string) (*shareMeta, error) {
	query := url.Values{}
	query.Set("shareCode", code)
	query.Set("noCache", strconv.FormatInt(time.Now().UnixMilli(), 10))

	var resp shareInfoResp
	if err := d.shareWebForm(ctx, http.MethodGet, "/open/share/getShareInfoByCodeV2.action", query, nil, &resp, false); err != nil {
		return nil, err
	}
	if is189NotFound(resp.ResCode) {
		return nil, domain.Errorf(domain.CodeNotFound, "天翼分享不存在、已失效或被取消")
	}
	if strings.TrimSpace(resp.ShareID) == "" || strings.TrimSpace(resp.FileID) == "" {
		return nil, domain.Errorf(domain.CodeDriverError,
			"天翼分享信息不完整（接口结构可能已变）：shareId=%q fileId=%q", resp.ShareID, resp.FileID)
	}
	meta := &shareMeta{
		ShareID:   strings.TrimSpace(resp.ShareID),
		ShareMode: firstNonEmpty189(resp.ShareMode, "3"),
		RootID:    strings.TrimSpace(resp.FileID),
		FileName:  strings.TrimSpace(resp.FileName),
	}

	// 有提取码时先校验一次：提取码不对在这一步就会明确报出来，
	// 而不是等 listShareDir 回一句含糊的「解析分享失败」。
	if ac := strings.TrimSpace(accessCode); ac != "" {
		if err := d.shareCheckAccessCode(ctx, code, ac); err != nil {
			return nil, err
		}
		q := url.Values{}
		q.Set("shareCode", code)
		q.Set("accessCode", ac)
		q.Set("noCache", strconv.FormatInt(time.Now().UnixMilli(), 10))
		var withPwd shareInfoResp
		if err := d.shareWebForm(ctx, http.MethodGet, "/open/share/getShareInfoByCodeV2.action", q, nil, &withPwd, false); err == nil {
			if id := strings.TrimSpace(withPwd.ShareID); id != "" {
				meta.ShareID = id
			}
			if mode := strings.TrimSpace(withPwd.ShareMode); mode != "" {
				meta.ShareMode = mode
			}
			if root := strings.TrimSpace(withPwd.FileID); root != "" {
				meta.RootID = root
			}
		}
	}
	return meta, nil
}

// shareCheckAccessCode 校验提取码；不对时给一句可执行的话。
func (d *Driver) shareCheckAccessCode(ctx context.Context, code, accessCode string) error {
	query := url.Values{}
	query.Set("shareCode", code)
	query.Set("accessCode", accessCode)
	query.Set("uuid", strconv.FormatInt(time.Now().UnixNano(), 10))
	query.Set("noCache", strconv.FormatInt(time.Now().UnixMilli(), 10))

	var resp shareInfoResp
	if err := d.shareWebForm(ctx, http.MethodGet, "/open/share/checkAccessCode.action", query, nil, &resp, false); err != nil {
		return err
	}
	if is189NotFound(resp.ResCode) || strings.TrimSpace(resp.ShareID) == "" {
		if strings.TrimSpace(resp.ResMessage) != "" {
			return domain.Errorf(domain.CodeValidation, "天翼分享提取码不正确：%s", resp.ResMessage)
		}
		return domain.Errorf(domain.CodeValidation, "天翼分享提取码不正确")
	}
	return nil
}

// shareEntry 是分享里的一个条目。
type shareEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsFolder bool   `json:"isFolder"`
}

// shareListResp 是 listShareDir 的响应。
//
// ⚠️ 条目在 `fileListAO` 里，分 `folderList` 与 `fileList` 两组，与驱动平时
// 列目录（ops.go 的 listResp）形状一致 —— 天翼网页与开放平台这两套接口共用
// 同一个响应外壳。所以这里复用 listResp 的字段名，别自己另起一套。
type shareListResp struct {
	ResCode    json.RawMessage `json:"res_code"`
	ResMessage string          `json:"res_message"`
	FileListAO shareFileListAO `json:"fileListAO"`
}

type shareFileListAO struct {
	Count      json.Number  `json:"count"`
	FolderList []shareEntry `json:"folderList"`
	FileList   []shareEntry `json:"fileList"`
}

// shareListDir 列出分享根目录下的条目（分页收齐）。
func (d *Driver) shareListDir(ctx context.Context, token string, meta *shareMeta) ([]shareEntry, error) {
	out := make([]shareEntry, 0, 16)
	for page := 1; page <= shareMaxPages; page++ {
		query := url.Values{}
		query.Set("pageNum", strconv.Itoa(page))
		query.Set("pageSize", strconv.Itoa(sharePageSize))
		query.Set("fileId", meta.RootID)
		query.Set("shareDirFileId", meta.RootID)
		query.Set("isFolder", "true")
		query.Set("shareId", meta.ShareID)
		query.Set("shareMode", meta.ShareMode)
		query.Set("iconOption", "5")
		query.Set("orderBy", "lastOpTime")
		query.Set("descending", "true")
		query.Set("accessCode", "")
		query.Set("noCache", strconv.FormatInt(time.Now().UnixMilli(), 10))

		var resp shareListResp
		if err := d.shareWebForm(ctx, http.MethodGet, "/open/share/listShareDir.action", query, nil, &resp, true); err != nil {
			return nil, err
		}
		if is189NotFound(resp.ResCode) {
			return nil, domain.Errorf(domain.CodeNotFound, "天翼分享已失效")
		}
		for _, it := range resp.FileListAO.FolderList {
			it.IsFolder = true
			out = append(out, it)
		}
		out = append(out, resp.FileListAO.FileList...)

		total := int64(0)
		if n, err := resp.FileListAO.Count.Int64(); err == nil {
			total = n
		}
		if len(resp.FileListAO.FolderList)+len(resp.FileListAO.FileList) < sharePageSize || int64(page)*sharePageSize >= total {
			break
		}
	}
	return out, nil
}

// shareSaveFiles 提交 SHARE_SAVE 批量任务并要求它收敛。
func (d *Driver) shareSaveFiles(ctx context.Context, meta *shareMeta, items []shareEntry, targetID string) error {
	infos := make([]map[string]any, 0, len(items))
	for _, it := range items {
		isFolder := 0
		if it.IsFolder {
			isFolder = 1
		}
		infos = append(infos, map[string]any{
			"fileId":   it.ID,
			"fileName": it.Name,
			"isFolder": isFolder,
		})
	}
	raw, err := json.Marshal(infos)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}

	form := url.Values{}
	form.Set("type", "SHARE_SAVE")
	form.Set("taskInfos", string(raw))
	form.Set("targetFolderId", targetID)
	form.Set("shareId", meta.ShareID)

	var created struct {
		ResCode    json.RawMessage `json:"res_code"`
		ResMessage string          `json:"res_message"`
		TaskID     json.Number     `json:"taskId"`
	}
	if err := d.shareWebForm(ctx, http.MethodPost, "/open/batch/createBatchTask.action", nil, form, &created, false); err != nil {
		return err
	}
	taskID := strings.TrimSpace(created.TaskID.String())
	if taskID == "" {
		return domain.Errorf(domain.CodeDriverError, "天翼没有返回转存任务号")
	}

	// 等任务收敛：转存是异步的，但要给用户一个确定的结论 ——
	// 立刻返回「已提交」会在任务实际失败时留下一条假成功。
	return d.waitShareTask(ctx, taskID)
}

// waitShareTask 轮询 SHARE_SAVE 任务直到落定。
//
// 与 115/123 那些「不阻塞等」不同：天翼的任务是**本次转存能否算成功**的唯一依据，
// 不等它就把一次会失败的任务记成成功，用户看到的是「已转存 5 个文件」而目录里是空的。
// 代价是可能多花几秒，换来的是结论可信。
func (d *Driver) waitShareTask(ctx context.Context, taskID string) error {
	deadline := time.Now().Add(shareTaskTimeout)
	for {
		form := url.Values{}
		form.Set("type", "SHARE_SAVE")
		form.Set("taskId", taskID)

		var resp struct {
			ResCode    json.RawMessage `json:"res_code"`
			ResMessage string          `json:"res_message"`
			TaskStatus json.Number     `json:"taskStatus"`
			ErrorCode  string          `json:"errorCode"`
			ErrorMsg   string          `json:"errorMessage"`
		}
		if err := d.shareWebForm(ctx, http.MethodPost, "/open/batch/checkBatchTask.action", nil, form, &resp, true); err != nil {
			return err
		}
		status, _ := resp.TaskStatus.Int64()
		if resp.ErrorCode != "" {
			return domain.Errorf(domain.CodeDriverError,
				"天翼转存失败：%s", firstNonEmpty189(resp.ErrorMsg, resp.ErrorCode))
		}
		// taskStatus: 3 表示完成（与 TgtoDrive 的判据一致），其余是进行中。
		if status == 3 {
			return nil
		}
		if time.Now().After(deadline) {
			return domain.Errorf(domain.CodeDriverError,
				"天翼转存任务超时未完成（taskId=%s），请稍后到网盘里确认文件是否已到", taskID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(shareTaskPollInterval):
		}
	}
}

//——————————————————— 目标目录核对 ———————————————————

// verifyShareTargetDir 确认目录 id 真的指向 expectPath。
//
// expectPath 为空时跳过（调用方没要求核对）。与 115/123/夸克同一条原则：
// 转存会真往用户网盘里写东西，**写错目录比写不进去严重得多**。
//
// 天翼的目录 id 是数字，面包屑靠 filePath 字段一次拿到（开放平台的文件详情带这个字段）。
func (d *Driver) verifyShareTargetDir(ctx context.Context, dirID, expectPath string) error {
	expectPath = strings.TrimSpace(expectPath)
	if expectPath == "" || dirID == d.rootID() || dirID == "0" || dirID == "-11" {
		return nil
	}
	actual, err := d.dirFullPath(ctx, dirID)
	if err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在天翼里读不出来：%v；请重新选择目标目录", dirID, err)
	}
	if !sameDirPath(actual, expectPath) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：天翼里 %s 是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			dirID, actual, expectPath)
	}
	return nil
}

// dirFullPath 从目录 id 往上拼出完整路径（`/a/b`），根目录返回 `/`。
//
// ⚠️ 天翼的文件详情里虽然有个 `filePath` 字段，但实测**不可靠**：
// 独立参考实现（TgtoDrive 的 tgto189）里它只在「按路径查 id」时被读，
// 而按 id 查详情时拿到的那份 `filePath` 在部分形态下是空的或只有末级名字。
// 所以这里不图省事，**一律逐层向上问 parentId 自己拼** —— 与夸克/光鸭同一条路，
// 防环靠深度上限。多打几次接口换来的是「目标目录核对」这道闸真的有效。
func (d *Driver) dirFullPath(ctx context.Context, dirID string) (string, error) {
	root := d.rootID()
	segments := make([]string, 0, 4)
	current := strings.TrimSpace(dirID)
	for depth := 0; current != "" && current != "0" && current != root; depth++ {
		if depth > max189PathDepth {
			return "", domain.Errorf(domain.CodeDriverError, "目录层级过深，可能形成了环")
		}
		raw, err := d.fetchFileInfo(ctx, current)
		if err != nil {
			return "", err
		}
		if name := strings.TrimSpace(raw.Name); name != "" {
			segments = append(segments, name)
		}
		parent := strings.TrimSpace(raw.ParentID.String())
		if parent == "" || parent == current {
			break
		}
		current = parent
	}
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}
	return "/" + strings.Join(segments, "/"), nil
}

//——————————————————— 网页通道 ———————————————————

// shareWebForm 打一次天翼的网页开放接口（无签名，靠 Cookies + AccessToken）。
//
// 与驱动平时的 signedForm 是两条路：
// 签名那条只能上 `api.cloud.189.cn/*.action` 的开放平台接口，而分享这几个
// 走的是 `open/share/*`、`open/batch/*`，它们**只要 Cookie**（外加 AccessToken
// 走网页会话）。两套混用会得到一个含糊的「签名错误」。
func (d *Driver) shareWebForm(ctx context.Context, method, path string, query, form url.Values, out any, needToken bool) error {
	if err := d.waitInterval(ctx); err != nil {
		return err
	}
	cookie := d.currentCookie()
	if cookie == "" {
		return domain.Errorf(domain.CodeValidation,
			"该天翼账号还没配「网页 Cookie」，无法转存分享；请到账号配置里粘贴一份 cloud.189.cn 的浏览器 Cookie")
	}

	rawURL := shareWebBase + path
	if len(query) > 0 {
		rawURL += "?" + query.Encode()
	}
	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	set189WebHeaders(req, cookie)
	if len(form) > 0 {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	if needToken {
		token, err := d.shareAccessToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("AccessToken", token)
	}

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errorf(domain.CodeAuthExpired, "天翼网页 Cookie 已失效，请重新粘贴")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.Errf(domain.CodeRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "天翼网页接口 HTTP %d：%s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env struct {
		ResCode    json.RawMessage `json:"res_code"`
		ResMessage string          `json:"res_message"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Errorf(domain.CodeDriverError, "天翼网页接口返回非 JSON：%s", httpx.Truncate(data, 300))
	}
	if !is189ResCodeOK(env.ResCode) {
		return map189ShareError(env.ResCode, env.ResMessage)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Errorf(domain.CodeDriverError, "天翼网页接口响应解析失败：%v", err)
		}
	}
	return nil
}

func set189WebHeaders(req *http.Request, cookie string) {
	req.Header.Set("Accept", "application/json;charset=UTF-8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", webURL+"/web/main.action")
	req.Header.Set("Cookie", cookie)
}

// extract189AccessToken 从 ssoH5 的响应里取 AccessToken。
//
// 两种形态都要认：直接回 JSON（`{"accessToken": "..."}`）、或 302 到带
// `accessToken=` 的地址（把 Location 与最终 URL 都找一遍）。
func extract189AccessToken(resp *http.Response, data []byte) string {
	var env struct {
		AccessToken  string `json:"accessToken"`
		AccessToken2 string `json:"access_token"`
	}
	if json.Unmarshal(data, &env) == nil {
		if t := firstNonEmpty189(env.AccessToken, env.AccessToken2); t != "" {
			return t
		}
	}
	for _, u := range []string{resp.Request.URL.String(), resp.Header.Get("Location")} {
		if t := accessTokenFromURL(u); t != "" {
			return t
		}
	}
	return ""
}

func accessTokenFromURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if t := strings.TrimSpace(parsed.Query().Get("accessToken")); t != "" {
		return t
	}
	if frag := parsed.Fragment; frag != "" {
		if q, err := url.ParseQuery(strings.TrimPrefix(frag, "?")); err == nil {
			if t := strings.TrimSpace(q.Get("accessToken")); t != "" {
				return t
			}
		}
	}
	return ""
}

// is189ResCodeOK 报告 res_code 是不是成功。
//
// 天翼这套接口的 res_code 有数字、字符串、甚至缺失三种形态；缺失时按成功处理
// （有的接口成功响应里根本没有这个字段，只看它会导致把成功判成失败）。
func is189ResCodeOK(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var num int64
	if json.Unmarshal(raw, &num) == nil {
		return num == 0
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		return text == "" || text == "0"
	}
	return true
}

func is189NotFound(raw json.RawMessage) bool {
	var num int64
	if json.Unmarshal(raw, &num) == nil {
		// 天翼的分享类失败码实测集中在 -1 与 4xxxx 段；这里只挑「分享没了」那一类。
		return num == -1
	}
	return false
}

// map189ShareError 把分享这条路上的 业务错误码翻成人话。
func map189ShareError(raw json.RawMessage, msg string) error {
	msg = strings.TrimSpace(msg)
	if is189NotFound(raw) {
		return domain.Errorf(domain.CodeNotFound, "天翼分享不存在、已失效或被取消：%s", firstNonEmpty189(msg, "接口未说明原因"))
	}
	return domain.Errorf(domain.CodeDriverError, "天翼转存接口错误：%s", firstNonEmpty189(msg, string(raw)))
}

func firstNonEmpty189(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// 分享转存这条路上的常量。
const (
	// sharePageSize 是 listShareDir 每页条目数。
	sharePageSize = 100
	// shareMaxPages 是翻页上限，防异常数据把循环拖死。
	shareMaxPages = 50
	// shareSaveBatchLimit 是单次 SHARE_SAVE 的条目上限（超了服务端整批拒）。
	shareSaveBatchLimit = 500
	// shareTaskPollInterval / shareTaskTimeout 是转存任务的轮询节奏与上限。
	shareTaskPollInterval = time.Second
	shareTaskTimeout      = 40 * time.Second
	// max189PathDepth 是往上拼路径时的深度上限，防环。
	max189PathDepth = 32
)

// sameDirPath 比两条目录路径是否指向同一个地方（忽略首尾斜杠与重复斜杠）。
func sameDirPath(a, b string) bool {
	return normalizeDirPath(a) == normalizeDirPath(b)
}

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
