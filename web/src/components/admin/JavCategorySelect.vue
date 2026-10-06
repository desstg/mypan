<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import { useDismissOnOutside } from "@/composables/useDismissOnOutside";

/**
 * 类别多选：触发器显示已选摘要，点开是一个「搜索框 + 捕获框」的面板。
 *
 * # 为什么自己写而不是用 AppDropdown
 *
 * AppDropdown 的浮层是**点击外部即关闭**的，而这个面板里要做多选 ——
 * 每点一项就关一次、还要重新点开，那是没法用的。这里要的是
 * 「点选项 → 面板留着，点面板外 → 才关」。
 *
 * # 选项来源
 *
 * `options` 由调用方给（本地影库出现过的全部标签，见后端 `/jav/categories`）。
 * 面板里的「刷新」是让调用方重新拉一次 —— 影库同步完之后可能多了新标签，
 * 而订阅弹窗可能已经开着。
 *
 * # 样式
 *
 * 一律走 tokens.css 的变量与既有的 .jav-* 语言，与弹窗里其它控件同一套观感；
 * 没有照搬源码那套紫边圆角，那与本项目的主题不搭。
 */
const props = withDefaults(
  defineProps<{
    modelValue: string[];
    options: string[];
    /** 触发器上的占位文案（未选中任何项时显示）。 */
    placeholder?: string;
    /** 拉选项失败时的提示，直接显示在面板里。 */
    error?: string;
    /** 刷新中（按钮转圈 / 禁用）。 */
    refreshing?: boolean;
  }>(),
  { placeholder: "选择类别…", error: "", refreshing: false },
);

const emit = defineEmits<{
  "update:modelValue": [string[]];
  /** 请求调用方刷新选项。 */
  refresh: [];
}>();

const open = ref(false);
const keyword = ref("");
const rootRef = ref<HTMLElement | null>(null);
const panelRef = ref<HTMLElement | null>(null);
const panelStyle = ref<Record<string, string>>({});

/**
 * 浮层用 **Teleport 到 body + 固定定位**，不走「挂在触发器下面的绝对定位」。
 *
 * 为什么必须这样：这个面板比触发器高得多（工具条 + 最多 200px 的列表）。
 * 弹窗的内容区 `.jav-modal__body` 是 `overflow-y: auto`，判据又正好排在弹窗
 * 下半部 —— 绝对定位的面板会被内容区裁掉一大截。实测面板底边 597px、
 * 弹窗底边 539px，列表最后几项**点不到**，看起来就像「类别不全」。
 *
 * 落到 body 之后不受任何祖先的 overflow 影响。代价是 z-index 必须高过弹窗：
 * 建订阅那张抬到了 10045（见 JavSubscribePanel 的 .jav-modal--above-drawer），
 * 所以这里取 `--z-dropdown`（10050）—— AppDropdown 的浮层同款，
 * 它同样要压过各种弹窗与抽屉，本项目的既有先例。
 */
async function refreshPosition() {
  await nextTick();
  await nextTick();
  const anchor = rootRef.value;
  if (!anchor) return;
  const rect = anchor.getBoundingClientRect();
  const menuH = panelRef.value?.offsetHeight || 260;
  const gap = 4;
  let top = rect.bottom + gap;
  let transform = "none";
  // 下方放不下就翻到触发器上方 —— 判据附近的字段多，往下顶会盖住「保存」。
  if (top + menuH > window.innerHeight - 8) {
    top = rect.top - gap;
    transform = "translateY(-100%)";
  }  panelStyle.value = {
    left: `${Math.max(8, Math.min(rect.left, window.innerWidth - rect.width - 8))}px`,
    width: `${rect.width}px`,
    top: `${top}px`,
    transform,
  };
}

function onScrollOrResize() {
  if (open.value) void refreshPosition();
}

watch(open, (v) => {
  if (v) {
    void refreshPosition();
    window.addEventListener("scroll", onScrollOrResize, true);
    window.addEventListener("resize", onScrollOrResize);
  } else {
    window.removeEventListener("scroll", onScrollOrResize, true);
    window.removeEventListener("resize", onScrollOrResize);
  }
});
const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase();
  if (!q) return props.options;
  return props.options.filter((o) => o.toLowerCase().includes(q));
});

const selected = computed(() => {
  // 顺序按 options 走，这样已选的排列与面板里看到的一致，不会因为点选顺序跳来跳去。
  const set = new Set(props.modelValue ?? []);
  return props.options.filter((o) => set.has(o));
});

const summary = computed(() => {
  const list = selected.value;
  if (!list.length) return "";
  if (list.length <= 3) return list.join("、");
  return `${list.slice(0, 3).join("、")} 等 ${list.length} 项`;
});

// 触发器上那颗计数角标要数**已选总数**，不能数 selected（它按 options 过滤）。
// 已选的项如果不在 options 里（影库刷新后那个标签没了），用 selected.length
// 会把它漏掉 —— 而它照样会被提交上去，数字对不上就是骗人。
const selectedCount = computed(() => (props.modelValue ?? []).length);

function toggle(value: string) {
  const current = props.modelValue ?? [];
  emit(
    "update:modelValue",
    current.includes(value) ? current.filter((v) => v !== value) : [...current, value],
  );
}

/** 清空**当前搜索可见的**那些。与源码面板里那颗「清空」同一语义。 */
function clearVisible() {
  const visible = new Set(filtered.value);
  emit("update:modelValue", (props.modelValue ?? []).filter((v) => !visible.has(v)));
}

function toggleOpen() {
  open.value = !open.value;
  if (open.value) keyword.value = "";
}

useDismissOnOutside(open, computed(() => [rootRef.value, panelRef.value]), () => {
  open.value = false;
});

// 弹窗关掉时组件会被 v-if 摘掉，但「点开面板 → 直接关弹窗」这条路上
// 文档级的 click 监听要跟着撤（useDismissOnOutside 在 onUnmounted 里做了）。
onUnmounted(() => {
  open.value = false;
  window.removeEventListener("scroll", onScrollOrResize, true);
  window.removeEventListener("resize", onScrollOrResize);
});

// 选项被清空（比如刷新后一个标签都没有）时把面板收起来，免得留一个空壳。
watch(
  () => props.options.length,
  (n) => {
    if (n === 0) open.value = false;
  },
);

defineExpose({ close: () => (open.value = false) });
</script>

<template>
  <div ref="rootRef" class="jav-cat">
    <button
      type="button"
      class="jav-cat__trigger"
      :class="{ 'jav-cat__trigger--open': open, 'jav-cat__trigger--filled': Boolean(summary) }"
      :aria-expanded="open"
      @click="toggleOpen"
    >
      <span class="jav-cat__trigger-text" :class="{ 'jav-cat__trigger-text--ph': !summary }">
        {{ summary || placeholder }}
      </span>
      <span class="jav-cat__tail">
        <span v-if="summary && selectedCount > 3" class="jav-cat__count">{{ selectedCount }}</span>
        <span class="jav-cat__chevron" :class="{ 'jav-cat__chevron--open': open }">▾</span>
      </span>
    </button>

    <Teleport to="body">
    <div v-if="open" ref="panelRef" class="jav-cat__panel" :style="panelStyle">
      <div class="jav-cat__tools">
        <div class="jav-cat__search">
          <AppInput v-model="keyword" placeholder="搜索选项…" />
        </div>
        <AppButton
          type="button"
          variant="secondary"
          size="sm"
          :disabled="refreshing"
          @click="emit('refresh')"
        >
          {{ refreshing ? "刷新中…" : "刷新" }}
        </AppButton>
        <AppButton
          type="button"
          variant="ghost"
          size="sm"
          :disabled="!filtered.length"
          title="取消当前搜索结果里已勾选的项"
          @click="clearVisible"
        >
          清空
        </AppButton>
      </div>

      <div v-if="error" class="jav-cat__error">{{ error }}</div>
      <div v-else-if="!options.length" class="jav-cat__empty">
        影库里还没有任何标签。同步过影库或抓过影片详情之后，这里才会有可选项。
      </div>
      <div v-else-if="!filtered.length" class="jav-cat__empty">没有匹配「{{ keyword }}」的类别。</div>

      <ul v-else class="jav-cat__list">
        <li v-for="opt in filtered" :key="opt" class="jav-cat__item">
          <label class="jav-cat__label">
            <input
              type="checkbox"
              :checked="(modelValue ?? []).includes(opt)"
              @change="toggle(opt)"
            />
            <span :title="opt">{{ opt }}</span>
          </label>
        </li>
      </ul>
    </div>
    </Teleport>
  </div>
</template>

<style scoped>
.jav-cat {
  position: relative;
}

/* 触发器与 AppSelect 逐项对齐（同样的 padding / 边框 / 圆角 / 字号），
   否则同一张弹窗里两个「选择框」会一个高一个矮。 */
.jav-cat__trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 14px;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.jav-cat__trigger:hover {
  border-color: var(--brand);
}

.jav-cat__trigger--open {
  border-color: var(--brand);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--brand) 12%, transparent);
}

.jav-cat__trigger-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jav-cat__trigger-text--ph {
  color: var(--text-muted);
}

.jav-cat__tail {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

/* 超过 3 项时触发器上写的是「前三项 + 等 N 项」，那颗数字胶囊把 N 单独挑出来 ——
   一串逗号顿号里数不清到底选了几个。 */
.jav-cat__count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: var(--radius-pill);
  background: var(--brand);
  color: #fff;
  font-size: 11px;
  line-height: 16px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.jav-cat__chevron {
  flex: none;
  color: var(--text-muted);
  transition: transform 0.15s ease;
}

.jav-cat__chevron--open {
  transform: rotate(180deg);
}

/* 面板：Teleport 到 body 后的固定定位浮层。位置由 refreshPosition 算好内联写进来。
   z-index 取 --z-dropdown：要压过弹窗（建订阅那张是 10045），与 AppDropdown 同档。 */
.jav-cat__panel {
  position: fixed;
  z-index: var(--z-dropdown);
  display: flex;
  flex-direction: column;
  max-height: 260px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  box-shadow: var(--shadow-pop);
}

.jav-cat__tools {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border-soft);
}

.jav-cat__search {
  flex: 1 1 auto;
  min-width: 0;
}

.jav-cat__list {
  margin: 0;
  padding: 4px 0 0;
  list-style: none;
  overflow-y: auto;
  /* 上面那堆工具条占掉约 52px，剩下的都留给列表。 */
  max-height: 196px;
}

.jav-cat__item + .jav-cat__item {
  margin-top: 2px;
}

.jav-cat__label {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
}

.jav-cat__label:hover {
  background: var(--border-soft);
}

.jav-cat__label span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jav-cat__empty,
.jav-cat__error {
  padding: 10px 4px;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-muted);
}

.jav-cat__error {
  color: var(--danger);
}
</style>
