<script setup lang="ts">
import { computed, ref, watch } from "vue";

/**
 * 卡片图 + **统一的占位图**。
 *
 * # 为什么要有它
 *
 * 上游的图（JAVDB / JAVBUS / TMDB 的封面与头像）时不时会挂：图床 404、防盗链给
 * 一张空图、代理那条路超时、或者记录里的 URL 本来就是坏的。以前这些位置只处理了
 * 「没有 URL」那一种情况（`v-if="cover"` / `v-else` 一个文字块），
 * **有 URL 但加载失败**时浏览器就留一块破图 —— 一屏卡片里冒出几个碎图标，
 * 比空着还难看，而且看不出是「这部片没图」还是「图挂了」。
 *
 * 所以判据从「有没有 URL」升级成「有没有一张能显示的图」：`src` 为空**或**
 * 加载报错，都落到同一张占位图上。
 *
 * # 两条实现上的讲究
 *
 * 1. **`inheritAttrs: false` + 显式 `v-bind="$attrs"`**：调用方传的 class
 *    （如 `jav-us__thumb`、`tg-card__image`）必须落到**真正渲染出来的那个元素**上，
 *    否则原有的尺寸 / 圆角 / object-fit 全失效。多根组件默认的 class 透传行为
 *    在 Vue 3 里是「不自动合并」，所以这里自己绑。
 * 2. **`src` 变化时重置 `failed`**：列表复用同一个组件实例是常态（`v-for` 加
 *    key 变化、或分页后同一位置换了部片）。不重置的话，前一张失败的图会把
 *    后一张也带成占位图 —— 而那种错是静默的（看起来只是「这张也没图」）。
 *
 * # 占位图是内联 SVG，不是图片文件
 *
 * 矢量、跟着 `currentColor` 走（浅色/深色主题都合适）、不增加一次请求。
 * 换成 png 的话既要新增一个二进制文件，又不会随主题变色。
 */
const props = withDefaults(
  defineProps<{
    /** 图片地址。空串 / undefined 直接显示占位图。 */
    src?: string | null;
    alt?: string;
    /**
     * 占位图的形态。`film` 是「无封面」（影片 / 海报 / 剧照），
     * `person` 是「无头像」（演员 / 分享者）。
     */
    variant?: "film" | "person";
    /**
     * 是否用原生 `loading="lazy"`。
     *
     * **海报墙那类一屏几十张图的地方要关掉**：原生懒加载的触发距离是按
     * 视口高度的倍数算的，一屏 30~40 张的长列表里，浏览器会在首屏渲染时就把
     * 下面好几屏的图一并排队下载 —— 实测一页 50 张全部发出去、2 MB 多。
     * 关掉之后由调用方自己按视口取图（见 StrmJavWall 的图窗格），
     * 发出去的张数与实际看得到的一致。
     *
     * 默认仍为 true：单张封面的地方（详情抽屉、订阅卡）不需要这层控制，
     * 原生懒加载足够，而且少一层 IntersectionObserver。
     */
    lazy?: boolean;
  }>(),
  { src: "", alt: "", variant: "film", lazy: true },
);

defineOptions({ inheritAttrs: false });

const failed = ref(false);

watch(
  () => props.src,
  () => {
    failed.value = false;
  },
);

const showImg = computed(() => Boolean((props.src ?? "").trim()) && !failed.value);
</script>

<template>
  <img
    v-if="showImg"
    v-bind="$attrs"
    :src="props.src ?? ''"
    :alt="props.alt"
    :loading="props.lazy ? 'lazy' : 'eager'"
    decoding="async"
    @error="failed = true"
  />
  <!--
    占位图。role="img" + aria-label 是给读屏用的：这是个「图」，不是装饰，
    说清它是「暂无封面」比让它被跳过有用。
  -->
  <span
    v-else
    v-bind="$attrs"
    class="media-ph"
    role="img"
    :aria-label="variant === 'person' ? '暂无头像' : '暂无封面'"
  >
    <svg
      class="media-ph__icon"
      viewBox="0 0 48 48"
      fill="none"
      stroke="currentColor"
      stroke-width="2.4"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <template v-if="variant === 'person'">
        <circle cx="24" cy="18" r="7" />
        <path d="M11 40c0-7.2 5.8-13 13-13s13 5.8 13 13" />
      </template>
      <template v-else>
        <!-- 画框 + 山与太阳：通用的「这里本该有张图」。 -->
        <rect x="6" y="9" width="36" height="30" rx="3.5" />
        <circle cx="17" cy="19" r="3.2" />
        <path d="M6 32.5 16.5 24l7.5 6.5L31 24l11 8.5" />
      </template>
    </svg>
  </span>
</template>

<style scoped>
/* 占位块铺满父容器（父容器负责尺寸与背景色 —— 那些是各卡片自己的事）。 */
.media-ph {
  display: grid;
  place-items: center;
  width: 100%;
  height: 100%;
  color: var(--text-muted);
  /* 没有背景色时给一层极淡的底，免得占位图直接贴在卡片白底上看不见边界。
     已有背景色的容器（.jav-card__cover 那类 surface-sunken）盖不掉它 ——
     同一层元素上父容器的背景色本来就在下面，不影响。 */
  background: var(--surface-sunken);
}

/* 图标按容器比例缩放，并夹在合理区间：
   - 下限 22px：小尺寸的缩略图（下载记录那一列 60px 宽）里也看得清；
   - 上限 64px：大封面上不至于糊成一大块。 */
.media-ph__icon {
  width: 34%;
  min-width: 22px;
  max-width: 64px;
  height: auto;
  aspect-ratio: 1;
  opacity: 0.7;
}
</style>
