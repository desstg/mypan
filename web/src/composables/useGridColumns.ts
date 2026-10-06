import { computed, onBeforeUnmount, onMounted, ref, watch, type Ref } from "vue";

/** 与 tg-subscribe.css 的 `.tg-grid` 保持一致：最小列宽与间距。 */
const MIN_COL = 140;
const GAP = 14;

/**
 * 数出海报墙**当前**排了几列。
 *
 * # 为什么要算，而不是数 DOM
 *
 * 网格是 `auto-fill` 自适应的，列数只由容器宽度决定 —— CSS 那边没法把它喂回来。
 * 而「最后一排不能空」只有拿到列数才算得出来：每页条数必须是列数的整数倍。
 *
 * 数首行有几张卡片也能得到列数，但那条路要求**容器里已经有卡片**，
 * 于是列表为空时永远量不到。所以这里直接按 auto-fill 的公式自己算：
 *
 *     cols = floor((W + gap) / (minCol + gap))
 *
 * 这与浏览器的行为一致（W 是容器的内容宽度）。
 *
 * # 侧栏收起 / 展开、窗口缩放都算「列数变了」
 *
 * 上面那个公式只在容器宽度变化时才需要重跑。侧栏一收，`.admin__body` 宽了 156px，
 * 列数就从 7 跳到 8 —— 每页条数必须跟着从 35 变成 40，否则最后一行会空出两格。
 * 所以这里挂在 ResizeObserver 上。
 *
 * # 观察必须在**网格元素出现之后**才挂得上
 *
 * 两张墙的网格都是 `v-if` 出来的（有数据才渲染），组件挂载那一刻它还不存在。
 * 早先的写法只在 onMounted 里 observe 一次，于是 `el.value` 是 null、
 * 观察者根本没建起来 —— 列数只在翻页时（load 里那次 measure）才更新，
 * 侧栏一收起就露馅：40 张卡片排进 7 列，最后一排空两格。
 * 现在用 watch(el) 补挂，元素一出现就观察上。
 */
export function useGridColumns(el: Ref<HTMLElement | null>) {
  const cols = ref(1);
  let ro: ResizeObserver | null = null;

  function measure() {
    const node = el.value;
    if (!node) return;
    const inner = node.clientWidth;
    if (inner <= 0) return; // 隐藏中的 tab：宽度为 0，保持上一次的值
    cols.value = Math.max(1, Math.floor((inner + GAP) / (MIN_COL + GAP)));
  }

  function attach(node: HTMLElement | null) {
    ro?.disconnect();
    ro = null;
    if (!node) return;
    measure();
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(() => measure());
      ro.observe(node);
    }
  }

  onMounted(() => {
    attach(el.value);
    // ResizeObserver 已经覆盖了窗口缩放；这条只是万一它不可用时的兜底。
    window.addEventListener("resize", measure);
  });

  onBeforeUnmount(() => {
    window.removeEventListener("resize", measure);
    ro?.disconnect();
    ro = null;
  });

  watch(el, (node) => attach(node));

  return { cols, measure };
}

/**
 * 把「列数 × 行数」换算成每页条数。
 *
 * `rows` 是**行数**而不是条数：列数会随窗口与侧栏变，钉死条数就一定会出现缺角的
 * 尾行，钉死行数则每页条数自动跟着列数走。桌面宽度下 7 列 × 5 行 = 35 部
 * （侧栏收起时 8 列 = 40 部），也就是「每页 40 部左右」。
 */
export function useGridPageSize(el: Ref<HTMLElement | null>, rows: number) {
  const { cols, measure } = useGridColumns(el);
  const pageSize = computed(() => Math.max(1, cols.value * rows));
  return { cols, pageSize, measure };
}

/** 导出给调用方复用，免得两处各写一份魔法数字。 */
export const TG_GRID_METRICS = { MIN_COL, GAP };
