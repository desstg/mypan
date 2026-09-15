<script setup lang="ts">
defineProps<{
  title: string;
  hint?: string;
}>();
</script>

<template>
  <button type="button" class="settings-entry-card">
    <div class="settings-entry-card__icon" aria-hidden="true">
      <svg viewBox="0 0 24 24" focusable="false">
        <path
          d="M12 8.2a3.8 3.8 0 1 1 0 7.6 3.8 3.8 0 0 1 0-7.6Zm0 1.8a2 2 0 1 0 0 4 2 2 0 0 0 0-4Z"
        />
        <path
          d="M13.1 2.75c.5 0 .92.35 1.02.84l.32 1.56c.38.14.74.3 1.08.5l1.36-.86c.42-.27.98-.2 1.34.16l.83.83c.36.36.43.92.16 1.34l-.86 1.36c.2.34.36.7.5 1.08l1.56.32c.49.1.84.52.84 1.02v1.2c0 .5-.35.92-.84 1.02l-1.56.32c-.14.38-.3.74-.5 1.08l.86 1.36c.27.42.2.98-.16 1.34l-.83.83c-.36.36-.92.43-1.34.16l-1.36-.86c-.34.2-.7.36-1.08.5l-.32 1.56c-.1.49-.52.84-1.02.84h-1.2c-.5 0-.92-.35-1.02-.84l-.32-1.56c-.38-.14-.74-.3-1.08-.5l-1.36.86c-.42.27-.98.2-1.34-.16l-.83-.83c-.36-.36-.43-.92-.16-1.34l.86-1.36c-.2-.34-.36-.7-.5-1.08l-1.56-.32a1.05 1.05 0 0 1-.84-1.02v-1.2c0-.5.35-.92.84-1.02l1.56-.32c.14-.38.3-.74.5-1.08l-.86-1.36c-.27-.42-.2-.98.16-1.34l.83-.83c.36-.36.92-.43 1.34-.16l1.36.86c.34-.2.7-.36 1.08-.5l.32-1.56c.1-.49.52-.84 1.02-.84h1.2Zm-.5 1.8h-.2l-.36 1.72a.9.9 0 0 1-.63.68 5.62 5.62 0 0 0-1.38.57.9.9 0 0 1-.93-.04l-1.5-.95-.14.14.95 1.5c.18.29.18.65.04.93-.24.43-.43.9-.57 1.38a.9.9 0 0 1-.68.63l-1.72.36v.2l1.72.36c.33.07.6.31.68.63.14.48.33.95.57 1.38.16.28.14.64-.04.93l-.95 1.5.14.14 1.5-.95a.9.9 0 0 1 .93-.04c.43.24.9.43 1.38.57.32.09.56.35.63.68l.36 1.72h.2l.36-1.72c.07-.33.31-.6.63-.68.48-.14.95-.33 1.38-.57a.9.9 0 0 1 .93.04l1.5.95.14-.14-.95-1.5a.9.9 0 0 1-.04-.93c.24-.43.43-.9.57-1.38.09-.32.35-.56.68-.63l1.72-.36v-.2l-1.72-.36a.9.9 0 0 1-.68-.63 5.62 5.62 0 0 0-.57-1.38.9.9 0 0 1 .04-.93l.95-1.5-.14-.14-1.5.95a.9.9 0 0 1-.93.04 5.62 5.62 0 0 0-1.38-.57.9.9 0 0 1-.63-.68l-.36-1.72Z"
        />
      </svg>
    </div>
    <div class="settings-entry-card__main">
      <div class="settings-entry-card__title">{{ title }}</div>
      <div class="settings-entry-card__hint">
        <span class="settings-entry-card__hint-action">点击打开设置 &gt;</span>
      </div>
    </div>
  </button>
</template>

<style scoped>
.settings-entry-card {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 108px;
  padding: 18px 18px 18px 20px;
  border-radius: var(--radius-xl);
  border: 1px solid rgba(99, 121, 168, 0.16);
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 0.98), rgba(247, 250, 255, 0.94)),
    var(--surface);
  box-shadow: 0 10px 28px rgba(15, 23, 42, 0.05);
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.settings-entry-card:hover {
  border-color: rgba(99, 121, 168, 0.16);
  background:
    linear-gradient(135deg, rgba(255, 255, 255, 1), rgba(238, 246, 255, 0.98)),
    var(--surface);
  box-shadow: 0 10px 28px rgba(15, 23, 42, 0.05);
}

.settings-entry-card:focus-visible {
  outline: 3px solid rgba(76, 116, 223, 0.18);
  outline-offset: 2px;
}

.settings-entry-card__icon {
  width: 46px;
  height: 46px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: rgba(244, 248, 255, 0.92);
  border: 1px solid rgba(76, 116, 223, 0.18);
  color: var(--brand, #4c74df);
  flex-shrink: 0;
}

.settings-entry-card__icon svg {
  width: 22px;
  height: 22px;
  display: block;
  fill: currentColor;
}

.settings-entry-card__main {
  min-width: 0;
  flex: 1;
}

.settings-entry-card__title {
  font-size: 16px;
  font-weight: 700;
  line-height: 1.2;
  color: var(--text);
}

.settings-entry-card__hint {
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.35;
  color: var(--text-muted);
}

.settings-entry-card__hint-action {
  color: var(--brand, #4c74df);
  font-weight: 600;
}

:global(:root[data-theme="dark"] .settings-entry-card) {
  border-color: rgba(115, 135, 174, 0.24);
  background:
    linear-gradient(135deg, rgba(30, 35, 43, 0.96), rgba(24, 29, 38, 0.94)),
    var(--surface);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.16);
}

:global(:root[data-theme="dark"] .settings-entry-card:hover) {
  border-color: rgba(115, 135, 174, 0.24);
  background:
    linear-gradient(135deg, rgba(33, 39, 49, 0.98), rgba(25, 34, 47, 0.96)),
    var(--surface);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.16);
}

:global(:root[data-theme="dark"] .settings-entry-card__icon) {
  background: color-mix(in srgb, var(--brand) 10%, var(--surface-muted));
  border-color: color-mix(in srgb, var(--brand) 24%, var(--border));
  color: color-mix(in srgb, var(--brand) 72%, #ffffff);
}

:global(:root[data-theme="dark"] .settings-entry-card__hint-action) {
  color: color-mix(in srgb, var(--brand) 72%, #ffffff);
}

@media (max-width: 860px) {
  .settings-entry-card {
    min-height: 96px;
  }
}
</style>
