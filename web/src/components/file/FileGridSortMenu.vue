<script setup lang="ts">
import { computed, ref } from "vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import type { SortKey, SortOrder } from "@/types/file-browser";

const props = defineProps<{
  sortKey: SortKey;
  sortOrder: SortOrder;
}>();

const emit = defineEmits<{
  "set-sort": [payload: { key: SortKey; order: SortOrder }];
}>();

const gridSortOptions: { key: SortKey; label: string }[] = [
  { key: "name", label: "文件名" },
  { key: "modified", label: "修改时间" },
  { key: "size", label: "文件大小" },
];

const menuOpen = ref(false);

const currentSortLabel = computed(() => {
  const option = gridSortOptions.find((o) => o.key === props.sortKey);
  const label = option?.label ?? "排序";
  return `${label} ${props.sortOrder === "asc" ? "升序" : "降序"}`;
});

function applySort(key: SortKey, order: SortOrder) {
  emit("set-sort", { key, order });
  menuOpen.value = false;
}
</script>

<template>
  <AppDropdown v-model:open="menuOpen" trigger="click">
    <template #trigger="{ open, toggle }">
      <button
        type="button"
        class="grid-sort-trigger"
        :title="currentSortLabel"
        :aria-expanded="open"
        @click.stop="toggle"
      >
        <svg
          class="grid-sort-icon"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          stroke-width="1.25"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M2 4h10" />
          <path d="M2 8h8" />
          <path d="M2 12h6" />
          <path d="m12 10 2 2 2-2" />
        </svg>
      </button>
    </template>
    <template #panel>
      <div class="grid-sort-panel">
        <div v-for="option in gridSortOptions" :key="option.key" class="grid-sort-row">
          <span class="grid-sort-label">{{ option.label }}</span>
          <div class="grid-sort-actions">
            <button
              type="button"
              class="grid-sort-order-btn"
              :class="{ active: sortKey === option.key && sortOrder === 'asc' }"
              @click="applySort(option.key, 'asc')"
            >
              升序
            </button>
            <button
              type="button"
              class="grid-sort-order-btn"
              :class="{ active: sortKey === option.key && sortOrder === 'desc' }"
              @click="applySort(option.key, 'desc')"
            >
              降序
            </button>
          </div>
        </div>
      </div>
    </template>
  </AppDropdown>
</template>

<style scoped>
.grid-sort-trigger {
  width: 34px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.grid-sort-trigger:hover {
  color: var(--text);
  background: var(--surface-sunken);
}
.grid-sort-icon {
  width: 16px;
  height: 16px;
}
.grid-sort-panel {
  min-width: 220px;
  padding: 8px;
}
.grid-sort-row + .grid-sort-row {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--border-soft);
}
.grid-sort-label {
  display: block;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 6px;
}
.grid-sort-actions {
  display: flex;
  gap: 6px;
}
.grid-sort-order-btn {
  flex: 1;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text-regular);
  font-size: 12px;
  padding: 6px 0;
  cursor: pointer;
}
.grid-sort-order-btn:hover {
  border-color: var(--brand);
  color: var(--brand);
}
.grid-sort-order-btn.active {
  background: #eef4ff;
  border-color: var(--brand);
  color: var(--brand-strong);
}
</style>
