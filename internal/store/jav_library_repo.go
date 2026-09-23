package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"litepan/internal/domain"
)

// ——————————————————————— jav_media_servers ———————————————————————

type javMediaServerRepo struct{ db *DB }

const javServerColumns = `id, name, url, api_key, type, enabled, last_sync_at, last_status,
       last_error, item_count, code_count, created_at, updated_at`

func (r *javMediaServerRepo) Create(ctx context.Context, s *domain.JavMediaServer) (int64, error) {
	if s == nil || strings.TrimSpace(s.URL) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的媒体服务器")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_media_servers(name, url, api_key, type, enabled) VALUES (?,?,?,?,?)
ON CONFLICT(url) DO UPDATE SET
    name=excluded.name, api_key=excluded.api_key, type=excluded.type, enabled=excluded.enabled,
    updated_at=CURRENT_TIMESTAMP`,
		s.Name, s.URL, s.APIKey, defaultStr(s.Type, domain.JavServerTypeEmby), boolToInt(s.Enabled))
	if err != nil {
		return 0, wrapDB(err)
	}
	// ON CONFLICT DO UPDATE 时 LastInsertId 拿到的是上一行的 rowid，
	// 所以统一回查一次，保证重复添加返回的是已有服务器的 id。
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	if existing, gerr := r.GetByURL(ctx, s.URL); gerr == nil {
		return existing.ID, nil
	}
	return id, nil
}

func (r *javMediaServerRepo) Update(ctx context.Context, s *domain.JavMediaServer) error {
	if s == nil || s.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的媒体服务器")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_media_servers SET name=?, url=?, type=?, enabled=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`, s.Name, s.URL, defaultStr(s.Type, domain.JavServerTypeEmby), boolToInt(s.Enabled), s.ID)
	return wrapDB(err)
}

func (r *javMediaServerRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM jav_library_items WHERE server_id=?`, id); err != nil {
		return wrapDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jav_media_servers WHERE id=?`, id); err != nil {
		return wrapDB(err)
	}
	return wrapDB(tx.Commit())
}

func (r *javMediaServerRepo) Get(ctx context.Context, id int64) (*domain.JavMediaServer, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javServerColumns+` FROM jav_media_servers WHERE id=?`, id)
	return scanJavServer(row)
}

func (r *javMediaServerRepo) GetByURL(ctx context.Context, url string) (*domain.JavMediaServer, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+javServerColumns+` FROM jav_media_servers WHERE url=?`, strings.TrimSpace(url))
	return scanJavServer(row)
}

func (r *javMediaServerRepo) List(ctx context.Context) ([]*domain.JavMediaServer, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javServerColumns+` FROM jav_media_servers ORDER BY id ASC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavMediaServer, 0)
	for rows.Next() {
		s, err := scanJavServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, wrapDB(rows.Err())
}

func (r *javMediaServerRepo) MarkSync(ctx context.Context, id int64, at time.Time, status, errMsg string, itemCount, codeCount int) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_media_servers SET last_sync_at=?, last_status=?, last_error=?, item_count=?, code_count=?,
       updated_at=CURRENT_TIMESTAMP
WHERE id=?`, tsValue(at), status, errMsg, itemCount, codeCount, id)
	return wrapDB(err)
}

func scanJavServer(sc javRowScanner) (*domain.JavMediaServer, error) {
	var (
		s          domain.JavMediaServer
		enabled    int
		lastSyncAt sql.NullString
		createdAt  sql.NullString
		updatedAt  sql.NullString
	)
	err := sc.Scan(&s.ID, &s.Name, &s.URL, &s.APIKey, &s.Type, &enabled, &lastSyncAt, &s.LastStatus,
		&s.LastError, &s.ItemCount, &s.CodeCount, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	s.Enabled = enabled != 0
	s.LastSyncAt = parseTS(lastSyncAt)
	s.CreatedAt = parseTS(createdAt)
	s.UpdatedAt = parseTS(updatedAt)
	return &s, nil
}

// ——————————————————————— jav_library_items ———————————————————————

type javLibraryRepo struct{ db *DB }

const javLibraryColumns = `server_id, item_id, code, title, path, resolution, size_bytes, synced_at`

func (r *javLibraryRepo) Upsert(ctx context.Context, it *domain.JavLibraryItem) error {
	if it == nil || it.ServerID <= 0 || strings.TrimSpace(it.ItemID) == "" {
		return domain.Errorf(domain.CodeValidation, "无效的媒体库条目")
	}
	var resolution any
	if it.HasQuality {
		resolution = it.Resolution
	}
	// synced_at 是 NOT NULL：零值写进去会变成 NULL 而撞约束，
	// 这里兜底成当前时间。DeleteMissing 靠它判断「本轮有没有见到这一条」，
	// 一个 NULL 会让那条记录永远删不掉也永远算「刚更新过」。
	syncedAt := it.SyncedAt
	if syncedAt.IsZero() {
		syncedAt = time.Now()
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_library_items(server_id, item_id, code, title, path, resolution, size_bytes, synced_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(server_id, item_id) DO UPDATE SET
    code=excluded.code, title=excluded.title, path=excluded.path,
    -- resolution/size 只在本次带值时覆盖：同步本身读不到 MediaSources，
    -- 无条件写会把质检回填补回来的画质信息每次同步都抹成 NULL。
    resolution=COALESCE(excluded.resolution, jav_library_items.resolution),
    size_bytes=COALESCE(excluded.size_bytes, jav_library_items.size_bytes),
    synced_at=excluded.synced_at`,
		it.ServerID, it.ItemID, it.Code, it.Title, it.Path, resolution,
		optionalInt64(it.SizeBytes, it.SizeBytes > 0), tsValue(syncedAt))
	return wrapDB(err)
}

func (r *javLibraryRepo) DeleteMissing(ctx context.Context, serverID int64, seenAt time.Time) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM jav_library_items WHERE server_id=? AND synced_at < ?`, serverID, tsValue(seenAt))
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *javLibraryRepo) ListByServer(ctx context.Context, serverID int64, limit, offset int) ([]*domain.JavLibraryItem, int, error) {
	var total int
	if err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jav_library_items WHERE server_id=?`, serverID).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javLibraryColumns+` FROM jav_library_items WHERE server_id=? ORDER BY code ASC LIMIT ? OFFSET ?`,
		serverID, clampLimit(limit, 100), maxInt(offset, 0))
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavLibraryItem, 0)
	for rows.Next() {
		it, err := scanJavLibraryItem(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, wrapDB(rows.Err())
}

// ListPendingQuality 返回还没质检出的条目，供后台回填消费。
func (r *javLibraryRepo) ListPendingQuality(ctx context.Context, limit int) ([]*domain.JavLibraryItem, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javLibraryColumns+` FROM jav_library_items WHERE resolution IS NULL LIMIT ?`, clampLimit(limit, 25))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavLibraryItem, 0)
	for rows.Next() {
		it, err := scanJavLibraryItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, wrapDB(rows.Err())
}

// Codes 返回库里出现过的全部番号（去重），空串不算 ——
// 提不出番号的条目放在里面会让「已入库」对任何空番号影片都为真。
func (r *javLibraryRepo) Codes(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT DISTINCT code FROM jav_library_items WHERE code <> ''`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, wrapDB(err)
		}
		out[code] = struct{}{}
	}
	return out, wrapDB(rows.Err())
}

// QualityMap 取每个番号在库里的最高画质。
//
// 同一番号可能散在多台服务器、多个条目里，取 max 是因为洗版判定问的是
// 「库里已经有更好的了吗」—— 只要有一份是超清，就不该再推一颗普通高清。
func (r *javLibraryRepo) QualityMap(ctx context.Context) (map[string]domain.JavLibraryQuality, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT code, MAX(COALESCE(resolution, 0)) AS res, MAX(COALESCE(size_bytes, 0)) AS sz
FROM jav_library_items WHERE code <> '' GROUP BY code`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make(map[string]domain.JavLibraryQuality)
	for rows.Next() {
		var (
			code string
			q    domain.JavLibraryQuality
		)
		if err := rows.Scan(&code, &q.Resolution, &q.SizeBytes); err != nil {
			return nil, wrapDB(err)
		}
		out[code] = q
	}
	return out, wrapDB(rows.Err())
}

func (r *javLibraryRepo) Lookup(ctx context.Context, code string) ([]*domain.JavLibraryItem, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javLibraryColumns+` FROM jav_library_items WHERE code=? COLLATE NOCASE ORDER BY server_id`,
		strings.TrimSpace(code))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavLibraryItem, 0)
	for rows.Next() {
		it, err := scanJavLibraryItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, wrapDB(rows.Err())
}

// Stats 返回每台服务器的计数，以及去重番号总数与总条目数。
// CountMoviesInLibrary 数出影片表里番号能在媒体库中命中的影片数。
//
// 比对方式与角标**完全一致**：精确字符串相等（toCards 里就是
// `inLibrary[m.Number]` 这么查的）。这里若自作主张加个大小写归一，
// 这一格的数字就会和卡片上的角标对不上 —— 用户一眼能看出来。
func (r *javLibraryRepo) CountMoviesInLibrary(ctx context.Context) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `
SELECT COUNT(*) FROM jav_movies m
WHERE m.number <> ''
  AND EXISTS (SELECT 1 FROM jav_library_items li WHERE li.code = m.number)`).Scan(&n)
	return n, wrapDB(err)
}

func (r *javLibraryRepo) Stats(ctx context.Context) ([]*domain.JavLibraryStats, int, int, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT s.id, s.name, s.type, s.url, s.last_sync_at, s.last_status, s.last_error,
       COUNT(li.item_id) AS items,
       COUNT(DISTINCT CASE WHEN li.code <> '' THEN li.code END) AS codes
FROM jav_media_servers s
LEFT JOIN jav_library_items li ON li.server_id = s.id
GROUP BY s.id ORDER BY s.id`)
	if err != nil {
		return nil, 0, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavLibraryStats, 0)
	totalItems := 0
	for rows.Next() {
		var (
			st         domain.JavLibraryStats
			lastSyncAt sql.NullString
		)
		if err := rows.Scan(&st.ServerID, &st.Name, &st.Type, &st.URL, &lastSyncAt,
			&st.LastStatus, &st.LastError, &st.ItemCount, &st.DistinctCodes); err != nil {
			return nil, 0, 0, wrapDB(err)
		}
		st.LastSyncAt = parseTS(lastSyncAt)
		st.CodeCount = st.DistinctCodes
		totalItems += st.ItemCount
		out = append(out, &st)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, wrapDB(err)
	}

	// 总数是**全局去重**的，不是各服务器之和 —— 同一部片在两台服务器上都入库时，
	// 相加会把「一共收藏了多少部」报大。
	var totalCodes int
	if err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT code) FROM jav_library_items WHERE code <> ''`).Scan(&totalCodes); err != nil {
		return nil, 0, 0, wrapDB(err)
	}
	return out, totalCodes, totalItems, nil
}

func (r *javLibraryRepo) DeleteByServer(ctx context.Context, serverID int64) (int64, error) {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM jav_library_items WHERE server_id=?`, serverID)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func scanJavLibraryItem(sc javRowScanner) (*domain.JavLibraryItem, error) {
	var (
		it         domain.JavLibraryItem
		resolution sql.NullInt64
		sizeBytes  sql.NullInt64
		syncedAt   sql.NullString
	)
	err := sc.Scan(&it.ServerID, &it.ItemID, &it.Code, &it.Title, &it.Path, &resolution, &sizeBytes, &syncedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	if resolution.Valid {
		it.Resolution, it.HasQuality = int(resolution.Int64), true
	}
	if sizeBytes.Valid {
		it.SizeBytes = sizeBytes.Int64
	}
	it.SyncedAt = parseTS(syncedAt)
	return &it, nil
}

// ——————————————————————— jav_push_records ———————————————————————

type javPushRecordRepo struct{ db *DB }

const javPushColumns = `id, magnet, name, size_text, movie_id, code, status, downloader, provider_kind,
       account_id, target_path, offline_task_id, subscription_id, error, source, pushed_at, created_at, updated_at`

func (r *javPushRecordRepo) Create(ctx context.Context, rec *domain.JavPushRecord) (int64, error) {
	if rec == nil || strings.TrimSpace(rec.Magnet) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的推送记录")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_push_records(magnet, name, size_text, movie_id, code, status, downloader, provider_kind,
       account_id, target_path, offline_task_id, subscription_id, source)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.Magnet, rec.Name, rec.SizeText, rec.MovieID, rec.Code,
		defaultStr(rec.Status, domain.JavPushPending), rec.Downloader, rec.ProviderKind,
		rec.AccountID, rec.TargetPath, rec.OfflineTaskID, rec.SubscriptionID, rec.Source)
	if err != nil {
		return 0, wrapDB(err)
	}
	return res.LastInsertId()
}

func (r *javPushRecordRepo) Get(ctx context.Context, id int64) (*domain.JavPushRecord, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javPushColumns+` FROM jav_push_records WHERE id=?`, id)
	return scanJavPushRecord(row)
}

// Update 只用于重推时刷新目标信息这类小幅改动，状态请走 SetStatus。
//
// 刻意**不写 source**：重推复用的是同一条记录、同一颗候选，来源不会变，
// 写它只是多一次无意义的覆盖（见 0035 迁移的注释）。
func (r *javPushRecordRepo) Update(ctx context.Context, rec *domain.JavPushRecord) error {
	if rec == nil || rec.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的推送记录")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_push_records SET magnet=?, name=?, size_text=?, movie_id=?, code=?, downloader=?,
       provider_kind=?, account_id=?, target_path=?, offline_task_id=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`,
		rec.Magnet, rec.Name, rec.SizeText, rec.MovieID, rec.Code, rec.Downloader,
		rec.ProviderKind, rec.AccountID, rec.TargetPath, rec.OfflineTaskID, rec.ID)
	return wrapDB(err)
}

func (r *javPushRecordRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM jav_push_records WHERE id=?`, id)
	return wrapDB(err)
}

func (r *javPushRecordRepo) DeleteBatch(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM jav_push_records WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func (r *javPushRecordRepo) List(ctx context.Context, f domain.JavPushRecordFilter) ([]*domain.JavPushRecord, int, error) {
	conds := make([]string, 0, 5)
	params := make([]any, 0, 5)
	if f.Status != "" {
		conds = append(conds, `status = ?`)
		params = append(params, f.Status)
	}
	if f.Downloader != "" {
		conds = append(conds, `downloader = ?`)
		params = append(params, f.Downloader)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		conds = append(conds, `(code LIKE ? OR name LIKE ? OR magnet LIKE ?)`)
		like := "%" + kw + "%"
		params = append(params, like, like, like)
	}
	// ⚠️ 日期区间用 `date(created_at, 'localtime')` 比，**不能直接比字符串**。
	//
	// 库里存的是 **UTC**（写入走 tsValue 的 t.UTC()，DDL 默认值 SQLite 的
	// CURRENT_TIMESTAMP 也是 UTC），而界面上那两个日期选择器给的是**本地日期**。
	// 直接比会差一个时区：东八区的用户**上午 8 点前**推的东西，UTC 日期还是昨天，
	// 于是被「今天」这个默认筛选整批过滤掉 —— 现象就是「推了、网盘也有文件、
	// 下载记录里却是空的」（2026-09-22 实测，四个 06:52~06:59 推的都这样）。
	// `date(..., 'localtime')` 把存的值先转成本地日期再比，两边才是同一种东西。
	if f.From != "" {
		conds = append(conds, `date(created_at, 'localtime') >= date(?)`)
		params = append(params, f.From)
	}
	if f.To != "" {
		conds = append(conds, `date(created_at, 'localtime') <= date(?)`)
		params = append(params, f.To)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_push_records`+where, params...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javPushColumns+` FROM jav_push_records`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(params, clampLimit(f.Limit, 50), maxInt(f.Offset, 0))...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavPushRecord, 0)
	for rows.Next() {
		rec, err := scanJavPushRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rec)
	}
	return out, total, wrapDB(rows.Err())
}

// Downloaders 返回出现过的下载器标签，供记录页的筛选下拉使用。
func (r *javPushRecordRepo) Downloaders(ctx context.Context) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT DISTINCT downloader FROM jav_push_records WHERE downloader <> '' ORDER BY downloader`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, d)
	}
	return out, wrapDB(rows.Err())
}

func (r *javPushRecordRepo) SetStatus(ctx context.Context, id int64, status, errMsg string, at time.Time) error {
	var pushedAt any
	if status == domain.JavPushPushed {
		pushedAt = tsValue(at)
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_push_records SET status=?, error=?, pushed_at=COALESCE(?, pushed_at), updated_at=CURRENT_TIMESTAMP
WHERE id=?`, status, errMsg, pushedAt, id)
	return wrapDB(err)
}

func (r *javPushRecordRepo) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_push_records SET status='failed', error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, errMsg, id)
	return wrapDB(err)
}

// CountPushedBySubscription 按订阅分组统计推送影片数（Pushed / Pending 分开数）。
//
// 一次查完所有订阅：订阅页一屏几十张卡，逐条查就是几十次往返。
// 分组统计的键是 movie_id 去重 —— 同一部片重推过只算一部。
//
// Pending 要单独给一个数：从「提交成功」到「下载完成」之间有一段窗口，这段时间
// 卡片若只显示 Pushed，用户看到的就是「推：0」——而他明明刚点了推送。
func (r *javPushRecordRepo) CountPushedBySubscription(ctx context.Context, ids []int64) (map[int64]domain.JavPushCounts, error) {
	out := make(map[int64]domain.JavPushCounts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")

	// 参数顺序**必须与 SQL 里占位符出现的顺序一致**：两个 CASE 在 SELECT 里、
	// ids 在 WHERE 里，所以状态在前、ids 在后。反过来的话位置绑定会整体错位
	// （status = <某个订阅 id>），查询不报错，只是每一行都数成 0 ——
	// 卡片上「推/下」全是 0 就是这么来的。
	args := make([]any, 0, len(ids)+2)
	args = append(args, domain.JavPushPushed, domain.JavPushPending)
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := r.db.read.QueryContext(ctx,
		`SELECT subscription_id,
		        COUNT(DISTINCT CASE WHEN status = ? THEN movie_id END),
		        COUNT(DISTINCT CASE WHEN status = ? THEN movie_id END)
		   FROM jav_push_records
		  WHERE subscription_id IN (`+placeholders+`)
		  GROUP BY subscription_id`, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c domain.JavPushCounts
		if err := rows.Scan(&id, &c.Pushed, &c.Pending); err != nil {
			return nil, wrapDB(err)
		}
		out[id] = c
	}
	return out, wrapDB(rows.Err())
}

// PushedMagnets 列出某个番号下推送过的磁链（成功与在途）。
//
// **逐颗**返回而不是一个布尔：一部影片几十颗磁链，而用户点的是其中一颗。
// 整片口径（「这部片推成功过没有」）盖到每一颗上，就等于每颗都标着「已推送」。
//
// 不过滤 failed：一颗试过没成的资源和从没推过在详情页上是同一个答案 ——
// 想推就再点一次，角标上多挂一个「失败」只会让早就翻篇的记录赖着不走。
// 于是同一颗可能同时有失败的和新的记录（失败后重试会新开一条，而不覆盖旧的），
// 所以调用方按 pushed 优先处理。
func (r *javPushRecordRepo) PushedMagnets(ctx context.Context, code string) ([]domain.JavMagnetPushState, error) {
	if strings.TrimSpace(code) == "" {
		return nil, nil
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT magnet, name, status FROM jav_push_records
		  WHERE code=? AND status IN (?, ?)`,
		code, domain.JavPushPushed, domain.JavPushPending)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	var out []domain.JavMagnetPushState
	for rows.Next() {
		var st domain.JavMagnetPushState
		if err := rows.Scan(&st.Magnet, &st.Name, &st.Status); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, st)
	}
	return out, wrapDB(rows.Err())
}

func scanJavPushRecord(sc javRowScanner) (*domain.JavPushRecord, error) {
	var (
		rec       domain.JavPushRecord
		pushedAt  sql.NullString
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	err := sc.Scan(&rec.ID, &rec.Magnet, &rec.Name, &rec.SizeText, &rec.MovieID, &rec.Code, &rec.Status,
		&rec.Downloader, &rec.ProviderKind, &rec.AccountID, &rec.TargetPath, &rec.OfflineTaskID,
		&rec.SubscriptionID, &rec.Error, &rec.Source, &pushedAt, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	rec.PushedAt = parseTS(pushedAt)
	rec.CreatedAt = parseTS(createdAt)
	rec.UpdatedAt = parseTS(updatedAt)
	return &rec, nil
}
