-- 番号（JAV）功能模块。
--
-- 表名统一 jav_ 前缀，与 tg_* 的命名纪律一致 —— 同一库里各功能模块自成一域，
-- 避免与已有的 strm_* / upload_* / automation_* 之类撞名。
--
-- 这一批表来自 Python 项目 javdb-center 的 javdb/db.py，但**没有逐表照搬**：
--   * 丢弃 subscription_priority_options：那张表存着 priority_order / score_version，
--     而 resource_score() 根本不读它 —— 候选顺序是硬编码的 [清晰度, 破解, 大小]。
--     存一个被忽略的配置项是个陷阱，将来真要做可配置优先级再加迁移。
--   * qualities 从关联表改成 JSON 列（与 tg_quality_profiles.config_json 同款）：
--     最多 4 个元素，关联表在这里只带来 join 成本。

-- ———————————————————————— 元数据 ————————————————————————

CREATE TABLE IF NOT EXISTS jav_movies (
    id                  TEXT PRIMARY KEY,            -- JAVDB API 的影片 id，如 ZY5eq
    number              TEXT NOT NULL DEFAULT '',    -- 番号，如 SSIS-001
    title               TEXT NOT NULL DEFAULT '',
    origin_title        TEXT NOT NULL DEFAULT '',
    cover_url           TEXT NOT NULL DEFAULT '',
    thumb_url           TEXT NOT NULL DEFAULT '',
    javbus_cover        TEXT NOT NULL DEFAULT '',    -- JAVBUS 抓到的干净封面
    duration            INTEGER,                     -- 分钟
    release_date        TEXT NOT NULL DEFAULT '',
    score               REAL,
    summary             TEXT NOT NULL DEFAULT '',
    review              TEXT NOT NULL DEFAULT '',
    director_id         TEXT NOT NULL DEFAULT '',
    director_name       TEXT NOT NULL DEFAULT '',
    maker_id            TEXT NOT NULL DEFAULT '',
    maker_name          TEXT NOT NULL DEFAULT '',
    publisher_id        TEXT NOT NULL DEFAULT '',
    publisher_name      TEXT NOT NULL DEFAULT '',
    series_id           TEXT NOT NULL DEFAULT '',
    series_name         TEXT NOT NULL DEFAULT '',
    tags_json           TEXT NOT NULL DEFAULT '[]',  -- 只存类别名数组，展示够用
    preview_images_json TEXT NOT NULL DEFAULT '[]',
    preview_video_url   TEXT NOT NULL DEFAULT '',
    magnets_count       INTEGER NOT NULL DEFAULT 0,
    reviews_count       INTEGER NOT NULL DEFAULT 0,
    has_cnsub           INTEGER NOT NULL DEFAULT 0,
    has_preview_images  INTEGER NOT NULL DEFAULT 0,
    has_preview_video   INTEGER NOT NULL DEFAULT 0,
    can_play            INTEGER NOT NULL DEFAULT 0,
    type                TEXT NOT NULL DEFAULT '',    -- '0' 有码 / '1' 无码 / '2' 欧美 / '3' FC2
    number_letter       TEXT NOT NULL DEFAULT '',
    raw_json            TEXT NOT NULL DEFAULT '',    -- 详情原始 JSON，保证不丢字段
    fetched_at          TEXT,
    last_viewed_at      TEXT,
    created_at          TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_jav_movies_number  ON jav_movies(number);
CREATE INDEX IF NOT EXISTS idx_jav_movies_release ON jav_movies(release_date DESC);
CREATE INDEX IF NOT EXISTS idx_jav_movies_score   ON jav_movies(score DESC);
CREATE INDEX IF NOT EXISTS idx_jav_movies_type    ON jav_movies(type, release_date DESC);

CREATE TABLE IF NOT EXISTS jav_actors (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    gender     INTEGER,
    avatar_url TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jav_actors_name ON jav_actors(name);

CREATE TABLE IF NOT EXISTS jav_movie_actors (
    movie_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    PRIMARY KEY (movie_id, actor_id)
);
-- 演员订阅的 TargetResolver 要「某演员的全部影片」，走这条索引。
CREATE INDEX IF NOT EXISTS idx_jav_movie_actors_actor ON jav_movie_actors(actor_id);

CREATE TABLE IF NOT EXISTS jav_magnets (
    -- 主键是资源指纹，与 tg_match_records.magnet_hash 同一套约定：
    -- 有 40 位 btih 就是裸 btih，没有就退化成 'sha1:<hex>'。
    --
    -- **绝不留 NULL**：SQLite 的 TEXT 主键允许 NULL，一旦落进 NULL，
    -- 同一颗磁链每次抓取都会再插一行（NULL 在唯一约束下互不相等）。
    fingerprint TEXT PRIMARY KEY,
    btih        TEXT NOT NULL DEFAULT '',
    movie_id    TEXT NOT NULL DEFAULT '',
    code        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    size_text   TEXT NOT NULL DEFAULT '',   -- 原文「2.5GB」，展示用
    size_bytes  INTEGER,                    -- 落库时解析好，订阅检查不必每次重算
    date_text   TEXT NOT NULL DEFAULT '',
    magnet      TEXT NOT NULL,
    has_hd      INTEGER NOT NULL DEFAULT 0, -- JAVBUS 的高清角标
    has_sub     INTEGER NOT NULL DEFAULT 0, -- JAVBUS 的字幕角标
    file_count  INTEGER,                    -- JAVBUS 给不出，留 NULL
    source      TEXT NOT NULL DEFAULT 'javbus',
    fetched_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_jav_magnets_code  ON jav_magnets(code);
CREATE INDEX IF NOT EXISTS idx_jav_magnets_movie ON jav_magnets(movie_id);

CREATE TABLE IF NOT EXISTS jav_reviews (
    id            INTEGER PRIMARY KEY,     -- API 的 review id
    movie_id      TEXT NOT NULL DEFAULT '',
    user_id       INTEGER NOT NULL DEFAULT 0,
    username      TEXT NOT NULL DEFAULT '',
    score         REAL,
    content       TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    status_title  TEXT NOT NULL DEFAULT '',
    watched_count INTEGER NOT NULL DEFAULT 0,
    likes_count   INTEGER NOT NULL DEFAULT 0,
    liked         INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jav_reviews_movie ON jav_reviews(movie_id, likes_count DESC);

-- ———————————————————————— 订阅 ————————————————————————

CREATE TABLE IF NOT EXISTS jav_subscriptions (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    target_type             TEXT NOT NULL CHECK (target_type IN ('movie','online','actor','list')),
    target_id               TEXT NOT NULL DEFAULT '',
    target_url              TEXT NOT NULL DEFAULT '',
    target_key              TEXT NOT NULL,          -- 规范化键，如 'actor:a1'
    target_name             TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','paused','completed')),
    -- download_mode 与 pre_download 刻意保持**两列正交**（Python 侧也是两列）：
    -- 前者决定「已入库的影片推不推」，后者决定「没有合格磁链时要不要留一颗最优的
    -- 等用户手动确认」。合成一个枚举会在 API 往返里丢掉信息，所以只在视图层合成三档。
    download_mode           TEXT NOT NULL DEFAULT 'strict' CHECK (download_mode IN ('strict','upgrade')),
    pre_download            INTEGER NOT NULL DEFAULT 0,
    qualities_json          TEXT NOT NULL DEFAULT '[]',
    min_size_mb             INTEGER,
    max_size_mb             INTEGER,
    max_file_count          INTEGER,
    release_date_from       TEXT NOT NULL DEFAULT '',
    release_date_to         TEXT NOT NULL DEFAULT '',
    expiry_days             INTEGER,
    categories_json         TEXT NOT NULL DEFAULT '[]',
    exclude_categories_json TEXT NOT NULL DEFAULT '[]',
    enabled                 INTEGER NOT NULL DEFAULT 1,
    -- 推送目标。字段与 tg_subscriptions 对齐，好让「订阅自身 → 全局默认」的回落
    -- 逻辑两边共用一套思路。
    target_account_id       INTEGER NOT NULL DEFAULT 0,   -- 0 = 用全局默认
    target_parent_id        TEXT NOT NULL DEFAULT '',
    target_display_path     TEXT NOT NULL DEFAULT '',
    push_provider           TEXT NOT NULL DEFAULT 'auto',
    -- 子目录策略。默认按番号建「SSIS-001/」—— 媒体服务器重扫时能按番号直接对上，
    -- 这是番号库与影视库最大的差别。
    subfolder_mode          TEXT NOT NULL DEFAULT 'code' CHECK (subfolder_mode IN ('none','code','title')),
    last_checked_at         TEXT,
    last_push_at            TEXT,
    completed_at            TEXT,
    last_error              TEXT NOT NULL DEFAULT '',
    matched_count           INTEGER NOT NULL DEFAULT 0,
    created_at              TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- 同一个目标只能有一条订阅，与 Python 侧同款约束。
    UNIQUE (target_type, target_key)
);
CREATE INDEX IF NOT EXISTS idx_jav_subs_status ON jav_subscriptions(status, enabled);
CREATE INDEX IF NOT EXISTS idx_jav_subs_target ON jav_subscriptions(target_type, target_id);

CREATE TABLE IF NOT EXISTS jav_subscription_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'manual',   -- manual / scheduled
    matcher_version TEXT NOT NULL DEFAULT 'v1',
    status          TEXT NOT NULL DEFAULT 'running',  -- running / completed / failed
    matched_count   INTEGER NOT NULL DEFAULT 0,
    rejected_count  INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_jav_runs_sub ON jav_subscription_runs(subscription_id, id DESC);

CREATE TABLE IF NOT EXISTS jav_subscription_candidates (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    check_run_id           INTEGER NOT NULL,
    subscription_id        INTEGER NOT NULL,
    movie_id               TEXT NOT NULL DEFAULT '',
    magnet_fingerprint     TEXT NOT NULL DEFAULT '',
    magnet_name            TEXT NOT NULL DEFAULT '',
    magnet_uri             TEXT NOT NULL DEFAULT '',
    size_text              TEXT NOT NULL DEFAULT '',
    size_bytes             INTEGER,
    file_count             INTEGER,
    release_date           TEXT NOT NULL DEFAULT '',
    quality_tags_json      TEXT NOT NULL DEFAULT '[]',
    resource_fingerprint   TEXT NOT NULL DEFAULT '',
    resource_score_json    TEXT NOT NULL DEFAULT '[0,0,0]',
    matched                INTEGER NOT NULL DEFAULT 0,
    push_ok                INTEGER NOT NULL DEFAULT 0,
    predownload            INTEGER NOT NULL DEFAULT 0,
    attempted              INTEGER NOT NULL DEFAULT 0,
    rejection_reasons_json TEXT NOT NULL DEFAULT '[]',
    created_at             TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- 一轮检查里同一颗磁链只落一条候选，重跑同一轮不会翻倍。
CREATE UNIQUE INDEX IF NOT EXISTS idx_jav_cand_unique
    ON jav_subscription_candidates(check_run_id, resource_fingerprint);
CREATE INDEX IF NOT EXISTS idx_jav_cand_run  ON jav_subscription_candidates(check_run_id, id);
-- 挑候选（订阅 + 影片 + matched）走这条。
CREATE INDEX IF NOT EXISTS idx_jav_cand_pick ON jav_subscription_candidates(subscription_id, movie_id, matched, attempted);

CREATE TABLE IF NOT EXISTS jav_subscription_push_attempts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL,
    candidate_id    INTEGER NOT NULL DEFAULT 0,
    push_record_id  INTEGER NOT NULL DEFAULT 0,   -- jav_push_records.id
    -- 幂等键是**持久契约**：格式一改，升级前推送过的磁链会全部重推一遍。
    -- 'auto:{sid}:{fp}' / 'sub:{sid}:{mid}:{fp}'，改动前先想清楚。
    idempotency_key TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'running',  -- running / succeeded / failed
    info_hash       TEXT NOT NULL DEFAULT '',
    offline_task_id TEXT NOT NULL DEFAULT '',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jav_attempt_key ON jav_subscription_push_attempts(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_jav_attempt_sub ON jav_subscription_push_attempts(subscription_id, id DESC);
-- 离线完成事件按 info_hash 反查投递尝试。
CREATE INDEX IF NOT EXISTS idx_jav_attempt_hash
    ON jav_subscription_push_attempts(info_hash) WHERE info_hash <> '';
CREATE INDEX IF NOT EXISTS idx_jav_attempt_running
    ON jav_subscription_push_attempts(status) WHERE status = 'running';

CREATE TABLE IF NOT EXISTS jav_subscription_blacklist (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    target_type TEXT NOT NULL CHECK (target_type IN ('movie','actor','list')),
    target_id   TEXT NOT NULL DEFAULT '',
    target_key  TEXT NOT NULL,
    target_name TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (target_type, target_key)
);
CREATE INDEX IF NOT EXISTS idx_jav_blacklist_type ON jav_subscription_blacklist(target_type);

CREATE TABLE IF NOT EXISTS jav_subscription_skips (
    subscription_id INTEGER NOT NULL,
    movie_id        TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (subscription_id, movie_id)
);

CREATE TABLE IF NOT EXISTS jav_list_movies (
    list_id   TEXT NOT NULL,
    movie_id  TEXT NOT NULL,
    position  INTEGER NOT NULL DEFAULT 0,
    synced_at TEXT,
    PRIMARY KEY (list_id, movie_id)
);
CREATE INDEX IF NOT EXISTS idx_jav_list_movies_list ON jav_list_movies(list_id);

-- ———————————————————————— 媒体库（Emby / Jellyfin） ————————————————————————

CREATE TABLE IF NOT EXISTS jav_media_servers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    api_key      TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL DEFAULT 'emby',      -- emby / jellyfin
    enabled      INTEGER NOT NULL DEFAULT 1,
    last_sync_at TEXT,
    last_status  TEXT NOT NULL DEFAULT 'unknown',   -- unknown / ok / error
    last_error   TEXT NOT NULL DEFAULT '',
    item_count   INTEGER NOT NULL DEFAULT 0,        -- 上次同步读到的条目总数
    code_count   INTEGER NOT NULL DEFAULT 0,        -- 其中提取得出番号的条数
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- 同一个地址只该有一行 —— 重复添加时复用已有行，而不是攒出一堆同地址的服务器。
CREATE UNIQUE INDEX IF NOT EXISTS idx_jav_servers_url ON jav_media_servers(url);

CREATE TABLE IF NOT EXISTS jav_library_items (
    server_id  INTEGER NOT NULL,
    item_id    TEXT NOT NULL,
    code       TEXT NOT NULL DEFAULT '',   -- 从 Name/Path 提取的番号，提不出的条目也存
    title      TEXT NOT NULL DEFAULT '',
    path       TEXT NOT NULL DEFAULT '',
    resolution INTEGER,                    -- 0 普通 / 1 高清 / 2 超清；NULL = 尚未质检
    size_bytes INTEGER,
    synced_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, item_id)
);
CREATE INDEX IF NOT EXISTS idx_jav_library_code ON jav_library_items(code);
-- 质检回填只需扫 resolution IS NULL 的行，部分索引把这个集合压到最小。
CREATE INDEX IF NOT EXISTS idx_jav_library_pending
    ON jav_library_items(server_id, item_id) WHERE resolution IS NULL;

-- ———————————————————————— 推送记录 ————————————————————————

CREATE TABLE IF NOT EXISTS jav_push_records (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    magnet          TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    size_text       TEXT NOT NULL DEFAULT '',
    movie_id        TEXT NOT NULL DEFAULT '',
    code            TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','pushed','failed')),
    -- downloader 是一句人话标签（'115 · 我的115' / '内置下载器'），不是驱动类型 ——
    -- 记录页的筛选下拉直接展示它，所以它必须能被人读懂。
    downloader      TEXT NOT NULL DEFAULT '',
    provider_kind   TEXT NOT NULL DEFAULT '',   -- native / builtin
    account_id      INTEGER NOT NULL DEFAULT 0,
    target_path     TEXT NOT NULL DEFAULT '',
    offline_task_id TEXT NOT NULL DEFAULT '',
    subscription_id INTEGER NOT NULL DEFAULT 0, -- 0 = 手动推送
    error           TEXT NOT NULL DEFAULT '',
    pushed_at       TEXT,
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_jav_push_status  ON jav_push_records(status, id DESC);
CREATE INDEX IF NOT EXISTS idx_jav_push_created ON jav_push_records(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jav_push_code    ON jav_push_records(code);
