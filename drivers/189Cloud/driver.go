package cloud189

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

type Driver struct {
	driver.AuthRefreshControl
	add          Addition
	client       *http.Client
	uploadClient *http.Client

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu            sync.Mutex
	accessToken   string
	refreshToken  string
	sessionKey    string
	sessionSecret string
	familyKey     string
	familySecret  string
	familyID      string
	loginName     string
	itemCache     map[string]domain.FileItem
	itemOrder     []string

	transferMD5Mu    sync.Mutex
	transferMD5Cache map[string]string
}

var config = driver.Config{
	Name:                   "189_cloud",
	DisplayName:            "天翼云盘",
	Description:            "天翼云盘 PC 接口接入，支持个人云、家庭云和文件管理",
	CardTags:               []string{"个人云", "家庭云", "扫码登录", "支持302", "支持秒传"},
	SortOrder:              8,
	AuthLabel:              "扫码登录",
	CardColor:              "#FEC52C",
	CardLogo:               "/logos/tianyi.png",
	DefaultRoot:            "-11",
	AuthType:               driver.AuthToken,
	TokenLifetime:          7 * 24 * time.Hour,
	RefreshAdvance:         24 * time.Hour,
	SupportsAccountProfile: true,
	ProvideHashes:          []string{"md5"},
	RapidUploadHashes:      []string{"md5"},
	UploadConflictPolicies: []string{"overwrite", "rename", "skip", "fail"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accessToken = strings.TrimSpace(creds.AccessToken)
	d.refreshToken = strings.TrimSpace(creds.RefreshToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	if d.uploadClient == nil {
		d.uploadClient = httpx.NewClient(httpx.ClientOptions{
			Timeout:            300 * time.Second,
			DisableCompression: true,
			DisableKeepAlives:  true,
		})
	}
	d.mu.Lock()
	if d.accessToken == "" {
		d.accessToken = strings.TrimSpace(d.add.AccessToken)
	}
	if d.refreshToken == "" {
		d.refreshToken = strings.TrimSpace(d.add.RefreshToken)
	}
	access := d.accessToken
	empty := d.refreshToken == ""
	d.mu.Unlock()
	if empty {
		return domain.Errorf(domain.CodeValidation, "请先扫码登录获取天翼云盘授权")
	}
	if !d.hasSession() {
		if access != "" {
			if err := d.refreshSession(ctx, access); err != nil {
				// 仅当会话确实失效（认证类错误）才回退换取新 Token；
				// 网络故障/503/限流等非认证错误直接上报，交给统一节流重试，
				// 不额外换 Token、不追加一次会话请求（约定：非认证错误不触发认证刷新）。
				if !isSessionExpired(err) {
					return err
				}
				if _, rerr := d.doRefresh(ctx); rerr != nil {
					return rerr
				}
			}
		} else {
			if _, err := d.doRefresh(ctx); err != nil {
				return err
			}
		}
	}
	if d.isFamily() {
		if err := d.ensureFamilyID(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	httpx.CloseClient(d.uploadClient)
	return nil
}

func (d *Driver) Ping(ctx context.Context) error {
	_, err := d.ListFiles(ctx, d.rootID())
	return err
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "请先扫码登录") || strings.Contains(technical, "缺少 refresh_token"):
		return prefix + "：请点击扫码登录，使用天翼云盘 App 扫码授权"
	case strings.Contains(lower, "invalidsessionkey") || strings.Contains(technical, "重新扫码"):
		return prefix + "：天翼云盘会话已失效，请重新扫码登录"
	case strings.Contains(technical, "天翼云盘 API") || strings.Contains(technical, "天翼云盘上传"):
		return prefix + "：" + technical
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.apiParentID(parentID)
	endpoint := apiURL + "/listFiles.action"
	baseParams := map[string]string{
		"folderId":   parent,
		"fileType":   "0",
		"mediaAttr":  "0",
		"iconOption": "5",
	}
	if d.isFamily() {
		endpoint = apiURL + "/family/file/listFiles.action"
		baseParams["familyId"] = d.currentFamilyID()
		baseParams["orderBy"] = "1"
		baseParams["descending"] = "false"
	} else {
		baseParams["recursive"] = "0"
		baseParams["orderBy"] = "filename"
		baseParams["descending"] = "false"
	}
	var out []domain.FileItem
	for page := 1; page < 10000; page++ {
		var resp listResp
		params := make(map[string]string, len(baseParams)+2)
		for key, value := range baseParams {
			params[key] = value
		}
		params["pageNum"] = itoa(page)
		params["pageSize"] = itoa(listPageSize)
		err := d.apiRequest(ctx, http.MethodGet, endpoint, params, &resp)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, f := range resp.FileListAO.FolderList {
			f.isDir = true
			item := f.toFileItem()
			out = append(out, item)
			count++
		}
		for _, f := range resp.FileListAO.FileList {
			f.isDir = false
			item := f.toFileItem()
			out = append(out, item)
			count++
		}
		if count < listPageSize {
			break
		}
	}
	d.rememberItems(out)
	return out, nil
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	id := strings.TrimSpace(fileID)
	if id == "" || id == "/" || id == d.rootID() || (d.isFamily() && id == "-11") {
		return &domain.FileItem{
			ID:     d.rootID(),
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDStable,
		}, nil
	}
	if d.isFamily() {
		if item, ok := d.cachedItem(id); ok {
			return &item, nil
		}
		return d.findFamilyItem(ctx, id)
	}

	raw, err := d.fetchFileInfo(ctx, id)
	if err != nil {
		return nil, err
	}

	resolvedPath := raw.FilePath
	if strings.TrimSpace(resolvedPath) == "" && strings.TrimSpace(raw.Name) != "" {
		resolvedPath = "/" + strings.TrimSpace(raw.Name)
	}

	item, err := d.resolveItemByFilePath(ctx, resolvedPath)
	if err != nil {
		return nil, err
	}
	mergeFileInfo(item, raw)
	return item, nil
}

func (d *Driver) fetchFileInfo(ctx context.Context, fileID string) (*fileInfoResp, error) {
	var out fileInfoResp
	if err := d.apiRequest(ctx, http.MethodGet, apiURL+"/getFileInfo.action", map[string]string{
		"fileId": strings.TrimSpace(fileID),
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (d *Driver) resolveItemByFilePath(ctx context.Context, rawPath string) (*domain.FileItem, error) {
	path := normalize189Path(rawPath)
	if path == "" {
		return nil, domain.Errf(domain.CodeNotFound)
	}
	if path == "/" {
		return &domain.FileItem{
			ID:     d.rootID(),
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDStable,
		}, nil
	}

	parts := split189Path(path)
	if len(parts) == 0 {
		return nil, domain.Errf(domain.CodeNotFound)
	}

	parentID := d.rootID()
	for i, part := range parts {
		items, err := d.ListFiles(ctx, parentID)
		if err != nil {
			return nil, err
		}
		wantDir := i < len(parts)-1
		child, ok := find189Child(items, part, i == len(parts)-1, wantDir)
		if !ok {
			return nil, domain.Errf(domain.CodeNotFound)
		}
		parentID = child.ID
		if i == len(parts)-1 {
			item := child
			return &item, nil
		}
	}
	return nil, domain.Errf(domain.CodeNotFound)
}

func find189Child(items []domain.FileItem, name string, allowEither bool, wantDir bool) (domain.FileItem, bool) {
	for _, item := range items {
		if (allowEither || item.IsDir == wantDir) && item.Name == name {
			return item, true
		}
	}
	for _, item := range items {
		if (allowEither || item.IsDir == wantDir) && strings.EqualFold(item.Name, name) {
			return item, true
		}
	}
	return domain.FileItem{}, false
}

func normalize189Path(raw string) string {
	path := strings.ReplaceAll(strings.TrimSpace(raw), `\'`, `'`)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path == "/" {
		return path
	}
	return strings.TrimRight(path, "/")
}

func split189Path(path string) []string {
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeFileInfo(item *domain.FileItem, raw *fileInfoResp) {
	if item == nil || raw == nil {
		return
	}
	if item.Name == "" && strings.TrimSpace(raw.Name) != "" {
		item.Name = normalize189Name(raw.Name)
	}
	if item.Size <= 0 && !item.IsDir {
		item.Size = raw.size()
	}
	if item.ModTime.IsZero() {
		item.ModTime = parse189Time(firstNonNil(raw.LastOpTime, raw.LastOpTimeStr, raw.CreateDate))
	}
	if md5 := raw.contentMD5(); md5 != "" && !item.IsDir {
		if item.Hash == nil {
			item.Hash = map[domain.HashType]string{}
		}
		if item.Hash[domain.HashMD5] == "" {
			item.Hash[domain.HashMD5] = strings.ToLower(md5)
		}
	}
	if item.Thumb == "" {
		item.Thumb = parseThumb(raw.Icon)
	}
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
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.AccountProfileProvider   = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.QRLoginProvider          = (*Driver)(nil)
	_ driver.RapidUploader            = (*Driver)(nil)
	_ driver.TransferHashResolver     = (*Driver)(nil)
)
