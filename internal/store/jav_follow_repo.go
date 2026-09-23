package store

import (
	"context"
	"database/sql"
	"strings"

	"litepan/internal/domain"
)

// ——————————————————————— jav_follows ———————————————————————

type javFollowRepo struct{ db *DB }

// Upsert 关注一个分享者。重复关注是幂等的，顺带把用户名刷新一遍
// （对方改过名字的话，本地这份也该跟上）。
func (r *javFollowRepo) Upsert(ctx context.Context, userID int64, username string) error {
	if userID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的用户 id")
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_follows(user_id, username) VALUES (?,?)
ON CONFLICT(user_id) DO UPDATE SET username=excluded.username`,
		userID, strings.TrimSpace(username))
	return wrapDB(err)
}

func (r *javFollowRepo) Delete(ctx context.Context, userID int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM jav_follows WHERE user_id=?`, userID)
	return wrapDB(err)
}

func (r *javFollowRepo) List(ctx context.Context) ([]*domain.JavFollow, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT user_id, username, created_at FROM jav_follows ORDER BY created_at DESC, user_id DESC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavFollow, 0)
	for rows.Next() {
		var (
			f         domain.JavFollow
			createdAt sql.NullString
		)
		if err := rows.Scan(&f.UserID, &f.Username, &createdAt); err != nil {
			return nil, wrapDB(err)
		}
		f.CreatedAt = parseTS(createdAt)
		out = append(out, &f)
	}
	return out, wrapDB(rows.Err())
}

// ShareCounts 数「这几个分享者各自贴过链接的评论有几条」。
//
// 用 LIKE 粗筛而不是把每条评论取回来跑正则：这一档可能有几十个关注的人，
// 逐个跑 commentlink.Extract 是几十次全表扫描。LIKE 的漏网之鱼只有
// 「链接被换行截断」这类极端情况，代价可接受 —— 对比之下，
// 为了计数去跑一遍完整提取不划算。
//
// **必须限定 userIDs**：不限定就是对整张 jav_reviews 做两个 `%…%` 的全表扫描
// 再分组（实测 74750 行要 576ms），而调用方要的只是已关注那几个人。限定之后
// 走 idx_jav_reviews_user（见 0032 迁移），同一份数据 7ms。
// 返回值里没有的 user_id 就是「一条都没贴过」，调用方按 0 处理即可。
func (r *javFollowRepo) ShareCounts(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int)
	if len(userIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(userIDs)), ",")
	args := make([]any, 0, len(userIDs))
	for _, id := range userIDs {
		args = append(args, id)
	}

	rows, err := r.db.read.QueryContext(ctx, `
SELECT user_id, COUNT(*) FROM jav_reviews
 WHERE user_id IN (`+placeholders+`) AND (content LIKE '%magnet:%' OR content LIKE '%ed2k:%')
 GROUP BY user_id`, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			userID int64
			n      int
		)
		if err := rows.Scan(&userID, &n); err != nil {
			return nil, wrapDB(err)
		}
		out[userID] = n
	}
	return out, wrapDB(rows.Err())
}
