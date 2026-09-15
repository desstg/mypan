<script setup lang="ts">
withDefaults(
  defineProps<{
    tone?: "neutral" | "info" | "success" | "warning" | "danger";
    dot?: boolean;
    pill?: boolean;
  }>(),
  { tone: "neutral", dot: false, pill: true },
);
</script>

<template>
  <span class="app-badge" :class="[`app-badge--${tone}`, { 'app-badge--pill': pill }]">
    <span v-if="dot" class="app-badge__dot" />
    <slot />
  </span>
</template>

<style scoped>
.app-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 24px;
  padding: 2px 10px;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.2;
}
.app-badge--pill {
  border-radius: var(--radius-pill);
}
.app-badge__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}
.app-badge--neutral {
  background: var(--surface-sunken);
  color: var(--text-muted);
}
.app-badge--info {
  background: color-mix(in srgb, var(--info) 12%, var(--surface));
  color: var(--info);
}
.app-badge--success {
  background: color-mix(in srgb, var(--success) 12%, var(--surface));
  color: var(--success);
}
.app-badge--warning {
  background: color-mix(in srgb, var(--warning) 12%, var(--surface));
  color: var(--warning);
}
.app-badge--danger {
  background: color-mix(in srgb, var(--danger) 12%, var(--surface));
  color: var(--danger);
}
</style>
