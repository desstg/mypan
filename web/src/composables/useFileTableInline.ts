import { computed, nextTick, onMounted, onUnmounted, ref, watch, type Ref } from "vue";
import type { FileItem } from "@/api/types";
import { renameSelectionEnd } from "@/utils/fileName";
import type { DeleteFileHooks } from "@/composables/useFileActions";
import { rowOperationText, type FileRowOperation } from "@/types/file-browser";

export type ContextMenuItem = {
  action: string;
  label: string;
  danger?: boolean;
};

export function useFileTableInline(options: {
  files: Ref<FileItem[]>;
  selectedIds: Ref<string[]>;
  isAdmin: Ref<boolean>;
  loading: Ref<boolean>;
  createFolderRequest: Ref<number>;
  // 外部（批量删除/移动/复制）下发的行内操作状态，与内部重命名/单删状态合并展示。
  externalRowOps?: Ref<Record<string, FileRowOperation> | undefined>;
  renameFile: (file: FileItem, newName: string) => Promise<boolean>;
  createFolder: (name: string) => Promise<boolean>;
  deleteFile: (file: FileItem, hooks?: DeleteFileHooks) => Promise<boolean>;
  downloadFile: (file: FileItem) => void;
  moveFile: (file: FileItem) => void;
  copyFile: (file: FileItem) => void;
  batchDeleteFiles: () => void;
  batchMoveFiles: () => void;
  batchCopyFiles: () => void;
  nameAlignFile: (file: FileItem) => void;
  coverExtractEnabled: Ref<boolean>;
  coverExtractFile: (file: FileItem) => void;
}) {
  const renameInputRef = ref<HTMLInputElement | null>(null);
  const createFolderInputRef = ref<HTMLInputElement | null>(null);

  const renamingId = ref<string | null>(null);
  const renameDraft = ref("");
  const renameSaving = ref(false);
  const renameComposing = ref(false);

  const inlineCreatingFolder = ref(false);
  const createFolderDraft = ref("");
  const createFolderSaving = ref(false);
  const createFolderComposing = ref(false);

  const activeRowOp = ref<{ key: string; op: FileRowOperation } | null>(null);
  const deleteSaving = ref(false);
  const createFolderPendingName = ref("");

  const contextMenu = ref({
    open: false,
    x: 0,
    y: 0,
    file: null as FileItem | null,
  });

  const emptyColSpan = computed(() => (options.isAdmin.value ? 4 : 3));

  const emptyStateText = computed(() => {
    if (inlineCreatingFolder.value) return "";
    if (options.loading.value) return "加载中…";
    if (!options.files.value.length) return "此目录为空";
    return "";
  });

  const showEmptyRow = computed(
    () => !!emptyStateText.value && !inlineCreatingFolder.value,
  );

  const contextMenuItems = computed((): ContextMenuItem[] => {
    const file = contextMenu.value.file;
    if (!file || !options.isAdmin.value) return [];
    return contextMenuItemsFor(file);
  });

  function fileKey(f: FileItem) {
    return f.id || f.name;
  }

  function isInlineRenaming(file: FileItem) {
    return String(fileKey(file)) === String(renamingId.value);
  }

  function getRowOperation(file: FileItem): FileRowOperation | null {
    const key = String(fileKey(file));
    if (activeRowOp.value?.key === key) return activeRowOp.value.op;
    return options.externalRowOps?.value?.[key] ?? null;
  }

  function getRowOperationText(file: FileItem): string {
    const op = getRowOperation(file);
    return op ? rowOperationText[op] : "";
  }

  function isInlineProcessing(file: FileItem) {
    return getRowOperation(file) !== null;
  }

  async function focusRenameInput(file: FileItem) {
    await nextTick();
    const input = renameInputRef.value;
    if (!input) return;
    input.focus();
    const end = renameSelectionEnd(file.name, file.is_dir);
    input.setSelectionRange(0, end);
  }

  async function focusCreateFolderInput() {
    await nextTick();
    createFolderInputRef.value?.focus();
  }

  function closeContextMenu() {
    contextMenu.value.open = false;
    contextMenu.value.file = null;
  }

  function openContextMenu(event: MouseEvent, file: FileItem) {
    if (!options.isAdmin.value || renameSaving.value || deleteSaving.value) return;
    if (isInlineRenaming(file)) return;

    const items = contextMenuItemsFor(file);
    if (!items.length) return;

    const menuWidth = 156;
    const menuHeight = items.length * 38 + 14;
    contextMenu.value = {
      open: true,
      x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
      y: Math.max(8, Math.min(event.clientY, window.innerHeight - menuHeight - 8)),
      file,
    };
  }

  function contextMenuItemsFor(file: FileItem): ContextMenuItem[] {
    if (!options.isAdmin.value) return [];
    const items: ContextMenuItem[] = [];
    if (!file.is_dir) items.push({ action: "download", label: "下载" });
    if (options.coverExtractEnabled.value && !file.is_dir && /\.(mp4|mkv|mov|webm)$/i.test(file.name)) {
      items.push({ action: "cover-extract", label: "生成视频海报" });
    }
    if (!file.is_dir && options.files.value.filter((item) => !item.is_dir).length >= 3) {
      items.push({ action: "name-align", label: "命名对齐" });
    }
    const useBatchActions = options.selectedIds.value.length > 1 && options.selectedIds.value.includes(fileKey(file));
    items.push(
      { action: "rename", label: "重命名" },
      { action: useBatchActions ? "batch-delete" : "delete", label: useBatchActions ? "批量删除" : "删除", danger: true },
      { action: useBatchActions ? "batch-move" : "move", label: useBatchActions ? "批量移动" : "移动到" },
      { action: useBatchActions ? "batch-copy" : "copy", label: useBatchActions ? "批量复制" : "复制到" },
    );
    return items;
  }

  async function startInlineRename(file: FileItem) {
    if (!options.isAdmin.value || renameSaving.value) return;
    renamingId.value = fileKey(file);
    renameDraft.value = file.name;
    closeContextMenu();
    await focusRenameInput(file);
  }

  function cancelInlineRename() {
    if (renameSaving.value) return;
    renamingId.value = null;
    renameDraft.value = "";
    renameComposing.value = false;
  }

  async function submitInlineRename() {
    if (!renamingId.value || renameSaving.value) return;
    const file = options.files.value.find((f) => fileKey(f) === renamingId.value);
    if (!file) {
      cancelInlineRename();
      return;
    }

    const newName = renameDraft.value.trim();
    if (!newName || newName === file.name) {
      cancelInlineRename();
      return;
    }

    const key = fileKey(file);
    renamingId.value = null;
    renameDraft.value = "";
    activeRowOp.value = { key, op: "rename" };
    renameSaving.value = true;
    const ok = await options.renameFile(file, newName);
    renameSaving.value = false;
    activeRowOp.value = null;
    if (ok) {
      renameComposing.value = false;
      return;
    }
    renamingId.value = key;
    renameDraft.value = newName;
    await focusRenameInput(file);
  }

  async function startInlineCreateFolder() {
    if (!options.isAdmin.value || createFolderSaving.value) return;
    cancelInlineRename();
    closeContextMenu();
    inlineCreatingFolder.value = true;
    createFolderDraft.value = "";
    await focusCreateFolderInput();
  }

  function cancelInlineCreateFolder() {
    if (createFolderSaving.value) return;
    inlineCreatingFolder.value = false;
    createFolderDraft.value = "";
    createFolderComposing.value = false;
  }

  async function submitInlineCreateFolder() {
    if (!inlineCreatingFolder.value || createFolderSaving.value) return;
    const name = createFolderDraft.value.trim();
    if (!name) {
      cancelInlineCreateFolder();
      return;
    }

    createFolderPendingName.value = name;
    createFolderSaving.value = true;
    const ok = await options.createFolder(name);
    createFolderSaving.value = false;
    createFolderPendingName.value = "";
    if (ok) cancelInlineCreateFolder();
    else await focusCreateFolderInput();
  }

  async function startInlineDelete(file: FileItem) {
    if (!options.isAdmin.value || deleteSaving.value) return;
    closeContextMenu();
    cancelInlineRename();
    const key = fileKey(file);
    await options.deleteFile(file, {
      onRequestStart: () => {
        activeRowOp.value = { key, op: "delete" };
        deleteSaving.value = true;
      },
      onRequestEnd: () => {
        deleteSaving.value = false;
        activeRowOp.value = null;
      },
    });
  }

  function handleContextAction(action: string) {
    const file = contextMenu.value.file;
    closeContextMenu();
    if (!file) return;

    if (action === "download") options.downloadFile(file);
    if (action === "name-align") options.nameAlignFile(file);
    if (action === "cover-extract") options.coverExtractFile(file);
    if (action === "rename") void startInlineRename(file);
    if (action === "delete") void startInlineDelete(file);
    if (action === "move") options.moveFile(file);
    if (action === "copy") options.copyFile(file);
    if (action === "batch-delete") options.batchDeleteFiles();
    if (action === "batch-move") options.batchMoveFiles();
    if (action === "batch-copy") options.batchCopyFiles();
  }

  function handleClickOutside() {
    closeContextMenu();
  }

  function handleContextKeydown(event: KeyboardEvent) {
    if (event.key === "Escape") closeContextMenu();
  }

  watch(
    () => options.createFolderRequest.value,
    (next, prev) => {
      if (next && next !== prev) void startInlineCreateFolder();
    },
  );

  onMounted(() => {
    document.addEventListener("click", handleClickOutside);
    document.addEventListener("keydown", handleContextKeydown);
    window.addEventListener("resize", closeContextMenu);
    window.addEventListener("scroll", closeContextMenu, true);
  });

  onUnmounted(() => {
    document.removeEventListener("click", handleClickOutside);
    document.removeEventListener("keydown", handleContextKeydown);
    window.removeEventListener("resize", closeContextMenu);
    window.removeEventListener("scroll", closeContextMenu, true);
  });

  return {
    renameInputRef,
    createFolderInputRef,
    renamingId,
    renameDraft,
    renameSaving,
    renameComposing,
    inlineCreatingFolder,
    createFolderDraft,
    createFolderSaving,
    createFolderComposing,
    contextMenu,
    contextMenuItems,
    emptyColSpan,
    emptyStateText,
    showEmptyRow,
    createFolderPendingName,
    isInlineRenaming,
    isInlineProcessing,
    getRowOperation,
    getRowOperationText,
    openContextMenu,
    cancelInlineRename,
    submitInlineRename,
    cancelInlineCreateFolder,
    submitInlineCreateFolder,
    handleContextAction,
    closeContextMenu,
  };
}
