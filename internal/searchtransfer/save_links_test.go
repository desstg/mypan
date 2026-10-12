package searchtransfer

import (
	"context"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
)

// 一个最小驱动：只实现 Driver 的必备方法，外加「网络离线支持 http/https」。
// 目的是让 SaveLinks 的**分流**真正跑起来（而不是在参数校验那一步就返回）。
type shareTestDriver struct{}

func (d *shareTestDriver) Config() driver.Config      { return driver.Config{Name: "share-test"} }
func (d *shareTestDriver) GetAddition() any           { return &struct{}{} }
func (d *shareTestDriver) Init(context.Context) error { return nil }
func (d *shareTestDriver) Drop(context.Context) error { return nil }
func (d *shareTestDriver) Ping(context.Context) error { return nil }
func (d *shareTestDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}

func (d *shareTestDriver) OfflineDownloadCapabilities() driver.OfflineDownloadCapabilities {
	// 故意：**只支持 http/https**，这样磁力会落到「网盘与内置下载器都不支持」，
	// 用来验证离线批整体被拒时分享链那一半不受影响。
	return driver.OfflineDownloadCapabilities{SupportsURLs: true, URLSchemes: []string{"http", "https"}}
}

func (d *shareTestDriver) AddOfflineURLs(context.Context, driver.OfflineURLRequest) ([]driver.OfflineAddResult, error) {
	return nil, domain.Errorf(domain.CodeNotImplement, "测试驱动不支持离线下载")
}

type shareTestProvider struct{ drv driver.Driver }

func (p shareTestProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type shareAccountRepo struct{ account *domain.Account }

func (r shareAccountRepo) Get(context.Context, int64) (*domain.Account, error) {
	return r.account, nil
}

// shareConfigRepo 是给 settings.New 用的空配置仓储（测试里不需要真配置）。
type shareConfigRepo struct{}

func (shareConfigRepo) Get(context.Context, string) (string, bool, error) { return "", false, nil }
func (shareConfigRepo) Set(context.Context, string, string) error         { return nil }
func (shareConfigRepo) All(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func shareTestSettings(t *testing.T) *settings.Service {
	t.Helper()
	svc, err := settings.New(context.Background(), shareConfigRepo{})
	if err != nil {
		t.Fatalf("创建测试设置失败: %v", err)
	}
	return svc
}

func (r shareAccountRepo) List(context.Context) ([]*domain.Account, error) { return nil, nil }
func (r shareAccountRepo) Create(context.Context, *domain.Account) (int64, error) {
	return 0, nil
}
func (r shareAccountRepo) Update(context.Context, *domain.Account) error { return nil }
func (r shareAccountRepo) Delete(context.Context, int64) error           { return nil }
func (r shareAccountRepo) SetDefault(context.Context, int64) error       { return nil }
func (r shareAccountRepo) NameTaken(context.Context, string, int64) (bool, error) {
	return false, nil
}

// 离线链整批被拒时，**分享链那一半仍要照常提交**。
//
// 钉的是 2026-10-11 真机踩到的那个 bug：离线批失败后直接 `return res, nil`，
// 于是「一条被 123 拒掉的磁力 + 一条能用的 123 分享链」这种混合提交里，
// 分享链连结果项都不出现，用户以为它没被处理。
//
// 这里用「账号能力上不支持磁力」来触发离线批失败（不发任何网络请求），
// 关键是两条都要出现在清单里。
func TestOfflineBatchFailureDoesNotSwallowShareLinks(t *testing.T) {
	settingsSvc := shareTestSettings(t)
	offline := offlinedownload.New(offlinedownload.Options{
		Exec:     driverexec.New(shareTestProvider{drv: &shareTestDriver{}}, nil),
		Accounts: shareAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "share-test"}},
		Settings: settingsSvc,
	})
	svc := New(Options{
		Offline:  offline,
		Exec:     driverexec.New(shareTestProvider{drv: &shareTestDriver{}}, nil),
		Settings: settingsSvc,
		Accounts: shareAccountRepo{account: &domain.Account{ID: 7, Name: "测试盘", DriverType: "share-test"}},
	})
	if !svc.Ready() {
		t.Fatal("测试服务应当 Ready")
	}

	res, err := svc.SaveLinks(context.Background(), []SaveLinkRequest{
		{AccountID: 7, URL: "magnet:?xt=urn:btih:abc"},
		{AccountID: 7, URL: "https://123865.com/s/Oqtgvd-pYn3h?pwd=YSRG"},
	}, "manual")
	if err != nil {
		t.Fatalf("不该整批返回 error: %v", err)
	}
	// 磁力那条走「能力判定」被拒（离线批为空 → 不进 AddURLs）；分享链那条
	// 必须仍然出现在清单里。早先的实现会把 0 条返回给用户。
	var shareSeen bool
	for _, item := range res.Items {
		if item.Kind == kindShare123 {
			shareSeen = true
		}
	}
	if !shareSeen {
		t.Fatalf("分享链必须在清单里出现，实际清单：%+v", res.Items)
	}
}
