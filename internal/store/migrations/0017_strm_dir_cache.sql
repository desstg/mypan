CREATE TABLE IF NOT EXISTS strm_remote_dir_cache (
    account_id    INTEGER NOT NULL,
    dir_id        TEXT    NOT NULL,
    dir_path      TEXT    NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    PRIMARY KEY (account_id, dir_id)
);

CREATE INDEX IF NOT EXISTS idx_strm_remote_dir_cache_account
    ON strm_remote_dir_cache (account_id);
