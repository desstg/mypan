package planner

import (
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

type ManualMatchGroup struct {
	GroupUID  string
	MediaKind string
	DirID     string
	DirName   string
	Title     string
}

func (p *Planner) ReplanMatchedGroup(group ManualMatchGroup, raw map[string]any) (*moplan.Plan, error) {
	if strings.TrimSpace(group.GroupUID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "当前计划中未找到该作品组")
	}
	if len(raw) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "TMDB 影片信息缺失")
	}
	if p.useTMDB && p.tmdb != nil && p.tmdbAPIKey() != "" {
		p.tmdbAvailable = true
	}
	entries, err := p.collectEntriesForManualMatch(group)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "当前作品组未找到可重建的媒体文件")
	}
	groups, pending := p.groupEntries(entries)
	for _, ps := range pending {
		p.skip(ps.item, ps.reason)
	}
	alignDefaults := map[groupKey]map[bucketKey]map[string]any{}
	if p.alignMediaTags {
		alignDefaults = p.computeAlignDefaults(groups)
	}
	key, items, ok := locateManualMatchGroup(groups, group)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "当前计划中未找到该作品组")
	}
	bucketDefaults := alignDefaults[key]
	selectedKind := strings.ToLower(strings.TrimSpace(group.MediaKind))
	if selectedKind == "movie" || selectedKind == "tv" {
		// 手动选中的 TMDB 类型高于原计划的自动猜测。
		// 否则纯数字剧集先被猜成电影后，即使用户选中电视剧仍会按电影重建。
		key.mediaKind = selectedKind
	}
	p.recordManualMatchGroup(key, len(items))
	match := manualTMDBMatchResult(raw, key.mediaKind)
	if err := p.planGroupWithMatch(key, items, bucketDefaults, &match, false); err != nil {
		return nil, err
	}
	return p.finalize(), nil
}

func (p *Planner) collectEntriesForManualMatch(group ManualMatchGroup) ([]batchEntry, error) {
	if group.DirID != "" {
		return p.collectEntriesUnderDir(group.DirID, group.DirName)
	}
	items, err := p.listWithRetry(p.parentID)
	if err != nil {
		return nil, err
	}
	entries := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			entries = append(entries, batchEntry{item: item})
		}
	}
	return entries, nil
}

func (p *Planner) collectEntriesUnderDir(dirID, dirName string) ([]batchEntry, error) {
	items, err := p.listWithRetry(dirID)
	if err != nil {
		return nil, err
	}
	ancestors := []rules.Ancestor{{ID: dirID, Name: dirName}}
	p.recordDirMeta(ancestors)
	entries := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			entries = append(entries, batchEntry{item: item, ancestors: cloneAncestors(ancestors)})
		}
	}
	for _, child := range items {
		if !child.IsDir {
			continue
		}
		next := append(cloneAncestors(ancestors), rules.Ancestor{ID: child.ID, Name: child.Name})
		if err := p.collectDescendants(child.ID, next, &entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func locateManualMatchGroup(groups map[groupKey][]batchEntry, group ManualMatchGroup) (groupKey, []batchEntry, bool) {
	if len(groups) == 0 {
		return groupKey{}, nil, false
	}
	for key, items := range groups {
		if groupUIDOf(key) == group.GroupUID {
			return key, items, true
		}
	}
	for key, items := range groups {
		if group.DirID != "" && key.dirID == group.DirID && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	for key, items := range groups {
		if strings.TrimSpace(group.DirName) != "" && key.dirName == group.DirName && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	for key, items := range groups {
		if strings.TrimSpace(group.Title) != "" && strings.TrimSpace(key.title) == strings.TrimSpace(group.Title) && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	if len(groups) == 1 {
		for key, items := range groups {
			return key, items, true
		}
	}
	return groupKey{}, nil, false
}

func sameMediaKind(a, b string) bool {
	a = strings.TrimSpace(strings.ToLower(a))
	b = strings.TrimSpace(strings.ToLower(b))
	if a == "" || b == "" {
		return true
	}
	return a == b
}

func manualTMDBMatchResult(raw map[string]any, mediaKind string) tmdbMatchResult {
	groupMediaType := "movie"
	if strings.TrimSpace(strings.ToLower(mediaKind)) == "tv" {
		groupMediaType = "tv"
	}
	tmdbID, tmdbTitle, tmdbOriginal, tmdbYear := rules.ExtractTMDBDisplayFields(raw, groupMediaType)
	title := tmdbTitle
	if title == "" {
		title = tmdbOriginal
	}
	return tmdbMatchResult{
		tmdbID:       tmdbID,
		tmdbTitle:    tmdbTitle,
		tmdbOriginal: tmdbOriginal,
		title:        title,
		year:         tmdbYear,
		raw:          raw,
		confidence:   0.99,
	}
}

func (p *Planner) recordManualMatchGroup(key groupKey, count int) {
	p.diagnostics["groups"] = []map[string]any{{
		"media_kind": key.mediaKind,
		"dir_id":     key.dirID,
		"dir_name":   key.dirName,
		"title":      key.title,
		"count":      count,
		"group_uid":  groupUIDOf(key),
	}}
	p.log(fmt.Sprintf("[计划] 手动匹配重建作品组: 目录=%q | 标题=%q | %d个文件", key.dirName, key.title, count))
}
