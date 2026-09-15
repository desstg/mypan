import { showConfirm } from "@/composables/useConfirm";
import type { ConfirmDialogResult } from "@/types/confirm";

function nullOnCancel<T>(promise: Promise<T>): Promise<T | null> {
  return promise.catch(() => null);
}

export function confirmUploadNotice(): Promise<ConfirmDialogResult | null> {
  return nullOnCancel(
    showConfirm({
      title: "上传说明",
      preset: "upload-notice",
      checkboxLabel: "不再提示",
      checkboxDefault: localStorage.getItem("litepan:index:upload-server-transfer-notice-hidden") === "true",
      showCancel: false,
      danger: false,
      actions: [{ id: "confirm", label: "我知道了，继续", variant: "primary" }],
    }),
  );
}

export function confirmUploadConflict(fileName: string): Promise<ConfirmDialogResult | null> {
  return nullOnCancel(
    showConfirm({
      title: "提示",
      icon: "warning",
      preset: "upload-conflict",
      presetData: { fileName },
      checkboxLabel: "应用到本次全部",
      danger: false,
      actions: [
        { id: "skip", label: "跳过", variant: "cancel" },
        { id: "overwrite", label: "覆盖", variant: "cancel" },
        { id: "rename", label: "保留两者", variant: "primary" },
      ],
    }),
  );
}

export function confirmUploadTaskDelete(
  taskName: string,
  allowDeleteUploadedFile: boolean,
): Promise<ConfirmDialogResult | null> {
  return nullOnCancel(
    showConfirm({
      title: "删除任务",
      message: `确定要删除任务「${taskName}」吗？`,
      icon: "trash",
      confirmText: "删除",
      hint: allowDeleteUploadedFile ? "如需同时删除网盘中的文件，请勾选下方选项。" : undefined,
      checkboxLabel: allowDeleteUploadedFile ? "同时删除文件" : undefined,
    }),
  );
}

export function confirmBatchUploadTaskDelete(
  taskCount: number,
  successCount: number,
  folderTaskCount = 0,
): Promise<ConfirmDialogResult | null> {
  const deletingFolders = folderTaskCount > 0;
  return nullOnCancel(
    showConfirm({
      title: deletingFolders ? "删除文件夹任务" : "批量删除任务",
      message: deletingFolders
        ? `确定要删除 ${folderTaskCount} 个文件夹任务吗？`
        : `确定要删除 ${taskCount} 个任务吗？`,
      icon: "trash",
      confirmText: "删除",
      checkboxLabel: successCount > 0 || deletingFolders
        ? (deletingFolders ? "同时删除网盘中的文件夹" : "同时删除文件")
        : undefined,
    }),
  );
}
