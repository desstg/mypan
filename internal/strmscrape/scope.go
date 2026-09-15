package strmscrape

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

type storedScopes map[string][]string

func normalizeScopeDir(raw string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.Trim(clean, "/")
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	return clean
}

func normalizeScopeDirs(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		clean := normalizeScopeDir(value)
		if clean == "" {
			continue
		}
		set[clean] = struct{}{}
	}
	items := make([]string, 0, len(set))
	for value := range set {
		items = append(items, value)
	}
	sort.Slice(items, func(i, j int) bool {
		if len(items[i]) != len(items[j]) {
			return len(items[i]) < len(items[j])
		}
		return items[i] < items[j]
	})
	result := make([]string, 0, len(items))
	for _, item := range items {
		covered := false
		for _, parent := range result {
			if item == parent || strings.HasPrefix(item, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, item)
		}
	}
	return result
}

func (s *Service) loadScopes() storedScopes {
	out := storedScopes{}
	if s.settings == nil {
		return out
	}
	raw := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyStrmScrapeScopes))
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return storedScopes{}
	}
	return out
}

func (s *Service) GetScope(strmTaskID int64) Scope {
	dirs := normalizeScopeDirs(s.loadScopes()[strconv.FormatInt(strmTaskID, 10)])
	return Scope{StrmTaskID: strmTaskID, ExcludedDirs: dirs}
}

func (s *Service) UpdateScope(ctx context.Context, in Scope) (Scope, error) {
	if in.StrmTaskID <= 0 {
		return Scope{}, domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	if _, _, err := s.resolveTask(ctx, in.StrmTaskID); err != nil {
		return Scope{}, err
	}
	dirs := normalizeScopeDirs(in.ExcludedDirs)
	scopes := s.loadScopes()
	key := strconv.FormatInt(in.StrmTaskID, 10)
	if len(dirs) == 0 {
		delete(scopes, key)
	} else {
		scopes[key] = dirs
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return Scope{}, err
	}
	if s.settings != nil {
		if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmScrapeScopes: string(raw)}); err != nil {
			return Scope{}, err
		}
	}
	// 索引属于可重建数据；任务尚未生成输出目录时也允许预先保存范围。
	_ = s.RebuildIndex(ctx, in.StrmTaskID)
	return Scope{StrmTaskID: in.StrmTaskID, ExcludedDirs: dirs}, nil
}

func (s *Service) ListScopeDirectories(ctx context.Context, strmTaskID int64, parent string) ([]ScopeDirectory, error) {
	_, root, err := s.resolveTask(ctx, strmTaskID)
	if err != nil {
		return nil, err
	}
	parent = normalizeScopeDir(parent)
	current := root
	if parent != "" {
		current = filepath.Join(root, filepath.FromSlash(parent))
		if !isInside(root, current) {
			return nil, domain.Errorf(domain.CodeValidation, "非法目录")
		}
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		if os.IsNotExist(err) {
			return []ScopeDirectory{}, nil
		}
		return nil, err
	}
	out := make([]ScopeDirectory, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		rel := normalizeScopeDir(filepath.ToSlash(filepath.Join(parent, entry.Name())))
		item := ScopeDirectory{ID: rel, Name: entry.Name()}
		if info, infoErr := entry.Info(); infoErr == nil {
			item.ModTime = info.ModTime().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func filterWorksByScope(works []workGroup, excluded []string) []workGroup {
	excluded = normalizeScopeDirs(excluded)
	if len(excluded) == 0 {
		return works
	}
	out := make([]workGroup, 0, len(works))
	for _, work := range works {
		rel := normalizeScopeDir(work.relKey)
		skip := false
		for _, dir := range excluded {
			if rel == dir || strings.HasPrefix(rel, dir+"/") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, work)
		}
	}
	return out
}
