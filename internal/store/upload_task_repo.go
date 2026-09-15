package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type uploadTaskRepo struct{ db *DB }

func (r *uploadTaskRepo) Upsert(ctx context.Context, rec *domain.UploadTaskRecord) error {
	if rec == nil || rec.TaskID == "" {
		return domain.Errorf(domain.CodeValidation, "无效上传任务")
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO upload_tasks(
    task_id, client_task_id, batch_id, batch_name, account_id, account_name, driver_type, file_name,
    source_type, source_account_id, source_account_name, source_driver_type, source_file_id,
    rel_path, rel_dir, target_path, target_display_path, status, phase, progress,
    downloaded_bytes, uploaded_bytes, speed_bps, total_bytes, message, error, result_json,
    resume_data_json, cleanup_local_mode, cleanup_local_path, queue_order, created_at, updated_at, local_path, conflict_policy
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(task_id) DO UPDATE SET
    client_task_id=excluded.client_task_id,
    batch_id=excluded.batch_id,
    batch_name=excluded.batch_name,
    account_id=excluded.account_id,
    account_name=excluded.account_name,
    driver_type=excluded.driver_type,
    file_name=excluded.file_name,
    source_type=excluded.source_type,
    source_account_id=excluded.source_account_id,
    source_account_name=excluded.source_account_name,
    source_driver_type=excluded.source_driver_type,
    source_file_id=excluded.source_file_id,
    rel_path=excluded.rel_path,
    rel_dir=excluded.rel_dir,
    target_path=excluded.target_path,
    target_display_path=excluded.target_display_path,
    status=excluded.status,
    phase=excluded.phase,
    progress=excluded.progress,
    downloaded_bytes=excluded.downloaded_bytes,
    uploaded_bytes=excluded.uploaded_bytes,
    speed_bps=excluded.speed_bps,
    total_bytes=excluded.total_bytes,
    message=excluded.message,
    error=excluded.error,
    result_json=excluded.result_json,
    resume_data_json=excluded.resume_data_json,
    cleanup_local_mode=excluded.cleanup_local_mode,
    cleanup_local_path=excluded.cleanup_local_path,
    queue_order=excluded.queue_order,
    created_at=excluded.created_at,
    updated_at=excluded.updated_at,
    local_path=excluded.local_path,
    conflict_policy=excluded.conflict_policy`,
		rec.TaskID, rec.ClientTaskID, rec.BatchID, rec.BatchName, rec.AccountID, rec.AccountName, rec.DriverType, rec.FileName,
		rec.SourceType, rec.SourceAccountID, rec.SourceAccountName, rec.SourceDriverType, rec.SourceFileID,
		rec.RelPath, rec.RelDir, rec.TargetPath, rec.TargetDisplayPath, rec.Status, rec.Phase, rec.Progress,
		rec.DownloadedBytes, rec.UploadedBytes, rec.SpeedBytesPerSecond, rec.TotalBytes, rec.Message, rec.Error, rec.ResultJSON,
		rec.ResumeDataJSON, rec.CleanupLocalMode, rec.CleanupLocalPath, rec.QueueOrder,
		rec.CreatedAt, rec.UpdatedAt, rec.LocalPath, rec.ConflictPolicy,
	)
	return wrapDB(err)
}

func (r *uploadTaskRepo) Delete(ctx context.Context, taskID string) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM upload_tasks WHERE task_id=?`, taskID)
	return wrapDB(err)
}

func (r *uploadTaskRepo) List(ctx context.Context) ([]*domain.UploadTaskRecord, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT task_id, client_task_id, batch_id, batch_name, account_id, account_name, driver_type, file_name,
       source_type, source_account_id, source_account_name, source_driver_type, source_file_id,
       rel_path, rel_dir, target_path, target_display_path, status, phase, progress,
       downloaded_bytes, uploaded_bytes, speed_bps, total_bytes, message, error, result_json,
       resume_data_json, cleanup_local_mode, cleanup_local_path, queue_order, created_at, updated_at, local_path, conflict_policy
FROM upload_tasks ORDER BY queue_order ASC, created_at ASC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.UploadTaskRecord
	for rows.Next() {
		rec, err := scanUploadTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, wrapDB(rows.Err())
}

func scanUploadTask(rows *sql.Rows) (*domain.UploadTaskRecord, error) {
	var rec domain.UploadTaskRecord
	err := rows.Scan(
		&rec.TaskID, &rec.ClientTaskID, &rec.BatchID, &rec.BatchName, &rec.AccountID, &rec.AccountName, &rec.DriverType, &rec.FileName,
		&rec.SourceType, &rec.SourceAccountID, &rec.SourceAccountName, &rec.SourceDriverType, &rec.SourceFileID,
		&rec.RelPath, &rec.RelDir, &rec.TargetPath, &rec.TargetDisplayPath, &rec.Status, &rec.Phase, &rec.Progress,
		&rec.DownloadedBytes, &rec.UploadedBytes, &rec.SpeedBytesPerSecond, &rec.TotalBytes, &rec.Message, &rec.Error, &rec.ResultJSON,
		&rec.ResumeDataJSON, &rec.CleanupLocalMode, &rec.CleanupLocalPath, &rec.QueueOrder,
		&rec.CreatedAt, &rec.UpdatedAt, &rec.LocalPath, &rec.ConflictPolicy,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	return &rec, nil
}
