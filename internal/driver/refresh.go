package driver

import (
	"context"
	"errors"

	"litepan/internal/domain"
)

type RefreshOutcome int

const (
	RefreshSuccess   RefreshOutcome = iota // 刷新成功
	RefreshRetryable                       // 网络超时等，可重试
	RefreshFatal                           // token 失效/账号被封，不可恢复
)

func (o RefreshOutcome) String() string {
	switch o {
	case RefreshSuccess:
		return "success"
	case RefreshRetryable:
		return "retryable"
	case RefreshFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// RefreshCaller 区分主动调度与被动兜底。
type RefreshCaller int

const (
	CallerActive  RefreshCaller = iota // 主动调度器
	CallerPassive                      // 被动请求拦截
)

// AuthRefresher 由支持认证刷新的驱动实现；公共层据此托管到认证调度器。
type AuthRefresher interface {
	RefreshAuth(ctx context.Context, caller RefreshCaller) (RefreshOutcome, error)
}

// ClassifyOAuthRefreshError 判断 OAuth 代理 refresh 失败是否不可恢复。
func ClassifyOAuthRefreshError(err error) RefreshOutcome {
	var refreshErr *AuthRefreshError
	if errors.As(err, &refreshErr) {
		return refreshErr.Outcome
	}
	if domain.IsAuthExpiredError(err) {
		return RefreshFatal
	}
	return RefreshRetryable
}

// AuthRefreshError 保留驱动的协议判断，初始化失败时也不丢失可重试/需重授权的区别。
type AuthRefreshError struct {
	Outcome RefreshOutcome
	Err     error
}

func (e *AuthRefreshError) Error() string { return e.Err.Error() }
func (e *AuthRefreshError) Unwrap() error { return e.Err }
