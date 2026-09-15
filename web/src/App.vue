<script setup lang="ts">
import { RouterView } from "vue-router";
import ToastHost from "@/components/base/ToastHost.vue";
import ConfirmHost from "@/components/base/ConfirmHost.vue";
</script>

<template>
  <RouterView />
  <ToastHost />
  <ConfirmHost />
</template>
