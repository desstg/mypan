package recognition

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("recognition enhancer unavailable")

type File struct {
	SourceID     string `json:"source_id"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type Work struct {
	WorkID          string `json:"work_id"`
	Directory       string `json:"directory,omitempty"`
	MediaTypeHint   string `json:"media_type_hint,omitempty"`
	CandidateTitle  string `json:"candidate_title,omitempty"`
	CandidateYear   *int   `json:"candidate_year,omitempty"`
	CandidateSeason *int   `json:"candidate_season,omitempty"`
	Files           []File `json:"files"`
}

type BatchRequest struct {
	Works []Work `json:"works"`
}

type FileResult struct {
	SourceID string `json:"source_id"`
	Episode  *int   `json:"episode,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type WorkResult struct {
	WorkID        string       `json:"work_id"`
	Recognized    bool         `json:"recognized"`
	Title         string       `json:"title,omitempty"`
	OriginalTitle string       `json:"original_title,omitempty"`
	Year          *int         `json:"year,omitempty"`
	MediaType     string       `json:"media_type,omitempty"`
	Season        *int         `json:"season,omitempty"`
	Files         []FileResult `json:"files,omitempty"`
}

type BatchResult struct {
	Items  []WorkResult `json:"items"`
	Cached int          `json:"cached,omitempty"`
	Failed int          `json:"failed,omitempty"`
}

type BatchProgress struct {
	Total        int
	Completed    int
	Cached       int
	Failed       int
	CurrentChunk int
	TotalChunks  int
}

type ProgressFunc func(BatchProgress)

type Enhancer interface {
	Available() bool
	Enhance(context.Context, BatchRequest) (BatchResult, error)
}

type ProgressEnhancer interface {
	Enhancer
	EnhanceWithProgress(context.Context, BatchRequest, ProgressFunc) (BatchResult, error)
}
