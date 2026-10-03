package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type notificationRepo struct{ db *DB }

func (r *notificationRepo) Create(ctx context.Context, n *domain.Notification) (int64, error) {
	first := n.FirstAt
	if first.IsZero() {
		first = n.CreatedAt
	}
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO notifications(level, category, title, message, account_id, ref_id, is_read, count, first_at)
		 VALUES (?,?,?,?,?,?,?,?,COALESCE(?, CURRENT_TIMESTAMP))`,
		n.Level, n.Category, n.Title, n.Message, n.AccountID, n.RefID, boolToInt(n.IsRead),
		notificationCount(n), tsValue(first))
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

// CreateOrMerge 落一条通知，同键的**未读**行合并而不是新增。
//
// 判重键是 (category, ref_id, title, message) 四元组：
//   - 带上 message 是**有意**的 —— 正文一变就是新情况（待删目录 28 → 30），
//     合并掉就等于把「情况变了」这件事本身丢了。
//   - 只认未读行：读掉之后再发生要重新亮红点，否则反复发生的告警会被永久静音。
//
// 合并时把 created_at 推到当前（列表按它倒序，反复发生的问题要一直浮在顶部），
// first_at 保持第一次的时间不动。
//
// 用一条带条件的 UPDATE 先试，命中就直接返回 —— 这样不会出现「查完再插」
// 之间被另一条通知插进来的竞态（写池是单连接，但 UPDATE 本身也是原子的）。
func (r *notificationRepo) CreateOrMerge(ctx context.Context, n *domain.Notification) (int64, bool, error) {
	first := n.FirstAt
	if first.IsZero() {
		first = n.CreatedAt
	}
	res, err := r.db.write.ExecContext(ctx,
		`UPDATE notifications
		    SET count = count + 1, created_at = CURRENT_TIMESTAMP
		  WHERE is_read = 0 AND category = ? AND ref_id = ? AND title = ? AND message = ?`,
		n.Category, n.RefID, n.Title, n.Message)
	if err != nil {
		return 0, false, wrapDB(err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected > 0 {
		// 取回被合并那一行的 id：前端按 id 删除/已读，合并后列表里只有这一行。
		var id int64
		if qerr := r.db.write.QueryRowContext(ctx,
			`SELECT id FROM notifications
			  WHERE is_read = 0 AND category = ? AND ref_id = ? AND title = ? AND message = ?
			  ORDER BY created_at DESC, id DESC LIMIT 1`,
			n.Category, n.RefID, n.Title, n.Message).Scan(&id); qerr == nil {
			return id, true, nil
		}
		return 0, true, nil
	}
	id, err := r.Create(ctx, n)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// notificationCount 取这次要写的次数：调用方没设（零值）时按 1 算。
func notificationCount(n *domain.Notification) int {
	if n.Count <= 0 {
		return 1
	}
	return n.Count
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
		`SELECT id, level, category, title, message, account_id, ref_id, is_read, count, first_at, created_at
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
	var first sql.NullString
	if err := s.Scan(
		&n.ID, &n.Level, &n.Category, &n.Title, &n.Message, &n.AccountID, &n.RefID, &isRead,
		&n.Count, &first, &created,
	); err != nil {
		return nil, wrapDB(err)
	}
	n.IsRead = isRead != 0
	n.FirstAt = parseTS(first)
	n.CreatedAt = parseTS(created)
	return &n, nil
}
