package strm

import (
	"strings"

	"litepan/internal/domain"
)

func parseKeywordRules(text string) []string {
	text = strings.ReplaceAll(text, "；", ";")
	parts := strings.Split(text, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func matchesKeywordRules(name string, rules []string) bool {
	if len(rules) == 0 {
		return false
	}
	candidate := strings.ToLower(name)
	for _, rule := range rules {
		if strings.Contains(candidate, rule) {
			return true
		}
	}
	return false
}

func normalizeConflictPolicy(policy string) string {
	switch strings.TrimSpace(strings.ToLower(policy)) {
	case domain.StrmConflictSizeAsc, domain.StrmConflictNameAsc:
		return strings.TrimSpace(strings.ToLower(policy))
	case "quality_then_size":
		return domain.StrmConflictSizeDesc
	default:
		return domain.StrmConflictSizeDesc
	}
}
