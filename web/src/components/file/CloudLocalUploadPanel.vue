<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  localUploadApi,
  type LocalUploadEntry,
  type LocalUploadMapping,
} from "@/api/cloudTools";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import FileIcon from "@/components/file/FileIcon.vue";
import BreadcrumbNav from "@/components/file/BreadcrumbNav.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import type { FileItem } from "@/api/types";
import type { Crumb } from "@/stores/browser";

const props = defineProps<{
  open: boolean;
  accountId: number | null;
  targetPath: string;
  targetDisplayPath: string;
  uploadKind?: "file" | "folder";
  onEnqueueFiles?: (files: File[]) => void;
  onEnqueueFolderFiles?: (files: File[]) => void | Promise<void>;
  onTasksCreated?: () => void;
  onFolderUploadAccepted?: (key: string, count: number) => void;
  onMarkCurrentDirRefresh?: () => void;
}>();

const emit = defineEmits<{
  close: [];
  "pick-file": [];
  "pick-folder": [];
}>();

const activeTab = ref<"terminal" | "local">("terminal");
const mappings = ref<LocalUploadMapping[]>([]);
const mappingIndex = ref(0);
const browsePath = ref("");
const entries = ref<LocalUploadEntry[]>([]);
const selected = ref<Set<string>>(new Set());
const loading = ref(false);
const uploading = ref(false);
const dragOver = ref(false);
const terminalFiles = ref<string[]>([]);

const visibleEntries = computed(() =>
  props.uploadKind === "folder"
    ? entries.value.filter((e) => e.is_dir)
    : entries.value,
);

const crumbs = computed<Crumb[]>(() => {
  const parts = browsePath.value.split("/").filter(Boolean);
  const name = mappings.value[mappingIndex.value]?.name || "目录";
  return [
    { id: "0", name },
    ...parts.map((p, i) => ({ id: String(i + 1), name: p })),
  ];
});

const mappingOptions = computed(() =>
  mappings.value.map((m, i) => ({ value: i, label: m.name })),
);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    activeTab.value = "terminal";
    selected.value = new Set();
    browsePath.value = "";
    terminalFiles.value = [];
    try {
      const cfg = await localUploadApi.getConfig();
      mappings.value = cfg.mappings;
      if (mappings.value.length > 0) {
        // 保留上次选中的映射，但需同步浏览其目录并防止映射列表变化导致越界，
        // 否则会出现下拉显示目录 A、内容却是目录 B 的错位。
        if (mappingIndex.value >= mappings.value.length) {
          mappingIndex.value = 0;
        }
        await loadBrowse(mappingIndex.value);
      }
    } catch (e) {
      toast.error(getApiErrorMessage(e, "加载本机上传配置失败"));
    }
  },
);

async function loadBrowse(idx: number) {
  if (!mappings.value[idx]) return;
  loading.value = true;
  try {
    const res = await localUploadApi.browse(mappings.value[idx].name, browsePath.value);
    entries.value = res.items;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "读取服务器目录失败"));
    entries.value = [];
  } finally {
    loading.value = false;
  }
}

function selectMapping() {
  selected.value = new Set();
  browsePath.value = "";
  void loadBrowse(mappingIndex.value);
}

function onMappingChange(value: string | number | boolean) {
  mappingIndex.value = Number(value);
  selectMapping();
}

function openEntry(entry: LocalUploadEntry) {
  if (!entry.is_dir) return;
  browsePath.value = entry.rel_path;
  selected.value = new Set();
  void loadBrowse(mappingIndex.value);
}

function jumpCrumb(index: number) {
  const parts = browsePath.value.split("/").filter(Boolean);
  browsePath.value = parts.slice(0, index).join("/");
  selected.value = new Set();
  void loadBrowse(mappingIndex.value);
}

function toggleItem(entry: LocalUploadEntry, checked: boolean) {
  if (checked) selected.value.add(entry.rel_path);
  else selected.value.delete(entry.rel_path);
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Escape") emit("close");
}

onMounted(() => window.addEventListener("keydown", onKeydown));
onUnmounted(() => window.removeEventListener("keydown", onKeydown));

function selectableEntry(entry: LocalUploadEntry): boolean {
  return props.uploadKind === "folder" ? entry.is_dir : !entry.is_dir;
}

function fmtSize(size: number) {
  if (size >= 1073741824) return (size / 1073741824).toFixed(1) + " GB";
  if (size >= 1048576) return (size / 1048576).toFixed(0) + " MB";
  if (size >= 1024) return (size / 1024).toFixed(0) + " KB";
  return size + " B";
}

type DroppedFileEntry = {
  name: string;
  isFile: boolean;
  isDirectory: boolean;
  file?: (success: (file: File) => void, error?: (reason: unknown) => void) => void;
  createReader?: () => {
    readEntries: (success: (entries: DroppedFileEntry[]) => void, error?: (reason: unknown) => void) => void;
  };
};

function readDroppedFile(entry: DroppedFileEntry) {
  return new Promise<File>((resolve, reject) => {
    if (!entry.file) {
      reject(new Error(`无法读取文件：${entry.name}`));
      return;
    }
    entry.file(resolve, reject);
  });
}

function readDroppedEntries(entry: DroppedFileEntry) {
  return new Promise<DroppedFileEntry[]>((resolve, reject) => {
    const reader = entry.createReader?.();
    if (!reader) {
      reject(new Error(`无法读取文件夹：${entry.name}`));
      return;
    }
    const all: DroppedFileEntry[] = [];
    const readNext = () => {
      reader.readEntries((entries) => {
        if (!entries.length) {
          resolve(all);
          return;
        }
        all.push(...entries);
        readNext();
      }, reject);
    };
    readNext();
  });
}

async function collectDroppedFolderFiles(dataTransfer: DataTransfer) {
  const entries = Array.from(dataTransfer.items || [])
    .map((item): DroppedFileEntry | null => {
      const getEntry = (item as unknown as { webkitGetAsEntry?: () => DroppedFileEntry | null })
        .webkitGetAsEntry;
      return getEntry?.call(item) || null;
    })
    .filter((entry): entry is DroppedFileEntry => entry !== null);
  const roots = entries.filter((entry) => entry.isDirectory);
  if (roots.length !== 1 || entries.length !== 1) {
    throw new Error("一次只能拖入一个文件夹");
  }

  const files: File[] = [];
  const walk = async (entry: DroppedFileEntry, parentPath: string) => {
    if (entry.isFile) {
      const file = await readDroppedFile(entry);
      Object.defineProperty(file, "litepanRelativePath", {
        configurable: true,
        value: `${parentPath}${entry.name}`,
      });
      files.push(file);
      return;
    }
    if (!entry.isDirectory) return;
    const children = await readDroppedEntries(entry);
    const childPath = `${parentPath}${entry.name}/`;
    await Promise.all(children.map((child) => walk(child, childPath)));
  };
  await walk(roots[0], "");
  return files;
}

async function onDrop(e: DragEvent) {
  dragOver.value = false;
  const dataTransfer = e.dataTransfer;
  if (!dataTransfer) return;
  if (props.uploadKind !== "folder") {
    const files = [...dataTransfer.files];
    if (!files.length) return;
    terminalFiles.value = files.map((file) => file.name);
    props.onEnqueueFiles?.(files);
    emit("close");
    return;
  }
  uploading.value = true;
  try {
    const files = await collectDroppedFolderFiles(dataTransfer);
    if (!files.length) throw new Error("所选文件夹中没有可上传的文件，空文件夹暂不支持");
    terminalFiles.value = files.map((file) => file.name);
    await props.onEnqueueFolderFiles?.(files);
    emit("close");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "读取拖入的文件夹失败"));
  } finally {
    uploading.value = false;
  }
}

function pickFromTerminal() {
  if (props.uploadKind === "folder") emit("pick-folder");
  else emit("pick-file");
}

function toFileItem(entry: LocalUploadEntry): FileItem {
  return { id: entry.rel_path, name: entry.name, size: entry.size, is_dir: entry.is_dir };
}

function fmtTime(ts: number) {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

async function startLocalUpload() {
  if (props.accountId == null) {
    toast.error("请先选择账号");
    return;
  }
  if (selected.value.size === 0) {
    toast.error("请先勾选要上传的文件");
    return;
  }
  const m = mappings.value[mappingIndex.value];
  if (!m) return;
  uploading.value = true;
  const isFolder = props.uploadKind === "folder";
  const batchKey = "local-" + Date.now();
  const items = [...selected.value].map((rel) => ({
    rel_path: rel,
    is_dir: entries.value.find((e) => e.rel_path === rel)?.is_dir ?? false,
  }));
  try {
    const res = await localUploadApi.upload({
      account_id: props.accountId,
      mapping: m.name,
      target_path: props.targetPath,
      target_display_path: props.targetDisplayPath,
      conflict_policy: "overwrite",
      client_task_id: isFolder ? batchKey : "",
      items,
    });
    toast.success(`已受理 ${res.count} 个文件，任务将陆续显示在上传面板`);
    if (isFolder) {
      props.onFolderUploadAccepted?.(batchKey, res.count);
    } else {
      props.onMarkCurrentDirRefresh?.();
    }
    selected.value = new Set();
    props.onTasksCreated?.();
    emit("close");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "创建上传任务失败"));
  } finally {
    uploading.value = false;
  }
}
</script>

<template>
  <div v-if="open" class="local-upload-overlay">
    <aside class="local-upload-drawer">
      <div class="local-upload-drawer__head">
        <div class="local-upload-seg">
          <button
            type="button"
            class="local-upload-seg__item"
            :class="{ 'is-active': activeTab === 'terminal' }"
            @click="activeTab = 'terminal'"
          >
            从访问机上传
          </button>
          <button
            type="button"
            class="local-upload-seg__item"
            :class="{ 'is-active': activeTab === 'local' }"
            @click="activeTab = 'local'"
          >
            从服务器上传
          </button>
        </div>
        <button type="button" class="local-upload-drawer__close" title="关闭" @click="emit('close')">
          ✕
        </button>
      </div>

      <div v-if="activeTab === 'terminal'" class="local-upload-body local-upload-body--terminal">
        <div
          class="local-upload-dropzone"
          :class="{ 'is-drag': dragOver }"
          @click="pickFromTerminal"
          @dragenter.prevent="dragOver = true"
          @dragover.prevent="dragOver = true"
          @dragleave.prevent="dragOver = false"
          @drop.prevent="onDrop"
        >
          <div class="local-upload-dropzone__icon">⬆️</div>
          <div class="local-upload-dropzone__text">拖放文件到此处，或点击选择</div>
          <div class="local-upload-dropzone__hint">
            {{ uploadKind === "folder" ? "将选择文件夹并保留目录结构" : "支持多文件" }}
          </div>
        </div>
        <div v-if="terminalFiles.length > 0" class="local-upload-pick-summary">
          已选 {{ terminalFiles.length }} 个文件：{{ terminalFiles.slice(0, 3).join("、")
          }}{{ terminalFiles.length > 3 ? "…" : "" }}（将直接进入上传任务）
        </div>
      </div>

      <div v-else class="local-upload-body">
        <div class="local-upload-mapping-row">
          <span>服务器目录</span>
          <div class="local-upload-select-wrap">
            <AppSelect :model-value="mappingIndex" :options="mappingOptions" @update:model-value="onMappingChange" />
          </div>
          <BreadcrumbNav :items="crumbs" compact @navigate="jumpCrumb" />
        </div>
        <div class="local-upload-list">
          <div class="local-upload-table-head">
            <span class="local-upload-table-head__name">名称</span>
            <span class="local-upload-table-head__size">大小</span>
            <span class="local-upload-table-head__time">修改时间</span>
          </div>
          <div v-if="loading" class="local-upload-empty">加载中…</div>
          <div v-else-if="visibleEntries.length === 0" class="local-upload-empty">
            {{ uploadKind === "folder" ? "当前目录没有子文件夹" : "当前目录没有文件" }}
          </div>
          <div v-for="entry in visibleEntries" :key="entry.rel_path" class="local-upload-row" :class="{ 'is-dir': entry.is_dir }">
            <label v-if="selectableEntry(entry)" class="local-upload-row__check">
              <input
                type="checkbox"
                :checked="selected.has(entry.rel_path)"
                @change="(e) => toggleItem(entry, (e.target as HTMLInputElement).checked)"
              />
            </label>
            <span v-else class="local-upload-row__check" />
            <span class="local-upload-row__icon"><FileIcon :file="toFileItem(entry)" :size="18" /></span>
            <span
              class="local-upload-row__name"
              :title="entry.name"
              @click="openEntry(entry)"
            >
              {{ entry.name }}
            </span>
            <span class="local-upload-row__size">{{ entry.is_dir ? "—" : fmtSize(entry.size) }}</span>
            <span class="local-upload-row__time">{{ fmtTime(entry.mtime) }}</span>
          </div>
        </div>
        <div class="local-upload-footer">
          <span class="local-upload-pick-summary">
            {{
              uploadKind === "folder"
                ? `已选 ${selected.size} 个文件夹`
                : `已选 ${selected.size} 个文件`
            }}
          </span>
          <AppButton variant="primary" :disabled="uploading || selected.size === 0" @click="startLocalUpload">
            {{ uploading ? "创建任务中…" : "开始上传" }}
          </AppButton>
        </div>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.local-upload-overlay {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-modal);
}
.local-upload-drawer {
  width: min(720px, 100vw);
  height: min(78vh, 720px);
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  display: flex;
  flex-direction: column;
  border-radius: var(--radius-lg);
  overflow: hidden;
  animation: local-upload-in 0.25s ease;
}
@keyframes local-upload-in {
  from { transform: translateY(16px); opacity: 0.6; }
  to { transform: translateY(0); opacity: 1; }
}
.local-upload-drawer__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}
.local-upload-drawer__close {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 15px;
  line-height: 1;
  padding: 6px 8px;
  border-radius: var(--radius-sm);
}
.local-upload-drawer__close:hover {
  background: var(--border-soft);
  color: var(--text);
}
.local-upload-seg {
  display: flex;
  gap: 2px;
  background: var(--surface-sunken);
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-pill);
  padding: 3px;
}
.local-upload-seg__item {
  border: none;
  background: transparent;
  padding: 6px 14px;
  border-radius: var(--radius-pill);
  color: var(--text-muted);
  font-weight: 500;
  font-size: 13px;
  transition: var(--transition);
}
.local-upload-seg__item.is-active {
  background: var(--surface);
  color: var(--text);
  font-weight: 600;
  box-shadow: var(--shadow-soft);
}
.local-upload-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 18px 20px 20px;
}
.local-upload-body--terminal {
  overflow: hidden;
}
.local-upload-dropzone {
  border: 2px dashed var(--border);
  border-radius: var(--radius-lg);
  padding: 32px 20px;
  text-align: center;
  color: var(--text-muted);
  cursor: pointer;
  transition: var(--transition);
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.local-upload-dropzone.is-drag {
  border-color: var(--brand);
  background: var(--tab-active-bg);
  color: var(--accent-text);
}
.local-upload-dropzone__icon { font-size: 28px; }
.local-upload-dropzone__text { margin-top: 6px; font-size: 13px; }
.local-upload-dropzone__hint { font-size: 12px; margin-top: 4px; opacity: 0.8; }
.local-upload-pick-summary {
  font-size: 12px;
  color: var(--text-muted);
}
.local-upload-mapping-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 8px;
}
.local-upload-select-wrap {
  width: 140px;
  flex-shrink: 0;
}
.local-upload-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  margin-bottom: 10px;
}
.local-upload-table-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  background: var(--surface-sunken);
  border-bottom: 1px solid var(--border-soft);
  font-size: 12px;
  color: var(--text-muted);
}
.local-upload-table-head__name { flex: 1; min-width: 0; }
.local-upload-table-head__size { width: 90px; text-align: right; }
.local-upload-table-head__time { width: 140px; text-align: right; }
.local-upload-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--border-soft);
  font-size: 13px;
  transition: background 0.15s ease;
}
.local-upload-row:last-child { border-bottom: none; }
.local-upload-row:hover { background: var(--surface-sunken); }
.local-upload-row__check { display: inline-flex; align-items: center; }
.local-upload-row__check input { accent-color: var(--brand); margin: 0; }
.local-upload-row__icon {
  flex-shrink: 0;
  line-height: 0;
  display: inline-flex;
}
.local-upload-row__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  color: var(--text-regular);
}
.local-upload-row.is-dir .local-upload-row__name { cursor: pointer; }
.local-upload-row__size { color: var(--text-muted); font-size: 12px; width: 90px; text-align: right; flex-shrink: 0; }
.local-upload-row__time { color: var(--text-muted); font-size: 12px; width: 140px; text-align: right; flex-shrink: 0; }
.local-upload-empty {
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
  padding: 22px 0;
}
.local-upload-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-top: 10px;
}
</style>
