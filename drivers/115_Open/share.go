package pan115open

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 网页版分享接口。参数形状都在 2026-09-17 实测过，
// 详见计划文件的「阶段 B 实测结果」。
const (
	webPathFiles      = "/files"
	webPathShareSnap  = "/share/snap"
	webPathShareRecv  = "/share/receive"
	webSharePageLimit = 200
	webShareMaxPages  = 10
	webShareStateOK   = 1
	webFilesAidMain   = "1"
)

// webPathEntry 是 /files 响应里 path（面包屑）的一项。
type webPathEntry struct {
	Name string `json:"name"`
	Cid  string `json:"cid"`
}

// filesResponse 是 `/files` 的**完整响应体**（不是 data 子对象）。
//
// ⚠️ 这里必须整包解析：条目在 `data`（数组）、面包屑在顶层的 `path`，
// 两者是兄弟字段。只解 `data` 的话既拿不到面包屑、也会因为 data 是数组而解析失败
// （实测：`json: cannot unmarshal array into Go value of type ...filesResponse`）。
// 这个坑是真实环境端到端跑出来的，单测里那版桩数据形状写错了才没暴露。
type filesResponse struct {
	State bool           `json:"state"`
	ErrNo int64          `json:"errNo"`
	Path  []webPathEntry `json:"path"`
	Data  []shareEntry   `json:"data"`
}

// shareEntry 是分享里的一项（/share/snap 的 data.list）。
type shareEntry struct {
	Fid  string `json:"fid"`
	Name string `json:"n"`
	Size int64  `json:"s"`
}

// snapResponse 是 `/share/snap` 的 data 形状。
//
// ⚠️ 分享者的 user_id 在 **data.userinfo** 里，不在 data.shareinfo 里 ——
// 后者只有标题、大小、过期时间这些元信息。`share/receive` 要的正是它，
// 拿错地方会拼出一个必然失败的表单。
type snapResponse struct {
	Count      int64 `json:"count"`
	ShareState int   `json:"share_state"`
	ShareInfo  struct {
		ShareTitle   string `json:"share_title"`
		FileSize     int64  `json:"file_size"`
		ForbidReason string `json:"forbid_reason"`
		ShareState   int    `json:"share_state"`
	} `json:"shareinfo"`
	UserInfo struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	} `json:"userinfo"`
	List []shareEntry `json:"list"`
}

// receiveResponse 的 data 形状在不同版本上不完全一致，按宽容方式解析。
type receiveResponse struct {
	FileIDs []string `json:"file_ids"`
	Count   int      `json:"count"`
}

// PeekShare 看一眼分享里有什么，**不写入任何东西、也不需要任何凭据**。
//
// 实测（2026-09-17）：`share/snap` 连裸请求、一个头都不带都能拿到完整响应，
// 只有真正写入的 `share/receive` 才要登录态（不带 Cookie 会回 990001 登录超时）。
// 所以这个方法**刻意不检查 Cookie** —— 它就是为了在用户还没配 Cookie 时也能用。
//
// 复用 webRequest（它会在 Cookie 为空时报错），所以这里走 rawWebRequest。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	query := url.Values{}
	query.Set("share_code", code)
	query.Set("receive_code", strings.TrimSpace(req.ReceiveCode))
	query.Set("cid", "0")
	query.Set("offset", "0")
	query.Set("limit", "1")
	query.Set("asc", "1")
	query.Set("o", "time")

	var snap snapResponse
	if err := d.rawWebRequest(ctx, http.MethodGet, webPathShareSnap, query, nil, &snap, false); err != nil {
		return driver.SharePeekResult{}, err
	}
	if snap.ShareState != webShareStateOK && snap.ShareInfo.ShareState != webShareStateOK {
		reason := strings.TrimSpace(snap.ShareInfo.ForbidReason)
		if reason == "" {
			reason = "分享已失效或已被取消"
		}
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeNotFound, "115 分享不可用：%s", reason)
	}

	// ⚠️ 内嵌的 data.shareinfo 不是信封，所以它里面的 state/errno 不会触发 mapWebErrno ——
	// 必须在这里自己判一次。实测没有 Cookie 时 115 回的是
	// `state:false, errno:990001 登录超时`（顶层），会被 mapWebErrno 拦下；
	// 但封在 data 里的错误没有统一形状，只能按经验值处理，所以保留上面那道校验。
	out := driver.SharePeekResult{
		Title:     strings.TrimSpace(snap.ShareInfo.ShareTitle),
		TotalSize: snap.ShareInfo.FileSize,
		FileCount: int(snap.Count),
		OwnerID:   strings.TrimSpace(snap.UserInfo.UserID),
	}
	// 分享里第一个文件的名字比分享标题更像「发布名」：
	// 分享整个文件夹时标题是文件夹名，而文件夹名通常就是发布名，两者都对；
	// 分享单个文件时标题就是文件名。取 list 里的第一条作为更精确的版本。
	if len(snap.List) > 0 {
		out.FileName = strings.TrimSpace(snap.List[0].Name)
		if out.TotalSize == 0 {
			out.TotalSize = snap.List[0].Size
		}
	}
	return out, nil
}

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那个阶段绝不能有网络往返。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该 115 账号还没配「网页 Cookie」，无法转存分享；" +
				"到账号配置里粘贴一份浏览器 Cookie 即可（分享转存只有网页接口能做）",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份分享转存进自己的网盘。
//
// 调用序列是**两步**：`share/snap` → `share/receive`。
// 计划初稿写的三步（shareinfo → snap → receive）多了一步：单独调 `share/shareinfo`
// （不带 receive_code）会直接回 errno 4100010 参数错误，而 snap 的响应里
// 已经把该拿的都带回来了 —— 包括分享者的 user_id。
//
// 整份接收：snap 拿到分享根目录下的全部条目，把它们的 fid 一起交给 receive。
// 只传分享码不行 —— receive 必须知道具体收哪些文件。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	targetCID := strings.TrimSpace(req.TargetCID)
	if targetCID == "" {
		// 空等于根目录：网页接口 cid=0 就是根，与开放平台的根 id "0" 恰好一致。
		targetCID = "0"
	}

	// ① 目标目录核对。必须在转存**之前** —— 转存是真的往用户网盘里写东西，
	//    写错目录比写不进去严重得多。
	targetPath, err := d.verifyTargetDir(ctx, targetCID, req.TargetPath)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② snap：拿分享者的 user_id 与根目录下的条目列表。
	snap, err := d.shareSnap(ctx, code, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if snap.ShareState != webShareStateOK {
		reason := strings.TrimSpace(snap.ShareInfo.ForbidReason)
		if reason == "" {
			reason = "分享已失效或已被取消"
		}
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "115 分享不可用：%s", reason)
	}
	if strings.TrimSpace(snap.UserInfo.UserID) == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeDriverError,
			"115 分享信息里没有分享者 ID，接口结构可能已变，无法转存")
	}

	fileIDs := req.FileIDs
	if len(fileIDs) == 0 {
		for _, entry := range snap.List {
			if id := strings.TrimSpace(entry.Fid); id != "" {
				fileIDs = append(fileIDs, id)
			}
		}
	}
	if len(fileIDs) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound,
			"这份分享里没有可转存的文件（可能是空目录，或分享已被清空）")
	}

	// ③ receive：整份转存到目标目录。
	result, err := d.shareReceive(ctx, code, strings.TrimSpace(req.ReceiveCode),
		snap.UserInfo.UserID, targetCID, fileIDs)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	result.TargetPath = targetPath
	if result.Count == 0 {
		result.Count = len(fileIDs)
	}
	return result, nil
}

// verifyTargetDir 确认 cid 真的指向 expectPath，返回驱动解析到的实际路径。
//
// expectPath 为空时只解析不比对（调用方没要求核对）。
//
// 这是「两套 ID 空间万一不一致」的最后一道闸：订阅的 target_parent_id 来自开放平台，
// 而网页接口要的是 cid。两边是不是同一套 ID，证据很强但**没有官方契约**；
// 万一不是，宁可让这次转存失败并报出来，也不能把文件默默塞进一个不相干的目录。
func (d *Driver) verifyTargetDir(ctx context.Context, cid, expectPath string) (string, error) {
	query := url.Values{}
	query.Set("aid", webFilesAidMain)
	query.Set("cid", cid)
	query.Set("limit", "1")
	query.Set("show_dir", "1")

	var page filesResponse
	if err := d.webRequestFull(ctx, http.MethodGet, webPathFiles, query, nil, &page); err != nil {
		return "", domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在 115 网页版里读不出来：%v；请重新选择订阅的目标目录", cid, err)
	}
	actual := joinBreadcrumb(page.Path)
	if strings.TrimSpace(expectPath) == "" {
		return actual, nil
	}
	if !sameDirPath(actual, expectPath) {
		return "", domain.Errorf(domain.CodeValidation,
			"目标目录对不上：网页版里 %s 是「%s」，而订阅记的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择该订阅的目标目录",
			cid, actual, strings.TrimSpace(expectPath))
	}
	return actual, nil
}

func (d *Driver) shareSnap(ctx context.Context, code, receiveCode string) (*snapResponse, error) {
	query := url.Values{}
	query.Set("share_code", code)
	query.Set("receive_code", receiveCode)
	query.Set("cid", "0")
	query.Set("offset", "0")
	query.Set("limit", strconv.Itoa(webSharePageLimit))
	query.Set("asc", "1")
	query.Set("o", "time")

	var snap snapResponse
	if err := d.webRequest(ctx, http.MethodGet, webPathShareSnap, query, nil, &snap); err != nil {
		return nil, err
	}
	// 分享根目录条目超过一页时继续翻页 —— 整份接收要求把根下所有条目都拿到。
	for page := 1; page < webShareMaxPages && int64(len(snap.List)) < snap.Count; page++ {
		query.Set("offset", strconv.Itoa(len(snap.List)))
		var more snapResponse
		if err := d.webRequest(ctx, http.MethodGet, webPathShareSnap, query, nil, &more); err != nil {
			return nil, err
		}
		if len(more.List) == 0 {
			break
		}
		snap.List = append(snap.List, more.List...)
	}
	return &snap, nil
}

func (d *Driver) shareReceive(
	ctx context.Context,
	code, receiveCode, userID, targetCID string,
	fileIDs []string,
) (driver.ShareReceiveResult, error) {
	form := url.Values{}
	form.Set("user_id", userID)
	form.Set("share_code", code)
	form.Set("receive_code", receiveCode)
	form.Set("file_id", strings.Join(fileIDs, ","))
	form.Set("cid", targetCID)

	var data receiveResponse
	if err := d.webRequest(ctx, http.MethodPost, webPathShareRecv, nil, form, &data); err != nil {
		return driver.ShareReceiveResult{}, err
	}
	return driver.ShareReceiveResult{FileIDs: data.FileIDs, Count: data.Count}, nil
}

// joinBreadcrumb 把 /files 响应里的 path 数组拼成完整目录路径。
//
// path 的第一项固定是根目录（{name:"根目录", cid:"0"}），不计入路径；
// 根目录本身返回 "/"。实测响应形如
// `[{name:"根目录",cid:"0"},{cid:"310...",name:"电视剧"}]`。
func joinBreadcrumb(entries []webPathEntry) string {
	if len(entries) <= 1 {
		return "/"
	}
	parts := make([]string, 0, len(entries)-1)
	for _, e := range entries[1:] {
		if name := strings.TrimSpace(e.Name); name != "" {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + path.Join(parts...)
}

// sameDirPath 比较两个目录路径是否指同一个目录。
//
// 宽容处只在与写法无关的地方：多余的斜杠、首尾斜杠、Windows 风格分隔符。
// 不放宽大小写（115 是大小写敏感的），也不做「忽略最后一段」这类猜测 ——
// 这个函数存在的意义就是严格，放宽它就失去了意义。
func sameDirPath(a, b string) bool {
	return normalizeDirPath(a) == normalizeDirPath(b)
}

func normalizeDirPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.Trim(p, "/")
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}
