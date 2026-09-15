import { ApiError, http } from "./client";
import type { ApiResp } from "./types";
import type {
  OfflineBatchDeleteResult,
  OfflineDownloadCapabilities,
  OfflineDownloadTask,
  OfflineTorrentPreparation,
} from "@/types/offline-download";

export interface AddOfflineURLsPayload {
  account_id: number;
  provider_kind?: "native" | "builtin";
  urls: string[];
  file_name?: string;
  target_parent_id: string;
  target_display_path: string;
}

export interface AddOfflineTorrentPayload {
  account_id: number;
  preparation_id: string;
  wanted: number[];
  target_parent_id: string;
  target_display_path: string;
  save_path?: string;
}

async function parseMultipartResponse<T>(resp: Response): Promise<T> {
  let payload: ApiResp<T> | null = null;
  try {
    payload = (await resp.json()) as ApiResp<T>;
  } catch {
    // 统一转换为可展示错误。
  }
  if (!payload?.success) {
    throw new ApiError(payload?.message || "请求失败", payload?.error_type || "unknown", resp.status);
  }
  return payload.data as T;
}

export const offlineDownloadApi = {
  capabilities(accountId: number) {
    return http.get<OfflineDownloadCapabilities>("/files/offline-download/capabilities", {
      account_id: accountId,
    });
  },

  addURLs(payload: AddOfflineURLsPayload) {
    return http.post<OfflineDownloadTask[]>("/files/offline-download/urls", payload);
  },

  prepareTorrent(accountId: number, file: File) {
    const form = new FormData();
    form.append("account_id", String(accountId));
    form.append("file", file, file.name);
    return fetch("/api/files/offline-download/torrent/prepare", {
      method: "POST",
      credentials: "include",
      body: form,
    }).then((resp) => parseMultipartResponse<OfflineTorrentPreparation>(resp));
  },

  addTorrent(payload: AddOfflineTorrentPayload) {
    return http.post<OfflineDownloadTask>("/files/offline-download/torrent", payload);
  },

  listTasks(refresh = true, accountId?: number) {
    return http.get<OfflineDownloadTask[]>("/files/offline-download/tasks", {
      refresh,
      account_id: accountId,
    });
  },

  refreshTasks(accountId?: number) {
    return http.post<OfflineDownloadTask[]>(
      "/files/offline-download/tasks/refresh",
      undefined,
      { account_id: accountId },
    );
  },

  batchDelete(taskIds: string[]) {
    return http.post<OfflineBatchDeleteResult>("/files/offline-download/tasks/batch-delete", {
      task_ids: taskIds,
    });
  },
};
