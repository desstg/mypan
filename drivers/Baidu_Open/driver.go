package baiduopen

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

type Driver struct {
	driver.AuthRefreshControl
	add    Addition
	client *http.Client

	oauthBase string

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu      sync.Mutex
	token   string
	refresh string

	// cookie 是网页版 Cookie，用于**分享转存**与**原生离线下载**（见 web_transport.go）。
	// 与 115 那份网页 Cookie 一样，它是与开放平台令牌并列的另一份凭据。
	cookie string
	// cookieJar 是**会话里新拿到的**网页 cookie（Set-Cookie 的回写），
	// 与配置里那份 d.cookie 合并着发，见 cookieHeaderLocked 的说明。
	cookieJar map[string]string
	// 网页接口的 bdstoken 缓存（绑在 Cookie 上，见 web_transport.go）。
	bdstoken       string
	bdstokenCookie string

	transferMD5Mu    sync.Mutex
	transferMD5Cache map[string]string
}

var config = driver.Config{
	Name:                   "baidu_open",
	DisplayName:            "百度网盘Open",
	Description:            "百度网盘官方开放API接入，当前支持OAuth认证与文件浏览",
	CardTags:               []string{"官方授权", "OAuth", "MD5分享"},
	SortOrder:              5,
	AuthLabel:              "OAuth",
	CardColor:              "#2932E1",
	CardLogo:               "/logos/baidu.png",
	DefaultRoot:            "/",
	AuthType:               driver.AuthToken,
	SupportsAccountProfile: true,
	OAuthName:              "百度网盘Open",
	TokenLifetime:          30 * 24 * time.Hour,
	RefreshAdvance:         10 * time.Hour,
	ProvideHashes:          []string{"md5"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetOAuthServer(baseURL string) { d.oauthBase = strings.TrimSpace(baseURL) }

// currentCookie 读网页 Cookie。
//
// ⚠️ 与 115 同一条路：值来自 account_auth_states（经 SetAuthCredentials 注入到
// d.cookie），而不是 Addition.Cookie 字段 —— 那个字段只是写入/展示用的入口。
func (d *Driver) currentCookie() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cookieHeaderLocked()
}

// cookieHeaderLocked 拼出要发出去的 Cookie 头，**调用方必须已持有 d.mu**。
//
// ⚠️ 这里必须把**会话里新拿到的** cookie 拼在配置那份后面，2026-10-13 真机踩到：
//
//   - `/share/verify` 校验提取码成功时，百度会 `Set-Cookie: BDCLND=...`
//     （提取码派生的访问凭证）与 `PANPSC=...`（新的会话票据，且会让旧的失效）。
//   - 不接住它们，下一步 `/share/list` 就回 **errno -9**（拿不到分享内容），
//     再下一步 `/share/transfer` 必然一起失败 —— 报错完全看不出是「少了两个 cookie」。
//
// 放在驱动上（而不是 `http.Client` 的 `Jar`）是因为我们的 client 是**共享池**里的，
// 塞一个 jar 进去会把别的账号的 cookie 串到一起。
func (d *Driver) cookieHeaderLocked() string {
	base := strings.TrimSpace(d.cookie)
	if base == "" {
		return ""
	}
	if len(d.cookieJar) == 0 {
		return base
	}
	keys := make([]string, 0, len(d.cookieJar))
	for k := range d.cookieJar {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, base)
	for _, k := range keys {
		parts = append(parts, k+"="+d.cookieJar[k])
	}
	return strings.Join(parts, "; ")
}

// absorbCookieJar 收下响应里的 Set-Cookie，合并进会话。
//
// `deleteme` 是百度清 cookie 的哨兵（删除该键）。
func (d *Driver) absorbCookieJar(setCookies []string) {
	if len(setCookies) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cookieJar == nil {
		d.cookieJar = map[string]string{}
	}
	for _, raw := range setCookies {
		name, value, ok := splitSetCookie(raw)
		if !ok {
			continue
		}
		if value == "" || value == "deleteme" {
			delete(d.cookieJar, name)
			continue
		}
		d.cookieJar[name] = value
	}
}

// splitSetCookie 取一条 Set-Cookie 的键与值（只要第一段，属性全部丢掉）。
func splitSetCookie(raw string) (name, value string, ok bool) {
	first := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
	idx := strings.IndexByte(first, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(first[:idx]), strings.TrimSpace(first[idx+1:]), true
}

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
	// 网页 Cookie 是**分享转存 / 原生离线下载**专用的一份附加凭据（见 web_transport.go）：
	// 开放平台令牌做不了这两个动作。存储位置与 115 那份网页 Cookie 一致 ——
	// account_auth_states.cookie。
	d.cookie = strings.TrimSpace(creds.Cookie)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}

	d.mu.Lock()
	token := d.token
	refresh := d.refresh
	d.mu.Unlock()
	if token == "" && refresh == "" {
		token = strings.TrimSpace(d.add.AccessToken)
		refresh = strings.TrimSpace(d.add.RefreshToken)
	}
	if token == "" && refresh == "" {
		return domain.Errorf(domain.CodeValidation, "access_token 与 refresh_token 不能都为空")
	}

	d.mu.Lock()
	d.token = token
	d.refresh = refresh
	// 网页 Cookie 与开放平台令牌是两份独立凭据：前者只在配置里填、不参与 OAuth 刷新，
	// 所以这里只是「配置里有就拿来用」，缺了不算初始化失败（不转存分享的账号不必填）。
	if cookie := strings.TrimSpace(d.add.Cookie); cookie != "" && d.cookie == "" {
		d.cookie = cookie
	}
	d.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		if _, err := d.doRefresh(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	return nil
}

func (d *Driver) Ping(ctx context.Context) error {
	// uinfo 含 uk/vip_type 等数字字段，Ping 只需确认 errno=0。
	return d.apiCall(ctx, http.MethodGet, opUserInfo, nil, nil, nil)
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "20011"):
		return prefix + "：百度开放平台应用仍在审核中，仅允许测试用户授权，请检查应用状态或测试用户名单"
	case strings.Contains(technical, "-7"):
		return prefix + "：百度返回目录无权访问，请把根目录设为应用授权目录，例如 /apps/应用名，或重新授权"
	case strings.Contains(technical, "20013") ||
		strings.Contains(technical, "31024") ||
		strings.Contains(technical, "权限不足") ||
		strings.Contains(technical, "没有访问权限"):
		return prefix + "：百度开放平台应用权限不足，请确认已申请网盘文件访问权限并重新授权"
	case strings.Contains(technical, "接口限流(6)") ||
		strings.Contains(technical, "错误(6)") ||
		strings.Contains(technical, "不允许接入用户数据"):
		return prefix + "：百度暂不允许该授权用户接入数据，请等待约 10 分钟后重新授权或重试"
	case strings.Contains(technical, "-6") ||
		strings.Contains(technical, "31045") ||
		strings.Contains(lower, "expired_token") ||
		strings.Contains(lower, "invalid_token") ||
		strings.Contains(lower, "auth_expired"):
		return prefix + "：百度访问令牌无效或已过期，请重新点击「自动获取 Token」"
	case strings.Contains(technical, "20012") ||
		strings.Contains(technical, "31034") ||
		strings.Contains(technical, "限流"):
		return prefix + "：百度接口调用过于频繁，请稍后再试"
	case strings.Contains(lower, "oauth 代理刷新失败") ||
		strings.Contains(lower, "缺少 refresh_token"):
		return prefix + "：OAuth 刷新失败，请重新点击「自动获取 Token」"
	case strings.Contains(technical, "百度 API 错误") ||
		strings.Contains(technical, "百度 HTTP"):
		return prefix + "：百度接口返回：" + technical
	case strings.Contains(technical, "json: cannot unmarshal"):
		return prefix + "：百度接口返回格式异常，请稍后重试或联系开发者"
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	dirPath := d.normalizePath(parentID)
	var items []domain.FileItem
	start := 0

	for {
		params := urlValues(map[string]string{
			"dir":       dirPath,
			"folder":    "0",
			"start":     strconv.Itoa(start),
			"limit":     strconv.Itoa(defaultListPageSize),
			"order":     "time",
			"desc":      "1",
			"web":       "1",
			"showempty": "1",
		})
		var resp listResp
		if err := d.apiCall(ctx, http.MethodGet, opFileList, params, nil, &resp); err != nil {
			if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodePermissionDenied && dirPath != d.rootPath() {
				return nil, domain.Errorf(domain.CodePermissionDenied, "%s。当前目录 %s 可能不在应用授权范围内，建议将根目录设置为应用目录，例如 /apps/应用名", ae.Message, dirPath)
			}
			return nil, err
		}
		for _, f := range resp.List {
			if strings.TrimSpace(f.Path) == "" {
				f.Path = d.childPath(dirPath, f.entryName())
			}
			items = append(items, fileToItem(f))
		}
		if len(resp.List) < defaultListPageSize {
			break
		}
		start += len(resp.List)
	}
	return items, nil
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	rawID := strings.TrimSpace(fileID)
	path := d.normalizePath(fileID)
	root := d.rootPath()
	if path == root {
		return &domain.FileItem{
			ID:     root,
			Name:   rootName(root),
			IsDir:  true,
			IDKind: domain.IDPath,
		}, nil
	}

	if isNumeric(rawID) {
		if item, err := d.getFileMetaByFsID(ctx, rawID); err == nil && item != nil {
			return item, nil
		}
	}

	parent := d.parentPath(path)
	children, err := d.ListFiles(ctx, parent)
	if err != nil {
		return nil, err
	}
	for _, item := range children {
		if d.normalizePath(item.ID) == path {
			return &item, nil
		}
	}
	return nil, domain.Errf(domain.CodeNotFound)
}

func (d *Driver) getFileMetaByFsID(ctx context.Context, fsID string) (*domain.FileItem, error) {
	params := urlValues(map[string]string{
		"fsids": "[" + fsID + "]",
		"dlink": "1",
	})
	var resp metasResp
	if err := d.apiCall(ctx, http.MethodGet, opFileMetas, params, nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.List) == 0 {
		return nil, domain.Errf(domain.CodeNotFound)
	}
	item := fileToItem(resp.List[0])
	return &item, nil
}

var (
	_ driver.Driver                   = (*Driver)(nil)
	_ driver.InfoGetter               = (*Driver)(nil)
	_ driver.Downloader               = (*Driver)(nil)
	_ driver.Deleter                  = (*Driver)(nil)
	_ driver.Mover                    = (*Driver)(nil)
	_ driver.Copier                   = (*Driver)(nil)
	_ driver.Renamer                  = (*Driver)(nil)
	_ driver.FolderCreator            = (*Driver)(nil)
	_ driver.LocalUploader            = (*Driver)(nil)
	_ driver.AuthRefresher            = (*Driver)(nil)
	_ driver.OAuthConsumer            = (*Driver)(nil)
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.TransferHashResolver     = (*Driver)(nil)
	_ driver.ShareReceiver            = (*Driver)(nil)
	_ driver.ShareKindReporter        = (*Driver)(nil)
	_ driver.ShareMetaPeeker          = (*Driver)(nil)
	_ driver.OfflineDownloadProvider  = (*Driver)(nil)
	_ driver.OfflineURLDownloader     = (*Driver)(nil)
	_ driver.OfflineTaskRefresher     = (*Driver)(nil)
	_ driver.OfflineTaskDeleter       = (*Driver)(nil)
	_ driver.WebLoginChecker          = (*Driver)(nil)
)
