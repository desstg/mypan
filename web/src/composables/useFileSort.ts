import { ref, watch, type Ref } from "vue";
import type { FileItem } from "@/api/types";
import type { SortKey, SortOrder } from "@/types/file-browser";
import { naturalSort } from "@/utils/naturalSort";

const STORAGE_PREFIX = "litepan_sort_account_";

function isSortKey(v: unknown): v is SortKey {
  return v === "name" || v === "size" || v === "modified";
}

function isSortOrder(v: unknown): v is SortOrder {
  return v === "asc" || v === "desc";
}

function loadSortState(accountKey: string): { key: SortKey; order: SortOrder } {
  if (!accountKey) return { key: "name", order: "asc" };
  try {
    const raw = localStorage.getItem(`${STORAGE_PREFIX}${accountKey}`);
    if (!raw) return { key: "name", order: "asc" };
    const parsed = JSON.parse(raw) as { key?: unknown; order?: unknown };
    if (isSortKey(parsed.key) && isSortOrder(parsed.order)) {
      return { key: parsed.key, order: parsed.order };
    }
  } catch {}
  return { key: "name", order: "asc" };
}

function saveSortState(accountKey: string, key: SortKey, order: SortOrder) {
  if (!accountKey) return;
  localStorage.setItem(`${STORAGE_PREFIX}${accountKey}`, JSON.stringify({ key, order }));
}

export function sortFileItems(
  items: FileItem[],
  sortKey: SortKey,
  sortOrder: SortOrder,
): FileItem[] {
  return [...items].sort((a, b) => compareFiles(a, b, sortKey, sortOrder));
}

function compareFiles(a: FileItem, b: FileItem, sortKey: SortKey, sortOrder: SortOrder): number {
  if (a.is_dir && !b.is_dir) return -1;
  if (!a.is_dir && b.is_dir) return 1;

  if (sortKey === "name") {
    const result = naturalSort(a.name || "", b.name || "");
    return sortOrder === "asc" ? result : -result;
  }

  if (sortKey === "size") {
    const aVal = a.size || 0;
    const bVal = b.size || 0;
    if (aVal < bVal) return sortOrder === "asc" ? -1 : 1;
    if (aVal > bVal) return sortOrder === "asc" ? 1 : -1;
    return 0;
  }

  const aVal = new Date(a.mod_time || 0).getTime();
  const bVal = new Date(b.mod_time || 0).getTime();
  if (aVal < bVal) return sortOrder === "asc" ? -1 : 1;
  if (aVal > bVal) return sortOrder === "asc" ? 1 : -1;
  return 0;
}

export function useFileSort(
  files: Ref<FileItem[]>,
  accountKey: Ref<string>,
  resortOnLoad: Ref<number>,
) {
  const sortKey = ref<SortKey>("name");
  const sortOrder = ref<SortOrder>("asc");
  let loadedAccount = "";

  watch(
    accountKey,
    (key) => {
      if (!key || key === loadedAccount) return;
      loadedAccount = key;
      const saved = loadSortState(key);
      sortKey.value = saved.key;
      sortOrder.value = saved.order;
    },
    { immediate: true },
  );

  function persist() {
    saveSortState(accountKey.value, sortKey.value, sortOrder.value);
  }

  function applySort() {
    files.value = sortFileItems(files.value, sortKey.value, sortOrder.value);
  }

  function sortBy(key: SortKey, order?: SortOrder) {
    if (order === "asc" || order === "desc") {
      sortKey.value = key;
      sortOrder.value = order;
    } else if (sortKey.value === key) {
      sortOrder.value = sortOrder.value === "asc" ? "desc" : "asc";
    } else {
      sortKey.value = key;
      sortOrder.value = "asc";
    }
    applySort();
    persist();
  }

  watch(resortOnLoad, () => {
    if (resortOnLoad.value > 0) applySort();
  });

  function sortClass(key: SortKey): SortOrder | "" {
    return sortKey.value === key ? sortOrder.value : "";
  }

  return {
    sortKey,
    sortOrder,
    sortBy,
    sortClass,
  };
}
