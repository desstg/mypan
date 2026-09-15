//go:build fuse

package fuse

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	virtualCapacityBytes uint64 = 1 << 50 // 1 PiB，仅用于兼容文件管理器的写入前检查。
	virtualBlockSize     uint32 = 4096
	virtualFileSlots     uint64 = 1 << 32
)

var (
	_ fs.NodeStatfser = (*cloudDir)(nil)
	_ fs.NodeStatfser = (*cloudFile)(nil)
	_ fs.NodeStatfser = (*stagingFile)(nil)
)

func fillVirtualStatfs(out *fuse.StatfsOut) {
	blocks := virtualCapacityBytes / uint64(virtualBlockSize)
	out.Blocks = blocks
	out.Bfree = blocks
	out.Bavail = blocks
	out.Files = virtualFileSlots
	out.Ffree = virtualFileSlots
	out.Bsize = virtualBlockSize
	out.Frsize = virtualBlockSize
	out.NameLen = 255
}

func (*cloudDir) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	fillVirtualStatfs(out)
	return 0
}

func (*cloudFile) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	fillVirtualStatfs(out)
	return 0
}

func (*stagingFile) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	fillVirtualStatfs(out)
	return 0
}
