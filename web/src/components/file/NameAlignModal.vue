<script setup lang="ts">
import { computed } from "vue";
import AppModal from "@/components/base/AppModal.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import type { FileNameAlignPreviewResult } from "@/api/types";

const props = defineProps<{
  open: boolean;
  loading: boolean;
  applying: boolean;
  error: string;
  preview: FileNameAlignPreviewResult | null;
  selectedSampleId: string;
  suspectIds: string[];
  includeSuspects: boolean;
  applyTotal: number;
  applyProgress: number;
}>();

const emit = defineEmits<{
  close: [];
  apply: [];
  "update:sampleId": [value: string];
  "update:include-suspects": [value: boolean];
  "remove-suspect": [fileId: string];
}>();

const sampleOptions = computed(() =>
  (props.preview?.sample_candidates ?? []).map((item) => ({
    value: item.file_id,
    label: item.file_name,
  })),
);

const selectedCountText = computed(() => {
  const count = 1 + (props.includeSuspects ? props.suspectIds.length : 0);
  return `将重命名 ${count} 个文件`;
});

const suspectCountText = computed(() => {
  const count = props.preview?.suspects.length ?? 0;
  return count ? `${count} 个疑似文件` : "无疑似文件";
});

const applyTotalText = computed(() => props.applyTotal || 1);

const applyProgressText = computed(() => {
  if (!props.applyTotal) return "正在准备";
  return `${Math.min(props.applyProgress, props.applyTotal)} / ${props.applyTotal}`;
});

const applyProgressPercent = computed(() => {
  if (!props.applyTotal) return 8;
  const progress = Math.min(props.applyProgress, props.applyTotal);
  return Math.max(8, Math.round((progress / props.applyTotal) * 100));
});

function handleClose() {
  if (props.applying) return;
  emit("close");
}
</script>

<template>
  <AppModal :open="open" size="lg" @close="handleClose">
    <template #header>
      <div class="name-align__title-wrap">
        <h3 class="name-align__title">命名对齐</h3>
        <span class="name-align__subtitle">按参考样本格式生成新文件名，执行前先核对。</span>
      </div>
    </template>

    <div class="name-align">
      <div v-if="applying" class="name-align__apply-state">
        <BusySpinner variant="notch" :size="16" color="var(--brand)" />
        <h4>正在执行命名对齐</h4>
        <p>本次将重命名 {{ applyTotalText }} 个文件，请等待网盘返回结果。</p>
        <div class="name-align__apply-progress">
          <div
            class="name-align__apply-progress-bar"
            :style="{ width: `${applyProgressPercent}%` }"
          />
        </div>
        <span class="name-align__apply-count">进度 {{ applyProgressText }}</span>
      </div>

      <div v-else-if="loading" class="name-align__state">
        <BusySpinner variant="notch" :size="28" color="var(--brand)" />
        <span>正在分析当前目录...</span>
      </div>

      <div v-else-if="error" class="name-align__state name-align__state--error">
        {{ error }}
      </div>

      <template v-else-if="preview">
        <section class="name-align__sample-row">
          <div class="name-align__sample-meta">
            <span class="name-align__eyebrow">参考样本</span>
            <strong>{{ preview.sample.pattern_label || "自动识别命名格式" }}</strong>
          </div>
          <div class="name-align__sample-control">
            <AppSelect
              class="name-align__sample-select"
              :model-value="selectedSampleId"
              :options="sampleOptions"
              placeholder="请选择参考样本"
              @update:model-value="emit('update:sampleId', String($event))"
            />
          </div>
        </section>

        <section class="name-align__section">
          <div class="name-align__rename-row name-align__rename-row--primary">
            <div class="name-align__rename-line">
              <span class="name-align__tag">原</span>
              <div class="name-align__filename">{{ preview.target.file_name }}</div>
            </div>
            <div class="name-align__rename-line name-align__rename-line--new">
              <span class="name-align__tag name-align__tag--new">新</span>
              <div class="name-align__new-name">{{ preview.target.new_name }}</div>
            </div>
          </div>
        </section>

        <section class="name-align__section">
          <div class="name-align__section-head">
            <label
              v-if="preview.suspects.length"
              class="name-align__suspect-toggle"
            >
              <input
                type="checkbox"
                :checked="includeSuspects"
                @change="emit('update:include-suspects', ($event.target as HTMLInputElement).checked)"
              />
              <span>同时重命名疑似文件</span>
            </label>
            <h4 v-else>疑似文件</h4>
            <div class="name-align__counter">
              <span>{{ suspectCountText }}</span>
              <strong>{{ selectedCountText }}</strong>
            </div>
          </div>
          <div v-if="!preview.suspects.length" class="name-align__empty">
            当前没有其他同格式的疑似文件，执行后只会重命名当前文件。
          </div>
          <div v-else class="name-align__suspects">
            <div
              v-for="item in preview.suspects"
              :key="item.file_id"
              class="name-align__suspect"
            >
              <div class="name-align__suspect-main name-align__rename-row">
                <div class="name-align__rename-line">
                  <span class="name-align__tag">原</span>
                  <div class="name-align__filename">{{ item.file_name }}</div>
                </div>
                <div class="name-align__rename-line name-align__rename-line--new">
                  <span class="name-align__tag name-align__tag--new">新</span>
                  <div class="name-align__preview">{{ item.new_name }}</div>
                </div>
              </div>
              <button
                type="button"
                class="name-align__remove"
                @click.prevent="emit('remove-suspect', item.file_id)"
              >
                移除
              </button>
            </div>
          </div>
        </section>
      </template>
    </div>

    <template #footer>
      <button
        type="button"
        class="name-align__btn name-align__btn--ghost"
        :disabled="applying"
        @click="handleClose"
      >
        取消
      </button>
      <button
        type="button"
        class="name-align__btn name-align__btn--primary"
        :disabled="loading || applying || !preview"
        @click="emit('apply')"
      >
        {{ applying ? "执行中..." : "执行命名对齐" }}
      </button>
    </template>
  </AppModal>
</template>

<style scoped>
.name-align__title-wrap {
  display: flex;
  align-items: baseline;
  gap: 12px;
  min-width: 0;
}

.name-align__title {
  margin: 0;
  color: var(--text);
  font-size: 20px;
  font-weight: 700;
}

.name-align__subtitle {
  min-width: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.45;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.name-align {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.name-align__sample-row {
  display: grid;
  grid-template-columns: 108px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  padding: 2px 0 2px;
}

.name-align__sample-meta {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.name-align__sample-meta strong {
  overflow: hidden;
  color: var(--text);
  font-size: 14px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.name-align__sample-control {
  min-width: 0;
}

.name-align__section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.name-align__section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.name-align__section-head h4 {
  margin: 3px 0 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
}

.name-align__eyebrow {
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 600;
}

.name-align__sample-select {
  min-width: 0;
}

.name-align__rename-row {
  min-width: 0;
}

.name-align__rename-row--primary {
  border-top: 1px solid var(--border-soft);
  border-bottom: 1px solid var(--border-soft);
  background: color-mix(in srgb, var(--surface-sunken) 52%, transparent);
  padding: 12px 0;
}

.name-align__rename-line {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  min-width: 0;
  padding: 3px 0;
}

.name-align__rename-line--new {
  margin-top: 5px;
}

.name-align__filename {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.55;
}

.name-align__preview,
.name-align__new-name {
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 14px;
  color: var(--brand);
  font-weight: 700;
  line-height: 1.55;
}

.name-align__tag {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 7px;
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
}

.name-align__tag--new {
  background: color-mix(in srgb, var(--brand) 10%, transparent);
  color: var(--brand);
}

.name-align__counter {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 10px;
  color: var(--text-muted);
  font-size: 12px;
}

.name-align__counter strong {
  color: var(--text);
  font-weight: 700;
}

.name-align__suspects {
  display: flex;
  flex-direction: column;
  max-height: min(38vh, 360px);
  overflow-y: auto;
  border-top: 1px solid var(--border-soft);
  border-bottom: 1px solid var(--border-soft);
}

.name-align__suspect {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: start;
  padding: 13px 0;
  border-bottom: 1px solid var(--border-soft);
}

.name-align__suspect:last-child {
  border-bottom: 0;
}

.name-align__suspect-main {
  min-width: 0;
}

.name-align__suspect-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 14px;
  font-weight: 700;
  color: var(--text);
  user-select: none;
}

.name-align__suspect-toggle input {
  width: 16px;
  height: 16px;
  accent-color: var(--brand);
}

.name-align__remove,
.name-align__btn {
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
}

.name-align__remove {
  margin-top: 1px;
  padding: 6px 12px;
  font-weight: 600;
}

.name-align__btn {
  min-width: 108px;
  padding: 9px 16px;
  font-weight: 600;
}

.name-align__btn--ghost:hover,
.name-align__remove:hover {
  border-color: var(--brand);
  color: var(--brand);
}

.name-align__btn--primary {
  border-color: transparent;
  background: var(--brand-gradient, var(--brand));
  color: var(--text-on-brand, #fff);
  box-shadow: var(--shadow-brand);
}

.name-align__btn--primary:disabled {
  opacity: 0.6;
  cursor: wait;
}

.name-align__empty,
.name-align__state,
.name-align__apply-state {
  padding: 18px;
  color: var(--text-muted);
  font-size: 13px;
}

.name-align__state {
  display: flex;
  align-items: center;
  gap: 10px;
}

.name-align__state--error {
  color: var(--danger, #c53b3b);
}

.name-align__apply-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 48px 24px;
  text-align: center;
}

.name-align__apply-state h4 {
  margin: 6px 0 0;
  color: var(--text);
  font-size: 17px;
  font-weight: 700;
}

.name-align__apply-state p {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
}

.name-align__apply-progress {
  width: min(360px, 100%);
  height: 7px;
  margin-top: 8px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--surface-sunken);
}

.name-align__apply-progress-bar {
  height: 100%;
  border-radius: inherit;
  background: var(--brand-gradient, var(--brand));
  transition: width 0.28s ease;
}

.name-align__apply-count {
  color: var(--text);
  font-size: 13px;
  font-weight: 700;
}

:global(:root[data-skin="brutal"]) .name-align__tag,
:global(:root[data-skin="brutal"]) .name-align__remove,
:global(:root[data-skin="brutal"]) .name-align__btn {
  border-radius: 0;
}

@media (max-width: 768px) {
  .name-align__title-wrap {
    flex-direction: column;
    gap: 4px;
    align-items: flex-start;
  }

  .name-align__subtitle {
    white-space: normal;
  }

  .name-align__sample-row {
    grid-template-columns: 1fr;
    align-items: stretch;
    gap: 8px;
  }

  .name-align__section-head,
  .name-align__suspect {
    grid-template-columns: 1fr;
    align-items: stretch;
  }

  .name-align__section-head {
    flex-direction: column;
  }

  .name-align__counter {
    justify-content: space-between;
  }

  .name-align__remove {
    justify-self: start;
  }

  .name-align__btn {
    min-width: 0;
  }
}
</style>
