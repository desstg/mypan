<script setup lang="ts">
defineProps<{
  icon?: string;
  title?: string;
  description?: string;
}>();
</script>

<template>
  <div class="admin-empty-state">
    <span v-if="icon" class="admin-empty-state__icon">{{ icon }}</span>
    <p v-if="title" class="admin-empty-state__title">{{ title }}</p>
    <p v-if="description" class="admin-empty-state__desc">{{ description }}</p>
    <slot />
  </div>
</template>

<style scoped>
.admin-empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 180px;
  padding: 32px 20px;
  border-radius: var(--radius-md);
  background: var(--surface);
  border: 1px dashed var(--border-soft);
  color: var(--text-muted);
  text-align: center;
}
.admin-empty-state__icon {
  font-size: 40px;
  line-height: 1;
}
.admin-empty-state__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.admin-empty-state__desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
}
</style>
