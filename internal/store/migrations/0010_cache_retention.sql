CREATE TABLE cache_retention_tasks (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id          INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    parent_id           TEXT NOT NULL,
    path                TEXT NOT NULL DEFAULT '',
    scan_depth          INTEGER NOT NULL DEFAULT 4,
    api_interval        INTEGER NOT NULL DEFAULT 200,
    refresh_interval    INTEGER NOT NULL DEFAULT 60,
    status              TEXT NOT NULL DEFAULT 'running',
    paused_reason       TEXT NOT NULL DEFAULT '',
    file_count          INTEGER NOT NULL DEFAULT 0,
    last_refresh        TIMESTAMP,
    last_refresh_status TEXT NOT NULL DEFAULT '',
    last_duration_ms    INTEGER NOT NULL DEFAULT 0,
    last_api_calls      INTEGER NOT NULL DEFAULT 0,
    last_skip_calls     INTEGER NOT NULL DEFAULT 0,
    last_scanned_dirs   INTEGER NOT NULL DEFAULT 0,
    error_message       TEXT NOT NULL DEFAULT '',
    time_window_enabled INTEGER NOT NULL DEFAULT 0,
    time_start          TEXT NOT NULL DEFAULT '00:00',
    time_end            TEXT NOT NULL DEFAULT '23:59',
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(account_id, parent_id)
);

CREATE INDEX idx_cache_retention_account ON cache_retention_tasks(account_id);
CREATE INDEX idx_cache_retention_status ON cache_retention_tasks(status);
