export interface OfflineDownloadCapabilities {
  supported: boolean;
  supports_urls: boolean;
  supports_batch_urls: boolean;
  supports_torrent: boolean;
  url_schemes: string[];
  root_target_allowed: boolean;
  remote_delete: boolean;
  builtin_enabled: boolean;
  builtin_supports_urls: boolean;
  builtin_url_schemes?: string[];
  builtin_supports_torrent: boolean;
}

export type OfflineDownloadStatus = "pending" | "running" | "retrying" | "success" | "failed";

export interface OfflineMagnetDiagnostics {
  stage?: string;
  tracker_count?: number;
  dht_nodes?: number;
  dht_good_nodes?: number;
  dht_outstanding_queries?: number;
  active_peers?: number;
  pending_peers?: number;
  total_peers?: number;
  connected_seeders?: number;
  half_open_peers?: number;
  metadata_ready?: boolean;
  last_sample_at?: number;
}

export interface OfflineDownloadTask {
  task_id: string;
  account_id: number;
  account_name: string;
  driver_type: string;
  provider_kind?: "native" | "builtin";
  executor_type?: string;
  source_kind: "url" | "bt";
  source: string;
  name: string;
  provider_task_id?: string;
  info_hash?: string;
  target_parent_id: string;
  target_display_path: string;
  status: OfflineDownloadStatus;
  phase?: "downloading" | "verifying" | "handoff" | "done";
  progress: number;
  size: number;
  downloaded_bytes?: number;
  speed_bytes?: number;
  local_temp_path?: string;
  magnet_diagnostics?: OfflineMagnetDiagnostics;
  file_id?: string;
  message: string;
  error?: string;
  remote_delete: boolean;
  created_at: number;
  updated_at: number;
}

export interface OfflineTorrentFile {
  index: number;
  path: string;
  size: number;
  wanted: boolean;
}

export interface OfflineTorrentPreparation {
  preparation_id: string;
  torrent_name: string;
  total_size: number;
  files: OfflineTorrentFile[];
  expires_at: number;
}

export interface OfflineBatchDeleteResult {
  deleted_task_ids: string[];
  failed_task_ids: string[];
  failed_messages: Record<string, string>;
}
