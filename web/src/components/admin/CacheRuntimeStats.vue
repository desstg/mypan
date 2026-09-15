<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { clearCache, fetchCacheStats } from "@/api/cache";
import AppCardActionButton from "@/components/base/AppCardActionButton.vue";
import StatCard from "@/components/base/StatCard.vue";
import { toast } from "@/composables/useToast";
import { formatSize } from "@/utils/format";

const stats = reactive({
  totalKeys: 0,
  totalSize: 0,
  hitRate: 0,
});

const refreshing = ref(false);
const clearing = ref(false);

async function loadStats() {
  refreshing.value = true;
  try {
    const data = await fetchCacheStats();
    stats.totalKeys = data.total_keys ?? 0;
    stats.totalSize = data.total_size_bytes ?? 0;
    stats.hitRate = data.hit_rate ?? 0;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载缓存统计失败"));
  } finally {
    refreshing.value = false;
  }
}

async function handleClearCache() {
  clearing.value = true;
  try {
    const res = await clearCache();
    toast.success(`已清空 ${res.cleared_count} 条缓存`);
    await loadStats();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清空缓存失败"));
  } finally {
    clearing.value = false;
  }
}

onMounted(() => {
  void loadStats();
});

</script>

<template>
  <StatCard icon="fa-database" :value="stats.totalKeys" label="缓存条目" tone="blue" side-actions>
    <template #actions>
      <div class="cache-runtime-actions">
        <AppCardActionButton
          icon="fa-sync-alt"
          label="刷新"
          variant="secondary"
          :disabled="refreshing"
          title="刷新缓存统计"
          @click="loadStats"
        />
        <AppCardActionButton
          icon-class="fas fa-trash-can"
          label="清理"
          variant="danger"
          :disabled="clearing"
          title="清空缓存"
          @click="handleClearCache"
        />
      </div>
    </template>
  </StatCard>
  <StatCard icon="fa-hdd" :value="formatSize(stats.totalSize)" label="缓存大小" tone="purple" />
  <StatCard icon="fa-bullseye" :value="`${stats.hitRate}%`" label="命中率" tone="amber" />
</template>

<style scoped>
.cache-runtime-actions {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 6px;
}
</style>
