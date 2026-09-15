package offlinedownload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/driver"
	"litepan/internal/upload"
)

const (
	TempCleanupInterval = 10 * time.Minute
)

func CleanupBuiltinTempDirs(dir string, active map[string]struct{}, maxAge time.Duration) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	deleted := 0
	for _, entry := range entries {
		if !entry.IsDir() || !isBuiltinTaskDirName(entry.Name()) {
			continue
		}
		path := filepath.Clean(filepath.Join(dir, entry.Name()))
		if active != nil {
			if _, ok := active[path]; ok {
				continue
			}
		}
		if maxAge > 0 {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) < maxAge {
				continue
			}
		}
		if err := os.RemoveAll(path); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

func isBuiltinTaskDirName(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, ch := range name {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}

func (s *Service) builtinRootSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	roots := make([]string, 0, len(s.builtinRoots))
	for root := range s.builtinRoots {
		roots = append(roots, root)
	}
	return roots
}

func builtinTempEntryForRoots(roots []string, taskID, path string) string {
	for _, root := range roots {
		entry := builtinTempEntryPath(root, path)
		if entry != "" && filepath.Base(entry) == taskID {
			return entry
		}
	}
	return ""
}

func builtinRootFromTaskPath(taskID, path string) (string, bool) {
	taskID = strings.TrimSpace(taskID)
	current := filepath.Clean(strings.TrimSpace(path))
	if taskID == "" || current == "" || current == "." {
		return "", false
	}
	for {
		if filepath.Base(current) == taskID {
			return filepath.Dir(current), true
		}
		parent := filepath.Dir(current)
		if parent == current || parent == "." {
			return "", false
		}
		current = parent
	}
}

func (s *Service) rememberRestoredBuiltinRoots() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		if task == nil || task.ProviderKind != ProviderBuiltin {
			continue
		}
		if root, ok := builtinRootFromTaskPath(task.TaskID, task.LocalTempPath); ok {
			s.builtinRoots[root] = struct{}{}
		}
	}
}

func (s *Service) builtinTaskTempPath(taskID, localPath string) string {
	roots := s.builtinRootSnapshot()
	if entry := builtinTempEntryForRoots(roots, taskID, localPath); entry != "" {
		return entry
	}
	return filepath.Join(s.BuiltinTempDir(), taskID)
}

func (s *Service) activeBuiltinTempPaths(ctx context.Context) map[string]struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	roots := s.builtinRootSnapshot()
	active := make(map[string]struct{})

	s.mu.Lock()
	uploads := s.uploads
	for _, task := range s.tasks {
		if task.ProviderKind != ProviderBuiltin || task.Status == driver.OfflineStatusSuccess {
			continue
		}
		if path := builtinTempEntryForRoots(roots, task.TaskID, task.LocalTempPath); path != "" {
			active[path] = struct{}{}
			continue
		}
		for _, root := range roots {
			if path := builtinTempEntryPath(root, filepath.Join(root, task.TaskID)); path != "" {
				active[path] = struct{}{}
				break
			}
		}
	}
	s.mu.Unlock()

	if uploads == nil {
		return active
	}
	for _, task := range uploads.List(ctx, 0) {
		if task.SourceType != upload.SourceTypeOfflineHandoff {
			continue
		}
		if task.Status == upload.StatusSuccess || task.Status == upload.StatusSkipped {
			continue
		}
		for _, root := range roots {
			if path := builtinTempEntryPath(root, task.CleanupLocalPath); path != "" {
				active[path] = struct{}{}
				break
			}
		}
	}
	return active
}

func (s *Service) CleanupOrphanTempDirs(ctx context.Context, maxAge time.Duration) (int, error) {
	active := s.activeBuiltinTempPaths(ctx)
	deleted := 0
	for _, root := range s.builtinRootSnapshot() {
		n, err := CleanupBuiltinTempDirs(root, active, maxAge)
		deleted += n
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

func (s *Service) removeBuiltinTaskTemp(taskID, localPath string) {
	entry := s.builtinTaskTempPath(taskID, localPath)
	if entry == "" {
		return
	}
	removeBuiltinTempPath(entry)
}

func (s *Service) runTempCleanup(ctx context.Context) {
	ticker := time.NewTicker(TempCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.CleanupOrphanTempDirs(ctx, 0)
			if err != nil {
				s.log.Warn("builtin offline temp cleanup failed", "err", err)
				continue
			}
			if n > 0 {
				s.log.Info("builtin offline temp cleanup done", "deleted", n)
			}
		}
	}
}

func builtinTempEntryPath(root, path string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	if root == "" || root == "." || path == "" || path == "." {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ""
	}
	head := rel
	if idx := strings.IndexRune(rel, os.PathSeparator); idx >= 0 {
		head = rel[:idx]
	}
	head = strings.TrimSpace(head)
	if head == "" || head == "." || head == ".." {
		return ""
	}
	return filepath.Join(root, head)
}
