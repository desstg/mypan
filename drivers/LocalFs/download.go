package localfs

import (
	"context"
	"os"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	_ = ctx
	target, err := d.resolveEntry(req.FileID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, domain.Wrap(domain.CodeNotFound, err)
	}
	if info.IsDir() {
		return nil, domain.Errorf(domain.CodeValidation, "目录不支持下载")
	}
	return &domain.DownloadInfo{
		LocalPath:  target,
		Mode:       domain.DownloadProxy,
		ForceProxy: true,
		Expiration: time.Hour,
		Size:       info.Size(),
		FileName:   info.Name(),
	}, nil
}
