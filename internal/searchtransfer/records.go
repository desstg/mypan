package searchtransfer

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
)

// 转存记录的读写与对账。
//
// 记什么：**用户按下「转存」那一刻的事实** —— 链接原文、提取码、提交到哪个目录。
// 结果随后由两处填上：
//   - 提交时的返回值（分享转存是同步落盘的，一次调用就知道结果）；
//   - 列表加载时的对账（离线下载还在跑，要等它的任务状态收敛）。
//
// 为什么不把记录塞进 offline_download_tasks：那张表只覆盖「走离线下载」的那一半
// （115 分享转存根本不在里面），而且它会被进度刷新频繁改写，不是留档。

// RecordFilter 是「转存记录」页的筛选条件。
type RecordFilter struct {
	Kind   string
	Status string
}

// RecordView 是给前端的一条记录。
type RecordView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Link     string `json:"link"`
	Password string `json:"password"`
	// ShareName 是分享标题 / 资源名（盘搜的 note）。离线下载那条路只有它。
	ShareName string `json:"share_name"`
	FileID    string `json:"file_id"`
	// FileName 是网盘落盘后的文件名，离线任务跑完才有。
	FileName    string `json:"file_name"`
	Status      string `json:"status"`
	Note        string `json:"note"`
	Source      string `json:"source"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	// TargetDisplayPath 是提交时的目标目录（展示用）。
	TargetDisplayPath string  `json:"target_display_path"`
	CreatedAt         float64 `json:"created_at"`
	UpdatedAt         float64 `json:"updated_at"`
}

// RecordList 是一页转存记录。
type RecordList struct {
	Items []RecordView `json:"items"`
	Total int          `json:"total"`
}

// newRecord 把一次提交的入参转成待落库的记录。
//
// CreatedAt/UpdatedAt 用**本地时间**：这一页是给人看的「什么时候转的」，
// 而库里其它业务表用的是 UTC 文本。差 8 小时在列表上很显眼，所以这里统一走
// time.Now() 写入 —— 与 offline_download_tasks 用 unix 浮点数的做法一致。
func newRecord(kind, link, password, shareName, source, accountName, taskID, targetPath string, accountID int64, status, note string) *domain.TransferRecord {
	now := time.Now()
	return &domain.TransferRecord{
		Kind:              kind,
		Link:              link,
		Password:          password,
		ShareName:         shareName,
		Status:            status,
		Note:              note,
		Source:            source,
		AccountID:         accountID,
		AccountName:       accountName,
		TaskID:            taskID,
		TargetDisplayPath: targetPath,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// recordCreate 落一条记录。失败只记日志 —— **绝不让写记录失败把转存本身判成失败**：
// 东西已经进网盘了，记录只是留档。
func (s *Service) recordCreate(ctx context.Context, rec *domain.TransferRecord) {
	if s == nil || s.records == nil || rec == nil {
		return
	}
	if err := s.records.Create(ctx, rec); err != nil {
		s.log.Warn("写转存记录失败", "kind", rec.Kind, "err", err)
	}
}

// Records 读一页转存记录。
//
// 读之前先做一次对账：把还停在 pending 的离线任务记录按当前任务状态收敛。
// 放在**读**的时候做而不是起一个后台循环，是因为这一页本来就是低频人工查看的 ——
// 每次打开时对一遍，拿到的就是最新的，不需要额外常驻一个 goroutine。
func (s *Service) Records(ctx context.Context, filter RecordFilter, page, pageSize int) (*RecordList, error) {
	if !s.Ready() {
		return nil, domain.Errorf(domain.CodeInternal, "搜索转存服务未就绪")
	}
	if s.records == nil {
		return nil, domain.Errorf(domain.CodeInternal, "转存记录仓储未就绪")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}

	s.reconcile(ctx)

	rows, total, err := s.records.List(ctx, domain.TransferRecordFilter{
		Kind:   strings.TrimSpace(filter.Kind),
		Status: strings.TrimSpace(filter.Status),
	}, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	items := make([]RecordView, 0, len(rows))
	for _, rec := range rows {
		items = append(items, recordToView(rec))
	}
	return &RecordList{Items: items, Total: total}, nil
}

// DeleteRecords 删掉选中的记录。**只删记录，不动网盘里的文件** —— 这一页是留档，
// 删档不等于删东西；真删文件要用文件管理那边。
func (s *Service) DeleteRecords(ctx context.Context, ids []int64) (int64, error) {
	if !s.Ready() {
		return 0, domain.Errorf(domain.CodeInternal, "搜索转存服务未就绪")
	}
	if s.records == nil {
		return 0, domain.Errorf(domain.CodeInternal, "转存记录仓储未就绪")
	}
	if len(ids) == 0 {
		return 0, domain.Errorf(domain.CodeValidation, "请先选择要删除的记录")
	}
	return s.records.Delete(ctx, ids)
}

// reconcile 把 pending 的记录按离线任务的真实状态收敛。
//
// 只处理**有 task_id 且还停在 pending** 的那些：分享转存提交时就已经写死了结果，
// 没有 task_id 的记录本来就是终态。
func (s *Service) reconcile(ctx context.Context) {
	if s.offline == nil || s.records == nil {
		return
	}
	// 读一页够用的量：pending 的记录不会堆积太多，用户点开这一页时也只看得到最近的一屏。
	pending, _, err := s.records.List(ctx, domain.TransferRecordFilter{Status: domain.TransferStatusPending}, 200, 0)
	if err != nil || len(pending) == 0 {
		return
	}
	// 离线任务在内存里，按账号分组刷新一次就够。accountID=0 表示全部账号。
	if err := s.offline.Refresh(ctx, 0, false); err != nil {
		s.log.Warn("对账前刷新离线任务失败", "err", err)
		return
	}
	tasks, err := s.offline.List(ctx, 0, false)
	if err != nil {
		s.log.Warn("对账时读离线任务失败", "err", err)
		return
	}
	byID := make(map[string]offlinedownload.Task, len(tasks))
	for _, t := range tasks {
		byID[t.TaskID] = t
	}
	for _, rec := range pending {
		task, ok := byID[rec.TaskID]
		if !ok {
			continue
		}
		status, note := mapTaskStatus(task)
		if status == domain.TransferStatusPending {
			continue
		}
		if err := s.records.UpdateStatusByTaskID(ctx, rec.TaskID, status, task.Name, task.FileID, note); err != nil {
			s.log.Warn("回写转存记录状态失败", "task", rec.TaskID, "err", err)
		}
	}
}

// mapTaskStatus 把离线任务状态映射成这一页的四态。
//
// ⚠️ 这是两个值域之间**唯一**的映射点。改离线那边的状态机时改这里，别在别处再写一份 ——
// 两处各写一份必然漂移，表现是「任务成功了但记录一直停在处理中」。
func mapTaskStatus(task offlinedownload.Task) (string, string) {
	switch task.Status {
	case driver.OfflineStatusSuccess:
		note := task.Message
		if strings.TrimSpace(note) == "" {
			note = "保存成功"
		}
		return domain.TransferStatusSuccess, note
	case driver.OfflineStatusFailed:
		note := strings.TrimSpace(task.Error)
		if note == "" {
			note = strings.TrimSpace(task.Message)
		}
		if note == "" {
			note = "保存失败"
		}
		return domain.TransferStatusFailed, note
	default:
		return domain.TransferStatusPending, ""
	}
}

// recordToView 把库里的记录转成前端视图。
func recordToView(rec *domain.TransferRecord) RecordView {
	if rec == nil {
		return RecordView{}
	}
	return RecordView{
		ID:                rec.ID,
		Kind:              rec.Kind,
		Link:              rec.Link,
		Password:          rec.Password,
		ShareName:         rec.ShareName,
		FileID:            rec.FileID,
		FileName:          rec.FileName,
		Status:            rec.Status,
		Note:              rec.Note,
		Source:            rec.Source,
		AccountID:         rec.AccountID,
		AccountName:       rec.AccountName,
		TargetDisplayPath: rec.TargetDisplayPath,
		CreatedAt:         float64(rec.CreatedAt.Unix()),
		UpdatedAt:         float64(rec.UpdatedAt.Unix()),
	}
}
