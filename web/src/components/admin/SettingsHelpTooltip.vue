<script setup lang="ts">
import { nextTick, ref } from "vue";
import "@/styles/settings-panel.css";

defineProps<{
  title: string;
}>();

const visible = ref(false);
const anchor = ref<HTMLElement | null>(null);
const popoverStyle = ref<Record<string, string>>({});

async function show() {
  visible.value = true;
  await nextTick();
  updatePosition();
}

function hide() {
  visible.value = false;
}

function updatePosition() {
  const el = anchor.value;
  if (!el) return;
  const rect = el.getBoundingClientRect();
  const narrow = window.innerWidth <= 640;
  if (narrow) {
    popoverStyle.value = {
      top: `${rect.bottom + 10}px`,
      left: `${Math.max(12, rect.left)}px`,
      transform: "none",
    };
    return;
  }
  popoverStyle.value = {
    top: `${rect.top + rect.height / 2}px`,
    left: `${rect.right + 12}px`,
    transform: "translateY(-50%)",
  };
}
</script>

<template>
  <span
    ref="anchor"
    class="settings-help"
    @mouseenter="show"
    @mouseleave="hide"
  >
    <i class="fas fa-question-circle settings-help__icon" aria-hidden="true" />
    <Teleport to="body">
      <div
        v-show="visible"
        class="settings-help__popover settings-help__popover--portal"
        :style="popoverStyle"
        role="tooltip"
      >
        <div class="settings-help__title">{{ title }}</div>
        <div class="settings-help__body">
          <slot />
        </div>
      </div>
    </Teleport>
  </span>
</template>
