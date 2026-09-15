<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";

export interface ProxyFieldOption {
  value: string;
  label: string;
  disabled?: boolean;
  tag?: string;
}

export interface ProxyField {
  key: string;
  label: string;
  helpTitle?: string;
  helpBody?: string;
  placeholder?: string;
  type?: "text" | "password" | "select" | "switch" | "segmented-text";
  inputmode?: "text" | "numeric";
  options?: ProxyFieldOption[];
  segmentKey?: string;
  // switch 专用
  switchLabel?: string;
  switchHint?: string;
  switchTag?: string;
  disabled?: boolean;
  hidden?: boolean;
}

export interface ProxyWorkspaceItem {
  id: string;
  name: string;
  running: boolean;
  port?: string;
  subtitle?: string;
  lastError?: string;
}

const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    caption: string;
    icon: string;
    subtitle: string;
    items: ProxyWorkspaceItem[];
    selectedId: string;
    fields: ProxyField[];
    namePlaceholder?: string;
    // 名称是否可编辑（夸克账号名来自网盘，不可改）
    nameEditable?: boolean;
    // 反代入口（夸克无入口，可隐藏）
    showEntry?: boolean;
    entryUrl?: string;
    entryRunning?: boolean;
    entryHelpTitle?: string;
    entryHelpBody?: string;
    // 底部按钮
    showTest?: boolean;
    showRefresh?: boolean;
    refreshing?: boolean;
    testing?: boolean;
    saving?: boolean;
    saveDisabled?: boolean;
    saveLabel?: string;
    addLabel?: string;
    removeLabel?: string;
    // 允许删除当前配置
    deletable?: boolean;
    // 允许添加配置（Emby 多配置显示，飞牛暂不显示）
    addable?: boolean;
  }>(),
  {
    namePlaceholder: "例如：家庭 Emby",
    nameEditable: true,
    showEntry: true,
    entryUrl: "",
    entryRunning: false,
    entryHelpTitle: "反代入口说明",
    entryHelpBody: "在播放器里添加服务器时，填这个地址。注意不是上面的服务地址，别填混了。",
    showTest: true,
    showRefresh: false,
    refreshing: false,
    testing: false,
    saving: false,
    saveDisabled: false,
    saveLabel: "保存",
    addLabel: "＋ 添加配置",
    removeLabel: "删除配置",
    deletable: true,
    addable: true,
  },
);

const emit = defineEmits<{
  select: [id: string];
  add: [];
  remove: [];
  test: [];
  refresh: [];
  copy: [];
  save: [];
  cancel: [];
}>();

const form = defineModel<Record<string, string>>({ required: true });

const currentName = computed(() => form.value.name?.trim() || "未命名");

function itemSubtitle(item: ProxyWorkspaceItem) {
  if (item.subtitle != null && item.subtitle !== "") return item.subtitle;
  return item.running ? `:${item.port || "?"} · 运行中` : "未监听";
}

// 标题行内编辑名称：点击铅笔进入编辑，回车/失焦提交，Esc 取消
const nameEditing = ref(false);
const nameInput = ref<HTMLInputElement | null>(null);
const nameBeforeEdit = ref("");

function startEditName() {
  nameBeforeEdit.value = form.value.name?.trim() || "";
  nameEditing.value = true;
  nextTick(() => {
    nameInput.value?.focus();
    nameInput.value?.select();
  });
}

function commitName() {
  if (!nameEditing.value) return;
  const value = form.value.name?.trim() || "";
  form.value.name = value || nameBeforeEdit.value || "未命名";
  nameEditing.value = false;
}

function cancelName() {
  form.value.name = nameBeforeEdit.value;
  nameEditing.value = false;
}
</script>

<template>
  <AppModal :open="open" size="lg" :title="title" body-flush @close="emit('cancel')">
    <div class="ws">
      <!-- 左侧配置列表 -->
        <aside class="ws-side">
          <div class="ws-side__cap">{{ caption }}</div>
          <div class="ws-side__list">
            <div
              v-for="item in items"
              :key="item.id"
              class="ws-side__item"
              :class="{ active: item.id === selectedId }"
              @click="emit('select', item.id)"
            >
              <span class="ws-side__ic">{{ icon }}</span>
              <span class="ws-side__tx">
                <b>{{ item.id === selectedId ? currentName : item.name }}</b>
                <small>{{ itemSubtitle(item) }}</small>
              </span>
              <span class="ws-side__st" :class="{ on: item.running }" />
            </div>
            <div v-if="!items.length" class="ws-side__empty">还没有配置<br>点下方「添加配置」新建</div>
          </div>
          <button v-if="addable" type="button" class="ws-add" @click="emit('add')">{{ addLabel }}</button>
        </aside>

        <!-- 右侧编辑区 -->
        <section class="ws-main">
          <div class="ws-main__head">
            <div class="ws-main__title-wrap">
              <template v-if="nameEditing">
                <input
                  ref="nameInput"
                  v-model.trim="form.name"
                  class="ws-name-edit"
                  :placeholder="namePlaceholder"
                  @keydown.enter.prevent="commitName"
                  @keydown.esc.prevent="cancelName"
                  @blur="commitName"
                />
              </template>
              <template v-else>
                <span class="ws-main__title">{{ currentName }}</span>
                <button v-if="nameEditable" type="button" class="ws-name-edit-btn" title="修改名称" @click="startEditName">
                  <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M11.3 2.3a1.5 1.5 0 0 1 2.4 2.4L6.2 12.2 3 13l.8-3.2z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" /></svg>
                </button>
              </template>
              <div class="ws-main__sub">{{ subtitle }}</div>
            </div>
            <button v-if="deletable" type="button" class="ws-del" @click="emit('remove')">{{ removeLabel }}</button>
          </div>

          <div
            v-for="field in fields"
            :key="field.key"
            class="ws-field"
            :class="{ 'ws-field--hidden': field.hidden }"
            :aria-hidden="field.hidden || undefined"
          >
            <div class="ws-field__head">
              <span class="ws-field__label">{{ field.label }}</span>
              <SettingsHelpTooltip v-if="field.helpTitle" :title="field.helpTitle">
                <div v-html="field.helpBody" />
              </SettingsHelpTooltip>
            </div>

            <!-- 下拉字段 -->
            <AppSelect
              v-if="field.type === 'select'"
              v-model="form[field.key]"
              :options="field.options || []"
            />

            <!-- 分段按钮 + 文本输入的一体式复合字段 -->
            <div v-else-if="field.type === 'segmented-text' && field.segmentKey" class="ws-segmented-input">
              <div class="ws-segmented-input__modes" role="group" :aria-label="field.label">
                <button
                  v-for="option in field.options || []"
                  :key="option.value"
                  type="button"
                  :class="{ active: form[field.segmentKey] === option.value }"
                  @click="form[field.segmentKey] = option.value"
                >
                  {{ option.label }}
                </button>
              </div>
              <input
                v-model.trim="form[field.key]"
                type="text"
                :placeholder="field.placeholder"
                autocomplete="off"
              />
            </div>

            <!-- 开关字段 -->
            <div v-else-if="field.type === 'switch'" class="ws-switch">
              <div class="ws-switch__txt">
                <b>{{ field.switchLabel || field.label }}
                  <span v-if="field.switchTag" class="ws-switch__tag">{{ field.switchTag }}</span>
                </b>
                <small v-if="field.switchHint">{{ field.switchHint }}</small>
              </div>
              <label class="ws-switch__ctrl" :class="{ 'ws-switch__ctrl--disabled': field.disabled }">
                <input
                  type="checkbox"
                  :checked="form[field.key] === 'true'"
                  :disabled="field.disabled"
                  @change="form[field.key] = ($event.target as HTMLInputElement).checked ? 'true' : 'false'"
                />
                <span class="ws-switch__track" />
                <span class="ws-switch__knob" />
              </label>
            </div>

            <!-- 文本/密码/数字字段 -->
            <input
              v-else
              v-model.trim="form[field.key]"
              :type="field.type || 'text'"
              :inputmode="field.inputmode || 'text'"
              :placeholder="field.placeholder"
              autocomplete="new-password"
            />
          </div>

          <div v-if="showEntry" class="ws-entry">
            <div class="ws-entry__label">
              反代入口
              <SettingsHelpTooltip :title="entryHelpTitle"><p v-html="entryHelpBody" /></SettingsHelpTooltip>
            </div>
            <div class="ws-entry__row">
              <span class="ws-entry__url" :class="{ muted: !entryRunning }">{{ entryRunning ? entryUrl : "启动后生成入口" }}</span>
              <button type="button" class="ws-entry__copy" :disabled="!entryRunning" @click="emit('copy')">复制</button>
            </div>
          </div>

          <div class="ws-foot">
            <AppButton v-if="showTest" variant="secondary" :disabled="testing" @click="emit('test')">{{ testing ? "测试中…" : "测试连接" }}</AppButton>
            <AppButton v-if="showRefresh" variant="secondary" :disabled="refreshing" @click="emit('refresh')">{{ refreshing ? "刷库中…" : "手动刷库" }}</AppButton>
            <div class="ws-foot__spacer" />
            <AppButton variant="primary" :disabled="saving || saveDisabled" @click="emit('save')">{{ saving ? "保存中…" : saveLabel }}</AppButton>
          </div>
        </section>
      </div>
  </AppModal>
</template>

<style scoped>
.ws {
  display: grid;
  grid-template-columns: 230px minmax(0, 1fr);
  min-height: 440px;
}

/* ── 左侧列表 ── */
.ws-side {
  border-right: 1px solid var(--border);
  background: var(--surface-sunken);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ws-side__cap {
  font-size: 11px;
  color: var(--text-muted);
  letter-spacing: 0.06em;
  padding: 4px 8px 8px;
}
.ws-side__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  overflow-y: auto;
}
.ws-side__item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  cursor: pointer;
  transition: 0.12s;
  border: 1px solid transparent;
}
.ws-side__item:hover {
  background: var(--surface);
}
.ws-side__item.active {
  background: var(--brand-soft);
  border-color: color-mix(in srgb, var(--brand) 35%, transparent);
}
.ws-side__ic {
  width: 30px;
  height: 30px;
  border-radius: 8px;
  background: var(--surface3, var(--surface));
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  flex-shrink: 0;
}
.ws-side__item.active .ws-side__ic {
  background: color-mix(in srgb, var(--brand) 20%, transparent);
}
.ws-side__tx {
  min-width: 0;
  flex: 1;
}
.ws-side__tx b {
  display: block;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text);
}
.ws-side__tx small {
  display: block;
  font-size: 11px;
  color: var(--text-muted);
  margin-top: 2px;
}
.ws-side__st {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--border2);
  flex-shrink: 0;
}
.ws-side__st.on {
  background: var(--success);
}
.ws-side__empty {
  padding: 26px 10px;
  text-align: center;
  font-size: 12px;
  line-height: 1.8;
  color: var(--text-muted);
}
.ws-add {
  margin-top: auto;
  width: 100%;
  padding: 8px 12px;
  border-radius: 9px;
  border: 1px dashed var(--border2);
  background: transparent;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  transition: 0.12s;
}
.ws-add:hover {
  border-color: var(--brand);
  color: var(--brand);
  background: var(--brand-soft);
}

/* ── 右侧编辑区 ── */
.ws-main {
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  overflow-y: auto;
}
.ws-main__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.ws-main__title-wrap {
  min-width: 0;
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.ws-main__title {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.ws-name-edit {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  width: min(280px, 100%);
  padding: 4px 8px;
  border: 1px solid var(--brand);
  border-radius: 8px;
  background: var(--surface);
  outline: none;
  box-shadow: 0 0 0 3px var(--brand-soft);
  font-family: inherit;
  box-sizing: border-box;
}
.ws-name-edit-btn {
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: 0.12s;
  flex-shrink: 0;
  padding: 0;
}
.ws-name-edit-btn:hover {
  background: var(--brand-soft);
  color: var(--brand);
}
.ws-name-edit-btn svg {
  width: 13px;
  height: 13px;
}
.ws-main__sub {
  font-size: 12px;
  color: var(--text-muted);
  width: 100%;
}
.ws-del {
  padding: 6px 12px;
  border-radius: 8px;
  border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
  background: transparent;
  color: var(--danger);
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  transition: 0.12s;
}
.ws-del:hover {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
}

.ws-field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.ws-field__head {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ws-field--hidden {
  visibility: hidden;
  pointer-events: none;
}
.ws-field__label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-regular);
}
.ws-field input {
  width: 100%;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface-sunken);
  color: var(--text);
  font-size: 13px;
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
  font-family: inherit;
  box-sizing: border-box;
}
.ws-field input:focus {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-soft);
}
.ws-field input::placeholder {
  color: var(--text-faint);
}

.ws-entry {
  padding: 11px 12px;
  border-radius: 12px;
  background: radial-gradient(120% 80% at 0% 0%, var(--brand-soft), transparent 55%), var(--surface-sunken);
  border: 1px solid var(--border-soft, var(--border));
}
.ws-entry__label {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin-bottom: 6px;
}
.ws-entry__row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ws-entry__url {
  flex: 1;
  min-width: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
  color: var(--text-regular);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ws-entry__url.muted {
  color: var(--text-muted);
}
.ws-entry__copy {
  appearance: none;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-regular);
  font: inherit;
  font-size: 12px;
  padding: 5px 10px;
  border-radius: 8px;
  cursor: pointer;
  white-space: nowrap;
}
.ws-entry__copy:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--brand) 35%, var(--border));
}
.ws-entry__copy:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

/* ── 开关字段 ── */
.ws-switch {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-sunken);
}
.ws-switch__txt {
  min-width: 0;
  flex: 1;
}
.ws-switch__txt b {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}
.ws-switch__txt small {
  display: block;
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 3px;
  line-height: 1.5;
}
.ws-switch__tag {
  display: inline-flex;
  align-items: center;
  height: 18px;
  margin-left: 6px;
  padding: 0 8px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--warning) 14%, var(--surface));
  color: #b45309;
  font-size: 11px;
  font-weight: 500;
}
.ws-switch__ctrl {
  position: relative;
  width: 42px;
  height: 24px;
  flex-shrink: 0;
  cursor: pointer;
}
.ws-switch__ctrl--disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.ws-switch__ctrl input {
  position: absolute;
  inset: 0;
  opacity: 0;
  margin: 0;
  cursor: pointer;
}
.ws-switch__ctrl--disabled input {
  cursor: not-allowed;
}
.ws-switch__track {
  position: absolute;
  inset: 0;
  border-radius: 999px;
  background: var(--border2);
  transition: background 0.15s;
}
.ws-switch__ctrl input:checked + .ws-switch__track {
  background: var(--success);
}
.ws-switch__knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25);
  transition: left 0.15s;
}
.ws-switch__ctrl input:checked ~ .ws-switch__knob {
  left: 21px;
}

.ws-segmented-input {
  display: flex;
  min-width: 0;
  height: 42px;
  overflow: hidden;
  border: 1px solid var(--brand);
  border-radius: 10px;
  background: var(--surface);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
}
.ws-segmented-input:focus-within {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-soft);
}
.ws-segmented-input__modes {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-right: 1px solid var(--border);
  background: var(--surface-sunken);
}
.ws-segmented-input__modes button {
  height: 34px;
  padding: 0 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-muted);
  font: inherit;
  font-size: 13px;
  font-weight: 650;
  cursor: pointer;
  white-space: nowrap;
}
.ws-segmented-input__modes button.active {
  background: var(--surface);
  color: var(--brand);
  box-shadow: 0 1px 4px rgb(15 23 42 / 10%);
}
.ws-segmented-input > input {
  min-width: 0;
  flex: 1;
  height: 100%;
  padding: 0 13px;
  border: 0;
  border-radius: 0;
  outline: 0;
  background: transparent;
  box-shadow: none;
}
.ws-segmented-input > input:focus {
  border: 0;
  box-shadow: none;
}

.ws-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: auto;
  padding-top: 14px;
}
.ws-foot__spacer {
  flex: 1;
}

@media (max-width: 760px) {
  .ws {
    grid-template-columns: 1fr;
  }
  .ws-side {
    border-right: 0;
    border-bottom: 1px solid var(--border);
    flex-direction: row;
    align-items: center;
    gap: 8px;
    overflow-x: auto;
  }
  .ws-side__list {
    flex-direction: row;
    overflow-x: auto;
  }
  .ws-side__item {
    flex-shrink: 0;
  }
  .ws-add {
    margin-top: 0;
    margin-left: auto;
    width: auto;
    flex-shrink: 0;
  }
  .ws-side__cap {
    display: none;
  }
}

@media (max-width: 520px) {
  .ws-segmented-input {
    height: auto;
    flex-direction: column;
  }
  .ws-segmented-input__modes {
    width: 100%;
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }
  .ws-segmented-input__modes button {
    flex: 1;
  }
  .ws-segmented-input > input {
    width: 100%;
    height: 42px;
  }
}
</style>
