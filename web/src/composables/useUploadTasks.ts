import {
  formatRelayPart,
  formatRelaySpeed,
  formatUploadPart,
  getUploadTaskDriverBadge,
  getUploadTaskDisplayStatus,
  getUploadTaskPhaseLabel,
  getUploadTaskSpeedText,
  getUploadTaskStatusText,
  isUploadTaskActive,
  shouldShowUploadTaskHairline,
  shouldShowUploadTaskMetaPercent,
} from "@/composables/upload/uploadTaskFormatters";
import { useLocalUploadDispatcher } from "@/composables/upload/useLocalUploadDispatcher";
import { useUploadTaskActions } from "@/composables/upload/useUploadTaskActions";
import { useUploadTaskStore } from "@/composables/upload/useUploadTaskStore";
import { useUploadTaskStream } from "@/composables/upload/useUploadTaskStream";
import type { UploadRuntimeHooks, UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";
import { showConfirm } from "@/composables/useConfirm";

export type { UploadTaskDeps as Deps } from "@/composables/upload/uploadTaskTypes";

export function useUploadTasks(deps: UploadTaskDeps) {
  const store = useUploadTaskStore(deps);
  store.restoreLocalUploadTasks();

  function kickUploadTaskPolling() {
    stream.bumpKeepPolling();
    void stream.fetchUploadTasks();
  }

  function markCurrentDirRefreshPending() {
    store.markFolderUploadRefreshPending();
  }

  function refreshCurrentFiles() {
    void deps.refreshFiles(true);
  }

  function afterLocalUploadCreated() {
    kickUploadTaskPolling();
  }

  function registerDirRefreshBatch(key: string, count: number) {
    store.registerDirRefreshBatch(key, count);
  }

  function resolveDirRefreshBatch(key: string) {
    store.resolveDirRefreshBatch(key);
  }

  const hooks: UploadRuntimeHooks = {
    startScheduler: async () => {},
    fetchTasks: async () => {},
    startPolling: () => {},
    stopPolling: () => {},
    connectStream: () => {},
    disconnectStream: () => {},
    closePanel: () => {},
  };

  const stream = useUploadTaskStream(deps, store, hooks);
  const dispatcher = useLocalUploadDispatcher(deps, store, stream);
  const actions = useUploadTaskActions(deps, store, stream, dispatcher);

  async function enqueueTerminalFiles(files: File[]) {
    if (!files.length) return;
    const result = await showConfirm({
      title: "确认上传",
      message: `将上传 ${files.length} 个项目到当前目录，是否继续？`,
      confirmText: "上传",
      cancelText: "取消",
      danger: false,
    }).catch(() => null);
    if (!result || result.action !== "confirm") return;
    for (const f of files) {
      void dispatcher.createSingleUploadTask(f).catch(() => {});
    }
  }

  async function enqueueTerminalFolderFiles(files: File[]) {
    if (!files.length) return;
    const rootName = String(
      (files[0] as File & { litepanRelativePath?: string }).litepanRelativePath || files[0].name,
    ).split("/")[0];
    const result = await showConfirm({
      title: "确认上传",
      message: `将上传文件夹「${rootName || "所选文件夹"}」中的 ${files.length} 个文件，是否继续？`,
      confirmText: "上传",
      cancelText: "取消",
      danger: false,
    }).catch(() => null);
    if (!result || result.action !== "confirm") return;
    await actions.enqueueUploadFolderFiles(files);
  }

  async function handleUploadFileChange(event: Event) {
    await actions.handleUploadFileChange(event);
  }

  async function handleUploadFolderChange(event: Event) {
    await actions.handleUploadFolderChange(event);
  }

  hooks.startScheduler = dispatcher.startUploadTaskScheduler;
  hooks.fetchTasks = stream.fetchUploadTasks;
  hooks.startPolling = stream.startUploadTaskPolling;
  hooks.stopPolling = stream.stopUploadTaskPolling;
  hooks.connectStream = stream.connectUploadTaskStream;
  hooks.disconnectStream = stream.disconnectUploadTaskStream;
  hooks.closePanel = actions.closeUploadTaskPanel;

  const getUploadTaskPhaseLabelBound = (task: Parameters<typeof getUploadTaskPhaseLabel>[0]) =>
    getUploadTaskPhaseLabel(task, store.pendingRemoteResumeTaskIds, store.localDispatchingTaskIds);

  const getUploadTaskDisplayStatusBound = (task: Parameters<typeof getUploadTaskDisplayStatus>[0]) =>
    getUploadTaskDisplayStatus(task, store.pendingRemoteResumeTaskIds);

  const getUploadTaskDriverBadgeBound = (
    task: Parameters<typeof getUploadTaskDriverBadge>[0],
  ) => getUploadTaskDriverBadge(task, deps.accounts.value);

  const getRelayTaskDriverBadge = (task: {
    source_driver_type?: string;
    source_account_id?: number;
    source_account_name?: string;
  }) =>
    getUploadTaskDriverBadge(
      {
        driver_type: task.source_driver_type,
        account_id: task.source_account_id ?? 0,
        account_name: task.source_account_name,
      },
      deps.accounts.value,
    );

  return {
    uploadTaskPanelOpen: store.uploadTaskPanelOpen,
    taskPanelCategory: store.taskPanelCategory,
    uploadTaskPanelLoading: store.uploadTaskPanelLoading,
    uploadTaskPanelLoadingText: store.uploadTaskPanelLoadingText,
    uploadTaskServerConcurrency: store.uploadTaskServerConcurrency,
    displayUploadTasks: store.displayUploadTasks,
    activeUploadTasks: store.activeUploadTasks,
    uploadTaskLabel: store.uploadTaskLabel,
    getUploadTaskStatusText,
    formatUploadPart,
    getUploadTaskSpeedText,
    getUploadTaskDriverBadge: getUploadTaskDriverBadgeBound,
    getUploadTaskDisplayStatus: getUploadTaskDisplayStatusBound,
    isUploadTaskActive,
    getUploadTaskPhaseLabel: getUploadTaskPhaseLabelBound,
    shouldShowUploadTaskMetaPercent,
    shouldShowUploadTaskHairline,
    handleDeleteUploadTask: actions.handleDeleteUploadTask,
    handleDeleteUploadTasks: actions.handleDeleteUploadTasks,
    handleUploadTaskPrimaryAction: actions.handleUploadTaskPrimaryAction,
    handleToggleUploadTasks: actions.handleToggleUploadTasks,
    openUploadTaskPanel: actions.openUploadTaskPanel,
    closeUploadTaskPanel: actions.closeUploadTaskPanel,
    openUploadNoticeFromPanel: actions.openUploadNoticeFromPanel,
    ensureUploadNoticeConfirmed: actions.ensureUploadNoticeConfirmed,
    handleTerminalUploadFile: actions.handleUploadFile,
    handleTerminalUploadFolder: actions.handleUploadFolder,
    enqueueTerminalFiles,
    enqueueTerminalFolderFiles,
    kickUploadTaskPolling,
    markCurrentDirRefreshPending,
    refreshCurrentFiles,
    afterLocalUploadCreated,
    registerDirRefreshBatch,
    resolveDirRefreshBatch,
    handleUploadFileChange,
    handleUploadFolderChange,
    fetchUploadTasks: stream.fetchUploadTasks,
    refreshUploadTaskServerConcurrency: stream.refreshUploadTaskServerConcurrency,
    getRelayTaskDriverBadge,
    handleDeleteRelayTasks: actions.handleDeleteRelayTasks,
    formatRelaySpeed,
    formatRelayPart,
    activeRelayTasks: store.activeRelayTasks,
    failedRelayTasks: store.failedRelayTasks,
    activeRelayCount: store.activeRelayCount,
    relayTasks: store.relayTasks,
    cleanupUploadTasks: stream.cleanupUploadTasks,
  };
}
