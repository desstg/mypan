package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type notificationRepo struct{ db *DB }

func (r *notificationRepo) Create(ctx context.Context, n *domain.Notification) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO notifications(level, category, title, message, account_id, ref_id, is_read)
		 VALUES (?,?,?,?,?,?,?)`,
		n.Level, n.Category, n.Title, n.Message, n.AccountID, n.RefID, boolToInt(n.IsRead))
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *notificationRepo) List(ctx context.Context, limit, offset int) ([]*domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT id, level, category, title, message, account_id, ref_id, is_read, created_at
		 FROM notifications
		 ORDER BY created_at DESC, id DESC
		 LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, wrapDB(rows.Err())
}

func (r *notificationRepo) UnreadCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE is_read=0`).Scan(&n)
	return n, wrapDB(err)
}

func (r *notificationRepo) MarkRead(ctx context.Context, id int64) error {
	res, err := r.db.write.ExecContext(ctx,
		`UPDATE notifications SET is_read=1 WHERE id=? AND is_read=0`, id)
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

func (r *notificationRepo) MarkAllRead(ctx context.Context) (int64, error) {
	res, err := r.db.write.ExecContext(ctx, `UPDATE notifications SET is_read=1 WHERE is_read=0`)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *notificationRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM notifications WHERE id=?`, id)
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

func (r *notificationRepo) DeleteAll(ctx context.Context) (int64, error) {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM notifications`)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *notificationRepo) DeleteByRef(ctx context.Context, category string, refID int64) (int64, error) {
	if refID <= 0 {
		return 0, nil
	}
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM notifications WHERE category=? AND ref_id=?`, category, refID)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func scanNotification(s rowScanner) (*domain.Notification, error) {
	var n domain.Notification
	var isRead int
	var created sql.NullString
	if err := s.Scan(
		&n.ID, &n.Level, &n.Category, &n.Title, &n.Message, &n.AccountID, &n.RefID, &isRead, &created,
	); err != nil {
		return nil, wrapDB(err)
	}
	n.IsRead = isRead != 0
	n.CreatedAt = parseTS(created)
	return &n, nil
}
