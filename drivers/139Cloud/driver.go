// Package cloud139 接入移动云盘新版个人云 API。
package cloud139

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
	add    Addition
	client *http.Client

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu            sync.RWMutex
	refreshMu     sync.Mutex
	authorization string
	account       string
	personalHost  string
}

var config = driver.Config{
	Name:                   "139_cloud",
	DisplayName:            "移动云盘",
	Description:            "移动云盘新版个人云接口，使用网页 Authorization 令牌",
	CardTags:               []string{"扫码登录", "Authorization", "支持302"},
	SortOrder:              9,
	AuthLabel:              "Authorization",
	CardColor:              "#3B82F6",
	CardLogo:               "/logos/yidong.png",
	DefaultRoot:            "/",
	AuthType:               driver.AuthToken,
	TokenLifetime:          30 * 24 * time.Hour,
	RefreshAdvance:         10 * time.Hour,
	UploadConflictPolicies: []string{"overwrite", "rename", "skip", "fail"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.authorization = normalizeAuthorization(creds.AccessToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 60 * time.Second})
	}
	authorization := d.currentAuthorization()
	if authorization == "" {
		authorization = normalizeAuthorization(d.add.AccessToken)
	}
	info, err := parseAuthorization(authorization)
	if err != nil {
		return err
	}
	d.setAuthorization(info)
	if time.Until(info.ExpiresAt) < config.RefreshAdvance {
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
	_, err := d.ListFiles(ctx, d.rootID())
	return err
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.normalizeParent(parentID)
	items := make([]domain.FileItem, 0)
	cursor := ""
	for {
		var data listData
		if err := d.apiRequest(ctx, pathFileList, map[string]any{
			"imageThumbnailStyleList": []string{"Small", "Large"},
			"orderBy":                 "updated_at",
			"orderDirection":          "DESC",
			"pageInfo": map[string]any{
				"pageCursor": cursor,
				"pageSize":   listPageSize,
			},
			"parentFileId": parent,
		}, &data); err != nil {
			return nil, err
		}
		for _, entry := range data.Items {
			item := entry.toFileItem()
			if item.ID != "" && item.Name != "" {
				items = append(items, item)
			}
		}
		cursor = strings.TrimSpace(data.NextPageCursor)
		if cursor == "" {
			break
		}
	}
	return items, nil
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "Authorization 令牌不能为空"), strings.Contains(technical, "Authorization 令牌格式"):
		return prefix + "：请在移动云盘网页请求头中复制完整 Authorization 值"
	case strings.Contains(lower, "auth_expired"), strings.Contains(technical, "认证已过期"):
		return prefix + "：移动云盘 Authorization 已失效，请重新从网页抓取"
	case strings.Contains(technical, "移动云盘 API"), strings.Contains(technical, "路由策略"):
		return prefix + "：" + technical
	default:
		return ""
	}
}

func (d *Driver) currentAuthorization() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.authorization
}

func (d *Driver) currentAccount() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.account
}

func (d *Driver) setAuthorization(info authorizationInfo) {
	d.mu.Lock()
	d.authorization = info.Authorization
	d.account = info.Account
	d.mu.Unlock()
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
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.QRLoginProvider          = (*Driver)(nil)
)
