package cache

import (
	"strconv"
	"strings"

	"litepan/internal/domain"
)

// 统一缓存 Key 结构：<type>:<accountID>:<id...>。集中生成，避免各处散拼。
const sep = ":"

const (
	prefixDir         = "dir"  // 目录列表
	prefixFileInfo    = "file" // 单文件详情
	prefixDownloadURL = "dl"   // 下载直链
)

// CacheType 是元数据缓存键前缀类型。
type CacheType string

const (
	TypeDownloadURL CacheType = prefixDownloadURL
)

// 缓存值类型别名（键仍用 DirKey / FileInfoKey / DownloadURLKey 生成）。
type (
	DirList  = []domain.FileItem
	FileInfo = domain.FileItem
)

// DirKey 目录列表缓存键。parentID 应先经 NormalizeDirParentID 规范化。
func DirKey(accountID int64, parentID string) string {
	return prefixDir + sep + strconv.FormatInt(accountID, 10) + sep + NormalizeDirParentID(parentID)
}

// NormalizeDirParentID 把根目录多种占位（""/"0"/"/"/root）统一为 ""，保证同目录只对应一个缓存键。
func NormalizeDirParentID(parentID string) string {
	p := strings.TrimSpace(parentID)
	if p == "" || p == "0" || p == "/" || strings.EqualFold(p, "root") {
		return ""
	}
	return p
}

// InvalidateDirKeys 失效目录列表缓存，含根目录别名键 "0"。
func InvalidateDirKeys(c *Service, accountID int64, parentID string) {
	if c == nil {
		return
	}
	norm := NormalizeDirParentID(parentID)
	c.InvalidateKey(DirKey(accountID, norm))
	if norm == "" {
		c.InvalidateKey(prefixDir + sep + strconv.FormatInt(accountID, 10) + sep + "0")
	}
}

// InvalidateAccountDirKeys 失效指定账号的全部目录列表缓存，不影响文件详情与下载直链缓存。
func InvalidateAccountDirKeys(c *Service, accountID int64) {
	if c == nil || accountID <= 0 {
		return
	}
	c.InvalidatePrefix(prefixDir + sep + strconv.FormatInt(accountID, 10) + sep)
}

// FileInfoKey 单文件详情缓存键。
func FileInfoKey(accountID int64, fileID string) string {
	return prefixFileInfo + sep + strconv.FormatInt(accountID, 10) + sep + fileID
}

// DownloadURLKey 下载直链缓存键（含 UA，兼容 NDM/Emby 分 UA 链）。
func DownloadURLKey(accountID int64, fileID, ua string) string {
	return prefixDownloadURL + sep + strconv.FormatInt(accountID, 10) + sep + fileID + sep + ua
}

// DownloadURLPrefix 某文件全部 UA 变体的键前缀，写操作失效时用 InvalidatePrefix。
func DownloadURLPrefix(accountID int64, fileID string) string {
	return prefixDownloadURL + sep + strconv.FormatInt(accountID, 10) + sep + fileID + sep
}

// accountTypePrefixes 返回某账号在各缓存类型下的键前缀，用于按账号批量失效。
func accountTypePrefixes(accountID int64) []string {
	id := strconv.FormatInt(accountID, 10)
	return []string{
		prefixDir + sep + id + sep,
		prefixFileInfo + sep + id + sep,
		prefixDownloadURL + sep + id + sep,
		prefixPathMap + sep + id + sep,
		prefixWebDAVMeta + sep + id + sep,
	}
}
