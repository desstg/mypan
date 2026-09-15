<script setup lang="ts">
import { computed, ref } from "vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import { usePerformancePanel } from "@/composables/usePerformancePanel";
import type { DropdownMenuItem } from "@/types/menu";

const emit = defineEmits<{
  refresh: [];
  "update:view": ["list" | "grid"];
  "create-folder": [];
  "upload-file": [];
  "upload-folder": [];
  "offline-download": [];
  "open-upload-tasks": [];
  "toggle-favorites": [];
}>();

const { expanded: performanceExpanded, toggle: togglePerformancePanel } = usePerformancePanel();

const createOpen = ref(false);
const props = defineProps<{
  isAdmin: boolean;
  view: "list" | "grid";
  refreshing: boolean;
  responseTime: string;
  cacheRate: string;
  uploadTaskActive?: boolean;
  uploadTaskFailed?: boolean;
  uploadTaskSuccess?: boolean;
  uploadTaskLabel?: string;
  uploadTaskCount?: number;
  favoritesOpen?: boolean;
  offlineDownloadSupported?: boolean;
  compactHome?: boolean;
}>();

const createItems = computed<DropdownMenuItem[]>(() => {
  const items: DropdownMenuItem[] = [
    { key: "create-folder", label: "新建文件夹", icon: "folder", type: "action" },
    { key: "upload-file", label: "上传文件", icon: "file", type: "action" },
    { key: "upload-folder", label: "上传文件夹", icon: "folder-open", type: "action" },
  ];
  if (props.offlineDownloadSupported) {
    items.push({ key: "offline-download", label: "离线下载", icon: "cloud", type: "action" });
  }
  return items;
});

function onCreateSelect(key: string) {
  if (key === "create-folder") emit("create-folder");
  else if (key === "upload-file") emit("upload-file");
  else if (key === "upload-folder") emit("upload-folder");
  else emit("offline-download");
}
</script>

<template>
  <div class="file-toolbar" :class="{ 'file-toolbar--compact': compactHome }">
    <div v-if="compactHome" class="file-toolbar__navigation">
      <slot name="navigation" :performance-expanded="performanceExpanded" />
    </div>
    <div class="file-toolbar__left">
      <button
        v-if="isAdmin"
        type="button"
        class="file-toolbar__favorites-toggle"
        :class="{ active: favoritesOpen }"
        :title="favoritesOpen ? '关闭收藏夹' : '打开收藏夹'"
        aria-label="切换收藏夹显示"
        @click="emit('toggle-favorites')"
      >
        <SvgIcon
          v-if="compactHome"
          :name="favoritesOpen ? 'star-solid' : 'star'"
          :size="17"
          class-name="file-toolbar__favorites-toggle-icon"
        />        <SvgIcon
          v-else
          name="chevron-down"
          :size="16"
          class-name="file-toolbar__favorites-toggle-icon"
        />
      </button>

      <AppDropdown
        v-if="isAdmin"
        v-model:open="createOpen"
        trigger="click"
        align="left"
        :items="createItems"
        density="comfortable"
        @select="onCreateSelect"
      >
        <template #trigger="{ open, toggle }">
          <AppButton
            variant="primary"
            class="file-toolbar__menu-trigger"
            :aria-expanded="open"
            @click="toggle"
          >
            <span class="file-toolbar__menu-main">
              <span class="file-toolbar__icon"><SvgIcon name="plus" :size="17" /></span>
              <span class="file-toolbar__menu-label">新建</span>
            </span>
            <span class="file-toolbar__menu-arrow" :class="{ open }">
              <SvgIcon name="chevron-down" :size="14" />
            </span>
          </AppButton>
        </template>
      </AppDropdown>

      <AppButton variant="secondary" class="file-toolbar__btn file-toolbar__btn--refresh" :disabled="refreshing" @click="emit('refresh')">
        <span class="file-toolbar__icon" :class="{ spin: refreshing }">
          <SvgIcon name="refresh" :size="17" />
        </span>
        <span>刷新</span>
      </AppButton>

    </div>

    <div class="file-toolbar__right">
      <button
        v-if="isAdmin && !compactHome"
        type="button"
        class="transfer-status-chip"
        :class="{
          active: uploadTaskActive,
          failed: uploadTaskFailed && !uploadTaskActive,
          success: uploadTaskSuccess && !uploadTaskActive && !uploadTaskFailed,
        }"
        :title="uploadTaskLabel || '传输列表'"
        @click="emit('open-upload-tasks')"
      >
        <span class="transfer-status-icon-wrap">
          <svg
            v-if="uploadTaskSuccess && !uploadTaskActive && !uploadTaskFailed"
            class="transfer-status-icon success"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="1.75"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m3.5 8.5 3 3 6-7" />
          </svg>
          <span v-else class="transfer-status-icon transfer-status-icon-svg">
            <SvgIcon name="upload" :size="14" />
          </span>
        </span>
        <span v-if="compactHome && (uploadTaskCount || 0) > 0" class="transfer-status-badge">
          {{ Math.min(uploadTaskCount || 0, 99) }}
        </span>
        <span class="transfer-status-text">{{ uploadTaskLabel || "暂无传输任务" }}</span>
      </button>

      <div v-if="!compactHome" class="performance-panel" :class="{ expanded: performanceExpanded }">
        <div class="performance-metrics" :aria-hidden="!performanceExpanded">
          <span class="performance-metric">
            <span class="performance-label">响应时间</span>
            <span class="performance-value">{{ responseTime }}</span>
          </span>
          <span class="performance-divider" />
          <span class="performance-metric">
            <span class="performance-label">缓存命中率</span>
            <span class="performance-value">{{ cacheRate }}</span>
          </span>
        </div>
        <button
          type="button"
          class="performance-toggle"
          :aria-expanded="performanceExpanded"
          :title="performanceExpanded ? '收起性能信息' : '展开性能信息'"
          @click="togglePerformancePanel"
        >
          <span class="file-toolbar__icon"><SvgIcon name="lightning" :size="17" /></span>
        </button>
      </div>

      <div class="view-mode-switch" role="group" aria-label="文件视图切换">
        <button
          type="button"
          class="view-mode-btn"
          :class="{ active: view === 'list' }"
          title="列表视图"
          @click="emit('update:view', 'list')"
        >
          <span class="view-icon list" />
        </button>
        <button
          type="button"
          class="view-mode-btn"
          :class="{ active: view === 'grid' }"
          title="网格视图"
          @click="emit('update:view', 'grid')"
        >
          <span class="view-icon grid" />
        </button>
      </div>
    </div>
  </div>
</template>
