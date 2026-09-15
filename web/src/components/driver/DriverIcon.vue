<script setup lang="ts">
import { computed, ref, watch } from "vue";

const props = withDefaults(
  defineProps<{ name: string; color?: string; logo?: string; size?: number }>(),
  { color: "", logo: "", size: 40 },
);

const imgError = ref(false);
watch(
  () => props.logo,
  () => {
    imgError.value = false;
  },
);

const showImg = computed(() => Boolean(props.logo) && !imgError.value);
const bg = computed(() => props.color || "#4c74df");
const initial = computed(() => props.name.trim().charAt(0).toUpperCase() || "?");
</script>

<template>
  <img
    v-if="showImg"
    :src="logo"
    :alt="name"
    class="driver-icon driver-icon--img"
    :style="{ width: `${size}px`, height: `${size}px` }"
    @error="imgError = true"
  />
  <span
    v-else
    class="driver-icon driver-icon--fallback"
    :style="{ width: `${size}px`, height: `${size}px`, background: bg, fontSize: `${size * 0.45}px` }"
  >
    {{ initial }}
  </span>
</template>

<style scoped>
.driver-icon {
  display: inline-block;
  flex-shrink: 0;
  border-radius: var(--radius-md);
}
.driver-icon--img {
  object-fit: cover;
}
.driver-icon--fallback {
  display: grid;
  place-items: center;
  color: #fff;
  font-weight: 700;
}

:root[data-skin="brutal"] .driver-icon {
  border-radius: 0;
}
</style>
