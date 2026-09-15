package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"

	"litepan/internal/domain"
)

type mediaOrganizeTaskRepo struct{ db *DB }

func (r *mediaOrganizeTaskRepo) Create(ctx context.Context, task *domain.MediaOrganizeTask) error {
	if task == nil {
		return domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if len(task.Config) == 0 {
		task.Config = emptyJSONObject
	}
	if task.Status == "" {
		task.Status = domain.MediaOrganizeStatusIdle
	}
	_, err := r.db.write.ExecContext(ctx,
		`INSERT INTO media_organize_tasks
		  (id, task_name, account_id, config, status, last_run_at, last_run_result)
		 VALUES (?,?,?,?,?,?,?)`,
		task.ID, task.TaskName, task.AccountID, string(task.Config), task.Status,
		tsValue(task.LastRunAt), string(task.LastRunResult))
	if err != nil {
		return wrapDB(err)
	}
	return nil
}

func (r *mediaOrganizeTaskRepo) Update(ctx context.Context, task *domain.MediaOrganizeTask) error {
	if task == nil || task.ID == "" {
		return domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	if len(task.Config) == 0 {
		task.Config = emptyJSONObject
	}
	res, err := r.db.write.ExecContext(ctx,
		`UPDATE media_organize_tasks
		 SET task_name=?, account_id=?, config=?, status=?, last_run_at=?, last_run_result=?,
		     updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		task.TaskName, task.AccountID, string(task.Config), task.Status,
		tsValue(task.LastRunAt), string(task.LastRunResult), task.ID)
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

func (r *mediaOrganizeTaskRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM media_organize_tasks WHERE id=?`, id)
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

func (r *mediaOrganizeTaskRepo) Get(ctx context.Context, id string) (*domain.MediaOrganizeTask, error) {
	row := r.db.read.QueryRowContext(ctx, selectMediaOrganizeTaskCols+` WHERE id=?`, id)
	return scanMediaOrganizeTask(row)
}

func (r *mediaOrganizeTaskRepo) List(ctx context.Context) ([]*domain.MediaOrganizeTask, error) {
	rows, err := r.db.read.QueryContext(ctx, selectMediaOrganizeTaskCols+` ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	return scanMediaOrganizeTaskRows(rows)
}

func (r *mediaOrganizeTaskRepo) ListByAccount(ctx context.Context, accountID int64) ([]*domain.MediaOrganizeTask, error) {
	rows, err := r.db.read.QueryContext(ctx, selectMediaOrganizeTaskCols+` WHERE account_id=? ORDER BY created_at DESC, id DESC`, accountID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	return scanMediaOrganizeTaskRows(rows)
}

const selectMediaOrganizeTaskCols = `SELECT id, task_name, account_id, config, status, last_run_at, last_run_result, created_at, updated_at
FROM media_organize_tasks`

const jsonEmptyObject = `{}`

var emptyJSONObject = json.RawMessage(jsonEmptyObject)

func scanMediaOrganizeTaskRows(rows *sql.Rows) ([]*domain.MediaOrganizeTask, error) {
	var out []*domain.MediaOrganizeTask
	for rows.Next() {
		task, err := scanMediaOrganizeTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, wrapDB(rows.Err())
}

func scanMediaOrganizeTask(s rowScanner) (*domain.MediaOrganizeTask, error) {
	var (
		task          domain.MediaOrganizeTask
		config        string
		lastRunResult string
		lastRunAt     sql.NullString
		createdAt     sql.NullString
		updatedAt     sql.NullString
	)
	err := s.Scan(
		&task.ID, &task.TaskName, &task.AccountID, &config, &task.Status,
		&lastRunAt, &lastRunResult, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	task.Config = json.RawMessage(config)
	task.LastRunResult = json.RawMessage(lastRunResult)
	task.LastRunAt = parseTS(lastRunAt)
	task.CreatedAt = parseTS(createdAt)
	task.UpdatedAt = parseTS(updatedAt)
	return &task, nil
}
