<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

interface Option {
  value: string | number | boolean;
  label: string;
  disabled?: boolean;
  tag?: string;
  /** 分组标题：同一个 group 的选项会被归到一块，并在**变化处**渲染一行小标题。
   *  不给 group 就与以前完全一样（一个平铺列表）。 */
  group?: string;
}

const props = withDefaults(
  defineProps<{
    modelValue: string | number | boolean | null;
    options: Option[];
    placeholder?: string;
    disabled?: boolean;
  }>(),
  { placeholder: "请选择", disabled: false },
);
const emit = defineEmits<{ "update:modelValue": [string | number | boolean] }>();

const open = ref(false);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});

const selectedLabel = computed(
  () => props.options.find((o) => o.value === props.modelValue)?.label ?? "",
);

/**
 * 渲染行：选项 + 分组标题。
 *
 * 分组标题只在**组名变化处**插一行（相邻同名只出一行）—— 这样调用方只管给每个
 * 选项写上 group，不需要自己算「要不要出标题」。不给 group 时 rows 就是 options，
 * 与以前逐字相同。
 */
type SelectRow = { kind: "group"; name: string } | { kind: "option"; opt: Option };

const rows = computed<SelectRow[]>(() => {
  const out: SelectRow[] = [];
  props.options.forEach((opt, i) => {
    if (opt.group && opt.group !== props.options[i - 1]?.group) {
      out.push({ kind: "group", name: opt.group });
    }
    out.push({ kind: "option", opt });
  });
  return out;
});

async function positionMenu() {
  await nextTick();
  await nextTick();
  const trigger = triggerRef.value;
  const menu = menuRef.value;
  if (!trigger || !menu) return;
  const rect = trigger.getBoundingClientRect();
  const menuH = menu.offsetHeight;
  const gap = 4;
  let top = rect.bottom + gap;
  if (top + menuH > window.innerHeight - 8) {
    top = Math.max(8, rect.top - menuH - gap);
  }
  menuStyle.value = {
    top: `${top}px`,
    left: `${rect.left}px`,
    width: `${rect.width}px`,
  };
}

function close() {
  open.value = false;
}

function toggle() {
  if (props.disabled) return;
  open.value = !open.value;
}

function choose(opt: Option) {
  if (opt.disabled) return;
  emit("update:modelValue", opt.value);
  close();
}

function onScrollOrResize() {
  if (open.value) void positionMenu();
}

watch(open, (v) => {
  if (v) void positionMenu();
});

onMounted(() => {
  window.addEventListener("scroll", onScrollOrResize, true);
  window.addEventListener("resize", onScrollOrResize);
});
onUnmounted(() => {
  window.removeEventListener("scroll", onScrollOrResize, true);
  window.removeEventListener("resize", onScrollOrResize);
});
</script>

<template>
  <div class="select" :class="{ 'select--disabled': disabled, 'select--open': open }">
    <button
      ref="triggerRef"
      type="button"
      class="select__trigger"
      :disabled="disabled"
      @click="toggle"
    >
      <span :class="{ select__placeholder: !selectedLabel }">
        {{ selectedLabel || placeholder }}
      </span>
      <span class="select__arrow" :class="{ 'select__arrow--open': open }">▾</span>
    </button>

    <Teleport to="body">
      <template v-if="open">
        <div class="select__backdrop" @click="close" />
        <ul ref="menuRef" class="select__menu" :style="menuStyle">
          <template v-for="(row, i) in rows" :key="row.kind === 'group' ? `g:${row.name}:${i}` : String(row.opt.value)">
            <li v-if="row.kind === 'group'" class="select__group">{{ row.name }}</li>
            <li
              v-else
              class="select__option"
              :class="{
                'select__option--active': row.opt.value === modelValue,
                'select__option--disabled': row.opt.disabled,
              }"
              @click="choose(row.opt)"
            >
              <span class="select__option-row">
                <span>{{ row.opt.label }}</span>
                <span v-if="row.opt.tag" class="select__option-tag">{{ row.opt.tag }}</span>
              </span>
            </li>
          </template>
          <li v-if="!options.length" class="select__empty">无可选项</li>
        </ul>
      </template>
    </Teleport>
  </div>
</template>

<style scoped>
.select {
  position: relative;
  width: 100%;
  min-width: 0;
}
.select__trigger {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text);
  transition: var(--transition);
}
.select__trigger:hover:not(:disabled) {
  border-color: var(--brand);
}
.select--open .select__trigger {
  border-color: var(--brand);
  box-shadow: 0 0 0 2px rgba(76, 116, 223, 0.12);
}
.select--disabled .select__trigger {
  opacity: 0.55;
  cursor: not-allowed;
}
.select__placeholder {
  color: var(--text-muted);
}
.select__arrow {
  color: var(--text-muted);
  transition: transform var(--transition);
}
.select__arrow--open {
  transform: rotate(180deg);
}
</style>

<style>
.select__backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--z-popover);
}
.select__menu {
  position: fixed;
  z-index: calc(var(--z-popover) + 1);
  margin: 0;
  list-style: none;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-pop);
  max-height: 260px;
  overflow-y: auto;
  padding: 4px;
  box-sizing: border-box;
}
.select__option {
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 14px;
  color: var(--text);
}
.select__option-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.select__option:hover {
  background: var(--border-soft);
}
.select__option--active {
  color: var(--brand);
  font-weight: 600;
}
.select__option--disabled {
  color: var(--text-muted);
  cursor: not-allowed;
}
.select__option--disabled:hover {
  background: transparent;
}
/* 分组标题：小号、灰、不可点（它不是一个选项，别让它看起来像）。 */
.select__group {
  padding: 7px 10px 3px;
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
  user-select: none;
  cursor: default;
}
.select__option-tag {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 500;
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--warning) 14%, var(--surface));
  color: #b45309;
}
.select__empty {
  padding: 8px 12px;
  color: var(--text-muted);
}
</style>
