<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import DOMPurify from "dompurify";
import MarkdownIt from "markdown-it";
import markdownItAnchor from "markdown-it-anchor";
import "@fortawesome/fontawesome-free/css/all.min.css";
import { filesApi, TEXT_PREVIEW_MAX_BYTES } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { formatSize } from "@/utils/format";
import PreviewHeader from "./PreviewHeader.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import { decodeTextBytes, TEXT_ENCODINGS } from "@/utils/textEncoding";

const props = defineProps<{
  accountId: number;
  file: FileItem;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const content = ref("");
const encoding = ref("UTF-8");
const loading = ref(true);
const error = ref("");
const truncated = ref(false);
const rendered = ref(true);
const wrap = ref(true);
const rtfHTML = ref("");
const rawBytes = ref<Uint8Array | null>(null);
const selectedEncoding = ref("auto");
const tocOpen = ref(false);
const controller = new AbortController();

const extension = computed(() => props.file.name.split(".").pop()?.toLowerCase() || "");
const isMarkdown = computed(() => extension.value === "md" || extension.value === "markdown");
const isRtf = computed(() => extension.value === "rtf");
const previewLabel = computed(() => isRtf.value ? "RTF 预览" : isMarkdown.value ? "Markdown 预览" : "文本预览");
const statusText = computed(() => {
  if (loading.value) return previewLabel.value;
  return `${previewLabel.value} · ${encoding.value} · ${formatSize(props.file.size)}`;
});

const markdown = new MarkdownIt({
  html: false,
  linkify: true,
  typographer: false,
});
markdown.use(markdownItAnchor);

markdown.renderer.rules.link_open = (tokens, index, options, _env, self) => {
  tokens[index].attrSet("target", "_blank");
  tokens[index].attrSet("rel", "noopener noreferrer");
  return self.renderToken(tokens, index, options);
};
markdown.renderer.rules.image = (tokens, index) => {
  const token = tokens[index];
  const label = token.content || token.attrGet("src") || "图片";
  return `<span class="markdown-image-note">[图片：${markdown.utils.escapeHtml(String(label))}]</span>`;
};

const markdownDocument = computed(() => {
  const env = {};
  const tokens = markdown.parse(content.value, env);
  const headings: Array<{ id: string; level: number; title: string }> = [];
  tokens.forEach((token, index) => {
    if (token.type !== "heading_open") return;
    const inline = tokens[index + 1];
    const id = String(token.attrGet("id") || "");
    if (id && inline?.type === "inline") {
      headings.push({ id, level: Number(String(token.tag).slice(1)) || 1, title: String(inline.content) });
    }
  });
  const html = markdown.renderer.render(tokens, markdown.options, env);
  return {
    headings,
    html: DOMPurify.sanitize(html, { ADD_ATTR: ["target", "id"], FORBID_TAGS: ["img", "style"] }),
  };
});
const renderedHTML = computed(() => markdownDocument.value.html);
const toc = computed(() => markdownDocument.value.headings);

async function renderRTF(bytes: Uint8Array) {
  const { RTFJS, EMFJS, WMFJS } = await import("rtf.js");
  RTFJS.loggingEnabled(false);
  EMFJS.loggingEnabled(false);
  WMFJS.loggingEnabled(false);
  const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
  const document = new RTFJS.Document(buffer, {
    onHyperlink(create) {
      const link = create();
      link.setAttribute("target", "_blank");
      link.setAttribute("rel", "noopener noreferrer");
      return { element: link, content: link };
    },
    onImport(_url, callback) {
      callback({ error: new Error("为保护隐私，RTF 预览不会加载外部资源") });
    },
  });
  const elements = await document.render();
  const container = window.document.createElement("div");
  container.replaceChildren(...elements);
  const safeHTML = String(DOMPurify.sanitize(container.innerHTML, {
    FORBID_TAGS: ["script", "iframe", "object", "embed", "style"],
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|data:image\/(?:png|jpeg|gif|bmp);base64,|blob:)/i,
  }));
  container.innerHTML = safeHTML;
  container.querySelectorAll<HTMLAnchorElement>("a[href]").forEach((link) => {
    link.target = "_blank";
    link.rel = "noopener noreferrer";
  });
  rtfHTML.value = container.innerHTML;
}

function applyEncoding() {
  if (!rawBytes.value || isRtf.value) return;
  const decoded = decodeTextBytes(rawBytes.value, selectedEncoding.value, truncated.value);
  content.value = decoded.text;
  encoding.value = decoded.encoding.toUpperCase();
}

function jumpToHeading(id: string) {
  const target = document.getElementById(id);
  target?.scrollIntoView({ behavior: "smooth", block: "start" });
  if (window.innerWidth <= 900) tocOpen.value = false;
}

async function loadText() {
  loading.value = true;
  error.value = "";
  try {
    const result = await filesApi.textPreviewBytes(
      props.accountId,
      props.file.id,
      props.file.name,
      props.file.size,
      controller.signal,
    );
    truncated.value = result.truncated;
    rawBytes.value = result.bytes;
    if (isRtf.value) {
      if (result.truncated) throw new Error(`RTF 超过 ${formatSize(TEXT_PREVIEW_MAX_BYTES)}，请下载后查看`);
      await renderRTF(result.bytes);
      encoding.value = "RTF";
      return;
    }
    applyEncoding();
  } catch (reason) {
    if (controller.signal.aborted) return;
    error.value = reason instanceof Error ? reason.message : "文本读取失败";
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
  void loadText();
});

onUnmounted(() => {
  controller.abort();
  window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview text-preview" role="dialog" aria-modal="true" aria-label="文本预览">
      <PreviewHeader
        :file-name="file.name"
        :status="statusText"
        download-label="下载当前文件"
        @close="emit('close')"
        @download="emit('download', file)"
      />

      <section class="text-preview__stage">
        <div v-if="loading" class="text-preview__state" role="status">
          <BusySpinner variant="notch" :size="22" color="#1687ff" />
          <strong>{{ isRtf ? "正在解析 RTF…" : "正在读取文本…" }}</strong>
        </div>

        <div v-else-if="error" class="text-preview__state text-preview__state--error" role="alert">
          <i class="fa-solid fa-file-circle-exclamation" aria-hidden="true" />
          <strong>无法预览这个文件</strong>
          <span>{{ error }}</span>
          <button type="button" @click="emit('download', file)">下载文件</button>
        </div>

        <div v-else class="text-preview__document">
          <div class="text-preview__toolbar">
            <div class="text-preview__meta">
              <span>{{ isMarkdown ? "Markdown" : extension.toUpperCase() || "TEXT" }}</span>
              <span v-if="!isRtf">{{ encoding }}</span>
              <span v-if="truncated" class="is-warning">仅显示前 {{ formatSize(TEXT_PREVIEW_MAX_BYTES) }}</span>
            </div>
            <div class="text-preview__modes">
              <template v-if="isMarkdown">
                <button type="button" :class="{ 'is-active': rendered }" @click="rendered = true">阅读</button>
                <button type="button" :class="{ 'is-active': !rendered }" @click="rendered = false">源文</button>
              </template>
              <button
                v-if="!isRtf && (!isMarkdown || !rendered)"
                type="button"
                :class="{ 'is-active': wrap }"
                @click="wrap = !wrap"
              >
                自动换行
              </button>
              <select
                v-if="!isRtf && (!isMarkdown || !rendered)"
                v-model="selectedEncoding"
                aria-label="文本编码"
                title="文本编码"
                @change="applyEncoding"
              >
                <option v-for="option in TEXT_ENCODINGS" :key="option.value" :value="option.value">{{ option.label }}</option>
              </select>
              <button v-if="isMarkdown && rendered && toc.length" type="button" :class="{ 'is-active': tocOpen }" @click="tocOpen = !tocOpen">
                目录
              </button>
            </div>
          </div>

          <div v-if="truncated" class="text-preview__truncated" role="status">
            文件较大，为保证浏览器流畅，只显示开头 {{ formatSize(TEXT_PREVIEW_MAX_BYTES) }}；下载后可查看完整内容。
          </div>

          <article
            v-if="isRtf"
            class="rtf-body"
            v-html="rtfHTML"
          />
          <aside v-if="isMarkdown && rendered && tocOpen" class="markdown-toc" aria-label="文档目录">
            <header><strong>文档目录</strong><button type="button" aria-label="关闭目录" @click="tocOpen = false">×</button></header>
            <nav>
              <button
                v-for="heading in toc"
                :key="heading.id"
                type="button"
                :style="{ paddingLeft: `${12 + Math.max(0, heading.level - 1) * 14}px` }"
                @click="jumpToHeading(heading.id)"
              >{{ heading.title }}</button>
            </nav>
          </aside>
          <article
            v-else-if="isMarkdown && rendered"
            class="markdown-body"
            v-html="renderedHTML"
          />
          <pre v-else class="text-source" :class="{ 'is-nowrap': !wrap }"><code>{{ content }}</code></pre>
        </div>
      </section>
    </main>
  </Teleport>
</template>

<style scoped>
.text-preview {
  color: #e7edf7;
}

.text-preview__stage {
  position: absolute;
  inset: 70px 0 0;
  overflow: auto;
  padding: 0 0 50px;
  background:
    radial-gradient(circle at 50% 0%, rgb(29 68 118 / 18%), transparent 38%),
    #020711;
}

.text-preview__document {
  width: 100%;
  min-height: calc(100dvh - 70px);
}

.text-preview__toolbar {
  position: sticky;
  top: 0;
  z-index: 4;
  min-height: 50px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 28px;
  border-bottom: 1px solid rgb(142 172 215 / 13%);
  background: rgb(3 11 23 / 91%);
  backdrop-filter: blur(14px);
}

.text-preview__meta,
.text-preview__modes { display: flex; align-items: center; gap: 8px; }

.text-preview__meta span {
  padding: 4px 8px;
  border-radius: 6px;
  color: #8da2bd;
  background: rgb(255 255 255 / 5%);
  font-size: 10px;
  font-weight: 650;
}

.text-preview__meta .is-warning { color: #ffc47a; background: rgb(255 164 62 / 10%); }

.text-preview__modes button {
  min-height: 32px;
  padding: 0 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: #8fa2ba;
  font-size: 12px;
  font-weight: 600;
}

.text-preview__modes button:hover { color: #fff; background: rgb(255 255 255 / 7%); }
.text-preview__modes button.is-active { color: #dceeff; background: rgb(31 132 239 / 20%); }
.text-preview__modes select {
  height: 32px;
  padding: 0 8px;
  color: #a9bad0;
  border: 1px solid rgb(145 174 216 / 16%);
  border-radius: 7px;
  outline: none;
  background: #081425;
  font-size: 11px;
}

.text-preview__truncated {
  width: min(1200px, calc(100% - 56px));
  margin: 20px auto 0;
  padding: 10px 13px;
  border: 1px solid rgb(255 180 91 / 20%);
  border-radius: 8px;
  color: #ffc47a;
  background: rgb(255 164 62 / 7%);
  font-size: 12px;
  line-height: 1.6;
}

.text-source {
  width: min(1400px, 100%);
  min-height: calc(100dvh - 220px);
  margin: 0 auto;
  padding: 30px 42px 48px;
  overflow: auto;
  color: #dbe5f3;
  background: transparent;
  font: 13px/1.75 "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  tab-size: 4;
}

.text-source.is-nowrap { white-space: pre; overflow-wrap: normal; }
.text-source code { font: inherit; }

.markdown-body {
  width: min(1200px, 100%);
  margin: 0 auto;
  padding: 34px 42px 64px;
  color: #dce5f2;
  font-size: 15px;
  line-height: 1.85;
  overflow-wrap: anywhere;
}

.rtf-body {
  width: min(1200px, 100%);
  margin: 0 auto;
  padding: 34px 42px 64px;
  color: #dce5f2;
  font-size: 15px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.rtf-body :deep(*) {
  max-width: 100%;
  color: inherit !important;
  background-color: transparent !important;
}
.rtf-body :deep(p) { margin: 0.8em 0; }
.rtf-body :deep(a) { color: #60adff !important; }
.rtf-body :deep(img), .rtf-body :deep(svg) { height: auto; }

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  margin: 1.55em 0 0.65em;
  color: #f4f8ff;
  line-height: 1.35;
}
.markdown-body :deep(h1) { margin-top: 0.25em; padding-bottom: 0.35em; border-bottom: 1px solid rgb(145 174 216 / 18%); font-size: 2em; }
.markdown-body :deep(h2) { padding-bottom: 0.3em; border-bottom: 1px solid rgb(145 174 216 / 13%); font-size: 1.55em; }
.markdown-body :deep(h3) { font-size: 1.28em; }
.markdown-body :deep(p), .markdown-body :deep(ul), .markdown-body :deep(ol) { margin: 0.85em 0; }
.markdown-body :deep(li) { margin: 0.25em 0; }
.markdown-body :deep(a) { color: #60adff; text-decoration: none; }
.markdown-body :deep(a:hover) { text-decoration: underline; }
.markdown-body :deep(blockquote) { margin: 1em 0; padding: 0.2em 1em; color: #9fb0c6; border-left: 3px solid #2e8ce9; background: rgb(41 111 180 / 7%); }
.markdown-body :deep(code) { padding: 0.16em 0.4em; border-radius: 5px; color: #b9d9ff; background: rgb(98 155 218 / 11%); font: 0.9em/1.6 "SFMono-Regular", Consolas, monospace; }
.markdown-body :deep(pre) { margin: 1.1em 0; padding: 16px 18px; overflow: auto; border: 1px solid rgb(145 174 216 / 12%); border-radius: 9px; background: #030a14; }
.markdown-body :deep(pre code) { padding: 0; color: #d2deed; background: transparent; }
.markdown-body :deep(hr) { height: 1px; margin: 2em 0; border: 0; background: rgb(145 174 216 / 18%); }
.markdown-body :deep(table) { width: 100%; margin: 1em 0; border-collapse: collapse; }
.markdown-body :deep(th), .markdown-body :deep(td) { padding: 9px 12px; border: 1px solid rgb(145 174 216 / 16%); text-align: left; }
.markdown-body :deep(th) { color: #eff6ff; background: rgb(255 255 255 / 5%); }
.markdown-body :deep(.markdown-image-note) { color: #8799b0; font-style: italic; }

.markdown-toc {
  position: fixed;
  right: 18px;
  top: 138px;
  z-index: 8;
  width: min(320px, calc(100vw - 36px));
  max-height: calc(100dvh - 164px);
  overflow: hidden;
  border: 1px solid rgb(145 174 216 / 18%);
  border-radius: 11px;
  background: rgb(4 13 27 / 94%);
  box-shadow: 0 18px 55px rgb(0 0 0 / 36%);
  backdrop-filter: blur(16px);
}
.markdown-toc header { display: flex; align-items: center; justify-content: space-between; padding: 12px 14px; border-bottom: 1px solid rgb(145 174 216 / 13%); }
.markdown-toc header strong { font-size: 13px; }
.markdown-toc header button { width: 28px; height: 28px; border: 0; border-radius: 6px; background: transparent; font-size: 20px; }
.markdown-toc header button:hover { background: rgb(255 255 255 / 8%); }
.markdown-toc nav { max-height: calc(100dvh - 220px); padding: 6px; overflow: auto; }
.markdown-toc nav button { width: 100%; min-height: 34px; padding-right: 10px; overflow: hidden; text-align: left; text-overflow: ellipsis; white-space: nowrap; color: #aebed2; border: 0; border-radius: 6px; background: transparent; font-size: 12px; }
.markdown-toc nav button:hover { color: #fff; background: rgb(40 137 239 / 13%); }

.text-preview__state {
  position: absolute;
  left: 50%;
  top: 46%;
  transform: translate(-50%, -50%);
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border: 1px solid rgb(255 255 255 / 14%);
  border-radius: 10px;
  background: rgb(5 14 28 / 88%);
  box-shadow: 0 18px 55px rgb(0 0 0 / 34%);
}

.text-preview__state strong { font-size: 13px; font-weight: 550; }
.text-preview__state--error { width: min(440px, calc(100vw - 32px)); flex-direction: column; text-align: center; padding: 24px; }
.text-preview__state--error > i { color: #ffb45e; font-size: 30px; }
.text-preview__state--error span { color: #9eb0c8; font-size: 13px; line-height: 1.7; }
.text-preview__state--error button { margin-top: 4px; padding: 9px 18px; border: 1px solid #268bff; border-radius: 8px; background: #187ce0; font-weight: 650; }

@media (max-width: 760px) {
  .text-preview__stage { inset: 58px 0 0; padding-bottom: 30px; }
  .text-preview__document { min-height: calc(100dvh - 58px); }
  .text-preview__toolbar { min-height: 46px; padding: 6px 10px; }
  .text-preview__meta span:nth-child(2) { display: none; }
  .text-preview__modes button { padding: 0 9px; }
  .text-source { padding: 18px 16px 34px; font-size: 12px; }
  .markdown-body { padding: 20px 18px 40px; font-size: 14px; }
  .rtf-body { padding: 20px 18px 40px; font-size: 14px; }
  .text-preview__truncated { width: calc(100% - 24px); margin-top: 12px; }
}
</style>
