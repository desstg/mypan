<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppMenuPanel from "@/components/base/AppMenuPanel.vue";
import { useDismissOnOutside } from "@/composables/useDismissOnOutside";
import type { DropdownMenuItem } from "@/types/menu";

const props = defineProps<{
  open: boolean;
  x: number;
  y: number;
  items: DropdownMenuItem[];
}>();

const emit = defineEmits<{ select: [key: string]; close: [] }>();

const panelRef = ref<HTMLElement | null>(null);
const active = computed(() => props.open && props.items.length > 0);
const dismissRefs = ref<(HTMLElement | null)[]>([]);

watch(panelRef, () => {
  dismissRefs.value = [panelRef.value];
});
useDismissOnOutside(active, dismissRefs, () => emit("close"));

function onSelect(key: string) {
  emit("select", key);
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="active"
      ref="panelRef"
      class="menu-panel--floating"
      :style="{ left: `${x}px`, top: `${y}px` }"
    >
      <AppMenuPanel variant="context" density="comfortable" :items="items" @select="onSelect" />
    </div>
  </Teleport>
</template>
