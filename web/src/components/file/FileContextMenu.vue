<script setup lang="ts">
import AppContextMenu from "@/components/base/AppContextMenu.vue";
import type { DropdownMenuItem } from "@/types/menu";

export type ContextMenuItem = {
  action: string;
  label: string;
  danger?: boolean;
};

const props = defineProps<{
  open: boolean;
  x: number;
  y: number;
  items: ContextMenuItem[];
}>();

const emit = defineEmits<{ action: [action: string]; close: [] }>();

function toMenuItems(items: ContextMenuItem[]): DropdownMenuItem[] {
  return items.map((item) => ({
    key: item.action,
    label: item.label,
    danger: item.danger,
    type: "action" as const,
  }));
}
</script>

<template>
  <AppContextMenu
    :open="props.open"
    :x="props.x"
    :y="props.y"
    :items="toMenuItems(props.items)"
    @select="emit('action', $event)"
    @close="emit('close')"
  />
</template>
