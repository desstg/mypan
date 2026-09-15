CREATE TABLE fuse_mounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    account_id      INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    root_item_id    TEXT NOT NULL,
    root_path       TEXT NOT NULL DEFAULT '',
    mount_point     TEXT NOT NULL UNIQUE,
    read_only       INTEGER NOT NULL DEFAULT 1,
    auto_mount      INTEGER NOT NULL DEFAULT 1,
    uid             INTEGER NOT NULL DEFAULT 0,
    gid             INTEGER NOT NULL DEFAULT 0,
    dir_mode        INTEGER NOT NULL DEFAULT 493,
    file_mode       INTEGER NOT NULL DEFAULT 420,
    enabled         INTEGER NOT NULL DEFAULT 1,
    state           TEXT NOT NULL DEFAULT 'unmounted',
    last_error      TEXT NOT NULL DEFAULT '',
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_fuse_mounts_account ON fuse_mounts(account_id);
CREATE INDEX idx_fuse_mounts_state ON fuse_mounts(state);
