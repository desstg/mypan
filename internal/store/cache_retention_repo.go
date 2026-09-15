package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type cacheRetentionRepo struct{ db *DB }

const selectRetentionCols = `SELECT crt.id, crt.account_id, COALESCE(ca.name,''), crt.parent_id, crt.path,
 crt.scan_depth, crt.api_interval, crt.refresh_interval, crt.status, crt.paused_reason,
 crt.file_count, crt.last_refresh, crt.last_refresh_status, crt.last_duration_ms,
 crt.last_api_calls, crt.last_skip_calls, crt.last_scanned_dirs, crt.last_run_config_fp, crt.error_message,
 crt.time_window_enabled, crt.time_start, crt.time_end, crt.ignore_large_scope_warn, crt.created_at, crt.updated_at
 FROM cache_retention_tasks crt
 LEFT JOIN cloud_accounts ca ON ca.id = crt.account_id`

func (r *cacheRetentionRepo) Create(ctx context.Context, task *domain.CacheRetentionTask) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO cache_retention_tasks
		  (account_id,parent_id,path,scan_depth,api_interval,refresh_interval,status,paused_reason,
		   time_window_enabled,time_start,time_end)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		task.AccountID, task.ParentID, task.Path, task.ScanDepth, task.ApiInterval, task.RefreshInterval,
		task.Status, task.PausedReason, boolToInt(task.TimeWindowEnabled), task.TimeStart, task.TimeEnd)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *cacheRetentionRepo) Update(ctx context.Context, task *domain.CacheRetentionTask) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE cache_retention_tasks
		 SET account_id=?,parent_id=?,path=?,scan_depth=?,api_interval=?,refresh_interval=?,
		     status=?,paused_reason=?,time_window_enabled=?,time_start=?,time_end=?,
		     error_message=?,updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		task.AccountID, task.ParentID, task.Path, task.ScanDepth, task.ApiInterval, task.RefreshInterval,
		task.Status, task.PausedReason, boolToInt(task.TimeWindowEnabled), task.TimeStart, task.TimeEnd,
		task.ErrorMessage, task.ID)
	return wrapDB(err)
}

func (r *cacheRetentionRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM cache_retention_tasks WHERE id=?`, id)
	return wrapDB(err)
}

func (r *cacheRetentionRepo) Get(ctx context.Context, id int64) (*domain.CacheRetentionTask, error) {
	row := r.db.read.QueryRowContext(ctx, selectRetentionCols+` WHERE crt.id=?`, id)
	return scanRetentionTask(row)
}

func (r *cacheRetentionRepo) List(ctx context.Context) ([]*domain.CacheRetentionTask, error) {
	rows, err := r.db.read.QueryContext(ctx, selectRetentionCols+` ORDER BY crt.id`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.CacheRetentionTask
	for rows.Next() {
		task, err := scanRetentionTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, wrapDB(rows.Err())
}

func (r *cacheRetentionRepo) Count(ctx context.Context) (int, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_retention_tasks`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, wrapDB(err)
	}
	return n, nil
}

func (r *cacheRetentionRepo) UpdateRunStats(ctx context.Context, id int64, stats domain.RetentionRunStats) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE cache_retention_tasks
		 SET file_count=?,last_refresh=?,last_refresh_status=?,last_duration_ms=?,
		     last_api_calls=?,last_skip_calls=?,last_scanned_dirs=?,last_run_config_fp=?,error_message=?,
		     updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		stats.FileCount, tsValue(stats.LastRefresh), stats.LastRefreshStatus, stats.LastDurationMS,
		stats.LastAPICalls, stats.LastSkipCalls, stats.LastScannedDirs, stats.LastRunConfigFP, stats.ErrorMessage, id)
	return wrapDB(err)
}

func (r *cacheRetentionRepo) SetIgnoreLargeScopeWarn(ctx context.Context, id int64, ignore bool) error {
	res, err := r.db.write.ExecContext(ctx,
		`UPDATE cache_retention_tasks SET ignore_large_scope_warn=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		boolToInt(ignore), id)
	if err != nil {
		return wrapDB(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return wrapDB(err)
	}
	if n == 0 {
		return domain.Errf(domain.CodeNotFound)
	}
	return nil
}

func scanRetentionTask(row rowScanner) (*domain.CacheRetentionTask, error) {
	var t domain.CacheRetentionTask
	var lastRefresh sql.NullString
	var createdAt sql.NullString
	var updatedAt sql.NullString
	var tw int
	var ignoreWarn int
	if err := row.Scan(
		&t.ID, &t.AccountID, &t.AccountName, &t.ParentID, &t.Path,
		&t.ScanDepth, &t.ApiInterval, &t.RefreshInterval, &t.Status, &t.PausedReason,
		&t.FileCount, &lastRefresh, &t.LastRefreshStatus, &t.LastDurationMS,
		&t.LastAPICalls, &t.LastSkipCalls, &t.LastScannedDirs, &t.LastRunConfigFP, &t.ErrorMessage,
		&tw, &t.TimeStart, &t.TimeEnd, &ignoreWarn, &createdAt, &updatedAt,
	); err != nil {
		return nil, wrapDB(err)
	}
	t.TimeWindowEnabled = tw != 0
	t.IgnoreLargeScopeWarn = ignoreWarn != 0
	if ts := parseTS(lastRefresh); !ts.IsZero() {
		tsCopy := ts
		t.LastRefresh = &tsCopy
	}
	t.CreatedAt = parseTS(createdAt)
	t.UpdatedAt = parseTS(updatedAt)
	return &t, nil
}
