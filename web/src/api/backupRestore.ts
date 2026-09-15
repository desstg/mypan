import { ApiError, http } from "./client";
import type { ApiResp } from "./types";

export type BackupScope = "settings" | "full";
export type RestoreState = "idle" | "waiting_restart" | "restore_success" | "restore_rollback";

export interface BackupRecord {
  id: string;
  backup_id: string;
  app_version: string;
  schema_version: number;
  created_at: string;
  note: string;
  scope: BackupScope;
  components: string[];
  account_count: number;
  task_count: number;
  size: number;
}

export interface BackupSummary {
  record: BackupRecord;
  account_count: number;
  task_count: number;
  restore_admin: boolean;
  needs_restart: boolean;
  secret_from_env: boolean;
}

export interface BackupRestoreStatus {
  state: RestoreState;
  message?: string;
  backup_id?: string;
  backup_note?: string;
  scope?: BackupScope;
  restore_admin?: boolean;
  updated_at?: string;
}

export interface CreateBackupPayload {
  note: string;
  password: string;
  include_accounts: boolean;
}

export const backupRestoreApi = {
  list: () => http.get<BackupRecord[]>("/admin/backups/"),
  create: (payload: CreateBackupPayload) => http.post<BackupRecord>("/admin/backups/", payload),
  remove: (id: string) => http.del<void>(`/admin/backups/${encodeURIComponent(id)}`),
  prepareRestore: (id: string, password: string, restoreAdmin: boolean) =>
    http.post<BackupSummary>(`/admin/backups/${encodeURIComponent(id)}/restore`, {
      password,
      restore_admin: restoreAdmin,
    }),
  status: () => http.get<BackupRestoreStatus>("/admin/backups/status"),
  cancelPending: () => http.del<void>("/admin/backups/pending"),
  acknowledgeStatus: () => http.post<void>("/admin/backups/status/ack"),
  restart: () => http.post<void>("/admin/backups/restart"),
  downloadURL: (id: string) => `/api/admin/backups/${encodeURIComponent(id)}/download`,
  async import(file: File, password: string): Promise<BackupSummary> {
    const form = new FormData();
    form.append("file", file, file.name);
    form.append("password", password);
    let response: Response;
    try {
      response = await fetch("/api/admin/backups/import", {
        method: "POST",
        credentials: "include",
        body: form,
      });
    } catch {
      throw new ApiError("网络请求失败，请检查后端是否在运行", "network_error", 0);
    }
    let payload: ApiResp<BackupSummary> | null = null;
    try {
      payload = (await response.json()) as ApiResp<BackupSummary>;
    } catch {
      throw new ApiError(`备份导入失败 (${response.status})`, "http_error", response.status);
    }
    if (!payload.success || !payload.data) {
      const errorType = payload.error_type || "unknown";
      if (response.status === 401 && errorType === "ADMIN_AUTH_REQUIRED") {
        const redirect = encodeURIComponent(window.location.pathname + window.location.search);
        window.location.assign(`/login?redirect=${redirect}`);
      }
      throw new ApiError(payload.message || "备份导入失败", errorType, response.status);
    }
    return payload.data;
  },
};
