//go:build fuse

package fuse

import (
	"litepan/internal/domain"
)

type backend struct {
	deps  Deps
	mount *domain.FuseMount
}

func (b *backend) accountID() int64 {
	if b == nil || b.mount == nil {
		return 0
	}
	return b.mount.AccountID
}
