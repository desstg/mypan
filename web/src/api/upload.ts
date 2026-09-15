import { ApiError } from "./client";
import type { ApiResp } from "./types";
import type { BatchDeleteUploadResult, UploadTask } from "@/types/upload";

async function parseJSON<T>(resp: Response): Promise<T> {
  const payload = (await resp.json()) as ApiResp<T>;
  if (!payload.success) {
    throw new ApiError(payload.message || "请求失败", payload.error_type || "unknown", resp.status);
  }
  return payload.data as T;
}

export interface UploadRuntimeConfig {
  concurrency: number;
  concurrency_min?: number;
  concurrency_max?: number;
  builtin_temp_dir?: string;
}

export interface BatchControlUploadResult {
  updated_task_ids: string[];
  missing_task_ids: string[];
}

export const uploadApi = {
  getRuntime() {
    return fetch("/api/files/upload/runtime").then((r) => parseJSON<UploadRuntimeConfig>(r));
  },

  updateRuntime(concurrency: number) {
    return fetch("/api/files/upload/runtime", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ concurrency }),
    }).then((r) => parseJSON<UploadRuntimeConfig>(r));
  },

  listTasks(accountId?: number) {
    const qs = accountId != null ? `?account_id=${accountId}` : "";
    return fetch(`/api/files/upload/tasks${qs}`).then((r) => parseJSON<UploadTask[]>(r));
  },

  pauseTask(taskId: string) {
    return fetch(`/api/files/upload/tasks/${taskId}/pause`, { method: "POST" }).then((r) =>
      parseJSON<UploadTask>(r),
    );
  },

  resumeTask(taskId: string) {
    return fetch(`/api/files/upload/tasks/${taskId}/resume`, { method: "POST" }).then((r) =>
      parseJSON<UploadTask>(r),
    );
  },

  batchPause(taskIds: string[]) {
    return fetch("/api/files/upload/tasks/batch-pause", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ task_ids: taskIds }),
    }).then((r) => parseJSON<BatchControlUploadResult>(r));
  },

  deleteTask(taskId: string, deleteUploadedFile = false) {
    const qs = deleteUploadedFile ? "?delete_uploaded_file=true" : "";
    return fetch(`/api/files/upload/tasks/${taskId}${qs}`, { method: "DELETE" }).then((r) =>
      parseJSON<unknown>(r),
    );
  },

  batchDelete(taskIds: string[], deleteUploadedFile = false, deleteBatchRoots = false) {
    return fetch("/api/files/upload/tasks/batch-delete", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        task_ids: taskIds,
        delete_uploaded_file: deleteUploadedFile,
        delete_batch_roots: deleteBatchRoots,
      }),
    }).then((r) => parseJSON<BatchDeleteUploadResult>(r));
  },

  createTask(
    formData: FormData,
    opts?: { signal?: AbortSignal; onProgress?: (loaded: number, total: number) => void },
  ): Promise<UploadTask> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/files/upload-task");
      if (opts?.signal) {
        opts.signal.addEventListener("abort", () => xhr.abort());
      }
      xhr.upload.onprogress = (e) => {
        if (opts?.onProgress && e.lengthComputable) {
          opts.onProgress(e.loaded, e.total);
        }
      };
      xhr.onload = () => {
        try {
          const payload = JSON.parse(xhr.responseText) as ApiResp<UploadTask>;
          if (!payload.success) {
            reject(new ApiError(payload.message || "创建上传任务失败", payload.error_type || "unknown", xhr.status));
            return;
          }
          resolve(payload.data as UploadTask);
        } catch {
          reject(new ApiError("创建上传任务失败", "parse_error", xhr.status));
        }
      };
      xhr.onerror = () => reject(new ApiError("网络请求失败", "network_error", 0));
      xhr.onabort = () => reject(new ApiError("任务已取消", "canceled", 0));
      xhr.send(formData);
    });
  },
};
