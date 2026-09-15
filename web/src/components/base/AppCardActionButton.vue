<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";

withDefaults(
  defineProps<{
    icon?: string;
    iconClass?: string;
    label: string;
    variant?: "secondary" | "danger";
    iconOnly?: boolean;
    disabled?: boolean;
    title?: string;
  }>(),
  { icon: "", iconClass: "", variant: "secondary", iconOnly: false, disabled: false, title: "" },
);

const emit = defineEmits<{ click: [MouseEvent] }>();
</script>

<template>
  <button
    type="button"
    class="card-action-btn"
    :class="[`card-action-btn--${variant}`, { 'card-action-btn--icon-only': iconOnly }]"
    :disabled="disabled"
    :title="title || label"
    :aria-label="label"
    @click="emit('click', $event)"
  >
    <i v-if="iconClass" :class="iconClass" aria-hidden="true" />
    <SvgIcon v-else-if="icon" :name="icon" :size="13" />
    <span v-if="!iconOnly">{{ label }}</span>
  </button>
</template>

<style scoped>
.card-action-btn {
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--radius-sm);
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
  cursor: pointer;
  transition: var(--transition);
}

.card-action-btn--secondary {
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
  color: var(--brand);
}

.card-action-btn--danger {
  background: color-mix(in srgb, var(--danger) 8%, var(--surface));
  color: var(--danger);
}

.card-action-btn--icon-only {
  width: 32px;
  padding: 0;
}

.card-action-btn:not(:disabled):hover {
  filter: brightness(0.97);
}

.card-action-btn:disabled {
  cursor: not-allowed;
  opacity: 0.58;
}
</style>
