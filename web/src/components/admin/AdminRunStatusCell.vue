<script setup lang="ts">
import AdminStatusIcon from "@/components/admin/AdminStatusIcon.vue";
import type { AdminRunStatusVariant } from "@/components/admin/adminRunStatus";

withDefaults(
  defineProps<{
    title?: string;
    primary: string;
    summary?: string;
    variant: AdminRunStatusVariant;
    live?: boolean;
    textLayout?: "hover" | "column";
    primaryTone?: "muted" | "default" | "strong";
  }>(),
  {
    live: false,
    textLayout: "hover",
    primaryTone: "muted",
  },
);
</script>

<template>
  <div class="admin-run-status" :class="{ 'admin-run-status--live': live }" :title="title">
    <AdminStatusIcon :variant="variant" />
    <span
      class="admin-run-status__text"
      :class="{
        'admin-run-status__text--hover': textLayout === 'hover' && !live,
        'admin-run-status__text--live': textLayout === 'hover' && live,
        'admin-run-status__text--column': textLayout === 'column',
      }"
    >
      <span
        class="admin-run-status__primary"
        :class="{
          'admin-run-status__primary--default': primaryTone === 'default',
          'admin-run-status__primary--strong': primaryTone === 'strong',
        }"
      >
        {{ primary }}
      </span>
      <span v-if="summary" class="admin-run-status__summary">{{ summary }}</span>
    </span>
  </div>
</template>

<style scoped>
.admin-run-status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  width: 100%;
}

.admin-run-status--live {
  align-items: flex-start;
}

.admin-run-status--live :deep(.admin-status-icon) {
  margin-top: 2px;
}

.admin-run-status__text {
  min-width: 0;
}

.admin-run-status__text--hover {
  display: grid;
  flex: 1;
}

.admin-run-status__text--live,
.admin-run-status__text--column {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
}

.admin-run-status__primary,
.admin-run-status__summary {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  line-height: 1.35;
}

.admin-run-status__primary {
  color: var(--text-muted);
}

.admin-run-status__primary--default,
.admin-run-status__primary--strong {
  color: var(--text);
}

.admin-run-status__primary--strong {
  font-weight: 650;
}

.admin-run-status__summary {
  color: var(--text-muted);
  font-size: 13px;
}

.admin-run-status__text--column .admin-run-status__summary {
  font-size: 11px;
}

.admin-run-status__text--hover .admin-run-status__primary,
.admin-run-status__text--hover .admin-run-status__summary {
  grid-area: 1 / 1;
  transition: opacity 0.18s ease;
}

.admin-run-status__text--hover .admin-run-status__summary {
  opacity: 0;
}

.admin-run-status__text--hover:hover .admin-run-status__summary {
  opacity: 1;
}

.admin-run-status__text--hover:hover .admin-run-status__primary {
  opacity: 0;
}

.admin-run-status__text--live .admin-run-status__primary,
.admin-run-status__text--live .admin-run-status__summary {
  display: block;
}
</style>
