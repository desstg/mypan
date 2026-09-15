<script setup lang="ts">
import BusySpinner from "@/components/base/BusySpinner.vue";
import type { AdminRunStatusVariant } from "@/components/admin/adminRunStatus";

defineProps<{
  variant: AdminRunStatusVariant;
}>();
</script>

<template>
  <span class="admin-status-icon" :class="`admin-status-icon--${variant}`" aria-hidden="true">
    <BusySpinner v-if="variant === 'running'" variant="notch" :size="14" />
    <svg
      v-else-if="variant === 'success'"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="m3.5 8.5 3 3 6-7" />
    </svg>
    <svg
      v-else-if="variant === 'error'"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M8 4.25v4.5" />
      <circle cx="8" cy="11.75" r=".75" fill="currentColor" stroke="none" />
      <path d="M8 1.75 14 14.25H2L8 1.75Z" />
    </svg>
    <svg
      v-else
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="8" cy="8" r="5.75" />
      <path d="M8 5.25v3.25l2 1.25" />
    </svg>
  </span>
</template>

<style scoped>
.admin-status-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}

.admin-status-icon svg {
  width: 16px;
  height: 16px;
}

.admin-status-icon--success {
  color: var(--success);
}

.admin-status-icon--error {
  color: var(--danger);
}

.admin-status-icon--pending {
  color: var(--text-muted);
}

.admin-status-icon--running {
  color: var(--brand);
}
</style>
