import { uploadApi } from "@/api/upload";
import { getApiErrorMessage } from "@/api/client";
import { isLocalUploadTask } from "@/composables/upload/uploadTaskFormatters";
import type { LocalUploadPayload, UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";
import {
  getNextLocalUploadTaskCandidate,
  getNextRemoteResumeTaskCandidate,
  type UploadTaskStream,
} from "@/composables/upload/useUploadTaskStream";
import type { UploadTaskStore } from "@/composables/upload/useUploadTaskStore";
import type { UploadTask } from "@/types/upload";

const localDispatchConcurrency = 2;

export function useLocalUploadDispatcher(
  deps: UploadTaskDeps,
  store: UploadTaskStore,
  stream: UploadTaskStream,
) {
  let uploadTaskSchedulerRunning = false;

  async function createSingleUploadTask(
    selectedFile: File,
    conflictPolicy = "overwrite",
    localTask?: UploadTask,
    options: Partial<LocalUploadPayload> = {},
  ) {
    const task = localTask || store.createLocalUploadTask(selectedFile);
    const targetPath = options.targetPath || task.target_path || deps.currentPath.value;
    const displayName = options.displayName || task.file_name || selectedFile.name;
    const targetDisplayPath =
      options.targetDisplayPath || task.target_display_path || buildUploadTargetDisplayPath();
    if (!localTask) store.addLocalUploadTask(task);
    store.localUploadTaskPayloads.set(task.task_id, {
      file: selectedFile,
      conflictPolicy,
      targetPath,
      displayName,
      targetDisplayPath,
    });

    if (store.canceledLocalUploadTaskIds.has(task.task_id)) {
      store.removeLocalUploadTask(task.task_id);
      store.localUploadTaskPayloads.delete(task.task_id);
      return { success: false, canceled: true };
    }
    if (store.batchPauseInProgress.value || store.pausedLocalUploadTaskIds.has(task.task_id)) {
      store.updateLocalUploadTask(task.task_id, { status: "paused", message: "上传已暂停", error: "" });
      return { success: false, paused: true };
    }

    const formData = new FormData();
    formData.append("account_id", String(deps.selectedAccountId.value));
    formData.append("path", targetPath);
    formData.append("file", selectedFile);
    formData.append("conflict_policy", conflictPolicy);
    formData.append("client_task_id", task.task_id);
    formData.append("display_name", displayName);
    formData.append("target_display_path", targetDisplayPath);
    formData.append("batch_id", String(task.batch_id || ""));
    formData.append("batch_name", String(task.batch_name || ""));
    formData.append("batch_root_id", String(options.batchRootId || ""));
    formData.append("batch_root_parent_id", String(options.batchRootParentId || ""));
    formData.append("batch_root_owned", options.batchRootOwned ? "true" : "false");
    formData.append("rel_path", String(task.rel_path || ""));
    formData.append("rel_dir", String(task.rel_dir || ""));

    const controller = new AbortController();
    store.localUploadTaskControllers.set(task.task_id, controller);
    try {
      const created = await uploadApi.createTask(formData, {
        signal: controller.signal,
        onProgress: (loaded, total) => {
          const progress = total > 0 ? Math.min(99, Math.round((loaded / total) * 100)) : 0;
          const message =
            total > 0 && loaded >= total
              ? "投递成功，创建任务中"
              : `投递到 MyPan 服务器 ${progress}%`;
          store.updateLocalUploadTask(task.task_id, {
            status: "pending",
            progress,
            uploaded_bytes: loaded,
            total_bytes: total > 0 ? total : selectedFile.size,
            message,
          });
        },
      });
      // 文件已经完整投递到服务端，立即释放浏览器中的 File 引用。
      // 万级文件夹上传时若保留这些 payload，会持续占用大量内存。
      store.localUploadTaskPayloads.delete(task.task_id);
      if (store.canceledLocalUploadTaskIds.has(task.task_id)) {
        try {
          await uploadApi.deleteTask(created.task_id);
        } catch {}
        store.removeLocalUploadTask(task.task_id);
        store.localUploadTaskPayloads.delete(task.task_id);
        store.canceledLocalUploadTaskIds.delete(task.task_id);
        store.pausedLocalUploadTaskIds.delete(task.task_id);
        return { success: false, canceled: true };
      }
      if (store.pausedLocalUploadTaskIds.has(task.task_id) || store.batchPauseInProgress.value) {
        store.canceledLocalUploadTaskIds.delete(task.task_id);
        store.pausedLocalUploadTaskIds.delete(task.task_id);
        try {
          await uploadApi.pauseTask(created.task_id);
        } catch {}
        await stream.fetchUploadTasks();
        stream.startUploadTaskPolling();
        return { success: false, paused: true };
      }
      store.canceledLocalUploadTaskIds.delete(task.task_id);
      store.pausedLocalUploadTaskIds.delete(task.task_id);
      store.updateLocalUploadTask(task.task_id, {
        status: "pending",
        progress: 0,
        uploaded_bytes: 0,
        message: "创建任务中",
        error: "",
      });
      await stream.fetchUploadTasks();
      stream.startUploadTaskPolling();
      return { success: true };
    } catch (e) {
      if (store.canceledLocalUploadTaskIds.has(task.task_id)) {
        store.removeLocalUploadTask(task.task_id);
        store.localUploadTaskPayloads.delete(task.task_id);
        store.canceledLocalUploadTaskIds.delete(task.task_id);
        store.pausedLocalUploadTaskIds.delete(task.task_id);
        return { success: false, canceled: true };
      }
      if (controller.signal.aborted && (store.pausedLocalUploadTaskIds.has(task.task_id) || store.batchPauseInProgress.value)) {
        store.updateLocalUploadTask(task.task_id, { status: "paused", message: "上传已暂停", error: "" });
        return { success: false, paused: true };
      }
      if (controller.signal.aborted) {
        store.updateLocalUploadTask(task.task_id, { status: "pending", message: "等待上传", error: "" });
        return { success: false };
      }
      const msg = getApiErrorMessage(e, "创建上传任务失败");
      store.updateLocalUploadTask(task.task_id, { status: "failed", message: "创建上传任务失败", error: msg });
      return { success: false };
    } finally {
      store.localUploadTaskControllers.delete(task.task_id);
      void startUploadTaskScheduler();
    }
  }

  async function activateQueuedUploadTask(task: UploadTask) {
    if (isLocalUploadTask(task)) {
      const payload = store.localUploadTaskPayloads.get(task.task_id);
      if (!payload?.file) return false;
      store.localDispatchingTaskIds.add(task.task_id);
      void createSingleUploadTask(payload.file, payload.conflictPolicy, task, payload).finally(() => {
        store.localDispatchingTaskIds.delete(task.task_id);
        void startUploadTaskScheduler();
      });
      return true;
    }
    store.pendingRemoteResumeTaskIds.delete(String(task.task_id));
    void uploadApi
      .resumeTask(task.task_id)
      .then(() => stream.fetchUploadTasks())
      .catch((e) => {
        store.pendingRemoteResumeTaskIds.add(String(task.task_id));
        store.patchRemoteUploadTask(task.task_id, {
          message: getApiErrorMessage(e, "继续上传任务失败"),
        });
      })
      .finally(() => void startUploadTaskScheduler());
    return true;
  }

  async function startUploadTaskScheduler() {
    if (uploadTaskSchedulerRunning) return;
    uploadTaskSchedulerRunning = true;
    try {
      await stream.refreshUploadTaskServerConcurrency();
      while (true) {
        let advanced = false;

        if (store.localDispatchingTaskIds.size < localDispatchConcurrency) {
          const nextLocal = getNextLocalUploadTaskCandidate(store);
          if (nextLocal) {
            const ok = await activateQueuedUploadTask(nextLocal);
            if (ok) {
              advanced = true;
              continue;
            }
          }
        }

        const nextRemote = getNextRemoteResumeTaskCandidate(store);
        if (nextRemote) {
          const ok = await activateQueuedUploadTask(nextRemote);
          if (ok) {
            advanced = true;
            continue;
          }
        }

        if (!advanced) break;
      }
    } finally {
      uploadTaskSchedulerRunning = false;
    }
  }

  function buildUploadTargetDisplayPath(relativeDirectory = "") {
    const parts = [...deps.getCurrentBreadcrumbNameParts(), ...relativeDirectory.split("/").filter(Boolean)];
    return parts.join("/");
  }

  return {
    createSingleUploadTask,
    activateQueuedUploadTask,
    startUploadTaskScheduler,
    buildUploadTargetDisplayPath,
  };
}

export type LocalUploadDispatcher = ReturnType<typeof useLocalUploadDispatcher>;
