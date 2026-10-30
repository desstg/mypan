<script setup lang="ts">
interface Tab {
  key: string;
  label: string;
  /** 该 Tab 内存在未保存改动时标一个小圆点。 */
  changed?: boolean;
  /**
   * 可展开的分组（如「番号」）。
   *
   * 带上它的按钮只是**展开开关**，不是内容 Tab —— 点击仍然照常发
   * update:modelValue，由父级决定「这一下是展开还是切页」。真正的 Tab 是它
   * 展开出来的那些子项（它们就是普通的 Tab 条目，混在同一个数组里）。
   */
  expandable?: boolean;
  /** 分组的展开状态。只对 expandable 的条目有意义，箭头方向随之翻转。 */
  expanded?: boolean;
  /**
   * 该 Tab 里的条目数，渲染成标签后面的一个小胶囊。
   *
   * 缺省（undefined）就不渲染 —— 别的页面一个字段都不用加，观感完全不变。
   * 给 0 是**会**渲染的：0 也是信息（「这一档确实是空的」），
   * 而「不知道有几条」是另一回事，那才该留空。
   */
  count?: number;
}

defineProps<{
  tabs: Tab[];
  modelValue: string;
  /** 可选的分组图标（FontAwesome 名，如 fire）。缺省不渲染，其它页面不受影响。 */
  icon?: string;
  /**
   * 整条栏粘在滚动容器顶部。**缺省不粘**，所以其它页面一个字段都不用改、观感完全不变。
   *
   * 只有「内容要滚很久」的页面才值得开（海报墙：一屏几十张卡片，滚十几屏之后
   * 页头那几颗动作按钮就够不着了 —— 用户 2026-10-30 要求）。
   */
  sticky?: boolean;
  /**
   * 手机上（≤768px）是否也保持吸顶。**缺省 true**，所以开了 `sticky` 的页面
   * 一个字都不用改。
   *
   * 只有番号海报墙传 `false`：手机上一屏只放得下一张卡片，这条栏
   * （Tab 一排 + 动作按钮一排）粘住之后吃掉的可视高度太多，那一档改用两颗
   * 只有图标的小浮标（见 `AuxToolsManagement.vue` 的 `.jav-mobile-actions`）。
   */
  stickyMobile?: boolean;
}>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <div
    class="tabbar"
    :class="{ 'tabbar--sticky': sticky, 'tabbar--sticky-desktop-only': sticky && !stickyMobile }"
  >
    <div class="tabbar__tabs">
      <i v-if="icon" class="tabbar__icon fas" :class="`fa-${icon}`" aria-hidden="true" />
      <!-- TransitionGroup 负责分组 Tab 的展开/收起。其余页面传的都是静态列表、
           不会有增删，所以包上它对那些页面没有任何可见影响。 -->
      <TransitionGroup name="tabbar-tab">
        <button
          v-for="t in tabs"
          :key="t.key"
          type="button"
          class="tabbar__tab"
          :class="{ 'tabbar__tab--active': t.key === modelValue }"
          :aria-expanded="t.expandable ? !!t.expanded : undefined"
          @click="emit('update:modelValue', t.key)"
        >
          {{ t.label }}
          <span v-if="t.count !== undefined" class="tabbar__tab-count">{{ t.count }}</span>
          <span v-if="t.changed" class="tabbar__tab-dot" aria-hidden="true" />
          <span v-if="t.changed" class="tabbar__sr-only">（有未保存改动）</span>
          <!-- 箭头方向用**旋转**表达，而不是换图标：换 chevron-right →
               chevron-left 是瞬变，没有过程可看；旋转才能看到它翻过去。 -->
          <svg
            v-if="t.expandable"
            class="tabbar__tab-arrow"
            :class="{ 'tabbar__tab-arrow--open': t.expanded }"
            viewBox="0 0 16 16"
            width="12"
            height="12"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M6 3.5 10.5 8 6 12.5" />
          </svg>
        </button>
      </TransitionGroup>
    </div>
    <div class="tabbar__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
.tabbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  background: var(--surface);
  border-radius: var(--radius-md);
  padding: 8px 10px;
  box-shadow: var(--shadow-card);
  margin-bottom: 18px;
}
.tabbar__tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.tabbar__icon {
  margin-right: 6px;
  color: var(--brand);
  font-size: 14px;
}
.tabbar__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  padding: 8px 14px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: var(--transition);
}
.tabbar__tab-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--warning);
}
.tabbar__tab-count {
  flex: none;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 700;
  line-height: 1.5;
}
/* 选中的那个 Tab 是品牌色底。再用 surface-sunken 会糊成一块，
   所以按「主色上的文字色」调一层半透明底。 */
.tabbar__tab--active .tabbar__tab-count {
  background: color-mix(in srgb, var(--text-on-brand) 22%, transparent);
  color: var(--text-on-brand);
}
.tabbar__sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  border: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.tabbar__tab:hover {
  color: var(--text);
  background: var(--border-soft);
}
.tabbar__tab--active,
.tabbar__tab--active:hover {
  background: var(--brand);
  color: var(--text-on-brand);
}
.tabbar__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 整条栏粘在滚动容器顶部（`.admin__body`），滚多远都够得着页头那几颗按钮。
 *
 * ⚠️ `top` 必须吃掉滚动容器的**上内边距**：`.admin__body` 桌面 24px、手机上 16px
 *    （见 AdminShell）。留成 0 的话，栏会停在内边距**之下**，它上方那 24px 里
 *    滚过去的卡片照旧露出来 —— 看着像栏浮在半空、底下还有一条缝。
 *    负偏移把它顶到容器的可视边，栏体本身不透明，滚过去的内容自然被盖住。
 *
 * 只有「内容要滚很久」的页面才开（海报墙），其余页面缺省不粘、观感不变。
 *
 * `z-index: 45` 的顺序是算过的：刮削面板里的 tooltip 气泡是 40（`.scrape-tip__bubble`，
 * 见 StrmScrapePanel），栏要压在它上面；而页面级下拉 / 弹窗都是 Teleport 到 body 的
 * `--z-dropdown`(10050) 起跳，远在它之上，不会被栏挡住。 */
.tabbar--sticky {
  position: sticky;
  top: calc(-1 * var(--tabbar-sticky-pad, 24px));
  z-index: 45;
}

@media (max-width: 768px) {
  /* 与 AdminShell 里 `.admin__body` 的手机内边距一致。 */
  .tabbar--sticky {
    --tabbar-sticky-pad: 16px;
  }

  /* 手机上**整条栏不粘**（`stickyMobile: false`，目前只有番号海报墙这么传）。
   *
   * 为什么单独一个类名而不是让调用方自己关 `sticky`：桌面还要粘。手机上一屏
   * 只放得下一张卡片，「Tab 一排 + 动作按钮一排」粘住之后海报区几乎没法看 ——
   * 那一档改用两颗只有图标的小浮标（见 `AuxToolsManagement.vue` 的
   * `.jav-mobile-actions`），栏自己退回普通流。
   *
   * 桌面（>768px）完全不受影响：栏照旧吸顶。 */
  .tabbar--sticky-desktop-only {
    position: static;
  }
}

/* 分组箭头的翻转。 */
.tabbar__tab-arrow {
  flex: none;
  transition: transform 0.24s ease;
}
.tabbar__tab-arrow--open {
  transform: rotate(180deg);
}

/* 分组子项的展开/收起。
   宽度用 max-width 过渡（0 → 200px）：标签都只有两三个字、连内边距不到 80px，
   200px 是个足够宽的上限，所以过渡结束后类名移除、max-width 回落成 none 时
   不会有任何跳变。 */
.tabbar-tab-enter-active,
.tabbar-tab-leave-active {
  overflow: hidden;
  transition: max-width 0.24s ease, padding 0.24s ease, opacity 0.16s ease,
    transform 0.24s ease;
}
.tabbar-tab-enter-from,
.tabbar-tab-leave-to {
  max-width: 0;
  padding-right: 0;
  padding-left: 0;
  opacity: 0;
  transform: translateX(-10px);
}
.tabbar-tab-enter-to,
.tabbar-tab-leave-from {
  max-width: 200px;
}

/* 系统里关了动效就别硬播。 */
@media (prefers-reduced-motion: reduce) {
  .tabbar-tab-enter-active,
  .tabbar-tab-leave-active,
  .tabbar__tab-arrow {
    transition: none;
  }
}
</style>
