<script setup lang="ts">
export type AdminStatusPillTone = "success" | "warning" | "brand" | "danger" | "muted";

withDefaults(
  defineProps<{
    tone?: AdminStatusPillTone;
  }>(),
  { tone: "muted" },
);
</script>

<template>
  <span class="admin-status-pill" :class="`admin-status-pill--${tone}`">
    <slot />
  </span>
</template>

<style scoped>
.admin-status-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  min-width: 50px;
  height: 24px;
  padding: 0 10px;
  border-radius: var(--radius-pill, 999px);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}

.admin-status-pill--success {
  background: color-mix(in srgb, var(--success) 12%, transparent);
  color: var(--success);
}

.admin-status-pill--warning {
  background: color-mix(in srgb, var(--warning) 12%, transparent);
  color: var(--warning);
}

.admin-status-pill--brand {
  background: color-mix(in srgb, var(--brand) 12%, transparent);
  color: var(--brand);
}

.admin-status-pill--danger {
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  color: var(--danger);
}

.admin-status-pill--muted {
  color: var(--text-muted);
  background: var(--surface-sunken);
}
</style>
