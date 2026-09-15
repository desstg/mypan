<script setup lang="ts">
import { ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import "@/styles/jav-editor.css";

// 字符串列表编辑器：显示为 chip 列表，可加一条 / 删一条 / 调序 / 一键清空。
//
// 观感与「替换字符」编辑器共用 jav-editor.css —— 两个编辑器必须长得一样。

const props = withDefaults(
  defineProps<{
    modelValue: string[];
    placeholder?: string;
    /** 列表为空时的提示 */
    emptyHint?: string;
    /** 底部说明 */
    inputHint?: string;
    disabled?: boolean;
  }>(),
  { placeholder: "输入后回车添加", emptyHint: "暂无条目", inputHint: "", disabled: false },
);

const emit = defineEmits<{ "update:modelValue": [string[]] }>();

const draft = ref("");

function add() {
  const value = draft.value.trim();
  if (!value) return;
  const list = props.modelValue ?? [];
  // 匹配不区分大小写，重复添加不会生效，直接拦掉
  if (list.some((item) => item.toLowerCase() === value.toLowerCase())) {
    draft.value = "";
    return;
  }
  emit("update:modelValue", [...list, value]);
  draft.value = "";
}

function remove(index: number) {
  const next = [...(props.modelValue ?? [])];
  next.splice(index, 1);
  emit("update:modelValue", next);
}

function clearAll() {
  emit("update:modelValue", []);
}

function move(index: number, delta: number) {
  const list = [...(props.modelValue ?? [])];
  const target = index + delta;
  if (target < 0 || target >= list.length) return;
  [list[index], list[target]] = [list[target], list[index]];
  emit("update:modelValue", list);
}
</script>

<template>
  <div class="jav-editor">
    <div v-if="modelValue.length" class="jav-editor__chips">
      <span v-for="(item, index) in modelValue" :key="`${item}-${index}`" class="jav-chip">
        <span class="jav-chip__text">{{ item }}</span>
        <button
          type="button"
          class="jav-chip__btn"
          title="上移（优先级更高）"
          :disabled="index === 0 || disabled"
          @click="move(index, -1)"
        >
          ↑
        </button>
        <button
          type="button"
          class="jav-chip__btn"
          title="下移"
          :disabled="index === modelValue.length - 1 || disabled"
          @click="move(index, 1)"
        >
          ↓
        </button>
        <button
          type="button"
          class="jav-chip__btn jav-chip__btn--remove"
          title="删除"
          :disabled="disabled"
          @click="remove(index)"
        >
          ×
        </button>
      </span>
    </div>
    <div v-else class="jav-editor__empty">{{ emptyHint }}</div>

    <div class="jav-editor__add-row">
      <AppInput v-model="draft" :placeholder="placeholder" :disabled="disabled" @keyup.enter="add" />
      <AppButton type="button" variant="primary" size="sm" :disabled="disabled || !draft.trim()" @click="add">
        + 添加
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
    <div v-if="inputHint" class="jav-editor__hint">{{ inputHint }}</div>
  </div>
</template>
