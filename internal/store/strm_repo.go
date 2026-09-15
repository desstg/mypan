package store

import (
	"context"
	"database/sql"
	"time"

	"litepan/internal/domain"
)

type strmTaskRepo struct{ db *DB }

func (r *strmTaskRepo) Create(ctx context.Context, task *domain.StrmTask) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO strm_tasks
		  (name,account_id,parent_id,path,recursive,scan_interval,scan_mode,extensions,output_folder,group_dir,
		   api_interval,exclude_dir_keywords,exclude_file_keywords,sync_metadata,branch_check_enabled,
		   time_window_enabled,time_start,time_end,schedule_mode,
		   status,paused_reason,error_message,last_scan_status)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.Name, task.AccountID, task.ParentID, task.Path, boolToInt(task.Recursive), task.ScanInterval,
		task.ScanMode, task.Extensions, task.OutputFolder, task.GroupDir,
		task.ApiInterval, task.ExcludeDirKeywords, task.ExcludeFileKeywords, boolToInt(task.SyncMetadata),
		boolToInt(task.BranchCheckEnabled), boolToInt(task.TimeWindowEnabled), task.TimeStart, task.TimeEnd,
		task.ScheduleMode, task.Status, task.PausedReason, task.ErrorMessage, task.LastScanStatus)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *strmTaskRepo) Update(ctx context.Context, task *domain.StrmTask) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE strm_tasks
		 SET name=?,account_id=?,parent_id=?,path=?,recursive=?,scan_interval=?,scan_mode=?,extensions=?,output_folder=?,group_dir=?,
		     api_interval=?,exclude_dir_keywords=?,exclude_file_keywords=?,sync_metadata=?,branch_check_enabled=?,
		     time_window_enabled=?,time_start=?,time_end=?,schedule_mode=?,
		     status=?,paused_reason=?,error_message=?,updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		task.Name, task.AccountID, task.ParentID, task.Path, boolToInt(task.Recursive), task.ScanInterval,
		task.ScanMode, task.Extensions, task.OutputFolder, task.GroupDir,
		task.ApiInterval, task.ExcludeDirKeywords, task.ExcludeFileKeywords, boolToInt(task.SyncMetadata),
		boolToInt(task.BranchCheckEnabled), boolToInt(task.TimeWindowEnabled), task.TimeStart, task.TimeEnd,
		task.ScheduleMode, task.Status, task.PausedReason, task.ErrorMessage, task.ID)
	return wrapDB(err)
}

func (r *strmTaskRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM strm_tasks WHERE id=?`, id)
	return wrapDB(err)
}

func (r *strmTaskRepo) Get(ctx context.Context, id int64) (*domain.StrmTask, error) {
	row := r.db.read.QueryRowContext(ctx, selectStrmTaskCols+` WHERE id=?`, id)
	return scanStrmTask(row)
}

func (r *strmTaskRepo) List(ctx context.Context) ([]*domain.StrmTask, error) {
	rows, err := r.db.read.QueryContext(ctx, selectStrmTaskCols+` ORDER BY id`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.StrmTask
	for rows.Next() {
		task, err := scanStrmTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, wrapDB(rows.Err())
}

func (r *strmTaskRepo) ListByAccount(ctx context.Context, accountID int64) ([]*domain.StrmTask, error) {
	rows, err := r.db.read.QueryContext(ctx, selectStrmTaskCols+` WHERE account_id=? ORDER BY id`, accountID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.StrmTask
	for rows.Next() {
		task, err := scanStrmTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, wrapDB(rows.Err())
}

func (r *strmTaskRepo) UpdateScan(ctx context.Context, id int64, patch domain.StrmScanPatch) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE strm_tasks
		 SET status=?,paused_reason=?,error_message=?,
		     scanned_count=?,generated_count=?,updated_count=?,removed_count=?,
		     last_scan=?,last_scan_status=?,updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		patch.Status, patch.PausedReason, patch.ErrorMessage,
		patch.ScannedCount, patch.GeneratedCount, patch.UpdatedCount, patch.RemovedCount,
		tsValue(patch.LastScan), patch.LastScanStatus, id)
	return wrapDB(err)
}

const selectStrmTaskCols = `SELECT id,name,account_id,parent_id,path,recursive,scan_interval,scan_mode,extensions,output_folder,group_dir,
       api_interval,exclude_dir_keywords,exclude_file_keywords,sync_metadata,branch_check_enabled,
       time_window_enabled,time_start,time_end,schedule_mode,
       status,paused_reason,error_message,scanned_count,generated_count,updated_count,removed_count,last_scan,last_scan_status,created_at,updated_at
FROM strm_tasks`

func scanStrmTask(s rowScanner) (*domain.StrmTask, error) {
	var (
		task        domain.StrmTask
		recursive   int
		syncMeta    int
		branchCheck int
		timeWindow  int
		lastScan    sql.NullString
		createdAt   sql.NullString
		updatedAt   sql.NullString
	)
	err := s.Scan(
		&task.ID, &task.Name, &task.AccountID, &task.ParentID, &task.Path, &recursive,
		&task.ScanInterval, &task.ScanMode, &task.Extensions, &task.OutputFolder, &task.GroupDir,
		&task.ApiInterval, &task.ExcludeDirKeywords, &task.ExcludeFileKeywords, &syncMeta, &branchCheck,
		&timeWindow, &task.TimeStart, &task.TimeEnd, &task.ScheduleMode,
		&task.Status, &task.PausedReason, &task.ErrorMessage,
		&task.ScannedCount, &task.GeneratedCount, &task.UpdatedCount, &task.RemovedCount,
		&lastScan, &task.LastScanStatus, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	task.Recursive = recursive != 0
	task.SyncMetadata = syncMeta != 0
	task.BranchCheckEnabled = branchCheck != 0
	task.TimeWindowEnabled = timeWindow != 0
	task.LastScan = parseTS(lastScan)
	task.CreatedAt = parseTS(createdAt)
	task.UpdatedAt = parseTS(updatedAt)
	return &task, nil
}

type strmBranchRepo struct{ db *DB }

func (r *strmBranchRepo) Create(ctx context.Context, branch *domain.StrmBranch) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO strm_branches
		  (task_id,account_id,parent_id,path,relative_path,recursive,retention_days,expires_at,branch_type,status,source)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		branch.TaskID, branch.AccountID, branch.ParentID, branch.Path, branch.RelativePath,
		boolToInt(branch.Recursive), branch.RetentionDays, tsValue(branch.ExpiresAt),
		branch.BranchType, branch.Status, branch.Source)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *strmBranchRepo) Update(ctx context.Context, branch *domain.StrmBranch) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE strm_branches
		 SET parent_id=?,path=?,relative_path=?,recursive=?,retention_days=?,expires_at=?,branch_type=?,status=?,updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND task_id=?`,
		branch.ParentID, branch.Path, branch.RelativePath, boolToInt(branch.Recursive),
		branch.RetentionDays, tsValue(branch.ExpiresAt), branch.BranchType, branch.Status,
		branch.ID, branch.TaskID)
	return wrapDB(err)
}

func (r *strmBranchRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM strm_branches WHERE id=?`, id)
	return wrapDB(err)
}

func (r *strmBranchRepo) Get(ctx context.Context, id int64) (*domain.StrmBranch, error) {
	row := r.db.read.QueryRowContext(ctx, selectStrmBranchCols+` WHERE id=?`, id)
	return scanStrmBranch(row)
}

func (r *strmBranchRepo) ListByTask(ctx context.Context, taskID int64) ([]*domain.StrmBranch, error) {
	rows, err := r.db.read.QueryContext(ctx, selectStrmBranchCols+` WHERE task_id=? ORDER BY path`, taskID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.StrmBranch
	for rows.Next() {
		b, err := scanStrmBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, wrapDB(rows.Err())
}

func (r *strmBranchRepo) DeleteExpired(ctx context.Context, taskID int64) (int, error) {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM strm_branches WHERE task_id=? AND expires_at IS NOT NULL AND expires_at <> '' AND expires_at < ?`,
		taskID, now)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrapDB(err)
	}
	return int(n), nil
}

const selectStrmBranchCols = `SELECT id,task_id,account_id,parent_id,path,relative_path,recursive,retention_days,expires_at,branch_type,status,source,created_at,updated_at
FROM strm_branches`

func scanStrmBranch(s rowScanner) (*domain.StrmBranch, error) {
	var (
		b         domain.StrmBranch
		recursive int
		expiresAt sql.NullString
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	err := s.Scan(
		&b.ID, &b.TaskID, &b.AccountID, &b.ParentID, &b.Path, &b.RelativePath, &recursive,
		&b.RetentionDays, &expiresAt, &b.BranchType, &b.Status, &b.Source, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	b.Recursive = recursive != 0
	b.ExpiresAt = parseTS(expiresAt)
	b.CreatedAt = parseTS(createdAt)
	b.UpdatedAt = parseTS(updatedAt)
	return &b, nil
}
