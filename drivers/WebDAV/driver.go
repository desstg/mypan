package webdav

import (
	"context"
	"strings"

	"github.com/studio-b12/gowebdav"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// Driver 把远端 WebDAV 服务器作为网盘账号挂载到 LitePan。
type Driver struct {
	add Addition

	client *gowebdav.Client
}

var config = driver.Config{
	Name:                   "webdav",
	DisplayName:            "WebDAV",
	Description:            "挂载远端 WebDAV 服务器，作为账号读取与写入",
	CardTags:               []string{"远端挂载", "目录读写", "本机代理"},
	SortOrder:              99,
	AuthLabel:              "用户名/密码",
	CardColor:              "#0ea5e9",
	CardLogo:               "/logos/webdav.png",
	DefaultRoot:            "/",
	AuthType:               driver.AuthNone,
	UploadConflictPolicies: []string{"overwrite", "skip", "rename"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) Init(ctx context.Context) error {
	addr := strings.TrimSpace(d.add.Address)
	if addr == "" {
		return domain.Errorf(domain.CodeValidation, "WebDAV 服务地址不能为空")
	}
	if strings.TrimSpace(d.add.Username) == "" {
		return domain.Errorf(domain.CodeValidation, "WebDAV 用户名不能为空")
	}
	if d.add.RootPath == "" {
		d.add.RootPath = "/"
	}

	c := gowebdav.NewAuthClient(addr, gowebdav.NewAutoAuth(d.add.Username, d.add.Password))
	c.SetTimeout(secondsOr(d.add.Timeout, defaultTimeout))
	c.SetHeader("User-Agent", httpx.DefaultUserAgent)
	if d.add.TLSSkip {
		c.SetTransport(buildTransport(d.add))
	}
	d.client = c
	_ = ctx
	return nil
}

func (d *Driver) Drop(context.Context) error {
	d.client = nil
	return nil
}

func (d *Driver) Ping(ctx context.Context) error {
	c, err := d.ensureClient()
	if err != nil {
		return err
	}
	_ = ctx
	if _, err := c.ReadDir(d.rootPath()); err != nil {
		return mapError(err)
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
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized"):
		return prefix + "：WebDAV 用户名或密码不正确，请核对凭据"
	case strings.Contains(lower, "403") || strings.Contains(lower, "forbidden"):
		return prefix + "：WebDAV 拒绝访问，请确认账号对该目录有权限"
	case strings.Contains(lower, "404") || strings.Contains(lower, "not found"):
		return prefix + "：WebDAV 根 URL 或子目录路径不存在，请检查填写内容"
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate"):
		return prefix + "：TLS 证书校验失败，可把「TLS 证书校验」切换为「不校验（自签名）」后重试"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "context deadline"):
		return prefix + "：连接 WebDAV 超时，请检查网络、根 URL，或适当调大请求超时"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "connection refused"):
		return prefix + "：无法连接 WebDAV 服务，请检查地址是否正确、服务是否在线"
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	c, err := d.ensureClient()
	if err != nil {
		return nil, err
	}
	dir := d.normalizePath(parentID)
	_ = ctx
	infos, err := c.ReadDir(dir)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]domain.FileItem, 0, len(infos))
	for _, fi := range infos {
		wdPath, ok := webdavPath(fi)
		if !ok {
			wdPath = d.childPath(dir, fi.Name())
		}
		wdPath = trimSlash(wdPath)
		items = append(items, fileToItem(wdPath, fi.Name(), fi.Size(), fi.IsDir(), fi.ModTime()))
	}
	return items, nil
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	c, err := d.ensureClient()
	if err != nil {
		return nil, err
	}
	path := d.normalizePath(fileID)
	_ = ctx
	if path == d.rootPath() {
		return &domain.FileItem{
			ID:     path,
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDPath,
		}, nil
	}
	fi, err := c.Stat(path)
	if err != nil {
		return nil, mapError(err)
	}
	wdPath, _ := webdavPath(fi)
	if wdPath == "" {
		wdPath = path
	}
	wdPath = trimSlash(wdPath)
	item := fileToItem(wdPath, fi.Name(), fi.Size(), fi.IsDir(), fi.ModTime())
	return &item, nil
}

// ensureClient 懒加载 gowebdav 客户端（Drop 后复用实例时重建）。
func (d *Driver) ensureClient() (*gowebdav.Client, error) {
	if d.client == nil {
		return nil, domain.Errorf(domain.CodeInternal, "WebDAV 客户端未初始化")
	}
	return d.client, nil
}

// webdavPath 从 os.FileInfo 中提取 gowebdav File 的完整路径（若可获取）。
func webdavPath(fi interface{ Name() string }) (string, bool) {
	type pather interface{ Path() string }
	if p, ok := fi.(pather); ok {
		if s := strings.TrimSpace(p.Path()); s != "" {
			return s, true
		}
	}
	return "", false
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
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
)
