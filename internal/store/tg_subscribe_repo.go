package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
)

// ——————————————————————— tg_channels ———————————————————————

type tgChannelRepo struct{ db *DB }

const tgChannelColumns = `id, chat_id, username, title, remark, level, enabled, status, last_error,
       last_message_id, last_post_at, matched_count, created_at, updated_at`

func (r *tgChannelRepo) Create(ctx context.Context, c *domain.TGChannel) (int64, error) {
	if c == nil || strings.TrimSpace(c.ChatID) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的 TG 频道")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO tg_channels(chat_id, username, title, remark, level, enabled, status, last_error,
                        last_message_id, last_post_at, matched_count)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		c.ChatID, c.Username, c.Title, c.Remark, c.Level, boolToInt(c.Enabled), c.Status, c.LastError,
		c.LastMessageID, tsValue(c.LastPostAt), c.MatchedCount,
	)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *tgChannelRepo) Update(ctx context.Context, c *domain.TGChannel) error {
	if c == nil || c.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的 TG 频道")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_channels SET chat_id=?, username=?, title=?, remark=?, level=?, enabled=?, status=?,
       last_error=?, last_message_id=?, last_post_at=?, matched_count=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`,
		c.ChatID, c.Username, c.Title, c.Remark, c.Level, boolToInt(c.Enabled), c.Status,
		c.LastError, c.LastMessageID, tsValue(c.LastPostAt), c.MatchedCount, c.ID,
	)
	return wrapDB(err)
}

func (r *tgChannelRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM tg_channels WHERE id=?`, id)
	return wrapDB(err)
}

func (r *tgChannelRepo) Get(ctx context.Context, id int64) (*domain.TGChannel, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+tgChannelColumns+` FROM tg_channels WHERE id=?`, id)
	return scanTGChannel(row)
}

func (r *tgChannelRepo) GetByChatID(ctx context.Context, chatID string) (*domain.TGChannel, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+tgChannelColumns+` FROM tg_channels WHERE chat_id=?`, strings.TrimSpace(chatID))
	return scanTGChannel(row)
}

func (r *tgChannelRepo) List(ctx context.Context, enabledOnly bool) ([]*domain.TGChannel, error) {
	q := `SELECT ` + tgChannelColumns + ` FROM tg_channels`
	if enabledOnly {
		q += ` WHERE enabled=1`
	}
	// level 降序：同一个资源被多个频道转发时，高优先级频道先落库、先占住去重位。
	q += ` ORDER BY level DESC, id ASC`
	rows, err := r.db.read.QueryContext(ctx, q)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGChannel, 0)
	for rows.Next() {
		c, err := scanTGChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, wrapDB(rows.Err())
}

func (r *tgChannelRepo) MarkPost(ctx context.Context, id int64, messageID int64, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_channels SET last_post_at=?, last_message_id=MAX(last_message_id, ?),
       matched_count=matched_count+1, status=?, last_error='', updated_at=CURRENT_TIMESTAMP
WHERE id=?`, tsValue(at), messageID, domain.TGChannelStatusOK, id)
	return wrapDB(err)
}

func (r *tgChannelRepo) MarkStatus(ctx context.Context, id int64, status, lastError string) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_channels SET status=?, last_error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, lastError, id)
	return wrapDB(err)
}

type tgScanner interface{ Scan(dest ...any) error }

func scanTGChannel(row tgScanner) (*domain.TGChannel, error) {
	var c domain.TGChannel
	var enabled int
	var lastPost, createdAt, updatedAt sql.NullString
	if err := row.Scan(&c.ID, &c.ChatID, &c.Username, &c.Title, &c.Remark, &c.Level, &enabled, &c.Status,
		&c.LastError, &c.LastMessageID, &lastPost, &c.MatchedCount, &createdAt, &updatedAt); err != nil {
		return nil, wrapDB(err)
	}
	c.Enabled = enabled != 0
	c.LastPostAt = parseTS(lastPost)
	c.CreatedAt = parseTS(createdAt)
	c.UpdatedAt = parseTS(updatedAt)
	return &c, nil
}

// ——————————————————— tg_quality_profiles ———————————————————

type tgQualityProfileRepo struct{ db *DB }

const tgQualityProfileColumns = `id, name, is_default, config_json, created_at, updated_at`

func (r *tgQualityProfileRepo) Create(ctx context.Context, p *domain.TGQualityProfile) (int64, error) {
	if p == nil || strings.TrimSpace(p.Name) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "画质方案名称不能为空")
	}
	cfg := p.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO tg_quality_profiles(name, is_default, config_json) VALUES (?,?,?)`,
		p.Name, boolToInt(p.IsDefault), string(cfg))
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *tgQualityProfileRepo) Update(ctx context.Context, p *domain.TGQualityProfile) error {
	if p == nil || p.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的画质方案")
	}
	cfg := p.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_quality_profiles SET name=?, config_json=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, string(cfg), p.ID)
	return wrapDB(err)
}

func (r *tgQualityProfileRepo) Delete(ctx context.Context, id int64) error {
	if id == 1 {
		return domain.Errorf(domain.CodeValidation, "默认画质方案不可删除")
	}
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM tg_quality_profiles WHERE id=?`, id)
	return wrapDB(err)
}

func (r *tgQualityProfileRepo) Get(ctx context.Context, id int64) (*domain.TGQualityProfile, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+tgQualityProfileColumns+` FROM tg_quality_profiles WHERE id=?`, id)
	return scanTGQualityProfile(row)
}

func (r *tgQualityProfileRepo) GetDefault(ctx context.Context) (*domain.TGQualityProfile, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+tgQualityProfileColumns+` FROM tg_quality_profiles ORDER BY is_default DESC, id ASC LIMIT 1`)
	return scanTGQualityProfile(row)
}

func (r *tgQualityProfileRepo) List(ctx context.Context) ([]*domain.TGQualityProfile, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+tgQualityProfileColumns+` FROM tg_quality_profiles ORDER BY is_default DESC, id ASC`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGQualityProfile, 0)
	for rows.Next() {
		p, err := scanTGQualityProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, wrapDB(rows.Err())
}

func (r *tgQualityProfileRepo) SetDefault(ctx context.Context, id int64) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tg_quality_profiles SET is_default=0`); err != nil {
		_ = tx.Rollback()
		return wrapDB(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tg_quality_profiles SET is_default=1 WHERE id=?`, id); err != nil {
		_ = tx.Rollback()
		return wrapDB(err)
	}
	return wrapDB(tx.Commit())
}

func scanTGQualityProfile(row tgScanner) (*domain.TGQualityProfile, error) {
	var p domain.TGQualityProfile
	var isDefault int
	var cfg string
	var createdAt, updatedAt sql.NullString
	if err := row.Scan(&p.ID, &p.Name, &isDefault, &cfg, &createdAt, &updatedAt); err != nil {
		return nil, wrapDB(err)
	}
	p.IsDefault = isDefault != 0
	p.Config = json.RawMessage(cfg)
	p.CreatedAt = parseTS(createdAt)
	p.UpdatedAt = parseTS(updatedAt)
	return &p, nil
}

// ——————————————————— tg_subscriptions ————————————————————

type tgSubscriptionRepo struct{ db *DB }

const tgSubscriptionColumns = `id, tmdb_id, media_type, title, original_title, year, poster_path, overview,
       aliases_json, aliases_synced_at, seasons_json, seasons_synced_at, status,
       target_account_id, target_parent_id, target_display_path, quality_profile_id, push_provider,
       collect_window_min, upgrade_enabled, pending_deadline_at, best_quality_score,
       last_match_at, last_push_at, matched_count, pushed_count, last_error, created_at, updated_at`

func (r *tgSubscriptionRepo) Create(ctx context.Context, s *domain.TGSubscription) (int64, error) {
	if s == nil || strings.TrimSpace(s.TMDBID) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的订阅")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO tg_subscriptions(tmdb_id, media_type, title, original_title, year, poster_path, overview,
       aliases_json, aliases_synced_at, seasons_json, seasons_synced_at, status,
       target_account_id, target_parent_id, target_display_path, quality_profile_id, push_provider,
       collect_window_min, upgrade_enabled, pending_deadline_at, best_quality_score,
       last_match_at, last_push_at, matched_count, pushed_count, last_error)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.TMDBID, s.MediaType, s.Title, s.OriginalTitle, s.Year, s.PosterPath, s.Overview,
		jsonOr(s.Aliases, "[]"), tsValue(s.AliasesSyncedAt), jsonRawOr(s.Seasons, "[]"), tsValue(s.SeasonsSyncedAt), s.Status,
		s.TargetAccountID, s.TargetParentID, s.TargetDisplayPath, s.QualityProfileID, s.PushProvider,
		s.CollectWindowMin, boolToInt(s.UpgradeEnabled), tsValue(s.PendingDeadlineAt), s.BestQualityScore,
		tsValue(s.LastMatchAt), tsValue(s.LastPushAt), s.MatchedCount, s.PushedCount, s.LastError,
	)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *tgSubscriptionRepo) Update(ctx context.Context, s *domain.TGSubscription) error {
	if s == nil || s.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的订阅")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET tmdb_id=?, media_type=?, title=?, original_title=?, year=?, poster_path=?, overview=?,
       aliases_json=?, aliases_synced_at=?, seasons_json=?, seasons_synced_at=?, status=?,
       target_account_id=?, target_parent_id=?, target_display_path=?, quality_profile_id=?, push_provider=?,
       collect_window_min=?, upgrade_enabled=?, pending_deadline_at=?, best_quality_score=?,
       last_match_at=?, last_push_at=?, matched_count=?, pushed_count=?, last_error=?, updated_at=CURRENT_TIMESTAMP
WHERE id=?`,
		s.TMDBID, s.MediaType, s.Title, s.OriginalTitle, s.Year, s.PosterPath, s.Overview,
		jsonOr(s.Aliases, "[]"), tsValue(s.AliasesSyncedAt), jsonRawOr(s.Seasons, "[]"), tsValue(s.SeasonsSyncedAt), s.Status,
		s.TargetAccountID, s.TargetParentID, s.TargetDisplayPath, s.QualityProfileID, s.PushProvider,
		s.CollectWindowMin, boolToInt(s.UpgradeEnabled), tsValue(s.PendingDeadlineAt), s.BestQualityScore,
		tsValue(s.LastMatchAt), tsValue(s.LastPushAt), s.MatchedCount, s.PushedCount, s.LastError, s.ID,
	)
	return wrapDB(err)
}

func (r *tgSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tg_subscription_episodes WHERE subscription_id=?`, id); err != nil {
		_ = tx.Rollback()
		return wrapDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tg_subscriptions WHERE id=?`, id); err != nil {
		_ = tx.Rollback()
		return wrapDB(err)
	}
	return wrapDB(tx.Commit())
}

func (r *tgSubscriptionRepo) Get(ctx context.Context, id int64) (*domain.TGSubscription, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+tgSubscriptionColumns+` FROM tg_subscriptions WHERE id=?`, id)
	return scanTGSubscription(row)
}

func (r *tgSubscriptionRepo) GetByTMDB(ctx context.Context, tmdbID, mediaType string) (*domain.TGSubscription, error) {
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+tgSubscriptionColumns+` FROM tg_subscriptions WHERE tmdb_id=? AND media_type=?`,
		strings.TrimSpace(tmdbID), strings.TrimSpace(mediaType))
	return scanTGSubscription(row)
}

func (r *tgSubscriptionRepo) List(ctx context.Context, status string) ([]*domain.TGSubscription, error) {
	q := `SELECT ` + tgSubscriptionColumns + ` FROM tg_subscriptions`
	args := make([]any, 0, 1)
	if s := strings.TrimSpace(status); s != "" {
		q += ` WHERE status=?`
		args = append(args, s)
	}
	q += ` ORDER BY id DESC`
	rows, err := r.db.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGSubscription, 0)
	for rows.Next() {
		s, err := scanTGSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, wrapDB(rows.Err())
}

func (r *tgSubscriptionRepo) ListPending(ctx context.Context, now time.Time) ([]*domain.TGSubscription, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT `+tgSubscriptionColumns+` FROM tg_subscriptions
WHERE pending_deadline_at IS NOT NULL AND pending_deadline_at <= ? AND status=?
ORDER BY pending_deadline_at ASC`, now.UTC().Format(tsLayout), domain.TGSubStatusActive)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGSubscription, 0)
	for rows.Next() {
		s, err := scanTGSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, wrapDB(rows.Err())
}

// TouchPending 只在 deadline 为空或更早时前移，避免窗口被后续候选无限延长。
func (r *tgSubscriptionRepo) TouchPending(ctx context.Context, id int64, deadline time.Time) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET pending_deadline_at=?, updated_at=CURRENT_TIMESTAMP
WHERE id=? AND (pending_deadline_at IS NULL OR pending_deadline_at > ?)`,
		tsValue(deadline), id, deadline.UTC().Format(tsLayout))
	return wrapDB(err)
}

func (r *tgSubscriptionRepo) ClearPending(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET pending_deadline_at=NULL, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return wrapDB(err)
}

func (r *tgSubscriptionRepo) MarkMatched(ctx context.Context, id int64, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET last_match_at=?, matched_count=matched_count+1, updated_at=CURRENT_TIMESTAMP
WHERE id=?`, tsValue(at), id)
	return wrapDB(err)
}

// MarkPushed 记录已推送的最高画质分。best_quality_score 只升不降 —— 它是洗版基线。
func (r *tgSubscriptionRepo) MarkPushed(ctx context.Context, id int64, at time.Time, qualityScore float64) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET last_push_at=?, pushed_count=pushed_count+1,
       best_quality_score=MAX(best_quality_score, ?), last_error='', updated_at=CURRENT_TIMESTAMP
WHERE id=?`, tsValue(at), qualityScore, id)
	return wrapDB(err)
}

func (r *tgSubscriptionRepo) MarkError(ctx context.Context, id int64, message string) error {
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_subscriptions SET last_error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, message, id)
	return wrapDB(err)
}

func scanTGSubscription(row tgScanner) (*domain.TGSubscription, error) {
	var s domain.TGSubscription
	var aliases, seasons string
	var aliasesAt, seasonsAt, pendingAt, lastMatchAt, lastPushAt, createdAt, updatedAt sql.NullString
	var upgrade int
	if err := row.Scan(&s.ID, &s.TMDBID, &s.MediaType, &s.Title, &s.OriginalTitle, &s.Year, &s.PosterPath, &s.Overview,
		&aliases, &aliasesAt, &seasons, &seasonsAt, &s.Status,
		&s.TargetAccountID, &s.TargetParentID, &s.TargetDisplayPath, &s.QualityProfileID, &s.PushProvider,
		&s.CollectWindowMin, &upgrade, &pendingAt, &s.BestQualityScore,
		&lastMatchAt, &lastPushAt, &s.MatchedCount, &s.PushedCount, &s.LastError, &createdAt, &updatedAt); err != nil {
		return nil, wrapDB(err)
	}
	s.UpgradeEnabled = upgrade != 0
	s.Seasons = json.RawMessage(seasons)
	_ = json.Unmarshal([]byte(aliases), &s.Aliases)
	s.AliasesSyncedAt = parseTS(aliasesAt)
	s.SeasonsSyncedAt = parseTS(seasonsAt)
	s.PendingDeadlineAt = parseTS(pendingAt)
	s.LastMatchAt = parseTS(lastMatchAt)
	s.LastPushAt = parseTS(lastPushAt)
	s.CreatedAt = parseTS(createdAt)
	s.UpdatedAt = parseTS(updatedAt)
	return &s, nil
}

// ——————————————— tg_subscription_episodes ———————————————

type tgSubscriptionEpisodeRepo struct{ db *DB }

func (r *tgSubscriptionEpisodeRepo) Upsert(ctx context.Context, e *domain.TGSubscriptionEpisode) error {
	if e == nil || e.SubscriptionID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的剧集进度")
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO tg_subscription_episodes(subscription_id, season, episode, record_id, offline_task_id,
       account_id, file_id, target_path)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(subscription_id, season, episode) DO UPDATE SET
    record_id=excluded.record_id,
    offline_task_id=excluded.offline_task_id,
    account_id=excluded.account_id,
    file_id=excluded.file_id,
    target_path=excluded.target_path`,
		e.SubscriptionID, e.Season, e.Episode, e.RecordID, e.OfflineTaskID,
		e.AccountID, e.FileID, e.TargetPath)
	return wrapDB(err)
}

func (r *tgSubscriptionEpisodeRepo) Has(ctx context.Context, subscriptionID int64, season, episode int) (bool, error) {
	var one int
	err := r.db.read.QueryRowContext(ctx, `
SELECT 1 FROM tg_subscription_episodes WHERE subscription_id=? AND season=? AND episode=? LIMIT 1`,
		subscriptionID, season, episode).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, wrapDB(err)
	}
	return true, nil
}

func (r *tgSubscriptionEpisodeRepo) ListBySubscription(ctx context.Context, subscriptionID int64) ([]*domain.TGSubscriptionEpisode, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT id, subscription_id, season, episode, record_id, offline_task_id, account_id, file_id, target_path, delivered_at
FROM tg_subscription_episodes WHERE subscription_id=? ORDER BY season ASC, episode ASC`, subscriptionID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGSubscriptionEpisode, 0)
	for rows.Next() {
		var e domain.TGSubscriptionEpisode
		var deliveredAt sql.NullString
		if err := rows.Scan(&e.ID, &e.SubscriptionID, &e.Season, &e.Episode, &e.RecordID, &e.OfflineTaskID,
			&e.AccountID, &e.FileID, &e.TargetPath, &deliveredAt); err != nil {
			return nil, wrapDB(err)
		}
		e.DeliveredAt = parseTS(deliveredAt)
		out = append(out, &e)
	}
	return out, wrapDB(rows.Err())
}

func (r *tgSubscriptionEpisodeRepo) DeleteBySubscription(ctx context.Context, subscriptionID int64) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM tg_subscription_episodes WHERE subscription_id=?`, subscriptionID)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

// ——————————————————— tg_match_records ————————————————————

type tgMatchRecordRepo struct{ db *DB }

const tgMatchRecordColumns = `id, channel_id, chat_title, message_id, message_date, raw_name, name_source,
       magnet, magnet_hash, size_bytes, parsed_title, parsed_year, season, episode, episode_end, is_batch,
       resolution, video_codec, source_tag, subscription_id, match_score, quality_score, status, reason,
       offline_task_id, account_id, target_parent_id, provider_kind, retry_count, next_retry_at,
       created_at, updated_at`

// Create 用 ON CONFLICT DO NOTHING 落库：命中唯一索引说明这条已经被处理过，
// 返回 id=0 让调用方直接跳过。这样 offset 回退 / 崩溃重启重放都是幂等的。
func (r *tgMatchRecordRepo) Create(ctx context.Context, m *domain.TGMatchRecord) (int64, error) {
	if m == nil || strings.TrimSpace(m.MagnetHash) == "" {
		return 0, domain.Errorf(domain.CodeValidation, "无效的匹配记录")
	}
	res, err := r.db.write.ExecContext(ctx, `
INSERT INTO tg_match_records(channel_id, chat_title, message_id, message_date, raw_name, name_source,
       magnet, magnet_hash, size_bytes, parsed_title, parsed_year, season, episode, episode_end, is_batch,
       resolution, video_codec, source_tag, subscription_id, match_score, quality_score, status, reason,
       offline_task_id, account_id, target_parent_id, provider_kind, retry_count, next_retry_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT DO NOTHING`,
		m.ChannelID, m.ChatTitle, m.MessageID, tsValue(m.MessageDate), m.RawName, m.NameSource,
		m.Magnet, m.MagnetHash, m.SizeBytes, m.ParsedTitle, m.ParsedYear, m.Season, m.Episode, m.EpisodeEnd, boolToInt(m.IsBatch),
		m.Resolution, m.VideoCodec, m.SourceTag, m.SubscriptionID, m.MatchScore, m.QualityScore, m.Status, m.Reason,
		m.OfflineTaskID, m.AccountID, m.TargetParentID, m.ProviderKind, m.RetryCount, tsValue(m.NextRetryAt),
	)
	if err != nil {
		return 0, wrapDB(err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return 0, nil
	}
	id, err := res.LastInsertId()
	return id, wrapDB(err)
}

func (r *tgMatchRecordRepo) Update(ctx context.Context, m *domain.TGMatchRecord) error {
	if m == nil || m.ID <= 0 {
		return domain.Errorf(domain.CodeValidation, "无效的匹配记录")
	}
	_, err := r.db.write.ExecContext(ctx, `
UPDATE tg_match_records SET subscription_id=?, match_score=?, quality_score=?, status=?, reason=?,
       offline_task_id=?, account_id=?, target_parent_id=?, provider_kind=?, retry_count=?, next_retry_at=?,
       updated_at=CURRENT_TIMESTAMP
WHERE id=?`,
		m.SubscriptionID, m.MatchScore, m.QualityScore, m.Status, m.Reason,
		m.OfflineTaskID, m.AccountID, m.TargetParentID, m.ProviderKind, m.RetryCount, tsValue(m.NextRetryAt), m.ID)
	return wrapDB(err)
}

func (r *tgMatchRecordRepo) Get(ctx context.Context, id int64) (*domain.TGMatchRecord, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+tgMatchRecordColumns+` FROM tg_match_records WHERE id=?`, id)
	return scanTGMatchRecord(row)
}

func (r *tgMatchRecordRepo) List(ctx context.Context, f domain.TGMatchRecordFilter) ([]*domain.TGMatchRecord, int, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if f.ChannelID > 0 {
		where = append(where, "channel_id=?")
		args = append(args, f.ChannelID)
	}
	if f.SubscriptionID > 0 {
		where = append(where, "subscription_id=?")
		args = append(args, f.SubscriptionID)
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		where = append(where, "status=?")
		args = append(args, s)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		where = append(where, "(raw_name LIKE ? OR parsed_title LIKE ? OR reason LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like, like)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM tg_match_records`+clause, args...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+tgMatchRecordColumns+` FROM tg_match_records`+clause+` ORDER BY id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGMatchRecord, 0)
	for rows.Next() {
		m, err := scanTGMatchRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, wrapDB(rows.Err())
}

func (r *tgMatchRecordRepo) ListPendingBySubscription(ctx context.Context, subscriptionID int64) ([]*domain.TGMatchRecord, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+tgMatchRecordColumns+` FROM tg_match_records WHERE subscription_id=? AND status=? ORDER BY id ASC`,
		subscriptionID, domain.TGRecordPending)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGMatchRecord, 0)
	for rows.Next() {
		m, err := scanTGMatchRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapDB(rows.Err())
}

func (r *tgMatchRecordRepo) ListRetryable(ctx context.Context, now time.Time, limit int) ([]*domain.TGMatchRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.read.QueryContext(ctx, `
SELECT `+tgMatchRecordColumns+` FROM tg_match_records
WHERE status=? AND retry_count < 5 AND (next_retry_at IS NULL OR next_retry_at <= ?)
ORDER BY next_retry_at ASC LIMIT ?`, domain.TGRecordFailed, now.UTC().Format(tsLayout), limit)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]*domain.TGMatchRecord, 0)
	for rows.Next() {
		m, err := scanTGMatchRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapDB(rows.Err())
}

// GetByOfflineTaskID 按离线任务 ID 反查记录。
//
// 下载完成事件带回的就是离线任务 ID，native 与 builtin 两条推送路径都是同一个值，
// 所以这是唯一的关联键。走索引，不用扫全表。
func (r *tgMatchRecordRepo) GetByOfflineTaskID(ctx context.Context, taskID string) (*domain.TGMatchRecord, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "缺少离线任务 ID")
	}
	row := r.db.read.QueryRowContext(ctx,
		`SELECT `+tgMatchRecordColumns+` FROM tg_match_records WHERE offline_task_id=? ORDER BY id DESC LIMIT 1`,
		taskID)
	return scanTGMatchRecord(row)
}

func (r *tgMatchRecordRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.read.QueryContext(ctx, `SELECT status, COUNT(*) FROM tg_match_records GROUP BY status`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, wrapDB(err)
		}
		out[status] = n
	}
	return out, wrapDB(rows.Err())
}

func (r *tgMatchRecordRepo) ClearBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`DELETE FROM tg_match_records WHERE created_at < ?`, before.UTC().Format(tsLayout))
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	return n, wrapDB(err)
}

func scanTGMatchRecord(row tgScanner) (*domain.TGMatchRecord, error) {
	var m domain.TGMatchRecord
	var messageDate, nextRetryAt, createdAt, updatedAt sql.NullString
	var isBatch int
	if err := row.Scan(&m.ID, &m.ChannelID, &m.ChatTitle, &m.MessageID, &messageDate, &m.RawName, &m.NameSource,
		&m.Magnet, &m.MagnetHash, &m.SizeBytes, &m.ParsedTitle, &m.ParsedYear, &m.Season, &m.Episode, &m.EpisodeEnd, &isBatch,
		&m.Resolution, &m.VideoCodec, &m.SourceTag, &m.SubscriptionID, &m.MatchScore, &m.QualityScore, &m.Status, &m.Reason,
		&m.OfflineTaskID, &m.AccountID, &m.TargetParentID, &m.ProviderKind, &m.RetryCount, &nextRetryAt,
		&createdAt, &updatedAt); err != nil {
		return nil, wrapDB(err)
	}
	m.IsBatch = isBatch != 0
	m.MessageDate = parseTS(messageDate)
	m.NextRetryAt = parseTS(nextRetryAt)
	m.CreatedAt = parseTS(createdAt)
	m.UpdatedAt = parseTS(updatedAt)
	return &m, nil
}

func jsonOr(v []string, fallback string) string {
	if len(v) == 0 {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func jsonRawOr(v json.RawMessage, fallback string) string {
	if len(v) == 0 {
		return fallback
	}
	return string(v)
}
