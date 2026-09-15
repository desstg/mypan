package webdav

import (
	"context"
	"fmt"
	"os"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
)

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	targetName := strings.TrimSpace(req.FileName)
	if targetName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(targetName); err != nil {
		return nil, err
	}
	localFile, err := uploadutil.StatLocalFile(req.LocalPath)
	if err != nil {
		return nil, err
	}
	localPath := localFile.Path
	fileSize := localFile.Size

	parentID := d.normalizePath(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)
	resolvedName, skipped, err := d.prepareUploadTarget(ctx, parentID, targetName, policy)
	if err != nil {
		return nil, err
	}
	if skipped {
		return &driver.LocalUploadResult{
			ParentID: parentID,
			FileName: targetName,
			Size:     fileSize,
			Message:  fmt.Sprintf("文件 '%s' 已存在，已跳过", targetName),
			Skipped:  true,
		}, nil
	}
	targetName = resolvedName
	targetPath := d.childPath(parentID, targetName)

	c, err := d.ensureClient()
	if err != nil {
		return nil, err
	}

	// overwrite 模式下 gowebdav WriteStream 直接覆盖；其余模式已由 prepareUploadTarget 解析出唯一名。
	src, err := os.Open(localPath)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	defer src.Close()

	reader := &uploadutil.ReadProgress{
		R:     src,
		Total: fileSize,
		OnProgress: func(uploaded int64) {
			uploadutil.NotifyProgress(req.OnProgress, uploaded, fileSize, "正在上传到 WebDAV")
		},
	}
	if err := c.WriteStream(targetPath, reader, 0o644); err != nil {
		return nil, mapError(err)
	}
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return &driver.LocalUploadResult{
		FileID:   targetPath,
		ParentID: parentID,
		FileName: targetName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 上传成功", targetName),
	}, nil
}


func (d *Driver) prepareUploadTarget(ctx context.Context, parentID, name, policy string) (string, bool, error) {
	if policy == "overwrite" {
		return name, false, nil
	}
	c, err := d.ensureClient()
	if err != nil {
		return "", false, err
	}
	_ = ctx
	infos, err := c.ReadDir(d.normalizePath(parentID))
	if err != nil {
		return "", false, mapError(err)
	}
	existing := make(map[string]struct{}, len(infos))
	hasSame := false
	for _, fi := range infos {
		existing[fi.Name()] = struct{}{}
		if fi.Name() == name {
			if fi.IsDir() {
				return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹 '%s'", name)
			}
			hasSame = true
		}
	}
	if !hasSame {
		return name, false, nil
	}
	switch policy {
	case "skip":
		return name, true, nil
	case "fail":
		return "", false, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件 '%s'", name)
	case "keep_both", "keep_both_new", "rename":
		return uploadutil.KeepBothName(name, existing), false, nil
	default:
		return name, false, nil
	}
}
