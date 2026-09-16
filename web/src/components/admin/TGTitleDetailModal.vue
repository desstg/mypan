<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import TGMatchHistoryPanel from "@/components/admin/TGMatchHistoryPanel.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  createTGSubscription,
  deleteTGSubscription,
  fetchTGSubscriptionByTMDB,
  fetchTGSubscriptionEpisodes,
  fetchTGQualityProfiles,
  resetTGSubscription,
  setTGSubscriptionStatus,
  tgPosterURL,
  updateTGSubscription,
  type TGSubscriptionInput,
} from "@/api/tgSubscribe";
import { useAccountsStore } from "@/stores/accounts";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import type {
  TGMediaType,
  TGQualityProfile,
  TGSubscription,
  TGSubscriptionEpisode,
  TGTMDBSearchResult,
} from "@/types/tg-subscribe";

const props = defineProps<{
  open: boolean;
  item: TGTMDBSearchResult | null;
  mediaType: TGMediaType;
}>();

const emit = defineEmits<{ close: []; changed: [] }>();

const accountsStore = useAccountsStore();

const loading = ref(false);
const saving = ref(false);
const profiles = ref<TGQualityProfile[]>([]);
const subscription = ref<TGSubscription | null>(null);
const episodes = ref<TGSubscriptionEpisode[]>([]);
const pickerOpen = ref(false);

const form = reactive<TGSubscriptionInput>({
  tmdb_id: "",
  media_type: "movie",
  title: "",
  original_title: "",
  year: 0,
  poster_path: "",
  overview: "",
  quality_profile_id: 0,
  target_account_id: 0,
  target_parent_id: "",
  target_display_path: "",
  push_provider: "auto",
  collect_window_min: 5,
  upgrade_enabled: true,
});

const title = computed(() => form.title || form.original_title || "（无标题）");
const subscribed = computed(() => subscription.value !== null);

const profileOptions = computed(() => [
  { value: 0, label: "跟随默认方案" },
  ...profiles.value.map((p) => ({
    value: p.id,
    label: p.is_default ? `${p.name}（默认）` : p.name,
  })),
]);

const targetText = computed(() => {
  if (!form.target_account_id) return "";
  const account = accountsStore.accounts.find((a) => a.id === form.target_account_id);
  return `${account?.name ?? `账号 ${form.target_account_id}`} · ${form.target_display_path || "/"}`;
});

const progressText = computed(() => {
  const sub = subscription.value;
  if (!sub || sub.media_type !== "tv") return "";
  if (!sub.aired_episodes && !sub.collected_episodes) return "";
  return `已收集 ${sub.collected_episodes} / ${sub.aired_episodes || "?"} 集`;
});

const progressPercent = computed(() => {
  const sub = subscription.value;
  if (!sub || !sub.aired_episodes) return 0;
  return Math.min(100, Math.round((sub.collected_episodes / sub.aired_episodes) * 100));
});

const statusTone = computed(() => {
  switch (subscription.value?.status) {
    case "active":
      return "success" as const;
    case "paused":
      return "warning" as const;
    case "completed":
      return "muted" as const;
    default:
      return "muted" as const;
  }
});

const statusLabel = computed(() => {
  switch (subscription.value?.status) {
    case "active":
      return "订阅中";
    case "paused":
      return "已暂停";
    case "completed":
      return "已完成";
    default:
      return "";
  }
});

/**
 * 剧集的集数进度明细：按季列出每一集，已入库的标绿、还缺的留在虚线上。
 *
 * 「缺哪一集」是追更时最常问的问题，所以这里要把没收集到的也画出来 ——
 * 只显示已有集数的话，用户还得自己数。
 */
const episodeGrid = computed(() => {
  const sub = subscription.value;
  if (!sub || sub.media_type !== "tv" || !sub.seasons?.length) return [];

  const collected = new Set(episodes.value.map((e) => `${e.season}:${e.episode}`));
  const now = Date.now();

  return sub.seasons
    // 特别篇（第 0 季）不参与追更进度，和完成判定里的口径保持一致。
    .filter((season) => season.season_number > 0 && season.episode_count > 0)
    .map((season) => {
      const aired =
        !season.air_date || new Date(season.air_date).getTime() <= now
          ? season.episode_count
          : 0;
      const items = Array.from({ length: season.episode_count }, (_, i) => {
        const episode = i + 1;
        return {
          episode,
          collected: collected.has(`${season.season_number}:${episode}`),
          aired: episode <= aired,
        };
      });
      const owned = items.filter((it) => it.collected).length;
      return { seasonNumber: season.season_number, items, owned };
    });
});

/** 弹窗打开时重置并加载。 */
watch(
  () => [props.open, props.item] as const,
  async ([open, item]) => {
    if (!open || !item) return;
    form.tmdb_id = String(item.id);
    form.media_type = props.mediaType;
    form.title = item.title || item.name || "";
    form.original_title = item.original_title || item.original_name || "";
    form.year = Number((item.release_date || item.first_air_date || "").slice(0, 4)) || 0;
    form.poster_path = item.poster_path || "";
    form.overview = item.overview || "";
    form.quality_profile_id = 0;
    form.target_account_id = 0;
    form.target_parent_id = "";
    form.target_display_path = "";
    form.push_provider = "auto";
    form.collect_window_min = 5;
    form.upgrade_enabled = true;

    loading.value = true;
    episodes.value = [];
    try {
      const [subs, list] = await Promise.all([
        fetchTGSubscriptionByTMDB(item.id, props.mediaType),
        profiles.value.length ? Promise.resolve(profiles.value) : fetchTGQualityProfiles(),
      ]);
      profiles.value = list;
      subscription.value = subs;
      // 已订阅时用真实配置覆盖表单，方便「已订阅」形态直接改。
      if (subs) {
        form.quality_profile_id = subs.quality_profile_id;
        form.target_account_id = subs.target_account_id;
        form.target_parent_id = subs.target_parent_id;
        form.target_display_path = subs.target_display_path;
        form.push_provider = subs.push_provider;
        form.collect_window_min = subs.collect_window_min;
        form.upgrade_enabled = subs.upgrade_enabled;
        if (subs.media_type === "tv") {
          await loadEpisodes(subs.id);
        }
      }
    } catch (error) {
      toast.error(getApiErrorMessage(error, "加载订阅状态失败"));
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);

/** 拉取已入库的集数明细。失败只影响进度明细，不打断整个弹窗。 */
async function loadEpisodes(subscriptionId: number) {
  try {
    episodes.value = await fetchTGSubscriptionEpisodes(subscriptionId);
  } catch {
    episodes.value = [];
  }
}

function onTargetPicked(payload: { accountId: number; parentId: string; path: string }) {
  form.target_account_id = payload.accountId;
  form.target_parent_id = payload.parentId;
  form.target_display_path = payload.path || "/";
  pickerOpen.value = false;
}

async function subscribe() {
  saving.value = true;
  try {
    subscription.value = await createTGSubscription({ ...form });
    toast.success("订阅成功，正在为你追更");
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "订阅失败"));
  } finally {
    saving.value = false;
  }
}

async function saveSettings() {
  if (!subscription.value) return;
  saving.value = true;
  try {
    subscription.value = await updateTGSubscription(subscription.value.id, { ...form });
    toast.success("订阅设置已保存");
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function changeStatus(status: "active" | "paused" | "completed") {
  if (!subscription.value) return;
  try {
    await setTGSubscriptionStatus(subscription.value.id, status);
    toast.success(status === "paused" ? "已暂停追更" : status === "completed" ? "已标记完成" : "已恢复追更");
    subscription.value = await fetchTGSubscriptionByTMDB(form.tmdb_id, props.mediaType);
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "操作失败"));
  }
}

async function resetProgress() {
  if (!subscription.value) return;
  try {
    await confirm({
      title: "重置订阅进度",
      message: "会清掉已收集的集数记录，让这部片重新开始追。已经下载到网盘的文件不会删除。",
      confirmText: "重置",
      danger: true,
      icon: "warning",
    });
  } catch {
    return;
  }
  try {
    const { removed } = await resetTGSubscription(subscription.value.id);
    toast.success(`已重置，清除了 ${removed} 条进度记录`);
    subscription.value = await fetchTGSubscriptionByTMDB(form.tmdb_id, props.mediaType);
    episodes.value = [];
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "重置失败"));
  }
}

async function unsubscribe() {
  if (!subscription.value) return;
  try {
    await confirm({
      title: "取消订阅",
      message: `确认取消《${title.value}》的订阅？已下载到网盘的文件会保留。`,
      confirmText: "取消订阅",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    await deleteTGSubscription(subscription.value.id);
    toast.success("已取消订阅");
    subscription.value = null;
    emit("changed");
    emit("close");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "取消失败"));
  }
}
</script>

<template>
  <AppModal
    :open="open"
    :title="subscribed ? '订阅详情' : '订阅影片'"
    size="lg"
    body-flush
    @close="emit('close')"
  >
    <div class="tg-detail" style="padding: 0 24px 8px">
      <div class="tg-detail__head">
        <div class="tg-detail__poster">
          <img v-if="form.poster_path" :src="tgPosterURL(form.poster_path, 'w300')" :alt="title" />
        </div>
        <div class="tg-detail__head-main">
          <h3 class="tg-detail__title">{{ title }}</h3>
          <div class="tg-detail__sub">
            <template v-if="form.original_title && form.original_title !== title">
              {{ form.original_title }}
            </template>
          </div>
          <div class="tg-detail__badges">
            <AdminStatusPill tone="brand">{{ mediaType === "movie" ? "电影" : "剧集" }}</AdminStatusPill>
            <AdminStatusPill v-if="form.year" tone="muted">{{ form.year }}</AdminStatusPill>
            <AdminStatusPill v-if="item?.vote_average" tone="warning">
              ★ {{ item.vote_average.toFixed(1) }}
            </AdminStatusPill>
            <AdminStatusPill v-if="subscribed" :tone="statusTone">{{ statusLabel }}</AdminStatusPill>
          </div>
          <div v-if="progressText" class="tg-detail__progress">
            <span class="tg-detail__sub">{{ progressText }}</span>
            <div class="tg-progress">
              <div class="tg-progress__bar" :style="{ width: `${progressPercent}%` }" />
            </div>
          </div>
        </div>
      </div>

      <p v-if="form.overview" class="tg-detail__overview">{{ form.overview }}</p>

      <div v-if="episodeGrid.length" class="tg-episode-grid">
        <div v-for="season in episodeGrid" :key="season.seasonNumber" class="tg-episode-season">
          <div class="tg-episode-season__head">
            <span class="tg-form__label">第 {{ season.seasonNumber }} 季</span>
            <span class="tg-detail__sub">
              {{ season.owned }} / {{ season.items.length }} 集
            </span>
          </div>
          <div class="tg-episodes">
            <span
              v-for="ep in season.items"
              :key="ep.episode"
              class="tg-episode"
              :class="{
                'tg-episode--missing': !ep.collected && ep.aired,
                'tg-episode--future': !ep.aired,
              }"
              :title="ep.collected ? '已入库' : ep.aired ? '还没找到' : '尚未播出'"
            >
              {{ ep.episode }}
            </span>
          </div>
        </div>
      </div>

      <div v-if="subscribed && subscription?.aliases?.length" class="tg-detail__aliases">
        <span
          v-for="alias in subscription.aliases.slice(0, 16)"
          :key="alias"
          class="tg-detail__alias"
        >
          {{ alias }}
        </span>
      </div>

      <div class="tg-form">
        <div class="tg-form__field">
          <label class="tg-form__label">目标目录</label>
          <div class="tg-form__value">
            <AccountFolderField
              :display="targetText"
              title="选择推送到的网盘目录"
              placeholder="留空则用订阅设置里的默认目标"
              @browse="pickerOpen = true"
            />
            <p class="tg-form__hint">
              推送时会在这个目录下自动建「片名 (年份)」子目录，同一部的不同版本不会散开。
            </p>
          </div>
        </div>

        <div class="tg-form__field">
          <label class="tg-form__label">画质方案</label>
          <div class="tg-form__value">
            <AppSelect v-model="form.quality_profile_id" :options="profileOptions" />
          </div>
        </div>

        <div class="tg-form__field">
          <label class="tg-form__label">推送方式</label>
          <div class="tg-form__value">
            <SettingsSegment
              v-model="form.push_provider"
              label="推送方式"
              :options="[
                { value: 'auto', label: '自动' },
                { value: 'native', label: '网盘离线' },
                { value: 'builtin', label: '内置下载' },
              ]"
            />
            <p class="tg-form__hint">
              「自动」会探测网盘能力：支持磁力就交给网盘离线下载，不支持就自动走内置下载器。
            </p>
          </div>
        </div>

        <div class="tg-form__field">
          <label class="tg-form__label">聚合窗口</label>
          <div class="tg-form__value" style="max-width: 140px">
            <AppInput v-model.number="form.collect_window_min" type="number" placeholder="5" />
            <p class="tg-form__hint">单位分钟。命中后先等这么久，把窗口内的多个版本一起比较再推最优的。</p>
          </div>
        </div>

        <div class="tg-form__field">
          <label class="tg-form__label">洗版</label>
          <div class="tg-form__value">
            <SettingsBoolSegment v-model="form.upgrade_enabled" label="开启洗版" />
            <p class="tg-form__hint">
              开启后，已经推过低画质版本时，后续出现明显更好的版本会再推一次（标记为「洗版升级」）。
              旧版本不会被自动删除。
            </p>
          </div>
        </div>
      </div>

      <TGMatchHistoryPanel v-if="subscribed && subscription" compact :subscription-id="subscription.id" />
    </div>

    <template #footer>
      <template v-if="subscribed">
        <AppButton type="button" variant="ghost" @click="unsubscribe">取消订阅</AppButton>
        <AppButton type="button" variant="ghost" @click="resetProgress">重置进度</AppButton>
        <AppButton
          v-if="subscription?.status !== 'paused'"
          type="button"
          variant="secondary"
          @click="changeStatus('paused')"
        >
          暂停
        </AppButton>
        <AppButton
          v-else
          type="button"
          variant="secondary"
          @click="changeStatus('active')"
        >
          恢复
        </AppButton>
        <AppButton
          v-if="subscription?.status !== 'completed'"
          type="button"
          variant="secondary"
          @click="changeStatus('completed')"
        >
          标记完成
        </AppButton>
        <AppButton type="button" variant="primary" :disabled="saving" @click="saveSettings">
          {{ saving ? "保存中…" : "保存设置" }}
        </AppButton>
      </template>
      <template v-else>
        <AppButton type="button" variant="secondary" @click="emit('close')">取消</AppButton>
        <AppButton type="button" variant="primary" :disabled="saving || loading" @click="subscribe">
          {{ saving ? "订阅中…" : "开始订阅" }}
        </AppButton>
      </template>
    </template>

    <FolderPickerModal
      :open="pickerOpen"
      title="选择推送目录"
      confirm-text="用这个目录"
      nested
      selectable-account
      :accounts="accountsStore.accounts"
      :account-id="form.target_account_id || null"
      allow-create-folder
      @close="pickerOpen = false"
      @resolve="onTargetPicked"
    />
  </AppModal>
</template>
