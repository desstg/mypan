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
  { key: SCRAPE_TAB, label: "海报墙" },
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
        <!-- 番号影片的任务上**不显示**这两个按钮：那套走的是 TMDB 刮削，会往同一批目录
             写 nfo，而「标记为正常」那一步（clearScrapedMetadata）的正则正好命中
             thumb.jpg / poster.jpg —— 把番号生成器写的图删掉。番号自己的动作在
             卡片上（编辑 / 重刮）与设置下拉里（刷新元数据）。 -->
        <template v-if="isScrapeTab && !scrapePanelRef?.isJavSource">
          <!-- 批量选择：只刮 / 只补勾中的那几部。
               位置由用户指定 —— 在「补齐剧照与演员」**左边**，所以它同时管着右边那两颗：
               勾了片子之后点「补齐剧照与演员」就只补这几部，点「开始刮削」就只刮这几部。
               一部没勾时右边两颗**禁用**（开着批量选择却整库扫，等于这颗按钮白开）。

               开态用 primary 高亮，与右边两颗区分开「我现在在哪一档」。
               ⚠️ 刮削期间禁用：提交那拍要按这份选择集算 id，中途改选中会让「点了哪几部」
               与「实际刮了哪几部」对不上（墙里也有一道同样的守卫）。 -->
          <AppButton
            type="button"
            :variant="scrapePanelRef?.tmdbBatchMode ? 'primary' : 'secondary'"
            :disabled="scrapePanelRef?.running"
            :title="
              scrapePanelRef?.tmdbBatchMode
                ? '点卡片勾选要刮的影片，再点右边的按钮开始；再点一次退出'
                : '只处理勾选的影片，不再整库扫一遍'
            "
            @click="scrapePanelRef?.tmdbToggleBatchMode()"
          >
            {{ scrapePanelRef?.tmdbBatchModeLabel ?? "批量选择" }}
          </AppButton>
          <!-- 补齐：给**已经刮过**的作品补新抓的剧照/演员/背景图。它必须放在页头 ——
               藏在面板里那颗 ⚙ 的下拉菜单里时用户根本找不到（实测问「在哪里」）。
               `missing_only` 策略下已刮过的作品会被正常刮削跳过，这是它们唯一的入口。
               勾了几部就只补这几部（`item_ids` 那条路，后端按作品过滤）。 -->
          <AppButton
            type="button"
            variant="secondary"
            :disabled="scrapePanelRef?.running || scrapePanelRef?.tmdbBatchDisabled"
            :title="
              scrapePanelRef?.tmdbBatchDisabled
                ? '先在卡片上勾几部，或关掉批量选择'
                : undefined
            "
            @click="scrapePanelRef?.backfillArtwork()"
          >
            补齐剧照与演员
          </AppButton>
          <!-- 开始刮削：任务源与本地目录源**共用这一颗**（原来本地目录上还有一颗
               「AI 识别刮削」，只差一个「这次要不要用 AI」的意图，已按用户要求合并掉，
               好给页头腾位置）。

               ⚠️ AI 现在是**设置项**（「海报墙 → 设置 → 刮削策略 → AI 补认识别」），
               不再是点击时带的参数：合并之后页头没有第二个入口能表达「这次别用 AI」，
               只能读设置。所以这两颗按钮**不去读设置、不弹「先配置 AI」、按钮也不为此变样** ——
               用户原话「AI 没配好时点了不能出错」。
               后端的闸是 `src.isLocal() && 设置开着 && AI 可用`（strmscrape/scrape_ai.go）：
               STRM 任务源**永远不走 AI**，那套的时间与结果与改造前逐字一致。 -->
          <AppButton
            v-if="scrapePanelRef?.running"
            type="button"
            variant="secondary"
            @click="scrapePanelRef?.stopScrape()"
          >
            停止刮削
          </AppButton>
          <AppButton
            v-else
            type="button"
            variant="primary"
            :disabled="scrapePanelRef?.tmdbBatchDisabled"
            :title="
              scrapePanelRef?.tmdbBatchDisabled
                ? '先在卡片上勾几部，或关掉批量选择'
                : undefined
            "
            @click="scrapePanelRef?.startScrape()"
          >
            开始刮削
          </AppButton>
        </template>
        <!-- 番号影片任务：同一位置、同样式的主按钮，但干的是**另一件事** ——
             打上游（JAVDB）补齐缺的元数据。TMDB 那两颗（开始刮削 / 补齐剧照与演员）
             在番号任务上**不能出现**：那套会往同一批目录写 nfo，而「标记为正常」
             那一步的正则正好命中 thumb.jpg / poster.jpg，会把番号生成的图删掉。 -->
        <template v-else-if="isScrapeTab && scrapePanelRef?.isJavSource">
          <!-- 批量选择：只刮勾中的那几部（原来那颗按钮只会整根扫，补几部新片也要跑全库）。
               开态用 primary 高亮，与右边那颗「在线刮削」区分开「我现在在哪一档」。

               ⚠️ 刮削期间禁用：收尾要靠这份选择集清场，中途退出会留下一堆
               「看不见但仍被选中」的条目（墙里也有同一道守卫，这里是可见的那一道）。 -->
          <AppButton
            type="button"
            :variant="scrapePanelRef?.batchMode ? 'primary' : 'secondary'"
            :disabled="scrapePanelRef?.onlineScraping"
            :title="
              scrapePanelRef?.batchMode
                ? '点卡片勾选要刮的影片，再点右边的按钮开始；再点一次退出'
                : '只刮选中的影片，不再整目录扫一遍'
            "
            @click="scrapePanelRef?.toggleBatchMode()"
          >
            {{ scrapePanelRef?.batchModeLabel ?? "批量选择" }}
          </AppButton>
          <AppButton
            type="button"
            variant="primary"
            :disabled="scrapePanelRef?.onlineScraping || scrapePanelRef?.onlineScrapeDisabled"
            :title="
              scrapePanelRef?.onlineScrapeDisabled
                ? '先在卡片上勾几部，或关掉批量选择刮整个目录'
                : undefined
            "
            @click="scrapePanelRef?.onlineScrape()"
          >
            {{ scrapePanelRef?.onlineScrapeLabel ?? "在线刮削" }}
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
