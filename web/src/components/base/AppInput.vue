<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    modelValue: string | number | null;
    type?: "text" | "password" | "number" | "date" | "time";
    placeholder?: string;
    disabled?: boolean;
    autocomplete?: string;
    ignoreAutofill?: boolean;
    /**
     * v-model 的修饰符。**必须声明**，否则它不会作为 prop 传进来：
     * Vue 只把声明过的 prop 挑出来，剩下的进 attrs —— 于是 `v-model.number`
     * 会被当成一个普通 attribute 落到下面那个 `<input>` 上（渲染成
     * `modelmodifiers="[object Object]"`），而这里永远读不到它。
     *
     * 实测过：给**组件**写 `v-model.number` 时，编译器不会像原生 input 那样自己
     * 转数字，它只生成 `modelModifiers: { number: true }` 让子组件自己处理。
     * 子组件不处理 = 拿到的一直是字符串。见下面 onInput。
     */
    modelModifiers?: { number?: boolean };
  }>(),
  {
    type: "text",
    placeholder: "",
    disabled: false,
    autocomplete: undefined,
    ignoreAutofill: false,
    modelModifiers: () => ({}),
  },
);
const emit = defineEmits<{ "update:modelValue": [string | number] }>();

function clearReadonly(event: FocusEvent) {
  (event.target as HTMLInputElement).removeAttribute("readonly");
}

function resolvedAutocomplete() {
  if (props.autocomplete) return props.autocomplete;
  if (props.ignoreAutofill) return props.type === "password" ? "new-password" : "off";
  return undefined;
}

/**
 * 输入事件 → 向父组件 emit 的值。
 *
 * 只做一件事：**带 `.number` 修饰符时把值转成数字**。这和原生
 * `<input v-model.number>` 的语义一致，也是这个组件以前缺掉的一环 ——
 * 以前无论写不写 `.number`，emit 出去的一律是字符串。
 *
 * 后果曾经很实际：`type="number"` 的输入框接一个 Go 里的 int 字段，用户一改数字
 * 保存就报 `cannot unmarshal string into Go struct field ... of type int`；
 * 不碰它反而没事（模型里本来就是个数字）。属于「静默坏了、还得用户先动它才炸」。
 *
 * 空串**原样传空串**，不转成 0：清空输入框是用户正在编辑的中间态，
 * 把它悄悄变成 0 会让人以为清不掉；真要校验也该由后端给出明确报错。
 *
 * 类型是 `string | number` 而不是 `string`：写 `.number` 的调用方拿到的是数字，
 * 而它多半会直接塞进一个 `number` 字段。收窄成 `string` 会逼着每个调用点
 * 手写 `Number(...)`，那正是这个修饰符本该替他们做的事。
 */
function onInput(event: Event) {
  const raw = (event.target as HTMLInputElement).value;
  if (props.modelModifiers?.number && raw !== "") {
    const n = Number(raw);
    if (!Number.isNaN(n)) {
      emit("update:modelValue", n);
      return;
    }
  }
  emit("update:modelValue", raw);
}
</script>

<template>
  <input
    class="app-input"
    :type="type"
    :value="modelValue ?? ''"
    :placeholder="placeholder"
    :disabled="disabled"
    :autocomplete="resolvedAutocomplete()"
    :readonly="ignoreAutofill || undefined"
    :autocapitalize="ignoreAutofill ? 'off' : undefined"
    :spellcheck="ignoreAutofill ? false : undefined"
    :data-1p-ignore="ignoreAutofill ? 'true' : undefined"
    :data-lpignore="ignoreAutofill ? 'true' : undefined"
    :data-form-type="ignoreAutofill ? 'other' : undefined"
    @focus="ignoreAutofill ? clearReadonly($event) : undefined"
    @input="onInput"
  />
</template>

<style scoped>
.app-input {
  width: 100%;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  transition: var(--transition);
}
.app-input:focus {
  outline: none;
  border-color: var(--brand);
}
.app-input:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
</style>
