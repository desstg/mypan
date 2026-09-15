package auth

import "time"

const (
	activeFailedThreshold  = 5
	passiveCooldown        = 60 * time.Second
	failedRetryCooldown    = 24 * time.Hour
	passiveReuseWindow     = 20 * time.Second
	checkTolerance         = 30 * time.Second
	betweenAccountRefresh  = 2 * time.Second
	activeAuthStartupDelay = 5 * time.Second
)

var activeCooldownSteps = []struct {
	attempt int
	cd      time.Duration
}{
	{1, 60 * time.Second},
	{2, 120 * time.Second},
	{3, 300 * time.Second},
	{4, 1800 * time.Second},
}

// SteppedCooldown 返回主动刷新失败后的冷却时长。
func SteppedCooldown(attempts int) time.Duration {
	for _, step := range activeCooldownSteps {
		if attempts <= step.attempt {
			return step.cd
		}
	}
	return 1200 * time.Second
}
