<script setup lang="ts">
import BusySpinner from "@/components/base/BusySpinner.vue";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  RenderingCancelledException,
  TextLayer,
  type PDFDocumentProxy,
  type PDFPageProxy,
  type RenderTask,
} from "pdfjs-dist";

const props = defineProps<{
  document: PDFDocumentProxy;
  pageNumber: number;
  scale: number;
  rotation: number;
  scrollRoot: HTMLElement | null;
  searchQuery: string;
  activeSearch: boolean;
}>();

const emit = defineEmits<{
  ready: [payload: { pageNumber: number; width: number; height: number }];
}>();

const rootRef = ref<HTMLElement | null>(null);
const canvasRef = ref<HTMLCanvasElement | null>(null);
const textLayerRef = ref<HTMLDivElement | null>(null);
const width = ref(794);
const height = ref(1123);
const active = ref(false);
const rendered = ref(false);
const rendering = ref(false);
let page: PDFPageProxy | null = null;
let renderTask: RenderTask | null = null;
let textLayer: TextLayer | null = null;
let observer: IntersectionObserver | null = null;
let releaseTimer = 0;
let renderSequence = 0;

const pageStyle = computed(() => ({
  width: `${Math.max(1, Math.round(width.value * props.scale))}px`,
  height: `${Math.max(1, Math.round(height.value * props.scale))}px`,
}));

async function ensurePage() {
  if (!page) page = await props.document.getPage(props.pageNumber);
  const viewport = page.getViewport({ scale: 1, rotation: props.rotation });
  width.value = viewport.width;
  height.value = viewport.height;
  emit("ready", { pageNumber: props.pageNumber, width: viewport.width, height: viewport.height });
  return page;
}

async function renderPage() {
  const canvas = canvasRef.value;
  const textContainer = textLayerRef.value;
  if (!canvas || !textContainer || !active.value) return;
  const sequence = ++renderSequence;
  renderTask?.cancel();
  rendering.value = true;
  try {
    const currentPage = await ensurePage();
    if (sequence !== renderSequence || !active.value) return;
    const viewport = currentPage.getViewport({ scale: props.scale, rotation: props.rotation });
    const outputScale = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.max(1, Math.floor(viewport.width * outputScale));
    canvas.height = Math.max(1, Math.floor(viewport.height * outputScale));
    canvas.style.width = `${Math.round(viewport.width)}px`;
    canvas.style.height = `${Math.round(viewport.height)}px`;
    renderTask = currentPage.render({
      canvas,
      viewport,
      transform: outputScale === 1 ? undefined : [outputScale, 0, 0, outputScale, 0, 0],
    });
    await renderTask.promise;
    if (sequence === renderSequence) {
      textLayer?.cancel();
      textContainer.replaceChildren();
      textLayer = new TextLayer({
        textContentSource: currentPage.streamTextContent({ includeMarkedContent: true }),
        container: textContainer,
        viewport,
      });
      await textLayer.render();
      updateSearchHighlights();
      rendered.value = true;
    }
  } catch (reason) {
    if (!(reason instanceof RenderingCancelledException)) throw reason;
  } finally {
    if (sequence === renderSequence) rendering.value = false;
  }
}

function updateSearchHighlights() {
  const query = props.searchQuery.trim().toLocaleLowerCase();
  textLayerRef.value?.querySelectorAll("span").forEach((span) => {
    const matched = !!query && (span.textContent || "").toLocaleLowerCase().includes(query);
    span.classList.toggle("pdf-text-match", matched);
    span.classList.toggle("is-current", matched && props.activeSearch);
  });
}

function releaseCanvas() {
  if (active.value) return;
  renderSequence += 1;
  renderTask?.cancel();
  textLayer?.cancel();
  textLayer = null;
  const canvas = canvasRef.value;
  if (canvas) {
    canvas.width = 1;
    canvas.height = 1;
  }
  textLayerRef.value?.replaceChildren();
  page?.cleanup();
  rendered.value = false;
  rendering.value = false;
}

watch(() => [props.scale, props.rotation], async () => {
  await ensurePage();
  rendered.value = false;
  if (active.value) void renderPage();
});

watch(() => [props.searchQuery, props.activeSearch], updateSearchHighlights);

onMounted(async () => {
  await ensurePage();
  observer = new IntersectionObserver((entries) => {
    active.value = entries.some((entry) => entry.isIntersecting);
    window.clearTimeout(releaseTimer);
    if (active.value) void renderPage();
    else releaseTimer = window.setTimeout(releaseCanvas, 800);
  }, { root: props.scrollRoot, rootMargin: "1200px 0px" });
  if (rootRef.value) observer.observe(rootRef.value);
});

onUnmounted(() => {
  renderSequence += 1;
  renderTask?.cancel();
  textLayer?.cancel();
  observer?.disconnect();
  window.clearTimeout(releaseTimer);
  page?.cleanup();
});
</script>

<template>
  <article
    ref="rootRef"
    class="pdf-page"
    :class="{ 'is-rendered': rendered }"
    :style="pageStyle"
    :data-pdf-page="pageNumber"
    :aria-label="`PDF 第 ${pageNumber} 页`"
  >
    <canvas ref="canvasRef" />
    <div ref="textLayerRef" class="textLayer pdf-page__text" />
    <BusySpinner v-if="rendering && !rendered" variant="notch" :size="22" color="#1687ff" />
    <small>{{ pageNumber }}</small>
  </article>
</template>

<style scoped>
.pdf-page {
  position: relative;
  flex: 0 0 auto;
  overflow: hidden;
  background: #fff;
  box-shadow: 0 20px 65px rgb(0 0 0 / 46%);
  transition: width 120ms ease, height 120ms ease;
}

.pdf-page canvas {
  display: block;
  opacity: 0;
}
.pdf-page__text {
  position: absolute;
  inset: 0;
  overflow: hidden;
  line-height: 1;
  opacity: 1;
}
.pdf-page__text :deep(span) { cursor: text; }
.pdf-page__text :deep(.pdf-text-match) {
  color: transparent;
  background: rgb(255 221 73 / 55%);
  border-radius: 2px;
}
.pdf-page__text :deep(.pdf-text-match.is-current) { background: rgb(255 151 51 / 78%); }
.pdf-page.is-rendered canvas { opacity: 1; }

.pdf-page small {
  position: absolute;
  right: 10px;
  bottom: 8px;
  padding: 2px 6px;
  border-radius: 5px;
  color: #6e7b8c;
  background: rgb(255 255 255 / 82%);
  font-size: 10px;
  pointer-events: none;
}
.pdf-page.is-rendered small { opacity: 0; }


</style>
