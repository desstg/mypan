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

// 百度网盘的**分享链转存**。
//
// 走网页接口（见 web_transport.go），三步：
//
//  1. 拉分享页 HTML，从 `locals.mset({...})` / `yunData.setData({...})` 里解析出
//     `shareid` / `share_uk`（= 转存接口的 `from`）/ `bdstoken`；
//  2. 有提取码时 `POST /share/verify`（form `pwd` / `vcode` / `vcode_str`）过一遍；
//  3. 列分享根目录（`GET /share/list`）拿到 fs_id 列表 → `POST /share/transfer`
//     （form `fsidlist` / `path`）存进自己的目标目录。
//
// ⚠️ 这是本次三个新驱动里**最脆**的一条：第 1 步依赖百度分享页的 HTML 结构
// （参考实现 BaiduPCS-Go 就为它打过两次补丁）。所以解析走「候选正则列表」，
// 失败时给出一句指向「分享页结构可能已变」的话，而不是含糊的解析错误。

// ShareKinds 报告这个驱动能转存百度自己的分享链。
//
// ⚠️ 必须实现：internal/driver/share.go 的契约是「不实现 ShareKindReporter 就按
// 只认 115 处理」，不实现的话百度分享会被当成 115 的分享码去打 115 的接口。
func (d *Driver) ShareKinds() string { return driver.ShareKindBaidu }

// ShareReceiveCapabilities 报告当前账号能不能做分享转存。
//
// 廉价、不发请求 —— 它在派发窗口的过滤阶段会被调用，那一段绝不能有网络往返。
// 判据只有「配没配网页 Cookie」；Cookie 过期会在实际请求里以 errno 报出来。
func (d *Driver) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	if strings.TrimSpace(d.currentCookie()) == "" {
		return driver.ShareReceiveCapabilities{
			Reason: "该百度账号还没配「网页 Cookie」，无法转存分享；" +
				"到账号配置里粘贴一份 pan.baidu.com 的浏览器 Cookie 即可" +
				"（百度转存只有网页接口能做，开放平台令牌不认）",
		}
	}
	return driver.ShareReceiveCapabilities{Ready: true}
}

// ReceiveShare 把一份百度分享整份转存进目标目录。
func (d *Driver) ReceiveShare(ctx context.Context, req driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	targetPath := strings.TrimSpace(req.TargetCID)
	if targetPath == "" {
		targetPath = d.rootPath()
	}

	// ① 目标目录核对。必须在转存**之前** —— 写错目录比写不进去严重得多。
	//    百度是**纯路径**驱动（fileID 就是路径），所以核对 = 归一化比对
	//    + 确认这个路径在网盘里真的存在且是目录（路径被删掉时字符串仍相等）。
	if err := d.verifyTargetDir(ctx, targetPath, strings.TrimSpace(req.TargetPath)); err != nil {
		return driver.ShareReceiveResult{}, err
	}

	shareURL := panBaseURL + "/s/" + code
	tokens, err := d.panSharePage(ctx, shareURL, code)
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}

	// ② 有提取码先过校验（提取码错在这一步就明确报出来）。
	if pwd := strings.TrimSpace(req.ReceiveCode); pwd != "" {
		if err := d.shareVerify(ctx, code, pwd); err != nil {
			return driver.ShareReceiveResult{}, err
		}
	}

	// ③ 列分享根目录，拿 fs_id。
	fsIDs, _, err := d.shareList(ctx, tokens, "/")
	if err != nil {
		return driver.ShareReceiveResult{}, err
	}
	if len(fsIDs) == 0 {
		return driver.ShareReceiveResult{}, domain.Errorf(domain.CodeNotFound, "百度分享是空的，或已失效")
	}

	// ④ 转存。
	if err := d.shareTransfer(ctx, tokens, fsIDs, targetPath); err != nil {
		return driver.ShareReceiveResult{}, err
	}
	return driver.ShareReceiveResult{
		TargetPath: d.normalizePath(targetPath),
		Count:      len(fsIDs),
		FileIDs:    fsIDs,
	}, nil
}

// PeekShare 看一眼分享里有什么（不写入任何东西）。
//
// ⚠️ 与 115 的 `share/snap` 不同，百度这一步**要网页 Cookie** ——
// 分享页里的 loginstate 只有登录态下才带。所以没配 Cookie 时它也会失败。
func (d *Driver) PeekShare(ctx context.Context, req driver.SharePeekRequest) (driver.SharePeekResult, error) {
	code := strings.TrimSpace(req.ShareCode)
	if code == "" {
		return driver.SharePeekResult{}, domain.Errorf(domain.CodeValidation, "分享码为空")
	}
	shareURL := panBaseURL + "/s/" + code
	tokens, err := d.panSharePage(ctx, shareURL, code)
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	if pwd := strings.TrimSpace(req.ReceiveCode); pwd != "" {
		if err := d.shareVerify(ctx, code, pwd); err != nil {
			return driver.SharePeekResult{}, err
		}
	}
	fsIDs, names, err := d.shareList(ctx, tokens, "/")
	if err != nil {
		return driver.SharePeekResult{}, err
	}
	res := driver.SharePeekResult{
		FileCount: len(fsIDs),
		OwnerID:   tokens.ShareUK,
	}
	if len(names) > 0 {
		res.Title = names[0]
		res.FileName = names[0]
	}
	return res, nil
}

//——————————————————— 底层调用 ———————————————————

// shareVerify 过提取码校验。
//
// ⚠️ `/share/verify` 要的 `surl` 是**去掉前导 `1`** 的那一段
// （`/s/1XSoUbEG` → `XSoUbEG`），与抓分享页时用的路径**不是同一个东西** ——
// 分享页要 `/s/1XSoUbEG`，verify 要 `XSoUbEG`。传错只会得到一句与提取码无关的
// 「非法请求」，很容易误判成提取码不对。参考实现（BaiduPCS-Go）也是这么剥的。
func (d *Driver) shareVerify(ctx context.Context, code, pwd string) error {
	surl := strings.TrimPrefix(strings.TrimSpace(code), "1")
	query := url.Values{}
	query.Set("surl", surl)
	query.Set("t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("app_id", panAppID)
	query.Set("clienttype", "0")

	form := url.Values{}
	form.Set("pwd", pwd)
	form.Set("vcode", "")
	form.Set("vcode_str", "")

	var resp struct {
		Errno  json.Number `json:"errno"`
		ErrMsg string      `json:"errmsg"`
	}
	rawURL := panBaseURL + "/share/verify?" + query.Encode()
	// ⚠️ Referer 必须指向 `share/init`，与参考实现一致：带错 Referer 会被判成
	// 「非法请求」，报出来的错与提取码无关。这里用 panJSON（它不校验 errno），
	// 因为提取码错本身就是一个非零 errno，交给下面自己判。
	if err := d.panJSONWithReferer(ctx, http.MethodPost, rawURL, form,
		"https://pan.baidu.com/share/init?surl="+surl, &resp); err != nil {
		return err
	}
	if codeNum, _ := resp.Errno.Int64(); codeNum != 0 {
		if codeNum == -9 || codeNum == -62 {
			return domain.Errorf(domain.CodeValidation, "百度分享提取码不正确")
		}
		if codeNum == 8001 {
			return domain.Errorf(domain.CodeValidation,
				"百度触发了验证（errno 8001）：请在浏览器里打开这条分享链并通过验证后再试")
		}
		return domain.Errorf(domain.CodeValidation,
			"百度分享校验失败(%d)：%s", codeNum, firstNonEmptyString(resp.ErrMsg, "提取码可能不正确"))
	}
	return nil
}

// shareListItem 是分享目录里的一条。
//
// ⚠️ `isdir` 用 json.Number 收，不是 int —— 2026-10-13 真机踩到：
// 同一条响应里它可能是数字也可能是字符串（`"0"`），按 int 收会让**整包解析失败**
// （`cannot unmarshal string into ... isdir of type int`），表现是「列分享目录失败」。
// 判据只需要「非零即目录」，宽松收完再自己判。
type shareListItem struct {
	FsID           json.Number `json:"fs_id"`
	ServerFilename string      `json:"server_filename"`
	Path           string      `json:"path"`
	IsDir          json.Number `json:"isdir"`
	Size           json.Number `json:"size"`
}

func (it shareListItem) isDir() bool {
	v, err := it.IsDir.Int64()
	if err != nil {
		// 字符串形态兜底。
		if f, ferr := strconv.ParseFloat(strings.TrimSpace(it.IsDir.String()), 64); ferr == nil {
			return f != 0
		}
		return false
	}
	return v != 0
}

// shareList 列分享目录下的条目，返回 fs_id 与名字。
//
// 只读根目录一层：转存接口收的 fs_id 列表里放**目录**的 id 时，百度会把整棵
// 子树一起带过去（与 123 的 copy/save 同一种语义），不需要自己递归。
func (d *Driver) shareList(ctx context.Context, tokens *panShareTokens, dir string) ([]string, []string, error) {
	query := url.Values{}
	query.Set("channel", "chunlei")
	query.Set("clienttype", "0")
	query.Set("web", "1")
	query.Set("page", "1")
	query.Set("num", "100")
	query.Set("dir", dir)
	query.Set("t", strconv.FormatFloat(float64(time.Now().UnixNano())/1e9, 'f', 3, 64))
	query.Set("uk", tokens.ShareUK)
	query.Set("shareid", tokens.ShareID)
	// ⚠️ `root=1` 必填。2026-10-13 真机对比出来的：少了它 `/share/list` 回
	// `errno 2`（"啊哦，链接出错了"），看着像分享坏了，其实只是少一个参数。
	query.Set("root", "1")
	query.Set("order", "other")
	query.Set("desc", "1")
	query.Set("showempty", "0")

	var resp struct {
		Errno  json.Number     `json:"errno"`
		ErrMsg string          `json:"errmsg"`
		List   []shareListItem `json:"list"`
	}
	if err := d.panRequest(ctx, http.MethodGet, panBaseURL+"/share/list?"+query.Encode(), nil, &resp); err != nil {
		return nil, nil, err
	}
	fsIDs := make([]string, 0, len(resp.List))
	names := make([]string, 0, len(resp.List))
	for _, it := range resp.List {
		id := strings.TrimSpace(it.FsID.String())
		if id == "" {
			continue
		}
		fsIDs = append(fsIDs, id)
		names = append(names, firstNonEmptyString(it.ServerFilename, baseNameOfShare(it.Path)))
	}
	return fsIDs, names, nil
}

// shareTransfer 把 fs_id 列表存进目标目录。
func (d *Driver) shareTransfer(ctx context.Context, tokens *panShareTokens, fsIDs []string, targetPath string) error {
	bdstoken, err := d.panBdstoken(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokens.BDSToken) != "" {
		bdstoken = tokens.BDSToken
	}

	query := url.Values{}
	query.Set("shareid", tokens.ShareID)
	query.Set("from", tokens.ShareUK)
	query.Set("bdstoken", bdstoken)
	query.Set("channel", "chunlei")
	query.Set("web", "1")
	query.Set("app_id", panAppID)
	query.Set("clienttype", "0")

	form := url.Values{}
	form.Set("fsidlist", "["+strings.Join(fsIDs, ",")+"]")
	form.Set("path", d.normalizePath(targetPath))

	// ⚠️ 这里用 panJSON 而不是 panRequest：转存撞上同名文件时 errno 非零（12），
	// 而真实原因在 info[0].errno —— 用 panRequest 会丢掉 info，只能报一句含糊的
	// 「errno 12」。自己解包才能给出「目标目录下已有同名文件」这种可执行的话。
	var resp struct {
		Errno  json.Number     `json:"errno"`
		ErrMsg string          `json:"errmsg"`
		Info   json.RawMessage `json:"info"`
	}
	if err := d.panJSON(ctx, http.MethodPost, panBaseURL+"/share/transfer?"+query.Encode(), form, &resp); err != nil {
		return err
	}
	if code, _ := resp.Errno.Int64(); code != 0 {
		return mapPanErrno(code, firstNonEmptyString(resp.ErrMsg, "转存失败"), resp.Info)
	}
	return nil
}

//——————————————————— 目标目录核对 ———————————————————

// verifyTargetDir 确认 targetPath 真的是调用方期望的那个目录。
//
// 百度是**纯路径**驱动（fileID 即路径），所以核对分两步：
//
//  1. 归一化后字符串必须相等（写法差异容忍，真的不同就拒）；
//  2. 该路径必须**在网盘里存在且是目录** —— 只看字符串相等是不够的：
//     目录被删掉之后「记录里的路径」与「传进来的路径」仍然相等，
//     而转存会把文件塞进一个百度自动新建的同名目录，用户以为存对了地方。
func (d *Driver) verifyTargetDir(ctx context.Context, targetPath, expectPath string) error {
	normalized := d.normalizePath(targetPath)
	if expect := strings.TrimSpace(expectPath); expect != "" && !sameDirPath(normalized, expect) {
		return domain.Errorf(domain.CodeValidation,
			"目标目录对不上：本次要存的是「%s」，而记录里的是「%s」。为避免把文件存错地方，本次转存已中止；请重新选择目标目录",
			normalized, expect)
	}
	// 路径存在性检查：列一下这个目录，失败即认为目标不可用。
	if _, err := d.listDirExists(ctx, normalized); err != nil {
		return domain.Errorf(domain.CodeValidation,
			"目标目录「%s」在百度网盘里读不出来：%v；请重新选择目标目录", normalized, err)
	}
	return nil
}

// listDirExists 列一次目录，确认它存在且是目录。
//
// ⚠️ 这是一次**开放平台**调用（要 access_token），不是网页接口 ——
// 目标目录的 ID 本来就是开放平台给的路径，用同一条通道核对才自洽。
func (d *Driver) listDirExists(ctx context.Context, path string) (bool, error) {
	params := urlValues(map[string]string{
		"dir":    path,
		"order":  "name",
		"limit":  "1",
		"start":  "0",
		"web":    "1",
		"folder": "0",
	})
	var resp listResp
	if err := d.apiCall(ctx, http.MethodGet, opFileList, params, nil, &resp); err != nil {
		return false, err
	}
	return true, nil
}

func baseNameOfShare(p string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(p), "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
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
