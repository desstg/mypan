package classification

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrUnavailable = errors.New("classification enhancer unavailable")

// DetailLoader 由 Planner 注入，让分类增强可在需要时补齐 TMDB detail 级元数据。
type DetailLoader interface {
	Lookup(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error)
}

type Request struct {
	MediaType string
	TMDBID    string
	Raw       map[string]any
	Loader    DetailLoader
}

// Decision 只描述相对 move 目标根的分类建议，不携带网盘目录 ID，也不执行写操作。
type Decision struct {
	Applied          bool           `json:"applied"`
	Matched          bool           `json:"matched"`
	Template         string         `json:"template,omitempty"`
	Category         string         `json:"category,omitempty"`
	RelativeSegments []string       `json:"relative_segments,omitempty"`
	Evidence         map[string]any `json:"evidence,omitempty"`
	DegradedReason   string         `json:"degraded_reason,omitempty"`
}

type Enhancer interface {
	Available() bool
	Classify(ctx context.Context, req Request) (Decision, error)
}
