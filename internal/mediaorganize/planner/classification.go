package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"litepan/internal/mediaorganize/classification"
)

func (p *Planner) classifyGroup(mediaType, tmdbID string, raw map[string]any) classification.Decision {
	if p.actionType != "move" || p.classification == nil || !p.classification.Available() {
		return classification.Decision{}
	}
	decision, err := p.classification.Classify(p.ctx, classification.Request{
		MediaType: mediaType,
		TMDBID:    tmdbID,
		Raw:       raw,
		Loader:    p,
	})
	if err != nil {
		if !errors.Is(err, classification.ErrUnavailable) {
			p.log(fmt.Sprintf("[计划] 分类整理降级：%v", err))
			p.recordClassificationDegraded("classification_error")
			return classification.Decision{Applied: true, DegradedReason: "classification_error"}
		}
		return classification.Decision{}
	}
	if decision.Applied {
		if decision.Matched {
			p.log(fmt.Sprintf("[计划] 分类命中：%s -> %s", decision.Template, strings.Join(decision.RelativeSegments, "/")))
		} else {
			p.recordClassificationDegraded(decision.DegradedReason)
			p.log(fmt.Sprintf("[计划] 无法归类，使用 move 目标根：%s", decision.DegradedReason))
		}
	}
	return decision
}

// Lookup 实现 classification.DetailLoader，统一复用 Planner 的 TMDB 请求间隔。
func (p *Planner) Lookup(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error) {
	if p.tmdb == nil {
		return nil, fmt.Errorf("TMDB 客户端不可用")
	}
	payload, err := p.tmdb.Lookup(ctx, tmdbID, mediaType)
	p.sleepTMDB()
	return payload, err
}

func (p *Planner) recordClassificationDegraded(reason string) {
	if p.diagnostics == nil {
		return
	}
	count, _ := p.diagnostics["classification_degraded_count"].(int)
	p.diagnostics["classification_degraded_count"] = count + 1
	if strings.TrimSpace(reason) != "" {
		reasons, _ := p.diagnostics["classification_degraded_reasons"].(map[string]int)
		if reasons == nil {
			reasons = make(map[string]int)
		}
		reasons[reason]++
		p.diagnostics["classification_degraded_reasons"] = reasons
	}
}

func (p *Planner) classificationParentRef(decision classification.Decision) string {
	parentRef := p.targetRootID
	if parentRef == "" {
		parentRef = p.parentID
	}
	if !decision.Matched {
		return parentRef
	}
	for _, segment := range decision.RelativeSegments {
		name := strings.TrimSpace(segment)
		if name != "" {
			parentRef = p.ensureDirAction(parentRef, name)
		}
	}
	return parentRef
}

func classificationMetadata(decision classification.Decision) map[string]any {
	if !decision.Applied {
		return nil
	}
	return map[string]any{
		"classification_applied":         true,
		"classification_matched":         decision.Matched,
		"classification_template":        decision.Template,
		"classification_category":        decision.Category,
		"classification_relative_path":   strings.Join(decision.RelativeSegments, "/"),
		"classification_evidence":        decision.Evidence,
		"classification_degraded_reason": decision.DegradedReason,
	}
}
