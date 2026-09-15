import { toast } from "@/composables/useToast";
import { confirmUploadNotice } from "@/composables/confirmUpload";
import type { LocalUploadDispatcher } from "@/composables/upload/useLocalUploadDispatcher";
import type { UploadTaskStream } from "@/composables/upload/useUploadTaskStream";
import type { UploadTaskStore } from "@/composables/upload/useUploadTaskStore";
import { UPLOAD_NOTICE_KEY, type UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";

export type UploadActionsCtx = {
  deps: UploadTaskDeps;
  store: UploadTaskStore;
  stream: UploadTaskStream;
  dispatcher: LocalUploadDispatcher;
};

export function useUploadPanelActions(ctx: UploadActionsCtx) {
  const { deps, store, stream } = ctx;

  async function openUploadTaskPanel(preferredCategory: "" | "relay" | "offline" = "") {
    store.uploadTaskPanelOpen.value = true;
    await Promise.all([
      stream.fetchUploadTasks(),
      deps.refreshOfflineTasks(true, true),
    ]);
    if (preferredCategory === "offline") {
      store.taskPanelCategory.value = "offline";
    } else if (
      preferredCategory === "relay" ||
      (store.activeRelayCount.value > 0 && store.activeUploadTasks.value.length === 0)
    ) {
      store.taskPanelCategory.value = "relay";
    } else {
      store.taskPanelCategory.value = "upload";
    }
    stream.connectUploadTaskStream();
    if (typeof EventSource === "undefined") {
      stream.startUploadTaskPolling();
    }
  }

  function closeUploadTaskPanel() {
    store.uploadTaskPanelOpen.value = false;
    if (store.activeUploadTasks.value.length === 0 && store.activeRelayCount.value === 0) {
      stream.disconnectUploadTaskStream();
      stream.stopUploadTaskPolling();
    }
  }

  async function openUploadNoticeDialog() {
    const result = await confirmUploadNotice();
    if (!result || result.action !== "confirm") return false;
    if (result.checked) localStorage.setItem(UPLOAD_NOTICE_KEY, "true");
    else localStorage.removeItem(UPLOAD_NOTICE_KEY);
    return true;
  }

  async function ensureUploadNoticeConfirmed() {
    if (localStorage.getItem(UPLOAD_NOTICE_KEY) === "true") return true;
    return openUploadNoticeDialog();
  }

  async function handleUploadFile() {
    if (!ctx.deps.selectedAccountId.value) {
      toast.info("请先选择账号");
      return;
    }
    if (!(await ensureUploadNoticeConfirmed())) return;
    ctx.deps.uploadFileInput.value?.click();
  }

  async function handleUploadFolder() {
    if (!ctx.deps.selectedAccountId.value) {
      toast.info("请先选择账号");
      return;
    }
    if (!(await ensureUploadNoticeConfirmed())) return;
    ctx.deps.uploadFolderInput.value?.click();
  }

  function openUploadNoticeFromPanel() {
    void openUploadNoticeDialog();
  }

  return {
    openUploadTaskPanel,
    closeUploadTaskPanel,
    openUploadNoticeFromPanel,
    handleUploadFile,
    handleUploadFolder,
    ensureUploadNoticeConfirmed,
  };
}
