import { computed, onMounted, onUnmounted, ref, watch, type Ref } from "vue";

function findScrollParent(el: HTMLElement | null): HTMLElement | Window {
  let node = el?.parentElement ?? null;
  while (node && node !== document.body) {
    const { overflowY } = getComputedStyle(node);
    if (overflowY === "auto" || overflowY === "scroll" || overflowY === "overlay") {
      return node;
    }
    node = node.parentElement;
  }
  return window;
}

function viewBounds(scrollParent: HTMLElement | Window): { top: number; bottom: number } {
  if (scrollParent instanceof HTMLElement) {
    const pr = scrollParent.getBoundingClientRect();
    return { top: pr.top, bottom: pr.bottom };
  }
  return { top: 0, bottom: window.innerHeight };
}

/** 跟随页面（或后台主区）滚动的虚拟海报墙，不另开内部滚动条。 */
export function useVirtualPosterWall<T>(items: Ref<readonly T[]>) {
  const MIN_COL = 140;
  const GAP = 14;
  const PAD = 16;
  // 海报 2:3 + 标题区 + 卡片 1px 边框；略留余量避免 phantom 偏短底行被裁切
  const META_H = 62;
  const ASPECT = 3 / 2;
  const OVERSCAN = 2;
  const BOTTOM_SLACK = 12;

  const rootEl = ref<HTMLElement | null>(null);
  const viewportW = ref(0);
  const localTop = ref(0);
  const localBottom = ref(0);

  const cols = computed(() => {
    const inner = Math.max(0, viewportW.value - PAD * 2);
    if (inner <= 0) return 1;
    return Math.max(1, Math.floor((inner + GAP) / (MIN_COL + GAP)));
  });

  const colW = computed(() => {
    const inner = Math.max(0, viewportW.value - PAD * 2);
    const c = cols.value;
    if (c <= 0) return MIN_COL;
    return Math.max(MIN_COL, (inner - GAP * (c - 1)) / c);
  });

  const rowH = computed(() => colW.value * ASPECT + META_H);
  const rowStride = computed(() => rowH.value + GAP);

  const totalRows = computed(() => Math.ceil(items.value.length / cols.value) || 0);
  const totalHeight = computed(() => {
    if (totalRows.value <= 0) return 0;
    return (
      PAD * 2 +
      totalRows.value * rowH.value +
      Math.max(0, totalRows.value - 1) * GAP +
      BOTTOM_SLACK
    );
  });

  const startRow = computed(() => {
    const y = Math.max(0, localTop.value - PAD);
    return Math.max(0, Math.floor(y / rowStride.value) - OVERSCAN);
  });

  const endRow = computed(() => {
    const y = Math.max(localBottom.value, localTop.value + rowStride.value);
    const last = Math.ceil(Math.max(0, y - PAD) / rowStride.value) + OVERSCAN;
    return Math.min(totalRows.value, Math.max(startRow.value + 1, last));
  });

  const offsetY = computed(() => PAD + startRow.value * rowStride.value);

  const visibleItems = computed(() => {
    const c = cols.value;
    const from = startRow.value * c;
    const to = Math.min(items.value.length, endRow.value * c);
    return items.value.slice(from, to) as T[];
  });

  let scrollParent: HTMLElement | Window = window;
  let ro: ResizeObserver | null = null;

  function measure() {
    const el = rootEl.value;
    if (!el) return;
    viewportW.value = el.clientWidth;
    const rect = el.getBoundingClientRect();
    const { top: viewTop, bottom: viewBottom } = viewBounds(scrollParent);
    localTop.value = Math.max(0, viewTop - rect.top);
    localBottom.value = Math.min(totalHeight.value, Math.max(0, viewBottom - rect.top));
  }

  function bindScrollParent(el: HTMLElement) {
    const next = findScrollParent(el);
    if (scrollParent !== next) {
      scrollParent.removeEventListener("scroll", measure as EventListener);
      scrollParent = next;
      scrollParent.addEventListener("scroll", measure as EventListener, { passive: true });
    }
  }

  function resetScroll() {
    localTop.value = 0;
    localBottom.value = Math.max(localBottom.value, rowStride.value * 4);
    requestAnimationFrame(measure);
  }

  onMounted(() => {
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, { passive: true });
  });

  onUnmounted(() => {
    ro?.disconnect();
    window.removeEventListener("resize", measure);
    window.removeEventListener("scroll", measure);
    scrollParent.removeEventListener("scroll", measure as EventListener);
  });

  watch(rootEl, (el, _prev, onCleanup) => {
    ro?.disconnect();
    ro = null;
    if (!el) return;
    bindScrollParent(el);
    measure();
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(() => {
        bindScrollParent(el);
        measure();
      });
      ro.observe(el);
      if (scrollParent instanceof HTMLElement) {
        ro.observe(scrollParent);
      }
      onCleanup(() => {
        ro?.disconnect();
        ro = null;
      });
    }
  });

  watch(
    () => items.value.length,
    () => {
      requestAnimationFrame(measure);
    },
  );

  watch(totalHeight, () => requestAnimationFrame(measure));

  return {
    rootEl,
    measure,
    resetScroll,
    cols,
    totalHeight,
    offsetY,
    visibleItems,
    gridStyle: computed(() => ({
      gridTemplateColumns: `repeat(${cols.value}, minmax(0, 1fr))`,
      gap: `${GAP}px`,
    })),
  };
}
