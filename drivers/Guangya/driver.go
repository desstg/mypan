// Package guangya 接入光鸭云盘（逆向 API + 扫码登录换 Token）。
package guangya

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	add    Addition
	client *http.Client

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu          sync.Mutex
	token       string
	refresh     string
	deviceIDVal string
}

var config = driver.Config{
	Name:                   "guangya",
	DisplayName:            "光鸭云盘",
	Description:            "光鸭云盘接入",
	CardTags:               []string{"扫码登录", "支持302", "支持秒传"},
	SortOrder:              7,
	AuthLabel:              "扫码登录",
	CardColor:              "#FF7A1A",
	CardLogo:               "/logos/guangya.png",
	DefaultRoot:            "",
	AuthType:               driver.AuthToken,
	TokenLifetime:          7200 * time.Second,
	RefreshAdvance:         15 * time.Minute,
	ProvideHashes:          []string{"md5"},
	RapidUploadHashes:      []string{"md5"},
	UploadConflictPolicies: []string{"rename"},
	SupportsAccountProfile: true,
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	d.deviceIDVal = normalizeDeviceID(d.add.DeviceID)

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
	d.mu.Unlock()

	if d.token == "" {
		if _, err := d.doRefresh(ctx); err != nil {
			return err
		}
	}
	if err := d.validateToken(ctx); err != nil {
		if !domain.IsAuthExpiredError(err) {
			return err
		}
		if _, rerr := d.doRefresh(ctx); rerr != nil {
			return rerr
		}
		if err := d.validateToken(ctx); err != nil {
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
	if err := d.validateToken(ctx); err != nil {
		return err
	}
	_, err := d.ListFiles(ctx, "0")
	return err
}

func (d *Driver) deviceID() string {
	if id := strings.TrimSpace(d.deviceIDVal); id != "" {
		return id
	}
	d.deviceIDVal = normalizeDeviceID(d.add.DeviceID)
	return d.deviceIDVal
}

func normalizeDeviceID(deviceID string) string {
	candidate := strings.ToLower(strings.TrimSpace(deviceID))
	if len(candidate) == 32 && isHex(candidate) {
		return candidate
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("litepan-guangya-dev-id!!"))
	}
	return hex.EncodeToString(b)
}

func isHex(s string) bool {
	for _, ch := range s {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "refresh_token") && strings.Contains(technical, "不能都为空"):
		return prefix + "：请填写 refresh_token，或点击「扫码获取授权」"
	case strings.Contains(lower, "auth_expired") ||
		strings.Contains(technical, "认证失败") ||
		strings.Contains(lower, "oauth 代理刷新失败"):
		return prefix + "：光鸭 Token 无效或已过期，请重新扫码登录"
	case strings.Contains(technical, "光鸭 HTTP") || strings.Contains(technical, "光鸭 API"):
		return prefix + "：" + technical
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	return d.listByParent(ctx, parentID, defaultBrowseListOptions())
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	id := strings.TrimSpace(fileID)
	if id == "" {
		return nil, domain.Errorf(domain.CodeValidation, "file_id 不能为空")
	}
	entry, err := d.fetchFileDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	item := entry.toFileItem()
	return &item, nil
}

func (d *Driver) fetchFileDetail(ctx context.Context, fileID string) (fileEntry, error) {
	var data fileDetailData
	if err := d.apiRequest(ctx, pathFileDetail, map[string]any{"fileId": fileID}, &data); err == nil {
		if data.FileInfo.FileID != "" {
			return data.FileInfo, nil
		}
		return fileEntry{}, domain.Errorf(domain.CodeNotFound, "光鸭文件不存在")
	}
	var byID fileDetailData
	if err := d.apiRequest(ctx, pathFileInfoByID, map[string]any{"fileId": fileID}, &byID); err != nil {
		return fileEntry{}, err
	}
	if byID.FileInfo.FileID == "" {
		return fileEntry{}, domain.Errorf(domain.CodeNotFound, "光鸭文件不存在")
	}
	return byID.FileInfo, nil
}

type listOptions struct {
	dirType           *int
	fileTypes         []any
	resType           *int
	needSubFolderStat bool
	needPlayRecord    bool
	orderBy           int
	sortType          int
	pageSize          int
}

func defaultBrowseListOptions() listOptions {
	return listOptions{
		orderBy:  listOrderByDefault,
		sortType: listSortTypeDefault,
		pageSize: listPageSize,
	}
}

func recycleListOptions(page int) map[string]any {
	body := map[string]any{
		"parentId": "",
		"pageSize": listPageSize,
		"dirType":  4,
		"orderBy":  10,
		"sortType": 0,
	}
	if page > 0 {
		body["page"] = page
	}
	return body
}

func (o listOptions) listBody(parentID string, page int) map[string]any {
	body := map[string]any{
		"parentId": parentID,
		"page":     page,
		"pageSize": o.pageSize,
		"orderBy":  o.orderBy,
		"sortType": o.sortType,
	}
	if o.dirType != nil {
		body["dirType"] = *o.dirType
	}
	if len(o.fileTypes) > 0 {
		body["fileTypes"] = o.fileTypes
	}
	if o.resType != nil {
		body["resType"] = *o.resType
	}
	if o.needSubFolderStat {
		body["needSubFolderStat"] = true
	}
	if o.needPlayRecord {
		body["needPlayRecord"] = true
	}
	return body
}

func (d *Driver) fetchFileList(ctx context.Context, body map[string]any) (listData, error) {
	var data listData
	if err := d.apiRequest(ctx, pathFileList, body, &data); err != nil {
		return listData{}, err
	}
	return data, nil
}

func (d *Driver) listAllPages(ctx context.Context, pageSize int, fetch func(context.Context, int) (listData, error)) ([]domain.FileItem, error) {
	if pageSize <= 0 {
		pageSize = listPageSize
	}
	var items []domain.FileItem
	for page := 0; ; page++ {
		data, err := fetch(ctx, page)
		if err != nil {
			return nil, err
		}
		if len(data.List) == 0 {
			break
		}
		for _, entry := range data.List {
			items = append(items, entry.toFileItem())
		}
		if data.Total > 0 && len(items) >= data.Total {
			break
		}
		if len(data.List) < pageSize {
			break
		}
	}
	return items, nil
}

func (d *Driver) listByParent(ctx context.Context, parentID string, opts listOptions) ([]domain.FileItem, error) {
	parent := d.resolveParent(parentID)
	return d.listAllPages(ctx, opts.pageSize, func(ctx context.Context, page int) (listData, error) {
		return d.fetchFileList(ctx, opts.listBody(parent, page))
	})
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
	_ driver.OfflineDownloadProvider  = (*Driver)(nil)
	_ driver.OfflineURLDownloader     = (*Driver)(nil)
	_ driver.OfflineTaskRefresher     = (*Driver)(nil)
	_ driver.OfflineTaskDeleter       = (*Driver)(nil)
	_ driver.AuthRefresher            = (*Driver)(nil)
	_ driver.QRLoginProvider          = (*Driver)(nil)
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
)
