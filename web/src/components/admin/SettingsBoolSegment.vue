<script setup lang="ts">
import "@/styles/settings-panel.css";

const model = defineModel<boolean>({ default: false });

withDefaults(
  defineProps<{
    label: string;
    offLabel?: string;
    onLabel?: string;
    disabled?: boolean;
  }>(),
  { offLabel: "关闭", onLabel: "开启", disabled: false },
);
</script>

<template>
  <div class="settings-segment" :class="{ 'settings-segment--disabled': disabled }" role="radiogroup" :aria-label="label">
    <button
      type="button"
      class="settings-segment__btn"
      :class="{ 'settings-segment__btn--active': model }"
      role="radio"
      :aria-checked="model"
      :disabled="disabled"
      @click="model = true"
    >
      {{ onLabel }}
    </button>
    <button
      type="button"
      class="settings-segment__btn"
      :class="{ 'settings-segment__btn--active': !model }"
      role="radio"
      :aria-checked="!model"
      :disabled="disabled"
      @click="model = false"
    >
      {{ offLabel }}
    </button>
  </div>
</template>

<style scoped>
.settings-segment--disabled {
  opacity: 0.6;
}

.settings-segment__btn:disabled {
  cursor: not-allowed;
}
</style>
