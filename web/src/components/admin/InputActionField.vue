<script setup lang="ts">
</script>

<template>
  <div class="input-action-field">
    <div class="input-action-field__main">
      <slot />
    </div>
    <slot name="action" />
  </div>
</template>

<style scoped>
.input-action-field {
  display: flex;
  gap: 8px;
  align-items: stretch;
  min-width: 0;
}
.input-action-field__main {
  flex: 1;
  min-width: 0;
}
@media (max-width: 640px) {
  .input-action-field {
    flex-direction: column;
  }
}
</style>
