<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from "vue";
import type { WorkBook, WorkSheet } from "xlsx";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import { decodeTextBytes } from "@/utils/textEncoding";
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

const MAX_FILE_BYTES = 50 * 1024 * 1024;
const ROWS_PER_PAGE = 100;
const MAX_VISIBLE_COLUMNS = 100;

interface SheetMeta {
  name: string;
  rows: number;
  columns: number;
}

interface GridCell {
  key: string;
  text: string;
  hidden: boolean;
  rowSpan: number;
  colSpan: number;
}

interface GridRow {
  number: number;
  cells: GridCell[];
}

const loading = ref(true);
const error = ref("");
const workbook = shallowRef<WorkBook | null>(null);
const sheetMetas = ref<SheetMeta[]>([]);
const activeSheetName = ref("");
const page = ref(0);
const csvEncoding = ref("");
const controller = new AbortController();
let XLSX: typeof import("xlsx") | null = null;

const activeMeta = computed(() => sheetMetas.value.find((item) => item.name === activeSheetName.value));
const pageCount = computed(() => Math.max(1, Math.ceil((activeMeta.value?.rows || 0) / ROWS_PER_PAGE)));
const pageStart = computed(() => page.value * ROWS_PER_PAGE);
const pageEnd = computed(() => Math.min(pageStart.value + ROWS_PER_PAGE, activeMeta.value?.rows || 0));
const visibleColumns = computed(() => Math.min(activeMeta.value?.columns || 0, MAX_VISIBLE_COLUMNS));
const columnsTruncated = computed(() => (activeMeta.value?.columns || 0) > MAX_VISIBLE_COLUMNS);

const statusText = computed(() => {
  const sheets = sheetMetas.value.length ? ` · ${sheetMetas.value.length} 个工作表` : "";
  const encoding = csvEncoding.value ? ` · ${csvEncoding.value.toUpperCase()}` : "";
  return `表格预览${sheets}${encoding} · ${formatSize(props.file.size)}`;
});

const sheetSummary = computed(() => {
  const meta = activeMeta.value;
  if (!meta) return "";
  const columns = columnsTruncated.value ? `前 ${MAX_VISIBLE_COLUMNS} / ${meta.columns} 列` : `${meta.columns} 列`;
  return `${meta.rows.toLocaleString()} 行 × ${columns}`;
});

const pageSummary = computed(() => {
  const meta = activeMeta.value;
  if (!meta?.rows) return "无数据";
  return `${(pageStart.value + 1).toLocaleString()}–${pageEnd.value.toLocaleString()} / ${meta.rows.toLocaleString()} 行`;
});

const columnLabels = computed(() => {
  if (!XLSX) return [];
  return Array.from({ length: visibleColumns.value }, (_, index) => XLSX!.utils.encode_col(index));
});

const gridRows = computed<GridRow[]>(() => {
  if (!XLSX || !workbook.value || !activeMeta.value || pageEnd.value <= pageStart.value) return [];
  const sheet = workbook.value.Sheets[activeSheetName.value];
  if (!sheet) return [];

  const anchors = new Map<string, GridCell>();
  const covered = new Set<string>();
  for (const merge of sheet["!merges"] || []) {
    const startRow = Math.max(merge.s.r, pageStart.value);
    const endRow = Math.min(merge.e.r, pageEnd.value - 1);
    const startCol = Math.max(merge.s.c, 0);
    const endCol = Math.min(merge.e.c, visibleColumns.value - 1);
    if (startRow > endRow || startCol > endCol) continue;
    const anchorKey = `${startRow}:${startCol}`;
    anchors.set(anchorKey, {
      key: anchorKey,
      text: readCellText(sheet, merge.s.r, merge.s.c),
      hidden: false,
      rowSpan: endRow - startRow + 1,
      colSpan: endCol - startCol + 1,
    });
    for (let row = startRow; row <= endRow; row += 1) {
      for (let column = startCol; column <= endCol; column += 1) {
        const key = `${row}:${column}`;
        if (key !== anchorKey) covered.add(key);
      }
    }
  }

  const rows: GridRow[] = [];
  for (let row = pageStart.value; row < pageEnd.value; row += 1) {
    const cells: GridCell[] = [];
    for (let column = 0; column < visibleColumns.value; column += 1) {
      const key = `${row}:${column}`;
      cells.push(anchors.get(key) || {
        key,
        text: readCellText(sheet, row, column),
        hidden: covered.has(key),
        rowSpan: 1,
        colSpan: 1,
      });
    }
    rows.push({ number: row + 1, cells });
  }
  return rows;
});

function readCellText(sheet: WorkSheet, row: number, column: number) {
  if (!XLSX) return "";
  const cell = sheet[XLSX.utils.encode_cell({ r: row, c: column })];
  if (!cell) return "";
  if (cell.v == null && cell.f) return `=${cell.f}`;
  try {
    return XLSX.utils.format_cell(cell);
  } catch {
    return String(cell.w ?? cell.v ?? "");
  }
}

function sheetMeta(name: string, sheet: WorkSheet): SheetMeta {
  if (!XLSX || !sheet["!ref"]) return { name, rows: 0, columns: 0 };
  try {
    const range = XLSX.utils.decode_range(sheet["!ref"]);
    return { name, rows: range.e.r + 1, columns: range.e.c + 1 };
  } catch {
    return { name, rows: 0, columns: 0 };
  }
}

function readSpreadsheet(source: Uint8Array | string, options: Parameters<typeof import("xlsx").read>[1], isOds: boolean) {
  if (!XLSX || !isOds) return XLSX!.read(source, options);
  const originalError = console.error;
  console.error = (...args: unknown[]) => {
    if (String(args[0] || "").startsWith("ODS number format may be incorrect:")) return;
    originalError(...args);
  };
  try {
    return XLSX.read(source, options);
  } finally {
    console.error = originalError;
  }
}

function selectSheet(name: string) {
  activeSheetName.value = name;
  page.value = 0;
}

function previousPage() {
  if (page.value > 0) page.value -= 1;
}

function nextPage() {
  if (page.value + 1 < pageCount.value) page.value += 1;
}

async function loadWorkbook() {
  loading.value = true;
  error.value = "";
  try {
    const bytes = await filesApi.binaryPreviewBytes(
      props.accountId,
      props.file.id,
      props.file.name,
      MAX_FILE_BYTES,
      controller.signal,
    );
    XLSX = await import("xlsx");
    const extension = props.file.name.split(".").pop()?.toLowerCase();
    const options = { cellDates: true, cellNF: extension !== "ods", cellText: true, dense: false } as const;
    let parsed: WorkBook;
    if (extension === "csv") {
      const decoded = decodeTextBytes(bytes);
      csvEncoding.value = decoded.encoding;
      parsed = readSpreadsheet(decoded.text, { ...options, type: "string" }, false);
    } else {
      parsed = readSpreadsheet(bytes, { ...options, type: "array" }, extension === "ods");
    }
    if (!parsed.SheetNames.length) throw new Error("文件中没有可显示的工作表");
    workbook.value = parsed;
    sheetMetas.value = parsed.SheetNames.map((name) => sheetMeta(name, parsed.Sheets[name]));
    selectSheet(parsed.SheetNames[0]);
  } catch (reason) {
    if (controller.signal.aborted) return;
    error.value = reason instanceof Error ? reason.message : "表格解析失败";
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
  void loadWorkbook();
});

onUnmounted(() => {
  controller.abort();
  window.removeEventListener("keydown", handleKeydown);
  workbook.value = null;
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview sheet-preview" role="dialog" aria-modal="true" aria-label="表格预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前表格"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section v-if="!loading && !error" class="sheet-preview__toolbar">
        <div class="sheet-preview__tabs" role="tablist" aria-label="工作表">
          <button
            v-for="sheet in sheetMetas"
            :key="sheet.name"
            type="button"
            role="tab"
            :aria-selected="sheet.name === activeSheetName"
            :class="{ active: sheet.name === activeSheetName }"
            :title="sheet.name"
            @click="selectSheet(sheet.name)"
          >
            {{ sheet.name }}
          </button>
        </div>
        <span class="sheet-preview__summary">{{ sheetSummary }}</span>
        <div class="sheet-preview__pages" aria-label="表格分页">
          <button type="button" title="上一页" :disabled="page <= 0" @click="previousPage">
            <i class="fa-solid fa-chevron-left" aria-hidden="true" />
          </button>
          <span>{{ pageSummary }}</span>
          <button type="button" title="下一页" :disabled="page + 1 >= pageCount" @click="nextPage">
            <i class="fa-solid fa-chevron-right" aria-hidden="true" />
          </button>
        </div>
      </section>

      <section class="sheet-preview__stage">
        <div v-if="loading" class="sheet-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>正在解析表格…</strong>
        </div>

        <div v-else-if="error" class="sheet-preview__state sheet-preview__error" role="alert">
          <i class="fa-solid fa-file-excel" aria-hidden="true" />
          <strong>无法预览这个表格</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载文件</button>
        </div>

        <div v-else-if="!gridRows.length" class="sheet-preview__state">
          <i class="fa-solid fa-table-cells" aria-hidden="true" />
          <strong>这个工作表没有数据</strong>
        </div>

        <div v-else class="sheet-preview__grid-wrap">
          <table class="sheet-preview__grid">
            <thead>
              <tr>
                <th class="sheet-preview__corner" aria-label="行号" />
                <th v-for="label in columnLabels" :key="label" scope="col">{{ label }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in gridRows" :key="row.number">
                <th scope="row">{{ row.number }}</th>
                <template v-for="cell in row.cells" :key="cell.key">
                  <td
                    v-if="!cell.hidden"
                    :rowspan="cell.rowSpan"
                    :colspan="cell.colSpan"
                    :title="cell.text"
                  >
                    {{ cell.text }}
                  </td>
                </template>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </main>
  </Teleport>
</template>

<style scoped>
.sheet-preview__toolbar {
  position: absolute;
  inset: 70px 0 auto;
  z-index: 10;
  height: 56px;
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto auto;
  align-items: center;
  gap: 18px;
  padding: 0 22px;
  border-bottom: 1px solid rgb(148 177 219 / 14%);
  background: rgb(7 18 35 / 96%);
}

.sheet-preview__tabs {
  min-width: 0;
  display: flex;
  gap: 5px;
  overflow-x: auto;
  scrollbar-width: none;
}

.sheet-preview__tabs::-webkit-scrollbar { display: none; }

.sheet-preview__tabs button {
  flex: 0 0 auto;
  max-width: 180px;
  height: 34px;
  overflow: hidden;
  padding: 0 14px;
  color: #899bb4;
  border: 1px solid transparent;
  border-radius: 7px;
  background: transparent;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 600;
}

.sheet-preview__tabs button:hover { color: #dce9fa; background: rgb(255 255 255 / 6%); }
.sheet-preview__tabs button.active { color: #fff; border-color: rgb(65 151 255 / 30%); background: rgb(29 112 216 / 24%); }

.sheet-preview__summary {
  color: #8192aa;
  white-space: nowrap;
  font-size: 11px;
}

.sheet-preview__pages {
  display: flex;
  align-items: center;
  gap: 9px;
  color: #98abc4;
  white-space: nowrap;
  font-size: 11px;
}

.sheet-preview__pages button {
  width: 30px;
  height: 30px;
  border: 0;
  border-radius: 7px;
  background: rgb(255 255 255 / 6%);
}

.sheet-preview__pages button:hover:not(:disabled) { color: #fff; background: rgb(44 130 239 / 24%); }
.sheet-preview__pages button:disabled { cursor: default; opacity: 0.28; }

.sheet-preview__stage {
  position: absolute;
  inset: 126px 0 0;
  overflow: hidden;
  background: radial-gradient(circle at 50% 0, rgb(31 70 121 / 15%), transparent 38%), #020711;
}

.sheet-preview__grid-wrap {
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  overflow: auto;
  padding: 20px 24px 34px;
  scrollbar-color: rgb(105 127 158 / 70%) transparent;
}

.sheet-preview__grid {
  min-width: 100%;
  border-spacing: 0;
  border-collapse: separate;
  color: #dce6f4;
  background: #07101e;
  box-shadow: 0 18px 60px rgb(0 0 0 / 28%);
  font-size: 12px;
}

.sheet-preview__grid th,
.sheet-preview__grid td {
  box-sizing: border-box;
  min-width: 112px;
  max-width: 320px;
  height: 34px;
  overflow: hidden;
  padding: 7px 10px;
  border-right: 1px solid rgb(130 158 196 / 12%);
  border-bottom: 1px solid rgb(130 158 196 / 12%);
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.sheet-preview__grid thead th {
  position: sticky;
  top: 0;
  z-index: 4;
  height: 36px;
  color: #91a6c1;
  background: #101c2d;
  text-align: center;
  font-size: 10px;
  font-weight: 650;
}

.sheet-preview__grid tbody th,
.sheet-preview__corner {
  position: sticky;
  left: 0;
  z-index: 3;
  min-width: 54px !important;
  width: 54px;
  color: #7689a2;
  background: #0d1828;
  text-align: center !important;
  font-size: 10px;
  font-weight: 600;
}

.sheet-preview__corner { z-index: 6 !important; }
.sheet-preview__grid tbody tr:nth-child(even) td { background: rgb(255 255 255 / 1.5%); }
.sheet-preview__grid tbody td:hover { background: rgb(39 122 226 / 12%); }

.sheet-preview__state {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 13px;
  color: #b6c5d8;
}

.sheet-preview__state > i { color: #59a2ff; font-size: 34px; }
.sheet-preview__state strong { color: #e7eff9; font-size: 14px; font-weight: 600; }
.sheet-preview__state span { max-width: 520px; color: #8ea1ba; text-align: center; font-size: 12px; line-height: 1.7; }


.sheet-preview__error button {
  margin-top: 5px;
  padding: 8px 15px;
  border: 1px solid rgb(91 160 247 / 26%);
  border-radius: 7px;
  background: rgb(43 126 230 / 18%);
  font-size: 12px;
}


@media (max-width: 760px) {
  .sheet-preview__toolbar {
    height: 96px;
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-rows: 48px 38px;
    gap: 0 10px;
    padding: 0 12px;
  }
  .sheet-preview__tabs { grid-column: 1 / -1; }
  .sheet-preview__summary { align-self: start; }
  .sheet-preview__pages { align-self: start; }
  .sheet-preview__stage { inset: 166px 0 0; }
  .sheet-preview__grid-wrap { padding: 12px 10px 26px; }
}
</style>
