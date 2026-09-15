package domain

import "context"

type AccountRepository interface {
	Create(ctx context.Context, a *Account) (int64, error)
	Update(ctx context.Context, a *Account) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*Account, error)
	List(ctx context.Context) ([]*Account, error)
	SetDefault(ctx context.Context, id int64) error
	// NameTaken 检查名称是否已被其它账号占用（大小写不敏感；excludeID>0 时排除该账号）。
	NameTaken(ctx context.Context, name string, excludeID int64) (bool, error)
}

type AuthStateRepository interface {
	Get(ctx context.Context, accountID int64) (*AuthState, error)
	Upsert(ctx context.Context, s *AuthState) error
	Delete(ctx context.Context, accountID int64) error
}

type ConfigRepository interface {
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	Set(ctx context.Context, key, value string) error
	All(ctx context.Context) (map[string]string, error)
}

type NotificationRepository interface {
	Create(ctx context.Context, n *Notification) (int64, error)
	List(ctx context.Context, limit, offset int) ([]*Notification, error)
	UnreadCount(ctx context.Context) (int, error)
	MarkRead(ctx context.Context, id int64) error
	MarkAllRead(ctx context.Context) (int64, error)
	Delete(ctx context.Context, id int64) error
	DeleteAll(ctx context.Context) (int64, error)
	DeleteByRef(ctx context.Context, category string, refID int64) (int64, error)
}
