CREATE TABLE IF NOT EXISTS quarktv_bindings (
    account_id        INTEGER PRIMARY KEY,
    device_id         TEXT NOT NULL,
    refresh_token     TEXT NOT NULL,
    access_token      TEXT NOT NULL DEFAULT '',
    token_expires_at  INTEGER NOT NULL DEFAULT 0,
    tv_uid            TEXT NOT NULL DEFAULT '',
    tv_nickname       TEXT NOT NULL DEFAULT '',
    bound_at          INTEGER NOT NULL
);
