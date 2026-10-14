<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  searchMediaOrganizeTmdb,
  type MediaOrganizeTmdbSearchHit,
} from "@/api/mediaOrganize";
import { fetchStrmTasks, type StrmTask } from "@/api/strm";
import StrmJavWall from "@/components/admin/StrmJavWall.vue";
import StrmJavWallHiddenDirsModal from "@/components/admin/StrmJavWallHiddenDirsModal.vue";
import StrmWallLocalDirDialog from "@/components/admin/StrmWallLocalDirDialog.vue";
import StrmTmdbWallDeleteModal from "@/components/admin/StrmTmdbWallDeleteModal.vue";
import { JAV_WALL_SORTS } from "@/api/strmJavWall";
import {
  backfillStrmScrapeImages,
  fetchStrmScrapeItems,
  fetchStrmScrapeScope,
  fetchStrmScrapeProgress,
  refreshStrmScrapeIndex,
  rematchStrmScrapeItem,
  rescrapeStrmScrapeItem,
  markStrmScrapeNormal,
  runStrmScrape,
  saveStrmScrapeScope,
  stopStrmScrape,
  type StrmScrapeItem,
  type StrmScrapeItemListQuery,
  type StrmScrapeItemListResult,
  type StrmScrapeItemListSort,
  type StrmScrapeItemListStats,
  type StrmScrapeItemStatus,
  type StrmScrapeProgress,
  fetchWallLocalDirs,
  type WallLocalDir,
  type WallSourceRef,
} from "@/api/strmScrape";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import StrmScrapeScopePicker from "@/components/admin/StrmScrapeScopePicker.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppIconButton from "@/components/base/AppIconButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useConditionalPolling } from "@/composables/useConditionalPolling";
import { confirm } from "@/composables/useConfirm";
import { useVirtualPosterWall } from "@/composables/useVirtualPosterWall";
import { useWallDetail } from "@/composables/useWallDetail";
import TmdbWallDetailDrawer from "@/components/admin/TmdbWallDetailDrawer.vue";
import WallPlayer from "@/components/admin/WallPlayer.vue";
import { toast } from "@/composables/useToast";
import {
  hitId,
  hitKey,
  hitMediaType,
  hitPosterURL,
  hitTitle,
  hitTypeLabel,
  hitYear,
} from "@/utils/tmdbHit";

const emit = defineEmits<{ "open-settings": [] }>();

type FilterKey = "all" | StrmScrapeItemStatus;
type TypeFilter = "all" | "movie" | "tv";
type TVSubFilter = "all" | "ended" | "updating";
type SortKey = StrmScrapeItemListSort;

const SORT_STORAGE_KEY = "litepan:strm-scrape:sort";
const SORT_KEYS: SortKey[] = ["title_asc", "year_desc", "year_asc", "added_desc", "added_asc"];
const PAGE_LIMIT = 120;
const MAX_RELOAD_LIMIT = 200;

function emptyStats(): StrmScrapeItemListStats {
  return { total: 0, ok: 0, miss: 0, doubt: 0 };
}

function loadSavedSortKey(): SortKey {
  try {
    const raw = localStorage.getItem(SORT_STORAGE_KEY);
    if (raw && (SORT_KEYS as string[]).includes(raw)) return raw as SortKey;
  } catch {}
  return "added_desc";
}

function saveSortKey(key: SortKey) {
  try {
    localStorage.setItem(SORT_STORAGE_KEY, key);
  } catch {}
}

const tasks = ref<StrmTask[]>([]);
/**
 * 墙上挂的**本地目录**（不在任何 STRM 任务之下的媒体库目录，见后端 wall_source.go）。
 *
 * 与 `tasks` 并列成为下拉里的两组选项 —— 它们不是任务，所以不能塞进 `tasks`。
 */
const wallDirs = ref<WallLocalDir[]>([]);
const localDirOpen = ref(false);
/**
 * 下拉当前选中的是**哪个数据源**，值是 `"T:12"`（任务）或 `"L:<id>"`（本地目录）。
 *
 * 为什么合并成一个字符串键而不是两个 ref：`AppSelect` 的 modelValue 本来就是一个
 * 字符串，两组合并渲染天然如此；而 `selectedTaskId` 由它派生（见下），
 * 面板里二十来处 `selectedTaskId.value` 的读点就不用动。
 */
const selectedKey = ref<string | null>(null);
/** 由 `selectedKey` 派生的任务 id（本地目录源时为 null）。 */
const selectedTaskId = computed<number | null>(() => {
  const k = selectedKey.value ?? "";
  return k.startsWith("T:") ? Number(k.slice(2)) || null : null;
});
/** 由 `selectedKey` 派生的本地目录注册项。 */
const selectedLocalDir = computed<WallLocalDir | null>(() => {
  const k = selectedKey.value ?? "";
  if (!k.startsWith("L:")) return null;
  return wallDirs.value.find((d: WallLocalDir) => d.id === k.slice(2)) ?? null;
});
const scopeOpen = ref(false);
const scopeLoading = ref(false);
const excludedScopeDirs = ref<string[]>([]);
const items = ref<StrmScrapeItem[]>([]);
const stats = ref<StrmScrapeItemListStats>(emptyStats());
const totalMatched = ref(0);
const hasMore = ref(false);
let loadItemsSeq = 0;
let loadMoreObserver: IntersectionObserver | null = null;
const loading = ref(false);
useAdminPageLoading("tools", loading);
const refreshing = ref(false);
const loadingMore = ref(false);
const booted = ref(false);
const filter = ref<FilterKey>("all");
const typeFilter = ref<TypeFilter>("all");
const tvSubFilter = ref<TVSubFilter>("all");
const sortKey = ref<SortKey>(loadSavedSortKey());
const keyword = ref("");
const progress = ref<StrmScrapeProgress | null>(null);
const loadMoreSentinelEl = ref<HTMLElement | null>(null);

const matchOpen = ref(false);
const matchItem = ref<StrmScrapeItem | null>(null);
const matchQuery = ref("");
const matchSearchType = ref<"auto" | "movie" | "tv">("auto");
const matchSearching = ref(false);
const matchApplying = ref(false);
const candidates = ref<MediaOrganizeTmdbSearchHit[]>([]);
const selectedCandidateKey = ref("");
const previewPosterURL = ref("");
const previewPosterTitle = ref("");

/**
 * 下拉的选项：两组（STRM 任务 / 本地目录）。
 *
 * `group` 是新加的（见 AppSelect）—— `tag` 槽已经被「番号 / tmdb」占用，塞不下
 * 「这行是任务还是本地目录」，所以靠分组标题区分来源。
 */
const sourceOptions = computed(() => {
  const taskOpts = tasks.value.map((t) => ({
    value: `T:${t.id}`,
    label: t.name || `任务 #${t.id}`,
    tag: t.media_kind === "jav" ? "番号" : "tmdb",
    group: "STRM 任务",
  }));
  const localOpts = wallDirs.value.map((d: WallLocalDir) => ({
    value: `L:${d.id}`,
    label: d.name || d.root,
    tag: d.media_kind === "jav" ? "番号" : "tmdb",
    group: "本地目录",
  }));
  return [...taskOpts, ...localOpts];
});
const selectedTask = computed(() =>
  tasks.value.find((t) => Number(t.id) === Number(selectedTaskId.value)) || null,
);
/** 当前数据源的媒体类型（任务看 task，本地目录看注册项）。 */
const sourceMediaKind = computed(
  () => selectedTask.value?.media_kind ?? selectedLocalDir.value?.media_kind ?? "",
);
// 数据源是不是「番号影片」：决定整面墙长什么样（番号有自己的一套）。
// 任务与本地目录共用这一个判据 —— 后端也是同一套词（tmdb / jav）。
const isJavSource = computed(() => sourceMediaKind.value === "jav");
// 只有**任务**源才谈得上「番号墙隐藏目录」那些按任务存的东西。
const isJavTask = isJavSource;
/**
 * 「全部目录」那颗按钮对本地目录源**不适用**：它那两套语义（番号=整档隐藏哪些一级
 * 目录 / tmdb=刮削范围）都建立在「这个目录是某个任务的输出」之上。
 * 禁用它并给一句 title，比留一颗按了没意义的按钮好。
 */
const scopeApplicable = computed(() => selectedLocalDir.value === null);
const javWallRef = ref<{
  refreshMeta: () => void;
  // 在线刮削住在番号墙里（它要按目录扫盘），页头那颗按钮转调它。
  onlineScrape?: () => void;
  onlineScraping?: boolean;
  onlineScrapeLabel?: string;
} | null>(null);
// 番号墙「整档隐藏的目录」：那份名单是**全局**的（不按任务存），但入口在墙上。
const javHiddenOpen = ref(false);
// 已隐藏的目录数（页头那颗按钮的文案要用）。从墙的列表响应里带回来，省一次请求。
const javHiddenCount = ref(0);

/**
 * 当前数据源在请求里该怎么带：任务给 `strm_task_id`、本地目录给 `local_dir_id`。
 *
 * 抽成一个 computed 而不是每个调用点自己拼 —— 拼错一处就是「某个动作打到了别的源上」，
 * 而那种错在界面上是静默的。
 */
const sourceRef = computed<number | WallSourceRef>(() => {
  const dir = selectedLocalDir.value;
  if (dir) return { local_dir_id: dir.id };
  return selectedTaskId.value ?? 0;
});

// 页头那颗「全部目录」按钮的文案：两种任务各是各的一套（外观与位置完全一样）。
const scopeLabel = computed(() => {
  if (isJavTask.value) {
    return javHiddenCount.value ? `隐藏 ${javHiddenCount.value} 个目录` : "全部目录";
  }
  return excludedScopeDirs.value.length ? `已排除 ${excludedScopeDirs.value.length} 个目录` : "全部目录";
});

/** 页头那颗按钮点了之后走哪套：番号墙是「隐藏哪些目录」，其余是「刮削范围」。 */
function openScopePanel() {
  if (!scopeApplicable.value) return;
  if (isJavTask.value) {
    javHiddenOpen.value = true;
    return;
  }
  scopeOpen.value = true;
}

/**
 * 本地目录注册项变了（加了 / 移除了）。
 *
 * `selectedKey` 指向的那一项被移除时**清空选中**，让 `loadTasks` 那条回落逻辑
 * 把它挪到第一个可选项上 —— 不清的话下拉会显示一个不存在的值，而墙上还是旧数据。
 */
function onWallDirsChanged(dirs: WallLocalDir[]) {
  wallDirs.value = dirs;
  if (selectedKey.value?.startsWith("L:") && !dirs.some((d: WallLocalDir) => `L:${d.id}` === selectedKey.value)) {
    selectedKey.value = null;
  }
  void loadTasks();
}

/** 隐藏名单改了之后：重列墙（服务端已经作废了所有快照），并让按钮文案跟上。 */
function onJavHiddenSaved() {
  javWallRef.value?.refreshMeta();
}

async function loadScope() {
  const ref = sourceRef.value;
  excludedScopeDirs.value = [];
  // 番号影片不吃「刮削范围」（那是 TMDB 索引那条路的东西），这一趟纯属白跑。
  if (!selectedKey.value || isJavSource.value) return;
  try {
    const data = await fetchStrmScrapeScope(ref);
    if (sourceRef.value === ref) excludedScopeDirs.value = data.excluded_dirs ?? [];
  } catch (e) {
    toast.error(getApiErrorMessage(e, "读取刮削范围失败"));
  }
}

async function saveScope(dirs: string[]) {
  const ref = sourceRef.value;
  if (!selectedKey.value || scopeLoading.value) return;
  scopeLoading.value = true;
  try {
    const data = await saveStrmScrapeScope(ref, dirs);
    excludedScopeDirs.value = data.excluded_dirs ?? [];
    scopeOpen.value = false;
    // 番号影片不走 TMDB 索引（这颗按钮对它们是另一套），别白列一次。
    if (!isJavTask.value) await loadItems();
    toast.success("刮削范围已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存刮削范围失败"));
  } finally {
    scopeLoading.value = false;
  }
}

// 排序与搜索**按任务类型分叉，共用页头那两个按钮**（用户要求：
// 番号墙自己的搜索框/排序框不要了，都用页头这一套）。
//
// 两套选项的**取值不重叠**（tmdb 是 title/year/added，番号是 number/release/added），
// 各有各的存储，`currentSort` 只负责「当前该显示哪一个」。
const tmdbSortOptions: { value: SortKey; label: string }[] = [
  { value: "added_desc", label: "添加时间 · 新→旧" },
  { value: "added_asc", label: "添加时间 · 旧→新" },
  { value: "year_desc", label: "上映年份 · 新→旧" },
  { value: "year_asc", label: "上映年份 · 旧→新" },
  { value: "title_asc", label: "标题 A→Z" },
];

const matchTypeOptions = [
  { value: "auto", label: "全部" },
  { value: "movie", label: "电影" },
  { value: "tv", label: "剧集" },
];

const sortMenuOpen = ref(false);
// 番号墙的排序（自己存一份，不与 tmdb 那套混）。
// 默认「添加时间（新→旧）」—— 与后端 sortJavWallRows 的兜底一致，也与菜单里第一项一致。
const javSortKey = ref<string>("added_desc");
const sortOptions = computed(() =>
  isJavTask.value
    ? JAV_WALL_SORTS.map((o) => ({ value: o.value, label: o.label }))
    : tmdbSortOptions,
);
const currentSort = computed(() =>
  isJavTask.value ? javSortKey.value : (sortKey.value as string),
);
const namingTipOpen = ref(false);
const searchOpen = ref(false);
const searchInputEl = ref<HTMLInputElement | null>(null);
const currentSortLabel = computed(
  () => sortOptions.value.find((o) => o.value === currentSort.value)?.label ?? "排序",
);
const currentListQuery = computed<StrmScrapeItemListQuery>(() => ({
  keyword: keyword.value.trim(),
  status: filter.value === "all" ? "" : filter.value,
  media_type: typeFilter.value === "all" ? "" : typeFilter.value,
  tv_state: typeFilter.value === "tv" && tvSubFilter.value !== "all" ? tvSubFilter.value : "",
  sort: sortKey.value,
}));
const currentListQueryKey = computed(() =>
  JSON.stringify({
    source_key: selectedKey.value ?? '',
    ...currentListQuery.value,
  }),
);
const hasActiveFilters = computed(
  () =>
    Boolean(
      currentListQuery.value.keyword ||
        currentListQuery.value.status ||
        currentListQuery.value.media_type ||
        currentListQuery.value.tv_state,
    ),
);

function applySort(value: string) {
  if (isJavTask.value) {
    // 番号墙按 props 重列（子组件 watch 了 sort），这里不用手动催它。
    javSortKey.value = value;
    sortMenuOpen.value = false;
    return;
  }
  sortKey.value = value as SortKey;
  saveSortKey(value as SortKey);
  sortMenuOpen.value = false;
}

// 搜索也共用：番号那套用同一个输入框，只是把词交给子组件去过滤。
const javKeyword = ref("");
const activeKeyword = computed({
  get: () => (isJavTask.value ? javKeyword.value : keyword.value),
  set: (v: string) => {
    if (isJavTask.value) javKeyword.value = v;
    else keyword.value = v;
  },
});

async function toggleSearch() {
  searchOpen.value = !searchOpen.value;
  if (searchOpen.value) {
    await nextTick();
    window.setTimeout(() => searchInputEl.value?.focus(), 220);
  } else {
    activeKeyword.value = "";
  }
}

const scrapedCount = computed(() => stats.value.ok);
const missCount = computed(() => stats.value.miss);
const doubtCount = computed(() => stats.value.doubt);
const totalCount = computed(() => stats.value.total);
const loadedCount = computed(() => items.value.length);
const taskCount = computed(() => tasks.value.length);
const running = computed(() => Boolean(progress.value?.running));
const markingNormalId = ref("");
const rescrapingId = ref("");

const {
  rootEl: wallRootEl,
  measure: measureWall,
  resetScroll: resetWallScroll,
  totalHeight: wallTotalHeight,
  offsetY: wallOffsetY,
  visibleItems: wallVisibleItems,
  gridStyle: wallGridStyle,
} = useVirtualPosterWall(items);

function setWallRootEl(el: unknown) {
  wallRootEl.value = el instanceof HTMLElement ? el : null;
}

function setTypeFilter(next: TypeFilter) {
  typeFilter.value = next;
  tvSubFilter.value = "all";
}

function setTVSubFilter(next: TVSubFilter) {
  typeFilter.value = "tv";
  tvSubFilter.value = tvSubFilter.value === next ? "all" : next;
}

function clearItemList() {
  items.value = [];
  stats.value = emptyStats();
  totalMatched.value = 0;
  hasMore.value = false;
}

function currentFetchLimit(preserveLoaded = false) {
  if (!preserveLoaded) return PAGE_LIMIT;
  return Math.min(Math.max(items.value.length || PAGE_LIMIT, PAGE_LIMIT), MAX_RELOAD_LIMIT);
}

function buildListQuery(offset: number, limit: number): StrmScrapeItemListQuery {
  return {
    ...currentListQuery.value,
    offset,
    limit,
  };
}

function applyListResult(data: StrmScrapeItemListResult, append: boolean) {
  const nextItems = Array.isArray(data.items) ? data.items : [];
  items.value = append ? items.value.concat(nextItems) : nextItems;
  totalMatched.value = Number(data.total || 0);
  hasMore.value = Boolean(data.has_more);
  stats.value = data.stats ? { ...emptyStats(), ...data.stats } : emptyStats();
}

function replaceItem(updated: StrmScrapeItem) {
  const idx = items.value.findIndex((item) => item.id === updated.id);
  if (idx < 0) return false;
  const next = items.value.slice();
  next[idx] = updated;
  items.value = next;
  void nextTick(() => measureWall());
  return true;
}

async function requestItems(
  ref: number | WallSourceRef,
  query: StrmScrapeItemListQuery,
  refreshIndex = false,
) {
  return refreshIndex ? refreshStrmScrapeIndex(ref, query) : fetchStrmScrapeItems(ref, query);
}

function disconnectLoadMoreObserver() {
  loadMoreObserver?.disconnect();
  loadMoreObserver = null;
}

function setupLoadMoreObserver() {
  disconnectLoadMoreObserver();
  if (typeof IntersectionObserver === "undefined") return;
  if (!hasMore.value || !loadMoreSentinelEl.value) return;
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      void loadItems({ append: true, silent: true });
    },
    { root: null, rootMargin: "320px 0px" },
  );
  loadMoreObserver.observe(loadMoreSentinelEl.value);
}

async function loadItems(opts?: {
  append?: boolean;
  silent?: boolean;
  preserveLoaded?: boolean;
  refreshIndex?: boolean;
}) {
  const ref = sourceRef.value;
  if (!selectedKey.value) {
    clearItemList();
    return false;
  }
  // 番号影片的卡片是子组件直接扫本地磁盘来的，**不经过 TMDB 索引**。
  // 不早退的话首屏 onMounted 那一次 /refresh-index 会被后端挡下来
  // （errNotTmdbTask），弹一条「该任务不是 tmdb 影片任务，番号影片请在海报墙上操作」
  // —— 用户什么都没点就看到一个红条。watch(currentListQueryKey) 那条路早有同样的
  // 守卫，这里补上它缺的那一处。
  if (isJavSource.value) {
    clearItemList();
    return false;
  }
  const append = Boolean(opts?.append);
  if (append && (!hasMore.value || loadingMore.value || loading.value || refreshing.value)) {
    return false;
  }
  const seq = ++loadItemsSeq;
  const limit = append ? PAGE_LIMIT : currentFetchLimit(Boolean(opts?.preserveLoaded));
  const offset = append ? items.value.length : 0;
  if (append) {
    loadingMore.value = true;
  } else if (opts?.silent || opts?.refreshIndex) {
    refreshing.value = true;
  } else {
    loading.value = true;
    clearItemList();
  }
  try {
    const data = await requestItems(ref, buildListQuery(offset, limit), Boolean(opts?.refreshIndex));
    if (seq !== loadItemsSeq || sourceRef.value !== ref) return false;
    applyListResult(data, append);
    await nextTick();
    if (!append) {
      resetWallScroll();
    }
    measureWall();
    setupLoadMoreObserver();
    return true;
  } catch (e) {
    if (seq !== loadItemsSeq || sourceRef.value !== ref) return false;
    const fallback = append
      ? "加载更多失败"
      : opts?.refreshIndex
        ? "重建索引失败"
        : "加载海报墙失败";
    toast.error(getApiErrorMessage(e, fallback));
    return false;
  } finally {
    if (append) {
      loadingMore.value = false;
    } else {
      if (seq === loadItemsSeq) {
        loading.value = false;
      }
      refreshing.value = false;
    }
  }
}

const progressPolling = useConditionalPolling({
  intervalMs: 1200,
  tickWhen: () => running.value,
  onTick: () => syncProgress(),
  shouldPoll: () => running.value,
});

async function loadTasks() {
  const list = await fetchStrmTasks();
  tasks.value = Array.isArray(list) ? list : [];
  // 本地目录注册项与任务一起拉：它们是同一份下拉里的两组选项。
  try {
    const dirs = await fetchWallLocalDirs();
    wallDirs.value = dirs.dirs ?? [];
  } catch {
    // 拉不到注册项不该挡住任务那组 —— 那是另一件事的故障。
    wallDirs.value = [];
  }
  const valid = new Set([
    ...tasks.value.map((t) => `T:${t.id}`),
    ...wallDirs.value.map((d: WallLocalDir) => `L:${d.id}`),
  ]);
  if (selectedKey.value && !valid.has(selectedKey.value)) {
    selectedKey.value = null; // 选中的源没了（任务删了 / 目录移除了）
  }
  if (!selectedKey.value) {
    // 默认落到第一个可选项：优先任务（用户最常用），没有才用本地目录。
    if (tasks.value.length) selectedKey.value = `T:${tasks.value[0].id}`;
    else if (wallDirs.value.length) selectedKey.value = `L:${wallDirs.value[0].id}`;
  }
}

async function syncProgress() {
  try {
    const p = await fetchStrmScrapeProgress();
    const previous = progress.value;
    const wasRunning = previous?.running;
    // ⚠️ 比 **source_key** 而不是 strm_task_id：进度是单槽全局，本地目录源跑起来时
    // strm_task_id 是 0，只比它会让任务墙「认领」别的源那一轮（亮起「刮削中」）。
    const sameTask = (p.source_key ?? "") === (selectedKey.value ?? "");
    const previousRevision =
      Number(previous?.strm_task_id) === Number(p.strm_task_id)
        ? Number(previous?.item_revision || 0)
        : 0;
    progress.value = p;
    if (sameTask && p.running && Number(p.item_revision || 0) > previousRevision) {
      const revisionDelta = Number(p.item_revision || 0) - previousRevision;
      if (revisionDelta !== 1 || !p.updated_item || !replaceItem(p.updated_item)) {
        await loadItems({ silent: true, preserveLoaded: true });
      }
    }
    if (wasRunning && !p.running) {
      progressPolling.sync();
      await loadItems({ silent: true, preserveLoaded: true });
      if (p.error) toast.error(p.error);
      else if (p.failed > 0) toast.warning(`刮削完成，失败 ${p.failed} 项，请在通知中查看详情`);
      else if (p.message) toast.success(p.message);
    }
  } catch {}
}

async function startScrape() {
  if (!selectedKey.value) {
    toast.error("请先选择任务或本地目录");
    return;
  }
  if (running.value) return;
  try {
    const p = await runStrmScrape(sourceRef.value);
    progress.value = p;
    progressPolling.sync();
    toast.success("已开始刮削");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "启动刮削失败"));
  }
}

async function stopScrape() {
  try {
    const p = await stopStrmScrape();
    progress.value = p;
    toast.success("已请求停止");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "停止失败"));
  }
}

/**
 * 补齐剧照与演员。
 *
 * 存量作品的入口：`missing_only` 策略下，已经刮过的作品会被跳过（那是对的 ——
 * 否则每次刮削都要全量重跑），所以加了新抓取范围之后得有个**单独**的入口去补。
 * 只补缺：不重写已有 nfo 的正文，也不覆盖已有的图。
 */
async function backfillArtwork() {
  if (!selectedKey.value) {
    toast.error("请先选择任务或本地目录");
    return;
  }
  if (running.value) {
    toast.error("刮削任务进行中，请稍后再试");
    return;
  }
  try {
    await confirm({
      title: "补齐剧照与演员",
      message:
        "将扫描本任务下**已经刮过**的作品，给缺少剧照、演员、背景图或完整 nfo 的那些补上（只补缺，不改动已有的 nfo 正文与图片）。每部作品要下载若干张图，数量多时请耐心等待。",
      icon: "info",
      confirmText: "开始补齐",
      danger: false,
    });
  } catch {
    return;
  }
  try {
    const p = await backfillStrmScrapeImages(sourceRef.value);
    progress.value = p;
    progressPolling.sync();
    toast.success("已开始补齐");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "启动补齐失败"));
  }
}

async function refreshAll() {
  await loadTasks();
  if (!selectedKey.value) {
    clearItemList();
    await syncProgress();
    return;
  }
  // 番号影片：那颗键是「重读本地文件」（不联网、不重建 nfo/图片），
  // 与 TMDB 那套「重建刮削索引」完全是两码事 —— 外观一样，功能各是各的。
  if (isJavTask.value) {
    javWallRef.value?.refreshMeta();
    await syncProgress();
    return;
  }
  const ok = await loadItems({ refreshIndex: true });
  if (ok) toast.success("索引已重建");
  await syncProgress();
}

function statusTitle(status: StrmScrapeItemStatus) {
  if (status === "doubt") return "自动匹配结果需要确认；正确可点「确认」，不正确可点「匹配」";
  if (status === "miss") return "待刮削：根目录缺 nfo/海报，或未收录作品可「完成」";
  return "根目录 nfo / 海报已齐备";
}

function episodeProgressText(item: StrmScrapeItem) {
  if (item.media_type !== "tv") {
    return item.file_count > 1 ? `${item.file_count} 个文件` : "";
  }
  const local = Number(item.ep_local || 0);
  const tmdb = Number(item.ep_tmdb || 0);
  if (item.tv_state === "ended") return "完结";
  if (item.tv_state === "updating") {
    if (tmdb > 0) return `${local || 0}/${tmdb}集`;
    return local > 0 ? `${local}集 · 更新中` : "更新中";
  }
  if (item.has_pending && tmdb > 0 && local > tmdb) return `${local}/${tmdb}集`;
  if (tmdb > 0) return `${local}/${tmdb}集`;
  if (local > 0) return `${local}集`;
  return item.file_count > 1 ? `${item.file_count} 集` : "";
}

function canMarkEnded(item: StrmScrapeItem) {
  return Boolean(
    item.status !== "doubt" &&
      (item.has_pending || item.status === "miss"),
  );
}

function markActionLabel(item: StrmScrapeItem) {
  return item.has_nfo && item.has_poster ? "完结" : "完成";
}

function canConfirmDoubt(item: StrmScrapeItem) {
  return Boolean(item.status === "doubt" && item.has_nfo && item.has_poster);
}

function canRescrape(item: StrmScrapeItem) {
  return Boolean(
    !item.has_pending &&
      item.status === "ok" &&
      (item.manual_done || (item.has_nfo && item.has_poster && String(item.tmdb_id || "").trim())),
  );
}

function isItemBusy(item: StrmScrapeItem) {
  if (rescrapingId.value && rescrapingId.value === item.id) return true;
  if (!running.value) return false;
  // 比 source_key：本地目录源的 strm_task_id 是 0，只比它会让别的源的进度被判成本源的。
  if ((progress.value?.source_key ?? "") !== (selectedKey.value ?? "")) return false;
  return String(progress.value?.current_item_id || "") === item.id;
}

function statusMarkTitle(item: StrmScrapeItem) {
  if (item.manual_done) return "已手动完成";
  if (item.status === "doubt") return statusTitle(item.status);
  if (item.tv_state === "updating") return "已刮削 · 追更中";
  return statusTitle(item.status);
}

function trimSlashes(input: string) {
  return String(input || "").replace(/^\/+|\/+$/g, "");
}

const rematchSourceDir = computed(() => {
  const item = matchItem.value;
  if (!item) return "";
  const taskBase = trimSlashes(
    [selectedTask.value?.group_dir || "", selectedTask.value?.output_folder || ""].filter(Boolean).join("/"),
  );
  const relDir = trimSlashes(item.rel_dir || "");
  const parts = [taskBase, relDir].filter(Boolean);
  return parts.length ? `/${parts.join("/")}` : "/";
});

function openRematch(item: StrmScrapeItem) {
  matchItem.value = item;
  matchQuery.value = item.title || item.folder_name || item.strm_name?.replace(/\.strm$/i, "") || "";
  matchSearchType.value = item.media_type === "tv" || item.media_type === "movie" ? item.media_type : "auto";
  candidates.value = [];
  selectedCandidateKey.value = "";
  matchOpen.value = true;
}

function closeRematch() {
  matchOpen.value = false;
  matchItem.value = null;
  closePosterPreview();
}

function openPosterPreview(hit: MediaOrganizeTmdbSearchHit, ev?: Event) {
  ev?.stopPropagation();
  const url = hitPosterURL(hit, "w500");
  if (!url) return;
  selectedCandidateKey.value = hitKey(hit);
  previewPosterURL.value = url;
  previewPosterTitle.value = hitTitle(hit);
  void nextTick(() => {
    const el = document.querySelector(".scrape-poster-preview") as HTMLElement | null;
    el?.focus();
  });
}

function closePosterPreview() {
  previewPosterURL.value = "";
  previewPosterTitle.value = "";
}

function onPosterPreviewKeydown(ev: KeyboardEvent) {
  if (ev.key === "Escape" && previewPosterURL.value) {
    ev.preventDefault();
    ev.stopPropagation();
    ev.stopImmediatePropagation();
    closePosterPreview();
  }
}

async function searchCandidates() {
  const q = matchQuery.value.trim();
  if (!q) {
    toast.error("请输入片名或 TMDB ID");
    return;
  }
  matchSearching.value = true;
  try {
    const results = await searchMediaOrganizeTmdb({
      query: q,
      media_type: matchSearchType.value,
    });
    candidates.value = Array.isArray(results) ? results.slice(0, 20) : [];
    selectedCandidateKey.value = candidates.value[0] ? hitKey(candidates.value[0]) : "";
    if (!candidates.value.length) toast.error("没有找到候选");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "搜索失败"));
  } finally {
    matchSearching.value = false;
  }
}

async function applyMatch() {
  if (!matchItem.value || !selectedKey.value || !selectedCandidateKey.value) return;
  const hit = candidates.value.find((c) => hitKey(c) === selectedCandidateKey.value);
  if (!hit) return;
  matchApplying.value = true;
  try {
    const year = hitYear(hit);
    const result = await rematchStrmScrapeItem({
      ...(typeof sourceRef.value === "number" ? { strm_task_id: sourceRef.value } : sourceRef.value),
      item_id: matchItem.value.id,
      tmdb_id: hitId(hit),
      media_type: hitMediaType(hit),
      title: hitTitle(hit),
      year,
    });
    if (result.started) {
      progress.value = result.progress;
      progressPolling.sync();
      toast.success("已开始后台重新匹配");
      closeRematch();
      return;
    }
    if (!replaceItem(result.item)) await loadItems({ silent: true, preserveLoaded: true });
    toast.success("已确认当前匹配");
    closeRematch();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "应用匹配失败"));
  } finally {
    matchApplying.value = false;
  }
}

async function clearMatchAndComplete() {
  const item = matchItem.value;
  if (!item || !selectedKey.value || matchApplying.value) return;
  try {
    await confirm({
      title: "取消错误匹配",
      message: `确认「${item.title}」在 TMDB 中没有对应影片？将清理由 STRM 刮削生成的匹配元数据并标记完成，网盘文件和 STRM 文件不受影响。`,
      icon: "warning",
      confirmText: "取消匹配并完成",
      danger: true,
    });
  } catch {
    return;
  }
  matchApplying.value = true;
  try {
    const mediaType = matchSearchType.value === "auto" ? item.media_type : matchSearchType.value;
    const updated = await markStrmScrapeNormal({
      ...(typeof sourceRef.value === "number" ? { strm_task_id: sourceRef.value } : sourceRef.value),
      item_id: item.id,
      media_type: mediaType,
      clear_match: true,
    });
    if (!replaceItem(updated)) await loadItems({ silent: true, preserveLoaded: true });
    toast.success("已取消匹配并标记完成");
    closeRematch();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "取消匹配失败"));
  } finally {
    matchApplying.value = false;
  }
}

async function markEnded(item: StrmScrapeItem) {
  if (!selectedKey.value || !canMarkEnded(item)) return;
  const title = (item.title || item.folder_name || "该影片").trim();
  const manual = markActionLabel(item) === "完成";
  try {
    await confirm({
      title: manual ? "标记完成" : "设为完结",
      message: manual
        ? `将「${title}」标记为完成：该作品将不再自动匹配 TMDB，需要时可用「重新刮削」恢复。确定继续？`
        : `将「${title}」标记为完结：之后即使 TMDB 或本地有新集，刮削也不会再处理该目录。需要时可用「重新刮削」。确定继续？`,
      icon: "warning",
      confirmText: manual ? "标记完成" : "设为完结",
      danger: false,
    });
  } catch {
    return;
  }
  markingNormalId.value = item.id;
  try {
    await applyNormalState(item);
    toast.success(manual ? "已标记完成" : "已设为完结");
  } catch (e) {
    toast.error(getApiErrorMessage(e, manual ? "标记完成失败" : "设为完结失败"));
  } finally {
    markingNormalId.value = "";
  }
}

async function confirmDoubt(item: StrmScrapeItem) {
  if (!selectedKey.value || !canConfirmDoubt(item)) return;
  markingNormalId.value = item.id;
  try {
    await applyNormalState(item);
    toast.success("已确认当前匹配");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "确认匹配失败"));
  } finally {
    markingNormalId.value = "";
  }
}

async function applyNormalState(item: StrmScrapeItem) {
  if (!selectedKey.value) return;
  const updated = await markStrmScrapeNormal({
    ...(typeof sourceRef.value === "number" ? { strm_task_id: sourceRef.value } : sourceRef.value),
    item_id: item.id,
    media_type: item.media_type,
  });
  if (!replaceItem(updated)) {
    await loadItems({ silent: true, preserveLoaded: true });
  }
}

async function rescrapeItem(item: StrmScrapeItem) {
  if (!selectedKey.value || !canRescrape(item) || running.value) return;
  rescrapingId.value = item.id;
  try {
    const result = await rescrapeStrmScrapeItem({
      ...(typeof sourceRef.value === "number" ? { strm_task_id: sourceRef.value } : sourceRef.value),
      item_id: item.id,
    });
    if (result.started) {
      progress.value = result.progress;
      progressPolling.sync();
      toast.success("已开始后台重新刮削");
      return;
    }
    if (!replaceItem(result.item)) await loadItems({ silent: true, preserveLoaded: true });
    toast.success(item.manual_done ? "已恢复待刮削" : "已重新刮削");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "重新刮削失败"));
  } finally {
    rescrapingId.value = "";
  }
}

// —— 删除一部作品（电影一部 / 剧集整部）——
//
// 点垃圾桶**不直接删**，先开确认弹窗（`StrmTmdbWallDeleteModal`）——那一份会去
// 服务端取「会删掉什么」的清单摆给用户看，再让他点第二次。这是会删用户东西的动作。
//
// 删完**只把这张卡从当前列表里摘掉**（不整表重列，滚动位置不跳）；服务端那边
// 已经作废了索引缓存，下一次真正刷新时列表会与磁盘一致。
const deleting = ref<StrmScrapeItem | null>(null);

function askDelete(item: StrmScrapeItem) {
  deleting.value = item;
}

function onDeleted() {
  const id = deleting.value?.id;
  deleting.value = null;
  if (!id) return;
  const idx = items.value.findIndex((it) => it.id === id);
  if (idx < 0) return;
  items.value.splice(idx, 1);
  stats.value = { ...stats.value, total: Math.max(0, stats.value.total - 1) };
}

watch(selectedTaskId, () => {
  void loadScope();
  if (!booted.value) return;
  filter.value = "all";
  typeFilter.value = "all";
  tvSubFilter.value = "all";
  keyword.value = "";
  // 番号那套的关键词也要清：它是页头同一个输入框的另一半，留着会带到下一个任务上。
  javKeyword.value = "";
  javHiddenCount.value = 0;
});

watch(currentListQueryKey, () => {
  if (!booted.value) return;
  // 番号影片的卡片是子组件直接扫本地磁盘来的，**不经过 TMDB 索引** ——
  // 不早退的话每敲一个字都会白发一次 TMDB 列表请求（错了还会弹 toast）。
  if (isJavSource.value) return;
  if (!selectedKey.value) {
    clearItemList();
    return;
  }
  void loadItems();
});

watch([loadMoreSentinelEl, hasMore], () => {
  void nextTick(() => setupLoadMoreObserver());
});

onMounted(async () => {
  window.addEventListener("keydown", onPosterPreviewKeydown, true);
  loading.value = true;
  try {
    await loadTasks();
    await syncProgress();
    if (selectedKey.value) {
      await loadItems();
    } else {
      clearItemList();
    }
    booted.value = true;
    progressPolling.sync();
  } finally {
    loading.value = false;
  }
});

onUnmounted(() => {
  disconnectLoadMoreObserver();
  window.removeEventListener("keydown", onPosterPreviewKeydown, true);
  progressPolling.stop?.();
});

// 卡片点击 → 详情抽屉 → 从抽屉里播放。逻辑在 composable 里（两面墙共用），
// 这里只把状态接到模板上。**卡片上不再有播放键**（用户要求：点卡片开详情，播放从详情里发起）。
const wall = useWallDetail();

defineExpose({
  startScrape,
  stopScrape,
  backfillArtwork,
  // 番号任务的「在线刮削」在子组件（StrmJavWall）里，页头那颗按钮要转调它 ——
  // 与上面几个同一条规矩：动作住在实现那一侧，页头只做入口。
  onlineScrape: () => javWallRef.value?.onlineScrape?.(),
  onlineScraping: computed(() => Boolean(javWallRef.value?.onlineScraping)),
  onlineScrapeLabel: computed(() => javWallRef.value?.onlineScrapeLabel ?? "在线刮削"),
  // 父组件（辅助工具页）要用它决定页头那几个按钮显不显示：番号那面跑 TMDB 刮削
  // 会往同一批目录写 nfo，而「标记为正常」那一步会把 thumb/poster 删掉。
  // ⚠️ 对外叫 `isJavSource`：它同时涵盖**任务**与**本地目录**两种源（判据是
  // 数据源的 media_kind，两者同一套词）。父组件原先读的是 `isJavTask`。
  isJavSource,
  refresh: refreshAll,
  refreshing,
  running,
  taskCount,
  totalCount,
  scrapedCount,
  missCount,
  doubtCount,
});
</script>

<template>
  <div class="scrape-panel">
    <div class="scrape-panel__head">
      <div class="scrape-panel__head-left">
        <h2>海报墙</h2>
        <div v-if="sourceOptions.length" class="scrape-panel__task-select">
          <AppSelect
            :model-value="selectedKey ?? ''"
            :options="sourceOptions"
            @update:model-value="(v) => (selectedKey = String(v) || null)"
          />
        </div>
        <AppButton
          v-if="selectedKey"
          variant="secondary"
          size="md"
          class="scrape-panel__scope-button"
          :disabled="running || scopeLoading || !scopeApplicable"
          :title="scopeApplicable ? undefined : '本地目录源没有「按任务存的范围 / 隐藏目录」'"
          @click="openScopePanel"
        >
          {{ scopeLabel }}
        </AppButton>
        <!-- 「本地目录」入口：把不在任何任务之下的媒体库目录挂到墙上。 -->
        <AppButton
          variant="secondary"
          size="md"
          class="scrape-panel__scope-button"
          :disabled="running"
          title="把本地的媒体库目录挂到海报墙上"
          @click="localDirOpen = true"
        >
          <i class="fas fa-folder-plus" />&nbsp;本地目录
        </AppButton>
      </div>
      <div class="scrape-panel__head-actions">
        <div class="scrape-search-expand" :class="{ 'scrape-search-expand--open': searchOpen }">
          <input
            ref="searchInputEl"
            v-model="activeKeyword"
            class="scrape-search-expand__input"
            type="search"
            :placeholder="isJavTask ? '搜索番号 / 片名' : '搜索片名'"
            :tabindex="searchOpen ? 0 : -1"
            @keydown.escape.prevent="toggleSearch"
          />
          <span class="scrape-tip">
            <button
              type="button"
              class="scrape-icon-btn"
              :class="{ 'scrape-icon-btn--active': searchOpen || Boolean(activeKeyword) }"
              :aria-label="searchOpen ? '收起搜索' : '搜索片名'"
              :aria-expanded="searchOpen"
              @click="toggleSearch"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <circle cx="7" cy="7" r="4.5" />
                <path d="m10.5 10.5 3 3" />
              </svg>
            </button>
            <span class="scrape-tip__bubble">{{ searchOpen ? "收起搜索" : "搜索片名" }}</span>
          </span>
        </div>
        <AppDropdown v-model:open="sortMenuOpen" trigger="click" align="right">
          <template #trigger="{ open, toggle }">
            <span class="scrape-tip">
              <button
                type="button"
                class="scrape-icon-btn"
                :class="{ 'scrape-icon-btn--active': open }"
                :aria-expanded="open"
                aria-label="排序"
                @click.stop="toggle"
              >
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
                  <path d="M2 4h10" />
                  <path d="M2 8h8" />
                  <path d="M2 12h6" />
                  <path d="m12 10 2 2 2-2" />
                </svg>
              </button>
              <span class="scrape-tip__bubble">{{ currentSortLabel }}</span>
            </span>
          </template>
          <template #panel>
            <div class="scrape-sort-menu">
              <button
                v-for="opt in sortOptions"
                :key="opt.value"
                type="button"
                class="scrape-sort-menu__item"
                :class="{ 'scrape-sort-menu__item--active': currentSort === opt.value }"
                @click="applySort(opt.value)"
              >
                {{ opt.label }}
              </button>
            </div>
          </template>
        </AppDropdown>
        <span class="scrape-tip scrape-tip--right">
          <AppIconButton
            icon="fa-sync-alt"
            label="刷新"
            variant="secondary"
            size="md"
            :disabled="refreshing"
            no-native-title
            @click="refreshAll"
          />
          <span class="scrape-tip__bubble">
            {{ isJavTask ? "刷新元数据（只重新读取本地文件）" : "重建刮削索引（扫描本地 STRM 目录）" }}
          </span>
        </span>
        <!-- 设置改成下拉：刮削设置一直有；番号影片时多一项「隐藏的目录」直达 ——
             那颗 ⚙ 在两种任务下长得一样，菜单里有几项按任务类型分。 -->
        <AppDropdown align="right">
          <template #trigger="{ toggle }">
            <span class="scrape-tip scrape-tip--right">
              <AppIconButton
                icon="settings"
                label="设置"
                variant="secondary"
                size="md"
                no-native-title
                @click.stop="toggle"
              />
              <span class="scrape-tip__bubble">STRM 刮削设置</span>
            </span>
          </template>
          <template #panel>
            <div class="scrape-menu">
              <button type="button" class="scrape-menu__item" @click="emit('open-settings')">
                <span>STRM 刮削设置</span>
              </button>
              <!-- 「补齐剧照与演员」**不在这里** —— 它挪到页头当按钮了（见
                   AuxToolsManagement.vue）。藏在设置下拉里用户找不到（实测问「在哪里」），
                   而它是个动作、不是设置项。 -->
              <button
                v-if="isJavTask"
                type="button"
                class="scrape-menu__item"
                @click="javHiddenOpen = true"
              >
                <span>番号墙隐藏的目录</span>
                <span class="scrape-menu__hint">勾上的目录整档不出现在墙上</span>
              </button>
            </div>
          </template>
        </AppDropdown>
      </div>
    </div>

    <StrmScrapeScopePicker
      v-if="selectedTaskId && !isJavSource"
      :open="scopeOpen"
      :task-id="selectedTaskId"
      :task-name="selectedTask?.name"
      :excluded-dirs="excludedScopeDirs"
      @close="scopeOpen = false"
      @save="saveScope"
    />

    <!-- 番号影片那颗按钮是另一码事：勾哪些一级目录**整档不出现在墙上**（全局一份）。 -->
    <StrmJavWallHiddenDirsModal
      v-if="isJavSource"
      :open="javHiddenOpen"
      :source="sourceRef"
      @close="javHiddenOpen = false"
      @saved="onJavHiddenSaved"
    />

    <!-- 本地目录：把不在任何任务之下的媒体库目录挂到墙上。 -->
    <StrmWallLocalDirDialog
      :open="localDirOpen"
      @close="localDirOpen = false"
      @changed="onWallDirsChanged"
    />

    <!-- TMDB 墙的删除确认弹窗（电影删这一部 / 剧集删整部）。 -->
    <StrmTmdbWallDeleteModal
      :open="deleting !== null"
      :source="sourceRef"
      :item="deleting"
      @close="deleting = null"
      @deleted="onDeleted"
    />

    <div v-if="running && progress" class="scrape-progress">
      <span class="scrape-progress__msg">{{ progress.message || "刮削进行中…" }}</span>
      <span class="scrape-progress__nums">{{ progress.done }}/{{ progress.total }}</span>
    </div>

    <AdminEmptyState
      v-if="!loading && !tasks.length && !wallDirs.length"
      icon="🎬"
      title="还没有可挂的数据源"
      description="可以先去「STRM 任务」里建一个任务；或者点上面的「本地目录」把已有的媒体库目录挂上来。"
    />

    <!-- 番号影片：整套换成番号自己的海报墙（一级目录 tab / 视图切换 / 编辑 / 重刮）。
         搜索与排序也由**页头**那两颗按钮驱动（外观与 TMDB 那套一样，功能各是各的）。 -->
    <StrmJavWall
      v-if="!loading && isJavTask"
      ref="javWallRef"
      :source="sourceRef"
      :keyword="javKeyword"
      :sort="javSortKey"
      @hidden-dirs="javHiddenCount = $event.length"
    />

    <template v-else-if="!loading">
      <div class="scrape-toolbar">
        <div class="scrape-filters">
          <button
            v-for="f in [
              { key: 'all', label: '全部状态' },
              { key: 'ok', label: '已刮削' },
              { key: 'doubt', label: '匹配存疑' },
              { key: 'miss', label: '缺失' },
            ]"
            :key="f.key"
            type="button"
            class="scrape-filter"
            :class="{ 'scrape-filter--active': filter === f.key }"
            @click="filter = f.key as FilterKey"
          >
            {{ f.label }}
          </button>
          <span class="scrape-toolbar__sep" />
          <button
            type="button"
            class="scrape-filter"
            :class="{ 'scrape-filter--active': typeFilter === 'all' }"
            @click="setTypeFilter('all')"
          >
            全部类型
          </button>
          <button
            type="button"
            class="scrape-filter"
            :class="{ 'scrape-filter--active': typeFilter === 'movie' }"
            @click="setTypeFilter('movie')"
          >
            电影
          </button>
          <span
            class="scrape-type-tv"
            :class="{ 'scrape-type-tv--on': typeFilter === 'tv' }"
          >
            <button
              type="button"
              class="scrape-filter scrape-filter--inline"
              :class="{ 'scrape-filter--active': typeFilter === 'tv' && tvSubFilter === 'all' }"
              @click="setTypeFilter('tv')"
            >
              剧集
            </button>
            <span class="scrape-type-tv__mark" aria-hidden="true">（</span>
            <button
              type="button"
              class="scrape-filter scrape-filter--inline"
              :class="{ 'scrape-filter--active': typeFilter === 'tv' && tvSubFilter === 'ended' }"
              @click="setTVSubFilter('ended')"
            >
              完结
            </button>
            <span class="scrape-type-tv__mark" aria-hidden="true">·</span>
            <button
              type="button"
              class="scrape-filter scrape-filter--inline"
              :class="{ 'scrape-filter--active': typeFilter === 'tv' && tvSubFilter === 'updating' }"
              @click="setTVSubFilter('updating')"
            >
              追更
            </button>
            <span class="scrape-type-tv__mark" aria-hidden="true">）</span>
          </span>
        </div>
        <div class="scrape-toolbar__right">
          <span v-if="totalMatched > 0" class="scrape-toolbar__count">已加载 {{ loadedCount }} / {{ totalMatched }}</span>
          <AppDropdown v-model:open="namingTipOpen" trigger="click" align="right" :min-width="340">
            <template #trigger="{ open, toggle }">
              <button
                type="button"
                class="scrape-naming-tip"
                :class="{ 'scrape-naming-tip--open': open }"
                :aria-expanded="open"
                @click.stop="toggle"
              >
                <span class="scrape-naming-tip__mark" aria-hidden="true">i</span>
                <span class="scrape-naming-tip__text">命名越规范，刮削越稳</span>
              </button>
            </template>
            <template #panel>
              <div class="scrape-naming-panel">
                <div class="scrape-naming-panel__title">刮削小提示</div>
                <p class="scrape-naming-panel__lead">
                  这里是辅助刮削，识别能力有限。若库里的命名比较杂乱，可能会出现漏刮或认不准；把目录和文件名整理得清楚一些，成功率会高很多。
                </p>
                <div class="scrape-naming-panel__subtitle">可以参考这样命名</div>
                <ul class="scrape-naming-panel__list">
                  <li>电影目录：<code>片名 (年份)</code></li>
                  <li>剧集目录：<code>剧名 (年份)</code>，集文件用 <code>SxxExx</code></li>
                  <li>发布组、分辨率等信息，尽量别塞进作品目录名里</li>
                  <li>已经比较乱的库，先稍微整理一下再刮，会轻松不少</li>
                </ul>
              </div>
            </template>
          </AppDropdown>
        </div>
      </div>

      <AdminEmptyState
        v-if="!items.length"
        icon="🖼️"
        :title="totalCount === 0 && !hasActiveFilters ? '这个库还没有刮削结果' : '没有符合筛选的条目'"
        :description="
          totalCount === 0 && !hasActiveFilters
            ? '点击右上角「开始刮削」，系统会扫描该 STRM 输出目录并写入 nfo / 海报。'
            : '试试切换筛选或清空搜索。'
        "
      >
        <AppButton
          v-if="totalCount === 0 && !hasActiveFilters && !running"
          type="button"
          variant="primary"
          @click="startScrape"
        >
          开始刮削
        </AppButton>
      </AdminEmptyState>

      <div v-else :ref="setWallRootEl" class="scrape-wall-root">
        <div class="scrape-wall-phantom" :style="{ height: `${wallTotalHeight}px` }">
          <div
            class="scrape-wall"
            :style="{ ...wallGridStyle, transform: `translateY(${wallOffsetY}px)` }"
          >
            <article
              v-for="item in wallVisibleItems"
              :key="item.id"
              class="scrape-card"
              :class="{ 'scrape-card--busy': isItemBusy(item) }"
              role="button"
              tabindex="0"
              :title="`《${item.title}》· 点开详情`"
              @click="wall.openTMDB(selectedTaskId ?? 0, item)"
              @keydown.enter.prevent="wall.openTMDB(selectedTaskId ?? 0, item)"
            >
              <div class="scrape-card__poster">
                <!-- 加载失败也落到统一占位图（以前只有「没有 URL」才有兜底）。 -->
                <MediaImage :src="item.poster_url ?? ''" :alt="item.title" />

                <span
                  class="scrape-card__mark"
                  :class="`scrape-card__mark--${item.status}`"
                  :title="statusMarkTitle(item)"
                >
                  <i
                    class="fas"
                    :class="{
                      'fa-check': item.status === 'ok',
                      'fa-minus': item.status === 'miss',
                      'fa-exclamation': item.status === 'doubt',
                    }"
                  ></i>
                </span>
                <!-- 原来的「追更」蓝底徽标已去掉（它只是个展示标签、点了没反应，
                     与番号墙那面也不统一）。右上角现在只有这一颗删除键，样式与
                     番号墙一致：悬停/聚焦才显形，触屏常显。点开的是确认弹窗、
                     不是直接删。 -->
                <button
                  type="button"
                  class="scrape-card__del"
                  title="删除这一部（本地文件与网盘那一份）"
                  aria-label="删除"
                  @click.stop="askDelete(item)"
                >
                  <i class="fas fa-trash-can" />
                </button>

                <div v-if="isItemBusy(item)" class="scrape-card__busy" title="正在刮削">
                  <BusySpinner :size="22" color="#fff" />
                  <span>刮削中</span>
                </div>

                <div class="scrape-card__shade" aria-hidden="true"></div>

                <div class="scrape-card__actions">
                  <button
                    v-if="canConfirmDoubt(item)"
                    type="button"
                    class="scrape-card__act scrape-card__act--ghost"
                    :disabled="running || markingNormalId === item.id || Boolean(rescrapingId)"
                    :title="markingNormalId === item.id ? '处理中…' : '确认当前匹配'"
                    @click.stop="confirmDoubt(item)"
                  >
                    <i class="fas fa-check"></i>
                    <span>{{ markingNormalId === item.id ? "…" : "确认" }}</span>
                  </button>
                  <button
                    v-else-if="canMarkEnded(item)"
                    type="button"
                    class="scrape-card__act scrape-card__act--ghost"
                    :disabled="running || markingNormalId === item.id || Boolean(rescrapingId)"
                    :title="markingNormalId === item.id ? '处理中…' : markActionLabel(item) === '完成' ? '标记完成' : '设为完结'"
                    @click.stop="markEnded(item)"
                  >
                    <i class="fas fa-flag-checkered"></i>
                    <span>{{ markingNormalId === item.id ? "…" : markActionLabel(item) }}</span>
                  </button>
                  <button
                    v-else-if="canRescrape(item)"
                    type="button"
                    class="scrape-card__act scrape-card__act--ghost"
                    :disabled="running || rescrapingId === item.id || Boolean(markingNormalId)"
                    :title="rescrapingId === item.id ? '处理中…' : '重新刮削'"
                    @click.stop="rescrapeItem(item)"
                  >
                    <i class="fas fa-rotate"></i>
                    <span>{{ rescrapingId === item.id ? "…" : "重刮" }}</span>
                  </button>
                  <button
                    type="button"
                    class="scrape-card__act"
                    :disabled="running || Boolean(markingNormalId) || Boolean(rescrapingId)"
                    title="重新匹配"
                    @click.stop="openRematch(item)"
                  >
                    <i class="fas fa-magnifying-glass"></i>
                    <span>匹配</span>
                  </button>
                </div>
              </div>
              <div class="scrape-card__meta">
                <div class="scrape-card__title" :title="item.title">{{ item.title }}</div>
                <div class="scrape-card__sub">
                  <span>{{ item.media_type === "tv" ? "剧集" : "电影" }}</span>
                  <span v-if="item.year && item.tv_state !== 'updating'">· {{ item.year }}</span>
                  <span
                    v-if="episodeProgressText(item)"
                    :class="{ 'scrape-card__ep-gap': item.tv_state === 'updating' }"
                  >
                    · {{ episodeProgressText(item) }}
                  </span>
                </div>
              </div>
            </article>
          </div>
        </div>
        <div class="scrape-wall-foot">
          <span v-if="loadingMore" class="scrape-wall-foot__hint">正在加载更多…</span>
          <span
            v-else-if="hasMore"
            ref="loadMoreSentinelEl"
            class="scrape-wall-foot__hint scrape-wall-foot__hint--more"
          >
            继续下滑加载更多（{{ loadedCount }} / {{ totalMatched }}）
          </span>
          <span v-else-if="totalMatched > 0" class="scrape-wall-foot__hint">已加载全部 {{ totalMatched }} 项</span>
        </div>
      </div>
    </template>

    <AppModal :open="matchOpen" title="重新匹配元数据" size="account" @close="closeRematch">
      <div v-if="matchItem" class="scrape-match">
        <div v-if="rematchSourceDir" class="scrape-match__source">
          <div class="scrape-match__source-label">作品目录</div>
          <div class="scrape-match__source-path" :title="rematchSourceDir">{{ rematchSourceDir }}</div>
        </div>
        <div class="scrape-match__row">
          <div class="scrape-match__type">
            <AppSelect v-model="matchSearchType" :options="matchTypeOptions" />
          </div>
          <div class="scrape-match__query">
            <AppInput
              v-model="matchQuery"
              placeholder="片名或 TMDB ID"
              @keydown.enter.prevent="searchCandidates"
            />
          </div>
          <AppButton type="button" variant="secondary" :disabled="matchSearching" @click="searchCandidates">
            {{ matchSearching ? "搜索中…" : "搜索" }}
          </AppButton>
        </div>
        <div class="scrape-match__grid">
          <button
            v-for="hit in candidates"
            :key="hitKey(hit)"
            type="button"
            class="scrape-match__card"
            :class="{ 'scrape-match__card--active': selectedCandidateKey === hitKey(hit) }"
            @click="selectedCandidateKey = hitKey(hit)"
          >
            <div
              class="scrape-match__card-poster"
              :class="{ 'scrape-match__card-poster--zoomable': Boolean(hitPosterURL(hit)) }"
              :title="hitPosterURL(hit) ? '点击放大' : undefined"
              @click="openPosterPreview(hit, $event)"
            >
              <MediaImage :src="hitPosterURL(hit)" :alt="hitTitle(hit)" />
            </div>
            <div class="scrape-match__card-body">
              <div class="scrape-match__card-title" :title="hitTitle(hit)">{{ hitTitle(hit) }}</div>
              <div class="scrape-match__card-sub">
                <span class="scrape-match__hit-type" :data-type="hitMediaType(hit)">{{ hitTypeLabel(hit) }}</span>
                <span v-if="hitYear(hit)">{{ hitYear(hit) }}</span>
                <span class="scrape-match__card-id">TMDB {{ hitId(hit) }}</span>
              </div>
            </div>
          </button>
          <p v-if="!candidates.length && !matchSearching" class="scrape-match__empty">
            填写片名或 TMDB ID 后点击「搜索」，可凭海报和类型选择
          </p>
        </div>
        <div class="scrape-match__foot">
          <AppButton
            v-if="String(matchItem.tmdb_id || '').trim()"
            class="scrape-match__clear"
            type="button"
            variant="danger"
            :disabled="matchApplying"
            @click="clearMatchAndComplete"
          >
            无匹配
          </AppButton>
          <AppButton
            type="button"
            variant="primary"
            :disabled="!selectedCandidateKey || matchApplying"
            @click="applyMatch"
          >
            {{ matchApplying ? "应用中…" : "应用匹配" }}
          </AppButton>
        </div>
      </div>
    </AppModal>

    <Teleport to="body">
      <div
        v-if="previewPosterURL"
        class="scrape-poster-preview"
        role="dialog"
        aria-modal="true"
        tabindex="-1"
        :aria-label="previewPosterTitle || '海报预览'"
        @click="closePosterPreview"
        @keydown.escape.prevent="closePosterPreview"
      >
        <img
          class="scrape-poster-preview__img"
          :src="previewPosterURL"
          :alt="previewPosterTitle"
          @click.stop
        />
      </div>
    </Teleport>

    <!-- 详情抽屉 + 从它里面发起的播放窗。数据都在 click 那一刻才取（见 useWallDetail）。
         TMDB 那面用**自己的**抽屉（版式与番号那面不同，见 TmdbWallDetailDrawer 的说明）。 -->
    <TmdbWallDetailDrawer
      :open="wall.open.value"
      :loading="wall.loading.value"
      :detail="wall.detail.value"
      :error="wall.error.value"
      @close="wall.close"
      @play="wall.play"
    />
    <WallPlayer
      v-if="wall.playerOpen.value"
      :title="wall.playerTitle.value"
      :file="wall.playerFile.value"
      :subtitles="wall.playerSubtitles.value"
      @close="wall.closePlayer"
    />
  </div>
</template>

<style scoped>
.scrape-panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
  box-shadow: var(--shadow-card);
  overflow: hidden;
}
.scrape-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-soft);
  background: var(--panel-head-bg);
}
@media (max-width: 560px) {
  /* 手机小屏：头部允许换行，避免标题/任务选择器与右侧操作按钮挤压重叠 */
  .scrape-panel__head {
    flex-wrap: wrap;
    gap: 8px 12px;
  }

  .scrape-panel__head-left {
    flex: 1 1 100%;
    min-width: 0;
  }

  .scrape-panel__head-actions {
    margin-left: auto;
  }

  .scrape-panel__task-select {
    min-width: 0;
  }

  .scrape-search-expand--open .scrape-search-expand__input {
    width: min(200px, 42vw);
    max-width: min(200px, 42vw);
  }
}
.scrape-panel__head-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1 1 auto;
}
.scrape-panel__head h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
  white-space: nowrap;
}
.scrape-panel__task-select {
  width: min(200px, 42vw);
  flex: 0 1 200px;
  min-width: 120px;
}
.scrape-panel__scope-button {
  white-space: nowrap;
}
.scrape-panel__head-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.scrape-icon-btn {
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
  flex: 0 0 auto;
}
.scrape-icon-btn:hover,
.scrape-icon-btn--active {
  color: var(--text);
  border-color: var(--brand);
}
.scrape-icon-btn svg {
  width: 16px;
  height: 16px;
}

/* 图标按钮 hover 气泡：替代浏览器原生 title 提示。
   气泡向下展开（面板容器 overflow:hidden，向上冒会被裁切）。 */
.scrape-tip {
  position: relative;
  display: inline-flex;
  flex: 0 0 auto;
}
.scrape-tip__bubble {
  position: absolute;
  top: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  z-index: 40;
  padding: 5px 10px;
  background: #1e293b;
  color: #e2e8f0;
  border-radius: 7px;
  font-size: 12px;
  line-height: 1.4;
  white-space: nowrap;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.2);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.12s ease;
}
.scrape-tip__bubble::after {
  content: "";
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 5px solid transparent;
  border-bottom-color: #1e293b;
}
.scrape-tip:hover .scrape-tip__bubble,
.scrape-tip:focus-within .scrape-tip__bubble {
  opacity: 1;
}
/* 靠右按钮：气泡右对齐，避免超出视口 */
.scrape-tip--right .scrape-tip__bubble {
  left: auto;
  right: 0;
  transform: none;
}
.scrape-tip--right .scrape-tip__bubble::after {
  left: auto;
  right: 8px;
  transform: none;
}
.dark .scrape-tip__bubble {
  background: #e2e8f0;
  color: #1e293b;
}
.dark .scrape-tip__bubble::after {
  border-bottom-color: #e2e8f0;
}
.scrape-search-expand {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.scrape-search-expand__input {
  width: 0;
  max-width: 0;
  height: 36px;
  padding: 0;
  margin: 0;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
  outline: none;
  opacity: 0;
  overflow: hidden;
  pointer-events: none;
  transition:
    width 0.22s ease,
    max-width 0.22s ease,
    padding 0.22s ease,
    border-color 0.22s ease,
    opacity 0.18s ease;
}
.scrape-search-expand--open .scrape-search-expand__input {
  width: 200px;
  max-width: 200px;
  padding: 0 12px;
  border-color: var(--border);
  opacity: 1;
  pointer-events: auto;
}
.scrape-search-expand--open .scrape-search-expand__input:focus {
  border-color: var(--brand);
}
.scrape-naming-tip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: min(240px, 40vw);
  padding: 4px 2px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--accent-text);
  font-size: 12px;
  line-height: 1.35;
  cursor: pointer;
  text-align: left;
}
.scrape-naming-tip:hover,
.scrape-naming-tip--open {
  color: var(--brand-strong);
}
.scrape-naming-tip__mark {
  display: inline-grid;
  place-items: center;
  flex: 0 0 auto;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: var(--brand);
  color: var(--text-on-brand);
  font-size: 10px;
  font-weight: 700;
  font-style: italic;
  line-height: 1;
}
.scrape-naming-tip__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.scrape-naming-panel {
  padding: 12px 14px;
  max-width: 360px;
}
.scrape-naming-panel__title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
  margin-bottom: 6px;
}
.scrape-naming-panel__subtitle {
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
  margin: 10px 0 6px;
}
.scrape-naming-panel__lead {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-muted);
}
.scrape-naming-panel__list {
  margin: 0;
  padding-left: 1.1em;
  font-size: 12px;
  line-height: 1.65;
  color: var(--text);
}
.scrape-naming-panel__list code {
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--border-soft);
  font-size: 11px;
}
.scrape-progress {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  background: var(--tab-active-bg);
  color: var(--accent-text);
  font-size: 13px;
  border-bottom: 1px solid var(--tab-active-border);
}
.scrape-progress__msg {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.scrape-progress__nums {
  font-weight: 700;
  white-space: nowrap;
}
.scrape-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border-soft);
  flex-wrap: wrap;
}
.scrape-toolbar__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
  min-width: 0;
}
.scrape-toolbar__count {
  color: var(--text-muted);
  font-size: 12px;
  white-space: nowrap;
}
.scrape-filters {
  display: flex;
  gap: 2px;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  flex: 1 1 auto;
}
.scrape-filter {
  position: relative;
  padding: 6px 11px;
  border: none;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  background: transparent;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  transition: color 0.15s ease, background 0.15s ease;
}
.scrape-filter--inline {
  padding: 6px 5px;
}
.scrape-filter:hover {
  color: var(--text);
  background: var(--surface-hover);
}
.scrape-filter--active {
  color: var(--brand);
  font-weight: 700;
  background: transparent;
}
.scrape-filter--active::after {
  content: "";
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: 1px;
  height: 2px;
  border-radius: 1px;
  background: var(--brand-gradient-h);
}
.scrape-filter--inline.scrape-filter--active::after {
  left: 2px;
  right: 2px;
}
.scrape-filter--active:hover {
  color: var(--brand-strong);
  background: var(--accent-soft);
}
.scrape-type-tv {
  display: inline-flex;
  align-items: center;
  gap: 0;
  padding: 0 2px;
  border-radius: var(--radius-sm);
}
.scrape-type-tv--on {
  background: color-mix(in srgb, var(--brand) 6%, transparent);
}
.scrape-type-tv__mark {
  color: var(--text-muted);
  font-size: 13px;
  user-select: none;
  line-height: 1;
}
.scrape-type-tv--on .scrape-type-tv__mark {
  color: var(--brand);
}
.scrape-type-tv .scrape-filter--active {
  background: transparent;
}
.scrape-type-tv .scrape-filter--active:hover {
  background: transparent;
}
.scrape-toolbar__sep {
  width: 1px;
  height: 18px;
  background: var(--border);
  margin: 0 2px;
}
.scrape-sort-menu {
  min-width: 180px;
  padding: 4px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-pop);
}
.scrape-sort-menu__item {
  display: block;
  width: 100%;
  text-align: left;
  padding: 8px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text);
  cursor: pointer;
  font-size: 13px;
}
.scrape-sort-menu__item:hover {
  background: var(--border-soft);
}
.scrape-sort-menu__item--active {
  color: var(--brand);
  font-weight: 600;
  background: var(--accent-soft);
}
.scrape-wall-root {
  width: 100%;
}
.scrape-wall-foot {
  display: flex;
  justify-content: center;
  padding: 12px 16px 16px;
}
.scrape-wall-foot__hint {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}
.scrape-wall-foot__hint--more {
  min-height: 24px;
}
.scrape-wall-phantom {
  position: relative;
  width: 100%;
}
.scrape-wall {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 14px;
  padding: 0 16px;
  box-sizing: border-box;
  will-change: transform;
}
/* 整张卡片可点（开详情）。cursor 与 hover 抬起是「这里能点」的信号 ——
   没有它，用户只会去点那排刮削按钮，永远发现不了详情。 */
.scrape-card {
  cursor: pointer;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid var(--border);
  background: var(--surface);
}
.scrape-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-pop);
}

.scrape-card:focus-visible {
  outline: 2px solid var(--brand);
  outline-offset: 2px;
}

.scrape-card__poster {
  position: relative;
  aspect-ratio: 2 / 3;
  background: var(--surface-sunken);
  overflow: hidden;
}
.scrape-card__poster img,
.scrape-card__poster .media-ph {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.scrape-card__mark {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 2;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 11px;
  box-shadow: 0 4px 10px rgba(15, 23, 42, 0.2);
}
.scrape-card__mark--ok {
  background: var(--success);
}
.scrape-card__mark--miss {
  background: var(--text-muted);
}
.scrape-card__mark--doubt {
  background: var(--warning);
}
/* 右上角的删除键。**与番号墙（`StrmJavWall.vue` 的 `.jav-card__del`）逐条对齐**：
   位置（top/right 6px）、尺寸 28、圆形、半透明黑底、悬停/聚焦才显形、触屏常显。
   两面临时各写一份（这里用的是 `.scrape-card` 前缀，样式在本组件的 scoped 块里），
   改一边时另一边要跟着改 —— 这是「两面墙的操作键长得一样」的必要代价。 */
.scrape-card__del {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 3;
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 50%;
  background: rgba(15, 23, 42, 0.55);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s, background 0.12s;
}
.scrape-card:hover .scrape-card__del,
.scrape-card:focus-within .scrape-card__del { opacity: 1; }
@media (hover: none) { .scrape-card__del { opacity: 1; } }
.scrape-card__del:hover { background: var(--danger); }
.scrape-card__del:focus-visible { opacity: 1; outline: 2px solid var(--danger); outline-offset: 1px; }
.scrape-card__busy {
  position: absolute;
  inset: 0;
  z-index: 4;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #fff;
  background: rgba(15, 23, 42, 0.62);
  font-size: 12px;
  font-weight: 700;
  pointer-events: none;
}
.scrape-card__shade {
  position: absolute;
  inset: 0;
  z-index: 1;
  background: linear-gradient(180deg, transparent 45%, rgba(8, 12, 24, 0.72) 100%);
  opacity: 0;
  transition: opacity 0.18s ease;
  pointer-events: none;
}
.scrape-card:hover .scrape-card__shade {
  opacity: 1;
}
.scrape-card--busy:hover .scrape-card__shade {
  opacity: 0;
}
.scrape-card__actions {
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: 8px;
  z-index: 3;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
  opacity: 0;
  transform: translateY(6px);
  transition: opacity 0.18s ease, transform 0.18s ease;
  pointer-events: none;
}
.scrape-card:hover .scrape-card__actions {
  opacity: 1;
  transform: none;
  pointer-events: auto;
}
.scrape-card--busy:hover .scrape-card__actions {
  opacity: 0;
  pointer-events: none;
}
.scrape-card__act {
  height: 32px;
  border: none;
  border-radius: 9px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  padding: 0 6px;
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
  background: rgba(255, 255, 255, 0.94);
  backdrop-filter: blur(10px);
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.18);
  transition: background 0.15s ease, transform 0.15s ease;
}
.scrape-card__act i {
  font-size: 11px;
  opacity: 0.78;
}
.scrape-card__act:hover:not(:disabled) {
  background: #fff;
  transform: translateY(-1px);
}
.scrape-card__act:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.scrape-card__act--ghost {
  color: #fff;
  background: rgba(255, 255, 255, 0.14);
  border: 1px solid rgba(255, 255, 255, 0.22);
  box-shadow: none;
}
.scrape-card__act--ghost:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.24);
}
.scrape-card__actions .scrape-card__act:only-child {
  grid-column: 1 / -1;
}
.scrape-card__meta {
  padding: 10px 10px 12px;
  background: var(--surface);
}
.scrape-card__title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.scrape-card__sub {
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.scrape-card__ep-gap {
  color: var(--warning);
  font-weight: 600;
}
.scrape-match {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.scrape-match__source {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  background: linear-gradient(
    180deg,
    var(--tab-active-bg) 0%,
    color-mix(in srgb, var(--surface) 88%, var(--brand) 12%) 100%
  );
}
.scrape-match__source-label {
  font-size: 12px;
  font-weight: 700;
  color: var(--text-muted);
  letter-spacing: 0.02em;
}
.scrape-match__source-path {
  font-size: 13px;
  line-height: 1.55;
  color: var(--text);
  word-break: break-all;
}
.scrape-match__row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.scrape-match__type {
  width: 96px;
  flex: 0 0 96px;
}
.scrape-match__query {
  flex: 1 1 auto;
  min-width: 0;
}
.scrape-match__grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  max-height: calc(3 * 116px + 2 * 10px);
  overflow: auto;
  padding: 2px;
}
.scrape-match__card {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 10px;
  text-align: left;
  padding: 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
  height: 116px;
  box-sizing: border-box;
}
.scrape-match__card:hover {
  border-color: var(--tab-active-border);
}
.scrape-match__card--active {
  border-color: var(--brand);
  background: var(--tab-active-bg);
  box-shadow: none;
}
.scrape-match__card-poster {
  width: 66px;
  flex: 0 0 66px;
  height: 99px;
  border-radius: 5px;
  background: var(--surface-sunken);
  overflow: hidden;
}
.scrape-match__card-poster--zoomable {
  cursor: zoom-in;
}
.scrape-match__card-poster--zoomable:hover {
  outline: 2px solid var(--brand);
  outline-offset: 1px;
}
.scrape-match__card-poster img,
.scrape-match__card-poster .media-ph {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.scrape-match__card-body {
  padding: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
  min-width: 0;
  min-height: 0;
  flex: 1 1 auto;
}
.scrape-match__card-title {
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.2;
}
.scrape-match__card-sub {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 11px;
  color: var(--text-muted);
  min-width: 0;
}
.scrape-match__card-id {
  font-variant-numeric: tabular-nums;
  color: var(--text-muted);
}
.scrape-match__hit-type {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
  background: var(--surface-sunken);
  color: var(--text-regular);
}
.scrape-match__hit-type[data-type="tv"] {
  background: color-mix(in srgb, var(--brand) 18%, var(--surface));
  color: var(--brand-strong);
}
.scrape-match__hit-type[data-type="movie"] {
  background: color-mix(in srgb, var(--warning) 22%, var(--surface));
  color: color-mix(in srgb, var(--warning) 72%, var(--text));
}
.scrape-match__empty {
  grid-column: 1 / -1;
  margin: 16px 0;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.scrape-match__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.scrape-match__clear {
  margin-right: auto;
}
@media (max-width: 560px) {
  .scrape-match__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    max-height: calc(4 * 116px + 3 * 10px);
  }
  .scrape-match__type {
    width: 84px;
    flex-basis: 84px;
  }
}
.scrape-poster-preview {
  position: fixed;
  inset: 0;
  z-index: calc(var(--z-modal) + 20);
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(15, 23, 42, 0.72);
  cursor: zoom-out;
}
.scrape-poster-preview__img {
  max-width: min(420px, 92vw);
  max-height: 88vh;
  width: auto;
  height: auto;
  object-fit: contain;
  border-radius: 10px;
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.45);
  cursor: default;
}
.scrape-menu { display: flex; flex-direction: column; min-width: 220px; padding: 6px; }
.scrape-menu__item { display: flex; flex-direction: column; gap: 2px; align-items: flex-start; padding: 8px 10px; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text); font-size: 13px; text-align: left; cursor: pointer; }
.scrape-menu__item:hover { background: var(--surface-sunken); }
/* 刮削进行中时「补齐」那颗要看起来是禁用的（它与刮削共用同一条后台队列）。 */
.scrape-menu__item:disabled { opacity: 0.5; cursor: not-allowed; }
.scrape-menu__item:disabled:hover { background: transparent; }
.scrape-menu__hint { font-size: 11px; color: var(--text-muted); }
</style>
