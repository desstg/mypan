ALTER TABLE strm_tasks ADD COLUMN api_interval INTEGER NOT NULL DEFAULT 200;
ALTER TABLE strm_tasks ADD COLUMN exclude_dir_keywords TEXT NOT NULL DEFAULT '';
ALTER TABLE strm_tasks ADD COLUMN exclude_file_keywords TEXT NOT NULL DEFAULT '';
ALTER TABLE strm_tasks ADD COLUMN sync_metadata INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strm_tasks ADD COLUMN branch_check_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strm_tasks ADD COLUMN time_window_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strm_tasks ADD COLUMN time_start TEXT NOT NULL DEFAULT '00:00';
ALTER TABLE strm_tasks ADD COLUMN time_end TEXT NOT NULL DEFAULT '00:00';
ALTER TABLE strm_tasks ADD COLUMN schedule_mode TEXT NOT NULL DEFAULT 'window';

CREATE TABLE IF NOT EXISTS strm_branches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         INTEGER NOT NULL REFERENCES strm_tasks(id) ON DELETE CASCADE,
    account_id      INTEGER NOT NULL,
    parent_id       TEXT NOT NULL,
    path            TEXT NOT NULL,
    relative_path   TEXT NOT NULL DEFAULT '',
    recursive       INTEGER NOT NULL DEFAULT 1,
    retention_days  INTEGER NOT NULL DEFAULT 30,
    expires_at      TIMESTAMP,
    branch_type     TEXT NOT NULL DEFAULT 'temporary',
    status          TEXT NOT NULL DEFAULT 'running',
    source          TEXT NOT NULL DEFAULT 'manual',
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, parent_id)
);

CREATE INDEX IF NOT EXISTS idx_strm_branches_task ON strm_branches(task_id);
