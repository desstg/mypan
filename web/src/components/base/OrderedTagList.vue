<script setup lang="ts">
import { computed, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import "@/styles/jav-editor.css";

// 带 ↑↓ 调序的字符串列表。
//
// 与 JavStringListEditor 的区别只有一点：这个能调优先级。画质方案里的
// 「优先分辨率 / 片源 / 编码」是**有序列**，位置即优先级，所以必须能排序。
// 项目里没有拖拽库，沿惯例用 ↑↓ 按钮。

const props = withDefaults(
  defineProps<{
    modelValue: string[];
    /** 预设候选，会以快捷按钮给出，减少手打错字。留空则是自由输入。 */
    suggestions?: string[];
    placeholder?: string;
    addLabel?: string;
    emptyHint?: string;
    inputHint?: string;
    disabled?: boolean;
  }>(),
  {
    suggestions: () => [],
    placeholder: "输入后回车添加",
    addLabel: "添加",
    emptyHint: "",
    inputHint: "",
    disabled: false,
  },
);

const emit = defineEmits<{ "update:modelValue": [string[]] }>();

const draft = ref("");

function update(next: string[]) {
  emit("update:modelValue", next);
}

function add(value?: string) {
  if (props.disabled) return;
  const raw = (value ?? draft.value).trim();
  if (!raw) return;
  // 去重（大小写不敏感）—— 同一个词出现两次只会让权重表出现重复项。
  if (props.modelValue.some((item) => item.toLowerCase() === raw.toLowerCase())) {
    draft.value = "";
    return;
  }
  update([...props.modelValue, raw]);
  draft.value = "";
}

function remove(index: number) {
  if (props.disabled) return;
  const next = [...props.modelValue];
  next.splice(index, 1);
  update(next);
}

function move(index: number, delta: number) {
  if (props.disabled) return;
  const target = index + delta;
  if (target < 0 || target >= props.modelValue.length) return;
  const next = [...props.modelValue];
  [next[index], next[target]] = [next[target], next[index]];
  update(next);
}

function clearAll() {
  if (props.disabled) return;
  update([]);
}

/** 还没被加进来的建议项。 */
const remaining = computed(() => {
  const used = new Set(props.modelValue.map((v) => v.toLowerCase()));
  return props.suggestions.filter((s) => !used.has(s.toLowerCase()));
});
</script>

<template>
  <div class="jav-editor">
    <div v-if="modelValue.length" class="jav-editor__chips">
      <span v-for="(item, index) in modelValue" :key="`${item}-${index}`" class="jav-chip">
        <span class="jav-chip__order">{{ index + 1 }}</span>
        <span class="jav-chip__text">{{ item }}</span>
        <button
          type="button"
          class="jav-chip__btn"
          title="上移（优先级更高）"
          :disabled="disabled || index === 0"
          @click="move(index, -1)"
        >
          ↑
        </button>
        <button
          type="button"
          class="jav-chip__btn"
          title="下移"
          :disabled="disabled || index === modelValue.length - 1"
          @click="move(index, 1)"
        >
          ↓
        </button>
        <button
          type="button"
          class="jav-chip__btn jav-chip__btn--remove"
          title="移除"
          :disabled="disabled"
          @click="remove(index)"
        >
          ×
        </button>
      </span>
    </div>
    <p v-else-if="emptyHint" class="jav-editor__empty">{{ emptyHint }}</p>

    <div class="jav-editor__add-row">
      <AppInput v-model="draft" :placeholder="placeholder" :disabled="disabled" @keyup.enter="add()" />
      <AppButton type="button" variant="secondary" size="sm" :disabled="disabled" @click="add()">
        {{ addLabel }}
      </AppButton>
      <AppButton
        v-if="modelValue.length"
        type="button"
        variant="ghost"
        size="sm"
        :disabled="disabled"
        @click="clearAll"
      >
        清空
      </AppButton>
    </div>

    <div v-if="remaining.length" class="ordered-tag-list__suggest">
      <span class="ordered-tag-list__suggest-label">常用：</span>
      <button
        v-for="item in remaining"
        :key="item"
        type="button"
        class="ordered-tag-list__suggest-btn"
        :disabled="disabled"
        @click="add(item)"
      >
        + {{ item }}
      </button>
    </div>

    <p v-if="inputHint" class="jav-editor__hint">{{ inputHint }}</p>
  </div>
</template>

<style scoped>
.ordered-tag-list__suggest {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}
.ordered-tag-list__suggest-label {
  color: var(--text-muted);
  font-size: 12px;
}
.ordered-tag-list__suggest-btn {
  border: 1px dashed var(--border);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-muted);
  padding: 2px 10px;
  font-size: 12px;
  cursor: pointer;
}
.ordered-tag-list__suggest-btn:hover:not(:disabled) {
  border-color: var(--brand);
  color: var(--brand);
}
.ordered-tag-list__suggest-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
