import { computed, reactive, ref } from "vue";
import type { UploadTask } from "@/types/upload";
import { getUploadTaskStableKey } from "@/composables/upload/uploadTaskFormatters";
import type { LocalUploadPayload, UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";

const LOCAL_UPLOAD_SESSION_KEY = "litepan.local-upload-tasks";

type PersistedLocalUploadTask = Pick<
  UploadTask,
  "task_id" | "batch_id" | "batch_name" | "batch_placeholder" | "account_id" | "account_name" | "file_name" | "rel_path" | "rel_dir" | "target_path" | "target_display_path" | "status" | "progress" | "uploaded_bytes" | "total_bytes" | "message" | "error"
>;

export function useUploadTaskStore(deps: UploadTaskDeps) {
  const uploadTasks = ref<UploadTask[]>([]);
  const localUploadTasks = ref<UploadTask[]>([]);
  const uploadTaskPanelOpen = ref(false);
  const taskPanelCategory = ref<"upload" | "relay" | "offline">("upload");
  const uploadTaskPanelLoading = ref(false);
  const uploadTaskPanelLoadingText = ref("正在准备上传任务...");
  const uploadTaskOrderMap = ref<Record<string, number>>({});
  const uploadTaskServerConcurrency = ref(3);
  const batchPauseInProgress = ref(false);
  const pendingDirRefreshBatches = ref<Record<string, { count: number; creationRefreshed: boolean }>>({});
  const remoteUploadTaskIndexes = new Map<string, number>();
  const localUploadTaskIndexes = new Map<string, number>();
  let localTaskPersistTimer: ReturnType<typeof setTimeout> | null = null;

  function registerDirRefreshBatch(key: string, count: number) {
    pendingDirRefreshBatches.value = {
      ...pendingDirRefreshBatches.value,
      [key]: { count, creationRefreshed: false },
    };
  }

  function markDirRefreshBatchCreated(key: string) {
    const cur = pendingDirRefreshBatches.value[key];
    if (!cur || cur.creationRefreshed) return;
    pendingDirRefreshBatches.value = {
      ...pendingDirRefreshBatches.value,
      [key]: { ...cur, creationRefreshed: true },
    };
  }

  function resolveDirRefreshBatch(key: string) {
    if (!(key in pendingDirRefreshBatches.value)) return;
    const next = { ...pendingDirRefreshBatches.value };
    delete next[key];
    pendingDirRefreshBatches.value = next;
  }

  let uploadTaskOrderCounter = 0;

  const localUploadTaskControllers = new Map<string, AbortController>();
  const localUploadTaskPayloads = new Map<string, LocalUploadPayload>();
  const canceledLocalUploadTaskIds = new Set<string>();
  const pausedLocalUploadTaskIds = new Set<string>();
  const localDispatchingTaskIds = new Set<string>();
  const pendingRemoteResumeTaskIds = new Set<string>();
  const hiddenUploadTaskKeys = reactive(new Set<string>());
  let folderUploadRefreshPending = false;

  function uploadAffectsCurrentDirectory(task: UploadTask, currentPath: string) {
    if (String(task.account_id) !== String(deps.selectedAccountId.value)) return false;
    if (task.status !== "success" && task.status !== "skipped") return false;
    const parentId = String(task.result?.parent_id ?? task.target_path ?? "");
    return parentId === currentPath || task.target_path === currentPath;
  }

  function ensureUploadTaskDisplayOrder(task: UploadTask) {
    const key = getUploadTaskStableKey(task);
    if (!key || uploadTaskOrderMap.value[key]) return;
    const preferred = Number(task.queue_order || 0);
    const next = preferred > 0 ? preferred : uploadTaskOrderCounter + 1;
    uploadTaskOrderCounter = Math.max(uploadTaskOrderCounter, next);
    uploadTaskOrderMap.value[key] = next;
  }

  const displayUploadTasks = computed(() => {
    return [...localUploadTasks.value, ...uploadTasks.value].filter(
      (t) => !hiddenUploadTaskKeys.has(getUploadTaskStableKey(t)),
    );
  });

  const relayTasks = computed(() =>
    displayUploadTasks.value.filter(
      (task) => task.source_type === "cross_transfer" && task.phase === "downloading",
    ),
  );

  const activeRelayTasks = computed(() =>
    relayTasks.value.filter((task) => ["pending", "running", "paused"].includes(task.status)),
  );

  const failedRelayTasks = computed(() =>
    relayTasks.value.filter((task) => ["failed", "canceled"].includes(task.status)),
  );

  const activeRelayCount = computed(() => activeRelayTasks.value.length);

  const activeUploadTasks = computed(() =>
    displayUploadTasks.value.filter(
      (task) =>
        !(task.source_type === "cross_transfer" && task.phase === "downloading") &&
        (task.status === "pending" || task.status === "running"),
    ),
  );
  const uploadTaskBadgeText = computed(() => {
    const running = activeUploadTasks.value.length;
    if (running > 0) return `上传中 ${running}`;
    if (activeRelayCount.value > 0) return `跨盘中 ${activeRelayCount.value}`;
    const failed = displayUploadTasks.value.filter((t) => t.status === "failed").length;
    if (failed > 0) return `失败 ${failed}`;
    const paused = displayUploadTasks.value.filter((t) => t.status === "paused").length;
    if (paused > 0) return `已暂停 ${paused}`;
    const success = displayUploadTasks.value.filter((t) => t.status === "success").length;
    if (success > 0) return `上传完成 ${success}`;
    return "";
  });

  const uploadTaskLabel = computed(() => uploadTaskBadgeText.value || "暂无传输任务");
  function createLocalUploadTask(file: File, options: Partial<UploadTask> = {}): UploadTask {
    const now = Date.now() / 1000;
    return {
      task_id: `local-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      batch_id: options.batch_id,
      batch_name: options.batch_name,
      batch_placeholder: options.batch_placeholder,
      account_id: deps.selectedAccountId.value as number,
      account_name: deps.selectedAccountName.value,
      file_name: options.file_name || file.name,
      rel_path: options.rel_path,
      rel_dir: options.rel_dir,
      target_path: options.target_path || deps.currentPath.value,
      target_display_path: options.target_display_path || "",
      status: "pending",
      progress: 0,
      uploaded_bytes: 0,
      total_bytes: file.size,
      message: "等待发送到 MyPan 服务器",
      error: "",
      created_at: now,
      updated_at: now,
    };
  }

  function createSkippedUploadTask(file: File, reason: string, options: Partial<UploadTask> = {}): UploadTask {
    return {
      ...createLocalUploadTask(file, options),
      task_id: `local-skip-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      status: "skipped",
      message: reason,
    };
  }

  function addLocalUploadTask(task: UploadTask) {
    addLocalUploadTasks([task]);
  }

  function rebuildLocalUploadTaskIndexes() {
    localUploadTaskIndexes.clear();
    localUploadTasks.value.forEach((task, index) => localUploadTaskIndexes.set(task.task_id, index));
  }

  function addLocalUploadTasks(tasks: UploadTask[]) {
    if (!tasks.length) return;
    tasks.forEach(ensureUploadTaskDisplayOrder);
    // 调度器按展示顺序取下一个本地任务，追加可保持先加入先上传。
    localUploadTasks.value = [...localUploadTasks.value, ...tasks];
    rebuildLocalUploadTaskIndexes();
    persistLocalUploadTasks();
  }

  function updateLocalUploadTask(taskId: string, patch: Partial<UploadTask>) {
    const index = localUploadTaskIndexes.get(taskId);
    if (index === undefined) return;
    localUploadTasks.value[index] = { ...localUploadTasks.value[index], ...patch };
    persistLocalUploadTasks();
  }

  function removeLocalUploadTask(taskId: string) {
    localUploadTasks.value = localUploadTasks.value.filter((t) => t.task_id !== taskId);
    rebuildLocalUploadTaskIndexes();
    localUploadTaskPayloads.delete(taskId);
    persistLocalUploadTasks();
  }

  function pruneLocalUploadTasksByStableKeys(keys: string[]) {
    if (!keys.length) return;
    const keySet = new Set(keys.filter(Boolean));
    if (!keySet.size) return;
    const removedTaskIDs: string[] = [];
    const next = localUploadTasks.value.filter((task) => {
      const removed = keySet.has(getUploadTaskStableKey(task));
      if (removed) removedTaskIDs.push(task.task_id);
      return !removed;
    });
    if (next.length === localUploadTasks.value.length) return;
    localUploadTasks.value = next;
    for (const taskID of removedTaskIDs) {
      localUploadTaskPayloads.delete(taskID);
      localUploadTaskControllers.delete(taskID);
      canceledLocalUploadTaskIds.delete(taskID);
      pausedLocalUploadTaskIds.delete(taskID);
      localDispatchingTaskIds.delete(taskID);
    }
    rebuildLocalUploadTaskIndexes();
    persistLocalUploadTasks();
  }

  function serializeLocalUploadTask(task: UploadTask): PersistedLocalUploadTask {
    return {
      task_id: task.task_id,
      batch_id: task.batch_id,
      batch_name: task.batch_name,
      batch_placeholder: task.batch_placeholder,
      account_id: task.account_id,
      account_name: task.account_name,
      file_name: task.file_name,
      rel_path: task.rel_path,
      rel_dir: task.rel_dir,
      target_path: task.target_path,
      target_display_path: task.target_display_path,
      status: task.status,
      progress: Number(task.progress || 0),
      uploaded_bytes: Number(task.uploaded_bytes || 0),
      total_bytes: Number(task.total_bytes || 0),
      message: String(task.message || ""),
      error: String(task.error || ""),
    };
  }

  function persistLocalUploadTasks() {
    if (localTaskPersistTimer) clearTimeout(localTaskPersistTimer);
    localTaskPersistTimer = setTimeout(() => {
      localTaskPersistTimer = null;
      flushLocalUploadTasks();
    }, 750);
  }

  function flushLocalUploadTasks() {
    if (typeof window === "undefined") return;
    if (!localUploadTasks.value.length) {
      window.sessionStorage.removeItem(LOCAL_UPLOAD_SESSION_KEY);
      return;
    }
    const payload = localUploadTasks.value.map(serializeLocalUploadTask);
    window.sessionStorage.setItem(LOCAL_UPLOAD_SESSION_KEY, JSON.stringify(payload));
  }

  function restoreLocalUploadTasks() {
    if (typeof window === "undefined") return;
    const raw = window.sessionStorage.getItem(LOCAL_UPLOAD_SESSION_KEY);
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw) as PersistedLocalUploadTask[];
      const restored = parsed.map((task) => ({
        ...task,
        status: "failed",
        progress: 0,
        message: "页面已刷新，本地投递已中断，请重新选择文件",
        error: "页面刷新后无法继续本地投递，请重新选择文件",
      })) as UploadTask[];
      restored.forEach((task) => ensureUploadTaskDisplayOrder(task));
      localUploadTasks.value = restored;
      rebuildLocalUploadTaskIndexes();
      persistLocalUploadTasks();
    } catch {
      window.sessionStorage.removeItem(LOCAL_UPLOAD_SESSION_KEY);
    }
  }

  function patchRemoteUploadTask(taskId: string, patch: Partial<UploadTask>) {
    const index = remoteUploadTaskIndexes.get(taskId);
    if (index === undefined) return;
    uploadTasks.value[index] = { ...uploadTasks.value[index], ...patch };
  }

  function removeRemoteUploadTask(taskId: string) {
    removeRemoteUploadTasks([taskId]);
  }

  function replaceRemoteUploadTasks(tasks: UploadTask[]) {
    const currentByID = new Map(uploadTasks.value.map((task) => [task.task_id, task] as const));
    uploadTasks.value = tasks.map((task) => {
      const current = currentByID.get(task.task_id);
      if (!current) return task;
      const incomingUpdatedAt = Number(task.updated_at || 0);
      const currentUpdatedAt = Number(current.updated_at || 0);
      return incomingUpdatedAt > 0 && currentUpdatedAt > incomingUpdatedAt ? current : task;
    });
    remoteUploadTaskIndexes.clear();
    uploadTasks.value.forEach((task, index) => remoteUploadTaskIndexes.set(task.task_id, index));
  }

  function upsertRemoteUploadTasks(tasks: UploadTask[]) {
    for (const task of tasks) {
      ensureUploadTaskDisplayOrder(task);
      const index = remoteUploadTaskIndexes.get(task.task_id);
      if (index === undefined) {
        remoteUploadTaskIndexes.set(task.task_id, uploadTasks.value.length);
        uploadTasks.value.push(task);
      } else {
        const current = uploadTasks.value[index];
        const incomingUpdatedAt = Number(task.updated_at || 0);
        const currentUpdatedAt = Number(current.updated_at || 0);
        if (incomingUpdatedAt > 0 && currentUpdatedAt > 0 && incomingUpdatedAt < currentUpdatedAt) {
          continue;
        }
        uploadTasks.value[index] = task;
      }
    }
  }

  function removeRemoteUploadTasks(taskIds: string[]) {
    if (!taskIds.length) return;
    const removed = new Set(taskIds);
    const next = uploadTasks.value.filter((task) => !removed.has(task.task_id));
    if (next.length === uploadTasks.value.length) return;
    replaceRemoteUploadTasks(next);
  }

  function markFolderUploadRefreshPending() {
    folderUploadRefreshPending = true;
  }

  function consumeFolderUploadRefreshPending() {
    const pending = folderUploadRefreshPending;
    folderUploadRefreshPending = false;
    return pending;
  }

  return {
    uploadTasks,
    localUploadTasks,
    uploadTaskPanelOpen,
    taskPanelCategory,
    uploadTaskPanelLoading,
    uploadTaskPanelLoadingText,
    uploadTaskOrderMap,
    uploadTaskServerConcurrency,
    batchPauseInProgress,
    localUploadTaskControllers,
    localUploadTaskPayloads,
    canceledLocalUploadTaskIds,
    pausedLocalUploadTaskIds,
    localDispatchingTaskIds,
    pendingRemoteResumeTaskIds,
    hiddenUploadTaskKeys,
    relayTasks,
    pendingDirRefreshBatches,
    registerDirRefreshBatch,
    markDirRefreshBatchCreated,
    resolveDirRefreshBatch,
    activeRelayTasks,
    failedRelayTasks,
    activeRelayCount,
    displayUploadTasks,
    activeUploadTasks,
    uploadTaskLabel,
    uploadAffectsCurrentDirectory,
    ensureUploadTaskDisplayOrder,
    createLocalUploadTask,
    createSkippedUploadTask,
    addLocalUploadTask,
    addLocalUploadTasks,
    updateLocalUploadTask,
    removeLocalUploadTask,
    pruneLocalUploadTasksByStableKeys,
    persistLocalUploadTasks,
    restoreLocalUploadTasks,
    patchRemoteUploadTask,
    removeRemoteUploadTask,
    replaceRemoteUploadTasks,
    upsertRemoteUploadTasks,
    removeRemoteUploadTasks,
    markFolderUploadRefreshPending,
    consumeFolderUploadRefreshPending,
  };
}

export type UploadTaskStore = ReturnType<typeof useUploadTaskStore>;
