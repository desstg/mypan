CREATE TABLE offline_download_tasks (
    task_id              TEXT PRIMARY KEY,
    account_id           INTEGER NOT NULL,
    account_name         TEXT NOT NULL DEFAULT '',
    driver_type          TEXT NOT NULL DEFAULT '',
    source_kind          TEXT NOT NULL DEFAULT 'url',
    source               TEXT NOT NULL DEFAULT '',
    name                 TEXT NOT NULL DEFAULT '',
    provider_task_id     TEXT NOT NULL DEFAULT '',
    info_hash            TEXT NOT NULL DEFAULT '',
    target_parent_id     TEXT NOT NULL DEFAULT '',
    target_display_path  TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending',
    progress             INTEGER NOT NULL DEFAULT 0,
    size                 INTEGER NOT NULL DEFAULT 0,
    file_id              TEXT NOT NULL DEFAULT '',
    message              TEXT NOT NULL DEFAULT '',
    error                TEXT NOT NULL DEFAULT '',
    remote_delete        INTEGER NOT NULL DEFAULT 0,
    created_at           REAL NOT NULL DEFAULT 0,
    updated_at           REAL NOT NULL DEFAULT 0
);

CREATE INDEX idx_offline_download_tasks_account ON offline_download_tasks(account_id);
CREATE INDEX idx_offline_download_tasks_status ON offline_download_tasks(status);
