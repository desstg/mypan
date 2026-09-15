import { http } from "./client";

export interface StrmKeyInfo {
  name: string;
  key: string;
  key_preview: string;
  format: "new";
  status: string;
  system: boolean;
  deletable: boolean;
  disableable: boolean;
}

export interface ApiKeyRecord {
  id?: number;
  name: string;
  key_type: string;
  status: string;
  key_prefix: string;
  key_suffix: string;
  key_preview: string;
  expires_at?: string;
  last_used_at?: string;
  note?: string;
  created_at?: string;
  updated_at?: string;
  key?: string;
}

export interface ApiKeyListResult {
  strm_key: StrmKeyInfo;
  keys: ApiKeyRecord[];
  max_keys: number;
  key_count: number;
}

export interface ApiKeyInput {
  name: string;
  key_type: string;
  expires_days?: number | null;
  status: string;
  note?: string;
}

export interface RotateStrmKeyResult {
  strm_key: StrmKeyInfo;
  replace_result?: { total: number; matched: number; updated: number };
}

export function fetchApiKeys() {
  return http.get<ApiKeyListResult>("/admin/api-keys");
}

export function createApiKey(body: ApiKeyInput) {
  return http.post<ApiKeyRecord>("/admin/api-keys", body);
}

export function updateApiKey(id: number, body: Partial<ApiKeyInput>) {
  return http.put<ApiKeyRecord>(`/admin/api-keys/${id}`, body);
}

export function toggleApiKey(id: number) {
  return http.post<ApiKeyRecord>(`/admin/api-keys/${id}/toggle`, {});
}

export function deleteApiKey(id: number) {
  return http.del<{ id: number }>(`/admin/api-keys/${id}`);
}

export function rotateStrmKey(applyToExistingStrm = true) {
  return http.post<RotateStrmKeyResult>("/admin/api-keys/strm/rotate", {
    apply_to_existing_strm: applyToExistingStrm,
  });
}
