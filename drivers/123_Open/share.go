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

// 123 的**分享转存**。
//
// 走网页接口（见 web_transport.go 的开头说明），两步：
//
//  1. `GET /b/api/share/get` 列分享根目录，拿条目 id + etag；
//  2. `POST /b/api/restful/goapi/v1/file/copy/save` 把这些条目存进目标目录。
//
// ⚠️ 两处与「照抄第三方实现」不同的地方，都是实测出来的：
//
//   - **字段名用 camelCase**（fileID / parentFileID / fileName / driveID）。
//     p123client 那条路走的是 `/b/api/file/copy/async` + snake_case，
//     实测回 `缺少Etag`；TgtoDrive 那条 goapi/save + camelCase 才通。
//   - **share/get 必须带 orderBy**，漏了回 `未定义OrderBy`（400）。
//
// `ReceiveShare` 的语义是「整份接收」：分享根目录下的条目全部收下，
// 目录在目标位置重建（copy/save 会把目录整棵树带过去，不需要像 115 那样自己建）。

// shareWebItem 是 share/get 返回的一条条目。
type shareWebItem struct {
	FileID   int64  `json:"FileId"`
	FileName string `json:"FileName"`
	// Type: 0 文件 / 1 目录。
	Type int    `json:"Type"`
	Size int64  `json:"Size"`
	Etag string `json:"Etag"`
}

type shareGetData struct {
	// Next 为 "-1" 表示最后一页。
	Next     string         `json:"Next"`
	Len      int            `json:"Len"`
	Expired  bool           `json:"Expired"`
	InfoList []shareWebItem `json:"InfoList"`
}

// WebLoginReady 报告「网页登录态才有能力」的那部分（磁力离线、分享转存）当下能不能用。
//
// 廉价、不发请求：只看配置里有没有网页账号密码。
func (d *Driver) WebLoginReady() bool {
	user, password := d.webCredentials()
	return user != "" && password != ""
}

// ShareKinds 报告这个驱动转存 123 自己的分享链。
func (d *Driver) ShareKinds() string { return driver.ShareKind123 }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 只判断配置里有没有网页账号密码；真正能不能登进去要等实际调用。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	user, password := d.webCredentials()
	if user == "" || password == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该 123 账号还没配「网页账号 / 密码」，无法转存分享；" +
				"到账号配置里填上 123 的账号（手机号或邮箱）与密码即可" +
				"（分享转存与磁力离线只有网页接口能做，开放平台令牌不认）",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份 123 分享整份转存进目标目录。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	target := strings.TrimSpace(req.TargetCID)
	if target == "" {
		target = "0"
	}

	// 目标目录核对：与 115 那条路同一条原则 —— 转存是真的往用户网盘里写东西，
	// 写错目录比写不进去严重得多。DisplayPath 非空时驱动必须先确认 TargetCID
	// 确实指向那个路径，对不上就拒绝。
	if err := d.verifyTargetDir(ctx, target, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	items, err := d.webShareList(ctx, code, req.ReceiveCode)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(items) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "123 分享是空的，或已失效")
	}

	// ⚠️ parentFileID 必须发**数字**，发字符串会回
	// `fileList.Alias.parentFileID格式异常`（真机 2026-10-11 实测）——
	// 服务端把它当目录 id 解析，`"0"` 过不了。
	targetID, err := parseDirID(target)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	fileList := make([]map[string]any, 0, len(items))
	for _, it := range items {
		fileList = append(fileList, map[string]any{
			"fileID":       it.FileID,
			"size":         it.Size,
			"etag":         it.Etag,
			"type":         it.Type,
			"parentFileID": targetID,
			"fileName":     it.FileName,
			"driveID":      0,
		})
	}

	var saved struct {
		TaskID int64 `json:"taskID"`
	}
	if err := d.webRequest(ctx, http.MethodPost, pathWebShareSave, nil, map[string]any{
		"fileList":     fileList,
		"shareKey":     code,
		"sharePwd":     strings.TrimSpace(req.ReceiveCode),
		"currentLevel": 0,
	}, &saved); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// 123 的转存是**异步任务**（返回 taskID）。这里不阻塞等它落盘：
	// 分享转存在调用方那里本来就是同步语义（115 那条路返回时文件已经在网盘上了），
	// 但等 123 的任务跑完可能要几十秒，而任务 id 已经能证明「收下了」。
	// 与 115 的 4200045「已接收过」同一条原则：宁可让用户看到「已提交」，
	// 也不要把一次真实成功记成失败。
	return driver.ShareReceiveResult{
		Count: len(items),
	}, nil
}

// parseDirID 把目录 id 字符串转成数字。123 只认数字形态（见 ReceiveShare 的说明），
// 非数字在这里就挡掉，别发出去换一句看不懂的接口错误。
func parseDirID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id < 0 {
		return 0, domain.Errorf(domain.CodeValidation, "123 目标目录 ID 不正确：%s", raw)
	}
	return id, nil
}

// verifyTargetDir 确认目录 id 真的指向 expectPath。
//
// expectPath 为空时跳过（调用方没要求核对）—— 手工转存那一侧用户是自己在
// 目录选择器里点的，没必要多打一次接口。
//
// 与 115 那条路同一条原则：转存是真的往用户网盘里写东西，**写错目录比写不进去
// 严重得多**，所以只要调用方给了路径，就必须先核对再动手。
func (d *Driver) verifyTargetDir(ctx context.Context, dirID, expectPath string) error {
	expectPath = strings.TrimSpace(expectPath)
	if expectPath == "" || dirID == "0" {
		// 根目录没有面包屑可核（expectPath 是 "/" 时 dirID 必为 "0"）。
		return nil
	}
	actual, err := d.dirFullPath(ctx, dirID)
	if err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在 123 里读不出来：%v；请重新选择目标目录", dirID, err)
	}
	if !sameDirPath(actual, expectPath) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：123 里 %s 是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			dirID, actual, expectPath)
	}
	return nil
}

// dirFullPath 从目录 id 往上拼出完整路径（`/a/b`），根目录返回 `/`。
//
// 123 没有 115 那种现成的面包屑字段，只能一层层往上问 parentFileID。
// 用驱动自己的 apiCall 直接读文件详情（domain.FileItem 不带父目录 id，
// 而这里正需要它来往上走）。
func (d *Driver) dirFullPath(ctx context.Context, dirID string) (string, error) {
	segments := make([]string, 0, 4)
	current := strings.TrimSpace(dirID)
	for depth := 0; current != "" && current != "0"; depth++ {
		if depth > maxPathDepth {
			return "", domain.Errorf(domain.CodeDriverError, "目录层级过深，可能形成了环")
		}
		params := url.Values{}
		params.Set("fileID", current)
		var raw json.RawMessage
		if err := d.apiCall(ctx, http.MethodGet, pathFileDetail, params, nil, &raw); err != nil {
			return "", err
		}
		entry, err := parseFileDetail(raw)
		if err != nil {
			return "", domain.Wrap(domain.CodeDriverError, err)
		}
		if name := strings.TrimSpace(entry.Filename); name != "" {
			segments = append(segments, name)
		}
		parent := entry.parentID()
		if parent == current {
			break
		}
		current = parent
	}
	// 从叶子往上收的，反转回来。
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}
	return "/" + strings.Join(segments, "/"), nil
}

// maxPathDepth 是往上拼路径时的深度上限，防环。
const maxPathDepth = 32

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

// webShareList 读分享根目录下的全部条目。
func (d *Driver) webShareList(ctx context.Context, code, pwd string) ([]shareWebItem, error) {
	out := make([]shareWebItem, 0, 8)
	page := 1
	for {
		query := url.Values{}
		query.Set("ShareKey", code)
		if p := strings.TrimSpace(pwd); p != "" {
			query.Set("SharePwd", p)
		}
		query.Set("limit", "100")
		query.Set("next", "0")
		query.Set("Page", strconv.Itoa(page))
		query.Set("parentFileId", "0")
		// ⚠️ orderBy 是必填的，漏了会回「未定义OrderBy」。
		query.Set("orderBy", "file_name")
		query.Set("orderDirection", "asc")
		query.Set("event", "homeListFile")

		var data shareGetData
		if err := d.webRequest(ctx, http.MethodGet, pathWebShareGet, query, nil, &data); err != nil {
			return nil, err
		}
		if data.Expired {
			return nil, domain.Errorf(domain.CodeNotFound, "123 分享已失效")
		}
		out = append(out, data.InfoList...)
		// Next == "-1" 表示最后一页；条目不足一页也直接停，少打一次请求。
		if data.Next == "-1" || data.Next == "" || len(data.InfoList) < webPageLimit {
			break
		}
		page++
		if page > 50 {
			// 兜底：分享里几千个文件时不至于无限翻页。
			break
		}
	}
	return out, nil
}

// PeekShare 看一眼分享里有什么（不写入）。
//
// ⚠️ 实测（2026-10-11）：123 的 share/get **读得到但要凭据** —— 不带 token 会被拒，
// 而带**开放平台**那份 token 反而能读（只有写入的转存那一步才校验网页登录态）。
// 这里仍然走网页通道（要网页凭据）：一条路径更好维护，而且订阅预览本来就在
// 「已经配好了账号」的前提下跑。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	items, err := d.webShareList(ctx, code, req.ReceiveCode)
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	res := driver.SharePeekResult{FileCount: len(items)}
	if len(items) > 0 {
		res.Title = items[0].FileName
		res.FileName = items[0].FileName
		for _, it := range items {
			res.TotalSize += it.Size
		}
	}
	return res, nil
}
