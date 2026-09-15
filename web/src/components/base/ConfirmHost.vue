<script setup lang="ts">
import ConfirmModal from "@/components/base/ConfirmModal.vue";
import { confirmChecked, useConfirm } from "@/composables/useConfirm";

const { state, close, confirmAction, emitAction } = useConfirm();
</script>

<template>
  <ConfirmModal
    :open="state.open"
    :title="state.title"
    :message="state.message"
    :icon="state.icon"
    :confirm-text="state.confirmText"
    :cancel-text="state.cancelText"
    :danger="state.danger"
    :size="state.size"
    :loading="state.loading"
    :hint="state.hint"
    :checkbox-label="state.checkboxLabel"
    :checked="confirmChecked"
    :actions="state.actions"
    :show-cancel="state.showCancel"
    :preset="state.preset"
    :preset-data="state.presetData"
    @close="close"
    @confirm="confirmAction"
    @action="emitAction"
    @update:checked="confirmChecked = $event"
  />
</template>
