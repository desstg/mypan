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

// GetScope 读某个数据源「排除哪些目录」的名单。
//
// ⚠️ 入参是 `scopeKey`（`wallSource.ScopeKey()`）而不是 taskID：
// 挂上来的本地目录也有自己的排除名单。**键沿用任务的裸十进制**，
// 所以已存的名单原样命中，不需要迁移。
func (s *Service) GetScope(scopeKey string) Scope {
	key := strings.TrimSpace(scopeKey)
	if key == "" {
		return Scope{}
	}
	dirs := normalizeScopeDirs(s.loadScopes()[key])
	// StrmTaskID 只有任务源才有意义；这里回填是为了让老的返回结构不变
	// （前端读的是 excluded_dirs，那个字段照常给）。
	id, _ := strconv.ParseInt(key, 10, 64)
	return Scope{StrmTaskID: id, ExcludedDirs: dirs}
}

// UpdateScope 保存名单。`in.StrmTaskID <= 0` 表示这是**本地目录**源 ——
// 那种情况下键由调用方通过 `in.ScopeKey` 给（见 Scope 结构）。
func (s *Service) UpdateScope(ctx context.Context, in Scope) (Scope, error) {
	key := strings.TrimSpace(in.ScopeKey)
	if key == "" {
		if in.StrmTaskID <= 0 {
			return Scope{}, domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
		}
		if _, _, err := s.resolveTask(ctx, in.StrmTaskID); err != nil {
			return Scope{}, err
		}
		key = strconv.FormatInt(in.StrmTaskID, 10)
	}
	dirs := normalizeScopeDirs(in.ExcludedDirs)
	scopes := s.loadScopes()
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
	// 用 `in` 自己还原一个最小数据源（不查库）——`RebuildIndex` 只需要 Root/MediaKind/Key。
	_ = s.RebuildIndex(ctx, &wallSrc{
		Kind:      scopeKindOf(key),
		Root:      in.Root,
		MediaKind: in.MediaKind,
		TaskID:    in.StrmTaskID,
		LocalID:   in.LocalDirID,
	})
	return Scope{StrmTaskID: in.StrmTaskID, ScopeKey: key, ExcludedDirs: dirs}, nil
}

// scopeKindOf 从 scopeKey 反推源类型（裸十进制 = 任务，`local-` 前缀 = 本地目录）。
func scopeKindOf(key string) string {
	if strings.HasPrefix(key, "local-") {
		return "local"
	}
	return "task"
}

// ListScopeDirectories 列某个数据源根下的一层子目录（「刮削范围」那棵树）。
//
// 入参是 `*wallSrc`：本地目录源也要能用这个选择器（它的根就是用户挂上来的那个目录）。
func (s *Service) ListScopeDirectories(ctx context.Context, ref WallSourceRef, parent string) ([]ScopeDirectory, error) {
	src, err := s.resolveSource(ctx, ref)
	if err != nil {
		return nil, err
	}
	root := src.Root
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
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
