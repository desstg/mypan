<script setup lang="ts">
</script>

<template>
  <div class="warning-banner">
    <slot />
  </div>
</template>

<style scoped>
.warning-banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 16px;
  padding: 12px 16px;
  border-radius: 12px;
  background: rgba(245, 158, 11, 0.12);
  border: 1px solid rgba(245, 158, 11, 0.35);
  color: var(--text);
  font-size: 14px;
  line-height: 1.6;
}
</style>
