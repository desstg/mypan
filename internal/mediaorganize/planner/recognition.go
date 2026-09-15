package planner

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"litepan/internal/mediaorganize/recognition"
)

type deferredGroup struct {
	key           groupKey
	items         []batchEntry
	alignDefaults map[bucketKey]map[string]any
	request       recognition.Work
	fileIndexes   map[string]int
	matchReason   string
	skipReason    string
	fallbackMatch *tmdbMatchResult
}

func (p *Planner) deferForRecognition(
	key groupKey,
	items []batchEntry,
	alignDefaults map[bucketKey]map[string]any,
	matchReason string,
	skipReason string,
	fallbackMatch *tmdbMatchResult,
) bool {
	if p.applyingAI || p.recognition == nil || !p.recognition.Available() {
		return false
	}
	workIndex := len(p.deferred) + 1
	work := recognition.Work{
		WorkID:          fmt.Sprintf("work_%d", workIndex),
		Directory:       recognitionDirectory(key, items),
		MediaTypeHint:   key.mediaKind,
		CandidateTitle:  strings.TrimSpace(key.title),
		CandidateYear:   key.yearPtr(),
		CandidateSeason: key.seasonPtr(),
		Files:           make([]recognition.File, 0, len(items)),
	}
	fileIndexes := make(map[string]int, len(items))
	for i, entry := range items {
		sourceID := fmt.Sprintf("source_%d_%d", workIndex, i+1)
		fileIndexes[sourceID] = i
		work.Files = append(work.Files, recognition.File{
			SourceID:     sourceID,
			Name:         entry.item.Name,
			RelativePath: recognitionFilePath(entry),
			Size:         entry.item.Size,
		})
	}
	var fallback *tmdbMatchResult
	if fallbackMatch != nil {
		copy := *fallbackMatch
		fallback = &copy
	}
	p.deferred = append(p.deferred, deferredGroup{
		key:           key,
		items:         append([]batchEntry(nil), items...),
		alignDefaults: alignDefaults,
		request:       work,
		fileIndexes:   fileIndexes,
		matchReason:   matchReason,
		skipReason:    skipReason,
		fallbackMatch: fallback,
	})
	return true
}

func (p *Planner) runRecognitionEnhancement() error {
	if len(p.deferred) == 0 {
		return nil
	}
	deferred := p.deferred
	p.deferred = nil
	request := recognition.BatchRequest{Works: make([]recognition.Work, 0, len(deferred))}
	for _, group := range deferred {
		request.Works = append(request.Works, group.request)
	}
	p.log(fmt.Sprintf("[计划] AI 辅助识别 %d 个低置信作品组", len(deferred)))
	p.emitRecognitionProgress(recognition.BatchProgress{Total: len(deferred)})
	var response recognition.BatchResult
	var err error
	if enhancer, ok := p.recognition.(recognition.ProgressEnhancer); ok {
		response, err = enhancer.EnhanceWithProgress(p.ctx, request, p.emitRecognitionProgress)
	} else {
		response, err = p.recognition.Enhance(p.ctx, request)
	}
	if err != nil {
		if !errors.Is(err, recognition.ErrUnavailable) {
			p.log(fmt.Sprintf("[计划] AI 辅助识别失败，已按原规则降级: %v", err))
		}
		for i := range deferred {
			if finishErr := p.finishDeferred(&deferred[i]); finishErr != nil {
				return finishErr
			}
		}
		p.recordRecognitionDiagnostics(len(deferred), 0, response.Cached, len(deferred))
		return nil
	}

	results := make(map[string]recognition.WorkResult, len(response.Items))
	for _, item := range response.Items {
		results[item.WorkID] = item
	}
	recognized := 0
	for i := range deferred {
		group := &deferred[i]
		result, ok := results[group.request.WorkID]
		if !ok || !result.Recognized || strings.TrimSpace(result.Title) == "" {
			if err := p.finishDeferred(group); err != nil {
				return err
			}
			continue
		}
		if p.maxWorksPerRun > 0 && p.plannedWorkCount >= p.maxWorksPerRun {
			p.quotaReached = true
			p.finishDeferredAsSkippedWithReason(group, "已达到本次整理作品上限")
			continue
		}
		before := len(p.actions)
		p.applyingAI = true
		err := p.planRecognizedGroup(group, result)
		p.applyingAI = false
		if err != nil {
			return err
		}
		for actionIndex := before; actionIndex < len(p.actions); actionIndex++ {
			if p.actions[actionIndex].Metadata == nil {
				p.actions[actionIndex].Metadata = map[string]any{}
			}
			p.actions[actionIndex].Metadata["recognition_source"] = "ai"
			p.actions[actionIndex].Metadata["ai_original_title"] = result.OriginalTitle
		}
		if len(p.actions) > before {
			p.plannedWorkCount++
		}
		recognized++
	}
	p.log(fmt.Sprintf("[计划] AI 辅助识别完成: %d/%d 个作品组", recognized, len(deferred)))
	if response.Failed > 0 {
		p.log(fmt.Sprintf("[计划] %d 个作品组请求失败，已按原规则降级", response.Failed))
	}
	p.recordRecognitionDiagnostics(len(deferred), recognized, response.Cached, response.Failed)
	return nil
}

func (p *Planner) emitRecognitionProgress(state recognition.BatchProgress) {
	if p.progress == nil {
		return
	}
	groups, _ := p.diagnostics["groups"].([]map[string]any)
	p.progress(Progress{
		Stage:        "ai_recognition",
		ScannedDirs:  p.scannedDirs,
		ScannedFiles: p.scannedFiles,
		Groups:       len(groups),
		Actions:      len(p.actions),
		Skipped:      len(p.skippedItems),
		PlannedWorks: p.plannedWorkCount,
		MaxWorks:     p.maxWorksPerRun,
		AITotal:      state.Total,
		AICompleted:  state.Completed,
		AICached:     state.Cached,
		AIFailed:     state.Failed,
		AIChunk:      state.CurrentChunk,
		AIChunks:     state.TotalChunks,
	})
}

func (p *Planner) planRecognizedGroup(group *deferredGroup, result recognition.WorkResult) error {
	key := group.key
	key.title = strings.TrimSpace(result.Title)
	if result.MediaType == "movie" || result.MediaType == "tv" {
		key.mediaKind = result.MediaType
	}
	key.setYear(result.Year)
	key.setSeason(result.Season)
	items := append([]batchEntry(nil), group.items...)
	fileResults := make(map[string]recognition.FileResult, len(result.Files))
	for _, file := range result.Files {
		fileResults[file.SourceID] = file
	}
	for sourceID, index := range group.fileIndexes {
		if index < 0 || index >= len(items) {
			continue
		}
		items[index].fileParsed.Title = key.title
		items[index].fileParsed.Year = key.yearPtr()
		if key.mediaKind == "tv" {
			items[index].fileParsed.Type = "episode"
			if result.Season != nil {
				items[index].fileParsed.Season = result.Season
			}
		} else {
			items[index].fileParsed.Type = "movie"
		}
		if file, ok := fileResults[sourceID]; ok {
			if file.Episode != nil {
				items[index].fileParsed.Episode = file.Episode
				if items[index].fileParsed.Season == nil {
					season := 1
					items[index].fileParsed.Season = &season
				}
			}
			if file.Kind == "movie" {
				items[index].fileParsed.Type = "movie"
			}
		}
	}
	return p.planGroupWithMatch(key, items, group.alignDefaults, nil, false)
}

func (p *Planner) finishDeferred(group *deferredGroup) error {
	if group.fallbackMatch != nil {
		if p.maxWorksPerRun > 0 && p.plannedWorkCount >= p.maxWorksPerRun {
			p.quotaReached = true
			p.finishDeferredAsSkippedWithReason(group, "已达到本次整理作品上限")
			return nil
		}
		before := len(p.actions)
		if err := p.planGroupWithMatch(group.key, group.items, group.alignDefaults, group.fallbackMatch, false); err != nil {
			return err
		}
		if len(p.actions) > before {
			p.plannedWorkCount++
		}
		return nil
	}
	p.recordNeedsMatch(group.key, group.items, group.matchReason, nil)
	for _, entry := range group.items {
		p.skip(entry.item, group.skipReason)
	}
	return nil
}

func (p *Planner) finishDeferredAsSkippedWithReason(group *deferredGroup, reason string) {
	p.recordNeedsMatch(group.key, group.items, reason, nil)
	for _, entry := range group.items {
		p.skip(entry.item, reason)
	}
}

func (p *Planner) recordRecognitionDiagnostics(candidateCount, recognizedCount, cachedCount, failedCount int) {
	p.diagnostics["ai_recognition"] = map[string]any{
		"candidate_count":  candidateCount,
		"recognized_count": recognizedCount,
		"cached_count":     cachedCount,
		"failed_count":     failedCount,
	}
}

func recognitionDirectory(key groupKey, items []batchEntry) string {
	if len(items) > 0 && len(items[0].ancestors) > 0 {
		parts := make([]string, 0, len(items[0].ancestors))
		for _, ancestor := range items[0].ancestors {
			parts = append(parts, ancestor.Name)
		}
		return path.Join(parts...)
	}
	if strings.TrimSpace(key.dirName) != "" {
		return key.dirName
	}
	return "/"
}

func recognitionFilePath(entry batchEntry) string {
	parts := make([]string, 0, len(entry.ancestors)+1)
	for _, ancestor := range entry.ancestors {
		parts = append(parts, ancestor.Name)
	}
	parts = append(parts, entry.item.Name)
	return path.Join(parts...)
}
