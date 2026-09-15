package webdav

import (
	"strings"
	"time"

	"github.com/studio-b12/gowebdav"

	"litepan/internal/domain"
)

func fileToItem(path, name string, size int64, isDir bool, mod time.Time) domain.FileItem {
	return domain.FileItem{
		ID:      path,
		Name:    name,
		Size:    size,
		IsDir:   isDir,
		ModTime: mod,
		IDKind:  domain.IDPath,
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case gowebdav.IsErrCode(err, 401):
		return domain.Errorf(domain.CodeAuthExpired, "WebDAV 认证失败：%s", msg)
	case gowebdav.IsErrCode(err, 403):
		return domain.Errorf(domain.CodePermissionDenied, "WebDAV 权限不足：%s", msg)
	case gowebdav.IsErrNotFound(err):
		return domain.Errf(domain.CodeNotFound)
	case gowebdav.IsErrCode(err, 429):
		return domain.Errorf(domain.CodeRateLimited, "WebDAV 限流：%s", msg)
	default:
		return domain.Wrap(domain.CodeDriverError, err)
	}
}

// trimSlash 清理路径首尾空白与多余斜杠，保留单个前导斜杠。
func trimSlash(p string) string {
	s := strings.TrimSpace(p)
	s = strings.Trim(s, "/")
	if s == "" {
		return "/"
	}
	return "/" + s
}
