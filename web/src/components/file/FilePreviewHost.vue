<script setup lang="ts">
import { computed, defineAsyncComponent } from "vue";
import type { FileItem } from "@/api/types";
import type { ActiveFilePreview, FilePreviewKind } from "./filePreview";

const props = defineProps<{
  accountId: number;
  files: FileItem[];
  active: ActiveFilePreview;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

const previewComponents = {
  video: defineAsyncComponent(() => import("./VideoPreview.vue")),
  audio: defineAsyncComponent(() => import("./AudioPreview.vue")),
  image: defineAsyncComponent(() => import("./ImagePreview.vue")),
  text: defineAsyncComponent(() => import("./TextPreview.vue")),
  pdf: defineAsyncComponent(() => import("./PdfPreview.vue")),
  docx: defineAsyncComponent(() => import("./DocxPreview.vue")),
  spreadsheet: defineAsyncComponent(() => import("./SpreadsheetPreview.vue")),
  archive: defineAsyncComponent(() => import("./ArchivePreview.vue")),
  pptx: defineAsyncComponent(() => import("./PptxPreview.vue")),
} satisfies Record<FilePreviewKind, ReturnType<typeof defineAsyncComponent>>;

const mediaPreviewKinds = new Set<FilePreviewKind>(["video", "audio", "image"]);
const activeComponent = computed(() => previewComponents[props.active.kind]);
const activeProps = computed(() =>
  mediaPreviewKinds.has(props.active.kind)
    ? { files: props.files, initialFileId: props.active.file.id }
    : { file: props.active.file },
);

function forwardDownload(file: FileItem) {
  emit("download", file);
}
</script>

<template>
  <component
    :is="activeComponent"
    :account-id="accountId"
    v-bind="activeProps"
    @close="emit('close')"
    @download="forwardDownload"
  />
</template>
