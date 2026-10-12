package quark

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

// 夸克的**分享链转存**。
//
// 三步（与 115/123 的分享转存同一套骨架，见 drivers/123_Open/share.go）：
//
//  1. `POST /1/clouddrive/share/sharepage/token` —— 用分享码（+提取码）换 `stoken`；
//  2. `GET  /1/clouddrive/share/sharepage/detail` —— 列分享根目录，拿每个条目的
//     `fid` 与 `fid_token`（**两者必须成对**，见 shareEntry 的说明）；
//  3. `POST /1/clouddrive/share/sharepage/save` —— 把这批条目存进自己的目标目录。
//
// ⚠️ 与 115/123 最大的一处不同：**夸克没有「网页凭据」这一层**，它平时就用 Cookie
// （drivers/Quark/auth.go 的 currentCookie），分享转存同样走这份 Cookie。
// 所以这里没有 123 那种 webUser/webPassword 换取 token 的机制。
//
// ⚠️ save 是**异步任务**（返回 task_id）。这里不阻塞等它落盘 —— 与 123 同一条原则：
// 任务号已经能证明「收下了」，等它跑完可能要几十秒，而把一次真实成功记成失败
// 对用户更糟。任务状态交给夸克自己的任务列表去收敛。

// ShareKinds 报告这个驱动能转存夸克自己的分享链。
//
// ⚠️ 这个方法**必须实现**：internal/driver/share.go 的契约是「不实现
// ShareKindReporter 就按只认 115 处理」，不实现的话夸克分享会被当成 115 的
// 分享码去打 115 的接口。
func (d *Driver) ShareKinds() string { return driver.ShareKindQuark }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 夸克只有 Cookie 一份凭据，所以判据就是「配没配 Cookie」；
// 真正能不能转要等实际调用（Cookie 过期会在请求里以 401 报出来）。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该夸克账号还没配 Cookie，无法转存分享；" +
				"到账号配置里扫码登录或粘贴夸克网页版的完整 Cookie 即可",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份夸克分享整份转存进目标目录。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	target := strings.TrimSpace(req.TargetCID)
	if target == "" {
		target = d.rootID()
	}

	// ① 目标目录核对。必须在转存**之前** —— 转存是真的往用户网盘里写东西，
	//    写错目录比写不进去严重得多。与 115/123 同一条闸。
	if err := d.verifyTargetDir(ctx, target, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② 换 stoken（提取码不对/分享失效都在这一步报出来）。
	stoken, err := d.shareToken(ctx, code, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ③ 列条目：根目录 + 递归子目录，拿到能转存的那批（fid + fid_token 成对）。
	entries, err := d.collectShareEntries(ctx, code, stoken)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(entries) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "夸克分享是空的，或已失效")
	}

	fids := make([]string, 0, len(entries))
	tokens := make([]string, 0, len(entries))
	for _, e := range entries {
		fids = append(fids, e.FID)
		tokens = append(tokens, e.token())
	}

	// ④ 存进目标目录。
	taskID, err := d.shareSave(ctx, code, stoken, target, fids, tokens)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ⑤ 不阻塞等落盘（见文件头说明）。任务号只用于日志排查。
	_ = taskID
	return driver.ShareReceiveResult{Count: len(entries), FileIDs: fids}, nil
}

// PeekShare 看一眼分享里有什么（不写入任何东西）。
//
// ⚠️ **不填 TotalSize**：夸克的 detail 只给每个条目自己的 size，
// 分享根若是文件夹，size 要么是 0、要么是各家实现不一致的聚合值；
// 递归求和又会把文件夹与它里面的文件重复计入。填一个错的非零值比留 0 更糟
// （上层按它排序），所以这里只在「根下就一个文件」时才给。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	stoken, err := d.shareToken(ctx, code, strings.TrimSpace(req.ReceiveCode))
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	// 只读第一页：偷看只要文件名与条目数。
	list, total, err := d.sharePage(ctx, code, stoken, "0", 1)
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	count := total
	if count < len(list) {
		count = len(list)
	}
	res := driver.SharePeekResult{FileCount: count}
	if len(list) > 0 {
		res.Title = strings.TrimSpace(list[0].FileName)
		res.FileName = res.Title
		if len(list) == 1 && !list[0].isDir() {
			res.TotalSize = list[0].Size
		}
	}
	return res, nil
}

// ——————————————————— 底层调用 ———————————————————

// shareHosts 是夸克接口的宿主，**按顺序重试**。
//
// 各家实现用的域名不一致，而且会换：
//   - 实测可用的第三方实现（115-media-hub）token/detail 走 drive-h，
//     save 失败时退回 drive-pc；
//   - 夸克官方开放文档把 drive-pc 列为这几个接口的**游客等价入口**。
//
// 与其赌一个，不如打不通就换下一个 —— 单点域名哪天变了就是全盘不可用。
var shareHosts = []string{
	"https://drive-h.quark.cn/1/clouddrive",
	"https://drive-pc.quark.cn/1/clouddrive",
}

// shareRequest 在候选宿主上依次尝试，返回第一个成功的响应。
//
// 只对「网络/宿主本身的问题」重试：业务错误（提取码错、分享失效）是确定性的，
// 换域名也一样，直接返回。
func (d *Driver) shareRequest(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var lastErr error
	for _, host := range shareHosts {
		out2 := out
		err := func() error {
			_, err := d.apiRequestTo(ctx, host, method, path, cloneValues(query), body, out2)
			return err
		}()
		if err == nil {
			return nil
		}
		if !isHostRetryable(err) {
			return err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = domain.Errorf(domain.CodeDriverError, "夸克分享接口不可用（%s）", path)
	}
	return lastErr
}

// isHostRetryable 报告这个错误是不是「换个域名可能就好了」。
//
// 判据：业务错误码（分享失效/提取码错）与鉴权失败是确定性的，不重试；
// 其余（网络、HTTP 5xx、返回非 JSON 的壳）才换域名。
func isHostRetryable(err error) bool {
	ae, ok := domain.AsAppError(err)
	if !ok {
		return true
	}
	switch ae.Code {
	case domain.CodeNotFound, domain.CodeValidation, domain.CodeAuthExpired, domain.CodePermissionDenied:
		return false
	default:
		return true
	}
}

func cloneValues(v url.Values) url.Values {
	if v == nil {
		return url.Values{}
	}
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// shareToken 用分享码换 stoken。
func (d *Driver) shareToken(ctx context.Context, code, passcode string) (string, error) {
	body := map[string]any{"pwd_id": code, "passcode": passcode}
	var data shareTokenData
	if err := d.shareRequest(ctx, http.MethodPost, pathShareToken, nil, body, &data); err != nil {
		return "", err
	}
	token := data.value()
	if token == "" {
		return "", domain.Errorf(domain.CodeDriverError, "夸克没有返回分享令牌（stoken）")
	}
	return token, nil
}

// sharePage 读一页分享目录。
func (d *Driver) sharePage(ctx context.Context, code, stoken, parentFID string, page int) ([]shareEntry, int, error) {
	query := url.Values{}
	query.Set("pwd_id", code)
	// ⚠️ stoken **不要**预先转义。第三方实现里有一份「双重编码」的写法
	// （先手工 quote 再交给 urlencode），但 2026-10-12 真机实测**不成立**：
	// 那样发出去会回 `400 14001 非法token` —— 当前接口只 decode 一次，
	// url.Values.Encode() 自带的那一次就够了。多转一次 = 服务端拿到的是
	// 转义后的残串，报出来的却是一句像是分享坏了的「非法token」。
	query.Set("stoken", stoken)
	query.Set("pdir_fid", parentFID)
	query.Set("force", "0")
	query.Set("_page", strconv.Itoa(page))
	query.Set("_size", strconv.Itoa(listPageSize))
	query.Set("_fetch_banner", "0")
	query.Set("_fetch_share", "1")
	query.Set("_fetch_total", "1")
	query.Set("_sort", "file_type:asc,file_name:asc")

	var data shareDetailData
	var meta sharePageMeta
	// detail 的条目在 data.list，分页信息在 metadata —— 两处都要，所以分开解。
	//
	// ⚠️ `stoken` 要**预先转义一次**：detail 接口对它会再做一次 decode，
	// 只编码一次会回「非法token」—— 看起来像分享失效，实际是编码层数不对
	// （参考实现明确这么处理）。url.Values.Encode() 之后还会再转义一次，
	// 所以这里先手工 QueryEscape，等价于双重编码。
	if err := d.shareRequestWithMeta(ctx, pathShareDetail, query, &data, &meta); err != nil {
		return nil, 0, err
	}
	if data.IsExpire {
		return nil, 0, domain.Errorf(domain.CodeNotFound, "夸克分享已失效")
	}
	return data.List, meta.Total, nil
}

// shareRequestWithMeta 与 shareRequest 相同，但额外解出 metadata（分页信息）。
func (d *Driver) shareRequestWithMeta(ctx context.Context, path string, query url.Values, out, meta any) error {
	var lastErr error
	for _, host := range shareHosts {
		env, err := d.apiRequestTo(ctx, host, http.MethodGet, path, cloneValues(query), nil, out)
		if err == nil {
			if meta != nil && len(env.Metadata) > 0 {
				// metadata 解不出来不算失败：它只用于翻页判断。
				_ = json.Unmarshal(env.Metadata, meta)
			}
			return nil
		}
		if !isHostRetryable(err) {
			return err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = domain.Errorf(domain.CodeDriverError, "夸克分享接口不可用（%s）", path)
	}
	return lastErr
}

// collectShareEntries 把分享根目录**及其子目录**下的条目收齐。
//
// 为什么要递归：分享出去的往往是一个文件夹（比如「某剧 S01」），
// 只存根目录那一层会得到一个空壳目录 —— 文件一个都没进去。
// 与 115 那条「整份接收」的语义对齐。
func (d *Driver) collectShareEntries(ctx context.Context, code, stoken string) ([]shareEntry, error) {
	var out []shareEntry
	queue := []string{"0"}
	seen := map[string]struct{}{"0": {}}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		parent := queue[0]
		queue = queue[1:]

		for page := 1; ; page++ {
			list, total, err := d.sharePage(ctx, code, stoken, parent, page)
			if err != nil {
				return nil, err
			}
			for _, e := range list {
				out = append(out, e)
				if e.isDir() {
					if _, ok := seen[e.FID]; !ok && strings.TrimSpace(e.FID) != "" {
						seen[e.FID] = struct{}{}
						queue = append(queue, e.FID)
					}
				}
			}
			if len(list) < listPageSize || page*listPageSize >= total {
				break
			}
			if page > shareMaxPages {
				break
			}
		}
	}
	return out, nil
}

// shareMaxPages 是单层目录的翻页上限，防异常数据把循环拖死。
const shareMaxPages = 50

// shareSave 把条目存进目标目录。
//
// 返回任务 id（可能为空 —— 有的响应直接回文件列表而不给 task_id，那也算已提交）。
func (d *Driver) shareSave(ctx context.Context, code, stoken, targetFID string, fids, tokens []string) (string, error) {
	if len(tokens) != len(fids) {
		// 长度对不上就整份不带 token（宁可让服务端按 fid 自己找，
		// 也不要发一个错位的列表 —— 错位可能落进错的条目）。
		tokens = nil
	}
	body := map[string]any{
		"pwd_id": code,
		"stoken": stoken,
		// pdir_fid 是**分享侧**的父目录（根 = 0），to_pdir_fid 才是自己网盘的目标。
		"pdir_fid":    "0",
		"to_pdir_fid": targetFID,
		"fid_list":    fids,
		// ⚠️ pdir_save_all=false：只存 fid_list 里列出的那些。参考实现两处不一致
		// （一处 false 一处 true），先按「列什么存什么」发；真机若发现存的是整份
		// 分享，再切 true（见计划书的风险清单）。
		"pdir_save_all":  false,
		"fid_token_list": tokens,
		"scene":          "link",
	}
	var data shareSaveData
	if err := d.shareRequest(ctx, http.MethodPost, pathShareSave, nil, body, &data); err != nil {
		return "", err
	}
	return strings.TrimSpace(data.TaskID.String()), nil
}

// ——————————————————— 目标目录核对 ———————————————————

// verifyTargetDir 确认目录 id 真的指向 expectPath。
//
// expectPath 为空时跳过（调用方没要求核对）—— 手工转存那一侧用户是自己点的目录。
// 与 115/123 同一条原则：转存会真往用户网盘里写东西，**写错目录比写不进去严重得多**。
func (d *Driver) verifyTargetDir(ctx context.Context, dirID, expectPath string) error {
	expectPath = strings.TrimSpace(expectPath)
	root := d.rootID()
	if expectPath == "" || dirID == root || dirID == "0" {
		// 根目录没有面包屑可核（expectPath 是 "/" 时 dirID 必为根）。
		return nil
	}
	actual, err := d.dirFullPath(ctx, dirID)
	if err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录（%s）在夸克里读不出来：%v；请重新选择目标目录", dirID, err)
	}
	if !sameDirPath(actual, expectPath) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：夸克里 %s 是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			dirID, actual, expectPath)
	}
	return nil
}

// maxPathDepth 是往上拼路径时的深度上限，防环。
const maxPathDepth = 32

// dirFullPath 从目录 id 往上拼出完整路径（`/a/b`），根目录返回 `/`。
//
// 夸克没有现成的面包屑字段，只能一层层往上问 pdir_fid。
// 不能用 GetFileInfo：它返回 domain.FileItem，**不带父目录 id**，而这里正需要它往上走。
func (d *Driver) dirFullPath(ctx context.Context, dirID string) (string, error) {
	segments := make([]string, 0, 4)
	current := strings.TrimSpace(dirID)
	for depth := 0; current != "" && current != "0" && current != d.rootID(); depth++ {
		if depth > maxPathDepth {
			return "", domain.Errorf(domain.CodeDriverError, "目录层级过深，可能形成了环")
		}
		params := url.Values{}
		params.Set("fid", current)
		var entry fileEntry
		if _, err := d.apiRequest(ctx, http.MethodGet, pathInfo, params, nil, &entry); err != nil {
			return "", err
		}
		if name := strings.TrimSpace(entry.FileName); name != "" {
			segments = append(segments, name)
		}
		parent := strings.TrimSpace(entry.PdirFID)
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

// mapShareError 把分享相关的业务错误码翻成用户能看懂的话。
//
// 码值来自实测与参考实现（115-media-hub-main 的 quark provider、pansou 的链接体检）：
//
//	41008      → 需要提取码（或提取码不对）
//	41004/41010/41011 → 分享不存在/已失效/违规
//
// 其余交给上层按「夸克接口错误」处理。
func mapShareError(code int, msg string) error {
	switch code {
	case 41008:
		return domain.Errorf(domain.CodeValidation, "这条夸克分享需要提取码（或提取码不正确）")
	case 41004, 41010, 41011, 41012:
		// 41012「好友已取消了分享」是实测回的一个（2026-10-12），与失效同类。
		return domain.Errorf(domain.CodeNotFound, "夸克分享不存在、已失效或被取消：%s", msg)
	case 14001:
		return domain.Errorf(domain.CodeDriverError, "夸克不接受这次请求（非法token）：%s", msg)
	}
	return nil
}
