package backuprestore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TempCandidate 是一次备份创建、导入或恢复准备中断后遗留的工作目录。
type TempCandidate struct {
	Path       string
	SizeBytes  int64
	FileCount  int64
	DirCount   int64
	ModifiedAt time.Time
}

// OrphanTempCandidates 在备份互斥锁内扫描未被待恢复计划引用的临时目录。
func (s *Service) OrphanTempCandidates(_ context.Context, minAge time.Duration) ([]TempCandidate, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orphanTempCandidatesLocked(minAge)
}

// CleanupOrphanTempCandidates 在互斥锁内重新核对候选，再删除指定路径。
func (s *Service) CleanupOrphanTempCandidates(_ context.Context, paths []string, minAge time.Duration) (removed int, freed int64, err error) {
	if s == nil || len(paths) == 0 {
		return 0, 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates, err := s.orphanTempCandidatesLocked(minAge)
	if err != nil {
		return 0, 0, err
	}
	allowed := make(map[string]TempCandidate, len(candidates))
	for _, item := range candidates {
		allowed[filepath.Clean(item.Path)] = item
	}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		item, ok := allowed[path]
		if !ok {
			continue
		}
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
			return removed, freed, err
		}
		removed++
		freed += item.SizeBytes
	}
	return removed, freed, nil
}

func (s *Service) orphanTempCandidatesLocked(minAge time.Duration) ([]TempCandidate, error) {
	pendingStage := ""
	if plan, ok := s.readPending(); ok && validRecordID(plan.StageDir) {
		pendingStage = filepath.Clean(filepath.Join(s.restoreDir, "staging", plan.StageDir))
	}
	now := time.Now()
	roots := []string{filepath.Join(s.backupsDir, ".tmp"), filepath.Join(s.restoreDir, "staging")}
	var out []TempCandidate
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			path := filepath.Clean(filepath.Join(root, entry.Name()))
			if pendingStage != "" && path == pendingStage {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if minAge > 0 && now.Sub(info.ModTime()) < minAge {
				continue
			}
			candidate := TempCandidate{Path: path, ModifiedAt: info.ModTime()}
			if err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					candidate.FileCount++
					return nil
				}
				if d.IsDir() {
					candidate.DirCount++
					return nil
				}
				fileInfo, err := d.Info()
				if err != nil {
					return err
				}
				candidate.FileCount++
				candidate.SizeBytes += fileInfo.Size()
				return nil
			}); err != nil {
				continue
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}
