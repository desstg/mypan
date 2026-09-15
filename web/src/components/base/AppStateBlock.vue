<script setup lang="ts">
import BusySpinner from "@/components/base/BusySpinner.vue";

withDefaults(
  defineProps<{
    message: string;
    loading?: boolean;
    minHeight?: string;
  }>(),
  { loading: false, minHeight: "160px" },
);
</script>

<template>
  <div class="state-block" :style="{ minHeight }">
    <BusySpinner v-if="loading" variant="notch" :size="28" color="var(--brand)" />
    <span>{{ message }}</span>
  </div>
</template>

<style scoped>
.state-block {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--text-muted);
  font-size: 15px;
  text-align: center;
}
</style>
