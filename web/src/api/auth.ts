import { http } from "./client";

export interface AuthStatus {
  is_admin: boolean;
  username?: string;
  public_index_enabled: boolean;
  must_change_password?: boolean;
  password_change_reason?: string;
}

export interface LoginResult {
  username: string;
  is_admin: boolean;
  must_change_password?: boolean;
  password_change_reason?: string;
}

export interface SystemConfig {
  admin_username: string;
  session_timeout: number;
  public_index_enabled: boolean;
  index_account_switch_mode?: string;
  compact_home_enabled?: boolean;
  admin_home_return_mode?: "sidebar" | "top_icon";
  header_effects_enabled?: boolean;
  index_strm_auto_detect_enabled?: boolean;
  must_change_password: boolean;
  password_change_reason?: string;
  oauth_server_url?: string;
  upload_task_concurrency?: number;
  log_retention_days?: number;
  auth_active_refresh_enabled?: boolean;
  webdav_enabled?: boolean;
}

export interface UpdateCredentialsRequest {
  admin_username: string;
  admin_password?: string;
  session_timeout?: number;
  public_index_enabled?: boolean;
  index_account_switch_mode?: "dropdown" | "floating";
  compact_home_enabled?: boolean;
  admin_home_return_mode?: "sidebar" | "top_icon";
  header_effects_enabled?: boolean;
  index_strm_auto_detect_enabled?: boolean;
  oauth_server_url?: string;
  upload_task_concurrency?: number;
  log_retention_days?: number;
  auth_active_refresh_enabled?: boolean;
}

export interface WebDAVConfigRequest {
  webdav_enabled?: boolean;
}

export interface ResetPasswordResult {
  reused?: boolean;
  expires_at?: number;
  remaining_seconds?: number;
  ttl_seconds?: number;
}

export async function fetchAuthStatus(): Promise<AuthStatus> {
  return http.get<AuthStatus>("/auth/status");
}

export async function login(input: {
  username: string;
  password: string;
  remember: boolean;
}): Promise<LoginResult> {
  const body = new URLSearchParams();
  body.set("username", input.username);
  body.set("password", input.password);
  body.set("remember", input.remember ? "1" : "");
  return http.form<LoginResult>("/auth/login", body);
}

export async function logout(): Promise<void> {
  await http.post<Record<string, never>>("/auth/logout");
}

export async function resetPassword(): Promise<ResetPasswordResult> {
  return http.post<ResetPasswordResult>("/auth/reset-password");
}

export async function fetchSystemConfig(): Promise<SystemConfig> {
  return http.get<SystemConfig>("/admin/system-config");
}

export async function updateCredentials(payload: UpdateCredentialsRequest): Promise<void> {
  await http.post<Record<string, never>>("/admin/update-credentials", payload);
}

export async function updateWebDAVConfig(payload: WebDAVConfigRequest): Promise<void> {
  await http.post<Record<string, never>>("/admin/webdav-config", payload);
}
