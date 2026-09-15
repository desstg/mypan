package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"litepan/internal/domain"
)

type quarktvBindingRepo struct{ db *DB }

func (r *quarktvBindingRepo) Get(ctx context.Context, accountID int64) (*domain.QuarkTVBinding, error) {
	var (
		b         domain.QuarkTVBinding
		expiresAt int64
		boundAt   int64
	)
	err := r.db.read.QueryRowContext(ctx,
		`SELECT account_id, device_id, refresh_token, access_token, token_expires_at, tv_uid, tv_nickname, preferred_resolution, allow_dolby, bound_at
		 FROM quarktv_bindings WHERE account_id=?`, accountID).
		Scan(&b.AccountID, &b.DeviceID, &b.RefreshToken, &b.AccessToken, &expiresAt, &b.TVUID, &b.TVNickname, &b.PreferredResolution, &b.AllowDolby, &boundAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapDB(err)
	}
	b.TokenExpiresAt = time.Unix(expiresAt, 0)
	b.PreferredResolution = domain.NormalizeQuarkTVResolution(b.PreferredResolution)
	b.BoundAt = time.Unix(boundAt, 0)
	return &b, nil
}

func (r *quarktvBindingRepo) Upsert(ctx context.Context, b *domain.QuarkTVBinding) error {
	preferred := domain.NormalizeQuarkTVResolution(b.PreferredResolution)
	b.PreferredResolution = preferred
	_, err := r.db.write.ExecContext(ctx,
		`INSERT INTO quarktv_bindings (account_id, device_id, refresh_token, access_token, token_expires_at, tv_uid, tv_nickname, preferred_resolution, allow_dolby, bound_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(account_id) DO UPDATE SET
		   device_id=excluded.device_id,
		   refresh_token=excluded.refresh_token,
		   access_token=excluded.access_token,
		   token_expires_at=excluded.token_expires_at,
		   tv_uid=excluded.tv_uid,
		   tv_nickname=excluded.tv_nickname,
		   preferred_resolution=excluded.preferred_resolution,
		   allow_dolby=excluded.allow_dolby,
		   bound_at=excluded.bound_at`,
		b.AccountID, b.DeviceID, b.RefreshToken, b.AccessToken, b.TokenExpiresAt.Unix(), b.TVUID, b.TVNickname, preferred, boolToInt(b.AllowDolby), b.BoundAt.Unix())
	return wrapDB(err)
}

func (r *quarktvBindingRepo) Delete(ctx context.Context, accountID int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM quarktv_bindings WHERE account_id=?`, accountID)
	return wrapDB(err)
}
