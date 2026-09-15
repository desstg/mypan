<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchMediaOrganizeJavRules,
  previewMediaOrganizeJavRules,
  saveMediaOrganizeJavRules,
  type MediaOrganizeJavClassifyRule,
  type MediaOrganizeJavExplain,
  type MediaOrganizeJavReplaceRule,
  type MediaOrganizeJavRules,
  type MediaOrganizeJavToggles,
} from "@/api/mediaOrganize";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import JavStringListEditor from "@/components/admin/JavStringListEditor.vue";
import JavReplaceRulesEditor from "@/components/admin/JavReplaceRulesEditor.vue";
import JavClassifyRulesEditor from "@/components/admin/JavClassifyRulesEditor.vue";
import { toast } from "@/composables/useToast";

// 番号匹配规则设置 tab。
//
// 自持草稿 + 基线快照，脏判断用 JSON 比对 —— 不走 useSettingsForm：
// 那套用 `!==` 比较字段，而 Object.assign 的快照让对象字段两边同引用，
// 嵌套规则改了永远脏不了。见 useSettingsForm.isFieldChanged。

const ACCENT = "#10b981";

const emit = defineEmits<{ dirtyChange: [boolean] }>();

const loading = ref(false);
const saving = ref(false);
const loaded = ref(false);
const problems = ref<string[]>([]);

const draft = reactive<MediaOrganizeJavRules>({
  junk_chars: [],
  replace_rules: [],
  classify_rules: [],
});
const toggles = reactive<MediaOrganizeJavToggles>({
  delete_small: false,
  small_file_mb: 300,
  delete_types: "",
  delete_exclude_types: "",
  clean_empty_dirs: true,
  max_dirs: 500,
});
let defaults: MediaOrganizeJavRules = { junk_chars: [], replace_rules: [], classify_rules: [] };
let baseline = "";

// ── 试跑 ──
//
// 输入框**默认为空**。之前塞了一份写死的示例值，导致这里永远显示那三行的结果，
// 看着像「卡住了」或者「显示的不是我输入的东西」。示例改成按需填入。
const TRY_SAMPLE = "hhd800.com@ABP-123 中文字幕.mp4\nSSIS-001 4k60.mp4\n普通家庭录像.mp4";
const tryText = ref("");
const tryResults = ref<MediaOrganizeJavExplain[]>([]);
const trying = ref(false);
let tryTimer: number | null = null;

function fillSample() {
  tryText.value = TRY_SAMPLE;
  scheduleTry();
}

const dirty = computed(() => loaded.value && serialize() !== baseline);

function serialize(): string {
  return JSON.stringify({ rules: draft, toggles });
}

/** AppInput 的数字框回传字符串，统一转成后端要的整数。 */
function toPositiveInt(value: unknown, fallback: number): number {
  const n = Number(value);
  if (!Number.isFinite(n) || n < 1) return fallback;
  return Math.floor(n);
}

function snapshot() {
  baseline = serialize();
  emit("dirtyChange", false);
}

function markDirty() {
  emit("dirtyChange", dirty.value);
}

/** 用后端下发的默认值填充草稿（「恢复默认」）。 */
function applyDefaults() {
  draft.junk_chars = [...defaults.junk_chars];
  draft.replace_rules = defaults.replace_rules.map((r) => ({ ...r }));
  draft.classify_rules = defaults.classify_rules.map((r) => ({
    ...r,
    includes: [...(r.includes ?? [])],
    excludes: [...(r.excludes ?? [])],
  }));
  markDirty();
  scheduleTry();
}

async function load() {
  loading.value = true;
  try {
    const data = await fetchMediaOrganizeJavRules();
    defaults = data.defaults;
    draft.junk_chars = [...data.rules.junk_chars];
    draft.replace_rules = data.rules.replace_rules.map((r) => ({ ...r }));
    draft.classify_rules = data.rules.classify_rules.map((r) => ({
      ...r,
      includes: [...(r.includes ?? [])],
      excludes: [...(r.excludes ?? [])],
    }));
    Object.assign(toggles, data.settings);
    loaded.value = true;
    snapshot();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载番号规则失败"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    const payload = {
      rules: {
        junk_chars: [...draft.junk_chars],
        replace_rules: draft.replace_rules.map((r) => ({ ...r })),
        classify_rules: draft.classify_rules.map((r) => ({
          ...r,
          includes: [...(r.includes ?? [])],
          excludes: [...(r.excludes ?? [])],
        })),
      },
      // AppInput 的 number 框回传的是字符串，后端 JavToggles 是 int，必须在这里转回来
      settings: {
        delete_small: Boolean(toggles.delete_small),
        small_file_mb: toPositiveInt(toggles.small_file_mb, 300),
        delete_types: toggles.delete_types ?? "",
        delete_exclude_types: toggles.delete_exclude_types ?? "",
        clean_empty_dirs: Boolean(toggles.clean_empty_dirs),
        max_dirs: toPositiveInt(toggles.max_dirs, 500),
      },
    };
    const data = await saveMediaOrganizeJavRules(payload);
    defaults = data.defaults;
    draft.junk_chars = [...data.rules.junk_chars];
    draft.replace_rules = data.rules.replace_rules.map((r) => ({ ...r }));
    draft.classify_rules = data.rules.classify_rules.map((r) => ({
      ...r,
      includes: [...(r.includes ?? [])],
      excludes: [...(r.excludes ?? [])],
    }));
    Object.assign(toggles, data.settings);
    problems.value = [];
    snapshot();
    toast.success("番号规则已保存");
  } catch (e) {
    // 后端保存前会校验（写错的正则、空关键词等），错误信息直接透给用户
    const message = getApiErrorMessage(e, "保存番号规则失败");
    problems.value = [message];
    toast.error(message);
  } finally {
    saving.value = false;
  }
}

function revert() {
  void load();
}

// ── 试跑 ──

function scheduleTry() {
  markDirty();
  if (tryTimer !== null) window.clearTimeout(tryTimer);
  // 防抖：规则编辑是连续输入，每敲一个字发一次请求没必要
  tryTimer = window.setTimeout(() => {
    tryTimer = null;
    void runTry();
  }, 400);
}

async function runTry() {
  const names = tryText.value
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
  if (!names.length) {
    tryResults.value = [];
    return;
  }
  trying.value = true;
  try {
    // 用**草稿**试跑，而不是已保存的规则 —— 保存前就能看到效果是这个功能的全部意义
    const data = await previewMediaOrganizeJavRules(names, {
      junk_chars: [...draft.junk_chars],
      replace_rules: draft.replace_rules.map((r) => ({ ...r })),
      classify_rules: draft.classify_rules.map((r) => ({
        ...r,
        includes: [...(r.includes ?? [])],
        excludes: [...(r.excludes ?? [])],
      })),
    });
    tryResults.value = data.results;
    problems.value = data.results.flatMap((r) => r.warnings);
  } catch (e) {
    problems.value = [getApiErrorMessage(e, "试跑失败")];
  } finally {
    trying.value = false;
  }
}

function setJunkChars(next: string[]) {
  draft.junk_chars = next;
  scheduleTry();
}

function setReplaceRules(next: MediaOrganizeJavReplaceRule[]) {
  draft.replace_rules = next;
  scheduleTry();
}

function setClassifyRules(next: MediaOrganizeJavClassifyRule[]) {
  draft.classify_rules = next;
  scheduleTry();
}

function onToggleChanged() {
  markDirty();
}

// 首次切到本 tab 时自动加载（父组件用 v-if 挂载本组件）
onMounted(() => {
  void load();
});

defineExpose({
  load,
  save,
  revert,
  isDirty: () => dirty.value,
  saving,
});
</script>

<template>
  <div v-if="loading" class="settings-card__loading">加载中…</div>

  <template v-else>
    <SettingsCard title="小文件与空目录" :accent="ACCENT">
      <SettingsRow :show-changed-badge="true" :changed="false">
        <template #info>
          <div class="settings-row__label">
            <span>清理小文件</span>
            <SettingsHelpTooltip title="清理小文件说明">
              <p>开启后，整理时会删除小于阈值的文件（预告片、样本、广告小文件等）。</p>
              <p><b>默认关闭</b> —— 删文件不可逆，请先开「预览」看清楚会删哪些再执行。</p>
              <p>字幕、NFO、海报等元数据文件永远不删：它们要跟着视频一起搬进新目录。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <SettingsBoolSegment v-model="toggles.delete_small" label="清理小文件" @update:model-value="onToggleChanged" />
        </template>
      </SettingsRow>

      <SettingsRow v-if="toggles.delete_small" :show-changed-badge="true" :changed="false">
        <template #info>
          <div class="settings-row__label">
            <span>小文件阈值（MB）</span>
            <SettingsHelpTooltip title="阈值说明">
              <p>小于该大小的文件会被删除（正好等于不删）。</p>
              <p>默认 300MB 与旧方案一致，但对短片段可能偏大，建议按自己的库调小。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <AppInput
            v-model="toggles.small_file_mb"
            type="number"
            min="1"
            max="102400"
            @update:model-value="onToggleChanged"
          />
        </template>
      </SettingsRow>

      <SettingsRow :show-changed-badge="true" :changed="false">
        <template #info>
          <div class="settings-row__label">
            <span>清理小文件的文件类型</span>
            <SettingsHelpTooltip title="文件类型过滤说明">
              <p><b>文件类型</b>：只有这些扩展名会被删（英文分号分隔）。留空表示不限类型。</p>
              <p><b>排除</b>：这些扩展名永远不会被删（英文分号分隔）。留空表示不排除任何类型。</p>
              <p>两者都留空时，<b>只要大小满足上面的阈值就会被删</b>。</p>
              <p>只比较扩展名本身（不带点、不区分大小写），例如 txt、html、jpg。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <div class="jav-filetype">
            <label class="jav-filetype__field">
              <span class="jav-filetype__caption">文件类型（可删除）</span>
              <AppInput
                v-model="toggles.delete_types"
                placeholder="留空 = 不限类型，如 txt;html"
                @update:model-value="onToggleChanged"
              />
            </label>
            <label class="jav-filetype__field">
              <span class="jav-filetype__caption">排除（不删除）</span>
              <AppInput
                v-model="toggles.delete_exclude_types"
                placeholder="留空 = 不排除，如 nfo;srt;jpg"
                @update:model-value="onToggleChanged"
              />
            </label>
          </div>
        </template>
      </SettingsRow>

      <SettingsRow :show-changed-badge="true" :changed="false">
        <template #info>
          <div class="settings-row__label">
            <span>清理空目录</span>
            <SettingsHelpTooltip title="清理空目录说明">
              <p>整理后把源目录里变空的子目录删掉。只删空目录，不影响有内容的目录。</p>
              <p>源目录本身永远不会被删。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <SettingsBoolSegment v-model="toggles.clean_empty_dirs" label="清理空目录" @update:model-value="onToggleChanged" />
        </template>
      </SettingsRow>

      <SettingsRow :show-changed-badge="true" :changed="false">
        <template #info>
          <div class="settings-row__label">
            <span>扫描目录上限</span>
            <SettingsHelpTooltip title="扫描上限说明">
              <p>单次扫描最多遍历的目录数，防止超大目录树把任务卡死；超限时计划会标记为不完整。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <AppInput
            v-model="toggles.max_dirs"
            type="number"
            min="10"
            max="100000"
            @update:model-value="onToggleChanged"
          />
        </template>
      </SettingsRow>
    </SettingsCard>

    <SettingsCard title="删除字符" :accent="ACCENT">
      <template #head-aside>
        <span class="jav-tab__hint">改名时整段删除</span>
      </template>
      <div class="jav-tab__stack">
        <JavStringListEditor
          :model-value="draft.junk_chars"
          placeholder="原字符，如 hhd800.com@"
          empty-hint="暂无条目"
          input-hint="不区分大小写。同一个位置有多条命中时取最长的那条；等长时取 ↑↓ 排在前面的。"
          @update:model-value="setJunkChars"
        />
      </div>
    </SettingsCard>

    <SettingsCard title="替换字符" :accent="ACCENT">
      <template #head-aside>
        <span class="jav-tab__hint">改名时 A → B 替换</span>
      </template>
      <div class="jav-tab__stack">
        <JavReplaceRulesEditor :model-value="draft.replace_rules" @update:model-value="setReplaceRules" />
      </div>
    </SettingsCard>

    <SettingsCard title="分类移动" :accent="ACCENT">
      <template #head-aside>
        <span class="jav-tab__hint">
          整理目录下的一级子目录按规则移动到「目标根目录/目标目录」，首个命中的规则生效
        </span>
      </template>
      <div class="jav-tab__stack">
        <JavClassifyRulesEditor :model-value="draft.classify_rules" @update:model-value="setClassifyRules" />
      </div>
    </SettingsCard>

    <SettingsCard title="试跑检查" :accent="ACCENT">
      <template #head-aside>
        <span class="jav-tab__hint">改完规则先在这里验证再保存 —— 每行一个文件名</span>
      </template>
      <template #head-actions>
        <AppButton type="button" variant="ghost" size="sm" :disabled="trying" @click="fillSample">填入示例</AppButton>
        <AppButton type="button" variant="secondary" size="sm" :disabled="trying" @click="runTry">
          {{ trying ? "试跑中…" : "试跑" }}
        </AppButton>
      </template>

      <div class="jav-tab__stack">
        <textarea
          v-model="tryText"
          class="jav-try__input"
          rows="3"
          placeholder="每行一个文件名"
          @input="scheduleTry"
        />

        <div v-if="tryResults.length" class="jav-try">
          <div v-for="(row, index) in tryResults" :key="`try-${index}`" class="jav-try__row">
            <div class="jav-try__main">
              <span class="jav-try__name">{{ row.name }}</span>
              <span class="jav-try__arrow">→</span>
              <span class="jav-try__result" :class="{ 'jav-try__result--changed': row.changed }">
                {{ row.renamed }}
              </span>
            </div>
            <div class="jav-try__meta">
              <span v-if="row.code" class="jav-try__tag">番号 {{ row.code }}</span>
              <span v-else class="jav-try__tag jav-try__tag--muted">未识别出番号</span>
              <span v-if="row.classify_target" class="jav-try__tag">
                分类 {{ row.classify_rule }} → {{ row.classify_target }}
              </span>
              <span v-else class="jav-try__tag jav-try__tag--muted">不参与分类</span>
            </div>
            <!-- 显示实际命中的规则，这是调规则时最需要知道的信息 -->
            <div v-if="row.steps.length" class="jav-try__steps">{{ row.steps.join(" · ") }}</div>
          </div>
        </div>
        <div v-else class="jav-try__empty">输入文件名后自动试跑，可看到改名结果与命中的规则</div>

        <div v-if="problems.length" class="jav-tab__problems">
          <div v-for="(p, i) in problems" :key="`problem-${i}`">{{ p }}</div>
        </div>
      </div>
    </SettingsCard>

    <div class="jav-tab__actions">
      <AppButton type="button" variant="ghost" size="sm" @click="applyDefaults">恢复默认规则</AppButton>
    </div>
  </template>
</template>

<style scoped>
.jav-tab__hint {
  font-size: 12px;
  color: var(--text-muted);
}

/* settings-card__body 是 flex 列，这里给内容补一点内边距与间距 */
.jav-tab__stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  padding: 12px 0 4px;
}

.jav-tab__actions {
  display: flex;
  justify-content: flex-end;
  padding: 0 4px 8px;
}

/* 「文件类型 / 排除」并排两栏：左侧白名单、右侧黑名单 */
.jav-filetype {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  width: 100%;
}

.jav-filetype__field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.jav-filetype__caption {
  font-size: 12px;
  color: var(--text-muted);
}

@media (max-width: 720px) {
  .jav-filetype {
    grid-template-columns: 1fr;
  }
}

.jav-tab__problems {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--warning) 12%, var(--surface));
  border: 1px solid color-mix(in srgb, var(--warning) 35%, var(--border));
  font-size: 12px;
  line-height: 1.6;
  color: var(--text);
}

.jav-try {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.jav-try__row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  border: 1px solid var(--border-soft);
}

.jav-try__main {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
}

.jav-try__name {
  color: var(--text-muted);
  word-break: break-all;
}

.jav-try__arrow {
  color: var(--text-muted);
}

.jav-try__result {
  font-weight: 600;
  color: var(--text);
  word-break: break-all;
}

.jav-try__result--changed {
  color: var(--accent, #10b981);
}

.jav-try__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.jav-try__tag {
  padding: 1px 8px;
  border-radius: var(--radius-pill, 999px);
  background: color-mix(in srgb, var(--brand) 12%, transparent);
  color: var(--brand);
  font-size: 11px;
}

.jav-try__tag--muted {
  background: var(--border-soft);
  color: var(--text-muted);
}

.jav-try__steps {
  font-size: 11px;
  line-height: 1.6;
  color: var(--text-muted);
}

.jav-try__empty {
  font-size: 13px;
  color: var(--text-muted);
}

/* 与 AppInput 同一套观感，只是多行 */
.jav-try__input {
  width: 100%;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-family: inherit;
  font-size: inherit;
  line-height: 1.6;
  resize: vertical;
  transition: var(--transition);
}

.jav-try__input:focus {
  outline: none;
  border-color: var(--brand);
}

.jav-try__input::placeholder {
  color: var(--text-muted);
}
</style>
