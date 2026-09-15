//go:build fuse

package fuse

import (
	"errors"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestVirtualStatfsReportsWritableCapacity(t *testing.T) {
	var out fuse.StatfsOut
	fillVirtualStatfs(&out)

	wantBlocks := virtualCapacityBytes / uint64(virtualBlockSize)
	if out.Blocks != wantBlocks || out.Bfree != wantBlocks || out.Bavail != wantBlocks {
		t.Fatalf("虚拟容量不正确: %+v", out)
	}
	if out.Bsize != virtualBlockSize || out.Frsize != virtualBlockSize {
		t.Fatalf("虚拟块大小不正确: %+v", out)
	}
	if out.Files == 0 || out.Ffree == 0 || out.NameLen != 255 {
		t.Fatalf("虚拟文件系统信息不完整: %+v", out)
	}
}

func TestErrnoForPreservesDiskFull(t *testing.T) {
	if got := errnoFor(errors.Join(errors.New("write failed"), syscall.ENOSPC)); got != syscall.ENOSPC {
		t.Fatalf("磁盘写满应保留 ENOSPC，实际 %v", got)
	}
	if got := errnoFor(errors.Join(errors.New("quota exceeded"), syscall.EDQUOT)); got != syscall.EDQUOT {
		t.Fatalf("配额不足应保留 EDQUOT，实际 %v", got)
	}
}
