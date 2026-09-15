package localfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/pkg/jsonvalue"
)

// Addition 是本地文件系统驱动的配置。
type Addition struct {
	RootPath string                   `json:"root_path" form:"required,full" type:"local_dir" label:"本地根目录"`
	CacheTTL jsonvalue.FlexibleString `json:"cache_ttl" label:"缓存时间（分钟）" type:"number" default:"0" form:"full"`
}

// Driver 把本地目录作为网盘驱动（路径型 ID）。
type Driver struct {
	add      Addition
	rootReal string
}

var config = driver.Config{
	Name:        "localfs",
	DisplayName: "本机存储",
	Description: "适合将 STRM 目录通过 WebDAV 暴露给爆米花等播放器",
	CardTags:    []string{"本地目录", "容器挂载"},
	SortOrder:   100,
	AuthLabel:   "本地",
	CardColor:   "#64748b",
	CardLogo:    "/logos/local.png",
	DefaultRoot: "/",
	AuthType:    driver.AuthNone,
}

func New() driver.Driver { return &Driver{} }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) Init(ctx context.Context) error {
	_ = ctx
	raw := strings.TrimSpace(d.add.RootPath)
	if raw == "" {
		raw = "/"
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return domain.Wrap(domain.CodeValidation, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return domain.Wrap(domain.CodeValidation, err)
	}
	if !info.IsDir() {
		return domain.Errorf(domain.CodeValidation, "本地路径不是目录")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return domain.Wrap(domain.CodeValidation, err)
	}
	d.add.RootPath = abs
	d.rootReal = filepath.Clean(real)
	return nil
}

func (d *Driver) Drop(ctx context.Context) error { return nil }

func (d *Driver) Ping(ctx context.Context) error {
	_, err := os.Stat(d.root())
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
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
	case strings.Contains(lower, "no such file") || strings.Contains(technical, "路径不存在"):
		return prefix + "：本地路径不存在，请确认目录已挂载且路径正确"
	case strings.Contains(lower, "not a directory") || strings.Contains(technical, "不是目录"):
		return prefix + "：请填写目录路径，而不是文件路径"
	case strings.Contains(lower, "permission") || strings.Contains(technical, "不可读"):
		return prefix + "：本地路径不可读，请检查读取权限"
	default:
		return ""
	}
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	_ = ctx
	dir, err := d.resolveDir(parentID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	items := make([]domain.FileItem, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, domain.FileItem{
			ID:      filepath.Join(dir, e.Name()),
			Name:    e.Name(),
			Size:    info.Size(),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
			IDKind:  domain.IDPath,
		})
	}
	return items, nil
}

func (d *Driver) GetFileInfo(_ context.Context, fileID string) (*domain.FileItem, error) {
	target, err := d.resolveEntry(fileID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, domain.Wrap(domain.CodeNotFound, err)
	}
	return &domain.FileItem{
		ID:      target,
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
		IDKind:  domain.IDPath,
	}, nil
}

func (d *Driver) root() string {
	if d.add.RootPath == "" {
		return "/"
	}
	return d.add.RootPath
}

func init() { driver.Register(New) }

var (
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.InfoGetter               = (*Driver)(nil)
	_ driver.Downloader               = (*Driver)(nil)
	_ driver.FolderCreator            = (*Driver)(nil)
	_ driver.Deleter                  = (*Driver)(nil)
	_ driver.Mover                    = (*Driver)(nil)
	_ driver.Copier                   = (*Driver)(nil)
	_ driver.Renamer                  = (*Driver)(nil)
	_ driver.LocalUploader            = (*Driver)(nil)
)
