package store

import (
	"context"
	"database/sql"
	"errors"
)

type configRepo struct{ db *DB }

func (r *configRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.read.QueryRowContext(ctx, `SELECT value FROM configs WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, wrapDB(err)
	}
	return v, true, nil
}

func (r *configRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.write.ExecContext(ctx,
		`INSERT INTO configs(key,value,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
		key, value)
	return wrapDB(err)
}

func (r *configRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.read.QueryContext(ctx, `SELECT key,value FROM configs`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, wrapDB(err)
		}
		m[k] = v
	}
	return m, wrapDB(rows.Err())
}
