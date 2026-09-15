<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  searchMediaOrganizeTmdb,
  type MediaOrganizeTmdbSearchHit,
} from "@/api/mediaOrganize";
import { fetchStrmTasks, type StrmTask } from "@/api/strm";
import {
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
} from "@/api/strmScrape";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import StrmScrapeScopePicker from "@/components/admin/StrmScrapeScopePicker.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppIconButton from "@/components/base/AppIconButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useConditionalPolling } from "@/composables/useConditionalPolling";
import { confirm } from "@/composables/useConfirm";
import { useVirtualPosterWall } from "@/composables/useVirtualPosterWall";
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
const selectedTaskId = ref<number | null>(null);
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

const taskOptions = computed(() =>
  tasks.value.map((t) => ({
    value: String(t.id),
    label: t.name || `任务 #${t.id}`,
  })),
);
const selectedTask = computed(() =>
  tasks.value.find((t) => Number(t.id) === Number(selectedTaskId.value)) || null,
);
const scopeLabel = computed(() =>
  excludedScopeDirs.value.length ? `已排除 ${excludedScopeDirs.value.length} 个目录` : "全部目录",
);

async function loadScope() {
  const taskId = selectedTaskId.value;
  excludedScopeDirs.value = [];
  if (!taskId) return;
  try {
    const data = await fetchStrmScrapeScope(taskId);
    if (selectedTaskId.value === taskId) excludedScopeDirs.value = data.excluded_dirs ?? [];
  } catch (e) {
    toast.error(getApiErrorMessage(e, "读取刮削范围失败"));
  }
}

async function saveScope(dirs: string[]) {
  const taskId = selectedTaskId.value;
  if (!taskId || scopeLoading.value) return;
  scopeLoading.value = true;
  try {
    const data = await saveStrmScrapeScope(taskId, dirs);
    excludedScopeDirs.value = data.excluded_dirs ?? [];
    scopeOpen.value = false;
    await loadItems();
    toast.success("刮削范围已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存刮削范围失败"));
  } finally {
    scopeLoading.value = false;
  }
}

const sortOptions: { value: SortKey; label: string }[] = [
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
const namingTipOpen = ref(false);
const searchOpen = ref(false);
const searchInputEl = ref<HTMLInputElement | null>(null);
const currentSortLabel = computed(
  () => sortOptions.find((o) => o.value === sortKey.value)?.label ?? "排序",
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
    task_id: selectedTaskId.value ?? 0,
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

function applySort(key: SortKey) {
  sortKey.value = key;
  saveSortKey(key);
  sortMenuOpen.value = false;
}

async function toggleSearch() {
  searchOpen.value = !searchOpen.value;
  if (searchOpen.value) {
    await nextTick();
    window.setTimeout(() => searchInputEl.value?.focus(), 220);
  } else {
    keyword.value = "";
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
  taskId: number,
  query: StrmScrapeItemListQuery,
  refreshIndex = false,
) {
  return refreshIndex ? refreshStrmScrapeIndex(taskId, query) : fetchStrmScrapeItems(taskId, query);
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
  const taskId = selectedTaskId.value;
  if (!taskId) {
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
    const data = await requestItems(taskId, buildListQuery(offset, limit), Boolean(opts?.refreshIndex));
    if (seq !== loadItemsSeq || selectedTaskId.value !== taskId) return false;
    applyListResult(data, append);
    await nextTick();
    if (!append) {
      resetWallScroll();
    }
    measureWall();
    setupLoadMoreObserver();
    return true;
  } catch (e) {
    if (seq !== loadItemsSeq || selectedTaskId.value !== taskId) return false;
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
  if (!selectedTaskId.value && tasks.value.length) {
    selectedTaskId.value = Number(tasks.value[0].id);
  } else if (selectedTaskId.value && !tasks.value.some((t) => Number(t.id) === Number(selectedTaskId.value))) {
    selectedTaskId.value = tasks.value.length ? Number(tasks.value[0].id) : null;
  }
}

async function syncProgress() {
  try {
    const p = await fetchStrmScrapeProgress();
    const previous = progress.value;
    const wasRunning = previous?.running;
    const sameTask = Number(p.strm_task_id) === Number(selectedTaskId.value);
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
  if (!selectedTaskId.value) {
    toast.error("请先选择 STRM 任务");
    return;
  }
  if (running.value) return;
  try {
    const p = await runStrmScrape(selectedTaskId.value);
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

async function refreshAll() {
  await loadTasks();
  if (!selectedTaskId.value) {
    clearItemList();
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
  if (Number(progress.value?.strm_task_id) !== Number(selectedTaskId.value)) return false;
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
  if (!matchItem.value || !selectedTaskId.value || !selectedCandidateKey.value) return;
  const hit = candidates.value.find((c) => hitKey(c) === selectedCandidateKey.value);
  if (!hit) return;
  matchApplying.value = true;
  try {
    const year = hitYear(hit);
    const result = await rematchStrmScrapeItem({
      strm_task_id: selectedTaskId.value,
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
  if (!item || !selectedTaskId.value || matchApplying.value) return;
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
      strm_task_id: selectedTaskId.value,
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
  if (!selectedTaskId.value || !canMarkEnded(item)) return;
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
  if (!selectedTaskId.value || !canConfirmDoubt(item)) return;
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
  if (!selectedTaskId.value) return;
  const updated = await markStrmScrapeNormal({
    strm_task_id: selectedTaskId.value,
    item_id: item.id,
    media_type: item.media_type,
  });
  if (!replaceItem(updated)) {
    await loadItems({ silent: true, preserveLoaded: true });
  }
}

async function rescrapeItem(item: StrmScrapeItem) {
  if (!selectedTaskId.value || !canRescrape(item) || running.value) return;
  rescrapingId.value = item.id;
  try {
    const result = await rescrapeStrmScrapeItem({
      strm_task_id: selectedTaskId.value,
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

watch(selectedTaskId, () => {
  void loadScope();
  if (!booted.value) return;
  filter.value = "all";
  typeFilter.value = "all";
  tvSubFilter.value = "all";
  keyword.value = "";
});

watch(currentListQueryKey, () => {
  if (!booted.value) return;
  if (!selectedTaskId.value) {
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
    if (selectedTaskId.value) {
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

defineExpose({
  startScrape,
  stopScrape,
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
        <div v-if="taskOptions.length" class="scrape-panel__task-select">
          <AppSelect
            :model-value="selectedTaskId != null ? String(selectedTaskId) : ''"
            :options="taskOptions"
            @update:model-value="(v) => (selectedTaskId = Number(v) || null)"
          />
        </div>
        <AppButton
          v-if="selectedTaskId"
          variant="secondary"
          size="md"
          class="scrape-panel__scope-button"
          :disabled="running || scopeLoading"
          @click="scopeOpen = true"
        >
          {{ scopeLabel }}
        </AppButton>
      </div>
      <div class="scrape-panel__head-actions">
        <div class="scrape-search-expand" :class="{ 'scrape-search-expand--open': searchOpen }">
          <input
            ref="searchInputEl"
            v-model="keyword"
            class="scrape-search-expand__input"
            type="search"
            placeholder="搜索片名"
            :tabindex="searchOpen ? 0 : -1"
            @keydown.escape.prevent="toggleSearch"
          />
          <span class="scrape-tip">
            <button
              type="button"
              class="scrape-icon-btn"
              :class="{ 'scrape-icon-btn--active': searchOpen || Boolean(keyword) }"
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
                :class="{ 'scrape-sort-menu__item--active': sortKey === opt.value }"
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
          <span class="scrape-tip__bubble">重建刮削索引（扫描本地 STRM 目录）</span>
        </span>
        <span class="scrape-tip scrape-tip--right">
          <AppIconButton
            icon="settings"
            label="STRM 刮削设置"
            variant="secondary"
            size="md"
            no-native-title
            @click="emit('open-settings')"
          />
          <span class="scrape-tip__bubble">STRM 刮削设置</span>
        </span>
      </div>
    </div>

    <StrmScrapeScopePicker
      v-if="selectedTaskId"
      :open="scopeOpen"
      :task-id="selectedTaskId"
      :task-name="selectedTask?.name"
      :excluded-dirs="excludedScopeDirs"
      @close="scopeOpen = false"
      @save="saveScope"
    />

    <div v-if="running && progress" class="scrape-progress">
      <span class="scrape-progress__msg">{{ progress.message || "刮削进行中…" }}</span>
      <span class="scrape-progress__nums">{{ progress.done }}/{{ progress.total }}</span>
    </div>

    <AdminEmptyState
      v-if="!loading && !tasks.length"
      icon="🎬"
      title="还没有 STRM 任务"
      description="请先在「STRM 任务」里创建任务，再回来刮削其输出目录。"
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
            >
              <div class="scrape-card__poster">
                <img
                  v-if="item.poster_url"
                  :src="item.poster_url"
                  :alt="item.title"
                  loading="lazy"
                  decoding="async"
                />
                <div v-else class="scrape-card__placeholder">{{ item.title.slice(0, 1) }}</div>

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
                <span
                  v-if="item.tv_state === 'updating'"
                  class="scrape-card__updating"
                  title="追更中"
                >
                  <i class="fas fa-bolt"></i>
                  追更
                </span>

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
                    @click="confirmDoubt(item)"
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
                    @click="markEnded(item)"
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
                    @click="rescrapeItem(item)"
                  >
                    <i class="fas fa-rotate"></i>
                    <span>{{ rescrapingId === item.id ? "…" : "重刮" }}</span>
                  </button>
                  <button
                    type="button"
                    class="scrape-card__act"
                    :disabled="running || Boolean(markingNormalId) || Boolean(rescrapingId)"
                    title="重新匹配"
                    @click="openRematch(item)"
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
              <img v-if="hitPosterURL(hit)" :src="hitPosterURL(hit)" :alt="hitTitle(hit)" loading="lazy" />
              <span v-else class="scrape-match__card-ph">无图</span>
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
.scrape-card {
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid var(--border);
  background: var(--surface);
}
.scrape-card__poster {
  position: relative;
  aspect-ratio: 2 / 3;
  background: var(--surface-sunken);
  overflow: hidden;
}
.scrape-card__poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.scrape-card__placeholder {
  width: 100%;
  height: 100%;
  display: grid;
  place-items: center;
  font-size: 42px;
  font-weight: 800;
  background: linear-gradient(135deg, color-mix(in srgb, var(--text) 88%, transparent), var(--text-muted));
  color: var(--text-on-brand);
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
.scrape-card__updating {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 2;
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
  background: color-mix(in srgb, #0ea5e9 92%, transparent);
  backdrop-filter: blur(6px);
  box-shadow: 0 4px 12px rgba(14, 165, 233, 0.28);
}
.scrape-card__updating i {
  font-size: 10px;
}
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
.scrape-match__card-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.scrape-match__card-ph {
  width: 100%;
  height: 100%;
  display: grid;
  place-items: center;
  font-size: 10px;
  color: var(--text-muted);
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
</style>
