package dav

import (
	"context"
	"os"

	"golang.org/x/net/webdav"

	"litepan/internal/driver"
)

func (fs *FileSystem) openUpload(ctx context.Context, name string, flag int) (webdav.File, error) {
	exclusive := flag&os.O_EXCL != 0
	plan, err := fs.planUpload(ctx, name, exclusive)
	if err != nil {
		return nil, err
	}
	if plan.noop {
		return &noopUpload{}, nil
	}
	tmp, tmpPath, release, err := fs.createWebDAVTempFile(plan.fileName)
	if err != nil {
		return nil, err
	}
	return &uploadHandle{
		fs:        fs,
		ctx:       ctx,
		accountID: plan.accountID,
		parentID:  plan.parentID,
		fileName:  plan.fileName,
		tmpPath:   tmpPath,
		file:      tmp,
		release:   release,
	}, nil
}

func (fs *FileSystem) createWebDAVTempFile(fileName string) (*os.File, string, func(), error) {
	return createWebDAVTempFile(fs.dataDir, fileName, fs.tempRegistry)
}

func (u *uploadHandle) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	if u.file != nil {
		_ = u.file.Close()
	}
	if u.release != nil {
		defer u.release()
	}
	req := driver.LocalUploadRequest{
		LocalPath:      u.tmpPath,
		FileName:       u.fileName,
		ParentID:       u.parentID,
		ConflictPolicy: "overwrite",
	}
	if times, ok := uploadTimesFromContext(u.ctx); ok {
		req.ModTime = times.ModTime
		req.CreateTime = times.CreateTime
	}
	_, err := u.fs.files.UploadLocal(u.ctx, u.accountID, req)
	return err
}
