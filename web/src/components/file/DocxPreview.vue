<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import PreviewHeader from "./PreviewHeader.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const DOCX_PREVIEW_MAX_BYTES = 50 * 1024 * 1024;
const MIN_ZOOM = 50;
const MAX_ZOOM = 200;
const ZOOM_STEP = 10;

const props = defineProps<{
  accountId: number;
  file: FileItem;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const stageRef = ref<HTMLElement | null>(null);
const documentRef = ref<HTMLElement | null>(null);
const loading = ref(true);
const error = ref("");
const pageCount = ref(0);
const zoom = ref(100);
const controller = new AbortController();

const statusText = computed(() => {
  const pages = pageCount.value ? ` · 共 ${pageCount.value} 页` : "";
  return `DOCX 预览${pages} · ${formatSize(props.file.size)}`;
});

const zoomText = computed(() => `${zoom.value}%`);
const zoomStyle = computed(() => ({ zoom: String(zoom.value / 100) }));

function setZoom(value: number) {
  zoom.value = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.round(value)));
}

function zoomIn() {
  setZoom(zoom.value + ZOOM_STEP);
}

function zoomOut() {
  setZoom(zoom.value - ZOOM_STEP);
}

async function fitWidth() {
  setZoom(100);
  await nextTick();
  const stage = stageRef.value;
  const page = documentRef.value?.querySelector<HTMLElement>("section.docx");
  if (!stage || !page) return;
  const availableWidth = Math.max(320, stage.clientWidth - 64);
  setZoom((availableWidth / page.offsetWidth) * 100);
}

function normalizeWordListGlyphs(container: HTMLElement) {
  container.querySelectorAll("style").forEach((style) => {
    const css = style.textContent;
    if (!css || !css.includes("\uf0b7")) return;
    style.textContent = css
      .replaceAll("\uf0b7", "•")
      .replaceAll("font-family: Symbol", 'font-family: Arial, "PingFang SC", sans-serif');
  });
}

async function loadDocument() {
  loading.value = true;
  error.value = "";
  try {
    if (props.file.size > DOCX_PREVIEW_MAX_BYTES) {
      throw new Error(`DOCX 超过 ${formatSize(DOCX_PREVIEW_MAX_BYTES)}，请下载后查看`);
    }
    const bytes = await filesApi.binaryPreviewBytes(
      props.accountId,
      props.file.id,
      props.file.name,
      DOCX_PREVIEW_MAX_BYTES,
      controller.signal,
    );
    if (controller.signal.aborted || !documentRef.value) return;

    const { renderAsync } = await import("docx-preview");
    if (controller.signal.aborted || !documentRef.value) return;
    documentRef.value.replaceChildren();
    await renderAsync(bytes, documentRef.value, documentRef.value, {
      className: "docx",
      inWrapper: true,
      breakPages: true,
      ignoreWidth: false,
      ignoreHeight: false,
      ignoreFonts: false,
      renderHeaders: true,
      renderFooters: true,
      renderFootnotes: true,
      renderEndnotes: true,
      renderComments: false,
      renderAltChunks: false,
      useBase64URL: false,
      experimental: false,
      debug: false,
    });
    if (controller.signal.aborted || !documentRef.value) return;
    normalizeWordListGlyphs(documentRef.value);
    pageCount.value = documentRef.value.querySelectorAll("section.docx").length;
  } catch (reason) {
    if (controller.signal.aborted) return;
    error.value = reason instanceof Error ? reason.message : "DOCX 解析失败";
  } finally {
    loading.value = false;
  }
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    emit("close");
    return;
  }
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  if (event.key === "+" || event.key === "=") {
    event.preventDefault();
    zoomIn();
  } else if (event.key === "-") {
    event.preventDefault();
    zoomOut();
  } else if (event.key === "0") {
    event.preventDefault();
    setZoom(100);
  }
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  void loadDocument();
});

onUnmounted(() => {
  controller.abort();
  window.removeEventListener("keydown", handleKeydown);
  documentRef.value?.replaceChildren();
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview docx-preview" role="dialog" aria-modal="true" aria-label="DOCX 预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前文档"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section ref="stageRef" class="docx-preview__stage">
        <div class="docx-preview__canvas">
          <div :style="zoomStyle">
            <div ref="documentRef" class="docx-preview__document" />
          </div>
        </div>

        <div v-if="loading" class="docx-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>正在解析 DOCX…</strong>
        </div>

        <div v-else-if="error" class="docx-preview__state docx-preview__error" role="alert">
          <i class="fa-solid fa-file-word" aria-hidden="true" />
          <strong>无法预览这个文档</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载文件</button>
        </div>
      </section>

      <div v-if="!loading && !error" class="docx-preview__toolbar" aria-label="文档缩放工具栏">
        <button type="button" title="缩小（-）" :disabled="zoom <= MIN_ZOOM" @click="zoomOut">
          <i class="fa-solid fa-minus" aria-hidden="true" />
        </button>
        <button type="button" class="docx-preview__scale" title="实际大小（100%）" @click="setZoom(100)">
          {{ zoomText }}
        </button>
        <button type="button" title="放大（+）" :disabled="zoom >= MAX_ZOOM" @click="zoomIn">
          <i class="fa-solid fa-plus" aria-hidden="true" />
        </button>
        <i class="docx-preview__divider" aria-hidden="true" />
        <button type="button" title="适应宽度" @click="fitWidth">
          <i class="fa-solid fa-arrows-left-right-to-line" aria-hidden="true" />
        </button>
      </div>
    </main>
  </Teleport>
</template>

<style scoped>
.docx-preview__stage {
  position: absolute;
  inset: 70px 0 0;
  overflow: auto;
  scrollbar-color: rgb(105 127 158 / 70%) transparent;
  background: radial-gradient(circle at 50% 0, rgb(31 70 121 / 18%), transparent 38%), #020711;
}

.docx-preview__canvas {
  box-sizing: border-box;
  width: max-content;
  min-width: 100%;
  min-height: 100%;
  padding: 28px 56px 104px;
}

.docx-preview__document :deep(.docx-wrapper) {
  min-height: 0 !important;
  padding: 0 !important;
  background: transparent !important;
}

.docx-preview__document :deep(.docx-wrapper > section.docx) {
  margin: 0 auto 22px !important;
  box-shadow: 0 20px 65px rgb(0 0 0 / 46%) !important;
}

.docx-preview__state {
  position: fixed;
  top: 48%;
  left: 50%;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border: 1px solid rgb(255 255 255 / 14%);
  border-radius: 10px;
  background: rgb(5 14 28 / 90%);
  box-shadow: 0 18px 55px rgb(0 0 0 / 34%);
  transform: translate(-50%, -50%);
}

.docx-preview__state strong { font-size: 13px; font-weight: 550; }


.docx-preview__error {
  width: min(430px, calc(100vw - 32px));
  flex-direction: column;
  padding: 24px;
  text-align: center;
}

.docx-preview__error > i { color: #68a9ff; font-size: 30px; }
.docx-preview__error span { color: #9eb0c8; font-size: 13px; line-height: 1.7; }
.docx-preview__error button {
  margin-top: 3px;
  padding: 9px 18px;
  border: 1px solid #268bff;
  border-radius: 8px;
  background: #187ce0;
  font-weight: 650;
}

.docx-preview__toolbar {
  position: fixed;
  bottom: 20px;
  left: 50%;
  z-index: 12;
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 5px;
  border: 1px solid rgb(151 181 224 / 18%);
  border-radius: 12px;
  background: rgb(4 13 27 / 82%);
  box-shadow: 0 12px 38px rgb(0 0 0 / 30%);
  backdrop-filter: blur(14px);
  transform: translateX(-50%);
}

.docx-preview__toolbar button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 39px;
  height: 38px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #d4dfed;
  font-size: 13px;
}

.docx-preview__toolbar button:hover:not(:disabled) { color: #fff; background: rgb(255 255 255 / 10%); }
.docx-preview__toolbar button:disabled { cursor: default; opacity: 0.25; }
.docx-preview__toolbar .docx-preview__scale { width: 50px; color: #8fc7ff; font-size: 11px; font-weight: 650; }
.docx-preview__divider { width: 1px; height: 24px; margin: 0 4px; background: rgb(145 174 216 / 18%); }


@media (max-width: 760px) {
  .docx-preview__stage { inset: 58px 0 0; }
  .docx-preview__canvas { padding: 16px 16px 88px; }
  .docx-preview__toolbar { bottom: 10px; }
}
</style>
