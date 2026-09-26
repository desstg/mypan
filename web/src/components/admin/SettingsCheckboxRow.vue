<script setup lang="ts">
// 一排复选框（横向，装不下自动换行）。
//
// 项目里原本没有这个形态：布尔项用 SettingsBoolSegment（开 / 关两段），多选用 chip
// （JavSubscribePanel 里那排）。而「番号元数据」要的是**打勾那种、横排排成一排** ——
// 六个等价的开关，两个按钮的段控件表达不了，chip 又不像复选框。
//
// 用真 `<input type="checkbox">` 而不是自己画：键盘可达、读屏器认、聚焦圈由浏览器给，
// 样式只改外观（accent-color）不改行为。

export interface CheckboxOption {
  key: string;
  label: string;
}

const props = withDefaults(
  defineProps<{
    /** 键 → 是否勾选。**缺项按勾选**（与后端「缺项回落全开」一致） */
    modelValue: Record<string, boolean>;
    options: CheckboxOption[];
    disabled?: boolean;
  }>(),
  { disabled: false },
);

const emit = defineEmits<{ "update:modelValue": [Record<string, boolean>] }>();

function toggle(key: string, checked: boolean) {
  emit("update:modelValue", { ...props.modelValue, [key]: checked });
}

function isChecked(key: string): boolean {
  return props.modelValue[key] !== false;
}
</script>

<template>
  <div class="settings-checks">
    <label v-for="opt in options" :key="opt.key" class="settings-check">
      <input
        type="checkbox"
        :checked="isChecked(opt.key)"
        :disabled="disabled"
        @change="toggle(opt.key, ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ opt.label }}</span>
    </label>
  </div>
</template>
