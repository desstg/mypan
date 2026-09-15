<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: "dots" | "notch";
    size?: number | string;
    color?: string;
  }>(),
  {
    variant: "dots",
    size: 22,
    color: "currentColor",
  },
);
</script>

<template>
  <svg
    class="busy-spinner"
    :class="`busy-spinner--${variant}`"
    viewBox="0 0 24 24"
    :width="size"
    :height="size"
    :style="{ color }"
    aria-hidden="true"
  >
    <g v-if="variant === 'dots'">
      <animateTransform
        attributeName="transform"
        type="rotate"
        from="0 12 12"
        to="360 12 12"
        dur="2s"
        repeatCount="indefinite"
      />
      <circle cx="12" cy="3.2" r="1.55" fill="currentColor" opacity="1" />
      <circle cx="18.2" cy="5.8" r="1.55" fill="currentColor" opacity="0.88" />
      <circle cx="20.8" cy="12" r="1.55" fill="currentColor" opacity="0.75" />
      <circle cx="18.2" cy="18.2" r="1.55" fill="currentColor" opacity="0.62" />
      <circle cx="12" cy="20.8" r="1.55" fill="currentColor" opacity="0.5" />
      <circle cx="5.8" cy="18.2" r="1.55" fill="currentColor" opacity="0.38" />
      <circle cx="3.2" cy="12" r="1.55" fill="currentColor" opacity="0.28" />
      <circle cx="5.8" cy="5.8" r="1.55" fill="currentColor" opacity="0.18" />
    </g>
    <g v-else>
      <animateTransform
        attributeName="transform"
        type="rotate"
        from="0 12 12"
        to="360 12 12"
        dur="0.85s"
        repeatCount="indefinite"
      />
      <circle
        cx="12"
        cy="12"
        r="9"
        fill="none"
        stroke="currentColor"
        stroke-width="2.6"
        stroke-linecap="round"
        stroke-dasharray="42 14"
        transform="rotate(-90 12 12)"
      />
    </g>
  </svg>
</template>

<style scoped>
.busy-spinner {
  display: block;
  flex-shrink: 0;
  overflow: visible;
}
</style>
