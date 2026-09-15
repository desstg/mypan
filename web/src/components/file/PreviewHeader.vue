<script setup lang="ts">
defineProps<{
  fileName?: string;
  status?: string;
  downloadLabel?: string;
}>();

const emit = defineEmits<{
  close: [];
  download: [];
}>();
</script>

<template>
  <header class="preview-header">
    <button type="button" class="preview-header__back" @click="emit('close')">
      <i class="fa-solid fa-arrow-left" aria-hidden="true" />
      <span>返回文件</span>
    </button>

    <div v-if="fileName" class="preview-header__title">
      <strong :title="fileName">{{ fileName }}</strong>
      <span v-if="status">{{ status }}</span>
    </div>

    <div class="preview-header__actions">
      <button
        type="button"
        :aria-label="downloadLabel || '下载当前文件'"
        title="下载"
        @click="emit('download')"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 3v12m0 0 4-4m-4 4-4-4M5 20h14" />
        </svg>
      </button>
      <button type="button" aria-label="关闭预览" title="关闭" @click="emit('close')">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M5 5l14 14M19 5 5 19" />
        </svg>
      </button>
    </div>
  </header>
</template>

<style scoped>
.preview-header {
  position: absolute;
  inset: 0 0 auto;
  z-index: 20;
  height: 70px;
  display: grid;
  grid-template-columns: minmax(150px, 1fr) minmax(300px, auto) minmax(150px, 1fr);
  align-items: center;
  gap: 18px;
  padding: 0 24px 0 20px;
  color: #f4f8ff;
  border-bottom: 1px solid rgb(148 177 219 / 16%);
  background: linear-gradient(180deg, rgb(3 11 25 / 98%), rgb(3 11 25 / 88%));
  backdrop-filter: blur(18px);
}

.preview-header button {
  color: inherit;
  border: 0;
  background: transparent;
}

.preview-header__back {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 10px;
  width: max-content;
  padding: 9px 13px;
  border-radius: 8px;
  background: rgb(255 255 255 / 5%) !important;
  font-size: 15px;
  font-weight: 650;
}

.preview-header__back:hover,
.preview-header__actions button:hover { background: rgb(255 255 255 / 12%) !important; }

.preview-header__title {
  grid-column: 2;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  white-space: nowrap;
  font-size: 15px;
}

.preview-header__title strong {
  max-width: 38vw;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preview-header__title span {
  color: #8192aa;
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.02em;
}

.preview-header__actions {
  grid-column: 3;
  justify-self: end;
  display: flex;
  gap: 12px;
}

.preview-header__actions button {
  width: 44px;
  height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
}

.preview-header__actions svg {
  width: 23px;
  height: 23px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.65;
  stroke-linecap: round;
  stroke-linejoin: round;
}

@media (max-width: 1100px) {
  .preview-header { grid-template-columns: 1fr auto 1fr; }
}

@media (max-width: 760px) {
  .preview-header { height: 58px; padding: 0 8px; gap: 4px; }
  .preview-header__back span,
  .preview-header__title span,
  .preview-header__actions button:first-child { display: none; }
  .preview-header__back { padding: 9px 12px; }
  .preview-header__title strong { max-width: 50vw; font-size: 12px; }
  .preview-header__actions { gap: 0; }
}
</style>
