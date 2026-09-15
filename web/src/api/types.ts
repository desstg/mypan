// 与后端 internal/api 的响应契约一一对应。

export interface ApiResp<T> {
  success: boolean;
  data?: T;
  message: string;
  error_type?: string;
  details?: unknown;
  timestamp: string;
}

export interface FieldOption {
  value: string;
  label: string;
}

// 驱动配置字段，后端反射 Addition 的 tag 生成，用于动态渲染表单。
export interface FieldSchema {
  name: string;
  label: string;
  type: "string" | "select" | "number" | "bool" | "password" | "local_dir";
  required: boolean;
  default?: string;
  options?: FieldOption[];
  full_width?: boolean;
  pair_key?: string;
  default_by?: string;
  defaults?: Record<string, string>;
}

export interface DriverInfo {
  name: string;
  display_name: string;
  description?: string;
  card_tags?: string[];
  sort_order?: number;
  auth_label?: string;
  card_color: string;
  card_logo?: string;
  auth_type: string;
  supports_oauth?: boolean;
  supports_qr_login?: boolean;
  qr_devices?: FieldOption[];
  qr_device_field?: string;
  internal_experimental?: boolean;
  fields: FieldSchema[];
}

export interface Account {
  id: number;
  name: string;
  driver_type: string;
  driver_card_name?: string;
  driver_card_color?: string;
  driver_card_logo?: string;
  auth_status?: "active" | "cooldown" | "token_expired" | "failed" | string;
  auth_last_error?: string;
  config: string; // JSON 字符串
  is_active: boolean;
  is_default: boolean;
  sort_order: number;
  created_at?: string;
  updated_at?: string;
  profile_nickname?: string;
  profile_vip?: string;
  profile_used_bytes?: number;
  profile_total_bytes?: number;
}

// 新建/更新账号时的载荷（config 为 JSON 字符串）。
export interface AccountPayload {
  name: string;
  driver_type: string;
  config: string;
  is_active: boolean;
  is_default: boolean;
  sort_order: number;
}

export interface FileItem {
  id: string;
  name: string;
  size: number;
  is_dir: boolean;
  mod_time?: string;
}

export interface FileListResult {
  parent_id: string;
  items: FileItem[];
}

export interface FileDeletePayload {
  account_id: number;
  file_ids: string[];
  parent_id?: string;
}

export interface FileTransferPayload {
  account_id: number;
  file_ids: string[];
  target_parent_id: string;
  source_parent_id?: string;
}

export interface FileRenamePayload {
  account_id: number;
  file_id: string;
  new_name: string;
  parent_id?: string;
}

export interface FileCreateFolderPayload {
  account_id: number;
  parent_id: string;
  name: string;
}

export interface FileNameAlignPreviewPayload {
  account_id: number;
  parent_id: string;
  target_file_id: string;
  sample_file_id?: string;
}

export interface FileNameAlignApplyPayload {
  account_id: number;
  parent_id: string;
  target_file_id: string;
  sample_file_id?: string;
  selected_file_ids: string[];
}

export interface FileNameAlignSample {
  file_id: string;
  file_name: string;
  pattern_label: string;
}

export interface FileNameAlignPreviewItem {
  file_id: string;
  file_name: string;
  new_name: string;
  episode: number;
  season?: number;
  pattern_hint: string;
}

export interface FileNameAlignPreviewResult {
  target: FileNameAlignPreviewItem;
  sample: FileNameAlignSample;
  sample_candidates: FileNameAlignSample[];
  suspects: FileNameAlignPreviewItem[];
}

export interface FileNameAlignApplyResult {
  renamed: Array<{
    file_id: string;
    old_name: string;
    new_name: string;
  }>;
}

export interface BrowserFavoriteCrumb {
  id: string;
  name: string;
}

export interface BrowserFavoriteItem {
  id: string;
  name: string;
  crumbs: BrowserFavoriteCrumb[];
}

export interface BrowserFavoritesState {
  open: boolean;
  items: BrowserFavoriteItem[];
}

export interface BrowserFavoritesPayload extends BrowserFavoritesState {
  account_id: number;
}
