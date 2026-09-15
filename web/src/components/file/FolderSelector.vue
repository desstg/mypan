<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { filesApi } from "@/api/files";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import { FileNameError, validateFileName } from "@/utils/fileName";
import type { BrowserFavoriteItem, FileItem } from "@/api/types";
import type { Crumb } from "@/stores/browser";
import type { SortKey, SortOrder } from "@/types/file-browser";
import { naturalSort } from "@/utils/naturalSort";
import { formatTime } from "@/utils/format";
import BreadcrumbNav from "./BreadcrumbNav.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import AppButton from "@/components/base/AppButton.vue";
import "@/styles/file-list.css";

type FolderSortKey = Extract<SortKey, "name" | "modified">;
type FolderLoader = (parentId: string, opts?: { forceRefresh?: boolean }) => Promise<FileItem[]>;
export type FolderSelection = {
  id: string;
  name: string;
  path?: string;
  ancestorIds?: string[];
};

const ROOT: Crumb = { id: "", name: "根目录" };

const props = withDefaults(
  defineProps<{
    // 网盘浏览必填；提供 loader 时可为 0（仅用于 resolve 回传）。
    accountId?: number;
    // 自定义目录加载（如 STRM 任务本地目录）；提供后不再走 filesApi。
    loader?: FolderLoader;
    title?: string;
    confirmText?: string;
    // 不可作为目标/不可进入的目录（如正在移动的文件夹自身）。
    excludedFolderIds?: string[];
    showRefresh?: boolean;
    // 允许内联新建文件夹（移动/复制场景为 true）；loader 模式下忽略。
    allowCreateFolder?: boolean;
    // 初始定位路径（含 ID 的面包屑）；缺省从根目录开始。
    initialBreadcrumb?: Crumb[];
    // 按路径逐级定位（目录不存在时回退根目录）。
    initialPath?: string;
    // 锁定浏览根为任务目录，不可回到更上层。
    rootAnchor?: { parentId: string; path: string; label?: string };
    // 同层多选子目录。
    multiSelect?: boolean;
    // 多选值仍表示被选中的目录，但复选框反向显示：未勾选代表命中该值。
    inverseSelectionDisplay?: boolean;
    // 受控已选目录；传入后跨目录浏览保留，并由父组件汇总。
    selectedItems?: FolderSelection[];
    // 是否显示右上角关闭按钮（嵌套在自带关闭栏的容器里时可关）。
    showClose?: boolean;
    // 是否显示当前目录筛选搜索框。
    showSearch?: boolean;
    // 由外层弹窗提供搜索框时，用该值控制当前目录筛选。
    searchValue?: string;
    // 当前账号收藏夹，用于目录选择器内快速跳转。
    favorites?: BrowserFavoriteItem[];
    favoritesLoading?: boolean;
  }>(),
  {
    accountId: 0,
    title: "选择目录",
    confirmText: "选择当前目录",
    excludedFolderIds: () => [],
    showRefresh: true,
    allowCreateFolder: false,
    initialBreadcrumb: () => [],
    initialPath: "",
    rootAnchor: undefined,
    multiSelect: false,
    inverseSelectionDisplay: false,
    selectedItems: undefined,
    showClose: true,
    showSearch: true,
    searchValue: undefined,
    favorites: undefined,
    favoritesLoading: false,
  },
);

const emit = defineEmits<{
  resolve: [payload: {
    accountId: number;
    parentId: string;
    path: string;
    selections?: FolderSelection[];
  }];
  cancel: [];
  "update:selectedItems": [items: FolderSelection[]];
  "update:searchValue": [value: string];
}>();

const loading = ref(false);
const error = ref("");
const dirs = ref<FileItem[]>([]);
const breadcrumb = ref<Crumb[]>([ROOT]);
const filterKeyword = ref("");
const sortKey = ref<FolderSortKey>("name");
const sortOrder = ref<SortOrder>("asc");
const creating = ref(false);
const showCreateInput = ref(false);
const newFolderName = ref("");
const createInputRef = ref<HTMLInputElement | null>(null);
const selectedMap = ref<Record<string, FolderSelection>>({});
const favoriteMenuOpen = ref(false);
const favoritePage = ref(0);
const FAVORITE_PAGE_SIZE = 12;

const columns: { key: FolderSortKey; label: string }[] = [
  { key: "name", label: "名称" },
  { key: "modified", label: "修改时间" },
];

function formatFolderTimeShort(value?: string) {
  const full = formatTime(value);
  const matched = full.match(/^\d{4}-(\d{2})-(\d{2}) (\d{2}):(\d{2})/);
  return matched ? `${matched[1]}/${matched[2]} ${matched[3]}:${matched[4]}` : full;
}

const currentParentId = computed(() => breadcrumb.value[breadcrumb.value.length - 1]?.id ?? "");
const currentPath = computed(() => {
  if (props.rootAnchor) {
    const base = props.rootAnchor.path.replace(/\/+$/, "") || "/";
    const extra = breadcrumb.value.slice(1).map((c) => c.name);
    if (!extra.length) return base;
    return `${base}/${extra.join("/")}`;
  }
  const names = breadcrumb.value.slice(1).map((c) => c.name);
  return names.length ? `/${names.join("/")}` : "/";
});

function anchorLabel(anchor: { path: string; label?: string }) {
  if (anchor.label?.trim()) return anchor.label.trim();
  const segs = anchor.path.split("/").filter(Boolean);
  return segs[segs.length - 1] || "任务目录";
}

const controlled = computed(() => props.selectedItems !== undefined);
const activeSelections = computed(() =>
  controlled.value ? (props.selectedItems ?? []) : Object.values(selectedMap.value),
);
const favoriteItems = computed(() => props.favorites ?? []);
const showFavoriteEntry = computed(() => props.favorites !== undefined);
const favoriteTriggerDisabled = computed(() =>
  loading.value || props.favoritesLoading || favoriteItems.value.length === 0,
);
const favoritePageCount = computed(() =>
  Math.max(1, Math.ceil(favoriteItems.value.length / FAVORITE_PAGE_SIZE)),
);
const pagedFavoriteItems = computed(() => {
  const start = favoritePage.value * FAVORITE_PAGE_SIZE;
  return favoriteItems.value.slice(start, start + FAVORITE_PAGE_SIZE);
});
const canPrevFavoritePage = computed(() => favoritePage.value > 0);
const canNextFavoritePage = computed(() => favoritePage.value < favoritePageCount.value - 1);
const favoriteTriggerTitle = computed(() => {
  if (props.favoritesLoading) return "正在加载收藏夹";
  if (!favoriteItems.value.length) return "当前账号暂无收藏夹";
  return favoriteMenuOpen.value ? "收起收藏夹快捷进入" : "从收藏夹快速进入";
});
const canCreateFolder = computed(() => props.allowCreateFolder && !props.loader);
const selectedCount = computed(() => activeSelections.value.length);
const primaryActionText = computed(() => {
  if (!props.multiSelect) return props.confirmText;
  if (controlled.value) {
    return selectedCount.value > 0 ? `${props.confirmText}（${selectedCount.value}）` : "选择当前目录";
  }
  if (selectedCount.value > 0) return `添加所选 (${selectedCount.value})`;
  return props.confirmText;
});
const tableClass = computed(() => ({
  "folder-selector__table--multi": props.multiSelect,
}));

type SelectState = "none" | "checked" | "partial" | "covered";

function clearSelection() {
  if (controlled.value) return;
  selectedMap.value = {};
}

function closeFavoriteMenu() {
  favoriteMenuOpen.value = false;
}

function toggleFavoriteMenu() {
  if (favoriteTriggerDisabled.value) return;
  if (!favoriteMenuOpen.value) favoritePage.value = 0;
  favoriteMenuOpen.value = !favoriteMenuOpen.value;
}

function prevFavoritePage() {
  if (!canPrevFavoritePage.value) return;
  favoritePage.value -= 1;
}

function nextFavoritePage() {
  if (!canNextFavoritePage.value) return;
  favoritePage.value += 1;
}

function formatFavoritePath(item: BrowserFavoriteItem) {
  const names = item.crumbs
    .map((crumb) => crumb.name)
    .filter((name) => name && name !== "根目录");
  return names.length ? `/${names.join("/")}` : "/";
}

function isFavoriteCurrent(item: BrowserFavoriteItem) {
  const ids = item.crumbs.map((crumb) => String(crumb.id || ""));
  return (
    ids.length === breadcrumb.value.length
    && ids.every((id, index) => id === String(breadcrumb.value[index]?.id || ""))
  );
}

function cloneBreadcrumb(items: Crumb[]) {
  return items.map((item) => ({ id: String(item.id || ""), name: item.name }));
}

function normalizeSelectPath(id: string) {
  return String(id || "").replace(/^\/+|\/+$/g, "");
}

function normalizedSelectionPath(path?: string) {
  return `/${String(path || "").split("/").filter(Boolean).join("/")}`;
}

const currentAncestorIds = computed(() =>
  breadcrumb.value.map((item) => String(item.id || "")).filter(Boolean),
);

function selectionForDir(dir: FileItem): FolderSelection {
  const base = currentPath.value === "/" ? "" : currentPath.value;
  return {
    id: String(dir.id),
    name: dir.name || String(dir.id),
    path: normalizedSelectionPath(`${base}/${dir.name || ""}`),
    ancestorIds: [...currentAncestorIds.value],
  };
}

function isAncestorSelection(ancestor: FolderSelection, child: FolderSelection) {
  if (child.ancestorIds?.some((id) => String(id) === String(ancestor.id))) return true;
  const ancestorPath = normalizedSelectionPath(ancestor.path);
  const childPath = normalizedSelectionPath(child.path);
  return ancestorPath !== "/"
    && childPath !== "/"
    && ancestorPath !== childPath
    && childPath.startsWith(`${ancestorPath}/`);
}

function selectionState(dir: FileItem): SelectState {
  const item = selectionForDir(dir);
  const items = activeSelections.value;
  if (items.some((selected) => String(selected.id) === item.id)) return "checked";
  if (items.some((selected) => isAncestorSelection(selected, item))) return "covered";
  if (items.some((selected) => isAncestorSelection(item, selected))) return "partial";
  return "none";
}

function isSelected(dir: FileItem) {
  const state = selectionState(dir);
  return state === "checked" || state === "covered";
}

function isPartialSelected(dir: FileItem) {
  return selectionState(dir) === "partial";
}

function isDisplayChecked(dir: FileItem) {
  return props.inverseSelectionDisplay ? selectionState(dir) === "none" : isSelected(dir);
}

function isDisplayPartial(dir: FileItem) {
  return isPartialSelected(dir);
}

function toggleDisplaySelect(dir: FileItem) {
  if (!props.inverseSelectionDisplay) {
    toggleSelect(dir, !isSelected(dir));
    return;
  }
  const item = selectionForDir(dir);
  const state = selectionState(dir);
  if (state === "covered") return;
  if (state === "none") {
    commitSelections(withHierarchyExclusive(activeSelections.value, item, true));
    return;
  }
  const next = activeSelections.value.filter((selected) => {
    if (String(selected.id) === item.id) return false;
    return !isAncestorSelection(item, selected);
  });
  commitSelections(next);
}

function selectionKey(item: FolderSelection) {
  const id = normalizeSelectPath(item.id);
  if (id) return id;
  return normalizedSelectionPath(item.path);
}

function commitSelections(next: FolderSelection[]) {
  if (controlled.value) {
    emit("update:selectedItems", next);
    return;
  }
  const map: Record<string, FolderSelection> = {};
  for (const item of next) map[selectionKey(item)] = item;
  selectedMap.value = map;
}

function withHierarchyExclusive(current: FolderSelection[], item: FolderSelection, selected: boolean) {
  const next = current.filter((entry) => {
    if (String(entry.id) === String(item.id)) return false;
    if (isAncestorSelection(entry, item) || isAncestorSelection(item, entry)) return false;
    return true;
  });
  if (selected) next.push(item);
  return next;
}

function toggleSelect(dir: FileItem, checked?: boolean) {
  if (!props.multiSelect) return;
  const state = selectionState(dir);
  if (state === "covered") return;
  const item = selectionForDir(dir);
  if (state === "partial") {
    commitSelections(withHierarchyExclusive(activeSelections.value, item, true));
    return;
  }
  const selected = checked ?? state !== "checked";
  commitSelections(withHierarchyExclusive(activeSelections.value, item, selected));
}

async function listDirs(parentId: string, opts?: { forceRefresh?: boolean }): Promise<FileItem[]> {
  if (props.loader) {
    return props.loader(parentId, opts);
  }
  const res = await filesApi.list(props.accountId, parentId, opts);
  return res.items.filter((it) => it.is_dir);
}

async function resolveInitialBreadcrumb(): Promise<Crumb[]> {
  if (props.rootAnchor) {
    return [{ id: props.rootAnchor.parentId, name: anchorLabel(props.rootAnchor) }];
  }
  const init = props.initialBreadcrumb;
  if (init.length && init[0]?.id === "") return [...init];

  const segments = (props.initialPath || "")
    .split("/")
    .map((item) => item.trim())
    .filter(Boolean);
  if (!segments.length) return [ROOT];

  let parentId = "";
  const crumbs: Crumb[] = [ROOT];
  try {
    for (const segment of segments) {
      const items = await listDirs(parentId);
      const matched = items.find(
        (item) => item.is_dir && String(item.name || "").trim() === segment,
      );
      if (!matched) throw new Error(`目录不存在: ${segment}`);
      parentId = String(matched.id);
      crumbs.push({ id: parentId, name: String(matched.name || segment) });
    }
    return crumbs;
  } catch {
    toast.info("已保存目录不存在或无法定位，已打开根目录");
    return [ROOT];
  }
}

async function resetAndLoad() {
  closeFavoriteMenu();
  breadcrumb.value = await resolveInitialBreadcrumb();
  filterKeyword.value = "";
  resetCreateState();
  clearSelection();
  await load(currentParentId.value);
}

const excludedSet = computed(() => new Set(props.excludedFolderIds.map(String)));

const visibleDirs = computed(() =>
  dirs.value.filter((d) => d.is_dir && !excludedSet.value.has(String(d.id))),
);

const filteredDirs = computed(() => {
  const kw = filterKeyword.value.trim().toLowerCase();
  if (!kw) return visibleDirs.value;
  return visibleDirs.value.filter((d) => (d.name || "").toLowerCase().includes(kw));
});

const sortedDirs = computed(() => {
  const order = sortOrder.value === "desc" ? -1 : 1;
  return [...filteredDirs.value].sort((a, b) => {
    if (sortKey.value === "modified") {
      const ta = Date.parse(a.mod_time || "") || 0;
      const tb = Date.parse(b.mod_time || "") || 0;
      return (ta - tb) * order;
    }
    return naturalSort(a.name || "", b.name || "") * order;
  });
});

const headerSelectState = computed<SelectState>(() => {
  if (!sortedDirs.value.length) return "none";
  let checkedOrCovered = 0;
  let partial = 0;
  for (const dir of sortedDirs.value) {
    const state = selectionState(dir);
    if (state === "checked" || state === "covered") checkedOrCovered += 1;
    else if (state === "partial") partial += 1;
  }
  if (checkedOrCovered === sortedDirs.value.length) return "checked";
  if (checkedOrCovered > 0 || partial > 0) return "partial";
  return "none";
});

function toggleSelectAllVisible() {
  if (!props.multiSelect || !sortedDirs.value.length) return;
  if (props.inverseSelectionDisplay) {
    if (headerSelectState.value === "none") {
      let next = activeSelections.value;
      for (const dir of sortedDirs.value) {
        next = withHierarchyExclusive(next, selectionForDir(dir), true);
      }
      commitSelections(next);
      return;
    }
    let next = activeSelections.value;
    for (const dir of sortedDirs.value) {
      const item = selectionForDir(dir);
      next = next.filter(
        (selected) => String(selected.id) !== item.id && !isAncestorSelection(item, selected),
      );
    }
    commitSelections(next);
    return;
  }
  if (headerSelectState.value === "checked") {
    let next = activeSelections.value;
    for (const dir of sortedDirs.value) {
      const item = selectionForDir(dir);
      next = next.filter(
        (selected) =>
          String(selected.id) !== item.id
          && !isAncestorSelection(selected, item)
          && !isAncestorSelection(item, selected),
      );
    }
    commitSelections(next);
    return;
  }
  let next = activeSelections.value;
  for (const dir of sortedDirs.value) {
    next = withHierarchyExclusive(next, selectionForDir(dir), true);
  }
  commitSelections(next);
}

function sortClass(key: FolderSortKey): SortOrder | "" {
  return sortKey.value === key ? sortOrder.value : "";
}

function toggleSort(key: FolderSortKey) {
  if (sortKey.value === key) {
    sortOrder.value = sortOrder.value === "asc" ? "desc" : "asc";
    return;
  }
  sortKey.value = key;
  sortOrder.value = key === "name" ? "asc" : "desc";
}

async function load(parentId: string, opts?: { forceRefresh?: boolean }) {
  loading.value = true;
  error.value = "";
  try {
    dirs.value = await listDirs(parentId, opts);
    return true;
  } catch (e) {
    dirs.value = [];
    error.value = getApiErrorMessage(e, "加载失败");
    return false;
  } finally {
    loading.value = false;
  }
}

function resetCreateState() {
  showCreateInput.value = false;
  newFolderName.value = "";
}

function openDir(dir: FileItem) {
  closeFavoriteMenu();
  breadcrumb.value = [...breadcrumb.value, { id: dir.id, name: dir.name }];
  filterKeyword.value = "";
  resetCreateState();
  clearSelection();
  void load(dir.id);
}

function goTo(index: number) {
  const minIndex = props.rootAnchor ? 0 : 0;
  if (index < minIndex) return;
  if (index >= breadcrumb.value.length - 1) return;
  closeFavoriteMenu();
  breadcrumb.value = breadcrumb.value.slice(0, index + 1);
  filterKeyword.value = "";
  resetCreateState();
  clearSelection();
  void load(currentParentId.value);
}

function refresh() {
  closeFavoriteMenu();
  resetCreateState();
  void load(currentParentId.value, { forceRefresh: true });
}

function startCreateFolder() {
  if (!canCreateFolder.value || creating.value) return;
  filterKeyword.value = "";
  showCreateInput.value = true;
  void nextTick(() => {
    createInputRef.value?.focus();
  });
}

function cancelCreateFolder() {
  if (creating.value) return;
  resetCreateState();
}

async function submitCreateFolder() {
  if (!canCreateFolder.value) return;
  let name: string;
  try {
    name = validateFileName(newFolderName.value);
  } catch (e) {
    toast.info(e instanceof FileNameError ? e.message : "文件夹名称无效");
    return;
  }
  if (visibleDirs.value.some((d) => (d.name || "").toLowerCase() === name.toLowerCase())) {
    toast.info("当前目录已存在同名文件夹");
    return;
  }

  creating.value = true;
  try {
    const res = await filesApi.createFolder({
      account_id: props.accountId,
      parent_id: currentParentId.value,
      name,
    });
    resetCreateState();
    await load(currentParentId.value);
    const created = dirs.value.find(
      (d) => String(d.id) === String(res.folder_id) || (d.name || "").toLowerCase() === name.toLowerCase(),
    );
    toast.success(`文件夹 "${name}" 创建成功`);
    if (created) openDir(created);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "创建文件夹失败"));
  } finally {
    creating.value = false;
  }
}

async function openFavorite(item: BrowserFavoriteItem) {
  if (!item.crumbs.length) return;
  closeFavoriteMenu();
  const previousBreadcrumb = cloneBreadcrumb(breadcrumb.value);
  const previousFilter = filterKeyword.value;
  const targetBreadcrumb = cloneBreadcrumb(item.crumbs);
  breadcrumb.value = targetBreadcrumb;
  filterKeyword.value = "";
  resetCreateState();
  clearSelection();
  const ok = await load(currentParentId.value);
  if (ok) return;
  breadcrumb.value = previousBreadcrumb;
  filterKeyword.value = previousFilter;
  await load(previousBreadcrumb[previousBreadcrumb.length - 1]?.id ?? "");
  toast.info("收藏夹目录不存在或暂时无法打开");
}

function selectCurrent() {
  closeFavoriteMenu();
  const payload: {
    accountId: number;
    parentId: string;
    path: string;
    selections?: FolderSelection[];
  } = {
    accountId: props.accountId,
    parentId: currentParentId.value,
    path: currentPath.value,
  };
  if (props.multiSelect && selectedCount.value > 0) {
    payload.selections = activeSelections.value;
  }
  emit("resolve", payload);
}

watch(
  () => props.searchValue,
  (value) => {
    if (value === undefined || value === filterKeyword.value) return;
    filterKeyword.value = value;
  },
  { immediate: true },
);

watch(filterKeyword, (value) => {
  if (props.searchValue === undefined || value === props.searchValue) return;
  emit("update:searchValue", value);
});

watch(
  () => [props.accountId, props.initialPath, props.initialBreadcrumb, props.rootAnchor] as const,
  () => {
    void resetAndLoad();
  },
  { immediate: true },
);

watch(
  () => [props.favorites, props.favoritesLoading] as const,
  () => {
    if (favoriteTriggerDisabled.value) closeFavoriteMenu();
    if (favoritePage.value >= favoritePageCount.value) {
      favoritePage.value = Math.max(0, favoritePageCount.value - 1);
    }
  },
  { deep: true },
);
</script>

<template>
  <div class="folder-selector">
    <div v-if="title || showFavoriteEntry || showSearch || showClose" class="folder-selector__header">
      <h3 v-if="title" class="folder-selector__title">{{ title }}</h3>
      <div v-if="showFavoriteEntry || showSearch" class="folder-selector__header-tools">
        <div v-if="showFavoriteEntry" class="folder-selector__favorite-entry">
          <button
            type="button"
            class="folder-selector__favorite-trigger"
            :class="{ active: favoriteMenuOpen }"
            :title="favoriteTriggerTitle"
            :aria-label="favoriteTriggerTitle"
            :aria-expanded="favoriteMenuOpen"
            :disabled="favoriteTriggerDisabled"
            @click="toggleFavoriteMenu"
          >
            <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path
                d="M12 2.8l2.8 5.68 6.27.91-4.53 4.42 1.07 6.25L12 17.17 6.39 20.06l1.07-6.25L2.93 9.39l6.27-.91L12 2.8z"
              />
            </svg>
          </button>
        </div>
        <label
          v-if="showSearch"
          class="folder-selector__search"
          title="仅筛选当前目录下已加载的文件夹"
        >
          <span class="folder-selector__search-icon"><SvgIcon name="search" :size="15" /></span>
          <input
            v-model.trim="filterKeyword"
            type="search"
            placeholder="筛选当前目录文件夹"
            :disabled="loading"
            maxlength="100"
          />
          <button
            v-if="filterKeyword"
            type="button"
            class="folder-selector__search-clear"
            aria-label="清空筛选"
            @click="filterKeyword = ''"
          >
            ×
          </button>
        </label>
      </div>
      <button
        v-if="showClose"
        type="button"
        class="folder-selector__close"
        aria-label="关闭"
        @click="emit('cancel')"
      >
        ×
      </button>
    </div>

    <div class="folder-selector__content" :class="{ 'favorite-mode': favoriteMenuOpen }">
      <div v-if="showFavoriteEntry" class="folder-selector__panel-stack">
        <div class="folder-selector__favorites-panel" :class="{ 'is-open': favoriteMenuOpen }">
          <div class="folder-selector__favorites-head">
            <span class="folder-selector__favorites-title">收藏夹快捷进入</span>
            <span v-if="favoriteItems.length > FAVORITE_PAGE_SIZE" class="folder-selector__favorites-pager">
              <button
                type="button"
                class="folder-selector__favorites-page-btn"
                :disabled="!canPrevFavoritePage"
                aria-label="上一页"
                @click="prevFavoritePage"
              >
                <SvgIcon name="chevron-down" :size="12" class-name="folder-selector__favorites-page-icon folder-selector__favorites-page-icon--prev" />
              </button>
              <span class="folder-selector__favorites-page-status">
                {{ favoritePage + 1 }} / {{ favoritePageCount }}
              </span>
              <button
                type="button"
                class="folder-selector__favorites-page-btn"
                :disabled="!canNextFavoritePage"
                aria-label="下一页"
                @click="nextFavoritePage"
              >
                <SvgIcon name="chevron-down" :size="12" class-name="folder-selector__favorites-page-icon folder-selector__favorites-page-icon--next" />
              </button>
            </span>
          </div>
          <div v-if="props.favoritesLoading" class="folder-selector__favorite-empty">收藏夹加载中…</div>
          <div v-else-if="!favoriteItems.length" class="folder-selector__favorite-empty">当前账号暂无收藏夹</div>
          <div v-else class="folder-selector__favorite-grid">
            <button
              v-for="item in pagedFavoriteItems"
              :key="item.id"
              type="button"
              class="folder-selector__favorite-card"
              :class="{ active: isFavoriteCurrent(item) }"
              @click="void openFavorite(item)"
            >
              <span class="folder-selector__favorite-card-icon">
                <SvgIcon name="folder" :size="16" />
              </span>
              <span class="folder-selector__favorite-card-body">
                <span class="folder-selector__favorite-name">{{ item.name }}</span>
                <span class="folder-selector__favorite-path">{{ formatFavoritePath(item) }}</span>
              </span>
            </button>
          </div>
        </div>

        <div class="folder-selector__directory-panel" :class="{ 'is-shifted': favoriteMenuOpen }">
          <BreadcrumbNav :items="breadcrumb" compact @navigate="goTo" />
          <div v-if="filterKeyword" class="folder-selector__filter-tip">
            仅筛选当前目录，匹配 {{ sortedDirs.length }} 项
          </div>

          <div class="file-list folder-selector__list" :class="tableClass">
            <div class="folder-table-header" role="row">
              <label
                v-if="multiSelect"
                class="checkbox-col"
                :title="inverseSelectionDisplay
                  ? (headerSelectState === 'none' ? '排除当前层' : '包含当前层')
                  : (headerSelectState === 'checked' ? '取消全选' : '全选当前层')"
              >
                <input
                  type="checkbox"
                  :checked="inverseSelectionDisplay ? headerSelectState === 'none' : headerSelectState === 'checked'"
                  :indeterminate="headerSelectState === 'partial'"
                  :disabled="loading || !sortedDirs.length"
                  @change="toggleSelectAllVisible"
                />
              </label>
              <button
                v-for="col in columns"
                :key="col.key"
                type="button"
                class="folder-table-heading"
                :class="[`col-${col.key}`, { active: sortKey === col.key }]"
                @click="toggleSort(col.key)"
              >
                <span class="folder-table-heading-label folder-table-heading-label--full">{{ col.label }}</span>
                <span class="folder-table-heading-label folder-table-heading-label--compact">
                  {{ col.key === "modified" ? "时间" : col.label }}
                </span>
                <span class="sort-indicator" :class="sortClass(col.key)" />
              </button>
            </div>

            <div class="folder-table-body">
              <div v-if="showCreateInput" class="folder-create-row">
                <span class="folder-name-icon"><SvgIcon name="folder" :size="18" /></span>
                <input
                  ref="createInputRef"
                  v-model.trim="newFolderName"
                  type="text"
                  class="inline-rename-input"
                  placeholder="输入文件夹名称"
                  maxlength="100"
                  :disabled="creating"
                  @keyup.enter="submitCreateFolder"
                  @keyup.esc="cancelCreateFolder"
                />
                <button
                  type="button"
                  class="folder-inline-btn confirm"
                  title="确认"
                  :disabled="creating"
                  @click="submitCreateFolder"
                >
                  ✓
                </button>
                <button
                  type="button"
                  class="folder-inline-btn cancel"
                  title="取消"
                  :disabled="creating"
                  @click="cancelCreateFolder"
                >
                  ×
                </button>
              </div>

              <div v-if="loading" class="folder-state">加载中…</div>
              <div v-else-if="error" class="folder-state error">{{ error }}</div>
              <div v-else-if="!sortedDirs.length && !showCreateInput" class="folder-state">
                {{ filterKeyword ? "当前目录没有匹配的文件夹" : "没有子目录" }}
              </div>
              <template v-else>
                <div
                  v-for="dir in sortedDirs"
                  :key="dir.id"
                  class="folder-table-row"
                  :class="{
                    selected: multiSelect && !inverseSelectionDisplay && (isSelected(dir) || isPartialSelected(dir)),
                  }"
                  @click="openDir(dir)"
                >
                  <label
                    v-if="multiSelect"
                    class="checkbox-col"
                    :title="inverseSelectionDisplay
                      ? (selectionState(dir) === 'covered' ? '已被上级目录排除' : `勾选参与刮削，取消勾选则排除 ${dir.name}`)
                      : (selectionState(dir) === 'covered'
                        ? '已包含在上级目录中，请先取消上级目录'
                        : selectionState(dir) === 'partial'
                          ? '已选部分子目录，点击改为全选此目录'
                          : `选择 ${dir.name}`)"
                    @click.stop
                  >
                    <input
                      type="checkbox"
                      :checked="isDisplayChecked(dir)"
                      :indeterminate="isDisplayPartial(dir)"
                      :disabled="selectionState(dir) === 'covered'"
                      :aria-label="`选择 ${dir.name}`"
                      @change="toggleDisplaySelect(dir)"
                    />
                  </label>
                  <div class="folder-name-cell">
                    <span class="folder-name-icon"><SvgIcon name="folder" :size="18" /></span>
                    <span class="folder-name-text" :title="dir.name">{{ dir.name }}</span>
                  </div>
                  <span class="folder-time-cell">
                    <span class="folder-time-cell__full">{{ formatTime(dir.mod_time) }}</span>
                    <span class="folder-time-cell__compact">{{ formatFolderTimeShort(dir.mod_time) }}</span>
                  </span>
                </div>
              </template>
            </div>
          </div>
        </div>
      </div>
      <template v-else>
        <BreadcrumbNav :items="breadcrumb" compact @navigate="goTo" />
        <div v-if="filterKeyword" class="folder-selector__filter-tip">
          仅筛选当前目录，匹配 {{ sortedDirs.length }} 项
        </div>

        <div class="file-list folder-selector__list" :class="tableClass">
          <div class="folder-table-header" role="row">
            <label
              v-if="multiSelect"
              class="checkbox-col"
              :title="inverseSelectionDisplay
                ? (headerSelectState === 'none' ? '排除当前层' : '包含当前层')
                : (headerSelectState === 'checked' ? '取消全选' : '全选当前层')"
            >
              <input
                type="checkbox"
                :checked="inverseSelectionDisplay ? headerSelectState === 'none' : headerSelectState === 'checked'"
                :indeterminate="headerSelectState === 'partial'"
                :disabled="loading || !sortedDirs.length"
                @change="toggleSelectAllVisible"
              />
            </label>
            <button
              v-for="col in columns"
              :key="col.key"
              type="button"
              class="folder-table-heading"
              :class="[`col-${col.key}`, { active: sortKey === col.key }]"
              @click="toggleSort(col.key)"
            >
              <span class="folder-table-heading-label folder-table-heading-label--full">{{ col.label }}</span>
              <span class="folder-table-heading-label folder-table-heading-label--compact">
                {{ col.key === "modified" ? "时间" : col.label }}
              </span>
              <span class="sort-indicator" :class="sortClass(col.key)" />
            </button>
          </div>

          <div class="folder-table-body">
            <div v-if="showCreateInput" class="folder-create-row">
              <span class="folder-name-icon"><SvgIcon name="folder" :size="18" /></span>
              <input
                ref="createInputRef"
                v-model.trim="newFolderName"
                type="text"
                class="inline-rename-input"
                placeholder="输入文件夹名称"
                maxlength="100"
                :disabled="creating"
                @keyup.enter="submitCreateFolder"
                @keyup.esc="cancelCreateFolder"
              />
              <button
                type="button"
                class="folder-inline-btn confirm"
                title="确认"
                :disabled="creating"
                @click="submitCreateFolder"
              >
                ✓
              </button>
              <button
                type="button"
                class="folder-inline-btn cancel"
                title="取消"
                :disabled="creating"
                @click="cancelCreateFolder"
              >
                ×
              </button>
            </div>

            <div v-if="loading" class="folder-state">加载中…</div>
            <div v-else-if="error" class="folder-state error">{{ error }}</div>
            <div v-else-if="!sortedDirs.length && !showCreateInput" class="folder-state">
              {{ filterKeyword ? "当前目录没有匹配的文件夹" : "没有子目录" }}
            </div>
            <template v-else>
              <div
                v-for="dir in sortedDirs"
                :key="dir.id"
                class="folder-table-row"
                :class="{
                  selected: multiSelect && !inverseSelectionDisplay && (isSelected(dir) || isPartialSelected(dir)),
                }"
                @click="openDir(dir)"
              >
                <label
                  v-if="multiSelect"
                  class="checkbox-col"
                  :title="inverseSelectionDisplay
                    ? (selectionState(dir) === 'covered' ? '已被上级目录排除' : `勾选参与刮削，取消勾选则排除 ${dir.name}`)
                    : (selectionState(dir) === 'covered'
                      ? '已包含在上级目录中，请先取消上级目录'
                      : selectionState(dir) === 'partial'
                        ? '已选部分子目录，点击改为全选此目录'
                        : `选择 ${dir.name}`)"
                  @click.stop
                >
                  <input
                    type="checkbox"
                    :checked="isDisplayChecked(dir)"
                    :indeterminate="isDisplayPartial(dir)"
                    :disabled="selectionState(dir) === 'covered'"
                    :aria-label="`选择 ${dir.name}`"
                    @change="toggleDisplaySelect(dir)"
                  />
                </label>
                <div class="folder-name-cell">
                  <span class="folder-name-icon"><SvgIcon name="folder" :size="18" /></span>
                  <span class="folder-name-text" :title="dir.name">{{ dir.name }}</span>
                </div>
                <span class="folder-time-cell">
                  <span class="folder-time-cell__full">{{ formatTime(dir.mod_time) }}</span>
                  <span class="folder-time-cell__compact">{{ formatFolderTimeShort(dir.mod_time) }}</span>
                </span>
              </div>
            </template>
          </div>
        </div>
      </template>
    </div>

    <div class="folder-selector__footer">
      <button
        v-if="canCreateFolder"
        type="button"
        class="folder-selector__secondary"
        :disabled="loading || creating"
        @click="startCreateFolder"
      >
        <span class="folder-selector__btn-icon"><SvgIcon name="folder-plus" :size="16" /></span>
        新建文件夹
      </button>
      <button
        v-if="showRefresh"
        type="button"
        class="folder-selector__secondary"
        :disabled="loading"
        @click="refresh"
      >
        <span class="folder-selector__btn-icon" :class="{ spin: loading }">
          <SvgIcon name="refresh" :size="16" />
        </span>
        刷新
      </button>
      <div class="folder-selector__spacer" aria-hidden="true" />
      <span v-if="multiSelect && selectedCount" class="folder-selector__count">已选 {{ selectedCount }} 项</span>
      <AppButton
        variant="primary"
        class="folder-selector__confirm"
        :disabled="loading"
        @click="selectCurrent"
      >
        {{ primaryActionText }}
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.folder-selector {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
}

.folder-selector__header {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 20px 24px 0;
  position: relative;
}
.folder-selector__title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text);
  flex-shrink: 0;
}
.folder-selector__header-tools {
  margin-left: auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
}
.folder-selector__favorite-entry {
  flex-shrink: 0;
}
.folder-selector__favorite-trigger {
  width: 36px;
  height: 36px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: var(--transition);
  box-shadow: none;
}
.folder-selector__favorite-trigger:hover:not(:disabled),
.folder-selector__favorite-trigger.active {
  border-color: color-mix(in srgb, var(--brand) 42%, var(--border));
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  color: var(--brand);
}
.folder-selector__favorite-trigger:disabled {
  opacity: 0.55;
  cursor: not-allowed;
  box-shadow: none;
}
.folder-selector__favorite-trigger svg {
  width: 16px;
  height: 16px;
}
.folder-selector__favorite-name {
  display: block;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.folder-selector__favorite-path {
  display: block;
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.folder-selector__favorite-empty {
  padding: 12px 14px;
  color: var(--text-muted);
  font-size: 12px;
}
.folder-selector__favorites-panel {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  opacity: 0;
  overflow: hidden;
  background: transparent;
  box-sizing: border-box;
  transition:
    opacity 0.18s ease,
    transform 0.24s ease;
  transform: translateY(-18px);
  pointer-events: none;
}
.folder-selector__favorites-panel.is-open {
  opacity: 1;
  transform: translateY(0);
  pointer-events: auto;
}
.folder-selector__favorites-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 2px 4px 12px;
}
.folder-selector__favorites-title {
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 600;
}
.folder-selector__favorites-pager {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.folder-selector__favorites-page-btn {
  width: 26px;
  height: 26px;
  border-radius: var(--radius-sm);
  border: none;
  background: transparent;
  color: var(--text-muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.folder-selector__favorites-page-btn:hover:not(:disabled) {
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
  color: var(--text);
}
.folder-selector__favorites-page-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.folder-selector__favorites-page-icon {
  line-height: 0;
  display: block;
}
.folder-selector__favorites-page-icon--prev {
  transform: rotate(90deg);
}
.folder-selector__favorites-page-icon--next {
  transform: rotate(-90deg);
}
.folder-selector__favorites-page-status {
  min-width: 34px;
  text-align: center;
  color: var(--text-muted);
  font-size: 12px;
}
.folder-selector__favorite-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  grid-auto-rows: 52px;
  gap: 10px 14px;
  padding: 0 0 8px;
  align-self: stretch;
  width: 100%;
  align-content: start;
  box-sizing: border-box;
}
.folder-selector__favorite-card {
  min-width: 0;
  min-height: 52px;
  border: none;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--surface-muted) 58%, var(--surface));
  color: var(--text);
  text-align: left;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 12px;
  cursor: pointer;
  transition:
    background 0.18s ease;
}
.folder-selector__favorite-card:hover,
.folder-selector__favorite-card.active {
  background: color-mix(in srgb, var(--brand) 9%, var(--surface));
}
.folder-selector__favorite-card-icon {
  flex: 0 0 auto;
  color: color-mix(in srgb, var(--brand) 56%, var(--text));
  display: inline-flex;
  line-height: 0;
}
.folder-selector__favorite-card-body {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.folder-selector__search {
  width: 220px;
  height: 36px;
  padding: 0 10px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-muted);
  display: flex;
  align-items: center;
  gap: 8px;
  box-sizing: border-box;
}
.folder-selector__search-icon {
  display: inline-flex;
  flex-shrink: 0;
  line-height: 0;
}
.folder-selector__search input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  appearance: none;
  -webkit-appearance: none;
  background: transparent;
  color: var(--text);
  font-size: 13px;
}
.folder-selector__search input::-webkit-search-cancel-button,
.folder-selector__search input::-webkit-search-decoration {
  display: none;
  appearance: none;
  -webkit-appearance: none;
}
.folder-selector__search input::placeholder {
  color: var(--text-muted);
}
.folder-selector__search-clear {
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.folder-selector__search-clear:hover {
  color: var(--text);
}
.folder-selector__close {
  background: none;
  border: none;
  color: var(--text-muted);
  font-size: 20px;
  line-height: 1;
  width: 24px;
  height: 24px;
  padding: 0;
  cursor: pointer;
}
.folder-selector__close:hover {
  color: var(--text);
}

.folder-selector__content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 14px 24px 12px;
  box-sizing: border-box;
  overflow: visible;
}
.folder-selector__content.favorite-mode {
  overflow: hidden;
}
.folder-selector__panel-stack {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.folder-selector__directory-panel {
  position: relative;
  z-index: 2;
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--surface);
  transition:
    transform 0.28s ease,
    opacity 0.2s ease;
}
.folder-selector__directory-panel.is-shifted {
  transform: translateY(100%);
  opacity: 0.96;
  pointer-events: none;
}
.folder-selector__content :deep(.breadcrumb) {
  flex: none;
}
.folder-selector__filter-tip {
  margin-top: 8px;
  color: var(--text-muted);
  font-size: 12px;
}

.folder-selector__list {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  margin-top: 6px;
  overflow: hidden;
}

.folder-table-header,
.folder-table-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 200px;
  align-items: center;
  column-gap: 16px;
}
.folder-selector__list.folder-selector__table--multi .folder-table-header,
.folder-selector__list.folder-selector__table--multi .folder-table-row {
  grid-template-columns: 48px minmax(0, 1fr) 200px;
}
.folder-table-header {
  flex-shrink: 0;
  min-height: 46px;
  margin: 0 0 6px;
  padding: 0 12px;
  background: var(--surface-muted);
  border-radius: var(--radius-md);
}
.folder-selector__list .checkbox-col {
  box-sizing: border-box;
  width: 48px;
  align-self: stretch;
  display: grid;
  place-items: center;
  margin: 0;
  cursor: pointer;
}
.folder-selector__list .folder-table-row .checkbox-col {
  min-height: 100%;
}
.folder-table-row.selected {
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}
.folder-selector__count {
  margin-right: 8px;
  color: var(--text-muted);
  font-size: 12px;
}
.folder-table-heading {
  min-width: 0;
  height: 100%;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 500;
  text-align: left;
}
.folder-table-heading.col-modified {
  justify-content: flex-end;
  text-align: right;
}
/* 排序箭头：与数据行时间文字右边缘对齐。
   表头是 inline-flex(gap 6px)，箭头占位 12px（8 宽 + 4 margin-left），
   所以表头文字右缘 = 列右 - 18px；数据行用相同 padding-right 补偿，两者右边缘一致。 */
.folder-selector .sort-indicator {
  display: inline-block;
  width: 8px;
  height: 10px;
  margin-left: 4px;
  position: relative;
  vertical-align: middle;
}
.folder-selector .sort-indicator::before,
.folder-selector .sort-indicator::after {
  content: "";
  position: absolute;
  left: 0;
  width: 0;
  height: 0;
  border-left: 4px solid transparent;
  border-right: 4px solid transparent;
}
.folder-selector .sort-indicator::before {
  top: 0;
  border-bottom: 4px solid var(--border);
}
.folder-selector .sort-indicator::after {
  bottom: 0;
  border-top: 4px solid var(--border);
}
.folder-selector .sort-indicator.asc::before {
  border-bottom-color: var(--text-regular);
}
.folder-selector .sort-indicator.asc::after {
  border-top-color: var(--border);
}
.folder-selector .sort-indicator.desc::before {
  border-bottom-color: var(--border);
}
.folder-selector .sort-indicator.desc::after {
  border-top-color: var(--text-regular);
}
.folder-table-heading.active {
  color: var(--text-regular);
}
.folder-table-heading-label {
  white-space: nowrap;
}
.folder-table-heading-label--compact,
.folder-time-cell__compact {
  display: none;
}

.folder-table-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: 2px;
}
.folder-table-row {
  padding: 12px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background 0.15s ease;
}
.folder-table-row:hover {
  background: var(--surface-sunken);
}
.folder-name-cell {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
}
.folder-name-icon {
  flex-shrink: 0;
  line-height: 0;
  display: inline-flex;
}
.folder-name-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  color: var(--text-regular);
}
.folder-time-cell {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
  font-size: 13px;
  text-align: right;
  /* 与表头「修改时间」文字右缘对齐：表头文字被排序箭头（gap 6 + 箭头 12）让出 18px */
  padding-right: 18px;
}

.folder-create-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
}
.folder-create-row .inline-rename-input {
  flex: 1;
  min-width: 0;
  height: 32px;
}

.folder-state {
  padding: 28px 20px;
  text-align: center;
  color: var(--text-muted);
  font-style: italic;
}
.folder-state.error {
  color: var(--danger);
}

.folder-selector__footer {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
  padding: 0 24px 24px;
}
.folder-selector__spacer {
  flex: 1;
}
/* 内联白色按钮（新建文件夹/刷新）：刻意的无 hover、字重 400 变体，保留本地样式。 */
.folder-selector__secondary {
  height: 38px;
  border-radius: var(--radius-sm);
  padding: 0 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  cursor: pointer;
  font-size: 14px;
  font-weight: 400;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-regular);
}
.folder-selector__secondary:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
/* 确认按钮：仅布局，配色/hover 走全局 .btn--primary。 */
.folder-selector__confirm {
  height: 38px;
  min-width: 140px;
  padding: 0 16px;
  font-size: 14px;
}
.folder-selector__btn-icon {
  display: inline-flex;
  line-height: 0;
}
@media (max-width: 640px) {
  .folder-selector__header {
    padding: 18px 18px 0;
    flex-wrap: wrap;
    gap: 10px;
  }
  .folder-selector__header-tools {
    order: 3;
    width: 100%;
  }
  .folder-selector__search {
    width: 100%;
  }
  .folder-selector__content {
    padding: 12px 18px 10px;
  }
  .folder-selector__favorite-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    grid-auto-rows: 44px;
  }
  .folder-table-header,
  .folder-table-row {
    grid-template-columns: minmax(0, 1fr) 82px;
    column-gap: 6px;
  }
  .folder-selector__list.folder-selector__table--multi .folder-table-header,
  .folder-selector__list.folder-selector__table--multi .folder-table-row {
    grid-template-columns: 36px minmax(0, 1fr) 82px;
    column-gap: 6px;
  }
  .folder-table-heading-label--full,
  .folder-time-cell__full {
    display: none;
  }
  .folder-table-heading-label--compact,
  .folder-time-cell__compact {
    display: inline;
  }
  .folder-selector__footer {
    padding: 0 18px 20px;
  }
}
@media (max-width: 900px) {
  .folder-selector__favorite-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
