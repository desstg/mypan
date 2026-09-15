<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";
import type { DropdownMenuItem } from "@/types/menu";

const props = withDefaults(
  defineProps<{
    items: DropdownMenuItem[];
    density?: "compact" | "comfortable";
    variant?: "default" | "context";
    minWidth?: number;
  }>(),
  { density: "compact", variant: "default", minWidth: 148 },
);

const emit = defineEmits<{ select: [key: string] }>();

function onItemClick(item: DropdownMenuItem) {
  if (item.type !== "action" || item.disabled) return;
  emit("select", item.key);
}
</script>

<template>
  <ul
    class="menu-panel"
    :class="[
      `menu-panel--${density}`,
      variant === 'context' ? 'menu-panel--context' : '',
    ]"
    :style="{ minWidth: `${minWidth}px` }"
    role="menu"
    @click.stop
  >
    <template v-for="item in items" :key="item.key">
      <li v-if="item.type === 'divider'" class="menu-panel__divider" role="separator" />
      <li v-else-if="item.type === 'hint'" class="menu-panel__hint">{{ item.label }}</li>
      <li v-else role="none">
        <button
          type="button"
          class="menu-panel__item"
          :class="{ 'menu-panel__item--danger': item.danger }"
          role="menuitem"
          :disabled="item.disabled"
          @click="onItemClick(item)"
        >
          <span v-if="item.icon" class="menu-panel__icon">
            <SvgIcon :name="item.icon" :size="17" />
          </span>
          <span>{{ item.label }}</span>
        </button>
      </li>
    </template>
    <slot />
  </ul>
</template>
