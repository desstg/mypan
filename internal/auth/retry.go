package auth

import (
	"context"

	"litepan/internal/domain"
)

func IsAuthError(err error) bool {
	return domain.IsAuthExpiredError(err)
}

func WithRetry(ctx context.Context, gate GateChecker, accountID int64, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if gate == nil || !IsAuthError(err) {
		return err
	}
	if rerr := gate.HandlePassiveError(ctx, accountID); rerr != nil {
		return rerr
	}
	err = fn()
	if reporter, ok := gate.(interface {
		HandleRetryFailure(context.Context, int64, error)
	}); ok && IsAuthError(err) {
		reporter.HandleRetryFailure(ctx, accountID, err)
	}
	return err
}
