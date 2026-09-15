package fusemount

import "litepan/internal/domain"

func isActiveMountState(state string) bool {
	switch state {
	case domain.FuseStateMounted, domain.FuseStateMounting, domain.FuseStateError:
		return true
	default:
		return false
	}
}
