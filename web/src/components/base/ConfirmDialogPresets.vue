<script setup lang="ts">
import UploadNoticeContent from "@/components/upload/UploadNoticeContent.vue";
import CrossTransferProbeNoticeDialog from "@/components/admin/CrossTransferProbeNoticeDialog.vue";
import type { ConfirmPreset } from "@/types/confirm";

defineProps<{
  preset: ConfirmPreset;
  presetData?: Record<string, unknown>;
}>();
</script>

<template>
  <UploadNoticeContent v-if="preset === 'upload-notice'" />
  <CrossTransferProbeNoticeDialog v-else-if="preset === 'cross-transfer-probe-notice'" />

  <div v-else-if="preset === 'upload-conflict'" class="confirm-modal__conflict">
    <p class="confirm-modal__message">检测到同名文件，文件名</p>
    <p class="confirm-modal__filename">{{ String(presetData?.fileName || "") }}</p>
    <p class="confirm-modal__message">已存在，请选择处理方式：</p>
  </div>
</template>
