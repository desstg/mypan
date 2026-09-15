CREATE TABLE strm_tasks (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    name              TEXT NOT NULL UNIQUE,
    account_id        INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    parent_id         TEXT NOT NULL DEFAULT '0',
    path              TEXT NOT NULL DEFAULT '',
    recursive         INTEGER NOT NULL DEFAULT 1,
    scan_interval     INTEGER NOT NULL DEFAULT 360,
    scan_mode         TEXT NOT NULL DEFAULT 'incremental_missing',
    extensions        TEXT NOT NULL DEFAULT '',
    output_folder     TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'active',
    paused_reason     TEXT NOT NULL DEFAULT '',
    scanned_count     INTEGER NOT NULL DEFAULT 0,
    generated_count   INTEGER NOT NULL DEFAULT 0,
    updated_count     INTEGER NOT NULL DEFAULT 0,
    removed_count     INTEGER NOT NULL DEFAULT 0,
    last_scan         TIMESTAMP,
    last_scan_status  TEXT NOT NULL DEFAULT '',
    error_message     TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_strm_tasks_account ON strm_tasks(account_id);
CREATE INDEX idx_strm_tasks_status ON strm_tasks(status);
