package guangya

import (
	"context"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 光鸭的**分享链转存**。
//
// 官方接口，三步（换 token → 列条目 → 转存）：
//
//  1. `POST /userres/v1/get_share_access_token`  body `{shareId}` → 分享访问令牌；
//  2. `POST /userres/v1/get_share_page_files_list`  body `{accessToken, parent_id, page, pageSize}`
//     → 分享里的条目（fileId / fileName / resType）；
//  3. `POST /userres/v1/restore_share`  body `{accessToken, fileIds, parentId}` → 存进自己网盘。
//
// 与 115/123/夸克最大的不同：**它是官方开放接口，用的就是本驱动已有的 Bearer token**，
// 不需要额外的网页 Cookie / 账号密码那一层。
//
// ⚠️ 两处**未经真机验证**（第三方封装只写了方法说明，没给响应结构）：
//
//   - 分享口令（提取码）走哪个字段。当前实现把用户给的提取码拼进 `shareId`
//     （见 shareIDWithCode），失败时给出的原因是「分享口令不正确或分享已失效」；
//   - 分享访问令牌在响应里叫什么名字（见 shareAccessTokenData 的候选名）。
//
// 真机跑一次就能把这两处钉死；在此之前，失败信息都指向「分享口令/分享失效」，
// 不会把用户指去查别的地方。

// ShareKinds 报告这个驱动能转存光鸭自己的分享链。
//
// ⚠️ 这个方法**必须实现**：internal/driver/share.go 的契约是「不实现
// ShareKindReporter 就按只认 115 处理」，不实现的话光鸭分享会被当成 115 的
// 分享码去打 115 的接口。
func (d *Driver) ShareKinds() string { return driver.ShareKindGuangya }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 光鸭用的是本驱动本来就有的 Bearer token，所以判据就是「有没有拿到 token」。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentToken()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该光鸭账号还没登录（没有访问令牌），无法转存分享；" +
				"到账号配置里重新扫码登录即可",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份光鸭分享整份转存进目标目录。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	shareID := strings.TrimSpace(req.ShareCode)
	if shareID == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	target := strings.TrimSpace(req.TargetCID)
	if target == "" {
		target = d.resolveParent("")
	}
	// ⚠️ 目标目录是「根」时必须发 "0"，**不能发空串**。
	// 光鸭账号的 RootFolderID 默认是空的，resolveParent("") 于是返回空串，
	// 而 restore_share 收到空 parentId 会回 `142 目录不存在` —— 看起来像
	// 「分享里的目录不存在」，实际是**自己的目标目录没解析出来**（真机踩到过）。
	if target == "" {
		target = "0"
	}

	// ① 目标目录核对。必须在转存**之前** —— 转存是真的往用户网盘里写东西，
	//    写错目录比写不进去严重得多。与 115/123/夸克同一条闸。
	if err := d.verifyTargetDir(ctx, target, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② 换分享访问令牌。
	accessToken, err := d.shareAccessToken(ctx, shareIDWithCode(shareID, req.ReceiveCode))
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ③ 列分享根目录下的条目。
	list, err := d.shareListRoot(ctx, accessToken)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(list) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "光鸭分享是空的，或已失效")
	}

	// fileIds 收**根目录下这一层**的 id —— 目录与文件都收。
	//
	// ⚠️ 这里**不递归**。第三方封装的 `share_restore` 说明写得很清楚：
	// fileIds 是「分享链接里的文件/文件夹 id」，也就是说**传一个文件夹 id，
	// 服务端会把整棵子树一起带过去**（与 123 的 copy/save 同一种语义）。
	// 真机实测（2026-10-12）也印证了这一点：分享根下就是一个文件夹
	// （`F凡。人。修。仙。传 动漫（2026）`，resType=2），而它下面才是文件。
	//
	// 曾经这里自己递归展开，结果卡死在一个坑里：用分享侧的子目录 id 去
	// `get_share_page_files_list` 的 parent_id 要下一层，返回的还是**根那一层**
	// （那个参数看来不是这么用的），于是无限套娃、一个文件都收不到。
	// 递归那条路既没必要（服务端会带子树）、又踩了未知参数的坑，去掉。
	fileIDs := make([]string, 0, len(list))
	for _, e := range list {
		if id := e.id(); id != "" {
			fileIDs = append(fileIDs, id)
		}
	}
	if len(fileIDs) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound,
			"光鸭分享里有 %d 个条目却一个 id 都没取到，服务端返回的首条为：%s",
			len(list), list[0].describe())
	}

	// ④ 转存。
	//
	// ⚠️ parentId 传的是**自己网盘里**的目标目录 id（"0" = 根）——
	// 与分享侧的目录 id 是两套命名空间，别把分享里的 fid 传进来。
	if err := d.shareRestore(ctx, accessToken, fileIDs, target); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	return driver.ShareReceiveResult{Count: len(fileIDs), FileIDs: fileIDs}, nil
}

// shareListRoot 列分享根目录下的条目。
//
// ⚠️ `parent_id` 传 "0"（分享根）。**不要**拿分享侧的子目录 id 来要下一层 ——
// 真机实测那样返回的还是根那一层（见 ReceiveShare 里的说明）。
func (d *Driver) shareListRoot(ctx context.Context, accessToken string) ([]shareFileEntry, error) {
	return d.shareListFiles(ctx, accessToken, "0")
}

// PeekShare 看一眼分享里有什么（不写入任何东西）。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	shareID := strings.TrimSpace(req.ShareCode)
	if shareID == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	full := shareIDWithCode(shareID, req.ReceiveCode)

	res := driver.SharePeekResult{}
	// 概要接口能直接给标题/条目数/总大小，优先用它。
	if summary, err := d.shareSummary(ctx, full); err == nil {
		res.Title = summary.displayTitle()
		res.FileCount = summary.count()
		res.TotalSize = summary.totalSize()
	}

	// 标题或条目数拿不到时再列一次目录补齐（概要接口的字段名是推断的，
	// 真机上可能一个都对不上）。
	if res.Title == "" || res.FileCount == 0 {
		if accessToken, err := d.shareAccessToken(ctx, full); err == nil {
			if list, err := d.shareListRoot(ctx, accessToken); err == nil && len(list) > 0 {
				if res.Title == "" {
					res.Title = list[0].name()
				}
				if res.FileCount == 0 {
					res.FileCount = len(list)
				}
			}
		}
	}
	if res.FileName == "" {
		res.FileName = res.Title
	}
	return res, nil
}

// ⚠️ 官方封装里没写口令走哪个字段，这里按「拼进分享标识」处理。
// 真机验证后如果发现是独立字段，改这一个函数即可 —— 它是全文件里唯一
// 构造 shareId 的地方。
func shareIDWithCode(shareID, code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return strings.TrimSpace(shareID)
	}
	return strings.TrimSpace(shareID) + "|" + code
}

// ——————————————————— 底层调用 ———————————————————

func (d *Driver) shareAccessToken(ctx context.Context, shareID string) (string, error) {
	var data shareAccessTokenData
	if err := d.apiRequest(ctx, pathShareAccessToken, map[string]any{"shareId": shareID}, &data); err != nil {
		return "", mapShareError(err)
	}
	token := data.value()
	if token == "" {
		return "", domain.Errorf(domain.CodeDriverError,
			"光鸭没有返回分享访问令牌，接口结构可能已变（分享口令不正确时也会这样）")
	}
	return token, nil
}

func (d *Driver) shareSummary(ctx context.Context, shareID string) (shareSummaryData, error) {
	var data shareSummaryData
	if err := d.apiRequest(ctx, pathShareSummary, map[string]any{"shareId": shareID}, &data); err != nil {
		return shareSummaryData{}, mapShareError(err)
	}
	return data, nil
}

// shareListFiles 列分享目录下的条目（分页）。
//
// parent_id 是**分享侧**的目录 id，分享根是 "0"（官方封装给的默认值是空串，
// 根用 "0" 与它等价，这里显式用 "0" 让判据自明）。
//
// ⚠️ 返回的条目用 rawEntry 收：**这份响应的字段名与 fileEntry 对不上**
// （真机 2026-10-12：按 fileId 解出来是空的）。分享侧接口是另一套契约，
// 所以这里用候选名逐个取，并在日志里把命中的那套名字打出来 ——
// 下次接口再改字段名，从日志能一眼看出该改哪个。
func (d *Driver) shareListFiles(ctx context.Context, accessToken, parentID string) ([]shareFileEntry, error) {
	out := make([]shareFileEntry, 0, 32)
	for page := 0; page < shareMaxPages; page++ {
		body := map[string]any{
			"accessToken": accessToken,
			"parent_id":   parentID,
			"page":        page,
			"pageSize":    sharePageSize,
		}
		var data sharePageData
		if err := d.apiRequest(ctx, pathSharePageFiles, body, &data); err != nil {
			return nil, mapShareError(err)
		}
		out = append(out, data.List...)
		if len(data.List) < sharePageSize {
			break
		}
	}
	return out, nil
}

// shareRestore 把条目存进自己的目录。
//
// ⚠️ parentId 是**自己网盘里**的目标目录 id（与分享侧的目录 id 是两套命名空间）。
// 真机实测（2026-10-12）：传**根**（""/"0"）都会回 `142 目录不存在`，
// 而传一个真实的目录 id 立刻成功 —— 说明这个接口**不接受根占位符**。
// 所以根目录这一档不传 parentId（省略字段），让服务端用它自己的默认根。
func (d *Driver) shareRestore(ctx context.Context, accessToken string, fileIDs []string, parentID string) error {
	body := map[string]any{
		"accessToken": accessToken,
		"fileIds":     fileIDs,
	}
	if p := strings.TrimSpace(parentID); p != "" && p != "0" {
		body["parentId"] = p
	}
	if err := d.apiRequest(ctx, pathShareRestore, body, nil); err != nil {
		return mapShareError(err)
	}
	return nil
}

// sharePageSize 是分享目录的分页大小（第三方封装给的默认值）。
const sharePageSize = 100

// shareMaxPages 是翻页上限，防异常数据把循环拖死。
const shareMaxPages = 50

// mapShareError 把分享这条路上的错误翻成用户能看懂的话。
//
// 光鸭的和分享有关的失败都被包成通用码，所以这里按**关键词**判：
// 命中「分享/口令/失效」这类词的，给一句可执行的提示；其余原样返回。
func mapShareError(err error) error {
	if err == nil {
		return nil
	}
	ae, ok := domain.AsAppError(err)
	if !ok {
		return err
	}
	if ae.Code != domain.CodeDriverError {
		return err
	}
	msg := ae.Message
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "分享") && (strings.Contains(msg, "不存在") ||
		strings.Contains(msg, "失效") || strings.Contains(msg, "取消") ||
		strings.Contains(msg, "过期")):
		return domain.Errorf(domain.CodeNotFound, "光鸭分享不存在、已失效或被取消：%s", msg)
	case strings.Contains(msg, "口令") || strings.Contains(msg, "密码") ||
		strings.Contains(msg, "提取码") || strings.Contains(lower, "code"):
		return domain.Errorf(domain.CodeValidation, "光鸭分享口令不正确或分享已失效：%s", msg)
	}
	return err
}

// ——————————————————— 目标目录核对 ———————————————————

// verifyTargetDir 确认目录 id 真的指向 expectPath。
//
// expectPath 为空时跳过（调用方没要求核对）—— 手工转存那一侧用户是自己点的目录。
// 与 115/123/夸克同一条原则：转存会真往用户网盘里写东西，
// **写错目录比写不进去严重得多**。
func (d *Driver) verifyTargetDir(ctx context.Context, dirID, expectPath string) error {
	expectPath = strings.TrimSpace(expectPath)
	root := d.rootParentID()
	if expectPath == "" || dirID == root || dirID == "0" {
		// 根目录没有面包屑可核（账号配了 RootFolderID 时，界面的 "/" 指的就是它）。
		return nil
	}
	actual, err := d.dirFullPath(ctx, dirID)
	if err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在光鸭里读不出来：%v；请重新选择目标目录", dirID, err)
	}
	if !sameDirPath(actual, expectPath) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：光鸭里 %s 是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			dirID, actual, expectPath)
	}
	return nil
}

// maxSharePathDepth 是往上拼路径时的深度上限，防环。
const maxSharePathDepth = 32

// dirFullPath 从目录 id 往上拼出完整路径（`/a/b`），根目录返回 `/`。
//
// 光鸭的文件详情带 parentId（fileEntry.ParentID），所以一层层往上问即可。
func (d *Driver) dirFullPath(ctx context.Context, dirID string) (string, error) {
	root := d.rootParentID()
	segments := make([]string, 0, 4)
	current := strings.TrimSpace(dirID)
	for depth := 0; current != "" && current != "0" && current != root; depth++ {
		if depth > maxSharePathDepth {
			return "", domain.Errorf(domain.CodeDriverError, "目录层级过深，可能形成了环")
		}
		var data fileDetailData
		if err := d.apiRequest(ctx, pathFileDetail, map[string]any{"fileId": current}, &data); err != nil {
			return "", err
		}
		if name := strings.TrimSpace(data.FileInfo.FileName); name != "" {
			segments = append(segments, name)
		}
		parent := strings.TrimSpace(data.FileInfo.ParentID)
		if parent == current {
			break
		}
		current = parent
	}
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}
	return "/" + strings.Join(segments, "/"), nil
}

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
