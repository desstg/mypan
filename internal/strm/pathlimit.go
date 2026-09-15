package strm

import (
	"os"
	"path/filepath"
	"strings"
)

const MaxPathComponentBytes = 255

const (
	pathTooLongDirReason  = "目录名超过255字节，该目录及其内容已跳过"
	pathTooLongFileReason = "文件名超过255字节，已跳过"
)

func pathHasOversizedComponent(path string) bool {
	_, _, ok := oversizedPathFailure(path, false)
	return ok
}

// oversizedPathFailure 返回首个超长路径分量及面向用户的跳过原因。
func oversizedPathFailure(path string, targetIsDir bool) (string, string, bool) {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(os.PathSeparator))
	visible := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		visible = append(visible, part)
		if len([]byte(part)) <= MaxPathComponentBytes {
			continue
		}
		reason := pathTooLongFileReason
		if targetIsDir || i < len(parts)-1 {
			reason = pathTooLongDirReason
		}
		return filepath.Join(visible...), reason, true
	}
	return "", "", false
}

func addOversizedPathFailure(failures *FailureCollector, kind ScanFailureKind, relPath string, targetIsDir bool) bool {
	failurePath, reason, oversized := oversizedPathFailure(relPath, targetIsDir)
	if oversized && failures != nil {
		failures.Add(kind, filepath.ToSlash(failurePath), reason)
	}
	return oversized
}
