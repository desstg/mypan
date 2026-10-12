package domain

import (
	"context"
	"time"
)

// 转存记录的两种来源。
const (
	// TransferSourceSearch 是「搜索页结果行上的转存按钮」提交的。
	TransferSourceSearch = "search"
	// TransferSourceManual 是「转存 tab 里手工粘贴链接」提交的。
	TransferSourceManual = "manual"
)

// 转存记录的状态。
//
// ⚠️ 与 offline_download_tasks 的 status **不是同一套值域**，别互相套用：
// 那边是网盘任务的细粒度状态（pending/running/retrying/success/failed），
// 这边是用户视角的五态。映射只有一处，见 searchtransfer 的 records.go。
//
// 秒传两个态现在没有产出方（没有秒传通道），留着是为了让这一页的筛选器
// 从一开始就与最终形态一致 —— 加通道时不用改表、不用改筛选器。
const (
	TransferStatusPending      = "pending"
	TransferStatusSuccess      = "success"
	TransferStatusFailed       = "failed"
	TransferStatusRapidSuccess = "rapid_success"
	TransferStatusRapidFailed  = "rapid_failed"
)

// TransferRecord 是一次「把链接存进网盘」的留档。
//
// 它记的是**提交那一刻**的事实：链接原文、提取码、目标目录。
// 结果随后由对账（离线任务刷新后回写）以及提交时的返回值填上。
type TransferRecord struct {
	ID          int64
	Kind        string
	Link        string
	Password    string
	ShareName   string
	FileID      string
	FileName    string
	Status      string
	Note        string
	Source      string
	AccountID   int64
	AccountName string
	// TaskID 关联 offline_download_tasks.task_id；分享转存为空。
	// 列表加载时靠它把 pending 的记录对账成真实结果。
	TaskID            string
	TargetDisplayPath string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// TransferRecordFilter 是列表查询的筛选条件（全部可选）。
type TransferRecordFilter struct {
	Kind   string
	Status string
}

// TransferRecordRepository 是转存记录的仓储。
type TransferRecordRepository interface {
	Create(ctx context.Context, rec *TransferRecord) error
	// Update 按 ID 覆盖状态/文件名/备注 —— 对账与结果回写走它。
	Update(ctx context.Context, rec *TransferRecord) error
	UpdateStatusByTaskID(ctx context.Context, taskID, status, fileName, fileID, note string) error
	Delete(ctx context.Context, ids []int64) (int64, error)
	List(ctx context.Context, filter TransferRecordFilter, limit, offset int) ([]*TransferRecord, int, error)
}
