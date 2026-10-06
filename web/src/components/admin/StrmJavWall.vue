<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavWallItems,
  rebuildJavWallItem,
  refreshJavWall,
  type JavWallItem,
  type JavWallListResult,
} from "@/api/strmJavWall";
import AppButton from "@/components/base/AppButton.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import StrmJavMetaDrawer from "@/components/admin/StrmJavMetaDrawer.vue";
import { findScrollBox, useFillViewport } from "@/composables/useFillViewport";
import { toast } from "@/composables/useToast";
import "@/styles/jav.css";

// 番号影片的海报墙（辅助工具 → 海报墙，任务媒体类型 = 番号影片时由 StrmScrapePanel 挂上来）。
//
// 与 TMDB 那面墙的区别都来自用户的要求：
//   * 「类型」不是电影/剧集，而是**磁盘上实际存在的一级目录**（有码/无码/欧美/国产…）；
//     哪些目录整档不显示由用户自己勾（页头「全部目录」按钮 / STRM 设置里那一行），
//     「未匹配」默认已勾 —— 它取的是分类规则里那条兜底规则的目标目录，改名会自动跟上。
//   * 多分片目录（`91CM-109-cd1/cd2/cd3`）**每个分片一张卡**，三张都显示 cd1 的图。
//   * 缩略图 / 海报图 两种视图切换（纯前端，两个 URL 都在数据里）。
//   * 鼠标指过去出「编辑」「重刮」。

// 搜索与排序由**页头**统一控制（用户要求：番号墙自己的那两个框不要了）。
// 子组件只按这三个 prop 请求 —— 页头那套按钮对两种任务长得一模一样，功能各是各的。
import { useWallPlayback } from "@/composables/useWallPlayback";
import WallPlayer from "@/components/admin/WallPlayer.vue";

const props = defineProps<{
  taskId: number | null;
  keyword?: string;
  sort?: string;
}>();

/** 隐藏名单变了（或拿到新的一份）时把名单抛给外面：页头那颗按钮要显示「隐藏 N 个目录」。 */
const emit = defineEmits<{
  "hidden-dirs": [string[]];
}>();

const loading = ref(false);
const refreshing = ref(false);
const result = ref<JavWallListResult | null>(null);
const category = ref<string>("");
const view = ref<"thumb" | "poster">("poster");
const busyStem = ref<string>("");

// ————————————————————— 分页 —————————————————————
//
// 后端 `listJavWall` 本来就有 offset / limit / has_more，这里原来只调一次、写死
// limit=200、不带 offset —— 于是**超过 200 张的部分永远看不到，界面上还没有任何提示**
// （属于「静默变空」那一族）。现在改成拉到底续加载。
//
// 每页 200 = 后端上限（`maxItemListLimit`）。首次就把上限取满，是为了让手机竖屏
// 也一次备够十屏，滚动时几乎碰不到「正在加载」。
const PAGE_LIMIT = 200;
const loadingMore = ref(false);
/** 还有没有下一页 —— 来自后端，不靠「这一页拿满了没」去猜。 */
const hasMore = ref(false);
/** 过滤后的总条数（后端报的），底部状态行要显示「还有 N 条」。 */
const total = ref(0);

const drawerOpen = ref(false);
const editing = ref<JavWallItem | null>(null);

const items = computed(() => result.value?.items ?? []);
const categories = computed(() => result.value?.categories ?? []);

/** 底部状态行该不该占位置。首屏还在转圈时不显示，免得底部先冒一条文案又跳走。 */
const showLoadMore = computed(() => !loading.value && items.value.length > 0);

/** 把隐藏名单抛给外面（页头那颗「全部目录」按钮要显示「隐藏 N 个目录」）。 */
function publishHiddenDirs() {
  emit("hidden-dirs", result.value?.hidden_dirs ?? []);
}

/**
 * 取一页。`append` 为真时接在现有列表后面（拉到底续加载）。
 *
 * ⚠️ 追加时**不能**把 `loading` 置真：模板里 `v-if="loading"` 会把整片网格换成
 * 「加载中…」，用户每滚一屏就看到列表闪一下没了。所以另开 `loadingMore`。
 */
async function load(options: { append?: boolean } = {}) {
  if (!props.taskId) {
    result.value = null;
    hasMore.value = false;
    total.value = 0;
    publishHiddenDirs();
    return;
  }
  const append = Boolean(options.append);
  if (append) {
    if (loading.value || loadingMore.value || !hasMore.value) return;
    loadingMore.value = true;
  } else {
    loading.value = true;
  }
  try {
    const next = await fetchJavWallItems(props.taskId, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "added_desc",
      limit: PAGE_LIMIT,
      offset: append ? (result.value?.items.length ?? 0) : 0,
    });
    if (append && result.value) {
      // 按 id 去重再拼：快照 TTL 是 60 秒，跨过一次重扫顺序可能挪位，
      // 重复的 key 会让 Vue 报「Duplicate keys」并且渲染错位。
      const seen = new Set(result.value.items.map((it) => it.id));
      result.value.items = [...result.value.items, ...next.items.filter((it) => !seen.has(it.id))];
      result.value.has_more = next.has_more;
      result.value.total = next.total;
    } else {
      result.value = next;
    }
    hasMore.value = Boolean(result.value?.has_more);
    total.value = result.value?.total ?? 0;
    publishHiddenDirs();
  } catch (error) {
    // 追加失败保持已有内容 —— 滚到一半一次请求失败，不该把看过的一屏清空。
    if (!append) {
      toast.error(getApiErrorMessage(error, "读取番号海报墙失败"));
      result.value = null;
      hasMore.value = false;
      total.value = 0;
      publishHiddenDirs();
    }
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
}

/** 「刷新元数据」= 重读本地磁盘并重列（不是重新生成；重新生成是卡片上的「重刮」）。 */
async function refreshMeta() {
  if (!props.taskId) return;
  refreshing.value = true;
  try {
    result.value = await refreshJavWall(props.taskId, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "added_desc",
      limit: PAGE_LIMIT,
    });
    hasMore.value = Boolean(result.value?.has_more);
    total.value = result.value?.total ?? 0;
    publishHiddenDirs();
    toast.success("已重新读取本地文件");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "刷新失败"));
  } finally {
    refreshing.value = false;
  }
}

function openEditor(item: JavWallItem) {
  editing.value = item;
  drawerOpen.value = true;
}

/** 保存之后只换这一张卡（不整表重列，免得滚动位置乱跳）。 */
function replaceItem(updated: JavWallItem) {
  if (!result.value) return;
  const idx = result.value.items.findIndex((it) => it.id === updated.id);
  if (idx >= 0) result.value.items[idx] = updated;
}

async function rebuild(item: JavWallItem) {
  if (!props.taskId) return;
  busyStem.value = item.stem;
  try {
    const detail = await rebuildJavWallItem(props.taskId, item.rel_dir, item.stem);
    replaceItem(detail.item);
    toast.success("已重刮：" + item.number);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "重刮失败"));
  } finally {
    busyStem.value = "";
  }
}

function subtitle(item: JavWallItem): string {
  const bits: string[] = [item.category || "根目录"];
  if (!item.has_nfo) bits.push("缺 nfo");
  if (!item.has_sidecar) bits.push("无侧车");
  return bits.join(" · ");
}

// ————————————————————— 取图窗口 —————————————————————
//
// 一页最多 200 张，一屏只看得见 30~40 张。**只给看得见的那些发图片请求**：
// `MediaImage` 的原生 `loading="lazy"` 在这里不管用 —— 它的触发距离按视口高度的
// 倍数算，长列表里首屏渲染就会把下面好几屏的图一起排队（实测 50 张全发、2 MB 多、
// 最后一张要几秒）。所以关掉原生懒加载，改由这里按滚动位置发牌。
//
// # 是**滑动窗口**，不是「前 N 张」（2026-10-04 修）
//
// 原来这里算的是一个「一屏 × 3」的**数字**，`imageSrc` 写成 `index >= budget` 就返回空
// —— 那只能表达「前 N 张」，表达不了「当前这一段」。于是滚动时算出来的永远是同一个
// 数，**第 N+1 张往后永远没有图**：群晖上一页 94 张、N 算出来 72，滚到底最后 22 张
// 清一色是占位骨架。用户看到的就是「有些显示、有些不显示」，切 tab / 切视图时数字
// 一变又会好一阵 —— 像极了缓存问题。
//
// 现在按**滚动位置**算出一段区间 `[start, end)`：当前视口那一屏，前后各多给一屏。
// 往前留一屏是为了**往回滚**时图已经在手里。
//
// # 已经发过图的下标**不再撤回**
//
// 窗口滑动时区间会往前移，真按它去撤图，往回滚就会看到图闪一下重新加载。
// 所以另记一个「已经发过图的下标区间」`[loadedMin, loadedMax)`：窗口的并集。
// `imageSrc` 只对**从没进过窗口**的下标返回空串。
//
// ⚠️ 用**两个 ref** 而不是一个 `Set`：Set 的增删不会触发 Vue 重渲染，
// 第一版写成 Set 时整面墙一张图都不出（`imageSrc` 加了新下标但组件没重渲染）。
/** 已经进过窗口的下标区间（并集）。空集用 min=+∞ / max=0 表示。 */
const loadedMin = ref(Number.POSITIVE_INFINITY);
const loadedMax = ref(0);

/** 视口高度：优先用滚动容器自己的（它才是真正被裁切的那个盒子）。 */
function viewportHeight(): number {
  const box = scrollTarget.value;
  return box instanceof HTMLElement ? box.clientHeight : window.innerHeight;
}

/** 量出网格的列数与行高（含间距）。量不到时回落估算（宁可多给，别少给）。 */
function measureGrid(): { cols: number; rowH: number } {
  const cards = gridEl.value?.querySelectorAll<HTMLElement>(".jav-card");
  if (cards && cards.length > 0) {
    const firstTop = cards[0].offsetTop;
    let cols = 0;
    for (const card of cards) {
      if (card.offsetTop !== firstTop) break;
      cols++;
    }
    if (cols > 0) {
      const cardH = cards[0].offsetHeight;
      const secondRow = cards[cols];
      const gap = secondRow ? secondRow.offsetTop - firstTop - cardH : (view.value === "thumb" ? 16 : 14);
      return { cols, rowH: Math.max(1, cardH + Math.max(0, gap)) };
    }
  }
  // 回落：列数按海报视图的 minmax(140px)+gap14 反推，行高按 300 估。
  const cols = Math.max(1, Math.floor((window.innerWidth - 48 + 14) / (140 + 14)));
  return { cols, rowH: 300 };
}

/**
 * 按当前滚动位置重算窗口，并把新区间并入 `loaded`。
 *
 * 用 `getBoundingClientRect` 而不是 `offsetTop`：后者相对 offsetParent，而滚动量在
 * 滚动盒子上，两个坐标系拼起来容易差一截（差一屏就是「滚到底还有一排没图」）。
 */
function refreshImageBudget() {
  const total = items.value.length;
  if (total === 0) {
    return;
  }
  const grid = gridEl.value;
  if (!grid) return;

  const { cols, rowH } = measureGrid();
  const box = scrollTarget.value;
  const viewTop = box instanceof HTMLElement ? box.getBoundingClientRect().top : 0;
  const gridTop = grid.getBoundingClientRect().top;

  // 视口顶落在网格里的第几行（往上滚过头时为负 → 夹到 0）
  const firstRow = Math.max(0, Math.floor((viewTop - gridTop) / rowH));
  const rowsOnScreen = Math.max(1, Math.ceil(viewportHeight() / rowH));
  // 前后各留一屏
  const startRow = Math.max(0, firstRow - rowsOnScreen);
  const endRow = firstRow + rowsOnScreen * 2;

  const start = Math.max(0, Math.min(total, startRow * cols));
  const end = Math.max(start, Math.min(total, endRow * cols));
  if (start < loadedMin.value) loadedMin.value = start;
  if (end > loadedMax.value) loadedMax.value = end;
}

let onScrollTimer: number | undefined;
function scheduleBudgetRefresh() {
  window.clearTimeout(onScrollTimer);
  onScrollTimer = window.setTimeout(refreshImageBudget, 120);
}

/**
 * 这个位置该显示哪张图 —— 轮不到就返回空串（组件会渲染占位/骨架）。
 *
 * 判据是**列表下标**而不是元素的真实可视状态：网格是等高的，下标与屏幕位置
 * 一一对应，算一次比给每张卡挂 IntersectionObserver 便宜得多，也不会因为
 * 卡片进出视口而反复卸载/重载图片（那种抖动比多下几张图更难受）。
 */
function imageSrc(item: JavWallItem, index: number): string {
  if (index < loadedMin.value || index >= loadedMax.value) return "";
  return view.value === "poster" ? (item.poster_url ?? "") : (item.thumb_url ?? "");
}

// ————————————————————— 滚动容器 —————————————————————
//
// 图片窗口要跟着**真正在滚的那个盒子**走。这个组件在后台里是挂在 `.admin__body`
// 下面的（`overflow-y: auto`），而它自己不知道这件事 —— 所以往上找第一个
// 「overflow-y 是 auto/scroll 且真的能滚」的祖先，找不到才回落 `window`。
//
// ⚠️ 别改回 `window.addEventListener("scroll", …)`：那样一次都不会触发，
// 而失败是**静默**的（图少了一半，没有任何报错）。
//
// 挂在 `document` 上用**捕获**阶段监听：scroll 事件不冒泡，但**捕获阶段能收到
// 所有后代的滚动**，所以不用去猜是哪个祖先在滚，也不会漏掉布局变化后换容器的情形。
const gridEl = ref<HTMLElement | null>(null);
/** 当前认定的滚动盒子（只用于读视口高度；找不到时是 null = 用 window）。 */
const scrollTarget = ref<HTMLElement | null>(null);

/** 重新认定滚动盒子（布局或内容变了之后调）。 */
function bindScrollTarget() {
  scrollTarget.value = findScrollBox(gridEl.value);
}

/** 捕获阶段的滚动处理：任何后代的滚动都会走到这里。 */
function onAnyScroll() {
  if (!scrollTarget.value) bindScrollTarget();
  scheduleBudgetRefresh();
}

// ————————————————————— 拉到底续加载 —————————————————————
//
// 两条路一起用：
//   1. `IntersectionObserver` 盯底部哨兵 —— 「滚到底」那条路（哨兵常驻 DOM，见模板）；
//   2. `useFillViewport` —— 「内容不足一屏、根本没法滚」那条路。光有 1 解决不了它：
//      没有滚动空间时观察器只在挂载那一刻回调过一次，用户怎么拖都不动。
const sentinel = ref<HTMLElement | null>(null);
let loadMoreObserver: IntersectionObserver | null = null;

function disconnectLoadMoreObserver() {
  loadMoreObserver?.disconnect();
  loadMoreObserver = null;
}

function setupLoadMoreObserver() {
  disconnectLoadMoreObserver();
  if (typeof IntersectionObserver === "undefined") return;
  if (!hasMore.value || !sentinel.value) return;
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      void load({ append: true });
    },
    // 提前 320px 开始拉，滚到底时下一页通常已经在了 —— 与榜单 / 影库同一档间距。
    { rootMargin: "320px 0px" },
  );
  loadMoreObserver.observe(sentinel.value);
}

const { fill: fillViewportIfShort, reset: resetAutoFill } = useFillViewport({
  sentinel,
  hasMore,
  busy: computed(() => loading.value || loadingMore.value),
  loadMore: () => void load({ append: true }),
});

// ————————————————————— 列表变了 —————————————————————
//
// ⚠️ 「重置取图窗口」的判据是**查询条件变了**，不是**数组变了**。
//
// 加续加载之前，`items` 变了就等于「换了一批」（首屏、翻档、搜索），无条件重置没问题。
// 加了追加之后这个假设不成立：**追加一页也会让 items 变**，无条件重置会把已经显示出来的
// 图撤回去，往回滚就闪 —— 那正是用户说的「有些海报显示不出来」。
//
// 所以拆成两类 watch：
//   * 条件变了 → 重置窗口 + 重列（换批才重置）
//   * 只是变长了 → 不重置，只重新算一次窗口（新下标会被 refreshImageBudget 补进来）

/** 换了一批（首屏 / 换档 / 搜索 / 换排序）：窗口归零，然后重列。 */
function resetAndLoad() {
  loadedMin.value = Number.POSITIVE_INFINITY;
  loadedMax.value = 0;
  resetAutoFill();
  void load();
}

// taskId 变了只重置分类 —— 真正的重列由下面那条 watch 触发（category 也在它的源里，
// 所以「换任务」与「换分类」各自只跑一次 load）。
watch(() => props.taskId, () => {
  category.value = "";
});
watch([category, () => props.sort], resetAndLoad);
// 关键词来自页头那个输入框：那边是 v-model 直连，这里跟着 prop 变就重列（防抖）。
let keywordTimer: number | undefined;
watch(() => props.keyword, () => {
  window.clearTimeout(keywordTimer);
  keywordTimer = window.setTimeout(resetAndLoad, 300);
});

watch(() => items.value.length, () => {
  void nextTick(() => {
    bindScrollTarget();
    refreshImageBudget();
    // 网格渲染完再判一次「够不够一屏」—— 这时量到的哨兵位置才是最终的。
    fillViewportIfShort();
  });
});

// 列表换完（首屏 / 追加）也要重挂观察器：hasMore 可能从 false 翻成 true。
watch([sentinel, hasMore], () => {
  void nextTick(setupLoadMoreObserver);
});

// 视图切换（海报 ↔ 缩略图）会换掉卡片高度与列数，窗口要跟着重量一次。
watch(view, () => {
  void nextTick(() => {
    refreshImageBudget();
    fillViewportIfShort();
  });
});

onMounted(() => {
  void nextTick(() => {
    bindScrollTarget();
    refreshImageBudget();
  });
  // 捕获阶段：scroll 不冒泡，但 document 的捕获阶段能收到**所有后代**的滚动。
  document.addEventListener("scroll", onAnyScroll, { passive: true, capture: true });
  window.addEventListener("resize", scheduleBudgetRefresh);
  void load();
});
onUnmounted(() => {
  window.clearTimeout(keywordTimer);
  window.clearTimeout(onScrollTimer);
  disconnectLoadMoreObserver();
  document.removeEventListener("scroll", onAnyScroll, { capture: true } as EventListenerOptions);
  window.removeEventListener("resize", scheduleBudgetRefresh);
});

// 卡片上的播放键。逻辑在 composable 里（两面墙共用），这里只用它的状态驱动按钮。
const playback = useWallPlayback();

defineExpose({ refreshMeta, load });
</script>

<template>
  <div class="jav-wall">
    <!-- 全量扫描的常驻警告：那种模式每轮都会用侧车重建 nfo/海报、并重下封面，手改的会被覆盖 -->
    <p v-if="result?.full_sync_wipe" class="jav-wall__banner">
      ⚠ 本任务是「全量」扫描模式：每次扫描都会用侧车 JSON 重建 nfo 与海报，并重新下载封面（thumb / fanart），
      手工编辑会被覆盖。想保留编辑请把扫描方式改成「更新」或「补缺」；想还原自动生成的版本，就跑一次全量扫描。
    </p>

    <div class="jav-wall__bar">
      <div class="jav-wall__tabs">
        <button
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === '' }"
          @click="category = ''"
        >
          全部
        </button>
        <button
          v-for="cat in categories"
          :key="cat.name"
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === cat.name }"
          @click="category = cat.name"
        >
          {{ cat.name || "根目录" }}<span class="jav-tab__count">{{ cat.count }}</span>
        </button>
      </div>

      <!-- 搜索与排序都挪到页头那两个按钮上了（与 tmdb 那套外观一致、功能各是各的），
           这里只留视图切换。 -->
      <div class="jav-wall__tools">
        <div class="jav-wall__view" role="group" aria-label="视图切换">
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'thumb' }"
            title="缩略图（封面原图，横版）"
            @click="view = 'thumb'"
          >
            缩略图
          </button>
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'poster' }"
            title="海报图（2:3 竖版）"
            @click="view = 'poster'"
          >
            海报图
          </button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="jav-wall__state">加载中…</div>
    <div v-else-if="!items.length" class="jav-wall__state">
      这个任务的输出目录里还没有番号影片。先在「STRM 任务」里同步一次，或确认任务的媒体类型与输出目录。
    </div>

    <div v-else ref="gridEl" class="jav-wall__grid" :class="`jav-wall__grid--${view}`">
      <article v-for="(item, index) in items" :key="item.id" class="jav-card jav-card--wall">
        <div class="jav-card__cover" :class="{ 'jav-card__cover--poster': view === 'poster' }">
          <!-- 两个视图各自的 URL；都没有或加载失败 → 统一占位图。
               这里给 lazy=false 并自己按视口决定「发不发」——见 imgWindow。 -->
          <MediaImage
            :src="imageSrc(item, index)"
            :alt="item.number"
            :lazy="false"
          />
          <!-- 还没轮到取图时占住位置，避免取到图之前卡片高度塌下去 -->
          <div v-if="!imageSrc(item, index)" class="jav-card__skeleton" />

          <!-- 播放键：封面正中，鼠标指过去才浮出（与详情页那颗 `.jd-cover__play`
               同一形态）。挂在这儿而不是 `.jav-card__hover` 那一排里 ——
               两个视图的封面比例不同（横版 3:2 / 竖版 2:3），居中的圆钮两边都站得住，
               塞进底部那排则会随比例上下飘。 -->
          <button
            type="button"
            class="jav-card__play"
            :title="playback.loadingId.value === item.id ? '正在读取播放地址…' : '播放'"
            :disabled="playback.loadingId.value === item.id"
            @click.stop="playback.playJav(taskId ?? 0, item)"
          >
            <i
              :class="
                playback.loadingId.value === item.id ? 'fas fa-spinner fa-spin' : 'fas fa-play'
              "
            />
          </button>

          <div class="jav-card__hover">
            <AppButton type="button" size="sm" variant="secondary" @click="openEditor(item)">编辑</AppButton>
            <AppButton
              type="button"
              size="sm"
              variant="primary"
              :disabled="busyStem === item.stem"
              title="用本地 json 重建这一部的 nfo，并重新下载封面与海报（剧照只补缺的）"
              @click="rebuild(item)"
            >
              {{ busyStem === item.stem ? "重刮中…" : "重刮" }}
            </AppButton>
          </div>
          <div v-if="busyStem === item.stem" class="jav-wall__busy" />
        </div>
        <div class="jav-wall__meta">
          <div class="jav-wall__number" :title="item.number">{{ item.number }}</div>
          <div class="jav-wall__title" :title="item.title">{{ item.title }}</div>
          <div class="jav-wall__sub">{{ subtitle(item) }}</div>
        </div>
      </article>
    </div>

    <!-- 播放窗。跟着卡片上的播放键走，数据在 click 那一刻才取（见 useWallPlayback）。 -->
    <WallPlayer
      v-if="playback.open.value"
      :title="playback.title.value"
      :items="playback.items.value"
      @close="playback.close"
    />

    <!-- 拉到底续加载。哨兵**常驻 DOM**（不在上面的 v-if 分支里）—— 它一旦被移除，
         挂在它上面的 IntersectionObserver 就再也收不到回调了。
         没有文案时（首屏还在转圈 / 空列表）用 --idle 把高度收掉，免得底部留一条空白；
         元素本身仍在 DOM 里、观察器仍挂着。 -->
    <div
      ref="sentinel"
      class="jav-loadmore"
      :class="{ 'jav-loadmore--idle': !showLoadMore }"
    >
      <span v-if="loadingMore">正在加载更多…</span>
      <span v-else-if="hasMore">往下滚，还有 {{ total - items.length }} 条</span>
      <span v-else-if="items.length">已全部加载（{{ total }} 条）</span>
    </div>

    <StrmJavMetaDrawer
      :open="drawerOpen"
      :task-id="props.taskId"
      :item="editing"
      :full-sync-wipe="result?.full_sync_wipe ?? false"
      @close="drawerOpen = false"
      @changed="replaceItem"
    />
  </div>
</template>

<style scoped>
.jav-wall { display: flex; flex-direction: column; gap: 12px; }
.jav-wall__banner { margin: 0; padding: 10px 12px; border-radius: var(--radius-sm); background: rgba(180, 83, 9, 0.08); color: #b45309; font-size: 12px; line-height: 1.6; }
.jav-wall__bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.jav-wall__tabs { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.jav-tab__count { margin-left: 6px; font-size: 11px; opacity: 0.7; }
.jav-wall__tools { display: flex; align-items: center; gap: 8px; }
.jav-wall__view { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-pill); overflow: hidden; }
.jav-wall__view-btn { padding: 5px 12px; font-size: 12px; background: transparent; border: 0; color: var(--text-muted); cursor: pointer; }
.jav-wall__view-btn--active { background: var(--brand); color: #fff; }
.jav-wall__state { padding: 40px; text-align: center; color: var(--text-muted); }
/* 两套网格，按视图切：
   - 缩略图（横版）：5 列固定，保持原来的大小 —— 横图铺满宽度后本来就矮，再缩小就看不清了；
   - 海报图（竖版）：**与「STRM 刮削」那面墙同尺寸**（auto-fill 140px + 14px 间距）。
     两边都是 2:3 的海报，尺寸一致才像同一套界面；原来固定 5 列在宽屏上一张有 260px 宽，
     比刮削墙大了一大截。 */
.jav-wall__grid--thumb { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 16px; }
@media (max-width: 1280px) { .jav-wall__grid--thumb { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 900px) { .jav-wall__grid--thumb { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 640px) { .jav-wall__grid--thumb { grid-template-columns: repeat(2, minmax(0, 1fr)); } }

.jav-wall__grid--poster { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 14px; }

/* 海报图视图用 2:3，缩略图视图保持 .jav-card__cover 原本的 3:2 */
.jav-card__cover--poster { aspect-ratio: 2 / 3; }
.jav-wall__meta { padding: 8px 2px 0; display: flex; flex-direction: column; gap: 2px; }
.jav-wall__number { font-weight: 600; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__title { font-size: 12px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__sub { font-size: 11px; color: var(--text-muted); }

/* 播放键：封面正中，**鼠标指过去才浮出**（与详情页 `.jd-cover__play` 同一形态）。
   挂在这个位置而不是底部 `.jav-card__hover` 那一排，是为了两个视图共用一套定位 ——
   横版封面 3:2、竖版 2:3，居中的圆钮两边都站得住。 */
.jav-card__play { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); width: 52px; height: 52px; display: flex; align-items: center; justify-content: center; border: none; border-radius: 50%; background: rgba(15, 23, 42, 0.55); color: #fff; font-size: 1.1rem; cursor: pointer; opacity: 0; z-index: 3; transition: opacity 0.15s ease, background 0.15s ease; }
.jav-card:hover .jav-card__play,
.jav-card:focus-within .jav-card__play,
.jav-card__play:focus-visible { opacity: 1; }
.jav-card__play:hover:not(:disabled) { background: rgba(15, 23, 42, 0.75); }
/* 取地址期间留在原地转圈 —— opacity 保持 1，否则鼠标一移开就像「点了没反应」。 */
.jav-card__play:disabled { opacity: 1; cursor: default; }
@media (hover: none) { .jav-card__play { opacity: 1; } }

/* 悬停显形（照 CoverExtractToolCard 的 .cand-rm 那一套）：触屏上常显，否则永远点不到 */
.jav-card__hover { position: absolute; inset: auto 6px 6px 6px; display: flex; gap: 6px; justify-content: center; opacity: 0; transition: opacity 0.12s; z-index: 2; }
.jav-card:hover .jav-card__hover,
.jav-card:focus-within .jav-card__hover { opacity: 1; }
@media (hover: none) { .jav-card__hover { opacity: 1; } }
.jav-wall__busy { position: absolute; inset: 0; background: rgba(15, 23, 42, 0.25); }
/* 还没轮到取图的卡片：占住封面那块位置，免得图到之前高度塌下去、
   滚动位置跟着跳（那比多下几张图更难受）。 */
.jav-card__skeleton { position: absolute; inset: 0; background: var(--surface-sunken); }
</style>
