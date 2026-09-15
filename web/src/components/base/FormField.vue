<script setup lang="ts">
defineProps<{ label: string; required?: boolean }>();
</script>

<template>
  <div class="form-field">
    <label class="form-field__label">
      <slot name="label">{{ label }}</slot>
      <span v-if="required" class="form-field__req">*</span>
      <slot name="help" />
    </label>
    <slot />
  </div>
</template>

<style scoped>
.form-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.form-field__label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-regular);
  font-weight: 500;
}
.form-field__req {
  color: var(--danger);
}
</style>
