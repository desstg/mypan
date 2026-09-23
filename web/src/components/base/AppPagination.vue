<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";

// 通用分页器：上一页 / 页码 / 下一页 + 「跳至第 N 页」。
//
// 页码用省略号折叠（首、末、当前 ±1 恒定可见），所以总页数上百时也不会把
// 一整排按钮铺满屏幕。跳转框在换页后自动清空 —— 留着上一页的数字，用户会
// 以为它已经生效了。

const props = withDefaults(
  defineProps<{
    page: number;
    totalPages: number;
  }>(),
  {},
);

const emit = defineEmits<{ "update:page": [number] }>();

/** 省略号占位。页码从 1 起，用字符串当哨兵比用 0 更不容易和真实页码混淆。 */
const GAP = "gap";
type Gap = typeof GAP;

const jumpTo = ref<number | null>(null);

/** 可见的页码序列：首页、末页、当前页 ±1，中间的跳跃用省略号折叠。 */
const items = computed<(number | Gap)[]>(() => {
  const total = props.totalPages;
  const current = props.page;
  // 7 以内全列出来：折叠反而会让「1 … 5 6 7 … 9」这种比「1..9」占得还宽。
  if (total <= 7) {
    return Array.from({ length: total }, (_, i) => i + 1);
  }
  const start = Math.max(2, current - 1);
  const end = Math.min(total - 1, current + 1);
  const out: (number | Gap)[] = [1];
  if (start > 2) out.push(GAP);
  for (let p = start; p <= end; p++) out.push(p);
  if (end < total - 1) out.push(GAP);
  out.push(total);
  return out;
});

watch(
  () => props.page,
  () => {
    jumpTo.value = null;
  },
);

/** 越界与小数统一在这里收口：调用方不必各自钳制。 */
function go(target: number) {
  const next = Math.min(Math.max(1, Math.trunc(target)), props.totalPages);
  if (next === props.page) return;
  emit("update:page", next);
}

function jump() {
  const value = jumpTo.value;
  if (value == null || Number.isNaN(value)) return;
  go(value);
}
</script>

<template>
  <div v-if="totalPages > 1" class="pager">
    <AppButton
      type="button"
      variant="secondary"
      size="sm"
      :disabled="page <= 1"
      @click="go(page - 1)"
    >
      上一页
    </AppButton>

    <div class="pager__pages">
      <template v-for="(item, index) in items" :key="`${item}-${index}`">
        <span v-if="item === GAP" class="pager__gap" aria-hidden="true">…</span>
        <button
          v-else
          type="button"
          class="pager__page"
          :class="{ 'pager__page--active': item === page }"
          :aria-current="item === page ? 'page' : undefined"
          @click="go(item)"
        >
          {{ item }}
        </button>
      </template>
    </div>

    <AppButton
      type="button"
      variant="secondary"
      size="sm"
      :disabled="page >= totalPages"
      @click="go(page + 1)"
    >
      下一页
    </AppButton>

    <div class="pager__jump">
      <span class="pager__jump-label">跳至</span>
      <div class="pager__jump-input">
        <AppInput v-model.number="jumpTo" type="number" placeholder="页码" @keyup.enter="jump" />
      </div>
      <span class="pager__jump-label">页 / 共 {{ totalPages }} 页</span>
      <AppButton type="button" variant="ghost" size="sm" @click="jump">跳转</AppButton>
    </div>
  </div>
</template>

<style scoped>
.pager {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 8px 0 4px;
  color: var(--text-muted);
  font-size: 13px;
}

.pager__pages {
  display: flex;
  align-items: center;
  gap: 4px;
}

.pager__page {
  min-width: 30px;
  height: 30px;
  padding: 0 8px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}

.pager__page:hover {
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border-soft));
  color: var(--brand);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}

.pager__page--active,
.pager__page--active:hover {
  border-color: var(--brand);
  background: var(--brand);
  color: #fff;
  font-weight: 600;
  cursor: default;
}

.pager__page:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--brand) 58%, transparent);
  outline-offset: 2px;
}

.pager__gap {
  padding: 0 2px;
  color: var(--text-muted);
}

.pager__jump {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: 6px;
}

.pager__jump-label {
  white-space: nowrap;
}

.pager__jump-input {
  width: 76px;
}
</style>
