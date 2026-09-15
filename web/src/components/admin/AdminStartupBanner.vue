<script setup lang="ts">
defineProps<{
  seconds: number;
}>();
</script>

<template>
  <div v-if="seconds > 0" class="admin-startup-banner">
    任务启动中，预计 <strong>{{ seconds }}</strong> 秒后开始执行…
  </div>
</template>

<style scoped>
.admin-startup-banner {
  padding: 12px 16px;
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.5;
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  border: 1px solid color-mix(in srgb, var(--brand) 24%, var(--border-soft));
  color: color-mix(in srgb, var(--brand) 70%, var(--text));
}
</style>
