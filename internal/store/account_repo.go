package store

import (
	"context"
	"database/sql"
	"strings"

	"litepan/internal/domain"
)

type accountRepo struct{ db *DB }

func (r *accountRepo) Create(ctx context.Context, a *domain.Account) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO cloud_accounts(name, driver_type, config, is_active, is_default, sort_order)
		 VALUES (?,?,?,?,?,?)`,
		a.Name, a.DriverType, a.Config, boolToInt(a.IsActive), boolToInt(a.IsDefault), a.SortOrder)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *accountRepo) Update(ctx context.Context, a *domain.Account) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE cloud_accounts
		 SET name=?, driver_type=?, config=?, is_active=?, is_default=?, sort_order=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		a.Name, a.DriverType, a.Config, boolToInt(a.IsActive), boolToInt(a.IsDefault), a.SortOrder, a.ID)
	return wrapDB(err)
}

func (r *accountRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM cloud_accounts WHERE id=?`, id)
	return wrapDB(err)
}

// SetDefault 将指定账号设为默认，并清除其它账号的默认标记。
func (r *accountRepo) SetDefault(ctx context.Context, id int64) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE cloud_accounts SET is_default=0`); err != nil {
		return wrapDB(err)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE cloud_accounts SET is_default=1, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
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
	return wrapDB(tx.Commit())
}

func (r *accountRepo) Get(ctx context.Context, id int64) (*domain.Account, error) {
	row := r.db.read.QueryRowContext(ctx, selectAccountCols+` WHERE id=?`, id)
	return scanAccount(row)
}

func (r *accountRepo) NameTaken(ctx context.Context, name string, excludeID int64) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil
	}
	var one int
	var err error
	if excludeID > 0 {
		err = r.db.read.QueryRowContext(ctx,
			`SELECT 1 FROM cloud_accounts WHERE LOWER(name)=LOWER(?) AND id<>? LIMIT 1`,
			name, excludeID).Scan(&one)
	} else {
		err = r.db.read.QueryRowContext(ctx,
			`SELECT 1 FROM cloud_accounts WHERE LOWER(name)=LOWER(?) LIMIT 1`,
			name).Scan(&one)
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, wrapDB(err)
	}
	return true, nil
}

func (r *accountRepo) List(ctx context.Context) ([]*domain.Account, error) {
	rows, err := r.db.read.QueryContext(ctx, selectAccountCols+` ORDER BY is_default DESC, sort_order, id`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, wrapDB(rows.Err())
}

const selectAccountCols = `SELECT id,name,driver_type,config,is_active,is_default,sort_order,created_at,updated_at FROM cloud_accounts`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(s rowScanner) (*domain.Account, error) {
	var (
		a              domain.Account
		active, def    int
		created, upded sql.NullString
	)
	err := s.Scan(&a.ID, &a.Name, &a.DriverType, &a.Config, &active, &def, &a.SortOrder, &created, &upded)
	if err != nil {
		return nil, wrapDB(err)
	}
	a.IsActive = active != 0
	a.IsDefault = def != 0
	a.CreatedAt = parseTS(created)
	a.UpdatedAt = parseTS(upded)
	return &a, nil
}
