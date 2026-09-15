package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"litepan/internal/domain"
)

type apiKeyRepo struct{ db *DB }

func (r *apiKeyRepo) List(ctx context.Context) ([]*domain.ApiKey, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT id,name,key_hash,key_prefix,key_suffix,key_type,status,expires_at,last_used_at,note,created_at,updated_at
		 FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.ApiKey
	for rows.Next() {
		k, err := scanApiKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, wrapDB(rows.Err())
}

func (r *apiKeyRepo) Get(ctx context.Context, id int64) (*domain.ApiKey, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT id,name,key_hash,key_prefix,key_suffix,key_type,status,expires_at,last_used_at,note,created_at,updated_at
		 FROM api_keys WHERE id=?`, id)
	k, err := scanApiKey(row)
	if err != nil {
		return nil, wrapDB(err)
	}
	return k, nil
}

func (r *apiKeyRepo) GetByHash(ctx context.Context, keyHash string) (*domain.ApiKey, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT id,name,key_hash,key_prefix,key_suffix,key_type,status,expires_at,last_used_at,note,created_at,updated_at
		 FROM api_keys WHERE key_hash=?`, keyHash)
	k, err := scanApiKey(row)
	if err != nil {
		return nil, wrapDB(err)
	}
	return k, nil
}

func (r *apiKeyRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&n)
	return n, wrapDB(err)
}

func (r *apiKeyRepo) Create(ctx context.Context, key *domain.ApiKey) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO api_keys(name,key_hash,key_prefix,key_suffix,key_type,status,expires_at,note,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`,
		key.Name, key.KeyHash, key.KeyPrefix, key.KeySuffix, key.KeyType, key.Status,
		tsValue(key.ExpiresAt), key.Note)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *apiKeyRepo) Update(ctx context.Context, key *domain.ApiKey) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE api_keys SET name=?,key_type=?,status=?,expires_at=?,note=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		key.Name, key.KeyType, key.Status, tsValue(key.ExpiresAt), key.Note, key.ID)
	return wrapDB(err)
}

func (r *apiKeyRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
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

func (r *apiKeyRepo) TouchLastUsed(ctx context.Context, id int64, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		tsValue(at), id)
	return wrapDB(err)
}

type apiKeyScanner interface {
	Scan(dest ...any) error
}

func scanApiKey(s apiKeyScanner) (*domain.ApiKey, error) {
	var k domain.ApiKey
	var expires, lastUsed, created, updated sql.NullString
	if err := s.Scan(
		&k.ID, &k.Name, &k.KeyHash, &k.KeyPrefix, &k.KeySuffix, &k.KeyType, &k.Status,
		&expires, &lastUsed, &k.Note, &created, &updated,
	); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, domain.Errf(domain.CodeNotFound)
		}
		return nil, err
	}
	k.ExpiresAt = parseTS(expires)
	k.LastUsedAt = parseTS(lastUsed)
	k.CreatedAt = parseTS(created)
	k.UpdatedAt = parseTS(updated)
	return &k, nil
}
