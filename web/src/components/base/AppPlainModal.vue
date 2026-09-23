<script setup lang="ts">
import AppModal from "@/components/base/AppModal.vue";

withDefaults(
  defineProps<{
    open: boolean;
    title?: string;
    size?: "sm" | "md" | "lg" | "wide" | "account" | "branch";
    /** 内容区贴边（去掉左右内边距），用于内容自带贴边布局的极简弹窗。 */
    bodyFlush?: boolean;
    /** 定高（88vh）+ 内容区自己滚，见 AppModal 的 steady。 */
    steady?: boolean;
  }>(),
  { title: "", size: "md", bodyFlush: false, steady: false },
);

const emit = defineEmits<{ close: [] }>();
</script>

<template>
  <AppModal
    :open="open"
    :title="title"
    :size="size"
    head-plain
    :body-flush="bodyFlush"
    :steady="steady"
    @close="emit('close')"
  >
    <slot />
    <template v-if="$slots.footer" #footer>
      <slot name="footer" />
    </template>
  </AppModal>
</template>
