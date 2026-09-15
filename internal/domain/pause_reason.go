package domain

type PauseReason string

const (
	PauseReasonUser            PauseReason = "user"
	PauseReasonAccountDisabled PauseReason = "account_disabled"
	PauseReasonAuthFailure     PauseReason = "auth_failure"
)

func (r PauseReason) AutoResumable() bool {
	return r == PauseReasonAccountDisabled || r == PauseReasonAuthFailure
}

func ValidAutoPauseReason(r PauseReason) bool {
	return r.AutoResumable()
}
