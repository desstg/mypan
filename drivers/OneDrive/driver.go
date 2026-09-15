// Package onedrive 接入 OneDrive 个人版 Microsoft Graph API。
package onedrive

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

const (
	graphBaseURL            = "https://graph.microsoft.com/v1.0"
	defaultOperationDelayMS = 150
)

type Driver struct {
	driver.AuthRefreshControl
	add    Addition
	client *http.Client

	oauthBase    string
	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu        sync.RWMutex
	refreshMu sync.Mutex
	token     string
	refresh   string
}

var config = driver.Config{
	Name:                   "onedrive",
	DisplayName:            "OneDrive",
	Description:            "OneDrive 个人版 Microsoft Graph 官方 API 接入",
	CardTags:               []string{"官方授权", "OAuth", "支持302"},
	SortOrder:              10,
	AuthLabel:              "OAuth",
	CardColor:              "#2563EB",
	CardLogo:               "/logos/onedrive.png",
	DefaultRoot:            "/",
	AuthType:               driver.AuthToken,
	OAuthName:              "OneDrive",
	TokenLifetime:          time.Hour,
	RefreshAdvance:         10 * time.Minute,
	SupportsAccountProfile: true,
	UploadConflictPolicies: []string{"overwrite", "rename", "skip", "fail"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
	d.mu.Unlock()
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetOAuthServer(baseURL string) {
	d.oauthBase = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 90 * time.Second})
	}
	d.mu.Lock()
	if d.token == "" {
		d.token = strings.TrimSpace(d.add.AccessToken)
	}
	if d.refresh == "" {
		d.refresh = strings.TrimSpace(d.add.RefreshToken)
	}
	token, refresh := d.token, d.refresh
	d.mu.Unlock()
	if token == "" && refresh == "" {
		return domain.Errorf(domain.CodeValidation, "access_token 与 refresh_token 不能都为空")
	}
	if token == "" {
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
	return d.apiRequest(ctx, http.MethodGet, "/me/drive", nil, nil, nil)
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "不能都为空"):
		return prefix + "：请点击自动获取 Token 完成 Microsoft 授权"
	case strings.Contains(lower, "client_id"), strings.Contains(lower, "client_secret"):
		return prefix + "：OAuth 服务未配置 OneDrive Client ID 或 Client Secret"
	case strings.Contains(technical, "认证已过期"):
		return prefix + "：OneDrive 授权已失效，请重新获取 Token"
	default:
		return ""
	}
}

func (d *Driver) currentToken() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.token
}

func (d *Driver) currentRefreshToken() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.refresh
}

func (d *Driver) rootReference() string {
	root := strings.TrimSpace(d.add.RootFolderID)
	if root == "" || root == "0" || strings.EqualFold(root, "root") {
		return "/"
	}
	if strings.HasPrefix(root, "/") {
		trimmed := strings.Trim(root, "/")
		if trimmed == "" {
			return "/"
		}
		return "/" + trimmed
	}
	return root
}

func (d *Driver) normalizeParent(parentID string) string {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" || parentID == "0" || strings.EqualFold(parentID, "root") {
		return d.rootReference()
	}
	return parentID
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
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
	_ driver.OAuthConsumer            = (*Driver)(nil)
	_ driver.AuthRefresher            = (*Driver)(nil)
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.AccountProfileProvider   = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
)
