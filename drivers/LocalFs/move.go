package localfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"litepan/internal/domain"
)

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	_ = ctx
	src, err := d.resolveEntry(fileID)
	if err != nil {
		return err
	}
	root := filepath.Clean(d.root())
	if src == root {
		return domain.Errorf(domain.CodeValidation, "不能重命名存储根目录")
	}
	name, err := validateEntryName(newName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(src); err != nil {
		return domain.Wrap(domain.CodeNotFound, err)
	}
	dst := filepath.Join(filepath.Dir(src), name)
	if err := d.ensureWithinRoot(dst); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return domain.Errorf(domain.CodeValidation, "目标已存在同名条目")
	} else if !os.IsNotExist(err) {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return mapFSError(err, "重命名")
	}
	return nil
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, sourceParentID string) error {
	_ = ctx
	_ = sourceParentID
	return d.transferFiles(fileIDs, targetParentID, false)
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	_ = ctx
	return d.transferFiles(fileIDs, targetParentID, true)
}

func (d *Driver) transferFiles(fileIDs []string, targetParentID string, copyMode bool) error {
	targetDir, err := d.resolveDir(targetParentID)
	if err != nil {
		return err
	}
	root := filepath.Clean(d.root())
	action := "移动"
	if copyMode {
		action = "复制"
	}
	for _, id := range fileIDs {
		src, err := d.resolveEntry(id)
		if err != nil {
			return err
		}
		if src == root {
			return domain.Errorf(domain.CodeValidation, "不能%s存储根目录", action)
		}
		info, err := os.Lstat(src)
		if err != nil {
			return domain.Wrap(domain.CodeNotFound, err)
		}
		dst := filepath.Join(targetDir, info.Name())
		if err := d.ensureWithinRoot(dst); err != nil {
			return err
		}
		if filepath.Clean(src) == filepath.Clean(dst) {
			continue
		}
		if isSubPath(src, dst) {
			return domain.Errorf(domain.CodeValidation, "不能把目录%s到其自身子目录", action)
		}
		if _, err := os.Stat(dst); err == nil {
			return domain.Errorf(domain.CodeValidation, "目标已存在同名条目: %s", info.Name())
		} else if !os.IsNotExist(err) {
			return domain.Wrap(domain.CodeDriverError, err)
		}
		if copyMode {
			if err := d.copyPath(src, dst, info); err != nil {
				return mapFSError(err, action)
			}
			continue
		}
		if err := d.renameOrCopyMove(src, dst); err != nil {
			return mapFSError(err, action)
		}
	}
	return nil
}

func (d *Driver) renameOrCopyMove(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !isCrossDevice(err) {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := d.copyPath(src, dst, info); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

func (d *Driver) copyPath(src, dst string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(link, dst)
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(sp)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, dp); err != nil {
				return err
			}
			continue
		}
		if e.IsDir() {
			if err := copyDir(sp, dp); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sp, dp, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EXDEV
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return isCrossDevice(le.Err)
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return isCrossDevice(pe.Err)
	}
	return false
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EBUSY
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return isBusy(le.Err)
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return isBusy(pe.Err)
	}
	return false
}

func mapFSError(err error, action string) error {
	if err == nil {
		return nil
	}
	if isBusy(err) {
		return domain.Errorf(domain.CodeValidation, "%s失败：目录正被占用（可能被 STRM/FUSE/进程打开），请先停止相关任务后再试", action)
	}
	return domain.Wrap(domain.CodeDriverError, err)
}
