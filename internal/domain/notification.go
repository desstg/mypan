package domain

import "time"

const NotificationCategoryCacheScopeWarn = "cache_scope_warn"
const NotificationCategoryStrmScanWarn = "strm_scan_warn"
const NotificationCategoryStrmScrapeWarn = "strm_scrape_warn"
const NotificationCategoryFuseMountWarn = "fuse_mount_warn"
const NotificationCategoryQuarkTVWarn = "quarktv_warn"

type Notification struct {
	ID        int64
	Level     string
	Category  string
	Title     string
	Message   string
	AccountID int64
	RefID     int64
	IsRead    bool
	CreatedAt time.Time
}
