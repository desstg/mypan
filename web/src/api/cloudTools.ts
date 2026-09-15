import { http } from "./client";

export interface CloudTool115Status {
  enabled: boolean;
  cache_count: number;
  available: boolean;
}

export interface CloudTool115Accounts {
  accounts: { id: number; name: string; is_active: boolean }[];
}

export interface LocalUploadMapping {
  name: string;
  path: string;
}

export interface LocalUploadConfig {
  enabled: boolean;
  mappings: LocalUploadMapping[];
}

export interface LocalUploadEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mtime: number;
  rel_path: string;
}

export interface LocalUploadBrowseResult {
  mapping: string;
  path: string;
  items: LocalUploadEntry[];
}

export interface LocalUploadCreatePayload {
  account_id: number;
  mapping: string;
  target_path: string;
  target_display_path?: string;
  conflict_policy: string;
  client_task_id: string;
  display_name?: string;
  items: { rel_path: string; is_dir: boolean }[];
}

export interface AIOrganizeInstance {
  id: string;
  name: string;
  base_url: string;
  api_key: string;
  model: string;
  default: boolean;
}

export interface AIOrganizeConfig {
  enabled: boolean;
  items: AIOrganizeInstance[];
}

export interface AIOrganizeInstanceUpdate {
  id?: string;
  name: string;
  base_url: string;
  api_key: string;
  model: string;
  default?: boolean;
}

export type ClassificationTemplateKind = "media" | "region" | "genre" | "custom";

export interface ClassificationRule {
  name: string;
  condition: string;
  fallback_mode?: "self" | "directory";
  fallback_dir?: string;
  children?: ClassificationRule[];
}

export interface ClassificationTemplate {
  kind: ClassificationTemplateKind;
  rules: ClassificationRule[];
}

export interface ClassificationConfig {
  version: number;
  enabled: boolean;
  selected_template: ClassificationTemplateKind;
  templates: ClassificationTemplate[];
}

export interface ClassificationTMDBGenre {
  id?: number;
  name?: string;
}

export interface ClassificationTMDBDetail extends Record<string, unknown> {
  id?: number;
  media_type?: "movie" | "tv";
  title?: string;
  name?: string;
  original_title?: string;
  original_name?: string;
  origin_country?: string[];
  original_language?: string;
  genres?: ClassificationTMDBGenre[];
}

export interface QuarkTVBinding {
  account_id: number;
  account_name: string;
  tv_nickname: string;
  preferred_resolution: string;
  allow_dolby: boolean;
  membership: string;
}

export interface QuarkTVStatus {
  enabled: boolean;
  available: boolean;
  play_mode: "split" | "adaptive" | "direct";
  client_list_mode: "direct_list" | "proxy_list";
  proxy_clients: string;
  bindings: QuarkTVBinding[];
}

export interface QuarkTVAccount {
  id: number;
  name: string;
}

export interface QuarkTVBindStart {
  token: string;
  qr_image: string;
  expires_in: number;
}

export interface QuarkTVBindPoll {
  status: "waiting" | "success" | "failed" | "expired";
  message: string;
}

export interface QuarkTVBindingSettingsPayload {
  account_id: number;
  preferred_resolution: string;
  allow_dolby: boolean;
  play_mode: "split" | "adaptive" | "direct";
  client_list_mode: "direct_list" | "proxy_list";
  proxy_clients: string;
}

export interface QuarkTVBindingSettingsResult {
  binding: QuarkTVBinding;
  play_mode: "split" | "adaptive" | "direct";
  client_list_mode: "direct_list" | "proxy_list";
  proxy_clients: string;
}

export const cloudToolsApi = {
  status115: () => http.get<CloudTool115Status>("/admin/tools/115-strm/status"),
  set115Enabled: (enabled: boolean) =>
    http.post<{ enabled: boolean }>("/admin/tools/115-strm/enabled", { enabled }),
  clear115Cache: (accountId = 0) =>
    http.post<{ removed: number }>("/admin/tools/115-strm/cache/clear", { account_id: accountId }),
};

export const localUploadApi = {
  getConfig: () => http.get<LocalUploadConfig>("/admin/tools/local-upload/config"),
  saveConfig: (payload: LocalUploadConfig) =>
    http.put<LocalUploadConfig>("/admin/tools/local-upload/config", payload),
  browse: (mapping: string, path = "") =>
    http.post<LocalUploadBrowseResult>("/admin/tools/local-upload/browse", { mapping, path }),
  upload: (payload: LocalUploadCreatePayload) =>
    http.post<{ accepted: boolean; count: number }>("/admin/tools/local-upload/upload", payload),
};

export const aiOrganizeApi = {
  getConfig: () => http.get<AIOrganizeConfig>("/admin/tools/ai-organize/config"),
  saveConfig: (payload: { enabled: boolean; items: AIOrganizeInstanceUpdate[] }) =>
    http.put<AIOrganizeConfig>("/admin/tools/ai-organize/config", payload),
  testConfig: (payload: AIOrganizeInstanceUpdate) =>
    http.post<{ ok: boolean }>("/admin/tools/ai-organize/test", payload),
};

export const classificationApi = {
  getConfig: () => http.get<ClassificationConfig>("/admin/tools/classification/config"),
  saveConfig: (payload: ClassificationConfig) =>
    http.put<ClassificationConfig>("/admin/tools/classification/config", payload),
  lookupTMDBDetail: (payload: { tmdb_id: string; media_type: "movie" | "tv" }) =>
    http.post<ClassificationTMDBDetail>("/admin/tools/classification/tmdb-detail", payload),
};

export const quarkTVApi = {
  status: () => http.get<QuarkTVStatus>("/admin/tools/quarktv/status"),
  setEnabled: (enabled: boolean) =>
    http.post<{ enabled: boolean }>("/admin/tools/quarktv/enabled", { enabled }),
  accounts: () => http.get<{ accounts: QuarkTVAccount[] }>("/admin/tools/quarktv/accounts"),
  bindStart: (accountId: number) =>
    http.post<QuarkTVBindStart>("/admin/tools/quarktv/bind/start", { account_id: accountId }),
  bindPoll: (token: string) =>
    http.post<QuarkTVBindPoll>("/admin/tools/quarktv/bind/poll", { token }),
  updateBindingSettings: (payload: QuarkTVBindingSettingsPayload) =>
    http.put<QuarkTVBindingSettingsResult>("/admin/tools/quarktv/binding/settings", payload),
  unbind: (accountId: number) =>
    http.del<{ removed: boolean }>("/admin/tools/quarktv/bind", { account_id: accountId }),
};
