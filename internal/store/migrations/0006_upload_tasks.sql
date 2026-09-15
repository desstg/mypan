CREATE TABLE upload_tasks (
    task_id              TEXT PRIMARY KEY,
    client_task_id       TEXT NOT NULL DEFAULT '',
    account_id           INTEGER NOT NULL,
    account_name         TEXT NOT NULL DEFAULT '',
    driver_type          TEXT NOT NULL DEFAULT '',
    file_name            TEXT NOT NULL DEFAULT '',
    target_path          TEXT NOT NULL DEFAULT '',
    target_display_path  TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending',
    progress             INTEGER NOT NULL DEFAULT 0,
    uploaded_bytes       INTEGER NOT NULL DEFAULT 0,
    speed_bps            REAL NOT NULL DEFAULT 0,
    total_bytes          INTEGER NOT NULL DEFAULT 0,
    message              TEXT NOT NULL DEFAULT '',
    error                TEXT NOT NULL DEFAULT '',
    result_json          TEXT NOT NULL DEFAULT '',
    resume_data_json     TEXT NOT NULL DEFAULT '',
    queue_order          INTEGER NOT NULL DEFAULT 0,
    created_at           REAL NOT NULL DEFAULT 0,
    updated_at           REAL NOT NULL DEFAULT 0,
    local_path           TEXT NOT NULL DEFAULT '',
    conflict_policy      TEXT NOT NULL DEFAULT 'overwrite'
);

CREATE INDEX idx_upload_tasks_account ON upload_tasks(account_id);
CREATE INDEX idx_upload_tasks_status ON upload_tasks(status);
