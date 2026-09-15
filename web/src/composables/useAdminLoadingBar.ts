import {
  inject,
  onBeforeUnmount,
  provide,
  readonly,
  ref,
  watch,
  type InjectionKey,
  type Ref,
  type WatchSource,
} from "vue";

const REVEAL_DELAY_MS = 120;
const MIN_VISIBLE_MS = 200;
const activePageKey: InjectionKey<Ref<string>> = Symbol("admin-active-page");
const pendingSources = new Set<symbol>();
const visible = ref(false);
let revealTimer: number | null = null;
let hideTimer: number | null = null;
let visibleSince = 0;

function clearRevealTimer() {
  if (revealTimer === null) return;
  window.clearTimeout(revealTimer);
  revealTimer = null;
}

function clearHideTimer() {
  if (hideTimer === null) return;
  window.clearTimeout(hideTimer);
  hideTimer = null;
}

function syncBar() {
  if (pendingSources.size > 0) {
    clearHideTimer();
    if (visible.value || revealTimer !== null) return;
    revealTimer = window.setTimeout(() => {
      revealTimer = null;
      if (pendingSources.size === 0) return;
      visibleSince = Date.now();
      visible.value = true;
    }, REVEAL_DELAY_MS);
    return;
  }

  clearRevealTimer();
  if (!visible.value || hideTimer !== null) return;
  const remaining = Math.max(0, MIN_VISIBLE_MS - (Date.now() - visibleSince));
  hideTimer = window.setTimeout(() => {
    hideTimer = null;
    if (pendingSources.size > 0) {
      syncBar();
      return;
    }
    visible.value = false;
  }, remaining);
}

function setPending(id: symbol, pending: boolean) {
  if (pending) pendingSources.add(id);
  else pendingSources.delete(id);
  syncBar();
}

export function provideAdminPageContext(activePage: Ref<string>) {
  provide(activePageKey, activePage);
}

export function useAdminPageLoading(pageKey: string, source: WatchSource<boolean>) {
  const activePage = inject(activePageKey);
  const id = Symbol(pageKey);
  let pending = false;

  const syncSource = () => {
    setPending(id, pending && (!activePage || activePage.value === pageKey));
  };

  const stopSource = watch(
    source,
    (value) => {
      pending = Boolean(value);
      syncSource();
    },
    { immediate: true },
  );
  const stopPage = activePage ? watch(activePage, syncSource) : null;

  onBeforeUnmount(() => {
    stopSource();
    stopPage?.();
    setPending(id, false);
  });
}

export function useAdminLoadingBar() {
  return { visible: readonly(visible) };
}
