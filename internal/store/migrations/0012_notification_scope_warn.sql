ALTER TABLE notifications ADD COLUMN ref_id INTEGER NOT NULL DEFAULT 0;

ALTER TABLE cache_retention_tasks ADD COLUMN ignore_large_scope_warn INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_notifications_ref ON notifications(category, ref_id);
