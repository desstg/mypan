package quark

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// Driver 是夸克网盘驱动实例；Cookie 失效需人工重新抓取。
type Driver struct {
	add    Addition
	client *http.Client

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu                sync.Mutex
	cookie            string
	lastCookieChanged bool
}

var config = driver.Config{
	Name:                   "quark",
	DisplayName:            "夸克网盘",
	Description:            "夸克网盘接入，支持Cookie认证和文件管理功能",
	CardTags:               []string{"扫码登录", "Cookie", "本机代理"},
	SortOrder:              6,
	AuthLabel:              "Cookie",
	CardColor:              "#2f7bff",
	CardLogo:               "/logos/quark.png",
	DefaultRoot:            "0",
	AuthType:               driver.AuthCookie,
	HealthCheckInterval:    70 * time.Minute,
	SupportsAccountProfile: true,
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
	if d.cookie == "" {
		d.cookie = strings.TrimSpace(d.add.Cookie)
	}
	empty := d.cookie == ""
	d.mu.Unlock()
	if empty {
		return domain.Errorf(domain.CodeValidation, "Cookie 不能为空")
	}
	return nil
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	return nil
}

// Ping 验证 Cookie 是否仍有效。
func (d *Driver) Ping(ctx context.Context) error {
	params := url.Values{}
	params.Set("pdir_fid", d.rootID())
	params.Set("_page", "1")
	params.Set("_size", "1")
	params.Set("_fetch_total", "1")
	_, err := d.apiRequest(ctx, http.MethodGet, pathList, params, nil, nil)
	return err
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "Cookie 不能为空"):
		return prefix + "：请填写夸克网盘 Cookie"
	case strings.Contains(technical, "认证失败") ||
		strings.Contains(technical, "权限不足") ||
		strings.Contains(lower, "auth_expired") ||
		strings.Contains(lower, "permission_denied"):
		return prefix + "：夸克 Cookie 无效或已过期，请重新登录夸克网页版抓取完整 Cookie"
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.normalizeParent(parentID)
	var items []domain.FileItem
	for page := 1; ; page++ {
		params := url.Values{}
		params.Set("pdir_fid", parent)
		params.Set("_page", strconv.Itoa(page))
		params.Set("_size", strconv.Itoa(listPageSize))
		params.Set("_fetch_total", "1")
		params.Set("fetch_all_file", "1")
		params.Set("fetch_risk_file_name", "1")

		var data listData
		if _, err := d.apiRequest(ctx, http.MethodGet, pathList, params, nil, &data); err != nil {
			return nil, err
		}
		if len(data.List) == 0 {
			break
		}
		for _, e := range data.List {
			items = append(items, e.toFileItem())
		}
		if len(data.List) < listPageSize {
			break
		}
	}
	return items, nil
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
)
