import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { filesApi } from "@/api/files";
import { publicApi } from "@/api/public";
import { getApiErrorMessage } from "@/api/client";
import type { BrowserFavoriteItem, BrowserFavoritesState, FileItem } from "@/api/types";
import { toast } from "@/composables/useToast";
import { useAccountsStore } from "./accounts";
import { useAuthStore } from "./auth";

export interface Crumb {
  id: string;
  name: string;
}

const ROOT: Crumb = { id: "", name: "根目录" };

function cloneCrumbs(crumbs: Crumb[]) {
  return crumbs.map((item) => ({ id: item.id, name: item.name }));
}

export interface LoadFilesOptions {
  silent?: boolean;
  forceRefresh?: boolean;
}

export const useBrowserStore = defineStore("browser", () => {
  const accountsStore = useAccountsStore();
  const authStore = useAuthStore();
  const currentAccountId = ref<number | null>(null);
  const breadcrumb = ref<Crumb[]>([ROOT]);
  const files = ref<FileItem[]>([]);
  const filesResortTick = ref(0);
  const loading = ref(false);
  const refreshing = ref(false);
  const error = ref("");
  const responseTime = ref("-");
  const cacheRate = ref("-");
  const favorites = ref<BrowserFavoriteItem[]>([]);
  const favoritesOpen = ref(false);
  const favoritesAccountId = ref<number | null>(null);
  let loadFilesSeq = 0;
  let refreshFilesSeq = 0;
  let favoritesStateSeq = 0;
  let favoritesSaveChain = Promise.resolve();

  const accounts = computed(() => accountsStore.accounts.filter((a) => a.is_active));
  const currentAccount = computed(
    () => accounts.value.find((a) => a.id === currentAccountId.value) ?? null,
  );
  const currentParentId = computed(() => breadcrumb.value[breadcrumb.value.length - 1]?.id ?? "");

  function clearFavoritesState() {
    favorites.value = [];
    favoritesOpen.value = false;
    favoritesAccountId.value = null;
  }

  function setCurrentAccount(accountId: number | null) {
    if (currentAccountId.value === accountId) return;
    currentAccountId.value = accountId;
    favoritesStateSeq += 1;
    clearFavoritesState();
  }

  function isCurrentFavoritesOperation(operationSeq: number, accountId: number) {
    return operationSeq === favoritesStateSeq && currentAccountId.value === accountId;
  }

  function enqueueFavoritesSave(payload: Parameters<typeof filesApi.saveFavorites>[0]) {
    const request = favoritesSaveChain.then(() => filesApi.saveFavorites(payload));
    favoritesSaveChain = request.then(() => undefined, () => undefined);
    return request;
  }

  async function loadFavorites(accountId = currentAccountId.value, opts?: { silent?: boolean }) {
    if (!authStore.isAdmin) {
      favoritesStateSeq += 1;
      clearFavoritesState();
      return;
    }
    if (accountId == null) {
      favoritesStateSeq += 1;
      clearFavoritesState();
      return;
    }
    const operationSeq = ++favoritesStateSeq;
    try {
      const data = await filesApi.getFavorites(accountId);
      if (!isCurrentFavoritesOperation(operationSeq, accountId)) return;
      favorites.value = data.items;
      favoritesOpen.value = data.open;
      favoritesAccountId.value = accountId;
    } catch (e) {
      if (!isCurrentFavoritesOperation(operationSeq, accountId)) return;
      clearFavoritesState();
      if (!opts?.silent) {
        toast.error(getApiErrorMessage(e, "收藏夹加载失败"));
      }
    }
  }

  async function persistFavoritesState(nextState: BrowserFavoritesState, opts?: { successMessage?: string; silent?: boolean }) {
    if (!authStore.isAdmin) return false;
    const accountId = currentAccountId.value;
    if (accountId == null) return false;
    const operationSeq = ++favoritesStateSeq;
    const prevItems = favorites.value;
    const prevOpen = favoritesOpen.value;
    favorites.value = nextState.items;
    favoritesOpen.value = nextState.open;
    try {
      const saved = await enqueueFavoritesSave({
        account_id: accountId,
        open: nextState.open,
        items: nextState.items,
      });
      if (!isCurrentFavoritesOperation(operationSeq, accountId)) return true;
      favorites.value = saved.items;
      favoritesOpen.value = saved.open;
      favoritesAccountId.value = accountId;
      if (opts?.successMessage) {
        toast.success(opts.successMessage);
      }
      return true;
    } catch (e) {
      if (!isCurrentFavoritesOperation(operationSeq, accountId)) return false;
      favorites.value = prevItems;
      favoritesOpen.value = prevOpen;
      if (!opts?.silent) {
        toast.error(getApiErrorMessage(e, "收藏夹保存失败"));
      }
      return false;
    }
  }

  async function setFavoritesOpen(open: boolean) {
    await persistFavoritesState({
      open,
      items: favorites.value,
    }, { silent: true });
  }

  async function toggleFavoritesOpen() {
    await setFavoritesOpen(!favoritesOpen.value);
  }

  async function addCurrentDirectoryFavorite(customName?: string) {
    if (currentParentId.value === "") {
      toast.info("根目录无需加入收藏夹");
      return;
    }
    if (favorites.value.some((item) => item.id === currentParentId.value)) {
      toast.info("当前文件夹已在收藏夹中");
      return;
    }
    const lastCrumb = breadcrumb.value[breadcrumb.value.length - 1];
    const nextName = (customName ?? lastCrumb?.name ?? "当前目录").trim();
    if (!nextName) {
      toast.info("收藏名不能为空");
      return;
    }
    await persistFavoritesState({
      open: true,
      items: [
        ...favorites.value,
        {
          id: currentParentId.value,
          name: nextName,
          crumbs: breadcrumb.value.map((item) => ({ id: item.id, name: item.name })),
        },
      ],
    }, { successMessage: "已加入收藏夹" });
  }

  async function removeFavorite(folderId: string) {
    await persistFavoritesState({
      open: favoritesOpen.value,
      items: favorites.value.filter((item) => item.id !== folderId),
    });
  }

  async function moveFavorite(folderId: string, direction: -1 | 1) {
    const idx = favorites.value.findIndex((item) => item.id === folderId);
    if (idx < 0) return;
    const next = idx + direction;
    if (next < 0 || next >= favorites.value.length) return;
    const items = [...favorites.value];
    [items[idx], items[next]] = [items[next], items[idx]];
    await persistFavoritesState({
      open: favoritesOpen.value,
      items,
    }, { silent: true });
  }

  async function renameFavorite(folderId: string, name: string) {
    const nextName = name.trim();
    if (!nextName) {
      toast.info("收藏名不能为空");
      return;
    }
    await persistFavoritesState({
      open: favoritesOpen.value,
      items: favorites.value.map((item) =>
        item.id === folderId ? { ...item, name: nextName } : item,
      ),
    }, { successMessage: "收藏名已更新" });
  }

  async function openFavorite(favorite: BrowserFavoriteItem) {
    const accountId = currentAccountId.value;
    if (accountId == null) return;
    await openDirectory(accountId, favorite.crumbs, { silent: true });
  }

  async function ensureValidCurrentAccount() {
    if (!accounts.value.length) {
      setCurrentAccount(null);
      breadcrumb.value = [ROOT];
      files.value = [];
      return false;
    }
    const current = accounts.value.find((a) => a.id === currentAccountId.value);
    if (currentAccountId.value !== null && current) {
      return false;
    }
    const next = accounts.value.find((a) => a.is_default) ?? accounts.value[0];
    setCurrentAccount(next.id);
    breadcrumb.value = [ROOT];
    return true;
  }

  function primeLocation(accountId: number, crumbs: Crumb[]) {
    setCurrentAccount(accountId);
    breadcrumb.value = crumbs.length ? cloneCrumbs(crumbs) : [ROOT];
  }

  async function loadAccounts(opts?: { reconcile?: boolean }) {
    await accountsStore.loadAccounts();
    if (!accounts.value.length) {
      setCurrentAccount(null);
      breadcrumb.value = [ROOT];
      files.value = [];
      return;
    }
    if (opts?.reconcile) {
      const switched = await ensureValidCurrentAccount();
      if (switched) {
        await loadFiles();
      }
    }
  }

  async function resetToDefaultAccount() {
    const active = accounts.value;
    if (!active.length) {
      setCurrentAccount(null);
      breadcrumb.value = [ROOT];
      files.value = [];
      return;
    }
    const def = active.find((a) => a.is_default) ?? active[0];
    await selectAccount(def.id);
  }

  async function selectAccount(id: number) {
    if (!accounts.value.some((a) => a.id === id)) {
      await ensureValidCurrentAccount();
      if (currentAccountId.value !== null) {
        await loadFiles();
      }
      return;
    }
    setCurrentAccount(id);
    breadcrumb.value = [ROOT];
    await Promise.all([loadFiles(), loadFavorites(id, { silent: true })]);
  }

  async function openDirectory(
    accountId: number,
    crumbs: Crumb[],
    opts?: LoadFilesOptions,
  ) {
    if (!accounts.value.some((a) => a.id === accountId)) {
      await ensureValidCurrentAccount();
      if (currentAccountId.value === null) return;
    } else {
      const changedAccount = currentAccountId.value !== accountId;
      setCurrentAccount(accountId);
      if (changedAccount || favoritesAccountId.value !== accountId) {
        await loadFavorites(accountId, { silent: true });
      }
    }
    breadcrumb.value = crumbs.length ? cloneCrumbs(crumbs) : [ROOT];
    await loadFiles(opts);
  }

  async function fetchCacheHitRate() {
    try {
      const data = await publicApi.cacheHitRate();
      cacheRate.value = `${data.hit_rate}%`;
    } catch {
      cacheRate.value = "-";
    }
  }

  async function loadFiles(opts?: LoadFilesOptions) {
    const accountId = currentAccountId.value;
    if (accountId === null) return;
    const parentId = currentParentId.value;
    const requestSeq = ++loadFilesSeq;
    const silent = opts?.silent ?? false;
    if (!silent) loading.value = true;
    error.value = "";
    const started = performance.now();
    try {
      const crumbsPath = breadcrumb.value
        .slice(1)
        .map((c) => c.name)
        .join("/");
      const res = await filesApi.list(accountId, parentId, {
        forceRefresh: opts?.forceRefresh,
        path: crumbsPath || undefined,
      });
      if (isStaleFileListRequest(requestSeq, accountId, parentId)) return;
      files.value = res.items;
      filesResortTick.value += 1;
      responseTime.value = `${Math.round(performance.now() - started)}ms`;
      void fetchCacheHitRate();
    } catch (e) {
      if (isStaleFileListRequest(requestSeq, accountId, parentId)) return;
      if (!silent) files.value = [];
      error.value = getApiErrorMessage(e, "加载失败");
      responseTime.value = "-";
      if (!silent) toast.error(error.value);
    } finally {
      if (!silent && requestSeq === loadFilesSeq) loading.value = false;
    }
  }

  function removeFilesLocally(ids: string[]) {
    const drop = new Set(ids.map(String));
    files.value = files.value.filter((f) => !drop.has(String(f.id || f.name)));
  }

  function renameFileLocally(fileId: string, newName: string) {
    const id = String(fileId);
    files.value = files.value.map((f) =>
      String(f.id || f.name) === id ? { ...f, name: newName } : f,
    );
  }

  function addFolderLocally(folder: FileItem) {
    if (files.value.some((f) => f.name.toLowerCase() === folder.name.toLowerCase())) return;
    files.value = [folder, ...files.value];
  }

  async function refreshFiles() {
    const accountId = currentAccountId.value;
    if (accountId === null) {
      toast.info("请先选择一个账号");
      return;
    }
    const parentId = currentParentId.value;
    const requestSeq = ++refreshFilesSeq;
    refreshing.value = true;
    error.value = "";
    const started = performance.now();
    try {
      const res = await filesApi.list(accountId, parentId, {
        forceRefresh: true,
      });
      if (isStaleRefreshRequest(requestSeq, accountId, parentId)) return;
      files.value = res.items;
      filesResortTick.value += 1;
      responseTime.value = `${Math.round(performance.now() - started)}ms`;
      void fetchCacheHitRate();
      toast.success(`强制刷新成功，获取到 ${res.items.length} 个项目`);
    } catch (e) {
      if (isStaleRefreshRequest(requestSeq, accountId, parentId)) return;
      error.value = getApiErrorMessage(e, "刷新失败");
      responseTime.value = "-";
      toast.error(error.value);
    } finally {
      if (requestSeq === refreshFilesSeq) refreshing.value = false;
    }
  }

  function isCurrentLocation(accountId: number, parentId: string) {
    return currentAccountId.value === accountId && currentParentId.value === parentId;
  }

  function isStaleFileListRequest(requestSeq: number, accountId: number, parentId: string) {
    return requestSeq !== loadFilesSeq || !isCurrentLocation(accountId, parentId);
  }

  function isStaleRefreshRequest(requestSeq: number, accountId: number, parentId: string) {
    return requestSeq !== refreshFilesSeq || !isCurrentLocation(accountId, parentId);
  }

  async function enterFolder(folder: FileItem) {
    breadcrumb.value.push({ id: folder.id, name: folder.name });
    await loadFiles();
  }

  async function goTo(index: number) {
    if (index >= breadcrumb.value.length - 1) return;
    breadcrumb.value = breadcrumb.value.slice(0, index + 1);
    await loadFiles();
  }

  function downloadURL(file: FileItem): string {
    return filesApi.downloadURL(currentAccountId.value as number, file.id);
  }

  return {
    accounts,
    currentAccountId,
    currentAccount,
    breadcrumb,
    favorites,
    favoritesOpen,
    files,
    filesResortTick,
    loading,
    refreshing,
    error,
    responseTime,
    cacheRate,
    currentParentId,
    loadAccounts,
    loadFavorites,
    primeLocation,
    resetToDefaultAccount,
    selectAccount,
    openDirectory,
    loadFiles,
    refreshFiles,
    removeFilesLocally,
    renameFileLocally,
    addFolderLocally,
    setFavoritesOpen,
    toggleFavoritesOpen,
    addCurrentDirectoryFavorite,
    removeFavorite,
    moveFavorite,
    renameFavorite,
    openFavorite,
    enterFolder,
    goTo,
    downloadURL,
  };
});
