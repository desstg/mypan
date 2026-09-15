package upload

import (
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) removeLocalFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (m *Manager) cleanupLocalSource(localPath, cleanupPath, mode string) {
	switch mode {
	case CleanupLocalFileOnSuccess:
		m.removeLocalFile(localPath)
	case CleanupLocalPathOnSuccess:
		path := strings.TrimSpace(cleanupPath)
		if path == "" {
			path = localPath
		}
		if path == "" {
			return
		}
		_ = os.RemoveAll(path)
	case CleanupLocalTreeOnSuccess:
		m.removeLocalFile(localPath)
		removeEmptyParentDirs(localPath, cleanupPath)
	default:
		return
	}
}

// removeEmptyParentDirs 只在 root 内向上回收空目录，避免一个文件完成时误删
// 同批次仍在上传或失败保留的其它文件。
func removeEmptyParentDirs(localPath, root string) {
	localPath = filepath.Clean(strings.TrimSpace(localPath))
	root = filepath.Clean(strings.TrimSpace(root))
	if localPath == "" || localPath == "." || root == "" || root == "." {
		return
	}
	rel, err := filepath.Rel(root, localPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return
	}
	for dir := filepath.Dir(localPath); ; dir = filepath.Dir(dir) {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		if dir == root {
			return
		}
	}
}
