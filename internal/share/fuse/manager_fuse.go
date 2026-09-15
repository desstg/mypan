//go:build fuse

package fuse

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"litepan/internal/domain"
)

const (
	defaultEntryTimeout = 30 * time.Second
	defaultAttrTimeout  = 3 * time.Second
	defaultMaxIOSize    = 1024 * 1024
)

type liveManager struct {
	deps Deps
	mu   sync.Mutex
	runs map[int64]*runningMount
}

type runningMount struct {
	server *fuse.Server
}

func NewManager(deps Deps) Manager {
	return &liveManager{
		deps: deps,
		runs: make(map[int64]*runningMount),
	}
}

func (m *liveManager) Mount(ctx context.Context, mount *domain.FuseMount) error {
	if mount == nil {
		return domain.Errorf(domain.CodeValidation, "挂载配置无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[mount.ID]; ok {
		return nil
	}
	b := &backend{
		deps:  m.deps,
		mount: mount,
	}
	root := &cloudDir{
		b:    b,
		item: domain.FileItem{ID: mount.RootItemID, Name: "", IsDir: true},
	}
	opts := &fs.Options{
		UID: mount.UID,
		GID: mount.GID,
		MountOptions: fuse.MountOptions{
			Name:              fmt.Sprintf("litepan-%d", mount.ID),
			FsName:            "litepan:" + mount.Name,
			MaxWrite:          defaultMaxIOSize,
			MaxReadAhead:      defaultMaxIOSize,
			DisableXAttrs:     true,
			AllowOther:        true,
			DirectMount:       true,
			DirectMountStrict: false,
		},
	}
	entryTO := defaultEntryTimeout
	attrTO := defaultAttrTimeout
	opts.EntryTimeout = &entryTO
	opts.AttrTimeout = &attrTO
	server, err := fs.Mount(mount.MountPoint, root, opts)
	if err != nil {
		return domain.Errorf(domain.CodeInternal, "FUSE 挂载失败 (%s): %v", mount.MountPoint, err)
	}
	go server.Wait()
	m.runs[mount.ID] = &runningMount{server: server}
	return nil
}

func (m *liveManager) Unmount(_ context.Context, id int64) error {
	m.mu.Lock()
	run, ok := m.runs[id]
	if ok {
		delete(m.runs, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if run.server == nil {
		return nil
	}
	return run.server.Unmount()
}

func fileIno(accountID int64, fileID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte{byte(accountID), byte(accountID >> 8), byte(accountID >> 16), byte(accountID >> 24)})
	_, _ = h.Write([]byte(fileID))
	return h.Sum64()
}
