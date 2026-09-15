// Package template 是新驱动脚手架：复制本目录为 drivers/<名>/，改包名与 Config.Name，按 README 实现后再于 all.go 空导入（勿注册本包）。
package template

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// Driver 为实例骨架，复制后按需增删字段。
type Driver struct {
	driver.AuthRefreshControl
	add    Addition
	client *http.Client

	oauthBase    string
	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu      sync.Mutex
	token   string
	refresh string
}

var config = driver.Config{
	Name:           "template",
	DisplayName:    "模板驱动（勿用于生产）",
	CardColor:      "#94a3b8",
	CardLogo:       "/logos/webdav.png",
	DefaultRoot:    "0",
	AuthType:       driver.AuthToken,
	OAuthName:      "模板驱动",
	TokenLifetime:  30 * 24 * time.Hour,
	RefreshAdvance: 10 * time.Hour,
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	d.mu.Lock()
	token := strings.TrimSpace(d.token)
	if token == "" {
		token = strings.TrimSpace(d.add.AccessToken)
	}
	refresh := strings.TrimSpace(d.refresh)
	if refresh == "" {
		refresh = strings.TrimSpace(d.add.RefreshToken)
	}
	d.token = token
	d.refresh = refresh
	d.mu.Unlock()
	if d.token == "" && d.refresh == "" {
		return domain.Errorf(domain.CodeValidation, "access_token 与 refresh_token 不能都为空")
	}
	_ = ctx
	return nil
}

func (d *Driver) Drop(context.Context) error { return nil }

func (d *Driver) Ping(ctx context.Context) error {
	return d.apiCall(ctx, http.MethodGet, pathList, nil, nil, nil)
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.normalizeParent(parentID)
	var resp listData
	params := url.Values{}
	params.Set("parent_id", parent)
	if err := d.apiCall(ctx, http.MethodGet, pathList, params, nil, &resp); err != nil {
		return nil, err
	}
	items := make([]domain.FileItem, 0, len(resp.Items))
	for _, e := range resp.Items {
		items = append(items, e.toDomain())
	}
	return items, nil
}

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetOAuthServer(baseURL string) { d.oauthBase = baseURL }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token
}

var (
	_ driver.Driver                  = (*Driver)(nil)
	_ driver.InfoGetter              = (*Driver)(nil)
	_ driver.Downloader              = (*Driver)(nil)
	_ driver.Deleter                 = (*Driver)(nil)
	_ driver.Mover                   = (*Driver)(nil)
	_ driver.Copier                  = (*Driver)(nil)
	_ driver.Renamer                 = (*Driver)(nil)
	_ driver.FolderCreator           = (*Driver)(nil)
	_ driver.OAuthConsumer           = (*Driver)(nil)
	_ driver.AuthCredentialConsumer  = (*Driver)(nil)
	_ driver.AuthPersistConsumer     = (*Driver)(nil)
	_ driver.AuthRefresher           = (*Driver)(nil)
	_ driver.RequestIntervalConsumer = (*Driver)(nil)
)
