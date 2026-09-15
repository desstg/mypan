CREATE TABLE IF NOT EXISTS api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL,
    key_suffix   TEXT NOT NULL,
    key_type     TEXT NOT NULL DEFAULT 'task',
    status       TEXT NOT NULL DEFAULT 'active',
    expires_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_api_keys_status ON api_keys(status);
