package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type authStateRepo struct{ db *DB }

func (r *authStateRepo) Get(ctx context.Context, accountID int64) (*domain.AuthState, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT account_id,status,access_token,refresh_token,token_expires,cookie,cookie_expires,
		        active_attempts,passive_attempts,last_error,last_failure_kind,next_retry_at,last_refresh_at,last_notified_at
		 FROM account_auth_states WHERE account_id=?`, accountID)

	var (
		s            domain.AuthState
		status       string
		tokenExp     sql.NullString
		cookieExp    sql.NullString
		nextRetry    sql.NullString
		lastRefresh  sql.NullString
		lastNotified sql.NullString
		failureKind  sql.NullString
	)
	err := row.Scan(&s.AccountID, &status, &s.AccessToken, &s.RefreshToken, &tokenExp,
		&s.Cookie, &cookieExp, &s.ActiveAttempts, &s.PassiveAttempts, &s.LastError, &failureKind,
		&nextRetry, &lastRefresh, &lastNotified)
	if err != nil {
		return nil, wrapDB(err)
	}
	s.Status = domain.AuthStatus(status)
	s.LastFailureKind = domain.AuthFailureKind(failureKind.String)
	s.TokenExpires = parseTS(tokenExp)
	s.CookieExpires = parseTS(cookieExp)
	s.NextRetryAt = parseTS(nextRetry)
	s.LastRefreshAt = parseTS(lastRefresh)
	s.LastNotifiedAt = parseTS(lastNotified)
	return &s, nil
}

func (r *authStateRepo) Upsert(ctx context.Context, s *domain.AuthState) error {
	status := s.Status
	if status == "" {
		status = domain.AuthActive
	}
	_, err := r.db.write.ExecContext(ctx,
		`INSERT INTO account_auth_states
		   (account_id,status,access_token,refresh_token,token_expires,cookie,cookie_expires,
		    active_attempts,passive_attempts,last_error,last_failure_kind,next_retry_at,last_refresh_at,last_notified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(account_id) DO UPDATE SET
		    status=excluded.status, access_token=excluded.access_token, refresh_token=excluded.refresh_token,
		    token_expires=excluded.token_expires, cookie=excluded.cookie, cookie_expires=excluded.cookie_expires,
		    active_attempts=excluded.active_attempts, passive_attempts=excluded.passive_attempts,
		    last_error=excluded.last_error, last_failure_kind=excluded.last_failure_kind,
		    next_retry_at=excluded.next_retry_at,
		    last_refresh_at=excluded.last_refresh_at, last_notified_at=excluded.last_notified_at`,
		s.AccountID, string(status), s.AccessToken, s.RefreshToken, tsValue(s.TokenExpires),
		s.Cookie, tsValue(s.CookieExpires), s.ActiveAttempts, s.PassiveAttempts, s.LastError,
		string(s.LastFailureKind), tsValue(s.NextRetryAt), tsValue(s.LastRefreshAt), tsValue(s.LastNotifiedAt))
	return wrapDB(err)
}

func (r *authStateRepo) Delete(ctx context.Context, accountID int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM account_auth_states WHERE account_id=?`, accountID)
	return wrapDB(err)
}
