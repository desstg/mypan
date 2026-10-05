import { nextTick, onUnmounted, watch, type Ref } from "vue";

/**
 * 从这个元素往上找第一个会裁切内容的祖先（后台上是 `.admin__body`）。
 * 找不到返回 null = 整页滚动，用 `window.innerHeight`。
 *
 * ⚠️ 判据里**故意不要求「当前已经能滚」**：内容不足一屏时那个盒子
 * `scrollHeight === clientHeight`，它仍然是正确的视口 —— 要求能滚会把它排除掉，
 * 直接掉进「找不到视口」的坑里。
 */
export function findScrollBox(from: HTMLElement | null): HTMLElement | null {
  let el = from?.parentElement ?? null;
  while (el && el !== document.body && el !== document.documentElement) {
    const overflowY = getComputedStyle(el).overflowY;
    if (overflowY === "auto" || overflowY === "scroll" || overflowY === "overlay") {
      return el;
    }
    el = el.parentElement;
  }
  return null;
}

/**
 * 「内容不足一屏就自动续上」。
 *
 * # 为什么需要它
 *
 * 拉到底续加载通常用 `IntersectionObserver` 盯着底部哨兵。但观察器的语义是
 * 「哨兵与视口相交时回调」—— **内容总高不够一屏时页面根本没有可滚动空间**，
 * 用户手指往下拖什么也不会发生，而回调只在挂载那一刻触发过一次。表现就是
 * 「图没满一屏，往下拉不到，显示不了后面的」（手机竖屏尤其明显）。
 *
 * 这里补一条判据：**哨兵还在视口内 = 这一页没填满屏幕 = 立刻再拉一页**，
 * 形成自动填充，直到内容超过一屏、用户有得滚为止。
 *
 * # 连拉上限
 *
 * `maxStreak` 防「后端一直说还有、每次只回一两条」时把请求打飞。
 * 计数在「查询条件变了」时由调用方通过 `reset()` 归零。
 *
 * # 用法
 *
 * `sentinel` 由**调用方自己持有**（它已经在模板里 `ref="sentinel"` 了），传进来即可 ——
 * 组件内部再 new 一个同名的 ref 会永远拿不到元素，而失败是静默的（一条请求都不发）。
 *
 * ```ts
 * const sentinel = ref<HTMLElement | null>(null);
 * const { reset } = useFillViewport({
 *   sentinel,
 *   hasMore,
 *   busy: computed(() => loading.value || loadingMore.value),
 *   loadMore: () => void load(true),
 * });
 * ```
 *
 * 组件自己挂 `IntersectionObserver` 做「滚到底」那条路（哨兵**必须常驻 DOM**）。
 */
export function useFillViewport(options: {
  /** 底部哨兵元素。**由调用方持有**（模板里已经 `ref="sentinel"`），必须常驻 DOM。 */
  sentinel: Ref<HTMLElement | null>;
  /** 还有没有下一页。为假时什么都不做。 */
  hasMore: Ref<boolean>;
  /** 正在请求中（首屏或续加载任一）。避免重入。 */
  busy: Ref<boolean>;
  /** 触发续加载。 */
  loadMore: () => void;
  /** 最多连续自动续几次。默认 10。 */
  maxStreak?: number;
}) {
  let streak = 0;
  const maxStreak = options.maxStreak ?? 10;

  /** 视口底边的屏幕坐标。滚动盒子由哨兵自己往上找，找不到就当整页滚。 */
  function viewBottom(): number {
    const box = findScrollBox(options.sentinel.value);
    return box ? box.getBoundingClientRect().bottom : window.innerHeight;
  }

  /** 内容没填满一屏就再拉一页。每次列表变长后（nextTick）调一次。 */
  function fill() {
    if (!options.hasMore.value || options.busy.value) return;
    if (streak >= maxStreak) return;
    const marker = options.sentinel.value;
    if (!marker) return;
    if (marker.getBoundingClientRect().top <= viewBottom()) {
      streak += 1;
      options.loadMore();
    }
  }

  /** 查询条件变了（换档 / 搜索 / 换排序）时归零，让下一批重新享有完整的自动填充额度。 */
  function reset() {
    streak = 0;
  }

  // 列表变长（首屏或追加）后量一次 —— 要在 DOM 更新之后，量到的位置才是最终的。
  watch(
    [options.hasMore, options.busy],
    () => {
      void nextTick(fill);
    },
  );

  // 哨兵换元素（v-if 分支重渲染）时也量一次。
  watch(options.sentinel, () => {
    void nextTick(fill);
  });

  onUnmounted(() => {
    streak = 0;
  });

  return { fill, reset };
}
