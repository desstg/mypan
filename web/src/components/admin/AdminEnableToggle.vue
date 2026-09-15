<script setup lang="ts">
withDefaults(
  defineProps<{
    enabled: boolean;
    ariaLabel?: string;
    onLabel?: string;
    offLabel?: string;
    onTitle?: string;
    offTitle?: string;
    disabled?: boolean;
  }>(),
  {
    ariaLabel: "任务启用切换",
    onLabel: "启",
    offLabel: "禁",
    onTitle: "启用",
    offTitle: "禁用",
    disabled: false,
  },
);

defineEmits<{ enable: [boolean] }>();
</script>

<template>
  <div
    class="admin-enable-toggle"
    role="group"
    :aria-label="ariaLabel"
    :class="{ 'admin-enable-toggle--disabled': disabled }"
  >
    <button
      type="button"
      class="admin-enable-toggle__btn"
      :class="{ 'admin-enable-toggle__btn--active': enabled }"
      :title="onTitle"
      :disabled="disabled"
      @click="$emit('enable', true)"
    >
      {{ onLabel }}
    </button>
    <button
      type="button"
      class="admin-enable-toggle__btn"
      :class="{ 'admin-enable-toggle__btn--active': !enabled }"
      :title="offTitle"
      :disabled="disabled"
      @click="$emit('enable', false)"
    >
      {{ offLabel }}
    </button>
  </div>
</template>

<style scoped>
.admin-enable-toggle {
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 2px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--surface);
  flex-shrink: 0;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
  gap: 2px;
}

.admin-enable-toggle--disabled {
  opacity: 0.6;
}

.admin-enable-toggle__btn {
  min-width: 30px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  transition: background 0.15s, color 0.15s;
}

.admin-enable-toggle__btn:hover:not(:disabled) {
  color: var(--text);
  background: var(--surface-sunken);
}

.admin-enable-toggle__btn--active {
  background: color-mix(in srgb, var(--brand) 12%, var(--surface));
  color: var(--brand);
}

.admin-enable-toggle__btn:disabled {
  cursor: not-allowed;
}
</style>
