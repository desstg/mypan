package domain

import (
	"context"
	"time"
)

const (
	ApiKeyTypeTask     = "task"
	ApiKeyTypeReadonly = "readonly"

	ApiKeyStatusActive   = "active"
	ApiKeyStatusDisabled = "disabled"
)

type ApiKey struct {
	ID         int64
	Name       string
	KeyHash    string
	KeyPrefix  string
	KeySuffix  string
	KeyType    string
	Status     string
	ExpiresAt  time.Time
	LastUsedAt time.Time
	Note       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ApiKeyRepository interface {
	List(ctx context.Context) ([]*ApiKey, error)
	Get(ctx context.Context, id int64) (*ApiKey, error)
	GetByHash(ctx context.Context, keyHash string) (*ApiKey, error)
	Count(ctx context.Context) (int, error)
	Create(ctx context.Context, key *ApiKey) (int64, error)
	Update(ctx context.Context, key *ApiKey) error
	Delete(ctx context.Context, id int64) error
	TouchLastUsed(ctx context.Context, id int64, at time.Time) error
}
