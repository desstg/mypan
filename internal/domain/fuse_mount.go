package domain

import (
	"context"
	"time"
)

const (
	FuseStateUnmounted = "unmounted"
	FuseStateMounting  = "mounting"
	FuseStateMounted   = "mounted"
	FuseStateError     = "error"
)

type FuseMount struct {
	ID         int64
	Name       string
	AccountID  int64
	RootItemID string
	RootPath   string
	MountPoint string
	ReadOnly   bool
	AutoMount  bool
	UID        uint32
	GID        uint32
	DirMode    uint32
	FileMode   uint32
	Enabled    bool
	State      string
	LastError  string
	SortOrder  int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type FuseMountRepository interface {
	Create(ctx context.Context, m *FuseMount) (int64, error)
	Update(ctx context.Context, m *FuseMount) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*FuseMount, error)
	List(ctx context.Context) ([]*FuseMount, error)
	UpdateRuntime(ctx context.Context, id int64, state, lastError string) error
	MountPointTaken(ctx context.Context, mountPoint string, excludeID int64) (bool, error)
}
