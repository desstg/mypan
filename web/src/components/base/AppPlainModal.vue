<script setup lang="ts">
import AppModal from "@/components/base/AppModal.vue";

withDefaults(
  defineProps<{
    open: boolean;
    title?: string;
    size?: "sm" | "md" | "lg" | "account" | "branch";
    /** 内容区贴边（去掉左右内边距），用于内容自带贴边布局的极简弹窗。 */
    bodyFlush?: boolean;
  }>(),
  { title: "", size: "md", bodyFlush: false },
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
    @close="emit('close')"
  >
    <slot />
    <template v-if="$slots.footer" #footer>
      <slot name="footer" />
    </template>
  </AppModal>
</template>
