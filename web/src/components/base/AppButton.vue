<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: "primary" | "secondary" | "ghost" | "danger" | "cancel";
    size?: "sm" | "md";
    block?: boolean;
    disabled?: boolean;
  }>(),
  { variant: "secondary", size: "md", block: false, disabled: false },
);
</script>

<template>
  <button
    class="btn"
    :class="[`btn--${variant}`, `btn--${size}`, { 'btn--block': block }]"
    :disabled="disabled"
  >
    <slot />
  </button>
</template>
