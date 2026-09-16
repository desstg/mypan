<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import TGDiscoverWall from "@/components/admin/TGDiscoverWall.vue";
import TGSubscribeSettingsDrawer from "@/components/admin/TGSubscribeSettingsDrawer.vue";
import TGTitleDetailModal from "@/components/admin/TGTitleDetailModal.vue";
import { getApiErrorMessage } from "@/api/client";
import { createTGSubscription, fetchTGSubscriptions, fetchTGStats } from "@/api/tgSubscribe";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useAccountsStore } from "@/stores/accounts";
import { toast } from "@/composables/useToast";
import type { TGMediaType, TGStats, TGSubscription, TGTMDBSearchResult } from "@/types/tg-subscribe";
import "@/styles/tg-subscribe.css";

// 顶栏形态照 8.png：左侧「图标 + 电影 / 剧集」，右侧「搜索框 + 齿轮」。
// 主题切换不在这里重复做 —— AdminGlobalActions 里已经有了。

const { activeTab, setActiveTab } = useSectionTabRoute("movie", ["movie", "tv"]);

const accountsStore = useAccountsStore();

const mediaType = computed<TGMediaType>(() => (activeTab.value === "tv" ? "tv" : "movie"));

const TABS = [
  { key: "movie", label: "电影" },
  { key: "tv", label: "剧集" },
];

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

const botHint = computed(() => {
  const status = stats.value?.status;
  if (!status) return null;
  if (!status.token_set) {
    return { tone: "warning" as const, text: "还没有配置 Bot Token，点右上角齿轮去设置。" };
  }
  if (status.status === "error") {
    return { tone: "danger" as const, text: `TG 连接异常：${status.status_message || "请检查代理与 Token"}` };
  }
  if (status.connected) {
    return { tone: "success" as const, text: `Bot ${status.bot_name || ""} 已连接`.trim() };
  }
  return { tone: "muted" as const, text: "TG 订阅未启用或尚未开始轮询。" };
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

// 切换 电影 / 剧集 时清掉搜索词 —— 上一个是关键词的结果留在另一个 tab 里很突兀。
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
          <div style="width: 240px">
            <AppInput
              v-model="keywordInput"
              placeholder="搜索影片，回车或稍候自动搜索"
              @input="onSearchInput"
              @keyup.enter="keyword = keywordInput"
            />
          </div>
          <AppButton v-if="keyword" type="button" variant="ghost" size="sm" @click="clearSearch">
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

    <div v-if="botHint" style="display: flex; align-items: center; gap: 10px">
      <AdminStatusPill :tone="botHint.tone">{{ botHint.text }}</AdminStatusPill>
      <span v-if="subscriptionCount" class="tg-detail__sub">
        正在追更 {{ subscriptionCount }} 部 · 共 {{ subscriptions.length }} 条订阅
      </span>
    </div>

    <TGDiscoverWall
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
