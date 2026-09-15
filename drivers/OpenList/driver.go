// Package openlist 挂载远端 OpenList 服务，通过原生 /api/fs 接口读取目录与解析播放链接。
package openlist

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// Driver 是 OpenList 挂载驱动实例；令牌失效时可凭账号密码自动重新登录。
type Driver struct {
	driver.AuthRefreshControl
	add    Addition
	client *http.Client

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu    sync.Mutex
	token string
}

var config = driver.Config{
	Name:        "openlist",
	DisplayName: "OpenList",
	Description: "挂载远端 OpenList 服务，作为账号读取与播放",
	CardTags:    []string{"远端挂载", "目录读写", "支持302"},
	SortOrder:   88,
	AuthLabel:   "Token/账号密码",
	CardColor:   "#6366f1",
	CardLogo:    "/logos/openlist.png",
	DefaultRoot: "/",
	AuthType:    driver.AuthToken,
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) Init(ctx context.Context) error {
	if strings.TrimSpace(d.add.Address) == "" {
		return domain.Errorf(domain.CodeValidation, "OpenList 服务地址不能为空")
	}
	d.ensureClient()
	d.mu.Lock()
	if d.token == "" {
		d.token = strings.TrimSpace(d.add.AccessToken)
	}
	hasToken := d.token != ""
	d.mu.Unlock()
	if !hasToken {
		if strings.TrimSpace(d.add.Username) == "" {
			return domain.Errorf(domain.CodeValidation, "请填写访问令牌，或提供用户名/密码")
		}
		if err := d.login(ctx); err != nil {
			return err
		}
	}
	return d.verifyMe(ctx)
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	d.client = nil
	return nil
}

// Ping 拉取 /me 验证令牌有效性。
func (d *Driver) Ping(ctx context.Context) error { return d.verifyMe(ctx) }

func (d *Driver) verifyMe(ctx context.Context) error {
	var me meResp
	if err := d.apiRequest(ctx, http.MethodGet, "/me", nil, &me, nil); err != nil {
		return err
	}
	if me.Role != guestRole {
		return nil
	}
	var st publicSettingsResp
	if err := d.apiRequest(ctx, http.MethodGet, "/public/settings", nil, &st, nil); err != nil {
		return domain.Errorf(domain.CodePermissionDenied, "OpenList 游客模式校验失败：%s", err.Error())
	}
	if !strings.EqualFold(strings.TrimSpace(st.AllowMounted), "true") {
		return domain.Errorf(domain.CodePermissionDenied, "对方 OpenList 站点未开启「允许挂载」，请到站点设置中开启后再添加")
	}
	return nil
}

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t := strings.TrimSpace(creds.AccessToken); t != "" {
		d.token = t
	}
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) currentToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token
}

func (d *Driver) ensureClient() {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{})
	}
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func (d *Driver) login(ctx context.Context) error {
	_, err := d.RefreshToken(ctx, d.currentToken, func(ctx context.Context) (string, error) {
		err := d.loginOnce(ctx)
		return d.currentToken(), err
	}, driver.ClassifyOAuthRefreshError)
	return err
}

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	var err error
	if strings.TrimSpace(d.add.Username) == "" {
		err = d.verifyMe(ctx)
	} else {
		err = d.login(ctx)
	}
	if err != nil {
		return driver.ClassifyOAuthRefreshError(err), err
	}
	return driver.RefreshSuccess, nil
}

func (d *Driver) loginOnce(ctx context.Context) error {
	username := strings.TrimSpace(d.add.Username)
	if username == "" {
		return domain.Errorf(domain.CodeValidation, "未配置用户名，无法自动登录")
	}
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	var out loginResp
	if err := d.rawRequest(ctx, http.MethodPost, "/auth/login", loginReq{
		Username: username,
		Password: d.add.Password,
	}, &out, nil); err != nil {
		if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
			return domain.Errorf(domain.CodeAuthExpired, "OpenList 用户名或密码不正确")
		}
		return err
	}
	token := strings.TrimSpace(out.Token)
	if token == "" {
		return domain.Errorf(domain.CodeDriverError, "OpenList 登录成功但未返回访问令牌")
	}
	d.mu.Lock()
	d.token = token
	d.mu.Unlock()
	if d.persist != nil {
		return d.persist(ctx, domain.AuthCredentials{AccessToken: token})
	}
	return nil
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "context deadline"):
		return prefix + "：无法连接 OpenList 服务，请检查地址是否正确、服务是否在线"
	case strings.Contains(lower, "用户名或密码"):
		return prefix + "：OpenList 用户名或密码不正确"
	case strings.Contains(lower, "认证") || strings.Contains(lower, "令牌") ||
		strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized"):
		return prefix + "：访问令牌失效或没有权限，请更新令牌"
	case strings.Contains(lower, "允许挂载"):
		return prefix + "：对方 OpenList 站点未开启「允许挂载」"
	case strings.Contains(lower, "404") || strings.Contains(lower, "not found"):
		return prefix + "：OpenList 接口路径不存在，请确认服务版本支持挂载"
	default:
		return ""
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
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
)
