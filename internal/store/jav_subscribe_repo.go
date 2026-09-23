package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
)

// ——————————————————————— jav_subscriptions ———————————————————————

type javSubscriptionRepo struct{ db *DB }

const javSubColumns = `id, target_type, target_id, target_url, target_key, target_name, status,
       download_mode, pre_download, include_comment_links, qualities_json, min_size_mb, max_size_mb, max_file_count,
       release_date_from, release_date_to, expiry_days, categories_json, exclude_categories_json,
       enabled, target_account_id, target_parent_id, target_display_path, push_provider, subfolder_mode,
       last_checked_at, last_push_at, completed_at, last_error, matched_count, created_at, updated_at`

func (r *javSubscriptionRepo) Create(ctx context.Context, s *domain.JavSubscription) (int64, error) {
	if s == nil || strings.TrimSpace(s.TargetKey) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的订阅")
	}
	// status / download_mode / subfolder_mode 都有 CHECK 约束，空串会直接撞上去。
	// 在仓储层兜底而不是靠调用方记得设：新建订阅没写状态是调用方最自然的样子，
	// 让它以一句 SQLite 的 constraint failed 收场实在没必要。
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscriptions(target_type, target_id, target_url, target_key, target_name, status,
       download_mode, pre_download, include_comment_links, qualities_json, min_size_mb, max_size_mb, max_file_count,
       release_date_from, release_date_to, expiry_days, categories_json, exclude_categories_json,
       enabled, target_account_id, target_parent_id, target_display_path, push_provider, subfolder_mode)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.TargetType, s.TargetID, s.TargetURL, s.TargetKey, s.TargetName,
		defaultStr(s.Status, domain.JavSubStatusActive),
		defaultStr(s.DownloadMode, domain.JavDownloadModeStrict), boolToInt(s.PreDownload),
		boolToInt(s.IncludeCommentLinks),
		jsonOr(s.Qualities, "[]"),
		optionalInt(s.MinSizeMB, s.HasMinSize), optionalInt(s.MaxSizeMB, s.HasMaxSize),
		optionalInt(s.MaxFileCount, s.HasMaxFileCount),
		s.ReleaseDateFrom, s.ReleaseDateTo, optionalInt(s.ExpiryDays, s.HasExpiryDays),
		jsonOr(s.Categories, "[]"), jsonOr(s.ExcludeCategories, "[]"),
		boolToInt(s.Enabled), s.TargetAccountID, s.TargetParentID, s.TargetDisplayPath,
		defaultStr(s.PushProvider, "auto"), defaultStr(s.SubfolderMode, domain.JavSubfolderCode))
	if err != nil {
		return 0, wrapDB(err)
	}
	return res.LastInsertId()
}

func (r *javSubscriptionRepo) Update(ctx context.Context, s *domain.JavSubscription) error {
	if s == nil || s.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的订阅")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscriptions SET
    target_type=?, target_id=?, target_url=?, target_key=?, target_name=?, status=?,
    download_mode=?, pre_download=?, include_comment_links=?, qualities_json=?, min_size_mb=?, max_size_mb=?, max_file_count=?,
    release_date_from=?, release_date_to=?, expiry_days=?, categories_json=?, exclude_categories_json=?,
    enabled=?, target_account_id=?, target_parent_id=?, target_display_path=?, push_provider=?,
    subfolder_mode=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`,
		s.TargetType, s.TargetID, s.TargetURL, s.TargetKey, s.TargetName,
		defaultStr(s.Status, domain.JavSubStatusActive),
		defaultStr(s.DownloadMode, domain.JavDownloadModeStrict), boolToInt(s.PreDownload),
		boolToInt(s.IncludeCommentLinks),
		jsonOr(s.Qualities, "[]"),
		optionalInt(s.MinSizeMB, s.HasMinSize), optionalInt(s.MaxSizeMB, s.HasMaxSize),
		optionalInt(s.MaxFileCount, s.HasMaxFileCount),
		s.ReleaseDateFrom, s.ReleaseDateTo, optionalInt(s.ExpiryDays, s.HasExpiryDays),
		jsonOr(s.Categories, "[]"), jsonOr(s.ExcludeCategories, "[]"),
		boolToInt(s.Enabled), s.TargetAccountID, s.TargetParentID, s.TargetDisplayPath,
		defaultStr(s.PushProvider, "auto"), defaultStr(s.SubfolderMode, domain.JavSubfolderCode), s.ID)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	// 手工级联：候选/尝试/skip 都是订阅的从属数据，留着只会变成孤儿。
	for _, q := range []string{
		`DELETE FROM jav_subscription_candidates WHERE subscription_id=?`,
		`DELETE FROM jav_subscription_push_attempts WHERE subscription_id=?`,
		`DELETE FROM jav_subscription_skips WHERE subscription_id=?`,
		`DELETE FROM jav_subscription_runs WHERE subscription_id=?`,
		`DELETE FROM jav_subscriptions WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return wrapDB(err)
		}
	}
	return wrapDB(tx.Commit())
}

func (r *javSubscriptionRepo) Get(ctx context.Context, id int64) (*domain.JavSubscription, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javSubColumns+` FROM jav_subscriptions WHERE id=?`, id)
	return scanJavSubscription(row)
}

func (r *javSubscriptionRepo) GetByTarget(ctx context.Context, targetType, targetKey string) (*domain.JavSubscription, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+javSubColumns+` FROM jav_subscriptions WHERE target_type=? AND target_key=?`,
		targetType, targetKey)
	return scanJavSubscription(row)
}

func (r *javSubscriptionRepo) List(ctx context.Context, f domain.JavSubscriptionFilter) ([]*domain.JavSubscription, int, error) {
	conds := make([]string, 0, 3)
	params := make([]any, 0, 3)
	if f.Status != "" {
		conds = append(conds, `status = ?`)
		params = append(params, f.Status)
	}
	if f.TargetType != "" {
		conds = append(conds, `target_type = ?`)
		params = append(params, f.TargetType)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		conds = append(conds, `(target_name LIKE ? OR target_id LIKE ?)`)
		like := "%" + kw + "%"
		params = append(params, like, like)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_subscriptions`+where, params...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javSubColumns+` FROM jav_subscriptions`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(params, clampLimit(f.Limit, 100), maxInt(f.Offset, 0))...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavSubscription, 0)
	for rows.Next() {
		s, err := scanJavSubscription(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, wrapDB(rows.Err())
}

func (r *javSubscriptionRepo) ListActive(ctx context.Context) ([]*domain.JavSubscription, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javSubColumns+` FROM jav_subscriptions WHERE status='active' AND enabled=1 ORDER BY id ASC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavSubscription, 0)
	for rows.Next() {
		s, err := scanJavSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, wrapDB(rows.Err())
}

func (r *javSubscriptionRepo) SetStatus(ctx context.Context, id int64, status string) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscriptions SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, id)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) MarkChecked(ctx context.Context, id int64, at time.Time, matched int) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscriptions SET last_checked_at=?, matched_count=?, last_error='', updated_at=CURRENT_TIMESTAMP
WHERE id=?`, tsValue(at), matched, id)
	return wrapDB(err)
}

// SetMatchedCount 只改「检」那个数，见 domain 里那条注释。
func (r *javSubscriptionRepo) SetMatchedCount(ctx context.Context, id int64, matched int) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscriptions SET matched_count=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		matched, id)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) MarkPushed(ctx context.Context, id int64, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscriptions SET last_push_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		tsValue(at), id)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) MarkCompleted(ctx context.Context, id int64, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscriptions SET status='completed', completed_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		tsValue(at), id)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) MarkError(ctx context.Context, id int64, message string) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscriptions SET last_error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, message, id)
	return wrapDB(err)
}

func (r *javSubscriptionRepo) HasRunningAttempt(ctx context.Context, subscriptionID int64) (bool, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `
SELECT COUNT(*) FROM jav_subscription_push_attempts
WHERE subscription_id=? AND status='running' AND info_hash <> ''`, subscriptionID).Scan(&n)
	if err != nil {
		return false, wrapDB(err)
	}
	return n > 0, nil
}

func scanJavSubscription(sc javRowScanner) (*domain.JavSubscription, error) {
	var (
		s            domain.JavSubscription
		preDownload  int
		commentLinks int
		qualities    string
		minSize      sql.NullInt64
		maxSize      sql.NullInt64
		maxFiles     sql.NullInt64
		expiryDays   sql.NullInt64
		cats         string
		excludeCats  string
		enabled      int
		lastChecked  sql.NullString
		lastPush     sql.NullString
		completedAt  sql.NullString
		createdAt    sql.NullString
		updatedAt    sql.NullString
	)
	err := sc.Scan(&s.ID, &s.TargetType, &s.TargetID, &s.TargetURL, &s.TargetKey, &s.TargetName, &s.Status,
		&s.DownloadMode, &preDownload, &commentLinks, &qualities, &minSize, &maxSize, &maxFiles,
		&s.ReleaseDateFrom, &s.ReleaseDateTo, &expiryDays, &cats, &excludeCats,
		&enabled, &s.TargetAccountID, &s.TargetParentID, &s.TargetDisplayPath, &s.PushProvider, &s.SubfolderMode,
		&lastChecked, &lastPush, &completedAt, &s.LastError, &s.MatchedCount, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	s.PreDownload = preDownload != 0
	s.IncludeCommentLinks = commentLinks != 0
	s.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(qualities), &s.Qualities)
	_ = json.Unmarshal([]byte(cats), &s.Categories)
	_ = json.Unmarshal([]byte(excludeCats), &s.ExcludeCategories)
	if minSize.Valid {
		s.MinSizeMB, s.HasMinSize = int(minSize.Int64), true
	}
	if maxSize.Valid {
		s.MaxSizeMB, s.HasMaxSize = int(maxSize.Int64), true
	}
	if maxFiles.Valid {
		s.MaxFileCount, s.HasMaxFileCount = int(maxFiles.Int64), true
	}
	if expiryDays.Valid {
		s.ExpiryDays, s.HasExpiryDays = int(expiryDays.Int64), true
	}
	s.LastCheckedAt = parseTS(lastChecked)
	s.LastPushAt = parseTS(lastPush)
	s.CompletedAt = parseTS(completedAt)
	s.CreatedAt = parseTS(createdAt)
	s.UpdatedAt = parseTS(updatedAt)
	return &s, nil
}

// ——————————————————————— jav_subscription_runs ———————————————————————

type javRunRepo struct{ db *DB }

func (r *javRunRepo) Create(ctx context.Context, run *domain.JavRun) (int64, error) {
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscription_runs(subscription_id, trigger_type, matcher_version, status)
VALUES (?,?,?,?)`,
		run.SubscriptionID, defaultStr(run.TriggerType, "manual"), defaultStr(run.MatcherVersion, "v1"), domain.JavRunRunning)
	if err != nil {
		return 0, wrapDB(err)
	}
	return res.LastInsertId()
}

func (r *javRunRepo) Finish(ctx context.Context, id int64, status string, matched, rejected int, errMsg string) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscription_runs SET status=?, matched_count=?, rejected_count=?, error=?, finished_at=CURRENT_TIMESTAMP
WHERE id=?`, status, matched, rejected, errMsg, id)
	return wrapDB(err)
}

func (r *javRunRepo) Get(ctx context.Context, id int64) (*domain.JavRun, error) {
	var (
		run        domain.JavRun
		startedAt  sql.NullString
		finishedAt sql.NullString
	)
	err := r.db.read.QueryRowContext(ctx, `
SELECT id, subscription_id, trigger_type, matcher_version, status, matched_count, rejected_count,
       error, started_at, finished_at
FROM jav_subscription_runs WHERE id=?`, id).
		Scan(&run.ID, &run.SubscriptionID, &run.TriggerType, &run.MatcherVersion, &run.Status,
			&run.MatchedCount, &run.RejectedCount, &run.Error, &startedAt, &finishedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	run.StartedAt = parseTS(startedAt)
	run.FinishedAt = parseTS(finishedAt)
	return &run, nil
}

func (r *javRunRepo) ListBySubscription(ctx context.Context, subscriptionID int64, limit int) ([]*domain.JavRun, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT id, subscription_id, trigger_type, matcher_version, status, matched_count, rejected_count,
       error, started_at, finished_at
FROM jav_subscription_runs WHERE subscription_id=? ORDER BY id DESC LIMIT ?`,
		subscriptionID, clampLimit(limit, 20))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavRun, 0)
	for rows.Next() {
		var (
			run        domain.JavRun
			startedAt  sql.NullString
			finishedAt sql.NullString
		)
		if err := rows.Scan(&run.ID, &run.SubscriptionID, &run.TriggerType, &run.MatcherVersion, &run.Status,
			&run.MatchedCount, &run.RejectedCount, &run.Error, &startedAt, &finishedAt); err != nil {
			return nil, wrapDB(err)
		}
		run.StartedAt = parseTS(startedAt)
		run.FinishedAt = parseTS(finishedAt)
		out = append(out, &run)
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— jav_subscription_candidates ———————————————————————

type javCandidateRepo struct{ db *DB }

const javCandidateColumns = `id, check_run_id, subscription_id, movie_id, magnet_fingerprint, magnet_name,
       magnet_uri, size_text, size_bytes, file_count, release_date, quality_tags_json,
       resource_fingerprint, resource_score_json, matched, push_ok, predownload, attempted,
       rejection_reasons_json, source, created_at`

func (r *javCandidateRepo) Create(ctx context.Context, c *domain.JavCandidate) (int64, error) {
	score := c.ResourceScore
	if len(score) == 0 {
		score = []int64{0, 0, 0}
	}
	scoreJSON, err := json.Marshal(score)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "无效的资源评分")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscription_candidates(check_run_id, subscription_id, movie_id, magnet_fingerprint,
       magnet_name, magnet_uri, size_text, size_bytes, file_count, release_date, quality_tags_json,
       resource_fingerprint, resource_score_json, matched, push_ok, predownload, attempted,
       rejection_reasons_json, source)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.CheckRunID, c.SubscriptionID, c.MovieID, c.MagnetFingerprint,
		c.MagnetName, c.MagnetURI, c.SizeText, optionalInt64(c.SizeBytes, c.HasSize),
		optionalInt(c.FileCount, c.HasFiles), c.ReleaseDate, jsonOr(c.QualityTags, "[]"),
		c.ResourceFingerprint, string(scoreJSON), boolToInt(c.Matched), boolToInt(c.PushOK),
		boolToInt(c.PreDownload), boolToInt(c.Attempted), jsonOr(c.RejectionReasons, "[]"), c.Source)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	// 顺手回填到结构体上：调用方紧接着就要用它去标记这条候选。
	c.ID = id
	return id, nil
}

func (r *javCandidateRepo) List(ctx context.Context, f domain.JavCandidateFilter) ([]*domain.JavCandidate, int, error) {
	where, params := javCandidateWhere(f)
	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_subscription_candidates`+where, params...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	// 排序把 push_ok 放最前：合格的资源先给用户看，不合格的排在后面带着拒绝原因。
	rows, err := r.db.read.QueryContext(ctx, `SELECT `+javCandidateColumns+`
FROM jav_subscription_candidates`+where+`
ORDER BY push_ok DESC, json_extract(resource_score_json,'$[0]') DESC,
         json_extract(resource_score_json,'$[1]') DESC, json_extract(resource_score_json,'$[2]') DESC, id DESC
LIMIT ? OFFSET ?`, append(params, clampLimit(f.Limit, 100), maxInt(f.Offset, 0))...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out, err := scanJavCandidates(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func javCandidateWhere(f domain.JavCandidateFilter) (string, []any) {
	conds := make([]string, 0, 5)
	params := make([]any, 0, 5)
	if f.CheckRunID > 0 {
		conds = append(conds, `check_run_id = ?`)
		params = append(params, f.CheckRunID)
	}
	if f.SubscriptionID > 0 {
		conds = append(conds, `subscription_id = ?`)
		params = append(params, f.SubscriptionID)
	}
	if f.MovieID != "" {
		conds = append(conds, `movie_id = ?`)
		params = append(params, f.MovieID)
	}
	if f.MatchedOnly {
		conds = append(conds, `matched = 1`)
	}
	if f.PushOKOnly {
		conds = append(conds, `push_ok = 1`)
	}
	if f.UntriedOnly {
		conds = append(conds, `attempted = 0`)
	}
	if len(conds) == 0 {
		return "", params
	}
	return " WHERE " + strings.Join(conds, " AND "), params
}

func (r *javCandidateRepo) Get(ctx context.Context, id int64) (*domain.JavCandidate, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+javCandidateColumns+` FROM jav_subscription_candidates WHERE id=?`, id)
	return scanJavCandidate(row)
}

// PickBest 取「分数最高」的候选。
//
// 注意它与 List 的排序**不是**一回事：List 把 push_ok 放最前，那是给人看的展示序
// （合格资源先列出来）；PickBest 只认分数，合格与否由调用方通过 PushOKOnly 决定。
//
// 这不是吹毛求疵 —— 源码里这两种语义各有一处：auto_push 是「先过滤 push_ok、
// 再按分数取最大」，而 subscribe_movie 是「优先 push_ok、没有就退而求其次」。
// 把 push_ok 塞进 PickBest 的 ORDER BY 会让前者退化成后者：一颗分数更高的
// 4K 破解资源会因为 push_ok=0（比如体积超了订阅上限）而被一颗 720p 顶掉。
// 想要 subscribe_movie 的回落语义，调用方查两次即可，与源码的两条回退查询一致。
//
// 排序交给 SQLite 而不是在 Go 里重排，是为了让空值处理与类型转换和存储层保持一致。
func (r *javCandidateRepo) PickBest(ctx context.Context, f domain.JavCandidateFilter) (*domain.JavCandidate, error) {
	where, params := javCandidateWhere(f)
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javCandidateColumns+`
FROM jav_subscription_candidates`+where+`
ORDER BY json_extract(resource_score_json,'$[0]') DESC,
         json_extract(resource_score_json,'$[1]') DESC,
         json_extract(resource_score_json,'$[2]') DESC, id DESC
LIMIT 1`, params...)
	return scanJavCandidate(row)
}

func (r *javCandidateRepo) SetAttempted(ctx context.Context, id int64, value bool) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscription_candidates SET attempted=? WHERE id=?`, boolToInt(value), id)
	return wrapDB(err)
}

func (r *javCandidateRepo) MarkPreDownload(ctx context.Context, id int64, value bool) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscription_candidates SET predownload=? WHERE id=?`, boolToInt(value), id)
	return wrapDB(err)
}

func (r *javCandidateRepo) DeleteBySubscription(ctx context.Context, subscriptionID int64) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM jav_subscription_candidates WHERE subscription_id=?`, subscriptionID)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func scanJavCandidate(sc javRowScanner) (*domain.JavCandidate, error) {
	var (
		c         domain.JavCandidate
		size      sql.NullInt64
		files     sql.NullInt64
		tags      string
		scoreJSON string
		matched   int
		pushOK    int
		preDL     int
		attempted int
		reasons   string
		createdAt sql.NullString
	)
	err := sc.Scan(&c.ID, &c.CheckRunID, &c.SubscriptionID, &c.MovieID, &c.MagnetFingerprint, &c.MagnetName,
		&c.MagnetURI, &c.SizeText, &size, &files, &c.ReleaseDate, &tags,
		&c.ResourceFingerprint, &scoreJSON, &matched, &pushOK, &preDL, &attempted, &reasons, &c.Source, &createdAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	if size.Valid {
		c.SizeBytes, c.HasSize = size.Int64, true
	}
	if files.Valid {
		c.FileCount, c.HasFiles = int(files.Int64), true
	}
	_ = json.Unmarshal([]byte(tags), &c.QualityTags)
	_ = json.Unmarshal([]byte(scoreJSON), &c.ResourceScore)
	_ = json.Unmarshal([]byte(reasons), &c.RejectionReasons)
	c.Matched = matched != 0
	c.PushOK = pushOK != 0
	c.PreDownload = preDL != 0
	c.Attempted = attempted != 0
	c.CreatedAt = parseTS(createdAt)
	return &c, nil
}

func scanJavCandidates(rows *sql.Rows) ([]*domain.JavCandidate, error) {
	out := make([]*domain.JavCandidate, 0)
	for rows.Next() {
		c, err := scanJavCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— jav_subscription_push_attempts ———————————————————————

type javAttemptRepo struct{ db *DB }

const javAttemptColumns = `id, subscription_id, candidate_id, push_record_id, idempotency_key,
       status, info_hash, offline_task_id, retry_count, error_message, created_at, updated_at`

func (r *javAttemptRepo) Create(ctx context.Context, a *domain.JavPushAttempt) (int64, error) {
	if a == nil || strings.TrimSpace(a.IdempotencyKey) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的投递尝试")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscription_push_attempts(subscription_id, candidate_id, push_record_id,
       idempotency_key, status, info_hash, offline_task_id)
VALUES (?,?,?,?,?,?,?)`,
		a.SubscriptionID, a.CandidateID, a.PushRecordID, a.IdempotencyKey,
		defaultStr(a.Status, domain.JavAttemptRunning), a.InfoHash, a.OfflineTaskID)
	if err != nil {
		return 0, wrapDB(err)
	}
	return res.LastInsertId()
}

func (r *javAttemptRepo) Get(ctx context.Context, id int64) (*domain.JavPushAttempt, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+javAttemptColumns+` FROM jav_subscription_push_attempts WHERE id=?`, id)
	return scanJavAttempt(row)
}

func (r *javAttemptRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.JavPushAttempt, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+javAttemptColumns+` FROM jav_subscription_push_attempts WHERE idempotency_key=?`, key)
	return scanJavAttempt(row)
}

func (r *javAttemptRepo) GetByInfoHash(ctx context.Context, infoHash string) (*domain.JavPushAttempt, error) {
	if strings.TrimSpace(infoHash) == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "投递尝试不存在")
	}
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javAttemptColumns+`
FROM jav_subscription_push_attempts WHERE info_hash=? ORDER BY id DESC LIMIT 1`, infoHash)
	return scanJavAttempt(row)
}

func (r *javAttemptRepo) GetByOfflineTaskID(ctx context.Context, taskID string) (*domain.JavPushAttempt, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "投递尝试不存在")
	}
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javAttemptColumns+`
FROM jav_subscription_push_attempts WHERE offline_task_id=? ORDER BY id DESC LIMIT 1`, taskID)
	return scanJavAttempt(row)
}

func (r *javAttemptRepo) ListRunning(ctx context.Context) ([]*domain.JavPushAttempt, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javAttemptColumns+` FROM jav_subscription_push_attempts WHERE status='running' ORDER BY id ASC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavPushAttempt, 0)
	for rows.Next() {
		a, err := scanJavAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, wrapDB(rows.Err())
}

func (r *javAttemptRepo) ListBySubscription(ctx context.Context, subscriptionID int64, limit, offset int) ([]*domain.JavPushAttempt, int, error) {
	var total int
	if err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jav_subscription_push_attempts WHERE subscription_id=?`, subscriptionID).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	rows, err := r.db.read.QueryContext(ctx, `SELECT `+javAttemptColumns+`
FROM jav_subscription_push_attempts WHERE subscription_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		subscriptionID, clampLimit(limit, 50), maxInt(offset, 0))
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavPushAttempt, 0)
	for rows.Next() {
		a, err := scanJavAttempt(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, wrapDB(rows.Err())
}

func (r *javAttemptRepo) Finish(ctx context.Context, id int64, status, errMsg string) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscription_push_attempts SET status=?, error_message=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, errMsg, id)
	return wrapDB(err)
}

func (r *javAttemptRepo) Link(ctx context.Context, id, recordID int64, taskID, infoHash string) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscription_push_attempts
SET push_record_id=?, offline_task_id=?, info_hash=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`, recordID, taskID, infoHash, id)
	return wrapDB(err)
}

// completedOrderBy 是「已完成影片」支持的排序键 → ORDER BY 片段。
//
// **白名单**：排序键是从 URL 上来的，绝不能拼进 SQL，所以只认这几张固定的片段。
// 键名与界面上的四颗胶囊一一对应：
//
//	code     番号
//	created  创建时间（这条推送所属**订阅**的创建时间）
//	resource 资源时间（推送记录创建时间 = 提交离线任务那一刻）
//	done     完成时间（网盘下完、事件回写那一刻）
var completedOrderBy = map[string]string{
	"code":     "MAX(m.number)",
	"created":  "MAX(sub.created_at)",
	"resource": "MAX(r.created_at)",
	"done":     "MAX(COALESCE(r.pushed_at, r.updated_at))",
}

// PushedMovieIDs 返回推送成功过的影片 id 集合。
//
// 影片粒度的「推成功了没有」用它，而不是逐订阅查尝试记录：**详情页那颗手动推送
// 不挂订阅**（subscription_id=0），按订阅查会看不见它 —— 于是「订阅里这部片该不该
// 显示已完成」就永远变不过来。推送记录覆盖所有投递路径，也是卡片上「推：N」的口径。
func (r *javPushRecordRepo) PushedMovieIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT DISTINCT movie_id FROM jav_push_records
		  WHERE status = ? AND movie_id <> ''`, domain.JavPushPushed)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out[id] = struct{}{}
	}
	return out, wrapDB(rows.Err())
}

// DeliveredMovieIDs 返回已经推出去过的影片（在途 + 已完成），见 domain 里那条注释。
func (r *javPushRecordRepo) DeliveredMovieIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT DISTINCT movie_id FROM jav_push_records
		  WHERE status IN (?, ?) AND movie_id <> ''`,
		domain.JavPushPending, domain.JavPushPushed)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out[id] = struct{}{}
	}
	return out, wrapDB(rows.Err())
}

func (r *javAttemptRepo) ListSucceededMovies(ctx context.Context, limit, offset int, sortKey string, desc bool) ([]*domain.JavCompletedMovie, int, error) {
	var total int
	if err := r.db.read.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT movie_id) FROM jav_push_records
WHERE status = ? AND movie_id <> ''`, domain.JavPushPushed).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}

	expr, ok := completedOrderBy[sortKey]
	if !ok {
		expr = completedOrderBy["done"]
	}
	dir := "DESC"
	if !desc {
		dir = "ASC"
	}

	// 数据源是**推送记录**（status=pushed），不是「尝试 + 候选」：
	// 手动推送没有候选行，按那条链算会把详情页推成功的片漏在「已完成」之外。
	//
	// 先按影片去重取「最近一次推成功」的时间与订阅，再回查影片与订阅名 ——
	// 一次 GROUP BY 比在 Go 里聚合稳，也不会因为同一部片被多条订阅覆盖而重复。
	//
	// 两个 LEFT JOIN 只为排序取字段（番号、订阅创建时间）。用 LEFT 而不是 INNER：
	// 影片或订阅被删过也不该让这条已完成记录从列表里消失，排序时取 NULL 即可。
	// 末尾再按 movie_id 兜一层，保证同键值的行顺序稳定、翻页不会重复或漏。
	rows, err := r.db.read.QueryContext(ctx, `
SELECT r.movie_id, MAX(COALESCE(r.pushed_at, r.updated_at)) AS pushed_at,
       MAX(r.subscription_id) AS sid
FROM jav_push_records r
LEFT JOIN jav_movies m ON m.id = r.movie_id
LEFT JOIN jav_subscriptions sub ON sub.id = r.subscription_id
WHERE r.status = ? AND r.movie_id <> ''
GROUP BY r.movie_id
ORDER BY `+expr+` `+dir+`, r.movie_id ASC
LIMIT ? OFFSET ?`, append([]any{domain.JavPushPushed}, clampLimit(limit, 60), maxInt(offset, 0))...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavCompletedMovie, 0)
	for rows.Next() {
		var (
			m        domain.JavCompletedMovie
			pushedAt sql.NullString
		)
		if err := rows.Scan(&m.MovieID, &pushedAt, &m.SubscriptionID); err != nil {
			return nil, 0, wrapDB(err)
		}
		m.PushedAt = parseTS(pushedAt)
		out = append(out, &m)
	}
	return out, total, wrapDB(rows.Err())
}

// ResetForRetry 把一条失败的尝试放回「在途」，供重试。
//
// **created_at 必须一起重置**：卡死判据是 `now.Sub(attempt.CreatedAt) < 6h`
// （jav/loops.go 的 attemptSweeperLoop）。只改 status 的话，任何超过 6 小时前
// 建的尝试一重试就被下一次扫描（≤60 秒）判成「等待网盘下载超时」，重试等于
// 白重试 —— 实测：07:31:02 重试、07:31:32 就被判超时。
func (r *javAttemptRepo) ResetForRetry(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE jav_subscription_push_attempts
SET status='running', error_message='', retry_count=retry_count+1,
    created_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP
WHERE id=?`, id)
	return wrapDB(err)
}

func (r *javAttemptRepo) IncRetry(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_subscription_push_attempts SET retry_count=retry_count+1, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return wrapDB(err)
}

func scanJavAttempt(sc javRowScanner) (*domain.JavPushAttempt, error) {
	var (
		a         domain.JavPushAttempt
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	err := sc.Scan(&a.ID, &a.SubscriptionID, &a.CandidateID, &a.PushRecordID, &a.IdempotencyKey,
		&a.Status, &a.InfoHash, &a.OfflineTaskID, &a.RetryCount, &a.ErrorMessage, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	a.CreatedAt = parseTS(createdAt)
	a.UpdatedAt = parseTS(updatedAt)
	return &a, nil
}

// ——————————————————————— jav_subscription_blacklist ———————————————————————

type javBlacklistRepo struct{ db *DB }

func (r *javBlacklistRepo) Create(ctx context.Context, e *domain.JavBlacklistEntry) (int64, error) {
	if e == nil || strings.TrimSpace(e.TargetKey) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的黑名单条目")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscription_blacklist(target_type, target_id, target_key, target_name, reason, movie_ids)
VALUES (?,?,?,?,?,?)
ON CONFLICT(target_type, target_key) DO UPDATE SET
    target_name=excluded.target_name, reason=excluded.reason, movie_ids=excluded.movie_ids`,
		e.TargetType, e.TargetID, e.TargetKey, e.TargetName, e.Reason, jsonOr(e.MovieIDs, "[]"))
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *javBlacklistRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM jav_subscription_blacklist WHERE id=?`, id)
	return wrapDB(err)
}

const javBlacklistColumns = `id, target_type, target_id, target_key, target_name, reason, created_at, movie_ids`

func (r *javBlacklistRepo) List(ctx context.Context, targetType string) ([]*domain.JavBlacklistEntry, error) {
	q := `SELECT ` + javBlacklistColumns + ` FROM jav_subscription_blacklist`
	args := []any{}
	if targetType != "" {
		q += ` WHERE target_type=?`
		args = append(args, targetType)
	}
	q += ` ORDER BY id DESC`

	rows, err := r.db.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	return scanJavBlacklist(rows)
}

func (r *javBlacklistRepo) Get(ctx context.Context, id int64) (*domain.JavBlacklistEntry, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javBlacklistColumns+` FROM jav_subscription_blacklist WHERE id=?`, id)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	got, err := scanJavBlacklist(rows)
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, nil
	}
	return got[0], nil
}

func scanJavBlacklist(rows *sql.Rows) ([]*domain.JavBlacklistEntry, error) {
	out := make([]*domain.JavBlacklistEntry, 0)
	for rows.Next() {
		var (
			e         domain.JavBlacklistEntry
			createdAt sql.NullString
			movieIDs  string
		)
		if err := rows.Scan(&e.ID, &e.TargetType, &e.TargetID, &e.TargetKey,
			&e.TargetName, &e.Reason, &createdAt, &movieIDs); err != nil {
			return nil, wrapDB(err)
		}
		e.CreatedAt = parseTS(createdAt)
		// 老条目这一列是空串（迁移之前加的），当成「没有记录」而不是错误 ——
		// 它只是少一份给人看的快照，拉黑本身照样生效。
		if strings.TrimSpace(movieIDs) != "" {
			_ = json.Unmarshal([]byte(movieIDs), &e.MovieIDs)
		}
		out = append(out, &e)
	}
	return out, wrapDB(rows.Err())
}

// Keys 返回 target_type → 键集合，供匹配时 O(1) 命中。
func (r *javBlacklistRepo) Keys(ctx context.Context) (map[string]map[string]struct{}, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT target_type, target_key FROM jav_subscription_blacklist`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make(map[string]map[string]struct{})
	for rows.Next() {
		var t, k string
		if err := rows.Scan(&t, &k); err != nil {
			return nil, wrapDB(err)
		}
		if out[t] == nil {
			out[t] = make(map[string]struct{})
		}
		out[t][k] = struct{}{}
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— jav_subscription_skips ———————————————————————

type javSkipRepo struct{ db *DB }

func (r *javSkipRepo) Add(ctx context.Context, subscriptionID int64, movieID string) error {
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_subscription_skips(subscription_id, movie_id) VALUES (?,?)
ON CONFLICT(subscription_id, movie_id) DO NOTHING`, subscriptionID, movieID)
	return wrapDB(err)
}

func (r *javSkipRepo) Remove(ctx context.Context, subscriptionID int64, movieID string) error {
	_, err := r.db.write.ExecContext(ctx,
		`DELETE FROM jav_subscription_skips WHERE subscription_id=? AND movie_id=?`, subscriptionID, movieID)
	return wrapDB(err)
}

func (r *javSkipRepo) List(ctx context.Context, subscriptionID int64) (map[string]struct{}, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT movie_id FROM jav_subscription_skips WHERE subscription_id=?`, subscriptionID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out[id] = struct{}{}
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— jav_list_movies ———————————————————————

type javListMovieRepo struct{ db *DB }

func (r *javListMovieRepo) Replace(ctx context.Context, listID string, movieIDs []string, at time.Time) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM jav_list_movies WHERE list_id=?`, listID); err != nil {
		return wrapDB(err)
	}
	for i, id := range movieIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO jav_list_movies(list_id, movie_id, position, synced_at) VALUES (?,?,?,?)
ON CONFLICT(list_id, movie_id) DO UPDATE SET position=excluded.position, synced_at=excluded.synced_at`,
			listID, id, i, tsValue(at)); err != nil {
			return wrapDB(err)
		}
	}
	return wrapDB(tx.Commit())
}

func (r *javListMovieRepo) ListIDs(ctx context.Context, listID string) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT movie_id FROM jav_list_movies WHERE list_id=? ORDER BY position ASC`, listID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, id)
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— 工具 ———————————————————————

func optionalInt(v int, has bool) any {
	if !has {
		return nil
	}
	return v
}

func optionalInt64(v int64, has bool) any {
	if !has {
		return nil
	}
	return v
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
