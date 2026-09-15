CREATE TABLE media_organize_tasks (
    id              TEXT PRIMARY KEY,
    task_name       TEXT NOT NULL,
    account_id      INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    config          TEXT NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'idle',
    last_run_at     TIMESTAMP,
    last_run_result TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_media_organize_tasks_account ON media_organize_tasks(account_id);
CREATE INDEX idx_media_organize_tasks_status ON media_organize_tasks(status);
