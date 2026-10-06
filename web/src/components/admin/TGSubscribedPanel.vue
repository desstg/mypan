<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppPagination from "@/components/base/AppPagination.vue";
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
import { useGridPageSize } from "@/composables/useGridColumns";
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
// 发现/搜索接口，这一张读的是本地订阅表 —— 所以筛选全在本地做，
// 分页也是本地的（不必再打后端）。
//
// 卡片尺寸与「电影 / 剧集」共用一套 `.tg-grid`（见 tg-subscribe.css），
// 每页行数也一致，来回切 tab 不会觉得换了个页面。

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

/** 每页行数，与发现墙（TGDiscoverWall.PAGE_ROWS）保持一致。 */
const PAGE_ROWS = 5;

const gridRef = ref<HTMLElement | null>(null);
const { pageSize, measure } = useGridPageSize(gridRef, PAGE_ROWS);
const page = ref(1);

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

const totalPages = computed(() => Math.max(1, Math.ceil(visible.value.length / pageSize.value)));

/**
 * 本页渲染的订阅。
 *
 * 每页条数 = 实测列数 × PAGE_ROWS —— 只有是列数的整数倍，最后一排才不会空出格子。
 * 第一帧还没量到列数时 pageSize 会偏小（cols 回落成 1），所以这里必须对越界兜底，
 * 否则量完之前会渲染出一张空页。
 */
const paged = computed(() => {
  const from = (page.value - 1) * pageSize.value;
  return visible.value.slice(from, from + pageSize.value);
});

/**
 * 批量操作的作用范围：**当前筛选结果里的勾选**。
 *
 * 换过筛选之后，看不见的那些不该还留在名单里 —— 「我明明只选了这几张」
 * 是批量操作最常见的翻车方式，所以这里每次都用 visible 过滤一遍。
 * 翻页不算「换筛选」：勾选是跨页保留的，见 toggleSelectAll 的说明。
 */
const batchIds = computed(() =>
  visible.value.filter((sub) => selectedIds.value.has(sub.id)).map((sub) => sub.id),
);
const batchCount = computed(() => batchIds.value.length);
/** 全选只作用于**本页** —— 有分页之后，一个按钮勾上 43 条而屏幕里只看得见 35 条会让人心里没底。 */
const allSelected = computed(
  () => paged.value.length > 0 && paged.value.every((sub) => selectedIds.value.has(sub.id)),
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
    // 列数是渲染后才量得到的，所以「每页几条」在第一帧之后才稳定；
    // 等 DOM 落定再量一次并收口页码，否则订阅被删光时会停在一个空页上。
    await nextTick();
    measure();
    if (page.value > totalPages.value) page.value = totalPages.value;
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

/** 全选 / 取消全选**只作用于本页**：跨页全选在屏幕上没有可核对的反馈。 */
function toggleSelectAll() {
  const next = new Set(selectedIds.value);
  if (allSelected.value) {
    for (const sub of paged.value) next.delete(sub.id);
  } else {
    for (const sub of paged.value) next.add(sub.id);
  }
  selectedIds.value = next;
}

// 换筛选条件时回到第 1 页 —— 停在第 5 页却只剩 2 页结果，用户看到的是空屏。
watch([kind, status], () => {
  page.value = 1;
});
// 窗口变宽/变窄会改列数，进而改每页条数；页码不重算就会指到不存在的页。
watch(pageSize, () => {
  if (page.value > totalPages.value) page.value = totalPages.value;
});

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

// 分页器在列表下方，翻页后不把视口带回顶部的话，看到的是新一页的末尾，
// 会以为页码没生效。（与「电影 / 剧集」那面墙同一套处理。）
function goPage(next: number) {
  page.value = next;
  gridRef.value?.scrollIntoView({ behavior: "smooth", block: "start" });
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

    <div v-if="paged.length" ref="gridRef" class="tg-grid">
      <div v-for="sub in paged" :key="sub.id" class="tg-card">
        <div class="tg-card__poster" :class="{ 'tg-card__poster--selected': selectedIds.has(sub.id) }">
          <button
            type="button"
            class="tg-card__open"
            :title="`《${titleOf(sub)}》· 点开订阅详情`"
            @click="openDetail(sub)"
          >
            <MediaImage
              class="tg-card__image"
              :src="tgPosterURL(sub.poster_path, 'w300')"
              :alt="titleOf(sub)"
            />
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

    <!-- 本地分页：每页条数是「实测列数 × 行数」，最后一排永远是满的。
         只有最后一页剩下的尾数会空 —— 那是列表的末尾，不是断行。 -->
    <AppPagination :page="page" :total-pages="totalPages" @update:page="goPage" />

    <TGTitleDetailModal
      :open="detailOpen"
      :item="null"
      :subscription="detailSubscription"
      @close="detailOpen = false"
      @changed="onDetailChanged"
    />
  </div>
</template>
