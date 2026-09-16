<script setup lang="ts">
import AppTabBar from "@/components/base/AppTabBar.vue";

interface Tab {
  key: string;
  label: string;
  /** 该 Tab 内存在未保存改动时标一个小圆点。 */
  changed?: boolean;
}

defineProps<{ tabs: Tab[]; modelValue: string; icon?: string }>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <AppTabBar
    :tabs="tabs"
    :model-value="modelValue"
    :icon="icon"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #actions>
      <slot name="actions" />
    </template>
  </AppTabBar>
</template>
