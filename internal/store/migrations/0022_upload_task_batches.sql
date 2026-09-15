ALTER TABLE upload_tasks ADD COLUMN batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE upload_tasks ADD COLUMN batch_name TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_upload_tasks_batch_id ON upload_tasks(batch_id);
