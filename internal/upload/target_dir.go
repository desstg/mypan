package upload

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
)

const uploadTargetCacheTTL = 30 * time.Second

type uploadTargetFiles interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	CreateFolder(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error)
}

type uploadTargetCacheKey struct {
	accountID int64
	rootID    string
	relDir    string
}

type uploadTargetCacheEntry struct {
	folderID  string
	expiresAt time.Time
}

type uploadTargetDirCache struct {
	mu      sync.Mutex
	entries map[uploadTargetCacheKey]uploadTargetCacheEntry
}

func newUploadTargetDirCache() *uploadTargetDirCache {
	return &uploadTargetDirCache{entries: make(map[uploadTargetCacheKey]uploadTargetCacheEntry)}
}

func (c *uploadTargetDirCache) get(accountID int64, rootID, relDir string, now time.Time) (string, bool) {
	if c == nil || relDir == "" {
		return "", false
	}
	key := uploadTargetCacheKey{accountID: accountID, rootID: rootID, relDir: relDir}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if now.After(entry.expiresAt) {
		delete(c.entries, key)
		return "", false
	}
	return entry.folderID, true
}

func (c *uploadTargetDirCache) put(accountID int64, rootID, relDir, folderID string, now time.Time) {
	if c == nil || relDir == "" || folderID == "" {
		return
	}
	key := uploadTargetCacheKey{accountID: accountID, rootID: rootID, relDir: relDir}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[uploadTargetCacheKey]uploadTargetCacheEntry)
	}
	c.entries[key] = uploadTargetCacheEntry{
		folderID:  folderID,
		expiresAt: now.Add(uploadTargetCacheTTL),
	}
	c.mu.Unlock()
}

func ensureUploadTargetDir(ctx context.Context, files uploadTargetFiles, cache *uploadTargetDirCache, accountID int64, rootID, relDir string) (string, error) {
	if files == nil {
		return "", domain.Errorf(domain.CodeInternal, "文件服务未就绪")
	}
	relDir = strings.Trim(relDir, "/")
	if relDir == "" {
		return rootID, nil
	}
	now := time.Now()
	cur := rootID
	parts := strings.Split(relDir, "/")
	prefixParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		prefixParts = append(prefixParts, part)
		prefix := strings.Join(prefixParts, "/")
		if cachedID, ok := cache.get(accountID, rootID, prefix, now); ok {
			cur = cachedID
			continue
		}
		items, err := files.List(ctx, accountID, cur, false)
		if err != nil {
			return "", err
		}
		next := ""
		for _, item := range items {
			if item.IsDir && item.Name == part {
				next = item.ID
				break
			}
		}
		if next == "" {
			created, err := files.CreateFolder(ctx, accountID, cur, part)
			if err != nil {
				return "", err
			}
			next = created.ID
		}
		cur = next
		cache.put(accountID, rootID, prefix, cur, now)
	}
	return cur, nil
}

func joinUploadDisplayPath(base, relDir string) string {
	base = "/" + strings.Trim(strings.TrimSpace(base), "/")
	if base == "/" {
		base = ""
	}
	relDir = strings.Trim(strings.TrimSpace(relDir), "/")
	if relDir == "" {
		if base == "" {
			return "/"
		}
		return base
	}
	joined := path.Join(base, relDir)
	if joined == "." || joined == "" {
		return "/"
	}
	if !strings.HasPrefix(joined, "/") {
		return "/" + joined
	}
	return joined
}
