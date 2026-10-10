package strm

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ReplaceBaseURLResult struct {
	Total   int `json:"total"`
	Updated int `json:"updated"`
}

var strmBaseURLPrefix = regexp.MustCompile(`^https?://[^/]+`)

// replaceBaseURLState 是「一键替换基址」的后台任务状态。
//
// 全局一个（不是 per-task）：这个动作作用于**整个** strmDir，不针对某个任务。
type replaceBaseURLState struct {
	BaseURL string
	Running bool
	Total   int
	Updated int
	Error   string
}

func ReplaceBaseURLInFiles(strmDir, newBaseURL string) (ReplaceBaseURLResult, error) {
	return ReplaceBaseURLInFilesWithProgress(strmDir, newBaseURL, nil)
}

// ReplaceBaseURLInFilesWithProgress 与 ReplaceBaseURLInFiles 一样，只是多一个进度回调。
//
// 抽出来是因为这条路要**改成后台跑**（见 Service.StartReplaceBaseURL）：走完整棵
// STRM 目录树要逐个读写文件，媒体库一大的话几分钟起步，同步等会被反代 502。
// 异步之后调用方拿不到返回值，进度就是唯一的观感来源。
//
// onProgress 在每个**改成功**的文件上回调一次（total, updated）。失败的不报 ——
// 前端要的是「还剩多少」，不是「扫到第几个」。
func ReplaceBaseURLInFilesWithProgress(strmDir, newBaseURL string, onProgress func(total, updated int)) (ReplaceBaseURLResult, error) {
	var result ReplaceBaseURLResult
	base := NormalizeBaseURL(newBaseURL)
	if base == "" {
		return result, fmt.Errorf("new base url required")
	}
	root := strings.TrimSpace(strmDir)
	if root == "" {
		root = "strm"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return result, err
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			return nil
		}
		result.Total++
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		if len(lines) == 0 {
			return nil
		}
		first := strings.TrimSpace(lines[0])
		if first == "" {
			return nil
		}
		replaced, changed := replaceBaseInLine(first, base)
		if !changed {
			return nil
		}
		lines[0] = replaced
		out := strings.Join(lines, "\n")
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err == nil {
			result.Updated++
			if onProgress != nil {
				onProgress(result.Total, result.Updated)
			}
		}
		return nil
	})
	return result, err
}

func replaceBaseInLine(line, base string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return line, false
	}
	if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
		return line, false
	}
	replaced := strmBaseURLPrefix.ReplaceAllString(line, base)
	return replaced, replaced != line
}

func ValidateBaseURL(raw string) error {
	base := NormalizeBaseURL(raw)
	if base == "" {
		return fmt.Errorf("新基址不能为空")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("新基址格式不正确，示例：https://litepan.top")
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if rest == "" || strings.ContainsAny(rest, " \t\r\n") {
		return fmt.Errorf("新基址格式不正确，示例：https://litepan.top")
	}
	return nil
}
