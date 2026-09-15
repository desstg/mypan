//go:build linux

package fusemount

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"litepan/internal/domain"

	"golang.org/x/sys/unix"
)

func forceReleaseMountPoint(mountPoint string) error {
	mountPoint = filepath.Clean(mountPoint)
	if mountPoint == "" || mountPoint == "/" {
		return nil
	}
	if err := lazyUnmount(mountPoint); err != nil {
		return err
	}
	if err := execFusermountUnmount(mountPoint); err != nil {
		return err
	}
	return nil
}

func lazyUnmount(mountPoint string) error {
	err := unix.Unmount(mountPoint, unix.MNT_DETACH)
	if err == nil || isBenignUnmountErr(err) {
		return nil
	}
	return err
}

func execFusermountUnmount(mountPoint string) error {
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		cmd := exec.Command(bin, "-u", "-z", mountPoint)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return nil
}

func isBenignUnmountErr(err error) bool {
	return errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOENT) ||
		errors.Is(err, unix.EPERM)
}

func mountPointBusy(mountPoint string) bool {
	mountPoint = filepath.Clean(mountPoint)
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if unescapeMountinfo(fields[4]) == mountPoint {
			return true
		}
	}
	return false
}

func busyMountAncestor(mountPoint string) string {
	mountPoint = filepath.Clean(mountPoint)
	root := filepath.Clean(MountRoot)
	for cur := mountPoint; cur != root && strings.HasPrefix(cur, root); cur = filepath.Dir(cur) {
		if mountPointBusy(cur) {
			return cur
		}
	}
	return ""
}

func reclaimMountPoint(mountPoint string) error {
	mountPoint = filepath.Clean(mountPoint)
	if mountPoint == "" || mountPoint == "/" {
		return nil
	}
	if busy := busyMountAncestor(mountPoint); busy != "" && busy != mountPoint {
		return domain.Errorf(domain.CodeValidation, "挂载点位于已占用路径 %s 之下，请更换挂载点或先在宿主机卸载该路径", busy)
	}
	if err := forceReleaseMountPoint(mountPoint); err != nil {
		return domain.Errorf(domain.CodeValidation, "清理挂载点 %s 失败: %v", mountPoint, err)
	}
	if mountPointBusy(mountPoint) {
		return domain.Errorf(domain.CodeValidation, "挂载点 %s 仍被系统占用，请稍后在宿主机执行 fusermount -u %s", mountPoint, mountPoint)
	}
	if err := os.Remove(mountPoint); err != nil && !os.IsNotExist(err) {
		return domain.Errorf(domain.CodeValidation, "清理挂载目录 %s 失败: %v", mountPoint, err)
	}
	return nil
}

func removeMountPointDir(mountPoint string) error {
	if err := reclaimMountPoint(mountPoint); err != nil {
		return err
	}
	return nil
}

func unescapeMountinfo(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if c := unescapeOctal(s[i+1 : i+4]); c >= 0 {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unescapeOctal(s string) int {
	if len(s) != 3 {
		return -1
	}
	n := 0
	for i := 0; i < 3; i++ {
		if s[i] < '0' || s[i] > '7' {
			return -1
		}
		n = n*8 + int(s[i]-'0')
	}
	if n > unicode.MaxASCII {
		return -1
	}
	return n
}
