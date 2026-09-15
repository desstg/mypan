package store

import (
	"context"
	"database/sql"
	"strings"

	"litepan/internal/domain"
)

type fuseMountRepo struct{ db *DB }

const fuseMountCols = `id,name,account_id,root_item_id,root_path,mount_point,read_only,auto_mount,uid,gid,dir_mode,file_mode,enabled,state,last_error,sort_order,created_at,updated_at`

func (r *fuseMountRepo) Create(ctx context.Context, m *domain.FuseMount) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO fuse_mounts
		  (name,account_id,root_item_id,root_path,mount_point,read_only,auto_mount,uid,gid,dir_mode,file_mode,enabled,state,last_error,sort_order)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Name, m.AccountID, m.RootItemID, m.RootPath, m.MountPoint,
		boolToInt(m.ReadOnly), boolToInt(m.AutoMount), m.UID, m.GID, m.DirMode, m.FileMode,
		boolToInt(m.Enabled), m.State, m.LastError, m.SortOrder)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *fuseMountRepo) Update(ctx context.Context, m *domain.FuseMount) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE fuse_mounts SET
		   name=?,account_id=?,root_item_id=?,root_path=?,mount_point=?,read_only=?,auto_mount=?,
		   uid=?,gid=?,dir_mode=?,file_mode=?,enabled=?,sort_order=?,updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		m.Name, m.AccountID, m.RootItemID, m.RootPath, m.MountPoint,
		boolToInt(m.ReadOnly), boolToInt(m.AutoMount),
		m.UID, m.GID, m.DirMode, m.FileMode, boolToInt(m.Enabled), m.SortOrder, m.ID)
	return wrapDB(err)
}

func (r *fuseMountRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM fuse_mounts WHERE id=?`, id)
	return wrapDB(err)
}

func (r *fuseMountRepo) Get(ctx context.Context, id int64) (*domain.FuseMount, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+fuseMountCols+` FROM fuse_mounts WHERE id=?`, id)
	return scanFuseMount(row)
}

func (r *fuseMountRepo) List(ctx context.Context) ([]*domain.FuseMount, error) {
	rows, err := r.db.read.QueryContext(ctx, `SELECT `+fuseMountCols+` FROM fuse_mounts ORDER BY sort_order,id`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.FuseMount
	for rows.Next() {
		m, err := scanFuseMount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *fuseMountRepo) UpdateRuntime(ctx context.Context, id int64, state, lastError string) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE fuse_mounts SET state=?,last_error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		state, lastError, id)
	return wrapDB(err)
}

func (r *fuseMountRepo) MountPointTaken(ctx context.Context, mountPoint string, excludeID int64) (bool, error) {
	mountPoint = strings.TrimSpace(mountPoint)
	var id int64
	err := r.db.read.QueryRowContext(ctx,
		`SELECT id FROM fuse_mounts WHERE mount_point=? AND id<>? LIMIT 1`, mountPoint, excludeID).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, wrapDB(err)
	}
	return true, nil
}

func scanFuseMount(row interface{ Scan(...any) error }) (*domain.FuseMount, error) {
	var m domain.FuseMount
	var readOnly, autoMount, enabled int
	var created, updated sql.NullString
	err := row.Scan(
		&m.ID, &m.Name, &m.AccountID, &m.RootItemID, &m.RootPath, &m.MountPoint,
		&readOnly, &autoMount, &m.UID, &m.GID, &m.DirMode, &m.FileMode, &enabled,
		&m.State, &m.LastError, &m.SortOrder, &created, &updated,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	m.ReadOnly = readOnly != 0
	m.AutoMount = autoMount != 0
	m.Enabled = enabled != 0
	m.CreatedAt = parseTS(created)
	m.UpdatedAt = parseTS(updated)
	return &m, nil
}
