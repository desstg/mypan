<script setup lang="ts">
import AppTabBar from "@/components/base/AppTabBar.vue";

// 与 AppTabBar 的 Tab 保持一致（那里是同一个结构的另一份声明）。
interface Tab {
  key: string;
  label: string;
  /** 该 Tab 内存在未保存改动时标一个小圆点。 */
  changed?: boolean;
  /** 可展开的分组（见 AppTabBar 的 Tab 注释）。 */
  expandable?: boolean;
  expanded?: boolean;
  /** 该 Tab 里的条目数（见 AppTabBar 的 Tab 注释）。 */
  count?: number;
}

defineProps<{
  tabs: Tab[];
  modelValue: string;
  icon?: string;
  sticky?: boolean;
  /** 手机上是否也粘（见 AppTabBar 的同名 prop）。缺省 true。 */
  stickyMobile?: boolean;
}>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <AppTabBar
    :tabs="tabs"
    :model-value="modelValue"
    :icon="icon"
    :sticky="sticky"
    :sticky-mobile="stickyMobile"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #actions>
      <slot name="actions" />
    </template>
  </AppTabBar>
</template>
