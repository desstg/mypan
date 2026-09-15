ALTER TABLE offline_download_tasks ADD COLUMN provider_kind TEXT NOT NULL DEFAULT 'native';
ALTER TABLE offline_download_tasks ADD COLUMN executor_type TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_download_tasks ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_download_tasks ADD COLUMN downloaded_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE offline_download_tasks ADD COLUMN speed_bytes REAL NOT NULL DEFAULT 0;
ALTER TABLE offline_download_tasks ADD COLUMN local_temp_path TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_download_tasks ADD COLUMN magnet_diagnostics_json TEXT NOT NULL DEFAULT '';

ALTER TABLE upload_tasks ADD COLUMN cleanup_local_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE upload_tasks ADD COLUMN cleanup_local_path TEXT NOT NULL DEFAULT '';
