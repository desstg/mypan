<script setup lang="ts">
withDefaults(defineProps<{
  direction: "previous" | "next";
  label: string;
  title: string;
  disabled?: boolean;
  placement?: "stage" | "viewport";
}>(), {
  disabled: false,
  placement: "stage",
});

const emit = defineEmits<{ click: [] }>();
</script>

<template>
  <button
    type="button"
    class="preview-side-navigation"
    :class="[
      `preview-side-navigation--${direction}`,
      `preview-side-navigation--${placement}`,
    ]"
    :aria-label="label"
    :title="title"
    :disabled="disabled"
    @click="emit('click')"
  >
    <slot />
  </button>
</template>

<style scoped>
.preview-side-navigation {
  top: 50%;
  z-index: 12;
  width: 50px;
  height: 64px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #e7effa;
  border: 1px solid rgb(151 181 224 / 16%);
  border-radius: 12px;
  background: rgb(7 17 32 / 58%);
  font-size: 20px;
  opacity: 0.82;
  backdrop-filter: blur(12px);
  transform: translateY(-50%);
  transition: color 150ms ease, background 150ms ease, opacity 150ms ease;
}

.preview-side-navigation--stage { position: absolute; }
.preview-side-navigation--viewport { position: fixed; }
.preview-side-navigation--previous { left: 22px; }
.preview-side-navigation--next { right: 22px; }
.preview-side-navigation:hover:not(:disabled) {
  color: #fff;
  background: rgb(22 69 119 / 72%);
  opacity: 1;
}
.preview-side-navigation:disabled { opacity: 0.18; cursor: default; }
.preview-side-navigation :deep(svg) {
  width: 21px;
  height: 21px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

@media (max-width: 720px) {
  .preview-side-navigation {
    width: 38px;
    height: 54px;
    border-radius: 9px;
    font-size: 16px;
  }
  .preview-side-navigation--previous { left: 6px; }
  .preview-side-navigation--next { right: 6px; }
}
</style>
