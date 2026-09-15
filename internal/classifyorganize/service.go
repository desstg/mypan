package classifyorganize

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"litepan/internal/mediaorganize/classification"
	"litepan/internal/settings"
)

const (
	detailCacheTTL = 30 * time.Minute
	maxDetailCache = 256
)

type detailCacheEntry struct {
	raw       map[string]any
	expiresAt time.Time
}

type Service struct {
	settings *settings.Service

	mu    sync.Mutex
	cache map[string]detailCacheEntry
}

type evaluationState struct {
	req             classification.Request
	raw             map[string]any
	detailAttempted bool
	detailLoaded    bool
	degradedReason  string
	evaluatedField  string
	evaluatedValues []string
	evaluated       map[string][]string
}

type customCandidate struct {
	segments    []string
	conditions  []parsedCondition
	expressions []string
}

type customMatchScore struct {
	specificity       int
	genreConditions   int
	regionConditions  int
	genreValueIndex   int
	regionValueIndex  int
	otherValueIndexes int
}

func New(settingsSvc *settings.Service) *Service {
	return &Service{settings: settingsSvc, cache: make(map[string]detailCacheEntry)}
}

func (s *Service) Available() bool {
	if s == nil || s.settings == nil || !s.settings.Bool(settings.KeyMOClassificationEnabled) {
		return false
	}
	_, err := normalizeConfig(s.Config())
	return err == nil
}

func (s *Service) Classify(ctx context.Context, req classification.Request) (classification.Decision, error) {
	if !s.Available() {
		return classification.Decision{}, classification.ErrUnavailable
	}
	cfg := s.Config()
	tpl, ok := findTemplate(cfg, cfg.SelectedTemplate)
	if !ok {
		return classification.Decision{}, classification.ErrUnavailable
	}
	decision := classification.Decision{Applied: true, Template: tpl.Kind}
	state := evaluationState{req: req, raw: req.Raw, evaluated: make(map[string][]string)}
	if tpl.Kind == TemplateCustom {
		return s.classifyCustom(ctx, decision, &state, tpl.Rules)
	}

	parent, matched, err := s.firstMatchingRule(ctx, &state, tpl.Rules)
	if err != nil {
		return classification.Decision{}, err
	}
	if matched {
		if len(parent.Children) == 0 {
			decision.Matched = true
			decision.Category = parent.Name
			decision.RelativeSegments = []string{parent.Name}
			decision.Evidence = matchedEvidence(state, []string{parent.Condition})
			return decision, nil
		}
		child, childMatched, err := s.firstMatchingRule(ctx, &state, parent.Children)
		if err != nil {
			return classification.Decision{}, err
		}
		if childMatched {
			decision.Matched = true
			decision.Category = child.Name
			decision.RelativeSegments = []string{parent.Name, child.Name}
			decision.Evidence = matchedEvidence(state, []string{parent.Condition, child.Condition})
			return decision, nil
		}
		decision.Matched = true
		decision.RelativeSegments = fallbackSegments(parent)
		decision.Category = decision.RelativeSegments[len(decision.RelativeSegments)-1]
		decision.Evidence = unmatchedEvidence(state, parent.Condition)
		decision.Evidence["fallback"] = true
		return decision, nil
	}

	decision.DegradedReason = fallbackReason(state.degradedReason)
	decision.Evidence = unmatchedEvidence(state, "")
	return decision, nil
}

func (s *Service) classifyCustom(ctx context.Context, decision classification.Decision, state *evaluationState, rules []Rule) (classification.Decision, error) {
	candidates, err := buildCustomCandidates(rules)
	if err != nil {
		return classification.Decision{}, err
	}
	bestIndex := -1
	bestScore := customMatchScore{specificity: -1}
	ambiguous := false
	for index, candidate := range candidates {
		matched, score, err := s.customCandidateMatches(ctx, state, candidate)
		if err != nil {
			return classification.Decision{}, err
		}
		if !matched {
			continue
		}
		switch compareCustomMatchScore(score, bestScore) {
		case 1:
			bestIndex = index
			bestScore = score
			ambiguous = false
		case 0:
			ambiguous = bestIndex >= 0
		}
	}
	if bestIndex < 0 || ambiguous {
		if ambiguous {
			state.degradedReason = "ambiguous_rule_matched"
		}
		decision.DegradedReason = fallbackReason(state.degradedReason)
		decision.Evidence = unmatchedEvidence(*state, "")
		return decision, nil
	}
	best := candidates[bestIndex]
	decision.Matched = true
	decision.Category = best.segments[len(best.segments)-1]
	decision.RelativeSegments = append([]string(nil), best.segments...)
	decision.Evidence = map[string]any{
		"conditions":    append([]string(nil), best.expressions...),
		"fields":        cloneEvaluatedFields(state.evaluated),
		"specificity":   bestScore.specificity,
		"match_policy":  "specificity_genres_region_tmdb_order",
		"detail_loaded": state.detailLoaded,
	}
	return decision, nil
}

func buildCustomCandidates(rules []Rule) ([]customCandidate, error) {
	candidates := make([]customCandidate, 0)
	var walk func([]Rule, []string, []parsedCondition, []string) error
	walk = func(items []Rule, parentSegments []string, parentConditions []parsedCondition, parentExpressions []string) error {
		for _, rule := range items {
			conditions, err := parseExpression(rule.Condition)
			if err != nil {
				return err
			}
			segments := append(append([]string(nil), parentSegments...), rule.Name)
			pathConditions := append(append([]parsedCondition(nil), parentConditions...), conditions...)
			expressions := append(append([]string(nil), parentExpressions...), rule.Condition)
			if len(rule.Children) == 0 {
				candidates = append(candidates, customCandidate{
					segments: segments, conditions: pathConditions, expressions: expressions,
				})
			} else {
				candidates = append(candidates, customCandidate{
					segments: fallbackSegments(rule), conditions: pathConditions, expressions: expressions,
				})
			}
			if len(rule.Children) > 0 {
				if err := walk(rule.Children, segments, pathConditions, expressions); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(rules, nil, nil, nil); err != nil {
		return nil, err
	}
	return candidates, nil
}

func fallbackSegments(rule Rule) []string {
	segments := []string{rule.Name}
	if rule.FallbackMode == "directory" && strings.TrimSpace(rule.FallbackDir) != "" {
		segments = append(segments, rule.FallbackDir)
	}
	return segments
}

func (s *Service) customCandidateMatches(ctx context.Context, state *evaluationState, candidate customCandidate) (bool, customMatchScore, error) {
	score := customMatchScore{
		specificity:      len(candidate.conditions),
		genreValueIndex:  int(^uint(0) >> 1),
		regionValueIndex: int(^uint(0) >> 1),
	}
	for _, condition := range candidate.conditions {
		actual := s.valuesForField(ctx, state, condition.Field)
		valueIndex := firstMatchingValueIndex(condition.Values, actual)
		if valueIndex < 0 {
			return false, customMatchScore{}, nil
		}
		switch condition.Field {
		case "genres":
			score.genreConditions++
			score.genreValueIndex = valueIndex
		case "origin_country":
			score.regionConditions++
			score.regionValueIndex = valueIndex
		case "type":
		default:
			score.otherValueIndexes += valueIndex
		}
	}
	return true, score, nil
}

func compareCustomMatchScore(left, right customMatchScore) int {
	comparisons := [][2]int{
		{left.specificity, right.specificity},
		{left.genreConditions, right.genreConditions},
		{left.regionConditions, right.regionConditions},
		{right.genreValueIndex, left.genreValueIndex},
		{right.regionValueIndex, left.regionValueIndex},
		{right.otherValueIndexes, left.otherValueIndexes},
	}
	for _, values := range comparisons {
		if values[0] > values[1] {
			return 1
		}
		if values[0] < values[1] {
			return -1
		}
	}
	return 0
}

func cloneEvaluatedFields(fields map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(fields))
	for field, values := range fields {
		cloned[field] = append([]string(nil), values...)
	}
	return cloned
}

func findTemplate(cfg Config, kind string) (Template, bool) {
	for _, tpl := range cfg.Templates {
		if tpl.Kind == kind {
			return tpl, true
		}
	}
	return Template{}, false
}

func (s *Service) firstMatchingRule(ctx context.Context, state *evaluationState, rules []Rule) (Rule, bool, error) {
	bestIndex := -1
	bestValueIndex := -1
	for index, rule := range rules {
		condition, err := parseCondition(rule.Condition)
		if err != nil {
			return Rule{}, false, err
		}
		actual := s.valuesForField(ctx, state, condition.Field)
		state.evaluatedField = condition.Field
		state.evaluatedValues = actual
		valueIndex := firstMatchingValueIndex(condition.Values, actual)
		if valueIndex < 0 || (bestValueIndex >= 0 && valueIndex >= bestValueIndex) {
			continue
		}
		bestIndex = index
		bestValueIndex = valueIndex
	}
	if bestIndex < 0 {
		return Rule{}, false, nil
	}
	return rules[bestIndex], true, nil
}

func (s *Service) valuesForField(ctx context.Context, state *evaluationState, field string) []string {
	if field == "type" {
		values := nonEmptyValues(state.req.MediaType)
		state.evaluated[field] = append([]string(nil), values...)
		return values
	}
	if raw, exists := state.raw[field]; exists {
		values := extractFieldValues(raw, field)
		state.evaluated[field] = append([]string(nil), values...)
		return values
	}
	if !state.detailAttempted {
		state.detailAttempted = true
		if strings.TrimSpace(state.req.TMDBID) == "" || state.req.Loader == nil {
			state.degradedReason = "tmdb_detail_unavailable"
			return nil
		}
		raw, err := s.loadDetail(ctx, state.req.Loader, state.req.TMDBID, state.req.MediaType)
		if err != nil {
			state.degradedReason = "tmdb_detail_failed"
			return nil
		}
		state.raw = raw
		state.detailLoaded = true
	}
	values := extractFieldValues(state.raw[field], field)
	state.evaluated[field] = append([]string(nil), values...)
	return values
}

func extractFieldValues(raw any, field string) []string {
	values := make([]string, 0)
	var appendValue func(any)
	appendValue = func(value any) {
		switch item := value.(type) {
		case nil:
			return
		case []any:
			for _, child := range item {
				appendValue(child)
			}
		case []string:
			for _, child := range item {
				appendValue(child)
			}
		case []map[string]any:
			for _, child := range item {
				appendValue(child)
			}
		case map[string]any:
			keys := []string{"name", "iso_3166_1", "iso_639_1", "code"}
			if field == "genres" {
				keys = []string{"name"}
			}
			for _, key := range keys {
				if child, ok := item[key]; ok {
					appendValue(child)
				}
			}
		default:
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				values = append(values, text)
			}
		}
	}
	appendValue(raw)
	return uniqueValues(values)
}

func nonEmptyValues(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func uniqueValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func firstMatchingValueIndex(expected, actual []string) int {
	for index, got := range actual {
		for _, want := range expected {
			if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got)) {
				return index
			}
		}
	}
	return -1
}

func fallbackReason(reason string) string {
	if reason != "" {
		return reason
	}
	return "no_rule_matched"
}

func matchedEvidence(state evaluationState, conditions []string) map[string]any {
	return map[string]any{
		"conditions":    conditions,
		"field":         state.evaluatedField,
		"values":        state.evaluatedValues,
		"detail_loaded": state.detailLoaded,
	}
}

func unmatchedEvidence(state evaluationState, parentCondition string) map[string]any {
	evidence := map[string]any{
		"field":         state.evaluatedField,
		"values":        state.evaluatedValues,
		"detail_loaded": state.detailLoaded,
	}
	if parentCondition != "" {
		evidence["parent_condition"] = parentCondition
	}
	return evidence
}

func (s *Service) loadDetail(ctx context.Context, loader classification.DetailLoader, tmdbID, mediaType string) (map[string]any, error) {
	key := strings.ToLower(strings.TrimSpace(mediaType)) + ":" + strings.TrimSpace(tmdbID)
	now := time.Now()
	s.mu.Lock()
	if entry, ok := s.cache[key]; ok && now.Before(entry.expiresAt) {
		s.mu.Unlock()
		return entry.raw, nil
	}
	s.mu.Unlock()

	payload, err := loader.Lookup(ctx, tmdbID, mediaType)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	if raw == nil || strings.TrimSpace(fmt.Sprint(raw["id"])) == "" {
		return nil, fmt.Errorf("TMDB detail 响应缺少影片标识")
	}
	s.mu.Lock()
	if len(s.cache) >= maxDetailCache {
		for cacheKey, entry := range s.cache {
			if now.After(entry.expiresAt) {
				delete(s.cache, cacheKey)
			}
		}
	}
	if len(s.cache) >= maxDetailCache {
		for cacheKey := range s.cache {
			delete(s.cache, cacheKey)
			break
		}
	}
	s.cache[key] = detailCacheEntry{raw: raw, expiresAt: now.Add(detailCacheTTL)}
	s.mu.Unlock()
	return raw, nil
}
