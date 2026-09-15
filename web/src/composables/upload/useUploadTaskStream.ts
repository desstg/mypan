import { ApiError } from "@/api/client";
import { uploadApi } from "@/api/upload";
import { getUploadTaskStableKey, isLocalUploadTask } from "@/composables/upload/uploadTaskFormatters";
import type { UploadRuntimeHooks, UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";
import type { UploadTaskStore } from "@/composables/upload/useUploadTaskStore";
import type { UploadTask } from "@/types/upload";

export function useUploadTaskStream(deps: UploadTaskDeps, store: UploadTaskStore, hooks: UploadRuntimeHooks) {
  let uploadTaskPollingTimer: ReturnType<typeof setInterval> | null = null;
  let uploadTaskEventSource: EventSource | null = null;
  let uploadTaskSseReconnectTimer: ReturnType<typeof setTimeout> | null = null;
  const refreshedSuccessfulTaskKeys = new Set<string>();
  let keepPollingUntil = 0;
  let uploadAuthDenied = false;

  function isAdminAuthError(error: unknown) {
    return error instanceof ApiError && error.status === 401 && error.errorType === "ADMIN_AUTH_REQUIRED";
  }

  function refreshCurrentDirectoryForNewSuccess(tasks: UploadTask[], fullSnapshot = true) {
    const currentSuccessKeys = new Set<string>();
    let hasNewSuccess = false;
    for (const task of tasks) {
      if (!store.uploadAffectsCurrentDirectory(task, deps.currentPath.value)) continue;
      const key = getUploadTaskStableKey(task);
      if (!key) continue;
      currentSuccessKeys.add(key);
      if (!refreshedSuccessfulTaskKeys.has(key)) {
        refreshedSuccessfulTaskKeys.add(key);
        hasNewSuccess = true;
      }
    }
    if (fullSnapshot) {
      for (const key of refreshedSuccessfulTaskKeys) {
        if (!currentSuccessKeys.has(key)) refreshedSuccessfulTaskKeys.delete(key);
      }
    }
    // 文件夹上传：等该批次所有任务结束（成功/跳过/失败）后再刷新一次当前目录，
    // 避免在任务创建/上传中途过早消费刷新标记。
    const batches = store.pendingDirRefreshBatches?.value || {};
    for (const [key, info] of Object.entries(batches)) {
      const members = store.uploadTasks.value.filter((t) =>
        String(t.batch_id || "") === key || String(t.client_task_id || "").startsWith(key + "-"),
      );
      if (members.length === 0) continue;
      // 任务出现即说明远端目录已创建，先刷新一次让用户看到文件夹
      if (!info.creationRefreshed) {
        store.markDirRefreshBatchCreated?.(key);
        hasNewSuccess = true;
      }
      const finished = members.filter((t) =>
        ["success", "skipped", "failed", "canceled"].includes(String(t.status)),
      ).length;
      if (finished >= Math.min(info.count, members.length)) {
        store.resolveDirRefreshBatch?.(key);
        hasNewSuccess = true;
      }
    }
    if (hasNewSuccess || store.consumeFolderUploadRefreshPending()) {
      void deps.refreshFiles(true);
    }
  }

  async function refreshUploadTaskServerConcurrency() {
    try {
      const data = await uploadApi.getRuntime();
      const limit = Number(data.concurrency || 3);
      store.uploadTaskServerConcurrency.value = Number.isFinite(limit) && limit > 0 ? limit : 3;
    } catch {
      store.uploadTaskServerConcurrency.value = 3;
    }
  }

  async function fetchUploadTasks() {
    try {
      const tasks = await uploadApi.listTasks();
      store.replaceRemoteUploadTasks(tasks);
      uploadAuthDenied = false;
      tasks.forEach(store.ensureUploadTaskDisplayOrder);
      store.pruneLocalUploadTasksByStableKeys(tasks.map((task) => getUploadTaskStableKey(task)));
      refreshCurrentDirectoryForNewSuccess(tasks);
      if (!store.uploadTaskPanelOpen.value) {
        if (hasActiveTransferTasks() || Date.now() < keepPollingUntil) {
          if (typeof EventSource === "undefined") startUploadTaskPolling();
          else connectUploadTaskStream();
        } else {
          disconnectUploadTaskStream();
          stopUploadTaskPolling();
        }
      }
      await hooks.startScheduler();
    } catch (e) {
      if (isAdminAuthError(e)) {
        uploadAuthDenied = true;
        disconnectUploadTaskStream();
        stopUploadTaskPolling();
        return;
      }
      console.error("获取上传任务失败:", e);
    }
  }

  function startUploadTaskPolling() {
    if (uploadTaskPollingTimer || uploadTaskEventSource || uploadAuthDenied) return;
    uploadTaskPollingTimer = setInterval(() => void fetchUploadTasks(), 2000);
  }

  // bumpKeepPolling 在上传受理后保持一段时间的轮询，避免任务尚未创建出来时轮询被停止。
  function bumpKeepPolling(ms = 60000) {
    keepPollingUntil = Date.now() + ms;
    if (typeof EventSource === "undefined") startUploadTaskPolling();
    else connectUploadTaskStream();
  }

  function stopUploadTaskPolling() {
    if (uploadTaskPollingTimer) {
      clearInterval(uploadTaskPollingTimer);
      uploadTaskPollingTimer = null;
    }
  }

  function connectUploadTaskStream() {
    if (uploadTaskEventSource || uploadAuthDenied) return;
    if (typeof EventSource === "undefined") {
      startUploadTaskPolling();
      return;
    }
    const es = new EventSource("/api/files/upload/tasks/stream");
    uploadTaskEventSource = es;
    es.addEventListener("tasks", (ev) => {
      try {
        const payload = JSON.parse(ev.data || "{}") as {
          kind?: "snapshot" | "delta";
          tasks?: UploadTask[];
          deleted_task_ids?: string[];
        };
        const tasks = payload.tasks || [];
        if (payload.kind === "delta") {
          store.upsertRemoteUploadTasks(tasks);
          store.removeRemoteUploadTasks(payload.deleted_task_ids || []);
          store.pruneLocalUploadTasksByStableKeys(tasks.map((task) => getUploadTaskStableKey(task)));
          refreshCurrentDirectoryForNewSuccess(tasks, false);
        } else {
          store.replaceRemoteUploadTasks(tasks);
          tasks.forEach(store.ensureUploadTaskDisplayOrder);
          store.pruneLocalUploadTasksByStableKeys(tasks.map((task) => getUploadTaskStableKey(task)));
          refreshCurrentDirectoryForNewSuccess(tasks);
        }
        if (!store.uploadTaskPanelOpen.value && !hasActiveTransferTasks() && Date.now() >= keepPollingUntil) {
          disconnectUploadTaskStream();
        }
      } catch (e) {
        console.error(e);
      }
    });
    es.onopen = () => stopUploadTaskPolling();
    es.onerror = () => {
      disconnectUploadTaskStream();
      if (uploadAuthDenied) return;
      startUploadTaskPolling();
      if (!uploadTaskSseReconnectTimer) {
        uploadTaskSseReconnectTimer = setTimeout(() => {
          uploadTaskSseReconnectTimer = null;
          if (uploadAuthDenied) return;
          connectUploadTaskStream();
        }, 3000);
      }
    };
  }

  function disconnectUploadTaskStream() {
    if (uploadTaskSseReconnectTimer) {
      clearTimeout(uploadTaskSseReconnectTimer);
      uploadTaskSseReconnectTimer = null;
    }
    uploadTaskEventSource?.close();
    uploadTaskEventSource = null;
  }

  function hasActiveTransferTasks() {
    return store.activeUploadTasks.value.length > 0 || store.activeRelayCount.value > 0;
  }

  function cleanupUploadTasks() {
    store.localUploadTaskControllers.forEach((c) => c.abort());
    store.localUploadTaskControllers.clear();
    store.localUploadTaskPayloads.clear();
    disconnectUploadTaskStream();
    stopUploadTaskPolling();
  }

  return {
    fetchUploadTasks,
    refreshUploadTaskServerConcurrency,
    startUploadTaskPolling,
    bumpKeepPolling,
    stopUploadTaskPolling,
    connectUploadTaskStream,
    disconnectUploadTaskStream,
    cleanupUploadTasks,
  };
}

export type UploadTaskStream = ReturnType<typeof useUploadTaskStream>;

export function getActiveRemoteUploadSlotUsage(store: UploadTaskStore) {
  return store.uploadTasks.value.filter((t) => {
    if (t.status === "running") return true;
    if (t.status === "pending") return !store.pendingRemoteResumeTaskIds.has(String(t.task_id));
    return false;
  }).length;
}

export function getNextLocalUploadTaskCandidate(store: UploadTaskStore) {
  return (
    store.displayUploadTasks.value.find((task) => {
      if (store.hiddenUploadTaskKeys.has(getUploadTaskStableKey(task))) return false;
      return (
        isLocalUploadTask(task) &&
        task.status === "pending" &&
        !store.localDispatchingTaskIds.has(task.task_id) &&
        !store.canceledLocalUploadTaskIds.has(task.task_id) &&
        !store.pausedLocalUploadTaskIds.has(task.task_id) &&
        Boolean(store.localUploadTaskPayloads.get(task.task_id)?.file)
      );
    }) || null
  );
}

export function getNextRemoteResumeTaskCandidate(store: UploadTaskStore) {
  return (
    store.displayUploadTasks.value.find((task) => {
      if (store.hiddenUploadTaskKeys.has(getUploadTaskStableKey(task))) return false;
      if (isLocalUploadTask(task)) return false;
      return (
        store.pendingRemoteResumeTaskIds.has(String(task.task_id)) &&
        ["pending", "paused", "failed", "canceled"].includes(task.status)
      );
    }) || null
  );
}
