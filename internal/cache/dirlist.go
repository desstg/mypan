package cache

import (
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

func UpsertDirListItem(list DirList, item domain.FileItem) DirList {
	out := make(DirList, 0, len(list)+1)
	for _, cur := range list {
		if cur.ID == item.ID || (!item.IsDir && !cur.IsDir && strings.EqualFold(cur.Name, item.Name)) {
			continue
		}
		out = append(out, cur)
	}
	out = append(out, item)
	sortDirList(out)
	return out
}

func sortDirList(list DirList) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

func tryUpsertDirOnCreate(c *Service, e eventbus.FileMutated) bool {
	if c == nil || e.FileID == "" || e.FileName == "" {
		return false
	}
	key := DirKey(e.AccountID, e.ParentID)
	raw, ok := c.Get(key)
	if !ok && NormalizeDirParentID(e.ParentID) == "" {
		key = DirKey(e.AccountID, "0")
		raw, ok = c.Get(key)
	}
	if !ok {
		return false
	}
	list, ok := raw.(DirList)
	if !ok {
		return false
	}
	item := domain.FileItem{
		ID:     e.FileID,
		Name:   e.FileName,
		Size:   e.FileSize,
		IsDir:  e.IsDir,
		IDKind: domain.IDStable,
	}
	if !e.ModTime.IsZero() {
		item.ModTime = e.ModTime
	} else {
		item.ModTime = time.Now()
	}
	updated := UpsertDirListItem(list, item)
	ttl, ok := c.RemainingTTL(key)
	if !ok {
		return false
	}
	c.Set(key, updated, ttl)
	if NormalizeDirParentID(e.ParentID) == "" {
		for _, aliasKey := range []string{DirKey(e.AccountID, ""), DirKey(e.AccountID, "0")} {
			if aliasKey == key {
				continue
			}
			if ttl2, ok2 := c.RemainingTTL(aliasKey); ok2 {
				c.Set(aliasKey, updated, ttl2)
			}
		}
	}
	return true
}
