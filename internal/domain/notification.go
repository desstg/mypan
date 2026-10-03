package domain

import "time"

const NotificationCategoryCacheScopeWarn = "cache_scope_warn"
const NotificationCategoryStrmScanWarn = "strm_scan_warn"
const NotificationCategoryStrmScrapeWarn = "strm_scrape_warn"
const NotificationCategoryFuseMountWarn = "fuse_mount_warn"
const NotificationCategoryQuarkTVWarn = "quarktv_warn"
const NotificationCategoryTGSubscribe = "tg_subscribe"
const NotificationCategoryTGSubscribeWarn = "tg_subscribe_warn"

// NotificationCategoryJavPush 番号订阅的推送结果：定时那一轮的汇总，以及手动推送的每一次。
const NotificationCategoryJavPush = "jav_push"

type Notification struct {
	ID        int64
	Level     string
	Category  string
	Title     string
	Message   string
	AccountID int64
	RefID     int64
	IsRead    bool
	// Count 是同一条通知重复发生的次数（未读期间同键合并，见 notificationRepo.CreateOrMerge）。
	// 1 表示只发生过一次；前端只在 >1 时显示次数。
	Count int
	// FirstAt 是第一次发生的时间；CreatedAt 会被合并推到最近一次（列表按它倒序）。
	FirstAt   time.Time
	CreatedAt time.Time
}
