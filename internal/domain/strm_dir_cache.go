package domain

import (
	"context"
	"time"
)

// StrmDirCacheEntry 是 STRM 清单模式使用的远端目录 pid→路径 缓存记录。
type StrmDirCacheEntry struct {
	AccountID  int64
	DirID      string
	DirPath    string
	LastSeenAt time.Time
}

// StrmDirCacheRepository 维护 pid→路径 翻译表，避免每次清单模式都调用目录详情接口。
type StrmDirCacheRepository interface {
	Get(ctx context.Context, accountID int64, dirID string) (string, bool, error)
	// GetBatch 批量读取；返回命中集合，缺失的由调用方反查。
	GetBatch(ctx context.Context, accountID int64, dirIDs []string) (map[string]string, error)
	// UpsertBatch 写入或更新路径，并刷新 last_seen_at。
	UpsertBatch(ctx context.Context, entries []StrmDirCacheEntry) error
	// ListByPathPrefix 返回某账号下 dir_path 等于 prefix 或以 prefix/ 开头的记录。
	ListByPathPrefix(ctx context.Context, accountID int64, prefix string) ([]StrmDirCacheEntry, error)
	// DeleteByIDs 删除某账号下指定 dir_id 集合的记录。
	DeleteByIDs(ctx context.Context, accountID int64, dirIDs []string) (int64, error)
	// DeleteByAccount 清空某账号的全部缓存（用于手动纠正路径漂移）。
	DeleteByAccount(ctx context.Context, accountID int64) (int64, error)
	// DeleteAll 清空全部账号缓存，返回删除条数。
	DeleteAll(ctx context.Context) (int64, error)
	// CountByAccount 返回某账号缓存条数。
	CountByAccount(ctx context.Context, accountID int64) (int64, error)
}
