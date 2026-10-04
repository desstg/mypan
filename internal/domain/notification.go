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

// NotificationCategoryJavBackfill 番号的后台回填结果（详情回填一轮跑完的汇总）。
//
// 与 JavPush 分开是**必须**的：通知中心按 category 合并同键通知（见通知合并那条），
// 混在一起会让「补了 N 部详情」把用户真正要查的「某条订阅推送失败」挤掉。
const NotificationCategoryJavBackfill = "jav_backfill"

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
