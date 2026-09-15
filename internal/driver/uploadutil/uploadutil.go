package uploadutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const DefaultReadChunk = 1024 * 1024

type LocalFile struct {
	Path string
	Size int64
}

func StatLocalFile(localPath string) (LocalFile, error) {
	path := strings.TrimSpace(localPath)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return LocalFile{}, domain.Errorf(domain.CodeValidation, "待上传文件不存在")
	}
	return LocalFile{Path: path, Size: info.Size()}, nil
}

func NormalizeConflictPolicy(policy string) string {
	p := strings.ToLower(strings.TrimSpace(policy))
	if p == "" {
		return "overwrite"
	}
	return p
}

func NotifyProgress(fn driver.UploadProgress, uploaded, total int64, message string) {
	if fn != nil {
		fn(uploaded, total, message)
	}
}

func KeepBothName(name string, existing map[string]struct{}) string {
	if _, ok := existing[name]; !ok {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}

func ValidateFileName(name string) error {
	if len(name) >= 256 {
		return domain.Errorf(domain.CodeValidation, "文件名长度不能超过255个字符")
	}
	if strings.ContainsAny(name, `"\\/:*?|><`) {
		return domain.Errorf(domain.CodeValidation, "文件名不能包含以下字符：\"\\/:*?|><")
	}
	return nil
}
