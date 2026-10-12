package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"litepan/internal/domain"
)

type transferRecordRepo struct{ db *DB }

func (r *transferRecordRepo) Create(ctx context.Context, rec *domain.TransferRecord) error {
	if rec == nil {
		return domain.Errorf(domain.CodeValidation, "无效转存记录")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO transfer_records(
    kind, link, password, share_name, file_id, file_name, status, note, source,
    account_id, account_name, task_id, target_display_path, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.Kind, rec.Link, rec.Password, rec.ShareName, rec.FileID, rec.FileName, rec.Status, rec.Note, rec.Source,
		rec.AccountID, rec.AccountName, rec.TaskID, rec.TargetDisplayPath, tsValue(rec.CreatedAt), tsValue(rec.UpdatedAt),
	)
	if err != nil {
		return wrapDB(err)
	}
	if id, err := res.LastInsertId(); err == nil {
		rec.ID = id
	}
	return nil
}

func (r *transferRecordRepo) Update(ctx context.Context, rec *domain.TransferRecord) error {
	if rec == nil || rec.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效转存记录")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE transfer_records
   SET status=?, note=?, file_id=?, file_name=?, updated_at=?
 WHERE id=?`,
		rec.Status, rec.Note, rec.FileID, rec.FileName, tsValue(rec.UpdatedAt), rec.ID,
	)
	return wrapDB(err)
}

// UpdateStatusByTaskID 是离线任务对账的回写口。
func (r *transferRecordRepo) UpdateStatusByTaskID(ctx context.Context, taskID, status, fileName, fileID, note string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE transfer_records
   SET status=?,
       file_name=CASE WHEN ?<>'' THEN ? ELSE file_name END,
       file_id=CASE WHEN ?<>'' THEN ? ELSE file_id END,
       note=CASE WHEN ?<>'' THEN ? ELSE note END,
       updated_at=?
 WHERE task_id=?`,
		status, fileName, fileName, fileID, fileID, note, note, tsValue(time.Now()), taskID,
	)
	return wrapDB(err)
}

func (r *transferRecordRepo) Delete(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM transfer_records WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *transferRecordRepo) List(ctx context.Context, filter domain.TransferRecordFilter, limit, offset int) ([]*domain.TransferRecord, int, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if v := strings.TrimSpace(filter.Kind); v != "" {
		where = append(where, "kind=?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		where = append(where, "status=?")
		args = append(args, v)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM transfer_records`+clause, args...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}

	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, kind, link, password, share_name, file_id, file_name, status, note, source,
       account_id, account_name, task_id, target_display_path, created_at, updated_at
FROM transfer_records` + clause + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := r.db.read.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.TransferRecord, 0, limit)
	for rows.Next() {
		rec, err := scanTransferRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rec)
	}
	return out, total, wrapDB(rows.Err())
}

func scanTransferRecord(rows *sql.Rows) (*domain.TransferRecord, error) {
	var rec domain.TransferRecord
	var createdAt, updatedAt sql.NullString
	err := rows.Scan(
		&rec.ID, &rec.Kind, &rec.Link, &rec.Password, &rec.ShareName, &rec.FileID, &rec.FileName,
		&rec.Status, &rec.Note, &rec.Source, &rec.AccountID, &rec.AccountName, &rec.TaskID,
		&rec.TargetDisplayPath, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	// 与其它仓储一致：库里的时间列先按 sql.NullString 取出来再 parseTS。
	rec.CreatedAt = parseTS(createdAt)
	rec.UpdatedAt = parseTS(updatedAt)
	return &rec, nil
}
