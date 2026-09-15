package dav

import (
	"context"
	"os"
	"strings"

	"litepan/internal/cache"
	"litepan/internal/domain"
)

func (r *Resolver) rememberFile(ctx context.Context, accountID int64, relParts []string, parentID string, item domain.FileItem) {
	wc := r.wc
	if wc == nil || !wc.enabled() || len(relParts) == 0 {
		return
	}
	wc.setPath(ctx, accountID, webPathFromParts(relParts), cache.PathMapEntry{
		Item:     item,
		ParentID: parentID,
	})
}

func webPathFromParts(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func (r *Resolver) resolveUnderAccountCached(ctx context.Context, accountID int64, parts []string, allowRetry bool) (*domain.FileItem, string, error) {
	wc := r.wc
	parentID := "0"
	start := 0

	if wc != nil && wc.enabled() {
		if ent, ok := wc.getPath(ctx, accountID, webPathFromParts(parts)); ok {
			return &ent.Item, ent.ParentID, nil
		}
		for end := len(parts); end > 0; end-- {
			prefix := webPathFromParts(parts[:end])
			ent, ok := wc.getPath(ctx, accountID, prefix)
			if !ok {
				continue
			}
			if end == len(parts) {
				return &ent.Item, ent.ParentID, nil
			}
			if !ent.Item.IsDir {
				break
			}
			parentID = ent.Item.ID
			start = end
			break
		}
	}

	item, parent, err := r.resolveUnderAccountFrom(ctx, accountID, parts, start, parentID, wc)
	if err != nil && allowRetry && wc != nil && wc.enabled() {
		wc.clearAccount(accountID)
		return r.resolveUnderAccountCached(ctx, accountID, parts, false)
	}
	return item, parent, err
}

func (r *Resolver) resolveUnderAccountFrom(ctx context.Context, accountID int64, parts []string, start int, parentID string, wc *webdavCache) (*domain.FileItem, string, error) {
	curParentID := parentID
	for i := start; i < len(parts); i++ {
		part := parts[i]
		items, err := r.files.List(ctx, accountID, curParentID, false)
		if err != nil {
			return nil, "", err
		}
		var found *domain.FileItem
		for j := range items {
			if items[j].Name == part {
				found = &items[j]
				break
			}
		}
		if found == nil {
			return nil, "", os.ErrNotExist
		}
		if i == len(parts)-1 {
			if wc != nil && wc.enabled() {
				path := webPathFromParts(parts[:i+1])
				wc.setPath(ctx, accountID, path, cache.PathMapEntry{Item: *found, ParentID: curParentID})
			}
			return found, curParentID, nil
		}
		if !found.IsDir {
			return nil, "", os.ErrInvalid
		}
		if wc != nil && wc.enabled() {
			path := webPathFromParts(parts[:i+1])
			wc.setPath(ctx, accountID, path, cache.PathMapEntry{Item: *found, ParentID: curParentID})
		}
		curParentID = found.ID
	}
	return nil, "", os.ErrNotExist
}
