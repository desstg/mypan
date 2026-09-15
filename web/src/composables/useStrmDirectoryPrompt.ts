import { computed, onUnmounted, ref, watch, type Ref } from "vue";
import type { FileItem } from "@/api/types";
import { fetchStrmDirectoryStatus, type StrmDirectoryStatus } from "@/api/strm";

function buildDirectoryItems(files: FileItem[]) {
  return files.map((file) => ({
    id: file.id,
    name: file.name,
    size: file.size,
    is_dir: file.is_dir,
  }));
}

function hasRegularFile(files: FileItem[]) {
  return files.some((file) => !file.is_dir);
}

export function formatStrmDirectoryPrompt(status: StrmDirectoryStatus): string {
  const parts: string[] = [];
  const strm = Number(status.pending_strm || 0);
  const meta = Number(status.pending_metadata || 0);
  if (strm > 0) parts.push(`${strm} 个 STRM`);
  if (meta > 0) parts.push(`${meta} 个元数据`);
  if (!parts.length) return "";
  return `发现待处理的 ${parts.join("、")}，是否现在处理？`;
}

export function useStrmDirectoryPrompt(options: {
  isAdmin: Ref<boolean>;
  accountId: Ref<number | null>;
  files: Ref<FileItem[]>;
  loading: Ref<boolean>;
  refreshing: Ref<boolean>;
  enabled?: Ref<boolean>;
  getDisplayPath: () => string;
  getParentId: () => string;
}) {
  const status = ref<StrmDirectoryStatus | null>(null);
  const dismissedKey = ref<string | null>(null);
  let seq = 0;
  let refreshTimer: number | undefined;
  let statusController: AbortController | undefined;

  function promptKey(): string {
    const id = options.accountId.value;
    if (!id) return "";
    return `${id}:${options.getParentId()}:${options.getDisplayPath()}`;
  }

  const promptText = computed(() => {
    if (!status.value) return "";
    return formatStrmDirectoryPrompt(status.value);
  });

  const showPrompt = computed(() => {
    if (options.enabled && !options.enabled.value) return false;
    if (!options.isAdmin.value || options.loading.value || options.refreshing.value) return false;
    if (!status.value?.matched_task_id) return false;
    const pending =
      Number(status.value.pending_strm || 0) + Number(status.value.pending_metadata || 0);
    if (pending <= 0) return false;
    return dismissedKey.value !== promptKey();
  });

  async function refreshStatus() {
    clearRefreshTimer();
    abortStatusRequest();
    const accountId = options.accountId.value;
    if ((options.enabled && !options.enabled.value) || !options.isAdmin.value || !accountId) {
      seq += 1;
      status.value = null;
      return;
    }
    if (!hasRegularFile(options.files.value)) {
      seq += 1;
      status.value = null;
      return;
    }
    const currentSeq = ++seq;
    const path = options.getDisplayPath();
    const parentId = options.getParentId();
    const items = buildDirectoryItems(options.files.value);
    const controller = new AbortController();
    statusController = controller;
    try {
      const result = await fetchStrmDirectoryStatus({
        account_id: accountId,
        parent_id: parentId,
        path,
        items,
      }, controller.signal);
      if (isStaleStatusRequest(currentSeq, accountId, parentId, path)) return;
      status.value = result;
    } catch (error) {
      if (isCancelledRequest(error)) return;
      if (isStaleStatusRequest(currentSeq, accountId, parentId, path)) return;
      status.value = null;
    } finally {
      if (statusController === controller) {
        statusController = undefined;
      }
    }
  }

  function isStaleStatusRequest(requestSeq: number, accountId: number, parentId: string, path: string) {
    return (
      requestSeq !== seq ||
      options.accountId.value !== accountId ||
      options.getParentId() !== parentId ||
      options.getDisplayPath() !== path ||
      options.loading.value ||
      options.refreshing.value
    );
  }

  function clearRefreshTimer() {
    if (refreshTimer !== undefined) {
      window.clearTimeout(refreshTimer);
      refreshTimer = undefined;
    }
  }

  function abortStatusRequest() {
    if (statusController) {
      statusController.abort();
      statusController = undefined;
    }
  }

  function isCancelledRequest(error: unknown) {
    return (
      typeof error === "object" &&
      error !== null &&
      "errorType" in error &&
      (error as { errorType?: string }).errorType === "aborted"
    );
  }

  function invalidateStatus() {
    seq += 1;
    clearRefreshTimer();
    abortStatusRequest();
    status.value = null;
  }

  function scheduleRefreshStatus() {
    clearRefreshTimer();
    refreshTimer = window.setTimeout(() => {
      refreshTimer = undefined;
      void refreshStatus();
    }, 400);
  }

  function dismissPrompt() {
    dismissedKey.value = promptKey();
  }

  function clearPrompt() {
    status.value = null;
  }

  watch(
    () =>
      [
        options.enabled?.value,
        options.isAdmin.value,
        options.accountId.value,
        options.getParentId(),
        options.getDisplayPath(),
        options.files.value,
        options.loading.value,
        options.refreshing.value,
      ] as const,
    () => {
      if (options.enabled && !options.enabled.value) {
        invalidateStatus();
        return;
      }
      if (options.loading.value || options.refreshing.value) {
        invalidateStatus();
        return;
      }
      if (!hasRegularFile(options.files.value)) {
        invalidateStatus();
        return;
      }
      scheduleRefreshStatus();
    },
    { deep: true },
  );

  onUnmounted(() => {
    invalidateStatus();
  });

  return {
    status,
    promptText,
    showPrompt,
    refreshStatus,
    dismissPrompt,
    clearPrompt,
  };
}
