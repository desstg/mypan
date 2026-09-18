<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import TGTitleDetailModal from "@/components/admin/TGTitleDetailModal.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  batchSetTGSubscriptionStatus,
  deleteTGSubscription,
  fetchTGSubscriptions,
  tgPosterURL,
} from "@/api/tgSubscribe";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import type { TGSubscription, TGSubscriptionStatus } from "@/types/tg-subscribe";
import {
  TG_KIND_FILTERS,
  TG_STATUS_FILTERS,
  tgStatusLabel,
  type TGSubscribeKindFilter,
  type TGSubscribeStatusFilter,
} from "@/types/tg-subscribe";

// 「已订阅」：把订阅过的影片按海报卡片摊开，支持按类型/状态筛选与批量改状态。
//
// 与「电影 / 剧集」两张海报墙的区别在于**数据来源**：那两张打的是 TMDB 的
// 发现/搜索接口，这一张读的是本地订阅表 —— 所以它不分页，也不需要分页
// （订阅总量是「几十条」量级），筛选全在本地做。

const emit = defineEmits<{ changed: [] }>();

const loading = ref(false);
const subscriptions = ref<TGSubscription[]>([]);

/** 勾选中的订阅 id。用 Set 而不是数组：卡片多起来后逐个 includes 会退化成平方。 */
const selectedIds = ref<Set<number>>(new Set());
const batchSaving = ref(false);

const detailOpen = ref(false);
const detailSubscription = ref<TGSubscription | null>(null);

const kind = ref<TGSubscribeKindFilter>("all");
const status = ref<TGSubscribeStatusFilter>("all");

const KIND_OPTIONS = TG_KIND_FILTERS;
const STATUS_OPTIONS = TG_STATUS_FILTERS;

/**
 * 筛选后的订阅。
 *
 * 「番号」是**刻意留的空档**：后端 domain.TGMediaType 只有 movie / tv，
 * 订阅表里不会出现别的值 —— 所以这一档筛出来必定是空的，由空状态去解释原因，
 * 而不是靠前端假装能显示点什么。
 */
const visible = computed(() =>
  subscriptions.value.filter((sub) => {
    if (kind.value === "jav") return false;
    if (kind.value !== "all" && sub.media_type !== kind.value) return false;
    if (status.value !== "all" && sub.status !== status.value) return false;
    return true;
  }),
);

/**
 * 批量操作的作用范围：**当前筛选结果里的勾选**。
 *
 * 换过筛选之后，看不见的那些不该还留在名单里 —— 「我明明只选了这几张」
 * 是批量操作最常见的翻车方式，所以这里每次都用 visible 过滤一遍。
 */
const batchIds = computed(() =>
  visible.value.filter((sub) => selectedIds.value.has(sub.id)).map((sub) => sub.id),
);
const batchCount = computed(() => batchIds.value.length);
const allSelected = computed(
  () => visible.value.length > 0 && batchCount.value === visible.value.length,
);

const BATCH_ACTIONS: { key: TGSubscriptionStatus; label: string }[] = [
  { key: "active", label: "订阅中" },
  { key: "paused", label: "暂停订阅" },
  { key: "completed", label: "完成订阅" },
];

async function load() {
  loading.value = true;
  try {
    subscriptions.value = await fetchTGSubscriptions();
    // 订阅可能已经在别处被删掉了，勾选集合要跟着收口。
    const alive = new Set(subscriptions.value.map((s) => s.id));
    const next = new Set([...selectedIds.value].filter((id) => alive.has(id)));
    if (next.size !== selectedIds.value.size) selectedIds.value = next;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载订阅列表失败"));
  } finally {
    loading.value = false;
  }
}

function toggleSelect(id: number) {
  // Set 的增删不是响应式的，必须整个换掉才会触发重算。
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}

function toggleSelectAll() {
  selectedIds.value = allSelected.value ? new Set() : new Set(visible.value.map((s) => s.id));
}

function titleOf(sub: TGSubscription) {
  return sub.title || sub.original_title || "（无标题）";
}

/**
 * 海报左上角的类型角标。
 *
 * 番号返回空串 —— 那个类型后端还没有，界面上也就没有它该显示的名字；
 * 硬塞一个「番号」进去只会让人以为真能订阅番号。
 */
function typeLabelOf(sub: TGSubscription) {
  switch (sub.media_type) {
    case "movie":
      return "影片";
    case "tv":
      return "剧集";
    default:
      return "";
  }
}

/** 剧集用「已收集 / 已播出」，电影没有集数概念就不占位置。 */
function progressOf(sub: TGSubscription) {
  if (sub.media_type !== "tv") return "";
  if (!sub.aired_episodes && !sub.collected_episodes) return "";
  return `${sub.collected_episodes} / ${sub.aired_episodes || "?"} 集`;
}

function statusToneOf(sub: TGSubscription) {
  switch (sub.status) {
    case "active":
      return "success" as const;
    case "paused":
      return "warning" as const;
    default:
      return "muted" as const;
  }
}

function openDetail(sub: TGSubscription) {
  detailSubscription.value = sub;
  detailOpen.value = true;
}

async function onDetailChanged() {
  await load();
  emit("changed");
}

async function batchChangeStatus(next: TGSubscriptionStatus) {
  const ids = batchIds.value;
  if (!ids.length) return;
  const label = tgStatusLabel(next);
  try {
    await confirm({
      title: "批量修改订阅状态",
      message: `把选中的 ${ids.length} 条订阅都改成「${label}」？`,
      confirmText: "修改",
      danger: false,
      icon: "warning",
    });
  } catch {
    return;
  }
  batchSaving.value = true;
  try {
    const { updated } = await batchSetTGSubscriptionStatus(ids, next);
    toast.success(`已把 ${updated} 条订阅改为「${label}」`);
    selectedIds.value = new Set();
    await load();
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "批量修改状态失败"));
  } finally {
    batchSaving.value = false;
  }
}

async function remove(sub: TGSubscription) {
  try {
    await confirm({
      title: "取消订阅",
      message: `确认取消《${titleOf(sub)}》的订阅？已下载到网盘的文件会保留。`,
      confirmText: "取消订阅",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    await deleteTGSubscription(sub.id);
    toast.success("已取消订阅");
    await load();
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "取消失败"));
  }
}

onMounted(load);

defineExpose({ load });
</script>

<template>
  <div class="tg-wall">
    <!-- 筛选与批量操作同处一行：批量改的是「筛选结果里的勾选」，
         两者隔着几屏的话，用户点完不知道到底动了哪些。 -->
    <div class="tg-subscribed__bar">
      <div class="tg-filters__field">
        <label class="tg-filters__label">类型</label>
        <AppSelect v-model="kind" :options="KIND_OPTIONS" />
      </div>
      <div class="tg-filters__field">
        <label class="tg-filters__label">状态</label>
        <AppSelect v-model="status" :options="STATUS_OPTIONS" />
      </div>
      <div class="tg-subscribed__bar-actions">
        <AppButton v-if="visible.length" type="button" variant="ghost" size="sm" @click="toggleSelectAll">
          {{ allSelected ? "取消全选" : "全选" }}
        </AppButton>
        <AppDropdown align="right" :items="[]" :min-width="170">
          <template #trigger="{ toggle }">
            <AppButton
              type="button"
              variant="secondary"
              size="sm"
              :disabled="!batchCount || batchSaving"
              @click="toggle"
            >
              {{ batchSaving ? "修改中…" : batchCount ? `批量修改状态（${batchCount}）` : "批量修改状态" }}
            </AppButton>
          </template>
          <template #panel="{ close }">
            <button
              v-for="action in BATCH_ACTIONS"
              :key="action.key"
              type="button"
              class="tg-subscribed__batch-item"
              @click="close(); batchChangeStatus(action.key)"
            >
              {{ action.label }}
            </button>
          </template>
        </AppDropdown>
      </div>
    </div>

    <p v-if="batchCount" class="tg-form__hint" style="margin-top: 0">
      已选中 {{ batchCount }} 条 —— <b>只作用于当前筛选结果里被勾选的那些</b>，
      换筛选条件后看不见的订阅不受影响。
    </p>

    <div v-if="visible.length" class="tg-grid">
      <div v-for="sub in visible" :key="sub.id" class="tg-card">
        <div class="tg-card__poster" :class="{ 'tg-card__poster--selected': selectedIds.has(sub.id) }">
          <button
            type="button"
            class="tg-card__open"
            :title="`《${titleOf(sub)}》· 点开订阅详情`"
            @click="openDetail(sub)"
          >
            <img
              v-if="sub.poster_path"
              class="tg-card__image"
              :src="tgPosterURL(sub.poster_path, 'w300')"
              :alt="titleOf(sub)"
              loading="lazy"
            />
            <div v-else class="tg-card__image tg-card__image--empty">🎬</div>
          </button>

          <!-- 海报四角各放一个元素：左上类型、右上勾选、左下状态、右下悬停操作。
               番号目前没有订阅，也就不会有角标 —— 真出现了也不硬塞一个名字进去。 -->
          <span v-if="typeLabelOf(sub)" class="tg-card__type">{{ typeLabelOf(sub) }}</span>

          <!-- 未选中时 hover 才浮出，选中后常驻（见 tg-subscribe.css）。
               用 SVG 而不是 ✓/○ 字符：这个字号下字体画的圆圈笔画粗细不匀，看着像没画圆。 -->
          <span
            class="tg-card__check"
            :class="{ 'tg-card__check--on': selectedIds.has(sub.id) }"
            role="checkbox"
            :aria-checked="selectedIds.has(sub.id)"
            :aria-label="`选择《${titleOf(sub)}》`"
            tabindex="0"
            @click.stop="toggleSelect(sub.id)"
            @keydown.enter.stop.prevent="toggleSelect(sub.id)"
            @keydown.space.stop.prevent="toggleSelect(sub.id)"
          >
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <circle cx="8" cy="8" r="6.4" fill="none" stroke="currentColor" stroke-width="1.7" />
              <path
                v-if="selectedIds.has(sub.id)"
                d="M4.7 8.2l2.2 2.2 4.4-4.6"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </span>

          <span class="tg-card__status">
            <AdminStatusPill :tone="statusToneOf(sub)">{{ tgStatusLabel(sub.status) }}</AdminStatusPill>
          </span>

          <!-- hover 才浮出。占右下角，左下角留给状态胶囊；卡片最窄只有 150px，
               两个按钮加左下角那颗胶囊会蹭到一起，所以悬停时让胶囊先淡出
               （见 tg-subscribe.css）——扫视时看状态、动手时看操作。 -->
          <div class="tg-card__hover-actions">
            <button type="button" class="tg-subscribed__hover-btn" title="订阅详情" @click="openDetail(sub)">
              详情
            </button>
            <button
              type="button"
              class="tg-subscribed__hover-btn tg-subscribed__hover-btn--danger"
              title="取消订阅"
              @click="remove(sub)"
            >
              取消
            </button>
          </div>
        </div>

        <div class="tg-card__meta">
          <button
            type="button"
            class="tg-card__title tg-card__title--link"
            :title="titleOf(sub)"
            @click="openDetail(sub)"
          >
            {{ titleOf(sub) }}
          </button>
          <span class="tg-card__sub">
            <span v-if="sub.year">{{ sub.year }}</span>
            <span v-if="progressOf(sub)">{{ progressOf(sub) }}</span>
          </span>
        </div>
      </div>
    </div>

    <AdminEmptyState
      v-else-if="!loading"
      icon="📌"
      :title="kind === 'jav' ? '番号订阅还没开放' : '没有符合条件的订阅'"
      :description="
        kind === 'jav'
          ? '这一档只是先占个位置 —— 番号订阅还没有实现，所以这里永远是空的。'
          : subscriptions.length
            ? '换个类型或状态看看。'
            : '到「电影 / 剧集」里点海报右上角的「+」就能订阅。'
      "
    />

    <div v-else class="tg-wall__footer">加载中…</div>

    <TGTitleDetailModal
      :open="detailOpen"
      :item="null"
      :subscription="detailSubscription"
      @close="detailOpen = false"
      @changed="onDetailChanged"
    />
  </div>
</template>
