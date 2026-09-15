package cache

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"

	"litepan/internal/domain"
)

const (
	prefixPathMap    = "pathmap"
	prefixWebDAVMeta = "webdavmeta"
)

// PathMapEntry 缓存 WebDAV 路径到网盘文件的映射。
type PathMapEntry struct {
	Item     domain.FileItem
	ParentID string
}

func normalizeWebDAVCachePath(webPath string) string {
	p := strings.TrimSpace(webPath)
	p = strings.Trim(p, "/")
	if p == "" {
		return "/"
	}
	return "/" + p
}

func webDAVPathHash(webPath string) string {
	sum := md5.Sum([]byte(normalizeWebDAVCachePath(webPath)))
	return hex.EncodeToString(sum[:])[:16]
}

// PathMapKey WebDAV 路径映射缓存键（账号内相对路径，如 /movies/a.mkv）。
func PathMapKey(accountID int64, webPath string) string {
	return prefixPathMap + sep + strconv.FormatInt(accountID, 10) + sep + webDAVPathHash(webPath)
}

// WebDAVMetaKey PROPFIND 等 WebDAV 元数据响应缓存键。
func WebDAVMetaKey(accountID int64, cacheKey string) string {
	sum := md5.Sum([]byte(cacheKey))
	return prefixWebDAVMeta + sep + strconv.FormatInt(accountID, 10) + sep + hex.EncodeToString(sum[:])[:16]
}

// PathMapPrefix 某账号全部路径映射键前缀。
func PathMapPrefix(accountID int64) string {
	return prefixPathMap + sep + strconv.FormatInt(accountID, 10) + sep
}

// WebDAVMetaPrefix 某账号全部 WebDAV 元数据键前缀。
func WebDAVMetaPrefix(accountID int64) string {
	return prefixWebDAVMeta + sep + strconv.FormatInt(accountID, 10) + sep
}

// InvalidateAllWebDAVCaches 关闭或重置 WebDAV 缓存设置时清理全部 WebDAV 相关键。
func InvalidateAllWebDAVCaches(c *Service) {
	if c == nil {
		return
	}
	c.InvalidatePrefix(prefixPathMap + sep)
	c.InvalidatePrefix(prefixWebDAVMeta + sep)
}

// InvalidateWebDAVAccountCaches 写操作后清理 WebDAV 路径映射与 PROPFIND 元数据缓存。
func InvalidateWebDAVAccountCaches(c *Service, accountID int64) {
	if c == nil {
		return
	}
	c.InvalidatePrefix(PathMapPrefix(accountID))
	c.InvalidatePrefix(WebDAVMetaPrefix(accountID))
}
