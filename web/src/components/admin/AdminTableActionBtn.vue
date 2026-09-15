<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from "vue";

type AdminTableActionIcon = "play" | "stop" | "edit" | "delete" | "log" | "copy" | "rotate";

withDefaults(
  defineProps<{
    icon: AdminTableActionIcon;
    title: string;
    danger?: boolean;
    disabled?: boolean;
    /** 直觉操作按钮（图标即语义）设为 true：完全无气泡提示 */
    noTip?: boolean;
  }>(),
  { danger: false, disabled: false, noTip: false },
);

const emit = defineEmits<{ click: [MouseEvent] }>();

// 自定义气泡：替代浏览器原生 title 提示。用 Teleport + fixed 定位，
// 避免被表格滚动容器裁切；贴近按钮并带箭头指向按钮中心，自动上下切换防溢出视口。
const visible = ref(false);
const below = ref(true);
const anchorRef = ref<HTMLElement | null>(null);
const bubbleRef = ref<HTMLElement | null>(null);
const bubbleStyle = ref<Record<string, string>>({});

function position() {
  const el = anchorRef.value;
  if (!el) return;
  const bubble = bubbleRef.value;
  const rect = el.getBoundingClientRect();
  const bw = bubble ? bubble.offsetWidth : 160;
  const bh = bubble ? bubble.offsetHeight : 30;
  const margin = 6;
  let left = rect.left + rect.width / 2 - bw / 2;
  left = Math.min(Math.max(margin, left), window.innerWidth - bw - margin);
  const showBelow = rect.bottom + bh + margin <= window.innerHeight;
  const top = showBelow ? rect.bottom + margin : rect.top - bh - margin;
  const arrowLeft = rect.left + rect.width / 2 - left;
  below.value = showBelow;
  bubbleStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    "--arrow-left": `${arrowLeft}px`,
  };
}

async function show() {
  if (visible.value) return;
  visible.value = true;
  await nextTick();
  position();
}

function hide() {
  visible.value = false;
}

function handleScroll() {
  if (visible.value) position();
}

function bindPositionListeners() {
  window.addEventListener("scroll", handleScroll, true);
  window.addEventListener("resize", handleScroll);
}

function onMouseEnter() {
  bindPositionListeners();
  void show();
}

function onMouseLeave() {
  window.removeEventListener("scroll", handleScroll, true);
  window.removeEventListener("resize", handleScroll);
  hide();
}

onBeforeUnmount(() => {
  window.removeEventListener("scroll", handleScroll, true);
  window.removeEventListener("resize", handleScroll);
});
</script>

<template>
  <span
    ref="anchorRef"
    class="aatip"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
    @focusin="onMouseEnter"
    @focusout="onMouseLeave"
  >
    <button
      type="button"
      class="admin-table__action-btn"
      :class="{ 'admin-table__action-btn--danger': danger }"
      :aria-label="title"
      :disabled="disabled"
      @click="emit('click', $event)"
    >
      <svg
        v-if="icon === 'play'"
        viewBox="0 0 16 16"
        fill="currentColor"
        aria-hidden="true"
      >
        <path d="M4 2.5v11l9-5.5-9-5.5z" />
      </svg>
      <svg
        v-else-if="icon === 'stop'"
        viewBox="0 0 16 16"
        fill="currentColor"
        aria-hidden="true"
      >
        <rect x="4" y="4" width="8" height="8" rx="1" />
      </svg>
      <svg
        v-else-if="icon === 'edit'"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        aria-hidden="true"
      >
        <path d="M2.5 13.5h3l8-8-3-3-8 8v3z" />
        <path d="M9.5 3.5l3 3" />
      </svg>
      <svg
        v-else-if="icon === 'log'"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.4"
        aria-hidden="true"
      >
        <rect x="2" y="3" width="12" height="10" rx="1.5" />
        <path d="M5 6h6M5 8.5h4" stroke-linecap="round" />
      </svg>
      <svg
        v-else-if="icon === 'copy'"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <rect x="5.5" y="5.5" width="8" height="8" rx="1.2" />
        <path d="M10.5 5.5V4.2c0-.7-.5-1.2-1.2-1.2H4.2c-.7 0-1.2.5-1.2 1.2v5.1c0 .7.5 1.2 1.2 1.2H5.5" />
      </svg>
      <svg
        v-else-if="icon === 'rotate'"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M13 3v4H9" />
        <path d="M3 13V9h4" />
        <path d="M11.7 6A4.5 4.5 0 0 0 4 5.5L3 7" />
        <path d="M4.3 10A4.5 4.5 0 0 0 12 10.5l1-1.5" />
      </svg>
      <svg
        v-else
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        aria-hidden="true"
      >
        <path d="M3 4h10" stroke-linecap="round" />
        <path d="M6 4V3.2c0-.7.5-1.2 1.2-1.2h1.6c.7 0 1.2.5 1.2 1.2V4" />
        <path d="M5 6v7c0 .6.4 1 1 1h4c.6 0 1-.4 1-1V6" />
      </svg>
    </button>
    <Teleport to="body">
      <div
        v-show="visible && !noTip"
        ref="bubbleRef"
        class="aatip__bubble"
        :class="{ 'aatip__bubble--below': below }"
        :style="bubbleStyle"
        role="tooltip"
      >
        {{ title }}
      </div>
    </Teleport>
  </span>
</template>

<style scoped>
.aatip {
  display: inline-flex;
  align-items: center;
}
.aatip__bubble {
  position: fixed;
  z-index: 2200;
  max-width: 260px;
  padding: 5px 10px;
  background: #1e293b;
  color: #e2e8f0;
  border-radius: 7px;
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.2);
  pointer-events: none;
  --arrow-left: 50%;
}
.aatip__bubble::after {
  content: "";
  position: absolute;
  left: var(--arrow-left);
  transform: translateX(-50%);
  border: 5px solid transparent;
}
.aatip__bubble--below::after {
  top: -5px;
  border-bottom-color: #1e293b;
}
.aatip__bubble:not(.aatip__bubble--below)::after {
  bottom: -5px;
  border-top-color: #1e293b;
}
.dark .aatip__bubble {
  background: #e2e8f0;
  color: #1e293b;
}
.dark .aatip__bubble--below::after {
  border-bottom-color: #e2e8f0;
}
.dark .aatip__bubble:not(.aatip__bubble--below)::after {
  border-top-color: #e2e8f0;
}
</style>
