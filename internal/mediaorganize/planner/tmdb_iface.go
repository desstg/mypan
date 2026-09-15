package planner

import (
	"context"
	"encoding/json"
)

type TMDBClient interface {
	ValidateConnection(ctx context.Context) bool
	Search(ctx context.Context, query string, year *int, mediaType string) ([]json.RawMessage, error)
	Lookup(ctx context.Context, tmdbID string, mediaType string) (json.RawMessage, error)
	FetchTVSeasons(ctx context.Context, tmdbID string) ([]json.RawMessage, error)
}
