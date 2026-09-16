-- TG 影片订阅：频道（订阅源）
CREATE TABLE IF NOT EXISTS tg_channels (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         TEXT    NOT NULL,
    username        TEXT    NOT NULL DEFAULT '',
    title           TEXT    NOT NULL DEFAULT '',
    remark          TEXT    NOT NULL DEFAULT '',
    level           INTEGER NOT NULL DEFAULT 10,
    enabled         INTEGER NOT NULL DEFAULT 1,
    status          TEXT    NOT NULL DEFAULT 'unknown',
    last_error      TEXT    NOT NULL DEFAULT '',
    last_message_id INTEGER NOT NULL DEFAULT 0,
    last_post_at    TEXT,
    matched_count   INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_channels_chat_id ON tg_channels(chat_id);
CREATE INDEX IF NOT EXISTS idx_tg_channels_enabled ON tg_channels(enabled);

-- TG 影片订阅：画质方案
CREATE TABLE IF NOT EXISTS tg_quality_profiles (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    is_default  INTEGER NOT NULL DEFAULT 0,
    config_json TEXT    NOT NULL DEFAULT '{}',
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- TG 影片订阅：订阅（影片/剧集）
CREATE TABLE IF NOT EXISTS tg_subscriptions (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id              TEXT    NOT NULL,
    media_type           TEXT    NOT NULL,
    title                TEXT    NOT NULL DEFAULT '',
    original_title       TEXT    NOT NULL DEFAULT '',
    year                 INTEGER NOT NULL DEFAULT 0,
    poster_path          TEXT    NOT NULL DEFAULT '',
    overview             TEXT    NOT NULL DEFAULT '',
    aliases_json         TEXT    NOT NULL DEFAULT '[]',
    aliases_synced_at    TEXT,
    seasons_json         TEXT    NOT NULL DEFAULT '[]',
    seasons_synced_at    TEXT,
    status               TEXT    NOT NULL DEFAULT 'active',
    target_account_id    INTEGER NOT NULL DEFAULT 0,
    target_parent_id     TEXT    NOT NULL DEFAULT '',
    target_display_path  TEXT    NOT NULL DEFAULT '',
    quality_profile_id   INTEGER NOT NULL DEFAULT 0,
    push_provider        TEXT    NOT NULL DEFAULT 'auto',
    collect_window_min   INTEGER NOT NULL DEFAULT 5,
    upgrade_enabled      INTEGER NOT NULL DEFAULT 1,
    pending_deadline_at  TEXT,
    best_quality_score   REAL    NOT NULL DEFAULT 0,
    last_match_at        TEXT,
    last_push_at         TEXT,
    matched_count        INTEGER NOT NULL DEFAULT 0,
    pushed_count         INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT    NOT NULL DEFAULT '',
    created_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_subs_tmdb ON tg_subscriptions(tmdb_id, media_type);
CREATE INDEX IF NOT EXISTS idx_tg_subs_status ON tg_subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_tg_subs_pending ON tg_subscriptions(pending_deadline_at);

-- TG 影片订阅：剧集已收集进度
CREATE TABLE IF NOT EXISTS tg_subscription_episodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL,
    season          INTEGER NOT NULL DEFAULT 0,
    episode         INTEGER NOT NULL DEFAULT 0,
    record_id       INTEGER NOT NULL DEFAULT 0,
    offline_task_id TEXT    NOT NULL DEFAULT '',
    account_id      INTEGER NOT NULL DEFAULT 0,
    file_id         TEXT    NOT NULL DEFAULT '',
    target_path     TEXT    NOT NULL DEFAULT '',
    delivered_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_ep_unique ON tg_subscription_episodes(subscription_id, season, episode);

-- TG 影片订阅：匹配历史 / 命中记录
CREATE TABLE IF NOT EXISTS tg_match_records (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id          INTEGER NOT NULL DEFAULT 0,
    chat_title          TEXT    NOT NULL DEFAULT '',
    message_id          INTEGER NOT NULL DEFAULT 0,
    message_date        TEXT,
    raw_name            TEXT    NOT NULL DEFAULT '',
    name_source         TEXT    NOT NULL DEFAULT '',
    magnet              TEXT    NOT NULL DEFAULT '',
    magnet_hash         TEXT    NOT NULL DEFAULT '',
    size_bytes          INTEGER NOT NULL DEFAULT 0,
    parsed_title        TEXT    NOT NULL DEFAULT '',
    parsed_year         INTEGER NOT NULL DEFAULT 0,
    season              INTEGER NOT NULL DEFAULT -1,
    episode             INTEGER NOT NULL DEFAULT -1,
    episode_end         INTEGER NOT NULL DEFAULT -1,
    is_batch            INTEGER NOT NULL DEFAULT 0,
    resolution          TEXT    NOT NULL DEFAULT '',
    video_codec         TEXT    NOT NULL DEFAULT '',
    source_tag          TEXT    NOT NULL DEFAULT '',
    subscription_id     INTEGER NOT NULL DEFAULT 0,
    match_score         REAL    NOT NULL DEFAULT 0,
    quality_score       REAL    NOT NULL DEFAULT 0,
    status              TEXT    NOT NULL DEFAULT 'unmatched',
    reason              TEXT    NOT NULL DEFAULT '',
    offline_task_id     TEXT    NOT NULL DEFAULT '',
    account_id          INTEGER NOT NULL DEFAULT 0,
    target_parent_id    TEXT    NOT NULL DEFAULT '',
    provider_kind       TEXT    NOT NULL DEFAULT '',
    retry_count         INTEGER NOT NULL DEFAULT 0,
    next_retry_at       TEXT,
    created_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_rec_msg_magnet ON tg_match_records(channel_id, message_id, magnet_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_rec_sub_magnet ON tg_match_records(subscription_id, magnet_hash) WHERE subscription_id > 0;
CREATE INDEX IF NOT EXISTS idx_tg_rec_status ON tg_match_records(status);
CREATE INDEX IF NOT EXISTS idx_tg_rec_sub ON tg_match_records(subscription_id);
CREATE INDEX IF NOT EXISTS idx_tg_rec_created ON tg_match_records(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tg_rec_retry ON tg_match_records(next_retry_at);
-- 下载完成后按离线任务 ID 反查记录：它是 native 与 builtin 两条推送路径共用的关联键。
CREATE INDEX IF NOT EXISTS idx_tg_rec_offline_task ON tg_match_records(offline_task_id);

-- 种子画质方案（默认方案固定 id=1）
INSERT INTO tg_quality_profiles (id, name, is_default, config_json)
SELECT 1, '默认方案', 1, '{"prefer_resolution":["2160p","1080p","720p"],"prefer_codec":["AV1","H.265","H.264"],"prefer_source":["Remux","BluRay","WEB-DL","HDTV"],"exclude_keywords":["CAM","TS","枪版","抢先","预告","Trailer","Sample"],"min_resolution":"","max_size_gb":0,"weights":{"resolution":50,"source":30,"codec":20}}'
WHERE NOT EXISTS (SELECT 1 FROM tg_quality_profiles WHERE id = 1);
