package strm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var strmSafeNameRepl = strings.NewReplacer(
	"/", "_",
	"\\", "_",
	":", "_",
	"*", "_",
	"?", "_",
	"\"", "_",
	"<", "_",
	">", "_",
	"|", "_",
)

func MediaStem(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return strings.TrimSuffix(name, ext)
}

func SafeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_"
	}
	name = strmSafeNameRepl.Replace(name)
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	return name
}

// SafeStem 保留 STEM 前后空格，只替换非法字符；全空白 / 空串 / 点目录名兜底为 _。
// 用于 STRM 文件名，保证「飞驰人生 .mp4」这类文件名生成的 STRM 保留扩展名前的空格。
func SafeStem(name string) string {
	if strings.TrimSpace(name) == "" {
		return "_"
	}
	name = strmSafeNameRepl.Replace(name)
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	return name
}

// SafeDirSegments 把相对目录字符串拆成安全目录段：
// 容错前后斜杠与反斜杠、压缩空段，并跳过 "." 与 ".."。
func SafeDirSegments(raw string) []string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil
	}
	var out []string
	for _, seg := range strings.Split(raw, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		out = append(out, SafeName(seg))
	}
	return out
}

// NormalizeGroupDir 归一化分组目录，返回存库用的相对路径（如 "电影/港台"，空为根目录）。
func NormalizeGroupDir(raw string) string {
	return strings.Join(SafeDirSegments(raw), "/")
}

// TaskRelDir 返回任务在 STRM 根下的相对目录（分组目录 + 输出文件夹）。
func TaskRelDir(groupDir, outputFolder string) string {
	segs := SafeDirSegments(groupDir)
	segs = append(segs, SafeName(outputFolder))
	return strings.Join(segs, "/")
}

func LocalRelPath(outputFolder string, relDirs []string, fileName string, isoFilenameEnabled bool) string {
	parts := SafeDirSegments(outputFolder)
	for _, dir := range relDirs {
		parts = append(parts, SafeName(dir))
	}
	parts = append(parts, LocalStrmFileName(fileName, isoFilenameEnabled))
	return filepath.Join(parts...)
}

func LegacyLocalRelPath(outputFolder string, relDirs []string, fileName string) string {
	return LocalRelPath(outputFolder, relDirs, fileName, false)
}

func LocalStrmFileName(fileName string, isoFilenameEnabled bool) string {
	if isoFilenameEnabled && isISOFileName(fileName) {
		return SafeName(fileName) + ".strm"
	}
	return SafeStem(MediaStem(fileName)) + ".strm"
}

func isISOFileName(fileName string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(fileName)), ".iso")
}

func MigrateLegacyISOStrmFile(root, outputFolder string, relDirs []string, fileName, fileID string, isoFilenameEnabled bool) (bool, error) {
	if !isoFilenameEnabled || !isISOFileName(fileName) {
		return false, nil
	}
	legacyRelPath := LegacyLocalRelPath(outputFolder, relDirs, fileName)
	currentRelPath := LocalRelPath(outputFolder, relDirs, fileName, true)
	if legacyRelPath == currentRelPath {
		return false, nil
	}
	legacyPath := filepath.Join(root, legacyRelPath)
	currentPath := filepath.Join(root, currentRelPath)
	if pathHasOversizedComponent(legacyPath) || pathHasOversizedComponent(currentPath) {
		return false, nil
	}
	content, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !strmContentReferencesFile(string(content), fileID) {
		return false, nil
	}
	if _, err := os.Stat(currentPath); err == nil {
		return true, os.Remove(legacyPath)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(legacyPath, currentPath); err != nil {
		return false, err
	}
	return true, nil
}

func strmContentReferencesFile(content, fileID string) bool {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return false
	}
	return strings.Contains(content, "/"+EncodeFileKey(fileID)+"/t/")
}

func TaskOutputDir(strmDir, outputFolder string) string {
	segs := SafeDirSegments(outputFolder)
	if len(segs) == 0 {
		return ""
	}
	root := strings.TrimSpace(strmDir)
	if root == "" {
		root = "strm"
	}
	return filepath.Join(append([]string{root}, segs...)...)
}

func DeleteTaskOutput(strmDir, outputFolder string) error {
	dir := TaskOutputDir(strmDir, outputFolder)
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}

// removeStrmScrapeIndex 删除 STRM 刮削海报墙索引（与 strmscrape.TaskIndexPath 约定一致）。
func removeStrmScrapeIndex(dataDir string, taskID int64) {
	base := filepath.Join(strings.TrimSpace(dataDir), "strmscrape", strconv.FormatInt(taskID, 10)+".sqlite")
	for _, p := range []string{base, base + "-wal", base + "-shm"} {
		_ = os.Remove(p)
	}
}

func WriteStrmFile(rootDir, relPath, content string) error {
	full := filepath.Join(rootDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content+"\n"), 0o644)
}
