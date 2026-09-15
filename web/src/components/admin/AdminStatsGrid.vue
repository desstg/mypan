<script setup lang="ts">
</script>

<template>
  <div class="admin-stats-grid">
    <slot />
  </div>
</template>

<style scoped>
.admin-stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 14px;
  margin-bottom: 16px;
}
</style>
