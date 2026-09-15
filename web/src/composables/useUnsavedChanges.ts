import { ref } from "vue";
import { confirm } from "@/composables/useConfirm";

// 全局共享的「未保存改动」标记。设置页置位，切换后台页/刷新/关闭标签前据此拦截。
const dirty = ref(false);
const dirtySources = new Map<symbol, { discard?: () => void }>();
let beforeUnloadInstalled = false;

function syncDirtyState(): void {
  dirty.value = dirtySources.size > 0;
}

function updateDirtySource(source: symbol, value: boolean, discard?: () => void): void {
  if (value) dirtySources.set(source, { discard });
  else dirtySources.delete(source);
  syncDirtyState();
}

function removeDirtySource(source: symbol): void {
  dirtySources.delete(source);
  syncDirtyState();
}

function discardChanges(): void {
  const handlers = new Set(
    [...dirtySources.values()]
      .map((entry) => entry.discard)
      .filter((handler): handler is () => void => Boolean(handler)),
  );
  dirtySources.clear();
  syncDirtyState();
  handlers.forEach((handler) => handler());
}

function ensureBeforeUnload(): void {
  if (beforeUnloadInstalled) return;
  beforeUnloadInstalled = true;
  window.addEventListener("beforeunload", (e: BeforeUnloadEvent) => {
    if (!dirty.value) return;
    e.preventDefault();
    e.returnValue = "";
  });
}

export function useUnsavedChanges() {
  ensureBeforeUnload();

  // 离开前确认：无改动直接放行；有改动弹确认，用户取消则留在本页。
  async function confirmLeave(): Promise<boolean> {
    if (!dirty.value) return true;
    try {
      return await confirm({
        title: "有未保存的改动",
        message: "当前页面有未保存的设置改动，离开将丢弃这些改动，确定离开吗？",
        confirmText: "离开",
        cancelText: "留在本页",
        danger: true,
      });
    } catch {
      return false;
    }
  }

  return {
    dirty,
    confirmLeave,
    discardChanges,
    updateDirtySource,
    removeDirtySource,
  };
}
