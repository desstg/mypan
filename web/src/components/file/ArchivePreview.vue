<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import type { Entry, ZipReader as ZipReaderType } from "@zip.js/zip.js";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import PreviewHeader from "./PreviewHeader.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const props = defineProps<{
  accountId: number;
  file: FileItem;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const MAX_ENTRIES = 20_000;

interface ArchiveNode {
  key: string;
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  compressedSize: number;
  modifiedAt: Date | null;
  encrypted: boolean;
  children: ArchiveNode[];
}

const loading = ref(true);
const error = ref("");
const root = ref<ArchiveNode>(createDirectory("", ""));
const currentPath = ref<string[]>([]);
const query = ref("");
const totalFiles = ref(0);
const totalDirectories = ref(0);
let zipReader: ZipReaderType<unknown> | null = null;

const currentDirectory = computed(() => findDirectory(currentPath.value) || root.value);
const allNodes = computed(() => flattenNodes(root.value.children));
const visibleItems = computed(() => {
  const keyword = query.value.trim().toLocaleLowerCase();
  const source = keyword
    ? allNodes.value.filter((item) => item.path.toLocaleLowerCase().includes(keyword))
    : currentDirectory.value.children;
  return [...source].sort((left, right) => {
    if (left.isDir !== right.isDir) return left.isDir ? -1 : 1;
    return left.name.localeCompare(right.name, "zh-CN", { numeric: true, sensitivity: "base" });
  });
});

const statusText = computed(
  () => `ZIP 压缩包 · ${totalFiles.value.toLocaleString()} 个文件 · ${totalDirectories.value.toLocaleString()} 个文件夹 · ${formatSize(props.file.size)}`,
);

function createDirectory(name: string, path: string): ArchiveNode {
  return {
    key: `directory:${path}`,
    name,
    path,
    isDir: true,
    size: 0,
    compressedSize: 0,
    modifiedAt: null,
    encrypted: false,
    children: [],
  };
}

function sanitizePath(value: string) {
  const parts: string[] = [];
  for (const part of value.replaceAll("\\", "/").split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") {
      parts.pop();
      continue;
    }
    parts.push(part);
  }
  return parts;
}

function ensureDirectory(parts: string[]) {
  let parent = root.value;
  const accumulated: string[] = [];
  for (const part of parts) {
    accumulated.push(part);
    let child = parent.children.find((item) => item.isDir && item.name === part);
    if (!child) {
      child = createDirectory(part, accumulated.join("/"));
      parent.children.push(child);
      totalDirectories.value += 1;
    }
    parent = child;
  }
  return parent;
}

function addEntry(entry: Entry) {
  const parts = sanitizePath(entry.filename);
  if (!parts.length) return;
  if (entry.directory) {
    const directory = ensureDirectory(parts);
    directory.modifiedAt = validDate(entry.lastModDate);
    return;
  }
  const name = parts.pop();
  if (!name) return;
  const parent = ensureDirectory(parts);
  const path = [...parts, name].join("/");
  parent.children.push({
    key: `file:${path}:${entry.offset}`,
    name,
    path,
    isDir: false,
    size: Number(entry.uncompressedSize) || 0,
    compressedSize: Number(entry.compressedSize) || 0,
    modifiedAt: validDate(entry.lastModDate),
    encrypted: Boolean(entry.encrypted),
    children: [],
  });
  totalFiles.value += 1;
}

function validDate(value: Date) {
  return value instanceof Date && Number.isFinite(value.getTime()) ? value : null;
}

function flattenNodes(nodes: ArchiveNode[]): ArchiveNode[] {
  const result: ArchiveNode[] = [];
  for (const node of nodes) {
    result.push(node);
    if (node.isDir) result.push(...flattenNodes(node.children));
  }
  return result;
}

function findDirectory(parts: string[]) {
  let directory = root.value;
  for (const part of parts) {
    const next = directory.children.find((item) => item.isDir && item.name === part);
    if (!next) return null;
    directory = next;
  }
  return directory;
}

function openItem(item: ArchiveNode) {
  if (!item.isDir) return;
  currentPath.value = item.path.split("/").filter(Boolean);
  query.value = "";
}

function openPath(index: number) {
  currentPath.value = index < 0 ? [] : currentPath.value.slice(0, index + 1);
  query.value = "";
}

function formatDate(value: Date | null) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(value);
}

function compressionRatio(item: ArchiveNode) {
  if (item.isDir || item.size <= 0) return "—";
  const ratio = Math.max(0, Math.min(100, (1 - item.compressedSize / item.size) * 100));
  return `${ratio.toFixed(0)}%`;
}

function errorMessage(reason: unknown) {
  const message = reason instanceof Error ? reason.message : String(reason || "");
  if (message === "ARCHIVE_TOO_MANY_ENTRIES") {
    return `压缩包超过 ${MAX_ENTRIES.toLocaleString()} 个条目，请下载后查看`;
  }
  if (/HTTP Range|Range requests|HTTP error 416/i.test(message)) {
    return "当前网盘不支持分段读取该压缩包，请下载后查看";
  }
  if (/central directory|signature|format|EOCDR|ZIP64/i.test(message)) {
    return "文件不是有效的 ZIP 压缩包，或压缩包已损坏";
  }
  return message || "压缩包目录读取失败";
}

async function loadArchive() {
  loading.value = true;
  error.value = "";
  try {
    const { HttpRangeReader, ZipReader } = await import("@zip.js/zip.js");
    const url = filesApi.proxyPreviewURL(props.accountId, props.file.id, props.file.name);
    const readerOptions = {
      combineSizeEocd: true,
      forceRangeRequests: true,
      credentials: "same-origin",
    } as ConstructorParameters<typeof HttpRangeReader>[1] & RequestInit;
    zipReader = new ZipReader(new HttpRangeReader(url, readerOptions));
    const entries = await zipReader.getEntries({
      decodeText: (bytes, encoding) => {
        if (encoding.toLowerCase() === "utf-8") return undefined;
        try {
          return new TextDecoder("gb18030").decode(bytes);
        } catch {
          return undefined;
        }
      },
      onprogress: async (_progress, total) => {
        if (total > MAX_ENTRIES) throw new Error("ARCHIVE_TOO_MANY_ENTRIES");
      },
    });
    for (const entry of entries) addEntry(entry);
  } catch (reason) {
    error.value = errorMessage(reason);
  } finally {
    loading.value = false;
  }
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") emit("close");
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  void loadArchive();
});

onUnmounted(() => {
  window.removeEventListener("keydown", handleKeydown);
  void zipReader?.close().catch(() => undefined);
  zipReader = null;
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview archive-preview" role="dialog" aria-modal="true" aria-label="压缩包预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前压缩包"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section v-if="!loading && !error" class="archive-preview__toolbar">
        <nav class="archive-preview__breadcrumb" aria-label="压缩包内路径">
          <button type="button" :class="{ active: currentPath.length === 0 }" @click="openPath(-1)">
            <i class="fa-solid fa-box-archive" aria-hidden="true" />
            <span>压缩包</span>
          </button>
          <template v-for="(part, index) in currentPath" :key="`${part}:${index}`">
            <i class="fa-solid fa-chevron-right" aria-hidden="true" />
            <button type="button" :class="{ active: index === currentPath.length - 1 }" @click="openPath(index)">
              {{ part }}
            </button>
          </template>
        </nav>

        <label class="archive-preview__search">
          <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
          <input v-model="query" type="search" placeholder="搜索压缩包内文件" />
          <button v-if="query" type="button" aria-label="清空搜索" @click="query = ''">
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </label>
      </section>

      <section class="archive-preview__stage">
        <div v-if="loading" class="archive-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>正在读取压缩包目录…</strong>
          <span>只读取目录信息，不会在服务器解压</span>
        </div>

        <div v-else-if="error" class="archive-preview__state archive-preview__error" role="alert">
          <i class="fa-solid fa-file-zipper" aria-hidden="true" />
          <strong>无法预览这个压缩包</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载文件</button>
        </div>

        <div v-else-if="!visibleItems.length" class="archive-preview__state">
          <i class="fa-regular fa-folder-open" aria-hidden="true" />
          <strong>{{ query ? '没有找到匹配文件' : '这个文件夹是空的' }}</strong>
        </div>

        <div v-else class="archive-preview__table-wrap">
          <table class="archive-preview__table">
            <thead>
              <tr>
                <th>名称</th>
                <th>原始大小</th>
                <th>压缩后</th>
                <th>节省</th>
                <th>修改时间</th>
                <th aria-label="加密状态" />
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="item in visibleItems"
                :key="item.key"
                :class="{ 'is-directory': item.isDir }"
                @dblclick="openItem(item)"
              >
                <td>
                  <button type="button" :disabled="!item.isDir" :title="item.path" @click="openItem(item)">
                    <i :class="item.isDir ? 'fa-solid fa-folder' : 'fa-regular fa-file'" aria-hidden="true" />
                    <span>{{ query ? item.path : item.name }}</span>
                    <i v-if="item.isDir" class="fa-solid fa-chevron-right archive-preview__enter" aria-hidden="true" />
                  </button>
                </td>
                <td>{{ item.isDir ? '—' : formatSize(item.size) }}</td>
                <td>{{ item.isDir ? '—' : formatSize(item.compressedSize) }}</td>
                <td>{{ compressionRatio(item) }}</td>
                <td>{{ formatDate(item.modifiedAt) }}</td>
                <td>
                  <i v-if="item.encrypted" class="fa-solid fa-lock" title="已加密" aria-label="已加密" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </main>
  </Teleport>
</template>

<style scoped>
.archive-preview__toolbar {
  position: absolute;
  inset: 70px 0 auto;
  z-index: 10;
  height: 58px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 0 24px;
  border-bottom: 1px solid rgb(148 177 219 / 14%);
  background: rgb(7 18 35 / 96%);
}

.archive-preview__breadcrumb {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  overflow-x: auto;
  scrollbar-width: none;
}

.archive-preview__breadcrumb::-webkit-scrollbar { display: none; }

.archive-preview__breadcrumb > i {
  flex: 0 0 auto;
  color: #465a73;
  font-size: 9px;
}

.archive-preview__breadcrumb button {
  flex: 0 0 auto;
  max-width: 220px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  overflow: hidden;
  padding: 0 10px;
  color: #8192aa;
  border: 0;
  border-radius: 7px;
  background: transparent;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}

.archive-preview__breadcrumb button:hover { color: #dce9fa; background: rgb(255 255 255 / 6%); }
.archive-preview__breadcrumb button.active { color: #fff; }

.archive-preview__search {
  flex: 0 0 280px;
  height: 36px;
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 0 11px;
  color: #70839e;
  border: 1px solid rgb(137 167 208 / 16%);
  border-radius: 8px;
  background: rgb(255 255 255 / 4%);
}

.archive-preview__search:focus-within {
  color: #4b9cff;
  border-color: rgb(54 142 250 / 52%);
  background: rgb(36 115 216 / 9%);
}

.archive-preview__search input {
  min-width: 0;
  flex: 1;
  color: #e5eefb;
  border: 0;
  outline: 0;
  background: transparent;
  font-size: 12px;
}

.archive-preview__search input::placeholder { color: #667991; }
.archive-preview__search button { width: 24px; height: 24px; border: 0; background: transparent; }

.archive-preview__stage {
  position: absolute;
  inset: 128px 0 0;
  overflow: hidden;
  background: radial-gradient(circle at 50% 0, rgb(31 70 121 / 15%), transparent 38%), #020711;
}

.archive-preview__table-wrap {
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  overflow: auto;
  padding: 22px 28px 36px;
  scrollbar-color: rgb(105 127 158 / 70%) transparent;
}

.archive-preview__table {
  width: 100%;
  table-layout: fixed;
  border-spacing: 0;
  border-collapse: separate;
  color: #cbd8e9;
  background: #07101e;
  box-shadow: 0 18px 60px rgb(0 0 0 / 28%);
  font-size: 12px;
}

.archive-preview__table th,
.archive-preview__table td {
  height: 48px;
  padding: 0 18px;
  border-bottom: 1px solid rgb(130 158 196 / 10%);
  text-align: left;
  white-space: nowrap;
}

.archive-preview__table th {
  position: sticky;
  top: 0;
  z-index: 2;
  height: 40px;
  color: #71849e;
  background: #0d1828;
  font-size: 10px;
  font-weight: 650;
  letter-spacing: 0.04em;
}

.archive-preview__table th:first-child { width: 46%; }
.archive-preview__table th:nth-child(2),
.archive-preview__table th:nth-child(3) { width: 110px; }
.archive-preview__table th:nth-child(4) { width: 70px; }
.archive-preview__table th:nth-child(5) { width: 155px; }
.archive-preview__table th:last-child { width: 28px; }

.archive-preview__table tbody tr:hover { background: rgb(37 114 214 / 8%); }
.archive-preview__table tbody tr:last-child td { border-bottom: 0; }

.archive-preview__table td:first-child { padding-left: 12px; }
.archive-preview__table td > button {
  width: 100%;
  height: 48px;
  display: flex;
  align-items: center;
  gap: 12px;
  overflow: hidden;
  padding: 0 6px;
  border: 0;
  background: transparent;
  text-align: left;
}

.archive-preview__table td > button:disabled { cursor: default; opacity: 1; }
.archive-preview__table td > button > i:first-child { width: 20px; color: #6f829c; text-align: center; font-size: 16px; }
.archive-preview__table tr.is-directory td > button > i:first-child { color: #3f9bff; }
.archive-preview__table td > button span { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.archive-preview__enter { margin-left: auto; color: #526780; font-size: 9px; }
.archive-preview__table td:last-child { color: #e6a63a; text-align: center; }

.archive-preview__state {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: #71839b;
  text-align: center;
}

.archive-preview__state > i { color: #5b7599; font-size: 42px; }
.archive-preview__state strong { color: #dfe9f7; font-size: 15px; }
.archive-preview__state span { max-width: 560px; font-size: 12px; line-height: 1.7; }


.archive-preview__error button {
  margin-top: 8px;
  padding: 9px 18px;
  border: 1px solid rgb(68 149 247 / 38%);
  border-radius: 8px;
  background: rgb(38 121 224 / 22%);
}


@media (max-width: 760px) {
  .archive-preview__toolbar { inset: 58px 0 auto; height: 96px; flex-direction: column; align-items: stretch; gap: 8px; padding: 10px 12px; }
  .archive-preview__search { flex-basis: 34px; width: 100%; }
  .archive-preview__stage { inset: 154px 0 0; }
  .archive-preview__table-wrap { padding: 12px 10px 24px; }
  .archive-preview__table th:nth-child(3),
  .archive-preview__table td:nth-child(3),
  .archive-preview__table th:nth-child(4),
  .archive-preview__table td:nth-child(4),
  .archive-preview__table th:nth-child(5),
  .archive-preview__table td:nth-child(5) { display: none; }
  .archive-preview__table th:first-child { width: auto; }
}
</style>
