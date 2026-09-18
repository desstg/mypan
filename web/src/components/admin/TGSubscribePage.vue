<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import TGDiscoverWall from "@/components/admin/TGDiscoverWall.vue";
import TGSubscribedPanel from "@/components/admin/TGSubscribedPanel.vue";
import TGSubscribeSettingsDrawer from "@/components/admin/TGSubscribeSettingsDrawer.vue";
import TGTitleDetailModal from "@/components/admin/TGTitleDetailModal.vue";
import { getApiErrorMessage } from "@/api/client";
import { createTGSubscription, fetchTGSubscriptions, fetchTGStats } from "@/api/tgSubscribe";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useAccountsStore } from "@/stores/accounts";
import { toast } from "@/composables/useToast";
import type { TGMediaType, TGStats, TGSubscription, TGTMDBSearchResult } from "@/types/tg-subscribe";
import "@/styles/tg-subscribe.css";

// 顶栏形态照 8.png：左侧「图标 + tab」，右侧「搜索框 + 齿轮」。
// 主题切换不在这里重复做 —— AdminGlobalActions 里已经有了。

// 「已订阅」放在最左：它是这一页的「我的」，后面三个才是去外面找片。
const TAB_SUBSCRIBED = "subscribed";
const TAB_MOVIE = "movie";
const TAB_TV = "tv";
const TAB_JAV = "jav";

const { activeTab, setActiveTab } = useSectionTabRoute(TAB_SUBSCRIBED, [
  TAB_SUBSCRIBED,
  TAB_MOVIE,
  TAB_TV,
  TAB_JAV,
]);

const accountsStore = useAccountsStore();

/**
 * 当前 tab 对应的 TMDB 媒体类型。
 *
 * 只看**发现墙用不用得上**：已订阅读的是本地订阅表，番号是占位空档 ——
 * 两者都不打 TMDB，所以统一回落成 movie 只是给不渲染的组件一个合法值。
 */
const mediaType = computed<TGMediaType>(() => (activeTab.value === TAB_TV ? "tv" : "movie"));

const TABS = [
  { key: TAB_SUBSCRIBED, label: "已订阅" },
  { key: TAB_MOVIE, label: "电影" },
  { key: TAB_TV, label: "剧集" },
  { key: TAB_JAV, label: "番号" },
];

/** 发现墙只在电影/剧集两个 tab 出现；搜索框也一样。 */
const discoverVisible = computed(() => activeTab.value === TAB_MOVIE || activeTab.value === TAB_TV);

const subscriptions = ref<TGSubscription[]>([]);
const stats = ref<TGStats | null>(null);
const configOpen = ref(false);

const detailOpen = ref(false);
const detailItem = ref<TGTMDBSearchResult | null>(null);

// 搜索：400ms 防抖后再交给海报墙，避免每敲一个字打一次 TMDB。
const keywordInput = ref("");
const keyword = ref("");
let searchTimer: ReturnType<typeof setTimeout> | null = null;

/** 已订阅的 TMDB 标识集合，海报墙用它把「+ 订阅」换成 ✓。 */
const subscribedKeys = computed(() => {
  const set = new Set<string>();
  for (const sub of subscriptions.value) {
    set.add(`${sub.media_type}:${sub.tmdb_id}`);
  }
  return set;
});

const subscriptionCount = computed(() => subscriptions.value.filter((s) => s.status === "active").length);

const fetchHint = computed(() => {
  const status = stats.value?.status;
  if (!status) return null;
  if (!status.enabled) {
    return { tone: "warning" as const, text: "TG 订阅未启用，点右上角齿轮打开。" };
  }
  if (status.status === "error") {
    return { tone: "danger" as const, text: `抓取异常：${status.status_message || "请到「系统设置 → 其他设置 → 网络代理」检查代理"}` };
  }
  if (status.connected) {
    const mins = Math.max(1, Math.round(status.poll_interval_sec / 60));
    return {
      tone: "success" as const,
      text: `正在监听 ${status.channel_count} 个频道，约每 ${mins} 分钟抓取一次`,
    };
  }
  return { tone: "muted" as const, text: "等待首轮抓取…" };
});

async function loadSubscriptions() {
  try {
    subscriptions.value = await fetchTGSubscriptions();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载订阅列表失败"));
  }
}

async function loadStats() {
  try {
    stats.value = await fetchTGStats();
  } catch {
    // 状态条是辅助信息，失败静默。
  }
}

function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    keyword.value = keywordInput.value;
  }, 400);
}

function clearSearch() {
  keywordInput.value = "";
  keyword.value = "";
}

function openDetail(item: TGTMDBSearchResult) {
  detailItem.value = item;
  detailOpen.value = true;
}

/** 海报墙右上角的「+」：直接按默认设置订阅，不用进弹窗。 */
async function quickSubscribe(item: TGTMDBSearchResult) {
  try {
    await createTGSubscription({
      tmdb_id: String(item.id),
      media_type: mediaType.value,
      title: item.title || item.name || "",
      original_title: item.original_title || item.original_name || "",
      year: Number((item.release_date || item.first_air_date || "").slice(0, 4)) || 0,
      poster_path: item.poster_path || "",
      overview: item.overview || "",
      quality_profile_id: 0,
      target_account_id: 0,
      target_parent_id: "",
      target_display_path: "",
      push_provider: "auto",
      collect_window_min: 5,
      upgrade_enabled: true,
    });
    toast.success(`已订阅《${item.title || item.name}》，可在卡片详情里调目标与画质`);
    await Promise.all([loadSubscriptions(), loadStats()]);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "订阅失败"));
  }
}

async function onChanged() {
  await Promise.all([loadSubscriptions(), loadStats()]);
}

// 切换 tab 时清掉搜索词 —— 上一个是关键词的结果留在另一个 tab 里很突兀。
watch(activeTab, () => clearSearch());

onMounted(async () => {
  await Promise.all([loadSubscriptions(), loadStats(), accountsStore.loadAccounts()]);
});
</script>

<template>
  <div class="tg-wall">
    <SectionTabBar
      :model-value="activeTab"
      :tabs="TABS"
      icon="fire"
      @update:model-value="setActiveTab"
    >
      <template #actions>
        <div style="display: flex; align-items: center; gap: 10px">
          <div v-if="discoverVisible" style="width: 240px">
            <AppInput
              v-model="keywordInput"
              placeholder="搜索影片，回车或稍候自动搜索"
              @input="onSearchInput"
              @keyup.enter="keyword = keywordInput"
            />
          </div>
          <AppButton v-if="discoverVisible && keyword" type="button" variant="ghost" size="sm" @click="clearSearch">
            清除
          </AppButton>
          <button
            type="button"
            class="tg-icon-btn"
            title="TG 订阅设置"
            aria-label="TG 订阅配置"
            @click="configOpen = true"
          >
            <i class="fas fa-cog" aria-hidden="true" />
          </button>
        </div>
      </template>
    </SectionTabBar>

    <div v-if="fetchHint" style="display: flex; align-items: center; gap: 10px">
      <AdminStatusPill :tone="fetchHint.tone">{{ fetchHint.text }}</AdminStatusPill>
      <span v-if="subscriptionCount" class="tg-detail__sub">
        正在追更 {{ subscriptionCount }} 部 · 共 {{ subscriptions.length }} 条订阅
      </span>
    </div>

    <TGSubscribedPanel v-if="activeTab === TAB_SUBSCRIBED" @changed="onChanged" />

    <!-- 番号：后端还没有这个媒体类型，所以这里只放一句说明。
         做成空状态而不是把电影的内容伪装成番号 —— 那会让人以为筛选坏了。 -->
    <AdminEmptyState
      v-else-if="activeTab === TAB_JAV"
      icon="🚧"
      title="番号订阅还没开放"
      description="这一档只是先占个位置。番号订阅还没有实现，等做出来再回到这里。"
    />

    <TGDiscoverWall
      v-else
      :media-type="mediaType"
      :keyword="keyword"
      :subscribed-keys="subscribedKeys"
      @open="openDetail"
      @subscribe="quickSubscribe"
    />

    <TGTitleDetailModal
      :open="detailOpen"
      :item="detailItem"
      :media-type="mediaType"
      @close="detailOpen = false"
      @changed="onChanged"
    />

    <TGSubscribeSettingsDrawer
      :open="configOpen"
      @close="configOpen = false"
      @changed="onChanged"
    />
  </div>
</template>
