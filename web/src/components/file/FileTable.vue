<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, toRef, watch } from "vue";
import type { FileItem } from "@/api/types";
import type { FileRowOperation, SortKey, SortOrder } from "@/types/file-browser";
import { formatSize, formatTime } from "@/utils/format";
import type { DeleteFileHooks } from "@/composables/useFileActions";
import { useFileTableInline } from "@/composables/useFileTableInline";
import FileIcon from "./FileIcon.vue";
import FileTableHeader from "./FileTableHeader.vue";
import FileGridSortMenu from "./FileGridSortMenu.vue";
import FileContextMenu from "./FileContextMenu.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import type { ContextMenuItem } from "@/composables/useFileTableInline";

const props = defineProps<{
  files: FileItem[];
  view: "list" | "grid";
  loading: boolean;
  isAdmin: boolean;
  selectedIds: string[];
  sortKey: SortKey;
  sortOrder: SortOrder;
  sortClass: (key: SortKey) => SortOrder | "";
  createFolderRequest: number;
  rowOperations?: Record<string, FileRowOperation>;
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
  coverExtractEnabled: boolean;
  coverExtractFile: (file: FileItem) => void;
  dragActive?: boolean;
  activeDropTargetId?: string;
  dragUnlockedTargetId?: string;
  dragLockProgress?: number;
  canDropOnFolder?: (file: FileItem) => boolean;
}>();

const INITIAL_LIST_RENDER_COUNT = 200;
const LIST_RENDER_CHUNK_SIZE = 150;

const emit = defineEmits<{
  open: [file: FileItem];
  "update:selectedIds": [ids: string[]];
  "sort-by": [key: SortKey];
  "set-sort": [payload: { key: SortKey; order: SortOrder }];
  "generate-current-directory-strm": [];
  "drag-file-start": [file: FileItem];
  "drag-file-end": [];
  "drag-enter-folder": [file: FileItem];
  "drag-leave-folder": [file: FileItem];
  "drop-on-folder": [file: FileItem];
}>();

const inline = useFileTableInline({
  files: toRef(props, "files"),
  selectedIds: toRef(props, "selectedIds"),
  isAdmin: toRef(props, "isAdmin"),
  loading: toRef(props, "loading"),
  createFolderRequest: toRef(props, "createFolderRequest"),
  externalRowOps: toRef(props, "rowOperations"),
  renameFile: (file, name) => props.renameFile(file, name),
  createFolder: (name) => props.createFolder(name),
  deleteFile: (file, hooks) => props.deleteFile(file, hooks),
  downloadFile: (file) => props.downloadFile(file),
  moveFile: (file) => props.moveFile(file),
  copyFile: (file) => props.copyFile(file),
  batchDeleteFiles: () => props.batchDeleteFiles(),
  batchMoveFiles: () => props.batchMoveFiles(),
  batchCopyFiles: () => props.batchCopyFiles(),
  nameAlignFile: (file) => props.nameAlignFile(file),
  coverExtractEnabled: toRef(props, "coverExtractEnabled"),
  coverExtractFile: (file) => props.coverExtractFile(file),
});

const {
  renameDraft,
  renameComposing,
  inlineCreatingFolder,
  createFolderDraft,
  createFolderSaving,
  createFolderComposing,
  createFolderPendingName,
  contextMenu,
  contextMenuItems,
  emptyColSpan,
  emptyStateText,
  showEmptyRow,
  isInlineRenaming,
  isInlineProcessing,
  getRowOperationText,
  openContextMenu,
  cancelInlineRename,
  submitInlineRename,
  cancelInlineCreateFolder,
  submitInlineCreateFolder,
  handleContextAction,
  closeContextMenu,
} = inline;

function bindRenameInput(el: unknown) {
  inline.renameInputRef.value = el as HTMLInputElement | null;
}

function bindCreateFolderInput(el: unknown) {
  inline.createFolderInputRef.value = el as HTMLInputElement | null;
}

const selectedSet = computed(() => new Set(props.selectedIds));
const selectAll = computed(
  () => props.files.length > 0 && props.selectedIds.length === props.files.length,
);
const selectedCount = computed(() => props.selectedIds.length);
const listVisibleCount = ref(INITIAL_LIST_RENDER_COUNT);
const listLoadMoreSentinel = ref<HTMLTableRowElement | null>(null);
let listLoadMoreObserver: IntersectionObserver | null = null;
const DRAG_PREVIEW_OFFSET_X = 14;
const DRAG_PREVIEW_OFFSET_Y = 26;
const DRAG_ACTIVITY_TIMEOUT_MS = 180;
const dragPreviewLeft = ref(0);
const dragPreviewTop = ref(0);
const dragPreviewVisible = ref(false);
const dragPreviewFile = ref<FileItem | null>(null);
const dragPreviewCount = ref(1);
const dragPreviewSubtitle = ref("");
const dragGhostImageRef = ref<HTMLImageElement | null>(null);
const dragPreviewRef = ref<HTMLElement | null>(null);
const fileListRef = ref<HTMLElement | null>(null);
const dragRowOutlineRect = ref<{ top: number; left: number; width: number; height: number; ready: boolean } | null>(null);
const TRANSPARENT_DRAG_GIF =
  "data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==";
let dragEndCleanupFrame: number | null = null;
let dragActivityCleanupTimer: number | null = null;

const visibleListFiles = computed(() =>
  props.view === "list" ? props.files.slice(0, listVisibleCount.value) : props.files,
);
const hasMoreListFiles = computed(
  () => props.view === "list" && visibleListFiles.value.length < props.files.length,
);
const headerContextMenu = ref({
  open: false,
  x: 0,
  y: 0,
});
const headerContextMenuItems = computed<ContextMenuItem[]>(() =>
  props.isAdmin ? [{ action: "generate-current-directory-strm", label: "生成当前目录 STRM" }] : [],
);

function fileKey(f: FileItem) {
  return f.id || f.name;
}

function closeDirectoryContextMenu() {
  headerContextMenu.value.open = false;
}

function openDirectoryContextMenu(event: MouseEvent) {
  if (!props.isAdmin) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest(".file-row, .file-card, .inline-create-row")) return;
  const items = headerContextMenuItems.value;
  if (!items.length) return;
  closeContextMenu();
  const menuWidth = 188;
  const menuHeight = items.length * 38 + 14;
  headerContextMenu.value = {
    open: true,
    x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
    y: Math.max(8, Math.min(event.clientY, window.innerHeight - menuHeight - 8)),
  };
}

function handleHeaderContextAction(action: string) {
  closeDirectoryContextMenu();
  if (action === "generate-current-directory-strm") {
    emit("generate-current-directory-strm");
  }
}

function expandVisibleListFiles() {
  listVisibleCount.value = Math.min(
    props.files.length,
    listVisibleCount.value + LIST_RENDER_CHUNK_SIZE,
  );
}

function resetVisibleListFiles() {
  listVisibleCount.value = INITIAL_LIST_RENDER_COUNT;
}

function disconnectListLoadMoreObserver() {
  listLoadMoreObserver?.disconnect();
  listLoadMoreObserver = null;
}

function bindListLoadMoreSentinel(el: unknown) {
  listLoadMoreSentinel.value = el instanceof HTMLTableRowElement ? el : null;
  void nextTick(updateListLoadMoreObserver);
}

async function updateListLoadMoreObserver() {
  disconnectListLoadMoreObserver();
  if (!hasMoreListFiles.value || !listLoadMoreSentinel.value) return;
  if (typeof window === "undefined" || typeof window.IntersectionObserver === "undefined") {
    listVisibleCount.value = props.files.length;
    return;
  }
  listLoadMoreObserver = new window.IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      expandVisibleListFiles();
      void nextTick(updateListLoadMoreObserver);
    },
    {
      root: null,
      rootMargin: "240px 0px",
      threshold: 0,
    },
  );
  listLoadMoreObserver.observe(listLoadMoreSentinel.value);
}

function toggleSelection(id: string, checked: boolean) {
  const next = new Set(props.selectedIds);
  if (checked) next.add(id);
  else next.delete(id);
  emit("update:selectedIds", Array.from(next));
}

function toggleSelectAll(checked: boolean) {
  emit("update:selectedIds", checked ? props.files.map(fileKey) : []);
}

function isDroppableFolder(file: FileItem) {
  return Boolean(props.dragActive) && file.is_dir && !isInlineProcessing(file) && (props.canDropOnFolder?.(file) ?? false);
}

function isActiveDropTarget(file: FileItem) {
  return props.activeDropTargetId === file.id;
}

function isUnlockedDropTarget(file: FileItem) {
  return props.dragUnlockedTargetId === file.id;
}

function dragLockProgressStyle() {
  const progress = Math.max(0, Math.min(1, props.dragLockProgress ?? 0));
  const radius = 11.5;
  const circumference = 2 * Math.PI * radius;
  return {
    "--drag-lock-dasharray": `${circumference}`,
    "--drag-lock-dashoffset": `${circumference * (1 - progress)}`,
  };
}

const dragRowOutlineStyle = computed(() => {
  if (!dragRowOutlineRect.value) return undefined;
  return {
    top: `${dragRowOutlineRect.value.top}px`,
    left: `${dragRowOutlineRect.value.left}px`,
    width: `${dragRowOutlineRect.value.width}px`,
    height: `${dragRowOutlineRect.value.height}px`,
  };
});

const dragPreviewStyle = computed(() => ({
  left: `${dragPreviewLeft.value}px`,
  top: `${dragPreviewTop.value}px`,
}));

const dragPreviewLockText = computed(() => {
  if (props.dragUnlockedTargetId) return "松手即可移入";
  if (props.activeDropTargetId) return "悬停片刻解锁";
  return "";
});

const dragPreviewStatusText = computed(() => dragPreviewLockText.value || dragPreviewSubtitle.value);

const dragPreviewShowLock = computed(() => Boolean(props.activeDropTargetId));
const dragPreviewLockIcon = computed(() => (props.dragUnlockedTargetId ? "lock-open" : "lock"));

function updateDragPreviewPosition(event: DragEvent) {
  if (!dragPreviewVisible.value) return;
  if (event.clientX === 0 && event.clientY === 0) return;
  dragPreviewLeft.value = event.clientX - DRAG_PREVIEW_OFFSET_X;
  dragPreviewTop.value = event.clientY - DRAG_PREVIEW_OFFSET_Y;
}

function updateDragRowOutline(event: DragEvent, ready = false) {
  if (props.view !== "list") {
    dragRowOutlineRect.value = null;
    return;
  }
  const row = event.currentTarget as HTMLElement | null;
  const container = fileListRef.value;
  if (!row || !container) return;
  const rowRect = row.getBoundingClientRect();
  const containerRect = container.getBoundingClientRect();
  dragRowOutlineRect.value = {
    top: rowRect.top - containerRect.top + 4,
    left: rowRect.left - containerRect.left + 8,
    width: Math.max(0, rowRect.width - 16),
    height: Math.max(0, rowRect.height - 8),
    ready,
  };
}

function resetDragRowOutline() {
  dragRowOutlineRect.value = null;
}

function getDragSubtitle(file: FileItem, count: number) {
  return count > 1 ? `等 ${count} 个项目` : file.is_dir ? "移动文件夹" : "移动文件";
}

function showCustomDragPreview(file: FileItem, event: DragEvent) {
  const draggingSelected = selectedSet.value.has(fileKey(file));
  const count = draggingSelected && selectedCount.value > 1 ? selectedCount.value : 1;
  if (dragPreviewRef.value) dragPreviewRef.value.style.opacity = "1";
  dragPreviewFile.value = file;
  dragPreviewCount.value = count;
  dragPreviewSubtitle.value = getDragSubtitle(file, count);
  dragPreviewVisible.value = true;
  updateDragPreviewPosition(event);
}

function resetCustomDragPreview() {
  if (dragPreviewRef.value) dragPreviewRef.value.style.opacity = "0";
  dragPreviewVisible.value = false;
  dragPreviewFile.value = null;
  dragPreviewCount.value = 1;
  dragPreviewSubtitle.value = "";
  dragPreviewLeft.value = 0;
  dragPreviewTop.value = 0;
}

function cancelPendingDragEndCleanup() {
  if (dragEndCleanupFrame === null || typeof window === "undefined") return;
  window.cancelAnimationFrame(dragEndCleanupFrame);
  dragEndCleanupFrame = null;
}

function cancelDragActivityCleanup() {
  if (dragActivityCleanupTimer === null || typeof window === "undefined") return;
  window.clearTimeout(dragActivityCleanupTimer);
  dragActivityCleanupTimer = null;
}

// mac 某些失败拖拽会先给异常 drag，再晚一点才给 dragend，这里补一帧兜底清理。
function scheduleDragEndCleanup() {
  if (dragEndCleanupFrame !== null || typeof window === "undefined") return;
  dragEndCleanupFrame = window.requestAnimationFrame(() => {
    dragEndCleanupFrame = null;
    if (!dragPreviewVisible.value) return;
    clearDragArtifacts();
  });
}

// Safari 失败拖拽有时不给明确结束信号，只能靠拖拽“心跳”断掉后主动收卡片。
function markDragActivity() {
  if (!dragPreviewVisible.value || typeof window === "undefined") return;
  cancelDragActivityCleanup();
  dragActivityCleanupTimer = window.setTimeout(() => {
    dragActivityCleanupTimer = null;
    if (!dragPreviewVisible.value) return;
    clearDragArtifacts();
  }, DRAG_ACTIVITY_TIMEOUT_MS);
}

function handleDragStart(event: DragEvent, file: FileItem) {
  if (!props.isAdmin || isInlineProcessing(file) || isInlineRenaming(file)) {
    event.preventDefault();
    return;
  }
  cancelDragActivityCleanup();
  cancelPendingDragEndCleanup();
  event.dataTransfer?.setData("text/plain", file.id || file.name);
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = "move";
    const ghostImage = dragGhostImageRef.value;
    if (ghostImage?.complete) event.dataTransfer.setDragImage(ghostImage, 0, 0);
  }
  showCustomDragPreview(file, event);
  markDragActivity();
  emit("drag-file-start", file);
}

function handleDragMove(event: DragEvent) {
  if (event.clientX === 0 && event.clientY === 0) {
    // 这类 0,0 坐标常见于拖拽会话将结束但 dragend 还没到的瞬间。
    scheduleDragEndCleanup();
    return;
  }
  cancelPendingDragEndCleanup();
  updateDragPreviewPosition(event);
  markDragActivity();
}

function handleDragEnd() {
  resetCustomDragPreview();
  resetDragRowOutline();
  emit("drag-file-end");
}

function handleFolderDragEnter(event: DragEvent, file: FileItem) {
  if (!isDroppableFolder(file)) return;
  event.preventDefault();
  updateDragPreviewPosition(event);
  updateDragRowOutline(event, isUnlockedDropTarget(file));
  markDragActivity();
  emit("drag-enter-folder", file);
}

function handleFolderDragOver(event: DragEvent, file: FileItem) {
  if (!isDroppableFolder(file)) return;
  event.preventDefault();
  updateDragPreviewPosition(event);
  updateDragRowOutline(event, isUnlockedDropTarget(file));
  markDragActivity();
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
  emit("drag-enter-folder", file);
}

function handleFolderDragLeave(event: DragEvent, file: FileItem) {
  if (!props.dragActive) return;
  const currentTarget = event.currentTarget as Node | null;
  const relatedTarget = event.relatedTarget as Node | null;
  if (currentTarget && relatedTarget && currentTarget.contains(relatedTarget)) return;
  resetDragRowOutline();
  emit("drag-leave-folder", file);
}

function handleFolderDrop(event: DragEvent, file: FileItem) {
  if (!props.dragActive) return;
  event.preventDefault();
  resetCustomDragPreview();
  resetDragRowOutline();
  emit("drop-on-folder", file);
}

function onRowClick(event: MouseEvent, file: FileItem) {
  if (!props.isAdmin) {
    emit("open", file);
    return;
  }
  const target = event.target as HTMLElement | null;
  if (target?.closest('input[type="checkbox"]')) return;
  if (target?.closest(".file-name")) {
    emit("open", file);
    return;
  }
  if (target?.closest(".inline-rename-wrap")) return;
  const row = event.currentTarget as HTMLElement | null;
  if (!row) return;
  const clickX = event.clientX - row.getBoundingClientRect().left;
  if (clickX > 70) return;
  toggleSelection(fileKey(file), !selectedSet.value.has(fileKey(file)));
}

watch(
  [() => props.files, () => props.view, () => props.sortKey, () => props.sortOrder],
  async () => {
    resetVisibleListFiles();
    await nextTick();
    void updateListLoadMoreObserver();
  },
);

watch(hasMoreListFiles, async () => {
  await nextTick();
  void updateListLoadMoreObserver();
});

function handleWindowDragOver(event: DragEvent) {
  updateDragPreviewPosition(event);
  markDragActivity();
}

function clearDragArtifacts() {
  cancelDragActivityCleanup();
  cancelPendingDragEndCleanup();
  resetCustomDragPreview();
  resetDragRowOutline();
}

// 用 window 兜底，是为了覆盖非目标目录区域 drop 的失败路径。
function handleWindowDrop() {
  clearDragArtifacts();
}

// 源元素自己的 dragend 不一定稳定冒泡到预期位置，这里统一从窗口收尾。
function handleWindowDragEnd() {
  clearDragArtifacts();
}

onMounted(() => {
  void nextTick(updateListLoadMoreObserver);
  document.addEventListener("keydown", handleHeaderMenuKeydown);
  window.addEventListener("dragover", handleWindowDragOver);
  window.addEventListener("drop", handleWindowDrop);
  window.addEventListener("dragend", handleWindowDragEnd, true);
  window.addEventListener("resize", closeDirectoryContextMenu);
  window.addEventListener("scroll", closeDirectoryContextMenu, true);
});

onUnmounted(() => {
  disconnectListLoadMoreObserver();
  document.removeEventListener("keydown", handleHeaderMenuKeydown);
  window.removeEventListener("dragover", handleWindowDragOver);
  window.removeEventListener("drop", handleWindowDrop);
  window.removeEventListener("dragend", handleWindowDragEnd, true);
  window.removeEventListener("resize", closeDirectoryContextMenu);
  window.removeEventListener("scroll", closeDirectoryContextMenu, true);
});

function handleHeaderMenuKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") closeDirectoryContextMenu();
}
</script>

<template>
  <div
    class="file-list"
    :class="`view-${view}`"
    ref="fileListRef"
    @contextmenu.prevent="openDirectoryContextMenu($event)"
  >
    <div
      v-if="view === 'list' && dragRowOutlineRect"
      class="file-list__drag-row-outline"
      :class="{ 'file-list__drag-row-outline--ready': dragRowOutlineRect.ready }"
      :style="dragRowOutlineStyle"
      aria-hidden="true"
    />
    <table v-if="view === 'list'" class="file-table">
      <FileTableHeader
        :is-admin="isAdmin"
        :selected-count="selectedCount"
        :select-all="selectAll"
        :files-count="files.length"
        :sort-class="sortClass"
        @toggle-select-all="toggleSelectAll"
        @sort-by="emit('sort-by', $event)"
      />
      <tbody>
        <tr v-if="inlineCreatingFolder" class="inline-create-row">
          <td v-if="isAdmin" class="checkbox-col" />
          <td class="name-col">
            <div
              v-if="createFolderSaving"
              class="file-name inline-create-processing"
              @click.stop
              @contextmenu.stop
            >
              <span class="file-icon-wrap"><SvgIcon name="folder" :size="18" /></span>
              <span class="file-label" :title="createFolderPendingName">{{
                createFolderPendingName
              }}</span>
              <span class="inline-delete-status">
                <span class="inline-rename-spinner" aria-label="正在创建" />
                创建中
              </span>
            </div>
            <div v-else class="inline-rename-wrap inline-create-wrap" @click.stop @contextmenu.stop>
              <span class="file-icon-wrap"><SvgIcon name="folder" :size="18" /></span>
              <input
                :ref="bindCreateFolderInput"
                v-model="createFolderDraft"
                class="inline-rename-input"
                placeholder="输入文件夹名称"
                maxlength="100"
                @compositionstart="createFolderComposing = true"
                @compositionend="createFolderComposing = false"
                @keydown.enter="!createFolderComposing && submitInlineCreateFolder()"
                @keydown.esc.prevent="cancelInlineCreateFolder()"
                @blur="submitInlineCreateFolder()"
              />
              <button
                type="button"
                class="folder-inline-btn confirm"
                title="确认"
                @mousedown.prevent
                @click="submitInlineCreateFolder()"
              >
                ✓
              </button>
              <button
                type="button"
                class="folder-inline-btn cancel"
                title="取消"
                @mousedown.prevent
                @click="cancelInlineCreateFolder()"
              >
                ×
              </button>
            </div>
          </td>
          <td class="size-col">-</td>
          <td class="time-col">-</td>
        </tr>

        <tr v-if="showEmptyRow">
          <td :colspan="emptyColSpan" class="state state--empty-cell">{{ emptyStateText }}</td>
        </tr>

        <tr
          v-for="(f, index) in visibleListFiles"
          v-if="!showEmptyRow"
          :key="fileKey(f)"
          class="file-row"
          :class="{
            processing: isInlineProcessing(f),
            'drag-target': isActiveDropTarget(f),
            'drag-target-unlocked': isUnlockedDropTarget(f),
          }"
          :draggable="isAdmin && !isInlineProcessing(f) && !isInlineRenaming(f)"
          @click="onRowClick($event, f)"
          @contextmenu.prevent.stop="openContextMenu($event, f)"
          @dragstart="handleDragStart($event, f)"
          @drag="handleDragMove($event)"
          @dragend="handleDragEnd"
          @dragenter="handleFolderDragEnter($event, f)"
          @dragover="handleFolderDragOver($event, f)"
          @dragleave="handleFolderDragLeave($event, f)"
          @drop="handleFolderDrop($event, f)"
        >
          <td v-if="isAdmin" class="checkbox-col" @click.stop>
            <input
              :id="`file-checkbox-${index}`"
              type="checkbox"
              :checked="selectedSet.has(fileKey(f))"
              @change="toggleSelection(fileKey(f), ($event.target as HTMLInputElement).checked)"
            />
          </td>
          <td class="name-col">
            <div
              v-if="isInlineRenaming(f)"
              class="inline-rename-wrap"
              @click.stop
              @contextmenu.stop
            >
              <span class="file-icon-wrap"><FileIcon :file="f" :size="18" /></span>
              <input
                :ref="bindRenameInput"
                v-model="renameDraft"
                class="inline-rename-input"
                @compositionstart="renameComposing = true"
                @compositionend="renameComposing = false"
                @keydown.enter="!renameComposing && submitInlineRename()"
                @keydown.esc.prevent="cancelInlineRename()"
                @blur="submitInlineRename()"
              />
              <button
                type="button"
                class="folder-inline-btn confirm"
                title="确认"
                @mousedown.prevent
                @click="submitInlineRename()"
              >
                ✓
              </button>
              <button
                type="button"
                class="folder-inline-btn cancel"
                title="取消"
                @mousedown.prevent
                @click="cancelInlineRename()"
              >
                ×
              </button>
            </div>
            <div v-else class="file-name" @click.stop="emit('open', f)">
              <span class="file-icon-wrap"><FileIcon :file="f" :size="18" /></span>
              <span class="file-text">
                <span class="file-label" :title="f.name">{{ f.name }}</span>
                <span class="file-mobile-meta">
                  {{ formatTime(f.mod_time) }}<template v-if="!f.is_dir"> · {{ formatSize(f.size, f.is_dir) }}</template>
                </span>
              </span>
              <span v-if="isInlineProcessing(f)" class="inline-delete-status">
                <span class="inline-rename-spinner" :aria-label="getRowOperationText(f)" />
                {{ getRowOperationText(f) }}
              </span>
            </div>
          </td>
          <td class="size-col">{{ formatSize(f.size, f.is_dir) }}</td>
          <td class="time-col">{{ formatTime(f.mod_time) }}</td>
        </tr>
        <tr
          v-if="hasMoreListFiles && !showEmptyRow"
          :ref="bindListLoadMoreSentinel"
          aria-hidden="true"
          class="file-list__load-more-row"
        >
          <td :colspan="emptyColSpan" class="file-list__load-more-sentinel" />
        </tr>
      </tbody>
    </table>

    <div v-else class="file-grid-wrapper">
      <div class="file-grid-toolbar">
        <div class="file-grid-toolbar-left">
          <label v-if="isAdmin" class="grid-select-all">
            <input
              type="checkbox"
              :checked="selectAll"
              :disabled="files.length === 0"
              @change="toggleSelectAll(($event.target as HTMLInputElement).checked)"
            />
            <span>{{ selectedCount > 0 ? `已选中 ${selectedCount} 项` : "全选" }}</span>
          </label>
          <span v-else class="grid-count">共 {{ files.length }} 项</span>
        </div>
        <FileGridSortMenu
          :sort-key="sortKey"
          :sort-order="sortOrder"
          @set-sort="emit('set-sort', $event)"
        />
      </div>

      <div v-if="showEmptyRow && !inlineCreatingFolder" class="grid-state">
        <template v-if="!loading">
          <SvgIcon name="folder" :size="40" />
        </template>
        <p>{{ emptyStateText }}</p>
      </div>

      <div v-else class="file-grid">
        <article v-if="inlineCreatingFolder" class="file-card file-card-inline-create">
          <div
            v-if="createFolderSaving"
            class="file-card-main inline-create-processing"
            @click.stop
            @contextmenu.stop
          >
            <span class="file-card-icon"><SvgIcon name="folder" :size="40" /></span>
            <span class="file-card-name" :title="createFolderPendingName">{{
              createFolderPendingName
            }}</span>
            <span class="inline-delete-status">
              <span class="inline-rename-spinner" aria-label="正在创建" />
              创建中
            </span>
          </div>
          <div v-else class="file-card-main file-card-rename" @click.stop @contextmenu.stop>
            <span class="file-card-icon"><SvgIcon name="folder" :size="40" /></span>
            <div class="inline-rename-wrap inline-create-wrap">
              <input
                :ref="bindCreateFolderInput"
                v-model="createFolderDraft"
                class="inline-rename-input grid-rename-input"
                placeholder="输入文件夹名称"
                maxlength="100"
                @compositionstart="createFolderComposing = true"
                @compositionend="createFolderComposing = false"
                @keydown.enter="!createFolderComposing && submitInlineCreateFolder()"
                @keydown.esc.prevent="cancelInlineCreateFolder()"
                @blur="submitInlineCreateFolder()"
              />
              <button
                type="button"
                class="folder-inline-btn confirm"
                @mousedown.prevent
                @click="submitInlineCreateFolder()"
              >
                ✓
              </button>
              <button
                type="button"
                class="folder-inline-btn cancel"
                @mousedown.prevent
                @click="cancelInlineCreateFolder()"
              >
                ×
              </button>
            </div>
          </div>
        </article>

        <article
          v-for="(f, index) in files"
          :key="fileKey(f)"
          class="file-card"
          :class="{
            selected: selectedSet.has(fileKey(f)),
            processing: isInlineProcessing(f),
            'drag-target': isActiveDropTarget(f),
            'drag-target-unlocked': isUnlockedDropTarget(f),
          }"
          :draggable="isAdmin && !isInlineProcessing(f) && !isInlineRenaming(f)"
          @contextmenu.prevent.stop="openContextMenu($event, f)"
          @dragstart="handleDragStart($event, f)"
          @drag="handleDragMove($event)"
          @dragend="handleDragEnd"
          @dragenter="handleFolderDragEnter($event, f)"
          @dragover="handleFolderDragOver($event, f)"
          @dragleave="handleFolderDragLeave($event, f)"
          @drop="handleFolderDrop($event, f)"
        >
          <label
            v-if="isAdmin"
            class="file-card-checkbox"
            :for="`grid-file-checkbox-${index}`"
            @click.stop
          >
            <input
              :id="`grid-file-checkbox-${index}`"
              type="checkbox"
              :checked="selectedSet.has(fileKey(f))"
              @change="toggleSelection(fileKey(f), ($event.target as HTMLInputElement).checked)"
            />
          </label>

          <div
            v-if="isInlineRenaming(f)"
            class="file-card-main file-card-rename"
            @click.stop
            @contextmenu.stop
          >
            <span class="file-card-icon"><FileIcon :file="f" :size="40" /></span>
            <input
              ref="renameInputRef"
              v-model="renameDraft"
              class="inline-rename-input grid-rename-input"
              @compositionstart="renameComposing = true"
              @compositionend="renameComposing = false"
              @keydown.enter="!renameComposing && submitInlineRename()"
              @keydown.esc.prevent="cancelInlineRename()"
              @blur="submitInlineRename()"
            />
            <div class="inline-rename-wrap">
              <button
                type="button"
                class="folder-inline-btn confirm"
                @mousedown.prevent
                @click="submitInlineRename()"
              >
                ✓
              </button>
              <button
                type="button"
                class="folder-inline-btn cancel"
                @mousedown.prevent
                @click="cancelInlineRename()"
              >
                ×
              </button>
            </div>
          </div>

          <button v-else type="button" class="file-card-main" @click="emit('open', f)">
            <span class="file-card-icon"><FileIcon :file="f" :size="40" /></span>
            <span class="file-card-name" :title="f.name">{{ f.name }}</span>
            <span v-if="isInlineProcessing(f)" class="inline-delete-status">
              <span class="inline-rename-spinner" :aria-label="getRowOperationText(f)" />
              {{ getRowOperationText(f) }}
            </span>
            <span v-else class="file-card-time">{{ formatTime(f.mod_time) }}</span>
          </button>
        </article>
      </div>
    </div>

    <div
      v-if="dragPreviewVisible && dragPreviewFile"
      ref="dragPreviewRef"
      class="drag-preview"
      :style="dragPreviewStyle"
      aria-hidden="true"
    >
      <span class="drag-preview__icon-stack">
        <span class="drag-preview__icon-shell">
          <FileIcon :file="dragPreviewFile" :size="18" />
        </span>
        <span v-if="dragPreviewCount > 1" class="drag-preview__badge">{{ dragPreviewCount }}</span>
      </span>
      <span class="drag-preview__body">
        <span class="drag-preview__title" :title="dragPreviewFile.name">{{ dragPreviewFile.name }}</span>
        <span class="drag-preview__subtitle">{{ dragPreviewStatusText }}</span>
      </span>
      <span
        v-if="dragPreviewShowLock"
        class="drag-preview__lock drag-lock"
        :class="{ 'drag-lock--ready': dragPreviewLockText === '松手即可移入' }"
      >
        <span class="drag-lock__ring" :style="dragLockProgressStyle()">
          <svg class="drag-lock__ring-svg" viewBox="0 0 28 28" aria-hidden="true">
            <circle class="drag-lock__ring-track" cx="14" cy="14" r="11.5" />
            <circle class="drag-lock__ring-progress" cx="14" cy="14" r="11.5" />
          </svg>
          <span class="drag-lock__core">
            <SvgIcon :name="dragPreviewLockIcon" :size="14" />
          </span>
        </span>
      </span>
    </div>

    <img
      ref="dragGhostImageRef"
      class="drag-ghost-image"
      :src="TRANSPARENT_DRAG_GIF"
      alt=""
      aria-hidden="true"
    />

    <FileContextMenu
      :open="contextMenu.open"
      :x="contextMenu.x"
      :y="contextMenu.y"
      :items="contextMenuItems"
      @action="handleContextAction"
      @close="closeContextMenu"
    />
    <FileContextMenu
      :open="headerContextMenu.open"
      :x="headerContextMenu.x"
      :y="headerContextMenu.y"
      :items="headerContextMenuItems"
      @action="handleHeaderContextAction"
      @close="closeDirectoryContextMenu"
    />
  </div>
</template>

<style scoped>
.file-list {
  overflow-x: auto;
  position: relative;
  border-radius: 0 0 12px 12px;
}

.file-list.view-grid {
  overflow: visible;
}

.state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 60px 20px;
  color: var(--text-muted);
}

.state--empty p,
.state--empty-cell {
  margin: 0;
  font-style: italic;
  text-align: center;
  color: var(--text-muted);
}

.file-row {
  cursor: pointer;
  transition: background 0.15s ease;
}

.file-row:hover {
  background: var(--surface-sunken);
}

.file-row.drag-target {
  position: relative;
  background:
    linear-gradient(90deg, color-mix(in srgb, var(--brand) 14%, var(--surface)) 0%, transparent 100%),
    var(--info-soft);
  box-shadow: 0 8px 22px color-mix(in srgb, var(--brand) 14%, transparent);
  transform: translateZ(0);
}

.file-row.drag-target-unlocked {
  position: relative;
  background:
    linear-gradient(90deg, color-mix(in srgb, #22c55e 12%, var(--surface)) 0%, transparent 100%),
    color-mix(in srgb, #22c55e 10%, var(--surface));
  box-shadow: 0 8px 22px color-mix(in srgb, #22c55e 16%, transparent);
  transform: translateZ(0);
}

.file-list__drag-row-outline {
  position: absolute;
  border-radius: 12px;
  border: 1px dashed color-mix(in srgb, var(--brand) 48%, transparent);
  pointer-events: none;
  z-index: 1;
}

.file-list__drag-row-outline--ready {
  border-color: color-mix(in srgb, #22c55e 58%, transparent);
}

.file-list__load-more-row {
  pointer-events: none;
}

.file-list__load-more-sentinel {
  height: 1px;
  padding: 0;
  border-bottom: none;
  background: transparent;
}

.name-col {
  position: relative;
}

.file-name {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 14px;
  color: var(--text-regular);
  min-width: 0;
  position: relative;
}

.file-icon-wrap {
  line-height: 0;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
}

.file-label {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
}

.file-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.file-mobile-meta {
  display: none;
  margin-top: 2px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-grid-wrapper {
  padding: 0 0 16px;
}

.file-grid-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-soft);
}

.grid-select-all {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-regular);
  cursor: pointer;
  user-select: none;
}

.grid-count {
  font-size: 13px;
  color: var(--text-muted);
}

.file-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(142px, 1fr));
  gap: 14px 12px;
  padding: 16px;
}

.file-card {
  position: relative;
  min-width: 0;
  border-radius: 14px;
}

.file-card-main {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 18px 10px 14px;
  border: none;
  border-radius: 14px;
  background: transparent;
  cursor: pointer;
  text-align: center;
  transition: background-color 0.18s ease;
  position: relative;
}

.file-card:hover .file-card-main,
.file-card.selected .file-card-main {
  background: var(--surface-sunken);
}

.file-card.selected .file-card-main {
  background: var(--info-soft);
}

.file-card.drag-target .file-card-main {
  background:
    radial-gradient(circle at top right, color-mix(in srgb, var(--brand) 16%, transparent), transparent 55%),
    var(--info-soft);
  box-shadow: 0 14px 26px color-mix(in srgb, var(--brand) 16%, transparent);
  transform: translateY(-2px) scale(1.01);
}

.file-card.drag-target-unlocked .file-card-main {
  background:
    radial-gradient(circle at top right, color-mix(in srgb, #22c55e 18%, transparent), transparent 55%),
    color-mix(in srgb, #22c55e 11%, var(--surface));
  box-shadow: 0 14px 26px color-mix(in srgb, #22c55e 20%, transparent);
}

.file-card-icon {
  width: 56px;
  height: 56px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 0;
}

.file-card-name {
  width: 100%;
  color: var(--text);
  font-size: 15px;
  font-weight: 500;
  line-height: 1.35;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-card-time {
  width: 100%;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-card-checkbox {
  position: absolute;
  top: 8px;
  left: 8px;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.18s ease;
  z-index: 2;
  display: inline-flex;
  align-items: center;
}

.file-card:hover .file-card-checkbox,
.file-card.selected .file-card-checkbox {
  opacity: 1;
  pointer-events: auto;
}

.file-card-inline-create .file-card-main {
  background: var(--surface-sunken);
}

.drag-preview {
  position: fixed;
  width: 240px;
  height: 54px;
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 0 14px;
  border: 1px solid var(--border-soft);
  border-radius: 16px;
  background: var(--surface);
  box-shadow: 0 16px 32px color-mix(in srgb, rgb(15 23 42) 14%, transparent);
  pointer-events: none;
  z-index: 30;
}

.drag-preview__icon-stack {
  position: relative;
  width: 28px;
  height: 28px;
  flex-shrink: 0;
}

.drag-preview__badge {
  position: absolute;
  top: -5px;
  right: -7px;
  min-width: 14px;
  height: 14px;
  padding: 0 4px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--brand);
  color: #fff;
  box-shadow: 0 0 0 2px var(--surface);
  font-size: 9px;
  font-weight: 700;
  line-height: 1;
}

.drag-preview__icon-shell {
  width: 100%;
  height: 100%;
  position: relative;
  width: 28px;
  height: 28px;
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--surface);
  flex-shrink: 0;
  line-height: 0;
}

.drag-preview__body {
  min-width: 0;
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 2px;
}

.drag-preview__title,
.drag-preview__subtitle {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.drag-preview__title {
  padding-right: 2px;
  color: var(--text);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.25;
}

.drag-preview__subtitle {
  padding-right: 2px;
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 600;
  line-height: 1.25;
}

.drag-preview__lock {
  width: 28px;
  justify-content: center;
  margin-left: 2px;
}

.drag-ghost-image {
  position: fixed;
  left: -9999px;
  top: -9999px;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.drag-lock {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 0;
  pointer-events: none;
  flex-shrink: 0;
}

.drag-lock__ring {
  position: relative;
  width: 28px;
  height: 28px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: color-mix(in srgb, var(--brand) 82%, white);
}

.drag-lock__ring-svg {
  position: absolute;
  inset: 0;
  transform: rotate(-90deg);
  overflow: visible;
}

.drag-lock__ring-track,
.drag-lock__ring-progress {
  fill: none;
  stroke-width: 2;
}

.drag-lock__ring-track {
  stroke: color-mix(in srgb, var(--border) 72%, transparent);
}

.drag-lock__ring-progress {
  stroke: currentColor;
  stroke-linecap: round;
  stroke-dasharray: var(--drag-lock-dasharray, 75.4);
  stroke-dashoffset: var(--drag-lock-dashoffset, 75.4);
}

.drag-lock__core {
  position: relative;
  z-index: 1;
  width: 20px;
  height: 20px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--surface) 78%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--border) 28%, transparent);
  color: var(--brand);
}

.drag-lock--ready .drag-lock__ring {
  color: #22c55e;
}

.drag-lock--ready .drag-lock__core {
  color: #16a34a;
}

@media (max-width: 768px) {
  .file-list {
    overflow-x: hidden;
  }

  .file-name {
    align-items: flex-start;
  }

  .file-mobile-meta {
    display: block;
  }

  .drag-preview {
    width: min(240px, calc(100vw - 20px));
  }

  .drag-lock {
    gap: 7px;
  }
}
</style>
