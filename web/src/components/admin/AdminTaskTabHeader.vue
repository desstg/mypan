<script setup lang="ts">
import AppIconButton from "@/components/base/AppIconButton.vue";
import StatCard from "@/components/base/StatCard.vue";
import AdminStatsGrid from "@/components/admin/AdminStatsGrid.vue";
import SettingsEntryCard from "@/components/admin/SettingsEntryCard.vue";
import type { AdminTaskTabStat } from "@/components/admin/adminTaskTabHeader";

withDefaults(
  defineProps<{
    stats?: AdminTaskTabStat[];
    settingsTitle: string;
    settingsHint?: string;
    refreshing?: boolean;
  }>(),
  {
    stats: () => [],
    refreshing: false,
  },
);

const emit = defineEmits<{
  refresh: [];
  "open-settings": [];
}>();
</script>

<template>
  <AdminStatsGrid>
    <slot />
    <StatCard
      v-for="(stat, index) in stats"
      :key="`${stat.label}-${index}`"
      :icon="stat.icon"
      :value="stat.value"
      :label="stat.label"
      :tone="stat.tone ?? 'blue'"
    >
      <template v-if="stat.refresh" #actions>
        <AppIconButton
          icon="fa-sync-alt"
          label="刷新"
          variant="secondary"
          size="xs"
          :disabled="refreshing"
          title="刷新任务列表"
          @click="emit('refresh')"
        />
      </template>
    </StatCard>
    <SettingsEntryCard
      :title="settingsTitle"
      :hint="settingsHint"
      @click="emit('open-settings')"
    />
  </AdminStatsGrid>
</template>
