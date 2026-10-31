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
    <!-- 手机端（≤768px）的动作按钮：缩成图标浮在**页面右上角**。
         桌面仍用下面那条吸顶栏里的文字按钮 —— 手机上一屏只放得下一张卡片，
         两行吸顶栏吃掉的比例太高（用户 2026-10-30 要求）。
         两处读的是**同一个** `scrapePanelRef`，所以文案 / 角标 / 禁用态不会各自为政。
         「已选几部」「刮到第几部」这些文字塞不进图标，改由角标承载。
         两面墙（番号 / TMDB）各一套，条件与页头那两段 `v-if` 逐条对应，
         不多一颗也不少一颗。 -->
    <div v-if="isScrapeTab && scrapePanelRef" class="wall-mobile-actions">
      <!-- —— 番号：批量选择 + 开始刮削（内部仍是在线刮削那套，只是按钮这样叫）—— -->
      <template v-if="scrapePanelRef.isJavSource">
        <button
          type="button"
          class="wall-mobile-actions__btn"
          :class="{ 'wall-mobile-actions__btn--on': scrapePanelRef.batchMode }"
          :disabled="scrapePanelRef.onlineScraping"
          :aria-label="scrapePanelRef.batchModeLabel ?? '批量选择'"
          :aria-pressed="Boolean(scrapePanelRef.batchMode)"
          @click="scrapePanelRef.toggleBatchMode()"
        >
          <i class="fas fa-check-square" aria-hidden="true" />
          <span v-if="scrapePanelRef.pickedBadge" class="wall-mobile-actions__badge">
            {{ scrapePanelRef.pickedBadge }}
          </span>
        </button>
        <button
          type="button"
          class="wall-mobile-actions__btn wall-mobile-actions__btn--primary"
          :class="{ 'wall-mobile-actions__btn--wide': scrapePanelRef.onlineScrapeBadge }"
          :disabled="scrapePanelRef.onlineScraping || scrapePanelRef.onlineScrapeDisabled"
          :aria-label="scrapePanelRef.onlineScrapeLabel ?? '开始刮削'"
          @click="scrapePanelRef.onlineScrape()"
        >
          <i class="fas fa-wand-magic-sparkles" aria-hidden="true" />
          <!-- 进度写成**按钮内的文字**（按钮随之撑宽），不做角标：
               `8/128` 这种五六个字符的东西当角标时会横跨整个 34px 圆钮、
               把图标整片盖住，连点都点不到（实测 `elementFromPoint` 命中角标）。 -->
          <span v-if="scrapePanelRef.onlineScrapeBadge" class="wall-mobile-actions__progress">
            {{ scrapePanelRef.onlineScrapeBadge }}
          </span>
        </button>
      </template>
      <!-- —— TMDB：批量选择 + 开始/停止刮削（存量补缺已并进「开始刮削」，见下）—— -->
      <template v-else>
        <button
          type="button"
          class="wall-mobile-actions__btn"
          :class="{ 'wall-mobile-actions__btn--on': scrapePanelRef.tmdbBatchMode }"
          :disabled="scrapePanelRef.running"
          :aria-label="scrapePanelRef.tmdbBatchModeLabel ?? '批量选择'"
          :aria-pressed="Boolean(scrapePanelRef.tmdbBatchMode)"
          @click="scrapePanelRef.tmdbToggleBatchMode()"
        >
          <i class="fas fa-check-square" aria-hidden="true" />
          <span v-if="scrapePanelRef.tmdbPickedBadge" class="wall-mobile-actions__badge">
            {{ scrapePanelRef.tmdbPickedBadge }}
          </span>
        </button>
        <button
          v-if="scrapePanelRef.running"
          type="button"
          class="wall-mobile-actions__btn wall-mobile-actions__btn--primary"
          aria-label="停止刮削"
          @click="scrapePanelRef.stopScrape()"
        >
          <i class="fas fa-stop" aria-hidden="true" />
        </button>
        <button
          v-else
          type="button"
          class="wall-mobile-actions__btn wall-mobile-actions__btn--primary"
          :class="{ 'wall-mobile-actions__btn--wide': scrapePanelRef.tmdbScrapeBadge }"
          :disabled="scrapePanelRef.tmdbBatchDisabled"
          aria-label="开始刮削"
          @click="scrapePanelRef.startScrape()"
        >
          <i class="fas fa-wand-magic-sparkles" aria-hidden="true" />
          <!-- 同一颗图标、同一种写法：两面墙的「开跑」都长这样（用户 2026-10-30
               要求统一）。进度同理放在按钮内当文字，不做角标。 -->
          <span v-if="scrapePanelRef.tmdbScrapeBadge" class="wall-mobile-actions__progress">
            {{ scrapePanelRef.tmdbScrapeBadge }}
          </span>
        </button>
      </template>
    </div>

    <!-- ⚠️ `sticky` 只开在「海报墙」这个 tab 上：一屏几十张卡片，滚十几屏之后
         页头这几颗动作按钮就够不着了（用户 2026-10-30 要求）。写条件而不是无条件开，
         是因为另外两个 tab（增强工具 / 备份管理）不长，粘着只是白挡内容。
         两面墙都开 —— 番号要「批量选择 / 开始刮削」，TMDB 要「批量选择 /
         开始刮削」（原来那颗「补齐剧照与演员」2026-10-31 并进了「开始刮削」）。
         手机那一档**不粘**（`sticky-mobile="false"`）：这条栏在手机上被
         「Tab 一排 + 动作按钮一排」顶成两行，粘住吃掉 12% 的可视高度，
         改为上面那组图标浮标。 -->
    <SectionTabBar
      :model-value="activeTab"
      :tabs="tabs"
      :sticky="isScrapeTab"
      :sticky-mobile="false"
      @update:model-value="setActiveTab"
    >
      <template #actions>
        <!-- 番号影片的任务上**不显示**这两个按钮：那套走的是 TMDB 刮削，会往同一批目录
             写 nfo，而「标记为正常」那一步（clearScrapedMetadata）的正则正好命中
             thumb.jpg / poster.jpg —— 把番号生成器写的图删掉。番号自己的动作在
             卡片上（编辑 / 重刮）与设置下拉里（刷新元数据）。 -->
        <template v-if="isScrapeTab && !scrapePanelRef?.isJavSource">
          <!-- 手机端（≤768px）这一组整个隐藏，改用上面那组图标浮标
               （`.wall-mobile-actions`）：手机上一屏只放得下一张卡片，
               页头栏被这三颗按钮顶成两行之后，吸顶一挂就吃掉 12% 的可视高度
               （用户 2026-10-30 要求）。桌面保持在栏里显示，一字不改。 -->
          <div class="wall-head-actions">
          <!-- 批量选择：只刮 / 只补勾中的那几部。
               位置由用户指定 —— 在「开始刮削」**左边**，所以它管着右边那颗：
               勾了片子之后点「开始刮削」就只处理这几部，一部没勾时右边那颗**禁用**
               （开着批量选择却整库扫，等于这颗按钮白开）。

               开态用 primary 高亮，与右边那颗区分开「我现在在哪一档」。
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
          <!-- 开始刮削：任务源与本地目录源**共用这一颗**（原来本地目录上还有一颗
               「AI 识别刮削」，只差一个「这次要不要用 AI」的意图，已按用户要求合并掉，
               好给页头腾位置）。

               **勾中的那几部里，「已经刮过、只是缺剧照/演员/背景图」的走补齐**
               （只补缺，不覆盖已有 nfo 正文与图）—— 原来页头另有一颗「补齐剧照与演员」，
               与这颗管的是同一件事、并排只会让人猜该点哪颗，用户 2026-10-31 拍板合并，
               页头只留一颗。整根扫与「覆盖」策略的行为一个字都没变（见后端 run()）。

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
          </div>
        </template>
        <!-- 番号影片任务：同一位置、同样式的主按钮，但干的是**另一件事** ——
             打上游（JAVDB）补齐缺的元数据。TMDB 那颗「开始刮削」
             在番号任务上**不能出现**：那套会往同一批目录写 nfo，而「标记为正常」
             那一步的正则正好命中 thumb.jpg / poster.jpg，会把番号生成的图删掉。 -->
        <template v-else-if="isScrapeTab && scrapePanelRef?.isJavSource">
          <!-- 手机端（≤768px）这一组整个隐藏，改用上面那两颗浮标（`.wall-mobile-actions`）：
               手机上一屏只放得下一张卡片，页头栏被这两颗按钮顶成两行之后，
               吸顶一挂就吃掉 12% 的可视高度（用户 2026-10-30 要求）。
               桌面保持在栏里显示，一字不改。 -->
          <div class="wall-head-actions">
            <!-- 批量选择：只刮勾中的那几部（原来那颗按钮只会整根扫，补几部新片也要跑全库）。
                 开态用 primary 高亮，与右边那颗「开始刮削」区分开「我现在在哪一档」。

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
              {{ scrapePanelRef?.onlineScrapeLabel ?? "开始刮削" }}
            </AppButton>
          </div>
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

<style scoped>
/* ——————— 手机端那组图标浮标（只在 ≤768px 存在） ———————
 *
 * 两面墙共用一套样式：两面都是「批量选择 + 开始/停止刮削」两颗，同一行右对齐
 * （TMDB 原来还有第三颗「补齐剧照与演员」，2026-10-31 并进了「开始刮削」）。
 *
 * 为什么是 `sticky` 而不是 `fixed`：滚动容器是 `.admin__body`（`overflow-y: auto`），
 * `fixed` 以视口为准 —— 它上方那 42px 是 `.global-chrome`，`fixed` 得写死
 * `top: 42px` 才不压住它，而那个高度是 AdminShell 的内部变量（手机上还随断点变），
 * 抄一份就等于埋一颗「改了壳忘了改这里」。`sticky` 跟着滚动容器自己的可视边算，天然对得上。
 *
 * 位置在右上角：整行右对齐，`top` 吃掉滚动容器的**上内边距**（手机上 16px，见
 * AdminShell 的 `.admin__body`）—— 与 `.tabbar--sticky` 同一条规矩，留成 0 的话
 * 它会浮在内边距之下、上方那条缝里滚过去的卡片照样露出来。
 *
 * `z-index: 45` 与吸顶栏同档：压过刮削面板里 40 的气泡，又远低于所有 Teleport 到
 * body 的下拉 / 弹窗（`--z-dropdown` 10050 起），它们不会被这两颗小圆钮挡住。
 *
 * ⚠️ 它**是普通流里的一行**（不是零高度浮层）：一屏只放得下一张卡片，零高度浮层
 * 会和下面那条 tab 栏抢同一行，320px 上直接压住「备份管理」那颗 tab。留成一行
 * 自带的高度，代价只是页面顶端多 44px，**一滚就走**（sticky 的占位随内容滚掉）。 */
.wall-mobile-actions {
  display: none;
}

/* 桌面这颗容器只是替 `#actions` 槽把两颗按钮继续横着排 —— 槽本身是 flex，
   加一层 div 之后若用默认的 block，两颗按钮会上下摞起来。 */
.wall-head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

@media (max-width: 768px) {
  .wall-mobile-actions {
    position: sticky;
    top: -16px;
    z-index: 45;
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-bottom: 10px;
    /* 这一行只有右边两颗圆钮是实心的，左边那截空白不该挡住底下的卡片 ——
       吸顶之后它是浮在内容上的一层，不设这个的话顶部那 34px 横条会整片
       吃掉点击（滚到一半想点最上面那张卡，点在它右边就点不动）。 */
    pointer-events: none;
  }

  .wall-mobile-actions__btn {
    pointer-events: auto;
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    width: 34px;
    height: 34px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-pill);
    background: var(--surface);
    color: var(--text-muted);
    box-shadow: var(--shadow-card);
    font-size: 14px;
    cursor: pointer;
    transition: var(--transition);
  }
  .wall-mobile-actions__btn:not(:disabled):active {
    background: var(--surface-sunken);
  }
  .wall-mobile-actions__btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  /* 开态（批量选择进行中）：与桌面那颗 primary 按钮同一套意思 ——
     品牌色底、白字，一眼看出「我现在在哪一档」。 */
  .wall-mobile-actions__btn--on {
    background: var(--brand-gradient-h);
    border-color: transparent;
    color: var(--text-on-brand);
  }
  .wall-mobile-actions__btn--on:not(:disabled):active {
    background: var(--brand-gradient-h-hover);
  }

  /* 主按钮（在线刮削 / 开始刮削）：与桌面那颗 primary 同色，一眼分得出
     「哪颗是真正开跑的那一个」。停止刮削也用它 —— 那时它是这一行里最要紧的按钮。 */
  .wall-mobile-actions__btn--primary {
    background: var(--brand-gradient-h);
    border-color: transparent;
    color: var(--text-on-brand);
  }
  .wall-mobile-actions__btn--primary:not(:disabled):active {
    background: var(--brand-gradient-h-hover);
  }
  .wall-mobile-actions__btn--primary:disabled {
    opacity: 0.55;
  }

  /* 角标：批量选择那颗上的「已选几部」。顶上按钮边界、跨在圆钮右上角。
     ⚠️ 只用于**一位到两位**的短数字。像 `8/128` 这种进度别往角标里塞 ——
        它比 34px 的圆钮还宽，会横跨整个按钮、把图标整片盖住，连点都点不到
        （实测 `elementFromPoint` 打在中心命中的就是角标）。进度用下面
        `.wall-mobile-actions__progress` 当按钮内文字。 */
  .wall-mobile-actions__badge {
    position: absolute;
    top: -6px;
    right: -8px;
    min-width: 17px;
    height: 17px;
    padding: 0 4px;
    border-radius: var(--radius-pill);
    background: var(--brand);
    color: var(--text-on-brand);
    font-size: 10px;
    font-weight: 700;
    line-height: 17px;
    text-align: center;
    white-space: nowrap;
    box-shadow: 0 0 0 2px var(--bg);
  }

  /* 开态时按钮自己就是品牌色底 —— 角标再用品牌色会糊成一团，翻成白底品牌字。 */
  .wall-mobile-actions__btn--on .wall-mobile-actions__badge {
    background: var(--surface);
    color: var(--brand);
  }

  /* 刮削进度（按钮内文字）：有它时按钮自动撑成胶囊，图标 + 进度一起读。 */
  .wall-mobile-actions__progress {
    margin-left: 6px;
    font-size: 11px;
    font-weight: 700;
    line-height: 1;
    font-variant-numeric: tabular-nums;
  }
  /* 撑宽那一档：`width: auto` + 左右内边距，从圆钮变成胶囊。
     不撑宽的话 `8/128` 会溢出到圆外、压到旁边那颗按钮上。 */
  .wall-mobile-actions__btn--wide {
    width: auto;
    padding: 0 10px;
  }

  /* 桌面那组文字按钮在手机上整个收起 —— 否则又会被排成两行，正是要治的那个毛病。
     （这条写在基础规则之后，同优先级下后写的赢，不需要 `!important`。） */
  .wall-head-actions {
    display: none;
  }
}
</style>
