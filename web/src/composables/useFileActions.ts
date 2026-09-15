import { reactive, ref, type Ref } from "vue";
import type { FileItem } from "@/api/types";
import { filesApi } from "@/api/files";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import { confirm } from "@/composables/useConfirm";
import { fileKey } from "@/composables/useFileSelection";
import { FileNameError, validateFileName } from "@/utils/fileName";
import type { ConfirmIcon } from "@/types/confirm";
import type { FileRowOperation } from "@/types/file-browser";

export type DeleteMode = "recycle" | "permanent";

export type DeleteFileHooks = {
  onRequestStart?: () => void | Promise<void>;
  onRequestEnd?: () => void | Promise<void>;
};

export function buildSelectedCountText(selectedFiles: FileItem[]) {
  const count = selectedFiles.length;
  if (!count) return "0 个项目";

  const hasFiles = selectedFiles.some((f) => !f.is_dir);
  const hasFolders = selectedFiles.some((f) => f.is_dir);

  if (hasFiles && hasFolders) return `${count} 个文件和文件夹`;
  if (hasFolders) return `${count} 个文件夹`;
  if (hasFiles) return `${count} 个文件`;
  return `${count} 个项目`;
}

function buildBatchDeleteConfirm(selectedFiles: FileItem[], mode: DeleteMode) {
  const countText = buildSelectedCountText(selectedFiles);
  if (mode === "permanent") {
    return {
      title: "确认批量彻底删除",
      message: `确定要彻底删除选中的${countText}吗？删除后无法恢复！`,
      icon: "trash" as ConfirmIcon,
      confirmText: "彻底删除",
    };
  }
  return {
    title: "确认批量删除",
    message: `确定要将选中的${countText}移入回收站吗？`,
    icon: "warning" as ConfirmIcon,
    confirmText: "删除",
  };
}

function deleteSuccessMessage(mode: DeleteMode, count = 1): string {
  if (mode === "permanent") {
    return count > 1 ? `已彻底删除 ${count} 个项目` : "已彻底删除";
  }
  return count > 1 ? `已删除到回收站 ${count} 个项目` : "已删除到回收站";
}

function buildSingleDeleteConfirm(file: FileItem, mode: DeleteMode) {
  const kind = file.is_dir ? "文件夹" : "文件";
  if (mode === "permanent") {
    return {
      title: "确认彻底删除",
      message: `确定要彻底删除${kind} "${file.name}" 吗？删除后无法恢复！`,
      icon: "trash" as ConfirmIcon,
      confirmText: "彻底删除",
    };
  }
  return {
    title: "确认删除",
    message: `确定要将${kind} "${file.name}" 移入回收站吗？`,
    icon: "warning" as ConfirmIcon,
    confirmText: "删除",
  };
}

export function useFileActions(options: {
  getAccountId: () => number | null;
  getParentId: () => string;
  files: Ref<FileItem[]>;
  selectedIds: Ref<string[]>;
  selectedFiles: Ref<FileItem[]>;
  getDeleteMode?: () => DeleteMode;
  removeFilesLocally: (ids: string[]) => void;
  renameFileLocally: (fileId: string, newName: string) => void;
  addFolderLocally: (folder: FileItem) => void;
  // 重新拉取当前目录。移动/复制后需强制刷新（写操作缓存失效是异步的）。
  reloadFiles: (opts?: { forceRefresh?: boolean }) => Promise<void>;
}) {
  const acting = ref(false);

  // 行内「处理中」状态：fileKey -> 操作类型。批量删除、移动、复制据此让被操作的行显示转圈+文案。
  const rowOps = reactive<Record<string, FileRowOperation>>({});
  function markRowOps(keys: string[], op: FileRowOperation) {
    for (const k of keys) rowOps[String(k)] = op;
  }
  function unmarkRowOps(keys: string[]) {
    for (const k of keys) delete rowOps[String(k)];
  }

  function markExternalDeleteRows(keys: string[]) {
    markRowOps(keys, "delete");
  }

  function clearExternalDeleteRows(keys: string[]) {
    unmarkRowOps(keys);
  }

  // 移动/复制目标选择：由外层 FolderPickerModal 绑定该状态。
  const transfer = reactive({
    open: false,
    action: "move" as "move" | "copy",
    fileIds: [] as string[],
    excluded: [] as string[],
    count: 0,
  });

  function openTransfer(action: "move" | "copy", targets: FileItem[]) {
    if (!targets.length) return;
    transfer.action = action;
    transfer.fileIds = targets.map((f) => f.id);
    transfer.excluded = targets.filter((f) => f.is_dir).map((f) => f.id);
    transfer.count = targets.length;
    transfer.open = true;
  }

  function cancelTransfer() {
    transfer.open = false;
  }

  async function performTransfer(
    action: "move" | "copy",
    ids: string[],
    rowKeys: string[],
    targetParentId: string,
  ) {
    const accountId = options.getAccountId();
    if (accountId == null) return false;
    const sourceParentId = options.getParentId();
    if (!ids.length) return false;
    if (action === "move" && targetParentId === sourceParentId) {
      toast.info("目标目录与当前目录相同");
      return false;
    }

    const payload = {
      account_id: accountId,
      file_ids: ids,
      target_parent_id: targetParentId,
      source_parent_id: sourceParentId,
    };
    const movedSet = new Set(rowKeys.map(String));
    markRowOps(rowKeys, action);

    acting.value = true;
    let ok = false;
    try {
      if (action === "move") {
        await filesApi.moveFiles(payload);
      } else {
        await filesApi.copyFiles(payload);
      }
      ok = true;
    } catch (e) {
      toast.error(getApiErrorMessage(e, action === "move" ? "移动失败" : "复制失败"));
    } finally {
      acting.value = false;
    }

    if (!ok) {
      unmarkRowOps(rowKeys);
      return false;
    }

    if (action === "move") {
      options.selectedIds.value = options.selectedIds.value.filter((id) => !movedSet.has(String(id)));
    }
    unmarkRowOps(rowKeys);
    toast.success(action === "move" ? `已移动 ${ids.length} 个项目` : `已复制 ${ids.length} 个项目`);

    void options.reloadFiles({ forceRefresh: true });
    return true;
  }

  async function confirmTransfer(target: { parentId: string }) {
    const action = transfer.action;
    const ids = [...transfer.fileIds];
    const rowKeys = ids.map(String);

    // 先关弹窗，让用户看到列表里被操作的行转圈+「移动中/复制中」。
    transfer.open = false;
    await performTransfer(action, ids, rowKeys, target.parentId);
  }

  async function moveTargetsToParent(targets: FileItem[], targetParentId: string) {
    if (!targets.length) return false;
    const ids = targets.map((file) => file.id);
    const rowKeys = targets.map((file) => fileKey(file));
    return performTransfer("move", ids, rowKeys, targetParentId);
  }

  function validateFolderName(name: string): string {
    const trimmed = validateFileName(name);
    const dup = options.files.value.find((f) => f.name.toLowerCase() === trimmed.toLowerCase());
    if (dup) {
      throw new FileNameError(dup.is_dir ? "当前目录已存在同名文件夹" : "当前目录已存在同名文件");
    }
    return trimmed;
  }

  function validateRenameTarget(file: FileItem, name: string): string {
    const trimmed = validateFileName(name);
    if (trimmed === file.name) throw new FileNameError("新名称不能与原名称相同");
    const dup = options.files.value.find(
      (f) => f.id !== file.id && f.name.toLowerCase() === trimmed.toLowerCase(),
    );
    if (dup) {
      throw new FileNameError(dup.is_dir ? "当前目录已存在同名文件夹" : "当前目录已存在同名文件");
    }
    return trimmed;
  }

  function notifyNameError(e: unknown): boolean {
    if (e instanceof FileNameError) {
      toast.info(e.message);
      return true;
    }
    return false;
  }

  async function renameFile(file: FileItem, newName: string): Promise<boolean> {
    const accountId = options.getAccountId();
    if (accountId == null) return false;
    try {
      const trimmed = validateRenameTarget(file, newName);
      await filesApi.renameFile({
        account_id: accountId,
        file_id: file.id,
        new_name: trimmed,
        parent_id: options.getParentId(),
      });
      options.renameFileLocally(file.id, trimmed);
      toast.success("重命名成功");
      return true;
    } catch (e) {
      if (notifyNameError(e)) return false;
      toast.error(getApiErrorMessage(e, "重命名失败"));
      return false;
    }
  }

  async function createFolder(name: string): Promise<boolean> {
    const accountId = options.getAccountId();
    if (accountId == null) {
      toast.info("请先选择一个账号");
      return false;
    }
    try {
      const trimmed = validateFolderName(name);
      const res = await filesApi.createFolder({
        account_id: accountId,
        parent_id: options.getParentId(),
        name: trimmed,
      });
      options.addFolderLocally({
        id: res.folder_id,
        name: res.folder_name,
        is_dir: true,
        size: 0,
      });
      toast.success(`文件夹 "${trimmed}" 创建成功`);
      return true;
    } catch (e) {
      if (notifyNameError(e)) return false;
      toast.error(getApiErrorMessage(e, "创建文件夹失败"));
      return false;
    }
  }

  async function deleteFile(file: FileItem, hooks?: DeleteFileHooks): Promise<boolean> {
    const accountId = options.getAccountId();
    if (accountId == null) return false;

    const modal = buildSingleDeleteConfirm(file, options.getDeleteMode?.() ?? "recycle");
    try {
      await confirm({ ...modal, danger: true, size: "md" });
    } catch {
      return false;
    }

    await hooks?.onRequestStart?.();

    let success = false;
    try {
      await filesApi.deleteFiles({
        account_id: accountId,
        file_ids: [file.id],
        parent_id: options.getParentId(),
      });
      success = true;
    } catch (e) {
      toast.error(getApiErrorMessage(e, "删除失败"));
    }

    if (success) {
      const key = fileKey(file);
      options.selectedIds.value = options.selectedIds.value.filter((id) => id !== key);
      options.removeFilesLocally([key]);
      toast.success(deleteSuccessMessage(options.getDeleteMode?.() ?? "recycle"));
    }

    await hooks?.onRequestEnd?.();
    return success;
  }

  function downloadFile(file: FileItem) {
    const accountId = options.getAccountId();
    if (accountId == null || file.is_dir) return;
    const url = filesApi.downloadURL(accountId, file.id, file.name);
    const link = document.createElement("a");
    link.href = url;
    link.download = file.name;
    link.style.display = "none";
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    toast.success(`开始下载：${file.name}`);
  }

  function requestSingleMove(file: FileItem) {
    openTransfer("move", [file]);
  }

  function requestSingleCopy(file: FileItem) {
    openTransfer("copy", [file]);
  }

  async function requestBatchDelete() {
    if (!options.selectedIds.value.length) {
      toast.info("请先选择要删除的文件");
      return;
    }

    const deleteMode = options.getDeleteMode?.() ?? "recycle";
    const modal = buildBatchDeleteConfirm(options.selectedFiles.value, deleteMode);

    try {
      await confirm({ ...modal, danger: true, size: "md" });
    } catch {
      return;
    }

    const accountId = options.getAccountId();
    if (accountId == null) return;

    const ids = options.selectedIds.value.map((id) => {
      const hit = options.selectedFiles.value.find((f) => fileKey(f) === id);
      return hit?.id || id;
    });
    const rowKeys = options.selectedFiles.value.map((f) => fileKey(f));

    markRowOps(rowKeys, "delete");
    acting.value = true;
    try {
      await filesApi.deleteFiles({
        account_id: accountId,
        file_ids: ids,
        parent_id: options.getParentId(),
      });
      options.removeFilesLocally(ids.map(String));
      options.selectedIds.value = [];
      toast.success(deleteSuccessMessage(deleteMode, ids.length));
    } catch (e) {
      toast.error(getApiErrorMessage(e, "批量删除失败"));
    } finally {
      unmarkRowOps(rowKeys);
      acting.value = false;
    }
  }

  function requestBatchMove() {
    if (!options.selectedIds.value.length) {
      toast.info("请先选择要移动的文件");
      return;
    }
    openTransfer("move", options.selectedFiles.value);
  }

  function requestBatchCopy() {
    if (!options.selectedIds.value.length) {
      toast.info("请先选择要复制的文件");
      return;
    }
    openTransfer("copy", options.selectedFiles.value);
  }

  return {
    acting,
    rowOps,
    markExternalDeleteRows,
    clearExternalDeleteRows,
    transfer,
    confirmTransfer,
    cancelTransfer,
    renameFile,
    createFolder,
    deleteFile,
    downloadFile,
    requestSingleMove,
    requestSingleCopy,
    moveTargetsToParent,
    requestBatchDelete,
    requestBatchMove,
    requestBatchCopy,
  };
}
