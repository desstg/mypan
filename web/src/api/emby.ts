import { http } from "./client";

export interface EmbyConfig {
  id: string;
  name: string;
  emby_url: string;
  api_key: string;
  proxy_port: string;
  direct_strm_clients: string;
  proxy_url: string;
  running: boolean;
  last_error?: string;
}

export interface EmbyConfigUpdate {
  id?: string;
  name: string;
  emby_url: string;
  api_key: string;
  proxy_port: string;
  direct_strm_clients: string;
}

export interface EmbyConfigState {
  enabled: boolean;
  items: EmbyConfig[];
}

export interface EmbyLibrary {
  id: string;
  name: string;
  collection_type?: string;
}

export interface EmbyRefreshRequest {
  config_id?: string;
  mode?: "global" | "library";
  library_id?: string;
}

export function fetchEmbyConfigs() {
  return http.get<EmbyConfigState>("/admin/emby/configs");
}

export function saveEmbyConfigs(enabled: boolean, items: EmbyConfigUpdate[]) {
  return http.put<EmbyConfigState>("/admin/emby/configs", { enabled, items });
}

export function testEmbyConfig(values: EmbyConfigUpdate) {
  return http.post<{ ok: boolean }>("/admin/emby/test", values);
}

export function fetchEmbyLibraries(configId = "") {
  const query = configId ? `?config_id=${encodeURIComponent(configId)}` : "";
  return http.get<EmbyLibrary[]>(`/admin/emby/libraries${query}`);
}

export function refreshEmbyLibrary(body: EmbyRefreshRequest = {}) {
  return http.post<{ mode: string; task_id?: string; library_id?: string; library_name?: string }>("/admin/emby/refresh", body);
}
