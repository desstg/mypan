import { uploadApi } from "@/api/upload";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import {
  confirmBatchUploadTaskDelete,
  confirmUploadTaskDelete,
} from "@/composables/confirmUpload";
import { getUploadTaskStableKey, isLocalUploadTask, buildUploadTaskBreadcrumb } from "@/composables/upload/uploadTaskFormatters";
import type { UploadActionsCtx } from "@/composables/upload/useUploadPanelActions";
import type { UploadTask } from "@/types/upload";

type VisibleDeleteTarget = {
  taskId: string;
  rowKey: string;
  removeId: string;
};

export function useUploadBatchActions(ctx: UploadActionsCtx, closePanel: () => void) {
  const { deps, store, stream, dispatcher } = ctx;

  function getPendingResumeMessage(task: UploadTask) {
    if (task.source_type === "cross_transfer" && task.phase === "downloading") return "等待下载";
    return "等待上传";
  }

  function getPausedMessage(task: UploadTask) {
    if (task.source_type === "cross_transfer" && task.phase === "downloading") return "已暂停";
    return "上传已暂停";
  }

  function isQueuedRemoteResumeTask(task: UploadTask) {
    return !isLocalUploadTask(task) && store.pendingRemoteResumeTaskIds.has(String(task.task_id));
  }

  function formatRemovedTaskToast(count: number) {
    return count > 1 ? `已移除 ${count} 条上传记录` : "已移除上传记录";
  }

  function formatCloudDeleteStartToast(count: number) {
    return count > 1
      ? `任务已移除，${count} 个云端文件删除中…`
      : "任务已移除，云端文件删除中…";
  }

  function formatCloudDeleteResultToast(successCount: number, failedCount: number) {
    if (failedCount <= 0) {
      return successCount > 1 ? `${successCount} 个云端文件已删除` : "云端文件已删除";
    }
    if (successCount > 0) {
      return `已删除 ${successCount} 个云端文件，${failedCount} 个删除失败`;
    }
    return failedCount > 1 ? `${failedCount} 个云端文件删除失败` : "云端文件删除失败";
  }

  function taskTargetsCurrentDirectory(task: UploadTask) {
    if (deps.selectedAccountId.value == null) return false;
    if (String(task.account_id) !== String(deps.selectedAccountId.value)) return false;
    const currentPath = String(deps.currentPath.value || "");
    const parentId = String(task.result?.parent_id ?? task.target_path ?? "");
    return parentId === currentPath || String(task.target_path || "") === currentPath;
  }

  function resolveVisibleDeleteTargets(tasks: UploadTask[]): VisibleDeleteTarget[] {
    const currentFiles = deps.files.value || [];
    type CurrentFile = (typeof currentFiles)[number];
    const fileById = new Map<string, CurrentFile>();
    const filesByName = new Map<string, CurrentFile[]>();
    for (const file of currentFiles) {
      const fileId = String(file.id || "");
      if (fileId) fileById.set(fileId, file);
      const name = String(file.name || "");
      const bucket = filesByName.get(name) || [];
      bucket.push(file);
      filesByName.set(name, bucket);
    }
    const targets: VisibleDeleteTarget[] = [];
    const seenRemoveIds = new Set<string>();
    for (const task of tasks) {
      if (!taskTargetsCurrentDirectory(task)) continue;
      let file: CurrentFile | undefined;
      const fileId = String(task.result?.file_id || "");
      if (fileId) {
        file = fileById.get(fileId);
      }
      if (!file) {
        const matched = filesByName.get(String(task.result?.file_name || task.file_name || "")) || [];
        if (matched.length === 1) {
          file = matched[0];
        }
      }
      if (!file) continue;
      const removeId = String(file.id || file.name || "");
      if (!removeId || seenRemoveIds.has(removeId)) continue;
      seenRemoveIds.add(removeId);
      targets.push({
        taskId: String(task.task_id),
        rowKey: removeId,
        removeId,
      });
    }
    return targets;
  }

  async function pauseUploadTask(task: UploadTask, silent = false) {
    if (isLocalUploadTask(task)) {
      store.pausedLocalUploadTaskIds.add(task.task_id);
      store.localUploadTaskControllers.get(task.task_id)?.abort();
      store.updateLocalUploadTask(task.task_id, { status: "paused", message: "上传已暂停", error: "" });
      return;
    }
    if (isQueuedRemoteResumeTask(task)) {
      store.pendingRemoteResumeTaskIds.delete(String(task.task_id));
      store.patchRemoteUploadTask(task.task_id, { status: "paused", message: getPausedMessage(task), error: "" });
      return;
    }
    try {
      store.pendingRemoteResumeTaskIds.delete(String(task.task_id));
      store.patchRemoteUploadTask(task.task_id, { status: "paused", message: getPausedMessage(task), error: "" });
      await uploadApi.pauseTask(task.task_id);
      await stream.fetchUploadTasks();
    } catch (e) {
      await stream.fetchUploadTasks();
      if (!silent) toast.error(getApiErrorMessage(e, "暂停上传任务失败"));
    }
  }

  async function resumeUploadTask(task: UploadTask, silent = false) {
    if (isLocalUploadTask(task)) {
      const payload = store.localUploadTaskPayloads.get(task.task_id);
      if (!payload?.file) {
        store.updateLocalUploadTask(task.task_id, {
          status: "failed",
          message: "请重新选择文件后再上传",
          error: "缺少本地上传数据，无法继续，请重新选择文件",
        });
        if (!silent) toast.error("页面刷新后本地文件引用已丢失，请重新选择文件");
        return;
      }
      store.pausedLocalUploadTaskIds.delete(task.task_id);
      store.canceledLocalUploadTaskIds.delete(task.task_id);
      store.updateLocalUploadTask(task.task_id, { status: "pending", message: "等待上传", error: "" });
      void dispatcher.startUploadTaskScheduler();
      return;
    }
    store.pendingRemoteResumeTaskIds.add(String(task.task_id));
    store.patchRemoteUploadTask(task.task_id, {
      status: "pending",
      message: getPendingResumeMessage(task),
      error: "",
    });
    void dispatcher.startUploadTaskScheduler();
  }

  async function handleToggleUploadTasks(tasks: UploadTask[]) {
    const unique = [...new Map(tasks.map((task) => [String(task.task_id), task])).values()];
    if (!unique.length) return;
    const resumeMode = unique.every((task) => ["paused", "failed", "canceled"].includes(String(task.status)));
    if (resumeMode) {
      for (const task of unique) {
        await resumeUploadTask(task, true);
      }
      return;
    }

    const remote: UploadTask[] = [];
    for (const task of unique) {
      if (!["pending", "running"].includes(String(task.status))) continue;
      if (isLocalUploadTask(task) || isQueuedRemoteResumeTask(task)) {
        await pauseUploadTask(task, true);
        continue;
      }
      store.pendingRemoteResumeTaskIds.delete(String(task.task_id));
      store.patchRemoteUploadTask(task.task_id, { status: "paused", message: getPausedMessage(task), error: "" });
      remote.push(task);
    }
    if (!remote.length) return;
    try {
      await uploadApi.batchPause(remote.map((task) => task.task_id));
      await stream.fetchUploadTasks();
    } catch (e) {
      await stream.fetchUploadTasks();
      toast.error(getApiErrorMessage(e, "批量暂停上传任务失败"));
    }
  }

  async function handleDeleteUploadTask(
    task: UploadTask,
    opts: { silent?: boolean; skipDialog?: boolean; deleteUploadedFile?: boolean } = {},
  ) {
    if (!task.task_id) return;
    if (isLocalUploadTask(task)) {
      if (!opts.skipDialog) {
        const r = await confirmUploadTaskDelete(task.file_name, task.status === "success");
        if (!r) return;
      }
      store.canceledLocalUploadTaskIds.add(task.task_id);
      store.localUploadTaskControllers.get(task.task_id)?.abort();
      store.removeLocalUploadTask(task.task_id);
      store.localUploadTaskPayloads.delete(task.task_id);
      return;
    }
    const allowDeleteFile = task.status === "success";
    const r = opts.skipDialog
      ? { action: "confirm", checked: Boolean(opts.deleteUploadedFile) }
      : await confirmUploadTaskDelete(task.file_name, allowDeleteFile);
    if (!r) return;
    const deleteUploadedFile = allowDeleteFile && r.checked;
    const visibleTargets = deleteUploadedFile ? resolveVisibleDeleteTargets([task]) : [];
    if (deleteUploadedFile && visibleTargets.length && !opts.silent) {
      deps.markDeletingFiles(visibleTargets.map((item) => item.rowKey));
    }
    store.hiddenUploadTaskKeys.add(getUploadTaskStableKey(task));
    store.removeRemoteUploadTask(task.task_id);
    if (!opts.silent) {
      if (deleteUploadedFile) toast.info(formatCloudDeleteStartToast(1));
      else toast.success(formatRemovedTaskToast(1));
    }
    try {
      await uploadApi.deleteTask(task.task_id, deleteUploadedFile);
      void stream.fetchUploadTasks();
      if (deleteUploadedFile) {
        if (visibleTargets.length) {
          deps.removeFilesLocally(visibleTargets.map((item) => item.removeId));
        }
        const shouldRefreshFiles = taskTargetsCurrentDirectory(task);
        if (shouldRefreshFiles) {
          void deps.loadFiles({ forceRefresh: true, silent: true });
        }
        if (!opts.silent) {
          toast.success(formatCloudDeleteResultToast(1, 0));
        }
      }
    } catch (e) {
      store.hiddenUploadTaskKeys.delete(getUploadTaskStableKey(task));
      if (visibleTargets.length) {
        deps.clearDeletingFiles(visibleTargets.map((item) => item.rowKey));
      }
      void stream.fetchUploadTasks();
      if (!opts.silent) toast.error(getApiErrorMessage(e, "删除上传任务失败"));
    }
  }

  async function handleUploadTaskPrimaryAction(task: UploadTask) {
    if (isQueuedRemoteResumeTask(task)) {
      await pauseUploadTask(task);
      return;
    }
    if (["pending", "running"].includes(task.status)) {
      await pauseUploadTask(task);
      return;
    }
    if (["failed", "paused", "canceled"].includes(task.status)) {
      await resumeUploadTask(task);
      return;
    }
    if (!["success", "skipped"].includes(task.status)) return;
    const account = deps.accounts.value.find((a) => String(a.id) === String(task.account_id));
    if (!account) {
      toast.warning("未找到对应账号");
      return;
    }
    const crumbs = await buildUploadTaskBreadcrumb(account, task, deps.getRootId);
    deps.selectedFilesList.value = [];
    await deps.openDirectory(account.id, crumbs, { forceRefresh: true });
    const openedParentId = String(deps.currentPath.value || "");
    closePanel();
    void refreshOpenedUploadDirectory(task, account.id, openedParentId);
  }

  function uploadResultVisible(task: UploadTask) {
    const fileId = String(task.result?.file_id || "");
    const fileName = String(task.result?.file_name || task.file_name || "");
    if (!fileId && !fileName) return true;
    return deps.files.value.some((file) =>
      (fileId && String(file.id || "") === fileId) ||
      (fileName && String(file.name || "") === fileName),
    );
  }

  async function refreshOpenedUploadDirectory(task: UploadTask, accountId: number, parentId: string) {
    for (const delay of [500, 1200, 2500]) {
      if (uploadResultVisible(task)) return;
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      if (String(deps.selectedAccountId.value) !== String(accountId)) return;
      if (String(deps.currentPath.value || "") !== parentId) return;
      await deps.loadFiles({ forceRefresh: true, silent: true });
    }
  }

  async function handleDeleteUploadTasks(
    tasks: UploadTask[],
    opts: { preferBatchRootDelete?: boolean; folderTaskCount?: number } = {},
  ) {
    if (!tasks.length) return;
    // 同一上传在本地投递态与服务端任务态短暂重叠时，只保留服务端任务，
    // 避免先 abort 本地请求、随后又对同一任务发起一次删除。
    const byStableKey = new Map<string, UploadTask>();
    for (const task of tasks) {
      const key = getUploadTaskStableKey(task) || String(task.task_id);
      const current = byStableKey.get(key);
      if (!current || (isLocalUploadTask(current) && !isLocalUploadTask(task))) {
        byStableKey.set(key, task);
      }
    }
    tasks = [...byStableKey.values()];
    const hasRemote = tasks.some((t) => !isLocalUploadTask(t));
    const remote = tasks.filter((t) => !isLocalUploadTask(t));
    const deleteBatchRoots = Boolean(opts.preferBatchRootDelete) && remote.length > 0 && remote.every((task) =>
      ["success", "failed", "canceled", "skipped"].includes(String(task.status)) &&
      Boolean(task.result?.batch_root_owned) &&
      Boolean(String(task.result?.batch_root_id || "").trim()),
    );
    let deleteUploadedFile = false;
    if (hasRemote) {
      const successCount = tasks.filter((t) => t.status === "success").length;
      const r = await confirmBatchUploadTaskDelete(
        tasks.length,
        successCount,
        deleteBatchRoots ? Number(opts.folderTaskCount || 1) : 0,
      );
      if (!r) return;
      deleteUploadedFile = r.checked;
    }
    for (const task of tasks.filter(isLocalUploadTask)) {
      await handleDeleteUploadTask(task, { silent: true, skipDialog: true });
    }
    if (!hasRemote) {
      toast.success(formatRemovedTaskToast(tasks.length));
      return;
    }
    if (remote.length) {
      const visibleTargets = deleteUploadedFile ? resolveVisibleDeleteTargets(remote) : [];
      const visibleTargetMap = new Map(visibleTargets.map((item) => [item.taskId, item] as const));
      const deleteFileCount = remote.filter((task) => task.status === "success").length;
      remote.forEach((t) => store.hiddenUploadTaskKeys.add(getUploadTaskStableKey(t)));
      if (deleteUploadedFile && (deleteFileCount > 0 || deleteBatchRoots)) {
        if (visibleTargets.length) {
          deps.markDeletingFiles(visibleTargets.map((item) => item.rowKey));
        }
        toast.info(deleteBatchRoots ? "任务已移除，网盘文件夹删除中…" : formatCloudDeleteStartToast(deleteFileCount));
      }
      try {
        const result = await uploadApi.batchDelete(
          remote.map((t) => t.task_id),
          deleteUploadedFile,
          deleteUploadedFile && deleteBatchRoots,
        );
        const failedTaskIds = new Set((result.failed_task_ids || []).map(String));
        const processedTaskIds = remote
          .map((task) => String(task.task_id))
          .filter((taskId) => !failedTaskIds.has(taskId));
        const processedVisibleTargets = processedTaskIds
          .map((taskId) => visibleTargetMap.get(taskId))
          .filter((item): item is VisibleDeleteTarget => Boolean(item));
        const failedVisibleTargets = [...failedTaskIds]
          .map((taskId) => visibleTargetMap.get(taskId))
          .filter((item): item is VisibleDeleteTarget => Boolean(item));
        failedTaskIds.forEach((taskId) => {
          const failedTask = remote.find((task) => String(task.task_id) === taskId);
          if (failedTask) {
            store.hiddenUploadTaskKeys.delete(getUploadTaskStableKey(failedTask));
          }
        });
        if (processedVisibleTargets.length) {
          deps.removeFilesLocally(processedVisibleTargets.map((item) => item.removeId));
        }
        if (failedVisibleTargets.length) {
          deps.clearDeletingFiles(failedVisibleTargets.map((item) => item.rowKey));
        }
        const shouldRefreshFiles = deleteUploadedFile && remote.some((task) => taskTargetsCurrentDirectory(task));
        void stream.fetchUploadTasks();
        if (shouldRefreshFiles) void deps.loadFiles({ forceRefresh: true, silent: true });
        if (!deleteUploadedFile) {
          const failedCount = failedTaskIds.size;
          const successCount = tasks.length - failedCount;
          if (failedCount === 0) toast.success(formatRemovedTaskToast(tasks.length));
          else if (successCount > 0) toast.warning(`已移除 ${successCount} 条上传记录，${failedCount} 条删除失败`);
          else toast.error("上传记录删除失败");
          return;
        }
        const failedCount = failedTaskIds.size;
        const successCount = Math.max(0, deleteFileCount - failedCount);
        if (deleteBatchRoots) {
          if (failedCount === 0) toast.success("网盘文件夹已删除");
          else toast.error("网盘文件夹删除失败");
          return;
        }
        if (failedCount === 0) toast.success(formatCloudDeleteResultToast(successCount, 0));
        else if (successCount > 0) toast.warning(formatCloudDeleteResultToast(successCount, failedCount));
        else toast.error(formatCloudDeleteResultToast(0, failedCount));
      } catch (e) {
        remote.forEach((t) => store.hiddenUploadTaskKeys.delete(getUploadTaskStableKey(t)));
        if (visibleTargets.length) {
          deps.clearDeletingFiles(visibleTargets.map((item) => item.rowKey));
        }
        toast.error(getApiErrorMessage(e, "批量删除失败"));
      }
    }
  }

  return {
    pauseUploadTask,
    resumeUploadTask,
    handleDeleteUploadTask,
    handleDeleteUploadTasks,
    handleUploadTaskPrimaryAction,
    handleToggleUploadTasks,
  };
}

export function useUploadRelayActions(ctx: UploadActionsCtx) {
  async function handleDeleteRelayTasks(ids: string[]) {
    if (!ids.length) return;
    try {
      const relayTaskIds = new Set(ctx.store.relayTasks.value.map((task) => String(task.task_id)));
      const taskIds = ids.map(String).filter((id) => relayTaskIds.has(id));
      if (!taskIds.length) return;
      const result = await uploadApi.batchDelete(taskIds, false);
      for (const taskId of result.deleted_task_ids || []) {
        ctx.store.removeRemoteUploadTask(String(taskId));
      }
      void ctx.stream.fetchUploadTasks();
      if ((result.failed_task_ids || []).length > 0) {
        toast.warning(`已删除 ${result.deleted_task_ids.length} 个跨盘任务，${result.failed_task_ids.length} 个失败`);
        return;
      }
      toast.success("跨盘任务已删除");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "删除跨盘任务失败");
    }
  }

  return { handleDeleteRelayTasks };
}
