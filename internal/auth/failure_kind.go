package auth

import (
	"litepan/internal/domain"
	"litepan/internal/driver"
)

func classifyFailureKind(outcome driver.RefreshOutcome, cause error) domain.AuthFailureKind {
	if outcome == driver.RefreshFatal {
		return domain.AuthFailureAuth
	}
	if domain.IsNetworkError(cause) {
		return domain.AuthFailureNetwork
	}
	if domain.IsAuthExpiredError(cause) {
		return domain.AuthFailureAuth
	}
	return domain.AuthFailureUpstream
}
