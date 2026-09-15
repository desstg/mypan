<script setup lang="ts">
import { ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import type { MediaOrganizeJavReplaceRule } from "@/api/mediaOrganize";
import "@/styles/jav-editor.css";

// 「替换字符」编辑器。
//
// 交互与「删除字符」完全一致：规则以 chip 形式排在上面（可上移/下移/删除），
// 底下是输入区。区别只是这里一条规则有「来源 + 目标」两个输入。
//
// 顺序有意义（按列表顺序从左到右一次扫描，先命中的先生效），所以每条 chip 上带调序按钮。

const props = defineProps<{
  modelValue: MediaOrganizeJavReplaceRule[];
}>();

const emit = defineEmits<{ "update:modelValue": [MediaOrganizeJavReplaceRule[]] }>();

const fromDraft = ref("");
const toDraft = ref("");

function update(next: MediaOrganizeJavReplaceRule[]) {
  emit("update:modelValue", next);
}

function add() {
  const from = fromDraft.value.trim();
  if (!from) return;
  const list = props.modelValue ?? [];
  // 匹配不区分大小写，所以 `4K` 和 `4k` 是同一条 —— 加了也不会生效，不如拦下来
  if (list.some((r) => r.from.toLowerCase() === from.toLowerCase())) {
    fromDraft.value = "";
    toDraft.value = "";
    return;
  }
  update([...list, { from, to: toDraft.value.trim() }]);
  fromDraft.value = "";
  toDraft.value = "";
}

function remove(index: number) {
  const next = [...(props.modelValue ?? [])];
  next.splice(index, 1);
  update(next);
}

function move(index: number, delta: number) {
  const next = [...(props.modelValue ?? [])];
  const target = index + delta;
  if (target < 0 || target >= next.length) return;
  [next[index], next[target]] = [next[target], next[index]];
  update(next);
}
</script>

<template>
  <div class="jav-editor">
    <div v-if="modelValue.length" class="jav-editor__chips">
      <span
        v-for="(rule, index) in modelValue"
        :key="`${rule.from}-${index}`"
        class="jav-chip"
        :title="index === 0 ? '优先级最高' : `优先级 ${index + 1}`"
      >
        <span class="jav-chip__text">{{ rule.from }}</span>
        <span class="jav-chip__arrow">→</span>
        <span class="jav-chip__text">{{ rule.to || "（删除）" }}</span>
        <button
          type="button"
          class="jav-chip__btn"
          title="上移（优先级更高）"
          :disabled="index === 0"
          @click="move(index, -1)"
        >
          ↑
        </button>
        <button
          type="button"
          class="jav-chip__btn"
          title="下移"
          :disabled="index === modelValue.length - 1"
          @click="move(index, 1)"
        >
          ↓
        </button>
        <button type="button" class="jav-chip__btn jav-chip__btn--remove" title="删除" @click="remove(index)">
          ×
        </button>
      </span>
    </div>
    <div v-else class="jav-editor__empty">暂无替换规则</div>

    <AppInput v-model="fromDraft" placeholder="原字符，如 4K" @keyup.enter="add" />
    <div class="jav-editor__add-row">
      <AppInput v-model="toDraft" placeholder="替换为，如 -4K（留空 = 删除）" @keyup.enter="add" />
      <AppButton type="button" variant="primary" size="sm" :disabled="!fromDraft.trim()" @click="add">
        + 添加
      </AppButton>
    </div>

    <div class="jav-editor__hint">
      不区分大小写。同一个位置有多条命中时取<strong>最长</strong>的那条
      （如 <code>4K</code> 与 <code>4k60</code> 都命中时用后者）；等长时取 ↑↓ 排在前面的。
      替换结果不会被再次匹配，所以不会越改越长。
    </div>
  </div>
</template>
