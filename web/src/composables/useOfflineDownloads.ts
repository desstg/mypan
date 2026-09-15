import { computed, onUnmounted, ref, type Ref } from "vue";
import { offlineDownloadApi } from "@/api/offlineDownload";
import { getApiErrorMessage } from "@/api/client";
import { filesApi } from "@/api/files";
import type { UploadCrumb } from "@/composables/upload/uploadTaskTypes";
import { showConfirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import type { OfflineDownloadCapabilities, OfflineDownloadTask } from "@/types/offline-download";

type Deps = {
  selectedAccountId: Ref<number | null>;
  currentParentId: Ref<string>;
  refreshFiles: () => Promise<void>;
  openDirectory: (
    accountId: number,
    crumbs: UploadCrumb[],
    opts?: { forceRefresh?: boolean; silent?: boolean },
  ) => Promise<void>;
};

const activeStatuses = new Set(["pending", "running", "retrying"]);
const pollIntervalMs = 3000;

function sameParent(left: string, right: string) {
  const normalize = (value: string) => (!value || value === "0" ? "" : value);
  return normalize(left) === normalize(right);
}

function isRootParent(value: string) {
  return !value || value === "0";
}

function getTargetSegments(task: OfflineDownloadTask) {
  return String(task.target_display_path || "")
    .replace(/\\/g, "/")
    .split("/")
    .filter(Boolean);
}

export function useOfflineDownloads(deps: Deps) {
  const capability = ref<OfflineDownloadCapabilities | null>(null);
  const capabilityLoading = ref(false);
  const modalOpen = ref(false);
  const tasks = ref<OfflineDownloadTask[]>([]);
  const loading = ref(false);
  const refreshing = ref(false);
  let pollTimer: number | undefined;
  let capabilityRequest = 0;

  const activeTasks = computed(() => tasks.value.filter((task) => activeStatuses.has(task.status)));
  const failedTasks = computed(() => tasks.value.filter((task) => task.status === "failed"));
  const successfulTasks = computed(() => tasks.value.filter((task) => task.status === "success" && task.provider_kind !== "builtin"));

  async function loadCapability(accountId = deps.selectedAccountId.value) {
    const request = ++capabilityRequest;
    capability.value = null;
    if (!accountId) {
      capabilityLoading.value = false;
      return;
    }
    capabilityLoading.value = true;
    try {
      const next = await offlineDownloadApi.capabilities(accountId);
      if (request === capabilityRequest) capability.value = next;
    } catch {
      if (request === capabilityRequest) capability.value = null;
    } finally {
      if (request === capabilityRequest) capabilityLoading.value = false;
    }
  }

  function openModal() {
    if (!deps.selectedAccountId.value) {
      toast.info("请先选择一个账号");
      return;
    }
    if (!capability.value?.supported) {
      toast.info("当前网盘不支持离线下载");
      return;
    }
    modalOpen.value = true;
  }

  function closeModal() {
    modalOpen.value = false;
  }

  function registerTasks(created: OfflineDownloadTask[]) {
    const byId = new Map(tasks.value.map((task) => [task.task_id, task]));
    for (const task of created) byId.set(task.task_id, task);
    tasks.value = [...byId.values()].sort((a, b) => b.created_at - a.created_at);
    ensurePolling();
  }

  async function replaceTasks(next: OfflineDownloadTask[]) {
    const before = new Map(tasks.value.map((task) => [task.task_id, task.status]));
    tasks.value = next;
    const affectsCurrent = next.some(
      (task) =>
        before.get(task.task_id) !== "success" &&
        task.status === "success" &&
        task.provider_kind !== "builtin" &&
        task.account_id === deps.selectedAccountId.value &&
        sameParent(task.target_parent_id, deps.currentParentId.value),
    );
    if (affectsCurrent) await deps.refreshFiles();
  }

  async function fetchTasks(refresh = true, quiet = false) {
    if (!quiet) loading.value = true;
    try {
      const next = await offlineDownloadApi.listTasks(refresh);
      await replaceTasks(next);
    } catch (error) {
      if (!quiet) toast.error(getApiErrorMessage(error, "离线下载任务加载失败"));
    } finally {
      if (!quiet) loading.value = false;
      ensurePolling();
    }
  }

  async function refreshTasks() {
    if (refreshing.value) return;
    refreshing.value = true;
    try {
      await replaceTasks(await offlineDownloadApi.refreshTasks());
    } catch (error) {
      toast.error(getApiErrorMessage(error, "离线下载任务刷新失败"));
    } finally {
      refreshing.value = false;
      ensurePolling();
    }
  }

  async function deleteTasks(target: OfflineDownloadTask[]) {
    if (!target.length) return;
    const allCompleted = target.every((task) => task.status === "success");
    const result = await showConfirm({
      title: allCompleted ? "清空已完成任务" : "删除当前任务",
      message: `将处理 ${target.length} 条离线下载任务。`,
      hint: "115 任务会同步删除网盘端任务历史；已下载文件不会被删除。",
      icon: "trash",
      confirmText: "确认删除",
      cancelText: "取消",
      danger: true,
    }).catch(() => null);
    if (result?.action !== "confirm") return;
    try {
      const deleted = await offlineDownloadApi.batchDelete(target.map((task) => task.task_id));
      const ids = new Set(deleted.deleted_task_ids);
      tasks.value = tasks.value.filter((task) => !ids.has(task.task_id));
      if (deleted.failed_task_ids.length) toast.warning(`${deleted.failed_task_ids.length} 个任务未能删除`);
      else toast.success("离线下载任务已清理");
    } catch (error) {
      toast.error(getApiErrorMessage(error, "批量删除离线下载任务失败"));
    }
  }

  async function buildTaskBreadcrumb(task: OfflineDownloadTask): Promise<UploadCrumb[]> {
    const rootCrumb: UploadCrumb = { id: "", name: "根目录" };
    const targetParentId = String(task.target_parent_id || "");
    if (isRootParent(targetParentId)) {
      return [rootCrumb];
    }

    const segments = getTargetSegments(task);
    if (segments.length === 0) {
      return [rootCrumb, { id: targetParentId, name: "当前目录" }];
    }

    const breadcrumb: UploadCrumb[] = [rootCrumb];
    let currentParentId = "";

    for (let index = 0; index < segments.length; index += 1) {
      const segment = segments[index];
      const isLast = index === segments.length - 1;
      try {
        const res = await filesApi.list(task.account_id, currentParentId);
        const matched = res.items.find((item) => item.is_dir && item.name === segment);
        const resolvedId = matched?.id ? String(matched.id) : isLast ? targetParentId : currentParentId;
        breadcrumb.push({ id: resolvedId, name: segment });
        currentParentId = resolvedId;
      } catch {
        const fallbackId = isLast ? targetParentId : currentParentId;
        breadcrumb.push({ id: fallbackId, name: segment });
        currentParentId = fallbackId;
      }
    }

    return breadcrumb;
  }

  async function handlePrimaryAction(task: OfflineDownloadTask) {
    if (task.status !== "success") return false;
    try {
      const crumbs = await buildTaskBreadcrumb(task);
      await deps.openDirectory(task.account_id, crumbs, { forceRefresh: true });
      return true;
    } catch (error) {
      toast.error(getApiErrorMessage(error, "打开离线任务目录失败"));
      return false;
    }
  }

  function ensurePolling() {
    if (pollTimer !== undefined) window.clearTimeout(pollTimer);
    pollTimer = undefined;
    if (activeTasks.value.length === 0) return;
    pollTimer = window.setTimeout(async () => {
      pollTimer = undefined;
      await fetchTasks(true, true);
    }, pollIntervalMs);
  }

  function statusText(task: OfflineDownloadTask) {
    if (task.provider_kind === "builtin") {
      if (task.status === "running" && task.phase === "verifying") return "校验中";
      if (task.status === "running" && task.phase === "handoff") return "准备上传";
      if (task.status === "success") return "已转入上传";
    }
    switch (task.status) {
      case "pending": return "等待中";
      case "running": return "下载中";
      case "retrying": return "重试中";
      case "success": return "已完成";
      case "failed": return "失败";
      default: return task.status;
    }
  }

  function sourceLabel(task: OfflineDownloadTask) {
    const providerPrefix = task.provider_kind === "builtin" ? "内置" : "原生";
    if (task.source_kind === "bt") return "BT";
    try {
      return `${providerPrefix} ${new URL(task.source).protocol.replace(":", "").toUpperCase()}`;
    } catch {
      return `${providerPrefix} 链接`;
    }
  }

  function providerLabel(task: OfflineDownloadTask) {
    return task.provider_kind === "builtin" ? "内置" : "原生";
  }

  function magnetDiagnosticsText(task: OfflineDownloadTask) {
    if (task.provider_kind !== "builtin" || task.executor_type !== "url_magnet") return "";
    const diagnostics = task.magnet_diagnostics;
    if (!diagnostics) return "";
    const parts: string[] = [];
    const active = diagnostics.active_peers || 0;
    const pending = diagnostics.pending_peers || 0;
    const total = diagnostics.total_peers || 0;
    const sources = active || total || pending;
    parts.push(`来源 ${sources}`);
    parts.push(`做种 ${diagnostics.connected_seeders || 0}`);
    parts.push(`节点 ${diagnostics.dht_good_nodes || diagnostics.dht_nodes || 0}`);
    return parts.join(" · ");
  }

  function detailText(task: OfflineDownloadTask) {
    if (task.error) {
      const message = String(task.error).replace(/^[A-Z_]+:\s*/, "");
      if (message.includes("获取磁力文件信息超时") || message.includes("获取磁力元数据超时")) {
        return "磁力文件信息获取超时，请稍后重试";
      }
      return message;
    }
    const diagnosticsText = magnetDiagnosticsText(task);
    if (diagnosticsText && task.message) return `${task.message} · ${diagnosticsText}`;
    return diagnosticsText || task.message || "";
  }

  function speedText(task: OfflineDownloadTask) {
    if (task.provider_kind !== "builtin") return "-";
    const speed = Number(task.speed_bytes || 0);
    if (!(speed > 0)) return "-";
    const units = ["B/s", "KB/s", "MB/s", "GB/s"];
    let value = speed;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
      value /= 1024;
      unitIndex += 1;
    }
    const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
    return `${value.toFixed(digits)} ${units[unitIndex]}`;
  }

  onUnmounted(() => {
    if (pollTimer !== undefined) window.clearTimeout(pollTimer);
  });

  return {
    capability,
    capabilityLoading,
    modalOpen,
    tasks,
    loading,
    refreshing,
    activeTasks,
    failedTasks,
    successfulTasks,
    loadCapability,
    openModal,
    closeModal,
    registerTasks,
    fetchTasks,
    refreshTasks,
    deleteTasks,
    handlePrimaryAction,
    statusText,
    sourceLabel,
    providerLabel,
    magnetDiagnosticsText,
    detailText,
    speedText,
  };
}

export type OfflineDownloads = ReturnType<typeof useOfflineDownloads>;
