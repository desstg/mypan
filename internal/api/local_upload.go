package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/upload"
)

type localUploadMapping struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var (
	systemJunkFiles = map[string]struct{}{
		".ds_store":   {},
		".localized":  {},
		"thumbs.db":   {},
		"desktop.ini": {},
	}
	systemJunkDirs = map[string]struct{}{
		"__macosx":                  {},
		".spotlight-v100":           {},
		".trashes":                  {},
		".fseventsd":                {},
		"$recycle.bin":              {},
		"system volume information": {},
	}
	systemTrashDirPattern = regexp.MustCompile(`^\.trash-\d+$`)
)

func isSystemJunkFile(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if _, ok := systemJunkFiles[n]; ok {
		return true
	}
	return strings.HasPrefix(n, "._")
}

func isSystemJunkDir(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if _, ok := systemJunkDirs[n]; ok {
		return true
	}
	return systemTrashDirPattern.MatchString(n)
}

func (h *Handler) loadLocalUploadMappings() []localUploadMapping {
	if h.settings == nil {
		return nil
	}
	raw := h.settings.String(settings.KeyLocalUploadMappings)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []localUploadMapping
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	valid := out[:0]
	seen := make(map[string]struct{}, len(out))
	for _, m := range out {
		name := strings.TrimSpace(m.Name)
		path := strings.TrimSpace(m.Path)
		if name == "" || path == "" || !strings.HasPrefix(path, "/") {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		valid = append(valid, localUploadMapping{Name: name, Path: filepath.Clean(path)})
	}
	return valid
}

func (h *Handler) findLocalUploadMapping(name string) (localUploadMapping, bool) {
	for _, m := range h.loadLocalUploadMappings() {
		if m.Name == strings.TrimSpace(name) {
			return m, true
		}
	}
	return localUploadMapping{}, false
}

func (h *Handler) getLocalUploadConfig(w http.ResponseWriter, _ *http.Request) {
	enabled := false
	if h.settings != nil {
		enabled = h.settings.Bool(settings.KeyLocalUploadEnabled)
	}
	writeOK(w, map[string]any{
		"enabled":  enabled,
		"mappings": h.loadLocalUploadMappings(),
	})
}

func (h *Handler) updateLocalUploadConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled  bool                 `json:"enabled"`
		Mappings []localUploadMapping `json:"mappings"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if h.settings == nil {
		writeErr(w, domain.Errorf(domain.CodeInternal, "设置服务未初始化"))
		return
	}
	seen := make(map[string]struct{}, len(in.Mappings))
	for i := range in.Mappings {
		m := &in.Mappings[i]
		m.Name = strings.TrimSpace(m.Name)
		m.Path = strings.TrimSpace(m.Path)
		if m.Name == "" {
			writeErr(w, domain.Errorf(domain.CodeValidation, "映射标签名不能为空"))
			return
		}
		if _, dup := seen[m.Name]; dup {
			writeErr(w, domain.Errorf(domain.CodeValidation, "映射标签名重复：%s", m.Name))
			return
		}
		seen[m.Name] = struct{}{}
		if m.Path == "" || !strings.HasPrefix(m.Path, "/") {
			writeErr(w, domain.Errorf(domain.CodeValidation, "映射路径必须是以 / 开头的容器内路径：%s", m.Path))
			return
		}
		cleaned := filepath.Clean(m.Path)
		if cleaned == "/" || cleaned != m.Path {
			writeErr(w, domain.Errorf(domain.CodeValidation, "映射路径不合法：%s", m.Path))
			return
		}
		m.Path = cleaned
	}
	raw, err := json.Marshal(in.Mappings)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.settings.Update(r.Context(), map[string]string{
		settings.KeyLocalUploadEnabled:  boolString(in.Enabled),
		settings.KeyLocalUploadMappings: string(raw),
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{
		"enabled":  in.Enabled,
		"mappings": in.Mappings,
	})
}

type localUploadEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime"`
	RelPath string `json:"rel_path"`
}

func (h *Handler) browseLocalUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mapping string `json:"mapping"`
		Path    string `json:"path"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	m, ok := h.findLocalUploadMapping(in.Mapping)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeValidation, "映射目录不存在：%s", in.Mapping))
		return
	}
	rel := cleanRelativePath(in.Path)
	dir := filepath.Join(m.Path, rel)
	if !isWithinRoot(dir, m.Path) {
		writeErr(w, domain.Errorf(domain.CodeValidation, "路径超出映射目录范围"))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeDriverError, "读取目录失败：%v", err))
		return
	}
	out := make([]localUploadEntry, 0, len(entries))
	for _, e := range entries {
		if isSystemJunkDir(e.Name()) || isSystemJunkFile(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		itemRel := e.Name()
		if rel != "" {
			itemRel = rel + "/" + e.Name()
		}
		out = append(out, localUploadEntry{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			MTime:   info.ModTime().Unix(),
			RelPath: itemRel,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeOK(w, map[string]any{"mapping": in.Mapping, "path": rel, "items": out})
}

func (h *Handler) createLocalUploadTasks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID      int64  `json:"account_id"`
		Mapping        string `json:"mapping"`
		TargetPath     string `json:"target_path"`
		TargetDisplay  string `json:"target_display_path"`
		ConflictPolicy string `json:"conflict_policy"`
		ClientTaskID   string `json:"client_task_id"`
		DisplayName    string `json:"display_name"`
		Items          []struct {
			RelPath string `json:"rel_path"`
			IsDir   bool   `json:"is_dir"`
		} `json:"items"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if len(in.Items) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "未选择任何文件"))
		return
	}
	m, ok := h.findLocalUploadMapping(in.Mapping)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeValidation, "映射目录不存在：%s", in.Mapping))
		return
	}
	conflict := strings.TrimSpace(in.ConflictPolicy)
	if conflict == "" {
		conflict = "overwrite"
	}
	var sources []localUploadSource
	for _, item := range in.Items {
		rel := cleanRelativePath(item.RelPath)
		if rel == "" {
			continue
		}
		abs := filepath.Join(m.Path, rel)
		if !isWithinRoot(abs, m.Path) {
			writeErr(w, domain.Errorf(domain.CodeValidation, "路径超出映射目录范围：%s", item.RelPath))
			return
		}
		collected, err := buildLocalUploadSources(abs, rel, item.IsDir)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeDriverError, "遍历目录失败：%v", err))
			return
		}
		sources = append(sources, collected...)
	}
	if len(sources) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "未找到可上传的文件"))
		return
	}
	batchID := strings.TrimSpace(in.ClientTaskID)
	batchName := strings.TrimSpace(in.DisplayName)
	if batchName == "" && len(in.Items) == 1 && in.Items[0].IsDir {
		batchName = path.Base(strings.Trim(cleanRelativePath(in.Items[0].RelPath), "/"))
	}

	ctx := context.WithoutCancel(r.Context())
	go func() {
		created, err := h.createLocalUploadTasksSync(ctx, m, in.AccountID, in.TargetPath,
			strings.TrimSpace(in.TargetDisplay), batchID, batchName, conflict,
			sources)
		if err == nil {
			return
		}
		h.logError("服务器上传任务未全部创建", "requested", len(sources), "created", len(created), "err", err.Error())
		if h.notifications != nil {
			h.notifications.Notify(ctx, "warning", "upload", "服务器上传任务未全部创建",
				fmt.Sprintf("已创建 %d/%d 个上传任务：%v", len(created), len(sources), err), in.AccountID, 0)
		}
	}()

	writeOK(w, map[string]any{"accepted": true, "count": len(sources)})
}

func (h *Handler) createLocalUploadTasksSync(
	ctx context.Context,
	m localUploadMapping,
	accountID int64,
	targetRoot, targetDisplay, clientTaskID, displayName, conflict string,
	sources []localUploadSource,
) ([]*upload.Task, error) {
	const batchSize = 100
	batch := make([]upload.CreateParams, 0, batchSize)
	seq := 0
	var tasks []*upload.Task
	targetDirs := map[string]string{"": targetRoot}
	createdDirs := map[string]bool{"": false}
	skipped := 0
	var firstErr error
	recordFailure := func(err error) {
		skipped++
		if firstErr == nil {
			firstErr = err
		}
	}
	accountName, driverType := "", ""
	if h.accountSvc != nil {
		accountName, driverType, _ = h.accountSvc.LookupUploadAccount(ctx, accountID)
	}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if h.uploads == nil {
			batch = batch[:0]
			return domain.Errorf(domain.CodeInternal, "上传服务未初始化")
		}
		created, err := h.uploads.CreateBatch(ctx, batch)
		batch = batch[:0]
		if err != nil {
			return err
		}
		tasks = append(tasks, created...)
		return nil
	}
	batchRootName := ""
	for _, source := range sources {
		parts := strings.Split(strings.Trim(filepath.ToSlash(source.relPath), "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		if batchRootName == "" {
			batchRootName = parts[0]
			continue
		}
		if batchRootName != parts[0] {
			batchRootName = ""
			break
		}
	}
	for _, s := range sources {
		if err := ctx.Err(); err != nil {
			return tasks, err
		}
		targetParent, ok := targetDirs[s.relDir]
		if !ok {
			parent, err := h.ensureLocalUploadTargetDir(ctx, accountID, targetRoot, s.relDir, targetDirs, createdDirs)
			if err != nil {
				h.logError("创建网盘子目录失败", "dir", s.relDir, "err", err.Error())
				recordFailure(fmt.Errorf("创建目录 %s 失败：%w", s.relDir, err))
				continue
			}
			targetParent = parent
			targetDirs[s.relDir] = targetParent
		}
		localPath, err := resolveLocalUploadSource(s.abs, m.Path)
		if err != nil {
			h.logError("检查服务器上传文件失败", "path", s.abs, "err", err.Error())
			recordFailure(fmt.Errorf("检查文件 %s 失败：%w", filepath.Base(s.abs), err))
			continue
		}
		info, err := statLocalFile(localPath)
		if err != nil {
			h.logError("读取本地文件失败", "path", localPath, "err", err.Error())
			recordFailure(fmt.Errorf("读取文件 %s 失败：%w", filepath.Base(s.abs), err))
			continue
		}
		taskID := clientTaskID
		if taskID != "" {
			taskID = taskID + "-" + strconv.Itoa(seq)
		}
		batch = append(batch, upload.CreateParams{
			ClientTaskID:      taskID,
			BatchID:           clientTaskID,
			BatchName:         batchRootName,
			BatchRootID:       targetDirs[batchRootName],
			BatchRootParentID: targetRoot,
			BatchRootOwned:    batchRootName != "" && createdDirs[batchRootName],
			AccountID:         accountID,
			AccountName:       accountName,
			DriverType:        driverType,
			FileName:          filepath.Base(s.abs),
			DisplayName:       filepath.Base(s.abs),
			RelPath:           trimUploadBatchRoot(s.relPath, batchRootName),
			RelDir:            trimUploadBatchRoot(s.relDir, batchRootName),
			TargetPath:        targetParent,
			TargetDisplayPath: joinLocalDisplayPath(targetDisplay, s.relDir),
			LocalPath:         localPath,
			TotalBytes:        info.Size(),
			ConflictPolicy:    conflict,
			SourceType:        upload.SourceTypeServerLocal,
			CleanupLocalMode:  upload.CleanupLocalModeKeep,
		})
		seq++
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return tasks, err
			}
		}
	}
	if err := flush(); err != nil {
		return tasks, err
	}
	if skipped > 0 {
		return tasks, fmt.Errorf("有 %d 个文件未创建上传任务：%w", skipped, firstErr)
	}
	return tasks, nil
}

func trimUploadBatchRoot(value, batchName string) string {
	value = strings.Trim(filepath.ToSlash(value), "/")
	batchName = strings.Trim(filepath.ToSlash(batchName), "/")
	if value == batchName {
		return ""
	}
	if batchName != "" && strings.HasPrefix(value, batchName+"/") {
		return strings.TrimPrefix(value, batchName+"/")
	}
	return value
}

type localUploadSource struct {
	abs     string
	relDir  string
	relPath string
}

func buildLocalUploadSources(abs, rel string, isDir bool) ([]localUploadSource, error) {
	if !isDir {
		if isSystemJunkFile(filepath.Base(abs)) {
			return nil, nil
		}
		return []localUploadSource{{abs: abs, relPath: filepath.Base(abs)}}, nil
	}
	rootName := strings.Trim(path.Base(strings.Trim(rel, "/")), "/")
	if rootName == "" || rootName == "." {
		rootName = strings.Trim(filepath.Base(abs), string(filepath.Separator))
	}
	sources := make([]localUploadSource, 0)
	if err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isSystemJunkDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isSystemJunkFile(d.Name()) {
			return nil
		}
		relDir := rootName
		if dir := filepath.Dir(p); dir != abs {
			inner, rerr := filepath.Rel(abs, dir)
			if rerr != nil {
				return rerr
			}
			if inner != "." {
				relDir = filepath.ToSlash(filepath.Join(rootName, inner))
			}
		}
		relPath := filepath.ToSlash(filepath.Join(relDir, d.Name()))
		sources = append(sources, localUploadSource{abs: p, relDir: relDir, relPath: relPath})
		return nil
	}); err != nil {
		return nil, err
	}
	return sources, nil
}

func statLocalFile(abs string) (fs.FileInfo, error) {
	return os.Stat(abs)
}

// 解析符号链接后检查边界。
func resolveLocalUploadSource(abs, root string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !isWithinRoot(resolvedPath, resolvedRoot) {
		return "", domain.Errorf(domain.CodeValidation, "路径超出映射目录范围")
	}
	return resolvedPath, nil
}

func joinLocalDisplayPath(base, relDir string) string {
	base = strings.Trim(strings.ReplaceAll(base, "\\", "/"), "/")
	relDir = strings.Trim(strings.ReplaceAll(relDir, "\\", "/"), "/")
	if relDir == "" {
		return base
	}
	if base == "" {
		return relDir
	}
	return base + "/" + relDir
}

func (h *Handler) logError(msg string, args ...any) {
	if h.log != nil {
		h.log.Error(msg, args...)
		return
	}
	slog.Error(msg, args...)
}

func (h *Handler) ensureLocalUploadTargetDir(
	ctx context.Context,
	accountID int64,
	rootID, relDir string,
	cache map[string]string,
	createdCache map[string]bool,
) (string, error) {
	if h.files == nil {
		return "", domain.Errorf(domain.CodeInternal, "文件服务未就绪")
	}
	if cache == nil {
		cache = make(map[string]string)
	}
	if createdCache == nil {
		createdCache = make(map[string]bool)
	}
	if _, ok := cache[""]; !ok {
		cache[""] = rootID
	}
	relDir = strings.Trim(strings.ReplaceAll(relDir, "\\", "/"), "/")
	if relDir == "" {
		return rootID, nil
	}
	cur := rootID
	parts := make([]string, 0, strings.Count(relDir, "/")+1)
	for _, part := range strings.Split(relDir, "/") {
		if part == "" {
			continue
		}
		parts = append(parts, part)
		key := strings.Join(parts, "/")
		if cached, ok := cache[key]; ok {
			cur = cached
			continue
		}
		items, err := h.files.List(ctx, accountID, cur, false)
		if err != nil {
			return "", err
		}
		next := ""
		for _, item := range items {
			if item.IsDir && item.Name == part {
				next = item.ID
				break
			}
		}
		if next == "" {
			created, err := h.files.CreateFolder(ctx, accountID, cur, part)
			if err != nil {
				return "", err
			}
			next = created.ID
			createdCache[key] = true
		} else {
			createdCache[key] = false
		}
		cur = next
		cache[key] = cur
	}
	return cur, nil
}

func cleanRelativePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(p))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func isWithinRoot(abs, root string) bool {
	root = filepath.Clean(root)
	abs = filepath.Clean(abs)
	if abs == root {
		return true
	}
	return strings.HasPrefix(abs, root+string(filepath.Separator))
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
