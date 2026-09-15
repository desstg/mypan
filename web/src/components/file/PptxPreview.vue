<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import type { PptxViewer } from "@aiden0z/pptx-renderer";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import PreviewHeader from "./PreviewHeader.vue";
import PreviewSideNavigation from "./PreviewSideNavigation.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const MAX_FILE_BYTES = 200 * 1024 * 1024;
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
const viewerRef = ref<HTMLElement | null>(null);
const loading = ref(true);
const error = ref("");
const slideCount = ref(0);
const currentSlide = ref(0);
const zoom = ref(100);
const controller = new AbortController();
let viewer: PptxViewer | null = null;

const statusText = computed(() => {
  const pages = slideCount.value ? ` · 第 ${currentSlide.value + 1} / ${slideCount.value} 页` : "";
  return `PPTX 预览${pages} · ${formatSize(props.file.size)}`;
});

function updateViewerWidth() {
  const stage = stageRef.value;
  const host = viewerRef.value;
  if (!stage || !host) return;
  const ratio = viewer?.slideWidth && viewer.slideHeight
    ? viewer.slideWidth / viewer.slideHeight
    : 16 / 9;
  const availableWidth = Math.max(280, stage.clientWidth - 96);
  const availableHeight = Math.max(180, stage.clientHeight - 116);
  host.style.width = `${Math.max(280, Math.min(availableWidth, availableHeight * ratio))}px`;
}

async function goToSlide(index: number) {
  if (!viewer || index < 0 || index >= slideCount.value) return;
  await viewer.goToSlide(index);
  currentSlide.value = viewer.currentSlideIndex;
}

async function setZoom(value: number) {
  if (!viewer) return;
  zoom.value = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.round(value)));
  await viewer.setZoom(zoom.value);
}

async function loadPresentation() {
  loading.value = true;
  error.value = "";
  try {
    if (props.file.size > MAX_FILE_BYTES) {
      throw new Error(`PPTX 超过 ${formatSize(MAX_FILE_BYTES)}，请下载后查看`);
    }
    const bytes = await filesApi.binaryPreviewBytes(
      props.accountId,
      props.file.id,
      props.file.name,
      MAX_FILE_BYTES,
      controller.signal,
    );
    if (controller.signal.aborted || !viewerRef.value) return;

    const { PptxViewer: Viewer, RECOMMENDED_ZIP_LIMITS } = await import("@aiden0z/pptx-renderer");
    if (controller.signal.aborted || !viewerRef.value) return;
    updateViewerWidth();
    viewer = new Viewer(viewerRef.value, {
      fitMode: "contain",
      zoomPercent: zoom.value,
      zipLimits: RECOMMENDED_ZIP_LIMITS,
      lazyMedia: true,
      lazySlides: true,
      pdfjs: false,
      onSlideChange: (index) => {
        currentSlide.value = index;
      },
    });
    await viewer.open(bytes, {
      renderMode: "slide",
      signal: controller.signal,
      lazyMedia: true,
      lazySlides: true,
    });
    if (controller.signal.aborted) return;
    slideCount.value = viewer.slideCount;
    currentSlide.value = viewer.currentSlideIndex;
    if (!slideCount.value) throw new Error("文件中没有可显示的幻灯片");
    updateViewerWidth();
  } catch (reason) {
    if (controller.signal.aborted) return;
    error.value = reason instanceof Error ? reason.message : "PPTX 解析失败";
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
  if (event.key === "ArrowLeft" || event.key === "PageUp") {
    event.preventDefault();
    void goToSlide(currentSlide.value - 1);
  } else if (event.key === "ArrowRight" || event.key === "PageDown" || event.key === " ") {
    event.preventDefault();
    void goToSlide(currentSlide.value + 1);
  } else if (event.key === "Home") {
    event.preventDefault();
    void goToSlide(0);
  } else if (event.key === "End") {
    event.preventDefault();
    void goToSlide(slideCount.value - 1);
  } else if (event.key === "+" || event.key === "=") {
    event.preventDefault();
    void setZoom(zoom.value + ZOOM_STEP);
  } else if (event.key === "-") {
    event.preventDefault();
    void setZoom(zoom.value - ZOOM_STEP);
  } else if (event.key === "0") {
    event.preventDefault();
    void setZoom(100);
  }
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  window.addEventListener("resize", updateViewerWidth);
  void loadPresentation();
});

onUnmounted(() => {
  controller.abort();
  viewer?.destroy();
  viewer = null;
  window.removeEventListener("keydown", handleKeydown);
  window.removeEventListener("resize", updateViewerWidth);
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview pptx-preview" role="dialog" aria-modal="true" aria-label="PPTX 预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前演示文稿"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section ref="stageRef" class="pptx-preview__stage">
        <div ref="viewerRef" class="pptx-preview__viewer" />

        <div v-if="loading" class="pptx-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>正在解析 PPTX…</strong>
        </div>

        <div v-else-if="error" class="pptx-preview__state pptx-preview__error" role="alert">
          <i class="fa-solid fa-file-powerpoint" aria-hidden="true" />
          <strong>无法预览这个演示文稿</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载文件</button>
        </div>
      </section>

      <PreviewSideNavigation
        v-if="!loading && !error && slideCount > 1"
        direction="previous"
        placement="viewport"
        label="侧边上一页"
        title="上一页（←）"
        :disabled="currentSlide <= 0"
        @click="goToSlide(currentSlide - 1)"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 18-6-6 6-6" /></svg>
      </PreviewSideNavigation>

      <PreviewSideNavigation
        v-if="!loading && !error && slideCount > 1"
        direction="next"
        placement="viewport"
        label="侧边下一页"
        title="下一页（→）"
        :disabled="currentSlide + 1 >= slideCount"
        @click="goToSlide(currentSlide + 1)"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg>
      </PreviewSideNavigation>

      <nav v-if="!loading && !error" class="pptx-preview__toolbar" aria-label="幻灯片工具栏">
        <button type="button" aria-label="第一页" title="第一页（Home）" :disabled="currentSlide <= 0" @click="goToSlide(0)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5v14M18 6l-8 6 8 6V6z" /></svg>
        </button>
        <button type="button" aria-label="上一页" title="上一页（←）" :disabled="currentSlide <= 0" @click="goToSlide(currentSlide - 1)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 18-6-6 6-6" /></svg>
        </button>
        <span class="pptx-preview__counter">{{ currentSlide + 1 }} / {{ slideCount }}</span>
        <button type="button" aria-label="下一页" title="下一页（→）" :disabled="currentSlide + 1 >= slideCount" @click="goToSlide(currentSlide + 1)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg>
        </button>
        <button type="button" aria-label="最后一页" title="最后一页（End）" :disabled="currentSlide + 1 >= slideCount" @click="goToSlide(slideCount - 1)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M19 5v14M6 6l8 6-8 6V6z" /></svg>
        </button>
        <i class="pptx-preview__divider" aria-hidden="true" />
        <button type="button" aria-label="缩小" title="缩小（-）" :disabled="zoom <= MIN_ZOOM" @click="setZoom(zoom - ZOOM_STEP)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14" /></svg>
        </button>
        <button type="button" class="pptx-preview__scale" aria-label="重置缩放" title="重置缩放（0）" @click="setZoom(100)">
          {{ zoom }}%
        </button>
        <button type="button" aria-label="放大" title="放大（+）" :disabled="zoom >= MAX_ZOOM" @click="setZoom(zoom + ZOOM_STEP)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
        </button>
      </nav>
    </main>
  </Teleport>
</template>

<style scoped>
.pptx-preview__stage {
  position: absolute;
  inset: 70px 0 0;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 28px 48px 88px;
  overflow: auto;
  scrollbar-color: rgb(105 127 158 / 70%) transparent;
  background: radial-gradient(circle at 50% 0, rgb(34 74 130 / 22%), transparent 43%), #020711;
}

.pptx-preview__viewer {
  flex: 0 0 auto;
  min-width: 280px;
  overflow: visible;
}

.pptx-preview__viewer :deep(> div) {
  box-shadow: 0 24px 80px rgb(0 0 0 / 55%) !important;
}

.pptx-preview__state {
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

.pptx-preview__state strong { font-size: 13px; font-weight: 550; }


.pptx-preview__error {
  width: min(430px, calc(100vw - 32px));
  flex-direction: column;
  padding: 24px;
  text-align: center;
}

.pptx-preview__error > i { color: #ff9a63; font-size: 32px; }
.pptx-preview__error span { color: #9eb0c8; font-size: 13px; line-height: 1.7; }
.pptx-preview__error button {
  margin-top: 4px;
  padding: 9px 18px;
  border: 1px solid rgb(104 169 255 / 36%);
  border-radius: 8px;
  background: rgb(25 116 230 / 20%);
}

.pptx-preview__toolbar {
  position: fixed;
  bottom: 18px;
  left: 50%;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 7px;
  border: 1px solid rgb(145 174 216 / 20%);
  border-radius: 12px;
  background: rgb(5 14 28 / 88%);
  box-shadow: 0 16px 46px rgb(0 0 0 / 42%);
  backdrop-filter: blur(16px);
  transform: translateX(-50%);
}

.pptx-preview__toolbar button {
  width: 38px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 7px;
  background: transparent;
  font-size: 12px;
}

.pptx-preview__toolbar button:hover:not(:disabled) { background: rgb(255 255 255 / 10%); }
.pptx-preview__toolbar button:disabled { cursor: default; opacity: 0.25; }
.pptx-preview__toolbar svg {
  width: 17px;
  height: 17px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.pptx-preview__toolbar .pptx-preview__scale { width: 50px; color: #8fc7ff; font-size: 11px; font-weight: 650; }
.pptx-preview__counter { min-width: 54px; color: #d8e5f7; text-align: center; font-size: 11px; font-variant-numeric: tabular-nums; }
.pptx-preview__divider { width: 1px; height: 24px; margin: 0 4px; background: rgb(145 174 216 / 18%); }


@media (max-width: 640px) {
  .pptx-preview__stage { inset: 58px 0 0; padding: 18px 16px 82px; }
  .pptx-preview__toolbar { bottom: 10px; }
  .pptx-preview__toolbar button:first-child,
  .pptx-preview__toolbar button:nth-child(5) { display: none; }
}
</style>
