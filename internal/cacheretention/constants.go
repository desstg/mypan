package cacheretention

import "time"

const (
	maxTasks              = 6
	startupDelay          = 0 * time.Second
	defaultScanDepth      = 4
	defaultAPIInterval    = 200
	maxAPIInterval        = 5000
	defaultRefreshMinutes = 60
)
