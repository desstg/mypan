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
  /** 行尾的浅色小字（如部数）。不给就不显示。 */
  hint?: string;
}

const props = withDefaults(
  defineProps<{
    /** 键 → 是否勾选。**缺项按 `missingChecked` 处理**（默认按勾选）。 */
    modelValue: Record<string, boolean>;
    options: CheckboxOption[];
    disabled?: boolean;
    /**
     * 键**不在** modelValue 里时算不算勾上。默认 true —— 与后端「番号元数据缺项回落全开」
     * 一致。语义相反的场景（如「番号墙隐藏的目录」：缺项 = 不隐藏）传 false。
     */
    missingChecked?: boolean;
  }>(),
  { disabled: false, missingChecked: true },
);

const emit = defineEmits<{ "update:modelValue": [Record<string, boolean>] }>();

function toggle(key: string, checked: boolean) {
  emit("update:modelValue", { ...props.modelValue, [key]: checked });
}

function isChecked(key: string): boolean {
  const raw = props.modelValue[key];
  if (raw === undefined) return props.missingChecked;
  return raw;
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
      <span v-if="opt.hint" class="settings-check__hint">{{ opt.hint }}</span>
    </label>
  </div>
</template>

<style scoped>
/* gap 由 .settings-check 给（见 styles/settings-panel.css），这里只调字号与颜色。 */
.settings-check__hint { color: var(--text-muted); font-size: 11px; }
</style>
