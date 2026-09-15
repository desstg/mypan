<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import AppMenuPanel from "@/components/base/AppMenuPanel.vue";
import { computeDropdownPosition } from "@/composables/useDropdownPosition";
import { useDismissOnOutside } from "@/composables/useDismissOnOutside";
import type { DropdownMenuItem, DropdownAlign, DropdownPlacement } from "@/types/menu";
import { alignToPlacement } from "@/types/menu";

const props = withDefaults(
  defineProps<{
    items?: DropdownMenuItem[];
    trigger?: "click" | "hover";
    /** 相对触发器水平对齐：left 左对齐 / center 居中 / right 右对齐 */
    align?: DropdownAlign;
    minWidth?: number;
    teleported?: boolean;
    hoverBridge?: boolean;
    density?: "compact" | "comfortable";
    variant?: "default" | "context";
    menuHeight?: number;
  }>(),
  {
    items: () => [],
    trigger: "click",
    align: "center",
    minWidth: 160,
    teleported: true,
    hoverBridge: false,
    density: "comfortable",
    variant: "default",
    menuHeight: 220,
  },
);

const open = defineModel<boolean>("open", { default: false });

const emit = defineEmits<{ select: [key: string] }>();

const rootRef = ref<HTMLElement | null>(null);
const panelRef = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});
let hoverCloseTimer: ReturnType<typeof setTimeout> | null = null;

const dismissRefs = ref<(HTMLElement | null)[]>([]);
watch([rootRef, panelRef, open], () => {
  dismissRefs.value = [rootRef.value, panelRef.value];
});
useDismissOnOutside(open, dismissRefs, () => {
  open.value = false;
});

function clearHoverCloseTimer() {
  if (hoverCloseTimer) {
    clearTimeout(hoverCloseTimer);
    hoverCloseTimer = null;
  }
}

const resolvedPlacement = computed<DropdownPlacement>(() => alignToPlacement(props.align));

async function refreshPosition() {
  await nextTick();
  await nextTick();
  const anchor = rootRef.value;
  const panel = panelRef.value;
  if (!anchor || !props.teleported) return;
  const rect = anchor.getBoundingClientRect();
  const menuW = panel?.offsetWidth || props.minWidth;
  const menuH = panel?.offsetHeight || props.menuHeight;
  menuStyle.value = computeDropdownPosition(rect, {
    placement: resolvedPlacement.value,
    minWidth: menuW,
    menuHeight: menuH,
  });
}

function toggle() {
  open.value = !open.value;
}

function close() {
  open.value = false;
}

function onSelect(key: string) {
  emit("select", key);
  close();
}

watch(open, (val) => {
  if (val) void refreshPosition();
});

function onMouseEnter() {
  if (props.trigger !== "hover") return;
  clearHoverCloseTimer();
  open.value = true;
}

function onMouseLeave() {
  if (props.trigger !== "hover") return;
  clearHoverCloseTimer();
  hoverCloseTimer = setTimeout(() => {
    open.value = false;
  }, 120);
}

function onScrollOrResize() {
  if (open.value && props.teleported) void refreshPosition();
}

watch(open, (val) => {
  if (val) {
    window.addEventListener("scroll", onScrollOrResize, true);
    window.addEventListener("resize", onScrollOrResize);
  } else {
    window.removeEventListener("scroll", onScrollOrResize, true);
    window.removeEventListener("resize", onScrollOrResize);
  }
});

onUnmounted(() => {
  clearHoverCloseTimer();
  window.removeEventListener("scroll", onScrollOrResize, true);
  window.removeEventListener("resize", onScrollOrResize);
});

const inlinePanelClass = computed(() => ({
  "app-dropdown__inline-panel--start": props.align === "left",
  "app-dropdown__inline-panel--center": props.align === "center",
  "app-dropdown__inline-panel--end": props.align === "right",
}));
</script>

<template>
  <div
    ref="rootRef"
    class="app-dropdown"
    :class="{ 'app-dropdown--hover-bridge': hoverBridge && trigger === 'hover' }"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
  >
    <slot name="trigger" :open="open" :toggle="toggle" :close="close" />

    <Teleport to="body" :disabled="!teleported">
      <div
        v-if="open && teleported"
        ref="panelRef"
        class="menu-panel--floating"
        :style="menuStyle"
        @mouseenter="onMouseEnter"
        @mouseleave="onMouseLeave"
      >
        <AppMenuPanel
          v-if="items.length"
          :items="items"
          :density="density"
          :variant="variant"
          :min-width="minWidth"
          @select="onSelect"
        />
        <div v-else class="menu-panel" :class="[`menu-panel--${density}`]">
          <slot name="panel" :close="close" />
        </div>
      </div>
    </Teleport>

    <div
      v-if="open && !teleported"
      ref="panelRef"
      class="app-dropdown__inline-panel"
      :class="inlinePanelClass"
      @mouseenter="onMouseEnter"
      @mouseleave="onMouseLeave"
    >
      <AppMenuPanel
        v-if="items.length"
        :items="items"
        :density="density"
        :variant="variant"
        :min-width="minWidth"
        @select="onSelect"
      />
      <div v-else class="menu-panel" :class="[`menu-panel--${density}`]">
        <slot name="panel" :close="close" />
      </div>
    </div>
  </div>
</template>
