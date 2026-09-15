<script setup lang="ts">
import { computed } from "vue";
import AppSelect from "@/components/base/AppSelect.vue";
import type { Account } from "@/api/types";

const props = defineProps<{ accounts: Account[]; modelValue: number | null }>();
const emit = defineEmits<{ "update:modelValue": [number] }>();

const options = computed(() => props.accounts.map((a) => ({ value: a.id, label: a.name })));
</script>

<template>
  <div class="account-selector">
    <div class="account-selector__control">
      <AppSelect
        :model-value="modelValue"
        :options="options"
        placeholder="选择账号"
        @update:model-value="(v) => emit('update:modelValue', Number(v))"
      />
    </div>
  </div>
</template>

<style scoped>
.account-selector {
  display: flex;
  align-items: center;
  gap: 10px;
}
.account-selector__control {
  min-width: 200px;
}
</style>
