<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import Panzoom, { type PanzoomEventDetail, type PanzoomObject } from "@panzoom/panzoom";
import "@fortawesome/fontawesome-free/css/all.min.css";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { fileKind } from "@/utils/fileIcon";
import PreviewHeader from "./PreviewHeader.vue";
import PreviewSideNavigation from "./PreviewSideNavigation.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const props = defineProps<{
  accountId: number;
  files: FileItem[];
  initialFileId: string;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const imageRef = ref<HTMLImageElement | null>(null);
const loading = ref(true);
const loadError = ref(false);
const resolvedImageURL = ref("");
const scale = ref(1);
const rotation = ref(0);
const notice = ref("");
let panzoom: PanzoomObject | null = null;
let noticeTimer: number | undefined;
let imageController: AbortController | null = null;
let decodedObjectURL = "";

const images = computed(() =>
  props.files
    .filter((file) => !file.is_dir && fileKind(file) === "image")
    .sort((left, right) =>
      left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: "base" }),
    ),
);

const initialIndex = images.value.findIndex((file) => file.id === props.initialFileId);
const currentIndex = ref(initialIndex >= 0 ? initialIndex : 0);
const currentFile = computed(() => images.value[currentIndex.value] ?? null);
const imageURL = computed(() => {
  const file = currentFile.value;
  return file ? filesApi.previewURL(props.accountId, file.id, file.name) : "";
});
const statusText = computed(() => `正在查看第${currentIndex.value + 1}张/共${images.value.length}张`);
const zoomText = computed(() => `${Math.round(scale.value * 100)}%`);

function showNotice(message: string) {
  notice.value = message;
  window.clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => {
    notice.value = "";
  }, 1400);
}

function applyTransform(element: HTMLElement | SVGElement, values: { x: number; y: number; scale: number }) {
  element.style.transform = `translate(${values.x}px, ${values.y}px) scale(${values.scale}) rotate(${rotation.value}deg)`;
}

function destroyPanzoom() {
  panzoom?.destroy();
  panzoom = null;
}

function clearDecodedImage() {
  imageController?.abort();
  imageController = null;
  if (decodedObjectURL) URL.revokeObjectURL(decodedObjectURL);
  decodedObjectURL = "";
}

async function prepareImage() {
  clearDecodedImage();
  const file = currentFile.value;
  if (!file) return;
  const ext = file.name.split(".").pop()?.toLowerCase();
  if (ext !== "heic" && ext !== "heif") {
    resolvedImageURL.value = imageURL.value;
    return;
  }
  resolvedImageURL.value = "";
  loading.value = true;
  loadError.value = false;
  const controller = new AbortController();
  imageController = controller;
  try {
    const response = await fetch(filesApi.proxyPreviewURL(props.accountId, file.id, file.name), {
      credentials: "include",
      signal: controller.signal,
    });
    if (!response.ok) throw new Error(`图片读取失败 (${response.status})`);
    const source = await response.blob();
    const { heicTo } = await import("heic-to/csp");
    const decoded = await heicTo({ blob: source, type: "image/jpeg", quality: 0.95 });
    if (controller.signal.aborted) return;
    decodedObjectURL = URL.createObjectURL(decoded);
    resolvedImageURL.value = decodedObjectURL;
  } catch (reason) {
    if (controller.signal.aborted) return;
    loading.value = false;
    loadError.value = true;
    showNotice(reason instanceof Error ? reason.message : "HEIC/HEIF 解码失败");
  }
}

function initPanzoom() {
  const image = imageRef.value;
  if (!image) return;
  destroyPanzoom();
  scale.value = 1;
  rotation.value = 0;
  panzoom = Panzoom(image, {
    canvas: true,
    maxScale: 8,
    minScale: 0.2,
    step: 0.2,
    panOnlyWhenZoomed: true,
    setTransform: applyTransform,
  });
}

function handleZoom(event: Event) {
  scale.value = (event as CustomEvent<PanzoomEventDetail>).detail.scale;
}

function handleWheel(event: WheelEvent) {
  if (!panzoom || loadError.value) return;
  event.preventDefault();
  const modeMultiplier = event.deltaMode === WheelEvent.DOM_DELTA_LINE
    ? 16
    : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
      ? 100
      : 1;
  const delta = Math.max(-80, Math.min(80, event.deltaY * modeMultiplier));
  if (Math.abs(delta) < 0.1) return;
  setScale(panzoom.getScale() * Math.exp(-delta * 0.0012), false);
}

function setScale(target: number, animate: boolean) {
  if (!panzoom) return;
  const pan = panzoom.getPan();
  const next = Math.max(0.2, Math.min(8, target));
  const result = panzoom.zoom(next, { animate });
  panzoom.pan(pan.x, pan.y, { animate, force: true });
  scale.value = result.scale;
}

function zoom(direction: -1 | 1) {
  if (!panzoom) return;
  setScale(panzoom.getScale() * (direction > 0 ? 1.2 : 1 / 1.2), true);
}

function resetView(message = "已适应窗口") {
  rotation.value = 0;
  const result = panzoom?.reset({ animate: true });
  scale.value = result?.scale ?? 1;
  if (imageRef.value && result) applyTransform(imageRef.value, result);
  showNotice(message);
}

function rotateImage() {
  if (!panzoom || !imageRef.value) return;
  rotation.value = (rotation.value + 90) % 360;
  const pan = panzoom.getPan();
  applyTransform(imageRef.value, { ...pan, scale: panzoom.getScale() });
  showNotice(`已旋转 ${rotation.value}°`);
}

function toggleZoom() {
  if (!panzoom) return;
  if (scale.value > 1.05) {
    resetView();
    return;
  }
  setScale(2, true);
}

function selectImage(index: number) {
  if (index < 0 || index >= images.value.length || index === currentIndex.value) return;
  destroyPanzoom();
  currentIndex.value = index;
  loading.value = true;
  loadError.value = false;
  scale.value = 1;
  rotation.value = 0;
  showNotice(images.value[index].name);
  void nextTick(prepareImage);
}

function selectAdjacent(direction: -1 | 1) {
  const target = currentIndex.value + direction;
  if (target < 0 || target >= images.value.length) {
    showNotice(direction > 0 ? "已经是最后一张图片" : "已经是第一张图片");
    return;
  }
  selectImage(target);
}

function handleImageLoad() {
  loading.value = false;
  loadError.value = false;
  void nextTick(initPanzoom);
}

function handleImageError() {
  loading.value = false;
  loadError.value = true;
  destroyPanzoom();
}

function handleKeydown(event: KeyboardEvent) {
  const key = event.key.toLowerCase();
  if (["arrowleft", "arrowright", "+", "=", "-", "0"].includes(key)) event.preventDefault();
  if (key === "escape") emit("close");
  else if (key === "arrowleft") selectAdjacent(-1);
  else if (key === "arrowright") selectAdjacent(1);
  else if (key === "+" || key === "=") zoom(1);
  else if (key === "-") zoom(-1);
  else if (key === "0") resetView();
  else if (key === "r") rotateImage();
}

function downloadCurrent() {
  if (currentFile.value) emit("download", currentFile.value);
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  void prepareImage();
});

onUnmounted(() => {
  destroyPanzoom();
  clearDecodedImage();
  window.clearTimeout(noticeTimer);
  window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview image-preview" role="dialog" aria-modal="true" aria-label="图片预览">
      <PreviewHeader
        :file-name="currentFile?.name"
        :status="statusText"
        download-label="下载当前图片"
        @close="emit('close')"
        @download="downloadCurrent"
      />

      <section class="image-preview__stage" @wheel="handleWheel">
        <PreviewSideNavigation
          v-if="images.length > 1"
          direction="previous"
          label="上一张图片"
          title="上一张"
          :disabled="currentIndex <= 0"
          @click="selectAdjacent(-1)"
        >
          <i class="fa-solid fa-chevron-left" aria-hidden="true" />
        </PreviewSideNavigation>

        <img
          v-if="currentFile && resolvedImageURL"
          ref="imageRef"
          :key="currentFile.id"
          :src="resolvedImageURL"
          :alt="currentFile.name"
          draggable="false"
          @load="handleImageLoad"
          @error="handleImageError"
          @dblclick="toggleZoom"
          @panzoomzoom="handleZoom"
          @panzoomreset="handleZoom"
        />

        <Transition name="notice">
          <div v-if="notice" class="image-preview__notice" role="status">{{ notice }}</div>
        </Transition>

        <div v-if="loading && !loadError" class="image-preview__loading" role="status">
          <BusySpinner :size="19" color="#1687ff" />
          <b>正在加载图片…</b>
        </div>

        <div v-if="loadError" class="image-preview__error" role="alert">
          <i class="fa-solid fa-image" aria-hidden="true" />
          <strong>浏览器无法显示这张图片</strong>
          <span>可能是 HEIC 等浏览器不支持的格式，也可能是图片链接已经失效。</span>
          <button type="button" @click="downloadCurrent">下载图片</button>
        </div>

        <PreviewSideNavigation
          v-if="images.length > 1"
          direction="next"
          label="下一张图片"
          title="下一张"
          :disabled="currentIndex >= images.length - 1"
          @click="selectAdjacent(1)"
        >
          <i class="fa-solid fa-chevron-right" aria-hidden="true" />
        </PreviewSideNavigation>
      </section>

      <footer class="image-preview__bottom">
        <div class="image-preview__toolbar">
          <button type="button" aria-label="缩小图片" title="缩小" @click="zoom(-1)">
            <i class="fa-solid fa-minus" aria-hidden="true" />
          </button>
          <button type="button" class="image-preview__scale" title="适应窗口" @click="resetView()">{{ zoomText }}</button>
          <button type="button" aria-label="放大图片" title="放大" @click="zoom(1)">
            <i class="fa-solid fa-plus" aria-hidden="true" />
          </button>
          <button type="button" aria-label="旋转图片" title="顺时针旋转" @click="rotateImage">
            <i class="fa-solid fa-rotate-right" aria-hidden="true" />
          </button>
          <button type="button" aria-label="适应窗口" title="适应窗口" @click="resetView()">
            <i class="fa-solid fa-expand" aria-hidden="true" />
          </button>
        </div>
      </footer>
    </main>
  </Teleport>
</template>

<style scoped>
.image-preview {
  min-height: 520px;
}

.image-preview__stage {
  position: absolute;
  inset: 70px 0 0;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  background:
    radial-gradient(circle at 50% 42%, rgb(31 66 112 / 20%), transparent 44%),
    #020711;
  touch-action: none;
}

.image-preview__stage > img {
  display: block;
  width: auto;
  height: auto;
  max-width: min(calc(100vw - 128px), 1800px);
  max-height: calc(100dvh - 94px);
  object-fit: contain;
  user-select: none;
  cursor: grab;
  box-shadow: 0 22px 70px rgb(0 0 0 / 36%);
  transform-origin: 50% 50%;
}

.image-preview__stage > img:active { cursor: grabbing; }

.image-preview__notice,
.image-preview__loading,
.image-preview__error {
  position: absolute;
  left: 50%;
  top: 50%;
  z-index: 4;
  transform: translate(-50%, -50%);
  border: 1px solid rgb(255 255 255 / 15%);
  border-radius: 10px;
  background: rgb(3 11 25 / 86%);
  box-shadow: 0 18px 55px rgb(0 0 0 / 36%);
}

.image-preview__notice {
  top: auto;
  bottom: 82px;
  max-width: min(70vw, 620px);
  padding: 9px 14px;
  overflow: hidden;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.image-preview__loading {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 13px 17px;
}


.image-preview__loading b { font-size: 13px; font-weight: 550; }

.image-preview__error {
  width: min(440px, calc(100vw - 32px));
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 24px;
  text-align: center;
}

.image-preview__error > i { color: #ffb45e; font-size: 30px; }
.image-preview__error strong { font-size: 17px; }
.image-preview__error span { color: #9eb0c8; font-size: 13px; line-height: 1.7; }
.image-preview__error button {
  margin-top: 4px;
  padding: 9px 18px;
  border: 1px solid #268bff;
  border-radius: 8px;
  background: #187ce0;
  font-weight: 650;
}


.notice-enter-active, .notice-leave-active { transition: opacity 150ms ease, transform 150ms ease; }
.notice-enter-from, .notice-leave-to { opacity: 0; transform: translate(-50%, 6px); }

.image-preview__bottom {
  position: absolute;
  left: 50%;
  bottom: 20px;
  z-index: 10;
  transform: translateX(-50%);
  padding: 4px;
  border: 1px solid rgb(151 181 224 / 18%);
  border-radius: 12px;
  background: rgb(4 13 27 / 72%);
  box-shadow: 0 12px 38px rgb(0 0 0 / 28%);
  backdrop-filter: blur(14px);
}

.image-preview__toolbar button {
  color: inherit;
  border: 0;
  background: transparent;
}

.image-preview__toolbar {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 5px;
}

.image-preview__toolbar button {
  width: 40px;
  height: 38px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  color: #d4dfed;
  font-size: 14px;
}

.image-preview__toolbar button:hover:not(:disabled) { color: #fff; background: rgb(255 255 255 / 10%); }
.image-preview__toolbar .image-preview__scale { width: 68px; color: #8fc7ff; font-size: 12px; font-weight: 650; }

@media (max-width: 760px) {
  .image-preview__stage { inset: 58px 0 0; }
  .image-preview__stage > img { max-width: calc(100vw - 64px); max-height: calc(100dvh - 76px); }
  .image-preview__bottom { bottom: 10px; }
  .image-preview__toolbar { gap: 1px; }
  .image-preview__toolbar button { width: 38px; }
}
</style>
