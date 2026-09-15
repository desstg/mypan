<script setup lang="ts">
defineProps<{
  name: string;
}>();
</script>

<template>
  <i :class="['fas', `fa-${name}`, 'admin-nav-icon']" aria-hidden="true"></i>
</template>

<style scoped>
.admin-nav-icon {
  flex: none;
  width: 24px;
  text-align: center;
  font-size: 18px;
  line-height: 1;
}
</style>
