import type { Account } from "@/api/types";
import { filesApi } from "@/api/files";
import type { UploadTask } from "@/types/upload";
import type { UploadCrumb } from "@/composables/upload/uploadTaskTypes";

export const SYSTEM_JUNK_FILES = new Set([".ds_store", ".localized", "thumbs.db", "desktop.ini"]);
export const SYSTEM_JUNK_DIRS = new Set([
  "__macosx",
  ".spotlight-v100",
  ".trashes",
  ".fseventsd",
  "$recycle.bin",
  "system volume information",
]);

export function getUploadTaskStableKey(task: UploadTask) {
  return String(task.client_task_id || task.task_id || "");
}

export function isLocalUploadTask(task: UploadTask) {
  return String(task.task_id).startsWith("local-");
}

export function isSendingToLitePanServerTask(task: UploadTask) {
  return isLocalUploadTask(task) && task.status === "pending";
}

export function isRemoteTaskWaitingResume(task: UploadTask, pendingRemoteResumeTaskIds: Set<string>) {
  return pendingRemoteResumeTaskIds.has(String(task.task_id));
}

export function getUploadTaskDisplayStatus(task: UploadTask, pendingRemoteResumeTaskIds: Set<string>) {
  if (
    isRemoteTaskWaitingResume(task, pendingRemoteResumeTaskIds) &&
    ["paused", "failed", "canceled"].includes(task.status)
  ) {
    return "pending";
  }
  return task.status;
}

export function isUploadTaskActive(task: UploadTask) {
  return ["pending", "running", "paused"].includes(task.status);
}

export function getUploadTaskStatusText(status: string) {
  const map: Record<string, string> = {
    pending: "等待中",
    running: "上传中",
    paused: "已暂停",
    success: "已完成",
    skipped: "已跳过",
    failed: "失败",
    canceled: "已取消",
  };
  return map[status] || "已取消";
}

export function formatUploadSpeed(bytesPerSecond?: number) {
  const speed = Number(bytesPerSecond || 0);
  if (!Number.isFinite(speed) || speed <= 0) return "";
  const units = ["B/s", "KB/s", "MB/s", "GB/s"];
  let value = speed;
  let idx = 0;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx += 1;
  }
  const precision = value >= 100 || idx === 0 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(precision)} ${units[idx]}`;
}

export function getUploadTaskSpeedText(task: UploadTask) {
  return task.status === "running" ? formatUploadSpeed(task.speed_bytes_per_second) : "";
}

export function formatUploadPart(task: UploadTask) {
  if (task.status !== "running") return "";
  const m = String(task.message || "").match(/分片[（(]\s*(\d+)\s*\/\s*(\d+)\s*[)）]/);
  return m ? `分片 ${m[1]}/${m[2]}` : "";
}

export function getUploadTaskPhaseLabel(
  task: UploadTask,
  pendingRemoteResumeTaskIds: Set<string>,
  localDispatchingTaskIds?: Set<string>,
) {
  const displayStatus = getUploadTaskDisplayStatus(task, pendingRemoteResumeTaskIds);
  if (displayStatus === "paused") return "已暂停";
  if (displayStatus === "running") return "上传到网盘";
  if (displayStatus === "pending") {
    if (isRemoteTaskWaitingResume(task, pendingRemoteResumeTaskIds)) return "等待继续";
    if (isSendingToLitePanServerTask(task)) {
      const progress = Number(task.progress || 0);
      if (
        localDispatchingTaskIds &&
        !localDispatchingTaskIds.has(task.task_id) &&
        progress <= 0
      ) {
        return "排队中";
      }
      return "发送至服务器";
    }
    return "等待中";
  }
  return getUploadTaskStatusText(displayStatus);
}

export function shouldShowUploadTaskMetaPercent(task: UploadTask) {
  if (["canceled", "success", "skipped"].includes(task.status)) return false;
  if (task.status === "pending" && isLocalUploadTask(task)) return true;
  return task.status === "running" || task.status === "paused" || task.status === "failed";
}

export function shouldShowUploadTaskHairline(task: UploadTask) {
  return task.status === "running";
}

export function findUploadTaskAccount(
  task: Pick<UploadTask, "account_id"> & Partial<UploadTask>,
  accounts: Account[],
) {
  if (task.account_id != null) {
    const byId = accounts.find((a) => String(a.id) === String(task.account_id));
    if (byId) return byId;
  }
  const driverType = String(task.driver_type || "").toLowerCase();
  if (!driverType) return undefined;
  return accounts.find((a) => a.driver_type.toLowerCase() === driverType);
}

export function getUploadTaskDriverBadge(
  task: Pick<UploadTask, "account_id"> & Partial<UploadTask>,
  accounts: Account[],
) {
  const account = findUploadTaskAccount(task, accounts);
  const title = task.account_name || account?.name || "上传目标网盘";
  if (account) {
    return {
      logo: account.driver_card_logo || "",
      color: account.driver_card_color || "#64748b",
      name:
        account.driver_card_name?.slice(0, 2) ||
        (task.account_name ? String(task.account_name).slice(0, 2) : "网盘"),
      title,
    };
  }
  return {
    logo: "",
    color: "#64748b",
    name: task.account_name ? String(task.account_name).slice(0, 2) : "网盘",
    title,
  };
}

export function normalizeUploadRelativePath(file: File) {
  const withRelativePath = file as File & { webkitRelativePath?: string; litepanRelativePath?: string };
  const raw = String(withRelativePath.litepanRelativePath || withRelativePath.webkitRelativePath || file.name).replace(/\\/g, "/");
  return raw.split("/").filter(Boolean).join("/");
}

export function getSystemUploadJunkReason(relativePath: string) {
  const parts = relativePath.split("/").filter(Boolean);
  if (!parts.length) return "";
  for (const part of parts.slice(0, -1)) {
    const n = part.toLowerCase();
    if (SYSTEM_JUNK_DIRS.has(n) || /^\.trash-\d+$/.test(n)) return "系统生成目录，已跳过";
  }
  const fileName = parts[parts.length - 1].toLowerCase();
  if (SYSTEM_JUNK_FILES.has(fileName) || fileName.startsWith("._")) return "系统生成文件，已跳过";
  return "";
}

export function formatRelaySpeed(task: { speed_bytes_per_second?: number }) {
  return formatUploadSpeed(task.speed_bytes_per_second);
}

export function formatRelayPart(task: { message?: string; phase?: string }) {
  if (task.phase !== "downloading" && task.phase !== "uploading") return "";
  const m = String(task.message || "").match(/分片[（(]\s*(\d+)\s*\/\s*(\d+)\s*[)）]/);
  return m ? `分片 ${m[1]}/${m[2]}` : "";
}

export function getUploadTaskOpenPath(task: UploadTask) {
  return String(task.result?.parent_id || task.result?.parent_path || task.target_path || "");
}

function isUploadRootPath(path: string, rootId: string) {
  const normalized = String(path || "").trim();
  if (!normalized) return true;
  return normalized === rootId || normalized === "0";
}

function getUploadTaskDirectorySegments(task: UploadTask) {
  const targetDisplayPath = String(task.target_display_path || "")
    .replace(/\\/g, "/")
    .split("/")
    .filter(Boolean);
  if (targetDisplayPath.length > 0) {
    return targetDisplayPath;
  }
  return String(task.file_name || "")
    .replace(/\\/g, "/")
    .split("/")
    .filter(Boolean)
    .slice(0, -1);
}

export async function buildUploadTaskBreadcrumb(
  account: Account,
  task: UploadTask,
  getRootId: (config: Record<string, unknown>) => string,
): Promise<UploadCrumb[]> {
  let config: Record<string, unknown> = {};
  try {
    config = JSON.parse(account.config || "{}") as Record<string, unknown>;
  } catch {
    config = {};
  }
  const rootId = getRootId(config);
  const targetPath = getUploadTaskOpenPath(task);
  const rootCrumb: UploadCrumb = { id: "", name: "根目录" };
  if (isUploadRootPath(targetPath, rootId)) {
    return [rootCrumb];
  }

  const directorySegments = getUploadTaskDirectorySegments(task);
  if (directorySegments.length === 0) {
    return [rootCrumb, { id: targetPath, name: "当前目录" }];
  }

  const breadcrumb: UploadCrumb[] = [rootCrumb];
  let currentParentId = "";

  for (let index = 0; index < directorySegments.length; index += 1) {
    const segment = directorySegments[index];
    const isLast = index === directorySegments.length - 1;
    try {
      // 上传刚完成时父目录缓存可能仍是旧数据，直接打开必须按最新目录结构解析。
      const res = await filesApi.list(account.id, currentParentId, { forceRefresh: true });
      const matchedFolder = res.items.find((item) => item.is_dir && item.name === segment);
      const resolvedPath = matchedFolder?.id ? String(matchedFolder.id) : isLast ? targetPath : currentParentId;
      breadcrumb.push({ id: resolvedPath, name: segment });
      currentParentId = resolvedPath;
    } catch {
      breadcrumb.push({
        id: isLast ? targetPath : currentParentId,
        name: segment,
      });
    }
  }

  return breadcrumb;
}
