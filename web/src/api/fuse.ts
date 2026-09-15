import { http } from "./client";

export interface FuseMount {
  id?: number;
  name: string;
  account_id: number;
  account_name?: string;
  root_item_id: string;
  root_path: string;
  mount_point: string;
  read_only: boolean;
  auto_mount: boolean;
  uid: number;
  gid: number;
  dir_mode: string;
  file_mode: string;
  enabled: boolean;
  state: string;
  last_error?: string;
  sort_order: number;
}

export interface FuseStatus {
  enabled: boolean;
  compile_support: boolean;
  mount_root: string;
  entry_timeout_s: number;
  attr_timeout_s: number;
  read_cache?: FuseReadCacheConfig;
}

export interface FuseReadCacheConfig {
  enabled: boolean;
  max_gb: number;
  retention_days: number;
  eviction_policy: "lru" | "large_file";
  used_bytes: number;
  limit_bytes: number;
  block_count: number;
  root_path: string;
}

export function fetchFuseReadCache() {
  return http.get<FuseReadCacheConfig>("/admin/fuse/read-cache");
}

export function updateFuseReadCache(body: Partial<FuseReadCacheConfig>) {
  return http.put<FuseReadCacheConfig>("/admin/fuse/read-cache", body);
}

export function clearFuseReadCache() {
  return http.post<{ cleared: boolean }>("/admin/fuse/read-cache/clear");
}

export function fetchFuseStatus() {
  return http.get<FuseStatus>("/admin/fuse/status");
}

export function updateFuseConfig(body: { enabled: boolean; mount_root?: string }) {
  return http.put<FuseStatus>("/admin/fuse/config", body);
}

export function fetchFuseMounts() {
  return http.get<FuseMount[]>("/admin/fuse/mounts");
}

export function createFuseMount(body: Partial<FuseMount>) {
  return http.post<FuseMount>("/admin/fuse/mounts", body);
}

export function updateFuseMount(id: number, body: Partial<FuseMount>) {
  return http.put<FuseMount>(`/admin/fuse/mounts/${id}`, body);
}

export function deleteFuseMount(id: number) {
  return http.del<{ deleted: boolean }>(`/admin/fuse/mounts/${id}`);
}

export function mountFuse(id: number) {
  return http.post<FuseMount>(`/admin/fuse/mounts/${id}/mount`);
}

export function unmountFuse(id: number) {
  return http.post<FuseMount>(`/admin/fuse/mounts/${id}/unmount`);
}
