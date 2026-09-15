<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";

withDefaults(
  defineProps<{
    icon?: string;
    label: string;
    variant?: "ghost" | "secondary" | "danger";
    size?: "xs" | "sm" | "md";
    disabled?: boolean;
    title?: string;
    /** 为 true 时不渲染原生 title 属性（改用自定义气泡时避免浏览器默认提示） */
    noNativeTitle?: boolean;
  }>(),
  { icon: "", variant: "ghost", size: "sm", disabled: false, title: "", noNativeTitle: false },
);

const emit = defineEmits<{ click: [MouseEvent] }>();
</script>

<template>
  <button
    type="button"
    class="icon-btn"
    :class="[`icon-btn--${variant}`, `icon-btn--${size}`]"
    :disabled="disabled"
    :title="noNativeTitle ? undefined : title || label"
    :aria-label="label"
    @click="emit('click', $event)"
  >
    <SvgIcon v-if="icon" :name="icon" :size="size === 'md' ? 16 : 14" />
    <slot v-else />
  </button>
</template>

<style scoped>
.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border: none;
  border-radius: var(--radius-sm);
  padding: 0;
  line-height: 1;
  cursor: pointer;
  transition: var(--transition);
}
.icon-btn--xs {
  width: 18px;
  height: 18px;
  font-size: 12px;
}
.icon-btn--sm {
  width: 28px;
  height: 28px;
}
.icon-btn--md {
  width: 36px;
  height: 36px;
}
.icon-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.icon-btn--ghost {
  background: transparent;
  color: var(--text-muted);
}
.icon-btn--ghost:not(:disabled):hover {
  color: var(--text-regular);
  background: var(--border-soft);
}
.icon-btn--secondary {
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-muted);
}
.icon-btn--secondary:not(:disabled):hover {
  color: var(--text-regular);
  background: var(--surface-sunken);
}
.icon-btn--danger {
  border: 1px solid color-mix(in srgb, var(--danger) 38%, var(--border));
  background: color-mix(in srgb, var(--danger) 8%, var(--surface));
  color: var(--danger);
}
.icon-btn--danger:not(:disabled):hover {
  background: color-mix(in srgb, var(--danger) 14%, var(--surface));
}
</style>
