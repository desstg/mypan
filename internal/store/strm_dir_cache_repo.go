package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"litepan/internal/domain"
)

type strmDirCacheRepo struct{ db *DB }

func (r *strmDirCacheRepo) Get(ctx context.Context, accountID int64, dirID string) (string, bool, error) {
	var path string
	err := r.db.read.QueryRowContext(ctx,
		`SELECT dir_path FROM strm_remote_dir_cache WHERE account_id=? AND dir_id=?`,
		accountID, dirID).Scan(&path)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, wrapDB(err)
	}
	return path, true, nil
}

func (r *strmDirCacheRepo) GetBatch(ctx context.Context, accountID int64, dirIDs []string) (map[string]string, error) {
	out := make(map[string]string)
	if len(dirIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT dir_id, dir_path FROM strm_remote_dir_cache WHERE account_id=? AND dir_id IN (`+placeholders(len(dirIDs))+`)`,
		append([]any{accountID}, stringsToAny(dirIDs)...)...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, wrapDB(err)
		}
		out[id] = path
	}
	return out, wrapDB(rows.Err())
}

func (r *strmDirCacheRepo) UpsertBatch(ctx context.Context, entries []domain.StrmDirCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO strm_remote_dir_cache (account_id, dir_id, dir_path, last_seen_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT(account_id, dir_id) DO UPDATE SET
		   dir_path=excluded.dir_path, last_seen_at=excluded.last_seen_at`)
	if err != nil {
		return wrapDB(err)
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, e.AccountID, e.DirID, e.DirPath, e.LastSeenAt.Unix()); err != nil {
			return wrapDB(err)
		}
	}
	return wrapDB(tx.Commit())
}

func (r *strmDirCacheRepo) ListByPathPrefix(ctx context.Context, accountID int64, prefix string) ([]domain.StrmDirCacheEntry, error) {
	prefix = strings.Trim(prefix, "/")
	escaped := escapeLike(prefix)
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT account_id, dir_id, dir_path, last_seen_at FROM strm_remote_dir_cache
		 WHERE account_id=? AND (dir_path=? OR dir_path LIKE ? ESCAPE '\')`,
		accountID, prefix, escaped+`/%`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []domain.StrmDirCacheEntry
	for rows.Next() {
		var e domain.StrmDirCacheEntry
		var ts int64
		if err := rows.Scan(&e.AccountID, &e.DirID, &e.DirPath, &ts); err != nil {
			return nil, wrapDB(err)
		}
		e.LastSeenAt = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, wrapDB(rows.Err())
}

func (r *strmDirCacheRepo) DeleteByIDs(ctx context.Context, accountID int64, dirIDs []string) (int64, error) {
	if len(dirIDs) == 0 {
		return 0, nil
	}
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM strm_remote_dir_cache WHERE account_id=? AND dir_id IN (`+placeholders(len(dirIDs))+`)`,
		append([]any{accountID}, stringsToAny(dirIDs)...)...)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *strmDirCacheRepo) DeleteByAccount(ctx context.Context, accountID int64) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM strm_remote_dir_cache WHERE account_id=?`, accountID)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *strmDirCacheRepo) CountByAccount(ctx context.Context, accountID int64) (int64, error) {
	var n int64
	if accountID > 0 {
		err := r.db.read.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM strm_remote_dir_cache WHERE account_id=?`, accountID).Scan(&n)
		return n, wrapDB(err)
	}
	err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM strm_remote_dir_cache`).Scan(&n)
	return n, wrapDB(err)
}

func (r *strmDirCacheRepo) DeleteAll(ctx context.Context) (int64, error) {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM strm_remote_dir_cache`)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func escapeLike(s string) string {
	repl := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return repl.Replace(s)
}
