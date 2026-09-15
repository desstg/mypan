import { http } from "./client";

export interface CacheRetentionTask {
  id?: number;
  account_id: number;
  account_name?: string;
  parent_id: string;
  path: string;
  scan_depth: number;
  api_interval: number;
  refresh_interval: number;
  status: string;
  paused_reason?: string;
  file_count: number;
  last_refresh?: string;
  last_refresh_status?: string;
  last_duration_ms: number;
  last_api_calls: number;
  last_skip_calls: number;
  last_scanned_dirs: number;
  error_message?: string;
  time_window_enabled: boolean;
  time_start: string;
  time_end: string;
  scanned_dirs?: number;
  scanned_files?: number;
  current_dir?: string;
  started_at?: string;
  current_duration_ms?: number;
  is_running?: boolean;
  is_pending?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface CacheRetentionListPayload {
  items: CacheRetentionTask[];
  startup_remaining: number;
}

export interface CacheRetentionStats {
  total: number;
  running: number;
  paused: number;
  executing_task_ids: number[];
  pending_task_ids?: number[];
  startup_remaining: number;
}

export interface CacheRetentionDefaults {
  api_interval: number;
  refresh_interval: number;
  scan_depth: number;
  max_configs: number;
  startup_remaining: number;
}

export interface RetentionRunResult {
  state: string;
  startup_remaining: number;
  retry_after_seconds?: number;
  cache_ttl_minutes?: number;
}

export type CacheRetentionTaskInput = Pick<
  CacheRetentionTask,
  | "account_id"
  | "parent_id"
  | "path"
  | "scan_depth"
  | "api_interval"
  | "refresh_interval"
  | "time_window_enabled"
  | "time_start"
  | "time_end"
>;

export const SCAN_DEPTH_OPTIONS = [
  { value: 1, label: "单层" },
  { value: 2, label: "2 层" },
  { value: 3, label: "3 层" },
  { value: 4, label: "4 层" },
  { value: 5, label: "5 层" },
] as const;

export function fetchCacheRetentionTasks() {
  return http.get<CacheRetentionListPayload>("/admin/cache-retention/configs");
}

export function fetchCacheRetentionStats() {
  return http.get<CacheRetentionStats>("/admin/cache-retention/stats");
}

export function fetchCacheRetentionDefaults() {
  return http.get<CacheRetentionDefaults>("/admin/cache-retention/defaults");
}

export function createCacheRetentionTask(body: CacheRetentionTaskInput) {
  return http.post<{ id: number; run?: RetentionRunResult }>("/admin/cache-retention/configs", body);
}

export function updateCacheRetentionTask(id: number, body: CacheRetentionTaskInput) {
  return http.put<CacheRetentionTask>(`/admin/cache-retention/configs/${id}`, body);
}

export function deleteCacheRetentionTask(id: number) {
  return http.del<null>(`/admin/cache-retention/configs/${id}`);
}

export function toggleCacheRetentionTask(id: number) {
  return http.post<CacheRetentionTask>(`/admin/cache-retention/configs/${id}/toggle`, {});
}

export function refreshCacheRetentionTask(id: number) {
  return http.post<RetentionRunResult>(`/admin/cache-retention/configs/${id}/refresh`, {});
}

export function forceStopCacheRetentionTask(id: number) {
  return http.post<null>(`/admin/cache-retention/configs/${id}/force-stop`, {});
}

export function ackRetentionScopeWarn(id: number) {
  return http.post<null>(`/admin/cache-retention/configs/${id}/ack-scope-warn`, {});
}
