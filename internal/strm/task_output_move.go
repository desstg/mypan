package strm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
)

// taskOutputMove 记录一次任务输出目录变更，用于数据库保存失败时回移。
type taskOutputMove struct {
	rootDir string
	oldDir  string
	newDir  string
	changed bool
	moved   bool
}

func (m taskOutputMove) Changed() bool {
	return m.changed
}

// Rollback 在任务配置保存失败时把已移动的整个目录放回原位。
func (m taskOutputMove) Rollback() error {
	if !m.moved {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.oldDir), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(m.oldDir); err == nil {
		return fmt.Errorf("旧目录已被占用：%s", m.oldDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(m.newDir, m.oldDir); err != nil {
		return err
	}
	_ = removeEmptyTaskParents(filepath.Dir(m.newDir), m.rootDir)
	return nil
}

// CleanupOldParents 仅向上清理旧任务目录的空父目录，永不删除 STRM 根目录。
func (m taskOutputMove) CleanupOldParents() error {
	if !m.changed || m.oldDir == "" {
		return nil
	}
	return removeEmptyTaskParents(filepath.Dir(m.oldDir), m.rootDir)
}

// moveTaskOutputDirectory 在同一 STRM 根目录内整体移动任务文件夹。
// 源目录不存在时允许继续保存，下次扫描会直接在新路径生成。
func moveTaskOutputDirectory(strmDir, oldTaskRelDir, newTaskRelDir string) (taskOutputMove, error) {
	move := taskOutputMove{}
	if filepath.Clean(oldTaskRelDir) == filepath.Clean(newTaskRelDir) {
		return move, nil
	}
	move.changed = true

	root := strings.TrimSpace(strmDir)
	if root == "" {
		root = "strm"
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return move, domain.Errorf(domain.CodeInternal, "解析 STRM 根目录失败：%v", err)
	}
	oldDir, err := taskOutputPath(rootAbs, oldTaskRelDir)
	if err != nil {
		return move, err
	}
	newDir, err := taskOutputPath(rootAbs, newTaskRelDir)
	if err != nil {
		return move, err
	}
	move.rootDir = rootAbs
	move.oldDir = oldDir
	move.newDir = newDir

	if _, err := os.Lstat(newDir); err == nil {
		return move, domain.Errorf(domain.CodeValidation, "新的 STRM 输出目录已存在，请先处理该目录后再保存：%s", newDir)
	} else if !os.IsNotExist(err) {
		return move, domain.Errorf(domain.CodeInternal, "检查新 STRM 目录失败：%v", err)
	}
	info, err := os.Lstat(oldDir)
	if os.IsNotExist(err) {
		return move, nil
	}
	if err != nil {
		return move, domain.Errorf(domain.CodeInternal, "检查旧 STRM 目录失败：%v", err)
	}
	if !info.IsDir() {
		return move, domain.Errorf(domain.CodeValidation, "旧 STRM 输出路径不是文件夹：%s", oldDir)
	}
	if pathIsDescendant(oldDir, newDir) {
		return move, domain.Errorf(domain.CodeValidation, "新的 STRM 输出目录不能位于旧任务目录内")
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return move, domain.Errorf(domain.CodeInternal, "创建新 STRM 父目录失败：%v", err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		_ = removeEmptyTaskParents(filepath.Dir(newDir), rootAbs)
		return move, domain.Errorf(domain.CodeInternal, "移动 STRM 任务目录失败：%v", err)
	}
	move.moved = true
	return move, nil
}

func taskOutputPath(rootAbs, taskRelDir string) (string, error) {
	dir := TaskOutputDir(rootAbs, taskRelDir)
	if dir == "" {
		return "", domain.Errorf(domain.CodeValidation, "STRM 输出目录不能为空")
	}
	dirAbs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", domain.Errorf(domain.CodeInternal, "解析 STRM 输出目录失败：%v", err)
	}
	if !pathIsDescendant(rootAbs, dirAbs) {
		return "", domain.Errorf(domain.CodeValidation, "STRM 输出目录超出根目录")
	}
	return dirAbs, nil
}

func pathIsDescendant(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func removeEmptyTaskParents(startDir, rootDir string) error {
	rootDir = filepath.Clean(rootDir)
	current := filepath.Clean(startDir)
	for pathIsDescendant(rootDir, current) {
		entries, err := os.ReadDir(current)
		if os.IsNotExist(err) {
			current = filepath.Dir(current)
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !isRemovableSystemMetadata(entry) {
				return nil
			}
		}
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(current, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
			return err
		}
		current = filepath.Dir(current)
	}
	return nil
}

func isRemovableSystemMetadata(entry os.DirEntry) bool {
	if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	name := entry.Name()
	switch strings.ToLower(name) {
	case ".ds_store",
		".localized",
		".lsoverride",
		".volumeicon.icns",
		"icon\r",
		"thumbs.db",
		"ehthumbs.db",
		"ehthumbs_vista.db",
		"desktop.ini",
		".directory",
		".hidden",
		".xdg-volume-info":
		return true
	default:
		return strings.HasPrefix(name, "._")
	}
}
