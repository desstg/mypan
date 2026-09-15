package dav

import (
	"context"
	"os"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/file"
)

// Node 是 WebDAV 路径解析后的资源节点。
type Node struct {
	IsRoot    bool
	IsAccount bool
	Account   *domain.Account
	Item      domain.FileItem
	ParentID  string
}

type Resolver struct {
	files    *file.Service
	accounts domain.AccountRepository
	wc       *webdavCache
}

func NewResolver(files *file.Service, accounts domain.AccountRepository, wc *webdavCache) *Resolver {
	return &Resolver{
		files:    files,
		accounts: accounts,
		wc:       wc,
	}
}

func (r *Resolver) Resolve(ctx context.Context, webPath string) (*Node, error) {
	parsed := ParseWebDAVPath(webPath)
	if parsed.AccountName == "" {
		return &Node{IsRoot: true, Item: domain.FileItem{Name: "", IsDir: true, ModTime: time.Now()}}, nil
	}
	acc, err := r.accountByName(ctx, parsed.AccountName)
	if err != nil {
		return nil, err
	}
	if len(parsed.RelParts) == 0 {
		return &Node{
			IsAccount: true,
			Account:   acc,
			Item: domain.FileItem{
				ID:      "0",
				Name:    acc.Name,
				IsDir:   true,
				ModTime: acc.CreatedAt,
			},
			ParentID: "0",
		}, nil
	}
	item, parentID, err := r.resolveUnderAccountCached(ctx, acc.ID, parsed.RelParts, true)
	if err != nil {
		return nil, err
	}
	return &Node{Account: acc, Item: *item, ParentID: parentID}, nil
}

func (r *Resolver) accountByName(ctx context.Context, name string) (*domain.Account, error) {
	if r.accounts == nil {
		return nil, os.ErrNotExist
	}
	list, err := r.accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, acc := range list {
		if !acc.IsActive {
			continue
		}
		if strings.ToLower(acc.Name) == want {
			return acc, nil
		}
	}
	return nil, os.ErrNotExist
}

func (r *Resolver) ListChildren(ctx context.Context, node *Node) ([]domain.FileItem, error) {
	switch {
	case node.IsRoot:
		if r.accounts == nil {
			return nil, nil
		}
		list, err := r.accounts.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]domain.FileItem, 0, len(list))
		for _, acc := range list {
			if !acc.IsActive {
				continue
			}
			out = append(out, domain.FileItem{
				ID:      acc.Name,
				Name:    acc.Name,
				IsDir:   true,
				ModTime: acc.CreatedAt,
			})
		}
		return out, nil
	case node.IsAccount, node.Item.IsDir:
		parentID := "0"
		if !node.IsAccount {
			parentID = node.Item.ID
		}
		return r.files.List(ctx, node.Account.ID, parentID, false)
	default:
		return nil, os.ErrInvalid
	}
}

func (r *Resolver) resolveUnderAccount(ctx context.Context, accountID int64, parts []string) (*domain.FileItem, string, error) {
	return r.resolveUnderAccountCached(ctx, accountID, parts, true)
}
