<script setup lang="ts">
import { onUnmounted, watch } from "vue";
import ConfirmDialogFrame from "@/components/base/ConfirmDialogFrame.vue";
import { lockPageScroll, unlockPageScroll } from "@/utils/scrollLock";
import type { ConfirmIcon, ConfirmPreset, ConfirmSize, ConfirmAction } from "@/types/confirm";

const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    message?: string;
    icon?: ConfirmIcon;
    confirmText?: string;
    cancelText?: string;
    danger?: boolean;
    size?: ConfirmSize;
    loading?: boolean;
    hint?: string;
    checkboxLabel?: string;
    checked?: boolean;
    actions?: readonly ConfirmAction[];
    showCancel?: boolean;
    preset?: ConfirmPreset;
    presetData?: Record<string, unknown>;
  }>(),
  {
    message: "",
    icon: "warning",
    confirmText: "确定",
    cancelText: "取消",
    danger: true,
    size: "md",
    loading: false,
    checked: false,
    showCancel: true,
  },
);

const emit = defineEmits<{
  close: [];
  confirm: [];
  action: [actionId: string];
  "update:checked": [value: boolean];
}>();

function onKey(e: KeyboardEvent) {
  if (e.key === "Escape" && !props.loading) emit("close");
}

function lockPageScrollState(lock: boolean) {
  if (lock) lockPageScroll();
  else unlockPageScroll();
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      window.addEventListener("keydown", onKey);
      lockPageScrollState(true);
    } else {
      window.removeEventListener("keydown", onKey);
      lockPageScrollState(false);
    }
  },
);

onUnmounted(() => {
  window.removeEventListener("keydown", onKey);
  lockPageScrollState(false);
});
</script>

<template>
  <Teleport to="body">
    <Transition name="confirm-modal">
      <div v-if="open" class="confirm-modal-overlay">
        <ConfirmDialogFrame
          :title="title"
          :message="message"
          :icon="icon"
          :confirm-text="confirmText"
          :cancel-text="cancelText"
          :danger="danger"
          :size="size"
          :loading="loading"
          :hint="hint"
          :checkbox-label="checkboxLabel"
          :checked="checked"
          :actions="actions"
          :show-cancel="showCancel"
          :preset="preset"
          :preset-data="presetData"
          @cancel="emit('close')"
          @confirm="emit('confirm')"
          @action="emit('action', $event)"
          @update:checked="emit('update:checked', $event)"
        />
      </div>
    </Transition>
  </Teleport>
</template>
