CREATE TABLE cloud_accounts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    driver_type TEXT NOT NULL,
    config      TEXT NOT NULL DEFAULT '',
    is_active   INTEGER NOT NULL DEFAULT 1,
    is_default  INTEGER NOT NULL DEFAULT 0,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE account_auth_states (
    account_id       INTEGER PRIMARY KEY REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    status           TEXT NOT NULL DEFAULT 'active',
    access_token     TEXT NOT NULL DEFAULT '',
    refresh_token    TEXT NOT NULL DEFAULT '',
    token_expires    TIMESTAMP,
    cookie           TEXT NOT NULL DEFAULT '',
    cookie_expires   TIMESTAMP,
    active_attempts  INTEGER NOT NULL DEFAULT 0,
    passive_attempts INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    next_retry_at    TIMESTAMP,
    last_refresh_at  TIMESTAMP,
    last_notified_at TIMESTAMP
);

CREATE TABLE configs (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
