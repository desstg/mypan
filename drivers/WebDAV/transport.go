package webdav

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/studio-b12/gowebdav"
)

const defaultTimeout = 60 * time.Second

// buildTransport 构造 gowebdav 客户端使用的 HTTP 传输层。
func buildTransport(add Addition) http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.IdleConnTimeout = 90 * time.Second
	if add.TLSSkip {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return tr
}

// rootPath 返回归一化后的根目录路径。
func (d *Driver) rootPath() string {
	if p := strings.TrimSpace(d.add.RootPath); p != "" {
		return trimSlash(p)
	}
	return "/"
}


// 空串/"/"/"0"/"root" 视为根目录。
func (d *Driver) normalizePath(parentID string) string {
	p := strings.TrimSpace(parentID)
	if p == "" || p == "/" || p == "0" || strings.EqualFold(p, "root") {
		return d.rootPath()
	}
	return trimSlash(p)
}

// childPath 拼接父目录与子名，保证唯一前导斜杠。
func (d *Driver) childPath(parent, name string) string {
	base := strings.TrimSuffix(d.normalizePath(parent), "/")
	return base + "/" + strings.TrimSpace(name)
}

// resourceURL 拼接 WebDAV 服务地址与资源路径，得到完整 GET URL。
func (d *Driver) resourceURL(p string) string {
	base := strings.TrimRight(strings.TrimSpace(d.add.Address), "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return base + gowebdav.PathEscape(p)
}

// baseName 取路径最后一段作为文件名。
func baseName(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if p == "" || p == "/" {
		return "根目录"
	}
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// parentPath 取路径的父目录。
func parentPath(path string) string {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if p == "" || p == "/" {
		return "/"
	}
	if idx := strings.LastIndex(p, "/"); idx > 0 {
		return p[:idx]
	}
	return "/"
}
