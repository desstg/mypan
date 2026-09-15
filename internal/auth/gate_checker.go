package auth

import "context"

// GateChecker 是文件层等调用方依赖的最小认证闸门接口。
type GateChecker interface {
	Check(ctx context.Context, accountID int64) error
	HandlePassiveError(ctx context.Context, accountID int64) error
}
