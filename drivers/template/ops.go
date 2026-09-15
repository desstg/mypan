package template

import (
	"context"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func notImplemented(feature string) error {
	return domain.Errorf(domain.CodeNotImplement, "template 驱动尚未实现：%s", feature)
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	_ = ctx
	_ = fileID
	return nil, notImplemented("GetFileInfo")
}

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	_ = ctx
	_ = req
	return nil, notImplemented("ResolveDownload")
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	_ = ctx
	_ = fileIDs
	return notImplemented("DeleteFiles")
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, sourceParentID string) error {
	_ = ctx
	_ = fileIDs
	_ = targetParentID
	_ = sourceParentID
	return notImplemented("MoveFiles")
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	_ = ctx
	_ = fileIDs
	_ = targetParentID
	return notImplemented("CopyFiles")
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	_ = ctx
	_ = fileID
	_ = newName
	return notImplemented("RenameFile")
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	_ = ctx
	_ = parentID
	_ = name
	return nil, notImplemented("CreateFolder")
}
