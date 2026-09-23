<script setup lang="ts">
import { onUnmounted, watch } from "vue";
import { lockPageScroll, unlockPageScroll } from "@/utils/scrollLock";
import { isTopModal, popModal, pushModal } from "@/composables/modalStack";

const props = withDefaults(
  defineProps<{
    open: boolean;
    title?: string;
    size?: "sm" | "md" | "lg" | "wide" | "account" | "branch";
    // bare：仅渲染弹窗外壳与默认插槽，去掉默认头部/内边距，由内容自带头部时使用。
    bare?: boolean;
    // nested：叠在另一层弹窗之上（目录选择等）。
    nested?: boolean;
    // footerDivider：底部操作区上方是否显示分割线（默认无）。
    footerDivider?: boolean;
    // bodyFlush：内容区贴边（去掉左右内边距），用于内容自带贴边布局的弹窗。
    bodyFlush?: boolean;
    // headPlain：极简头部（白底、无分割线、与内容一体），用于极简风弹窗。
    headPlain?: boolean;
    // steady：**定高**弹窗（88vh），内容区自己滚。
    //
    // 内容是长列表时用它。不定高的话面板会随内容「一下大一下小」——
    // 切页、翻页、筛选之后条数一变，鼠标还没移开窗口就缩了。
    // 与订阅页那张影片弹窗的 .jav-modal__panel--steady 是同一套观感。
    steady?: boolean;
  }>(),
  {
    title: "",
    size: "md",
    bare: false,
    nested: false,
    footerDivider: false,
    bodyFlush: false,
    headPlain: false,
    steady: false,
  },
);
const emit = defineEmits<{ close: [] }>();

const myToken = Symbol("modal");

function onKey(e: KeyboardEvent) {
  if (e.key === "Escape" && isTopModal(myToken)) emit("close");
}

function lockPageScrollState(lock: boolean) {
  if (lock) lockPageScroll();
  else unlockPageScroll();
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      pushModal(myToken);
      window.addEventListener("keydown", onKey);
      lockPageScrollState(true);
    } else {
      popModal(myToken);
      window.removeEventListener("keydown", onKey);
      lockPageScrollState(false);
    }
  },
);
onUnmounted(() => {
  popModal(myToken);
  window.removeEventListener("keydown", onKey);
  lockPageScrollState(false);
});
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <!-- overlay 滚动：溢出时滚动条在视口右侧，而非弹窗内部 -->
      <div v-if="open" class="overlay" :class="{ 'overlay--nested': nested }">
        <div class="overlay__center">
          <div
            class="modal"
            :class="[bare ? 'modal--bare' : `modal--${size}`, { 'modal--steady': steady }]"
            role="dialog"
          >
            <template v-if="bare">
              <slot />
            </template>
            <template v-else>
              <header class="modal__head" :class="{ 'modal__head--plain': headPlain }">
                <slot name="header">
                  <h3 v-if="title" class="modal__title">{{ title }}</h3>
                </slot>
                <button class="modal__close" aria-label="关闭" @click="emit('close')">×</button>
              </header>
              <div class="modal__body" :class="{ 'modal__body--flush': bodyFlush }">
                <slot />
              </div>
              <footer
                v-if="$slots.footer"
                class="modal__foot"
                :class="{ 'modal__foot--divider': footerDivider }"
              >
                <slot name="footer" />
              </footer>
            </template>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  overflow-y: auto;
  background: rgba(15, 23, 42, 0.45);
}
.overlay--nested {
  z-index: calc(var(--z-modal) + 40);
}

.overlay__center {
  min-height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 20px;
  box-sizing: border-box;
}

.modal {
  width: 100%;
  background: var(--surface);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-pop);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.modal--sm {
  max-width: 420px;
}
.modal--md {
  max-width: 620px;
}
.modal--lg {
  max-width: 860px;
}
/* wide：给「一屏卡片网格」用（如分享者分享过的影片）。
   网格本身按视口分列，窄了会把卡片挤成一溜小方块。 */
.modal--wide {
  max-width: 1000px;
}
.modal--account {
  max-width: 700px;
  width: 90%;
}
.modal--account .modal__head {
  padding: 14px 24px;
}
.modal--account .modal__body {
  padding: 16px 20px 18px;
}
.modal--branch {
  max-width: 900px;
  width: 90%;
}
.modal--branch .modal__body {
  min-height: 540px;
  max-height: min(86vh, 620px);
  display: flex;
  flex-direction: column;
}

/* 定高弹窗：面板高度写死，内容区自己滚（见 steady prop）。
 *
 * 两条都必要：
 *   - `min-height: 0`：flex 子项默认 min-height:auto，不写它内容再长也撑不出滚动条；
 *   - `overflow-y: auto` 要盖过 .modal__body--flush 的 `overflow: hidden`
 *     （那条是为了把内容裁进圆角）。这里靠**特异性**赢：`.modal--steady .modal__body`
 *     是两个类，`.modal__body--flush` 是一个。 */
.modal--steady {
  height: 88vh;
}
.modal--steady .modal__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
}
.modal--bare {
  width: auto;
  max-width: min(94vw, 960px);
  overflow: visible;
}

.modal__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 24px;
  background: var(--panel-head-bg);
  border-bottom: 1px solid var(--border);
  border-radius: var(--radius-md) var(--radius-md) 0 0;
}
.modal__head--plain {
  background: transparent;
  border-bottom: 0;
  padding-bottom: 12px;
}
.modal__title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
}
.modal__close {
  border: none;
  background: transparent;
  font-size: 22px;
  line-height: 1;
  color: var(--text-muted);
}
.modal__close:hover {
  color: var(--text);
}
.modal__body {
  padding: 20px;
}
.modal__body--flush {
  padding: 0;
  /* 贴边内容（侧栏等自带背景色）裁进弹窗圆角，避免盖出直角 */
  border-radius: 0 0 var(--radius-md) var(--radius-md);
  overflow: hidden;
}
.modal__foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 4px 20px 18px;
}
.modal__foot--divider {
  border-top: 1px solid var(--border);
  padding-top: 14px;
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
