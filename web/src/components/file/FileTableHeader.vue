<script setup lang="ts">
import type { SortKey, SortOrder } from "@/types/file-browser";

defineProps<{
  isAdmin: boolean;
  selectedCount: number;
  selectAll: boolean;
  filesCount: number;
  sortClass: (key: SortKey) => SortOrder | "";
}>();

const emit = defineEmits<{
  "toggle-select-all": [checked: boolean];
  "sort-by": [key: SortKey];
}>();
</script>

<template>
  <thead>
    <tr>
      <th v-if="isAdmin" class="checkbox-col">
        <label class="sr-only" for="select-all-checkbox">全选</label>
        <input
          id="select-all-checkbox"
          type="checkbox"
          :checked="selectAll"
          :disabled="filesCount === 0"
          title="全选 / 取消全选"
          @change="emit('toggle-select-all', ($event.target as HTMLInputElement).checked)"
        />
      </th>
      <th class="name-col sortable" @click="emit('sort-by', 'name')">
        <template v-if="isAdmin && selectedCount > 0">已选中 {{ selectedCount }} 项</template>
        <template v-else>
          名称<span class="sort-indicator" :class="sortClass('name')" />
        </template>
      </th>
      <th class="size-col sortable" @click="emit('sort-by', 'size')">
        大小<span class="sort-indicator" :class="sortClass('size')" />
      </th>
      <th class="time-col sortable" @click="emit('sort-by', 'modified')">
        修改时间<span class="sort-indicator" :class="sortClass('modified')" />
      </th>
    </tr>
  </thead>
</template>

<style scoped>
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
