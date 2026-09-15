package file

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"litepan/internal/domain"
	mrules "litepan/internal/mediaorganize/rules"
)

type NameAlignPreviewInput struct {
	AccountID    int64
	ParentID     string
	TargetFileID string
	SampleFileID string
}

type NameAlignApplyInput struct {
	AccountID       int64
	ParentID        string
	TargetFileID    string
	SampleFileID    string
	SelectedFileIDs []string
}

type NameAlignPreview struct {
	Target           NameAlignPreviewItem   `json:"target"`
	Sample           NameAlignSample        `json:"sample"`
	SampleCandidates []NameAlignSample      `json:"sample_candidates"`
	Suspects         []NameAlignPreviewItem `json:"suspects"`
}

type NameAlignSample struct {
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	PatternLabel string `json:"pattern_label"`
}

type NameAlignPreviewItem struct {
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	NewName     string `json:"new_name"`
	Episode     int    `json:"episode"`
	Season      *int   `json:"season,omitempty"`
	PatternHint string `json:"pattern_hint"`
}

type NameAlignApplyResult struct {
	Renamed []NameAlignRenameResult `json:"renamed"`
}

type NameAlignRenameResult struct {
	FileID  string `json:"file_id"`
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

type alignMeta struct {
	episode int
	season  *int
}

type alignTemplate struct {
	style        string
	patternLabel string
	prefix       string
	suffix       string
	episodeWidth int
}

type alignAnalyzedFile struct {
	item      domain.FileItem
	meta      alignMeta
	template  *alignTemplate
	signature string
	score     int
}

var (
	alignSxeRe             = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,4})`)
	alignCnSeasonEpisodeRe = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百]+)\s*([季部])\s*第\s*([0-9零〇一二两三四五六七八九十百]+)\s*([集话話回期])`)
	alignCnEpisodeRe       = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百]+)\s*([集话話回期])`)
	alignEpisodeOnlyRe     = regexp.MustCompile(`(?i)\b(EP|E)\s*(\d{1,4})\b`)
	alignBracketEpisodeRe  = regexp.MustCompile(`([\[【])(\d{1,4})([\]】])`)
	alignDigitRe           = regexp.MustCompile(`\d+`)
	alignMediaExtSet       = map[string]struct{}{
		".mp4": {}, ".mkv": {}, ".avi": {}, ".mov": {}, ".wmv": {}, ".flv": {}, ".ts": {},
		".m2ts": {}, ".mpg": {}, ".mpeg": {}, ".rmvb": {}, ".rm": {}, ".iso": {}, ".webm": {},
	}
)

func (s *Service) PreviewNameAlign(ctx context.Context, in NameAlignPreviewInput) (*NameAlignPreview, error) {
	preview, _, err := s.buildNameAlignPreview(ctx, in)
	return preview, err
}

func (s *Service) ApplyNameAlign(ctx context.Context, in NameAlignApplyInput) (*NameAlignApplyResult, error) {
	preview, analyzed, err := s.buildNameAlignPreview(ctx, NameAlignPreviewInput{
		AccountID:    in.AccountID,
		ParentID:     in.ParentID,
		TargetFileID: in.TargetFileID,
		SampleFileID: in.SampleFileID,
	})
	if err != nil {
		return nil, err
	}

	if len(in.SelectedFileIDs) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请至少选择一个待重命名文件")
	}

	selectedSet := make(map[string]struct{}, len(in.SelectedFileIDs))
	for _, id := range in.SelectedFileIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selectedSet[id] = struct{}{}
		}
	}
	if _, ok := selectedSet[preview.Target.FileID]; !ok {
		return nil, domain.Errorf(domain.CodeValidation, "当前文件必须参与命名对齐")
	}

	previewByID := map[string]NameAlignPreviewItem{
		preview.Target.FileID: preview.Target,
	}
	for _, item := range preview.Suspects {
		previewByID[item.FileID] = item
	}

	renames := make([]NameAlignPreviewItem, 0, len(selectedSet))
	for id := range selectedSet {
		item, ok := previewByID[id]
		if !ok {
			return nil, domain.Errorf(domain.CodeValidation, "包含无效的待重命名文件")
		}
		renames = append(renames, item)
	}

	existingByName := make(map[string]string, len(analyzed))
	selectedOldNames := make(map[string]string, len(renames))
	for _, item := range analyzed {
		existingByName[strings.ToLower(item.item.Name)] = item.item.ID
	}
	for _, item := range renames {
		selectedOldNames[strings.ToLower(item.FileName)] = item.FileID
	}

	targetNames := make(map[string]string, len(renames))
	for _, item := range renames {
		key := strings.ToLower(item.NewName)
		if owner, exists := targetNames[key]; exists && owner != item.FileID {
			return nil, domain.Errorf(domain.CodeValidation, "命名对齐结果存在重名冲突：%s", item.NewName)
		}
		targetNames[key] = item.FileID
		if owner, exists := existingByName[key]; exists && owner != item.FileID {
			if _, selected := selectedOldNames[key]; !selected {
				return nil, domain.Errorf(domain.CodeValidation, "目标文件名已存在：%s", item.NewName)
			}
			if owner != item.FileID {
				return nil, domain.Errorf(domain.CodeValidation, "命名对齐结果与已选文件旧名称冲突：%s", item.NewName)
			}
		}
	}

	sort.Slice(renames, func(i, j int) bool {
		return renames[i].Episode < renames[j].Episode
	})

	result := &NameAlignApplyResult{
		Renamed: make([]NameAlignRenameResult, 0, len(renames)),
	}
	for _, item := range renames {
		if err := s.RenameFile(ctx, in.AccountID, item.FileID, item.NewName, in.ParentID); err != nil {
			return nil, err
		}
		result.Renamed = append(result.Renamed, NameAlignRenameResult{
			FileID:  item.FileID,
			OldName: item.FileName,
			NewName: item.NewName,
		})
	}
	return result, nil
}

func (s *Service) buildNameAlignPreview(ctx context.Context, in NameAlignPreviewInput) (*NameAlignPreview, []alignAnalyzedFile, error) {
	items, err := s.List(ctx, in.AccountID, in.ParentID, false)
	if err != nil {
		return nil, nil, err
	}

	files := make([]domain.FileItem, 0, len(items))
	for _, item := range items {
		if item.IsDir {
			continue
		}
		files = append(files, item)
	}
	if len(files) < 3 {
		return nil, nil, domain.Errorf(domain.CodeValidation, "当前目录至少需要 3 个文件才可使用命名对齐")
	}

	targetIndex := -1
	for i, item := range files {
		if item.ID == in.TargetFileID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return nil, nil, domain.Errorf(domain.CodeValidation, "未找到当前要对齐的文件")
	}

	analyzed := make([]alignAnalyzedFile, 0, len(files))
	for _, item := range files {
		if !isMediaName(item.Name) {
			continue
		}
		meta, ok := extractAlignMeta(item.Name)
		if !ok {
			continue
		}
		tpl := buildAlignTemplate(item.Name, meta)
		if tpl == nil {
			continue
		}
		analyzed = append(analyzed, alignAnalyzedFile{
			item:      item,
			meta:      meta,
			template:  tpl,
			signature: tpl.signature(meta),
		})
	}

	if len(analyzed) == 0 {
		return nil, nil, domain.Errorf(domain.CodeValidation, "当前目录未识别到可用于命名对齐的剧集文件")
	}

	var target *alignAnalyzedFile
	for i := range analyzed {
		if analyzed[i].item.ID == in.TargetFileID {
			target = &analyzed[i]
			break
		}
	}
	if target == nil {
		return nil, nil, domain.Errorf(domain.CodeValidation, "当前文件未识别出有效集号，无法命名对齐")
	}

	sampleCandidates := make([]alignAnalyzedFile, 0, len(analyzed))
	for _, item := range analyzed {
		if item.item.ID == target.item.ID {
			continue
		}
		newName, ok := item.template.build(target.meta, path.Ext(target.item.Name))
		if !ok || strings.EqualFold(newName, target.item.Name) {
			continue
		}
		item.score = sampleScore(item, target.signature)
		sampleCandidates = append(sampleCandidates, item)
	}
	if len(sampleCandidates) == 0 {
		return nil, nil, domain.Errorf(domain.CodeValidation, "当前目录未识别到可作为参考样本的文件")
	}

	sort.Slice(sampleCandidates, func(i, j int) bool {
		return betterNameAlignSampleOrder(sampleCandidates[i], sampleCandidates[j])
	})
	sampleCandidates = uniqueNameAlignSampleCandidates(sampleCandidates)

	chosen := sampleCandidates[0]
	if sampleID := strings.TrimSpace(in.SampleFileID); sampleID != "" {
		found := false
		for _, item := range sampleCandidates {
			if item.item.ID == sampleID {
				chosen = item
				found = true
				break
			}
		}
		if !found {
			return nil, nil, domain.Errorf(domain.CodeValidation, "未找到指定的参考样本")
		}
	}

	targetName, ok := chosen.template.build(target.meta, path.Ext(target.item.Name))
	if !ok || strings.EqualFold(targetName, target.item.Name) {
		return nil, nil, domain.Errorf(domain.CodeValidation, "参考样本无法为当前文件生成新名称")
	}

	suspects := make([]NameAlignPreviewItem, 0)
	for _, item := range analyzed {
		if item.item.ID == target.item.ID || item.item.ID == chosen.item.ID {
			continue
		}
		if item.signature != target.signature {
			continue
		}
		newName, ok := chosen.template.build(item.meta, path.Ext(item.item.Name))
		if !ok || strings.EqualFold(newName, item.item.Name) {
			continue
		}
		suspects = append(suspects, NameAlignPreviewItem{
			FileID:      item.item.ID,
			FileName:    item.item.Name,
			NewName:     newName,
			Episode:     item.meta.episode,
			Season:      item.meta.season,
			PatternHint: item.template.patternLabel,
		})
	}
	sort.Slice(suspects, func(i, j int) bool {
		return suspects[i].Episode < suspects[j].Episode
	})

	resp := &NameAlignPreview{
		Target: NameAlignPreviewItem{
			FileID:      target.item.ID,
			FileName:    target.item.Name,
			NewName:     targetName,
			Episode:     target.meta.episode,
			Season:      target.meta.season,
			PatternHint: target.template.patternLabel,
		},
		Sample: NameAlignSample{
			FileID:       chosen.item.ID,
			FileName:     chosen.item.Name,
			PatternLabel: chosen.template.patternLabel,
		},
		SampleCandidates: make([]NameAlignSample, 0, len(sampleCandidates)),
		Suspects:         suspects,
	}
	for _, item := range sampleCandidates {
		resp.SampleCandidates = append(resp.SampleCandidates, NameAlignSample{
			FileID:       item.item.ID,
			FileName:     item.item.Name,
			PatternLabel: item.template.patternLabel,
		})
	}
	return resp, analyzed, nil
}

func uniqueNameAlignSampleCandidates(items []alignAnalyzedFile) []alignAnalyzedFile {
	if len(items) <= 1 {
		return items
	}
	bestBySignature := make(map[string]alignAnalyzedFile, len(items))
	for _, item := range items {
		current, ok := bestBySignature[item.signature]
		if !ok || betterNameAlignSample(item, current) {
			bestBySignature[item.signature] = item
		}
	}
	out := make([]alignAnalyzedFile, 0, len(bestBySignature))
	for _, item := range bestBySignature {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return betterNameAlignSampleOrder(out[i], out[j])
	})
	return out
}

func betterNameAlignSampleOrder(candidate, current alignAnalyzedFile) bool {
	if candidate.meta.episode != current.meta.episode {
		return candidate.meta.episode > current.meta.episode
	}
	if candidate.score != current.score {
		return candidate.score > current.score
	}
	return strings.ToLower(candidate.item.Name) < strings.ToLower(current.item.Name)
}

func betterNameAlignSample(candidate, current alignAnalyzedFile) bool {
	if candidate.meta.episode != current.meta.episode {
		return candidate.meta.episode > current.meta.episode
	}
	if candidate.score != current.score {
		return candidate.score > current.score
	}
	return strings.ToLower(candidate.item.Name) < strings.ToLower(current.item.Name)
}

func isMediaName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	_, ok := alignMediaExtSet[ext]
	return ok
}

func extractAlignMeta(name string) (alignMeta, bool) {
	out := alignMeta{}
	parsed := mrules.ParseFilenameWithGuessit(name)
	parsed = mrules.ApplyEpisodeFallbacks(name, parsed)
	episode := asAlignInt(parsed["episode"])
	if episode == nil {
		return out, false
	}
	out.episode = *episode
	out.season = asAlignInt(parsed["season"])
	return out, true
}

func asAlignInt(v any) *int {
	switch t := v.(type) {
	case int:
		n := t
		return &n
	case int32:
		n := int(t)
		return &n
	case int64:
		n := int(t)
		return &n
	case float64:
		n := int(t)
		return &n
	case string:
		return mrules.ParseEpisodeNumber(t)
	default:
		return nil
	}
}

// episodeLocator 描述一种集号写法：用 re 匹配，epGroup 指向集号数字所在的捕获组。
type episodeLocator struct {
	style   string
	label   string
	re      *regexp.Regexp
	epGroup int
}

var alignLocators = []episodeLocator{
	{"sxe", "SxxEyy", alignSxeRe, 2},
	{"cn-season-episode", "第X季第Y集", alignCnSeasonEpisodeRe, 3},
	{"cn-episode", "第Y集", alignCnEpisodeRe, 1},
	{"episode-only", "EPyy", alignEpisodeOnlyRe, 2},
	{"bracket", "[yy]", alignBracketEpisodeRe, 2},
}

func buildAlignTemplate(name string, meta alignMeta) *alignTemplate {
	stem := strings.TrimSuffix(name, path.Ext(name))
	for _, l := range alignLocators {
		if tpl := locateEpisodeTemplate(stem, meta, l); tpl != nil {
			return tpl
		}
	}
	return buildBareTemplate(stem, meta)
}

func locateEpisodeTemplate(stem string, meta alignMeta, l episodeLocator) *alignTemplate {
	for _, loc := range l.re.FindAllStringSubmatchIndex(stem, -1) {
		start, end := loc[2*l.epGroup], loc[2*l.epGroup+1]
		if start < 0 {
			continue
		}
		epStr := stem[start:end]
		episode := mrules.ParseEpisodeNumber(epStr)
		if episode == nil || *episode != meta.episode {
			continue
		}
		// 中文集号不反向重建
		width := 0
		if isASCIIDigits(epStr) {
			width = len(epStr)
		}
		label := l.label
		if l.style == "episode-only" {
			label = strings.ToUpper(stem[loc[2]:loc[3]]) + "yy"
		}
		return &alignTemplate{
			style:        l.style,
			patternLabel: label,
			prefix:       stem[:start],
			suffix:       stem[end:],
			episodeWidth: width,
		}
	}
	return nil
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func buildBareTemplate(stem string, meta alignMeta) *alignTemplate {
	locs := alignDigitRe.FindAllStringIndex(stem, -1)
	matches := make([][2]int, 0, len(locs))
	for _, loc := range locs {
		value := mrules.ParseEpisodeNumber(stem[loc[0]:loc[1]])
		if value == nil || *value != meta.episode {
			continue
		}
		matches = append(matches, [2]int{loc[0], loc[1]})
	}
	if len(matches) == 0 {
		return nil
	}
	loc, ok := pickBareEpisodeMatch(stem, matches)
	if !ok {
		return nil
	}
	return &alignTemplate{
		style:        "bare",
		prefix:       stem[:loc[0]],
		suffix:       stem[loc[1]:],
		patternLabel: "纯数字集号",
		episodeWidth: loc[1] - loc[0],
	}
}

func pickBareEpisodeMatch(stem string, matches [][2]int) ([2]int, bool) {
	if len(matches) == 1 {
		return matches[0], true
	}
	for _, loc := range matches {
		if isLeadingBareEpisodeMatch(stem, loc) {
			return loc, true
		}
	}
	return [2]int{}, false
}

func isLeadingBareEpisodeMatch(stem string, loc [2]int) bool {
	if strings.TrimSpace(stem[:loc[0]]) != "" {
		return false
	}
	if loc[1]-loc[0] > 3 {
		return false
	}
	if loc[1] >= len(stem) {
		return true
	}
	rest := stem[loc[1]:]
	for _, sep := range []string{".", " ", "_", "-", "[", "【", "(", "（"} {
		if strings.HasPrefix(rest, sep) {
			return true
		}
	}
	return false
}

func (t *alignTemplate) signature(meta alignMeta) string {
	return strings.Join([]string{
		t.style,
		normalizeAlignSignaturePart(t.prefix, meta),
		normalizeAlignSignaturePart(t.suffix, meta),
	}, "|")
}

// normalizeAlignSignaturePart 将同一文件名中重复出现的集号统一视为变量。
func normalizeAlignSignaturePart(part string, meta alignMeta) string {
	spans := make([][2]int, 0, 2)
	seen := make(map[[2]int]struct{}, 2)
	for _, locator := range alignLocators {
		for _, loc := range locator.re.FindAllStringSubmatchIndex(part, -1) {
			group := 2 * locator.epGroup
			if group+1 >= len(loc) {
				continue
			}
			start, end := loc[group], loc[group+1]
			if start < 0 || end <= start {
				continue
			}
			episode := mrules.ParseEpisodeNumber(part[start:end])
			if episode == nil || *episode != meta.episode {
				continue
			}
			span := [2]int{start, end}
			if _, ok := seen[span]; ok {
				continue
			}
			seen[span] = struct{}{}
			spans = append(spans, span)
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i][0] > spans[j][0]
	})
	for _, span := range spans {
		part = part[:span[0]] + "{episode}" + part[span[1]:]
	}
	return normalizeAlignPart(part)
}

// build 用目标集号替换样本集号数字串，样本其余字面（含季号）原样保留。
func (t *alignTemplate) build(meta alignMeta, ext string) (string, bool) {
	if t == nil {
		return "", false
	}
	return t.prefix + zeroPad(meta.episode, t.episodeWidth) + t.suffix + ext, true
}

func sampleScore(item alignAnalyzedFile, targetSignature string) int {
	score := 0
	switch item.template.style {
	case "sxe":
		score += 500
	case "cn-season-episode":
		score += 450
	case "cn-episode":
		score += 380
	case "episode-only":
		score += 320
	case "bracket":
		score += 280
	default:
		score += 180
	}
	if item.signature != targetSignature {
		score += 80
	}
	if strings.TrimSpace(item.template.prefix) != "" {
		score += 40
	}
	if strings.TrimSpace(item.template.suffix) != "" {
		score += 20
	}
	score += len([]rune(item.item.Name))
	return score
}

func normalizeAlignPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ", "　", " ")
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func zeroPad(v, width int) string {
	if width <= 1 {
		return strconv.Itoa(v)
	}
	return leftPadNumber(v, width)
}

func leftPadNumber(v, width int) string {
	s := strconv.Itoa(v)
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}
