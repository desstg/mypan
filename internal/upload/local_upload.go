package upload

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (m *Manager) runLocalUpload(ctx context.Context, accountID int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	if m.files != nil {
		return m.files.UploadLocal(ctx, accountID, req)
	}
	if err := m.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	var result *driver.LocalUploadResult
	err := m.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		uploader, ok := drv.(driver.LocalUploader)
		if !ok {
			return domain.Errorf(domain.CodeNotImplement, "当前驱动暂不支持后台上传任务")
		}
		r, uerr := uploader.UploadLocalFile(ctx, req)
		if uerr != nil {
			return uerr
		}
		result = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
