<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  getDocument,
  GlobalWorkerOptions,
  PasswordResponses,
  type PDFDocumentLoadingTask,
  type PDFDocumentProxy,
} from "pdfjs-dist";
import pdfWorkerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import "pdfjs-dist/web/pdf_viewer.css";
import "@fortawesome/fontawesome-free/css/all.min.css";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import PdfPreviewPage from "./PdfPreviewPage.vue";
import PreviewHeader from "./PreviewHeader.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

GlobalWorkerOptions.workerSrc = pdfWorkerURL;

const props = defineProps<{
  accountId: number;
  file: FileItem;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const stageRef = ref<HTMLElement | null>(null);
const loading = ref(true);
const documentReady = ref(false);
const error = ref("");
const progress = ref(0);
const pageNumber = ref(1);
const pageCount = ref(0);
const pageInput = ref("1");
const scale = ref(1);
const rotation = ref(0);
const fitWidth = ref(false);
const passwordRequired = ref(false);
const passwordValue = ref("");
const passwordError = ref("");
const pageMetrics = ref<Record<number, { width: number; height: number }>>({});
const searchOpen = ref(false);
const searchInput = ref("");
const searchQuery = ref("");
const searchRunning = ref(false);
const searchPages = ref<number[]>([]);
const searchIndex = ref(-1);
const pageTextCache = new Map<number, string>();
let passwordUpdater: ((password: string) => void) | null = null;
let loadingTask: PDFDocumentLoadingTask | null = null;
let pdfDocument: PDFDocumentProxy | null = null;
let resizeFrame = 0;
let scrollFrame = 0;
let resizeObserver: ResizeObserver | null = null;
let searchTimer = 0;
const zoomLevels = [25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400];

const statusText = computed(() => {
  if (passwordRequired.value) return "PDF 预览 · 需要密码";
  if (loading.value) return progress.value > 0 ? `PDF 预览 · 已加载 ${progress.value}%` : "PDF 预览";
  if (pageCount.value > 0) return `第${pageNumber.value}页/共${pageCount.value}页 · ${formatSize(props.file.size)}`;
  return `PDF 预览 · ${formatSize(props.file.size)}`;
});
const zoomText = computed(() => `${Math.round(scale.value * 100)}%`);
const pageNumbers = computed(() => Array.from({ length: pageCount.value }, (_, index) => index + 1));

function previewURL() {
  return filesApi.pdfPreviewURL(props.accountId, props.file.id, props.file.name);
}

async function loadPDF() {
  loading.value = true;
  error.value = "";
  progress.value = 0;
  try {
    loadingTask = getDocument({
      url: previewURL(),
      withCredentials: true,
      rangeChunkSize: 256 * 1024,
      disableStream: true,
      disableAutoFetch: true,
      useSystemFonts: true,
    });
    loadingTask.onProgress = ({ loaded, total }: { loaded: number; total: number }) => {
      if (total > 0) progress.value = Math.min(100, Math.round((loaded / total) * 100));
    };
    loadingTask.onPassword = (updatePassword: (password: string) => void, reason: number) => {
      passwordUpdater = updatePassword;
      passwordRequired.value = true;
      passwordError.value = reason === PasswordResponses.INCORRECT_PASSWORD ? "密码不正确，请重新输入" : "该 PDF 需要密码才能打开";
      passwordValue.value = "";
    };
    pdfDocument = await loadingTask.promise;
    pageCount.value = pdfDocument.numPages;
    pageNumber.value = 1;
    pageInput.value = "1";
    passwordRequired.value = false;
    documentReady.value = true;
  } catch (reason) {
    if (reason instanceof Error && reason.name === "AbortException") return;
    documentReady.value = false;
    error.value = reason instanceof Error ? reason.message : "PDF 加载失败";
  } finally {
    loading.value = false;
  }
}

function submitPassword() {
  const password = passwordValue.value;
  if (!password || !passwordUpdater) return;
  passwordRequired.value = false;
  passwordError.value = "";
  passwordUpdater(password);
}

function availablePageWidth() {
  const stage = stageRef.value;
  if (!stage) return 900;
  return Math.max(280, stage.clientWidth - (stage.clientWidth <= 760 ? 32 : 150));
}

function updateFitScale() {
  const widths = Object.values(pageMetrics.value).map((metric) => metric.width);
  if (widths.length === 0) return;
  scale.value = Math.max(0.25, Math.min(4, availablePageWidth() / Math.max(...widths)));
}

function onPageReady(payload: { pageNumber: number; width: number; height: number }) {
  pageMetrics.value = { ...pageMetrics.value, [payload.pageNumber]: { width: payload.width, height: payload.height } };
  if (fitWidth.value) updateFitScale();
}

function pageElement(target: number) {
  return stageRef.value?.querySelector<HTMLElement>(`[data-pdf-page="${target}"]`) ?? null;
}

function changePage(target: number, behavior: ScrollBehavior = "smooth") {
  if (!pdfDocument) return;
  const next = Math.max(1, Math.min(pageCount.value, target));
  pageNumber.value = next;
  pageInput.value = String(next);
  const stage = stageRef.value;
  const page = pageElement(next);
  if (stage && page) stage.scrollTo({ top: Math.max(0, page.offsetTop - 24), behavior });
}

function submitPageNumber() {
  const target = Number.parseInt(pageInput.value, 10);
  if (!Number.isFinite(target)) {
    pageInput.value = String(pageNumber.value);
    return;
  }
  changePage(target);
}

function changeZoom(direction: -1 | 1) {
  if (!pdfDocument) return;
  fitWidth.value = false;
  const current = scale.value * 100;
  const next = direction > 0
    ? zoomLevels.find((level) => level > current + 0.5) ?? zoomLevels.at(-1)!
    : zoomLevels.findLast((level) => level < current - 0.5) ?? zoomLevels[0];
  scale.value = next / 100;
}

function actualSize() {
  if (!pdfDocument) return;
  fitWidth.value = false;
  scale.value = 1;
}

function fitToWidth() {
  if (!pdfDocument) return;
  fitWidth.value = true;
  updateFitScale();
}

function rotatePages() {
  if (!pdfDocument) return;
  rotation.value = (rotation.value + 90) % 360;
  pageMetrics.value = Object.fromEntries(
    Object.entries(pageMetrics.value).map(([page, metric]) => [page, { width: metric.height, height: metric.width }]),
  );
  if (fitWidth.value) updateFitScale();
}

async function pageText(number: number) {
  const cached = pageTextCache.get(number);
  if (cached !== undefined) return cached;
  if (!pdfDocument) return "";
  const page = await pdfDocument.getPage(number);
  const content = await page.getTextContent();
  const text = content.items
    .map((item) => "str" in item ? item.str : "")
    .join("")
    .replace(/\s+/g, " ");
  pageTextCache.set(number, text);
  return text;
}

function occurrenceCount(text: string, query: string) {
  let count = 0;
  let offset = 0;
  while ((offset = text.indexOf(query, offset)) >= 0) {
    count += 1;
    offset += Math.max(1, query.length);
  }
  return count;
}

async function performSearch() {
  const query = searchInput.value.trim();
  searchQuery.value = query;
  searchPages.value = [];
  searchIndex.value = -1;
  if (!query || !pdfDocument) return;
  searchRunning.value = true;
  const normalized = query.toLocaleLowerCase();
  try {
    const found: number[] = [];
    for (let start = 1; start <= pageCount.value; start += 6) {
      const batch = Array.from({ length: Math.min(6, pageCount.value - start + 1) }, (_, index) => start + index);
      const texts = await Promise.all(batch.map(pageText));
      texts.forEach((text, index) => {
        const count = occurrenceCount(text.toLocaleLowerCase(), normalized);
        for (let match = 0; match < count; match += 1) found.push(batch[index]);
      });
    }
    searchPages.value = found;
    if (found.length > 0) {
      searchIndex.value = 0;
      changePage(found[0]);
    }
  } finally {
    searchRunning.value = false;
  }
}

function moveSearch(direction: -1 | 1) {
  if (searchPages.value.length === 0) return;
  searchIndex.value = (searchIndex.value + direction + searchPages.value.length) % searchPages.value.length;
  changePage(searchPages.value[searchIndex.value]);
}

function closeSearch() {
  searchOpen.value = false;
  searchInput.value = "";
  searchQuery.value = "";
  searchPages.value = [];
  searchIndex.value = -1;
}

watch(searchInput, () => {
  window.clearTimeout(searchTimer);
  searchTimer = window.setTimeout(() => void performSearch(), 250);
});

function updateCurrentPage() {
  cancelAnimationFrame(scrollFrame);
  scrollFrame = requestAnimationFrame(() => {
    const stage = stageRef.value;
    if (!stage) return;
    const targetY = stage.getBoundingClientRect().top + Math.min(220, stage.clientHeight * 0.3);
    let closestPage = pageNumber.value;
    let closestDistance = Number.POSITIVE_INFINITY;
    stage.querySelectorAll<HTMLElement>("[data-pdf-page]").forEach((page) => {
      const rect = page.getBoundingClientRect();
      const distance = targetY >= rect.top && targetY <= rect.bottom ? 0 : Math.min(Math.abs(rect.top - targetY), Math.abs(rect.bottom - targetY));
      if (distance < closestDistance) {
        closestDistance = distance;
        closestPage = Number(page.dataset.pdfPage) || closestPage;
      }
    });
    if (closestPage !== pageNumber.value) {
      pageNumber.value = closestPage;
      pageInput.value = String(closestPage);
    }
  });
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && !!target.closest("input, textarea, [contenteditable='true']");
}

function handleKeydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f") {
    event.preventDefault();
    searchOpen.value = true;
    requestAnimationFrame(() => document.querySelector<HTMLInputElement>(".pdf-preview__search input")?.focus());
    return;
  }
  if (event.key === "Escape") {
    if (searchOpen.value) {
      closeSearch();
      return;
    }
    emit("close");
    return;
  }
  if (isEditableTarget(event.target) || passwordRequired.value) return;
  const key = event.key.toLowerCase();
  if (["pageup", "pagedown", "+", "=", "-", "0"].includes(key)) event.preventDefault();
  if (key === "pageup") changePage(pageNumber.value - 1);
  else if (key === "pagedown") changePage(pageNumber.value + 1);
  else if (key === "+" || key === "=") changeZoom(1);
  else if (key === "-") changeZoom(-1);
  else if (key === "0") actualSize();
  else if (key === "r") rotatePages();
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  if (stageRef.value) {
    resizeObserver = new ResizeObserver(() => {
      if (!fitWidth.value) return;
      cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(updateFitScale);
    });
    resizeObserver.observe(stageRef.value);
  }
  void loadPDF();
});

onUnmounted(() => {
  documentReady.value = false;
  void loadingTask?.destroy();
  resizeObserver?.disconnect();
  window.clearTimeout(searchTimer);
  cancelAnimationFrame(resizeFrame);
  cancelAnimationFrame(scrollFrame);
  window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview pdf-preview" role="dialog" aria-modal="true" aria-label="PDF 预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前 PDF"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section ref="stageRef" class="pdf-preview__stage" @scroll.passive="updateCurrentPage">
        <div v-if="documentReady && pdfDocument" class="pdf-preview__pages">
          <PdfPreviewPage
            v-for="number in pageNumbers"
            :key="number"
            :document="pdfDocument"
            :page-number="number"
            :scale="scale"
            :rotation="rotation"
            :scroll-root="stageRef"
            :search-query="searchQuery"
            :active-search="searchPages[searchIndex] === number"
            @ready="onPageReady"
          />
        </div>

        <div v-if="loading && !passwordRequired && !error" class="pdf-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>{{ progress > 0 ? `正在加载 PDF… ${progress}%` : "正在加载 PDF…" }}</strong>
        </div>

        <form v-if="passwordRequired" class="pdf-preview__state pdf-preview__password" @submit.prevent="submitPassword">
          <i class="fa-solid fa-lock" aria-hidden="true" />
          <strong>受密码保护的 PDF</strong>
          <span>{{ passwordError }}</span>
          <input v-model="passwordValue" type="password" placeholder="请输入 PDF 密码" autocomplete="off" autofocus />
          <button type="submit" :disabled="!passwordValue">打开文件</button>
        </form>

        <div v-else-if="error" class="pdf-preview__state pdf-preview__error" role="alert">
          <i class="fa-solid fa-file-pdf" aria-hidden="true" />
          <strong>无法预览这个 PDF</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载 PDF</button>
        </div>

        <div v-if="documentReady && !passwordRequired && !error" class="pdf-preview__toolbar">
          <button type="button" aria-label="上一页" title="上一页" :disabled="pageNumber <= 1" @click="changePage(pageNumber - 1)">
            <i class="fa-solid fa-chevron-left" aria-hidden="true" />
          </button>
          <label class="pdf-preview__pager">
            <input v-model="pageInput" inputmode="numeric" aria-label="页码" @change="submitPageNumber" @keydown.enter.prevent="submitPageNumber" />
            <span>/ {{ pageCount }}</span>
          </label>
          <button type="button" aria-label="下一页" title="下一页" :disabled="pageNumber >= pageCount" @click="changePage(pageNumber + 1)">
            <i class="fa-solid fa-chevron-right" aria-hidden="true" />
          </button>
          <i class="pdf-preview__divider" aria-hidden="true" />
          <button type="button" aria-label="缩小页面" title="缩小" @click="changeZoom(-1)">
            <i class="fa-solid fa-minus" aria-hidden="true" />
          </button>
          <span class="pdf-preview__scale" aria-label="当前缩放比例">{{ zoomText }}</span>
          <button type="button" aria-label="放大页面" title="放大" @click="changeZoom(1)">
            <i class="fa-solid fa-plus" aria-hidden="true" />
          </button>
          <button type="button" class="pdf-preview__actual" aria-label="实际大小" title="实际大小（100%）" @click="actualSize">1:1</button>
          <button type="button" aria-label="适应宽度" title="适应宽度" @click="fitToWidth">
            <i class="fa-solid fa-arrows-left-right" aria-hidden="true" />
          </button>
          <button type="button" aria-label="旋转页面" title="顺时针旋转" @click="rotatePages">
            <i class="fa-solid fa-rotate-right" aria-hidden="true" />
          </button>
          <i class="pdf-preview__divider" aria-hidden="true" />
          <button type="button" aria-label="搜索 PDF" title="搜索（Ctrl/⌘ + F）" :class="{ 'is-active': searchOpen }" @click="searchOpen = !searchOpen">
            <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
          </button>
        </div>

        <form v-if="searchOpen && documentReady" class="pdf-preview__search" @submit.prevent="performSearch">
          <input v-model="searchInput" type="search" placeholder="搜索 PDF 文字" aria-label="搜索 PDF 文字" autofocus />
          <span v-if="searchRunning">搜索中…</span>
          <span v-else>{{ searchPages.length ? `${searchIndex + 1} / ${searchPages.length}` : searchQuery ? "没有结果" : "" }}</span>
          <button type="button" title="上一个结果" :disabled="!searchPages.length" @click="moveSearch(-1)"><i class="fa-solid fa-chevron-up" /></button>
          <button type="button" title="下一个结果" :disabled="!searchPages.length" @click="moveSearch(1)"><i class="fa-solid fa-chevron-down" /></button>
          <button type="button" title="关闭搜索" @click="closeSearch"><i class="fa-solid fa-xmark" /></button>
        </form>
      </section>
    </main>
  </Teleport>
</template>

<style scoped>
.pdf-preview__stage {
  position: absolute;
  inset: 70px 0 0;
  overflow: auto;
  background:
    radial-gradient(circle at 50% 0%, rgb(31 70 121 / 18%), transparent 38%),
    #020711;
  scrollbar-color: rgb(105 127 158 / 70%) transparent;
}

.pdf-preview__pages {
  width: max-content;
  min-width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 22px;
  box-sizing: border-box;
  padding: 28px 75px 104px;
}

.pdf-preview__toolbar {
  position: fixed;
  left: 50%;
  bottom: 20px;
  z-index: 12;
  display: flex;
  align-items: center;
  gap: 3px;
  transform: translateX(-50%);
  padding: 5px;
  border: 1px solid rgb(151 181 224 / 18%);
  border-radius: 12px;
  background: rgb(4 13 27 / 82%);
  box-shadow: 0 12px 38px rgb(0 0 0 / 30%);
  backdrop-filter: blur(14px);
}

.pdf-preview__toolbar button {
  width: 39px;
  height: 38px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #d4dfed;
  font-size: 13px;
}
.pdf-preview__toolbar button:hover:not(:disabled) { color: #fff; background: rgb(255 255 255 / 10%); }
.pdf-preview__toolbar button.is-active { color: #8fc7ff; background: rgb(37 141 241 / 18%); }
.pdf-preview__toolbar button:disabled { opacity: 0.25; cursor: default; }
.pdf-preview__toolbar .pdf-preview__actual { width: 43px; color: #dce9f8; font-size: 11px; font-weight: 700; }
.pdf-preview__scale { width: 48px; color: #8fc7ff; text-align: center; font-size: 11px; font-weight: 650; }

.pdf-preview__pager { display: flex; align-items: center; gap: 5px; padding: 0 5px; color: #8e9db2; font-size: 11px; white-space: nowrap; }
.pdf-preview__pager input {
  width: 38px;
  height: 30px;
  padding: 0 5px;
  text-align: center;
  color: #f3f7fc;
  border: 1px solid rgb(145 174 216 / 20%);
  border-radius: 6px;
  outline: none;
  background: rgb(255 255 255 / 6%);
  font: inherit;
}
.pdf-preview__pager input:focus { border-color: #258df1; }
.pdf-preview__divider { width: 1px; height: 24px; margin: 0 4px; background: rgb(145 174 216 / 18%); }

.pdf-preview__search {
  position: fixed;
  right: 22px;
  top: 86px;
  z-index: 13;
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 6px;
  color: #dbe8f7;
  border: 1px solid rgb(151 181 224 / 18%);
  border-radius: 10px;
  background: rgb(4 13 27 / 92%);
  box-shadow: 0 14px 42px rgb(0 0 0 / 32%);
  backdrop-filter: blur(14px);
}
.pdf-preview__search input {
  width: 220px;
  height: 34px;
  padding: 0 10px;
  color: #f3f7fc;
  border: 1px solid rgb(145 174 216 / 22%);
  border-radius: 7px;
  outline: none;
  background: rgb(255 255 255 / 6%);
}
.pdf-preview__search input:focus { border-color: #258df1; }
.pdf-preview__search span { min-width: 55px; color: #96a9c1; text-align: center; font-size: 11px; }
.pdf-preview__search button {
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 7px;
  background: transparent;
}
.pdf-preview__search button:hover:not(:disabled) { background: rgb(255 255 255 / 10%); }
.pdf-preview__search button:disabled { opacity: 0.25; }

.pdf-preview__state {
  position: fixed;
  left: 50%;
  top: 48%;
  z-index: 10;
  transform: translate(-50%, -50%);
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border: 1px solid rgb(255 255 255 / 14%);
  border-radius: 10px;
  background: rgb(5 14 28 / 90%);
  box-shadow: 0 18px 55px rgb(0 0 0 / 34%);
}
.pdf-preview__state strong { font-size: 13px; font-weight: 550; }

.pdf-preview__password,
.pdf-preview__error { width: min(430px, calc(100vw - 32px)); flex-direction: column; text-align: center; padding: 24px; }
.pdf-preview__password > i, .pdf-preview__error > i { color: #69b3ff; font-size: 29px; }
.pdf-preview__error > i { color: #ffb45e; }
.pdf-preview__password span, .pdf-preview__error span { color: #9eb0c8; font-size: 13px; line-height: 1.7; }
.pdf-preview__password input {
  width: 100%;
  height: 42px;
  padding: 0 12px;
  color: #f4f8ff;
  border: 1px solid rgb(145 174 216 / 24%);
  border-radius: 8px;
  outline: none;
  background: rgb(255 255 255 / 6%);
}
.pdf-preview__password input:focus { border-color: #258df1; }
.pdf-preview__password button, .pdf-preview__error button { margin-top: 3px; padding: 9px 18px; border: 1px solid #268bff; border-radius: 8px; background: #187ce0; font-weight: 650; }
.pdf-preview__password button:disabled { opacity: 0.45; }


@media (max-width: 760px) {
  .pdf-preview__stage { inset: 58px 0 0; }
  .pdf-preview__pages { gap: 12px; padding: 16px 16px 88px; }
  .pdf-preview__toolbar { bottom: 10px; gap: 0; max-width: calc(100vw - 12px); }
  .pdf-preview__toolbar button { width: 34px; }
  .pdf-preview__toolbar .pdf-preview__actual { width: 38px; }
  .pdf-preview__scale { width: 42px; }
  .pdf-preview__pager { padding: 0 2px; }
  .pdf-preview__divider { margin: 0 1px; }
  .pdf-preview__search { left: 8px; right: 8px; top: 66px; }
  .pdf-preview__search input { min-width: 0; width: 100%; }
}
</style>
