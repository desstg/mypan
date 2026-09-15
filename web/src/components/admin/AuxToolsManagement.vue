<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onDeactivated, reactive, ref, watch, watchEffect } from "vue";
import { useRoute } from "vue-router";
import AppButton from "@/components/base/AppButton.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import type StrmScrapePanelComponent from "@/components/admin/StrmScrapePanel.vue";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { readPanelSaving, type SettingsPanelExpose } from "@/composables/useSettingsForm";

const StrmScrapePanel = defineAsyncComponent(() => import("@/components/admin/StrmScrapePanel.vue"));
const StrmScrapeSettings = defineAsyncComponent(() => import("@/components/admin/StrmScrapeSettings.vue"));
const CloudToolsPanel = defineAsyncComponent(() => import("@/components/admin/CloudToolsPanel.vue"));
const BackupRestorePanel = defineAsyncComponent(() => import("@/components/admin/BackupRestorePanel.vue"));

const SCRAPE_TAB = "scrape";
const PROXY_TAB = "proxy";
const ENHANCED_TAB = "enhanced";
const BACKUP_TAB = "backup";
const tabs = [
  { key: SCRAPE_TAB, label: "STRM 刮削" },
  { key: ENHANCED_TAB, label: "增强工具" },
  { key: BACKUP_TAB, label: "备份管理" },
];

const settingsDrawerOpen = ref(false);
const enhancedSearchOpen = ref(false);
const scrapeSettingsVisited = ref(false);
const scrapePanelRef = ref<InstanceType<typeof StrmScrapePanelComponent> | null>(null);
const scrapeSettingsRef = ref<SettingsPanelExpose | null>(null);
const scrapePanelDirty = ref(false);

watchEffect(() => {
  scrapePanelDirty.value = (scrapeSettingsRef.value as SettingsPanelExpose | null)?.isDirty?.() ?? false;
});

const settingsPageDirty = computed(() => settingsDrawerOpen.value && scrapePanelDirty.value);

function revertDrawerSettings() {
  scrapeSettingsRef.value?.revert?.();
}

const { confirmDiscardChanges } = useSettingsPageDirty(settingsPageDirty, revertDrawerSettings);

const route = useRoute();
const initialTab = String(route.query.tab ?? "") === PROXY_TAB ? ENHANCED_TAB : SCRAPE_TAB;
const { activeTab, setActiveTab } = useSectionTabRoute(
  initialTab,
  [SCRAPE_TAB, ENHANCED_TAB, BACKUP_TAB],
  {
    beforeTabChange: async () => {
      if (!settingsDrawerOpen.value) return true;
      const ok = await confirmDiscardChanges(() => scrapePanelDirty.value);
      if (!ok) return false;
      settingsDrawerOpen.value = false;
      return true;
    },
  },
);

// 重面板首次访问时才挂载，之后只隐藏不销毁，保留已加载状态。
const tabsVisited = reactive<Record<string, boolean>>({});
watch(
  activeTab,
  (tab) => {
    tabsVisited[tab] = true;
    if (tab !== ENHANCED_TAB) enhancedSearchOpen.value = false;
  },
  { immediate: true },
);

const drawerSaving = computed(() => readPanelSaving(scrapeSettingsRef.value?.saving));
const isScrapeTab = computed(() => activeTab.value === SCRAPE_TAB);
const isEnhancedTab = computed(() => activeTab.value === ENHANCED_TAB);
const isBackupTab = computed(() => activeTab.value === BACKUP_TAB);

async function openScrapeSettings() {
  scrapeSettingsVisited.value = true;
  settingsDrawerOpen.value = true;
  await nextTick();
  if (scrapeSettingsRef.value && !scrapeSettingsRef.value.isDirty?.()) {
    await scrapeSettingsRef.value.reload?.();
  }
}

async function closeSettingsDrawer() {
  if (!(await confirmDiscardChanges(() => scrapePanelDirty.value))) return;
  settingsDrawerOpen.value = false;
}

onDeactivated(() => {
  enhancedSearchOpen.value = false;
  if (!settingsDrawerOpen.value) return;
  if (scrapePanelDirty.value) revertDrawerSettings();
  settingsDrawerOpen.value = false;
});

async function handleDrawerSave() {
  await scrapeSettingsRef.value?.save?.();
}
</script>

<template>
  <div class="settings">
    <SectionTabBar :model-value="activeTab" :tabs="tabs" @update:model-value="setActiveTab">
      <template #actions>
        <template v-if="isScrapeTab">
          <AppButton
            v-if="scrapePanelRef?.running"
            type="button"
            variant="secondary"
            @click="scrapePanelRef?.stopScrape()"
          >
            停止刮削
          </AppButton>
          <AppButton v-else type="button" variant="primary" @click="scrapePanelRef?.startScrape()">
            开始刮削
          </AppButton>
        </template>
        <AppButton v-else-if="isEnhancedTab" type="button" variant="secondary" @click="enhancedSearchOpen = true">
          搜索工具
        </AppButton>
      </template>
    </SectionTabBar>

    <StrmScrapePanel
      v-if="tabsVisited[SCRAPE_TAB]"
      v-show="isScrapeTab"
      ref="scrapePanelRef"
      @open-settings="openScrapeSettings"
    />
    <CloudToolsPanel
      v-if="tabsVisited[ENHANCED_TAB]"
      v-show="isEnhancedTab"
      :search-open="enhancedSearchOpen"
      @update:search-open="enhancedSearchOpen = $event"
    />
    <BackupRestorePanel v-if="tabsVisited[BACKUP_TAB]" v-show="isBackupTab" />

    <AdminSettingsDrawer
      :open="settingsDrawerOpen"
      title="STRM 刮削设置"
      :saving="drawerSaving"
      :can-save="scrapePanelDirty"
      @close="closeSettingsDrawer"
      @cancel="closeSettingsDrawer"
      @save="handleDrawerSave"
    >
      <StrmScrapeSettings v-if="scrapeSettingsVisited" ref="scrapeSettingsRef" />
    </AdminSettingsDrawer>
  </div>
</template>
