<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  classificationApi,
  type ClassificationConfig,
  type ClassificationRule,
  type ClassificationTemplate,
  type ClassificationTemplateKind,
  type ClassificationTMDBDetail,
} from "@/api/cloudTools";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const templateMeta: Array<{ kind: ClassificationTemplateKind; name: string; desc: string }> = [
  { kind: "media", name: "内置模板一", desc: "仅按 电影 / 剧集分类" },
  { kind: "region", name: "内置模板二", desc: "按原产国家二级分类" },
  { kind: "genre", name: "内置模板三", desc: "按影片类型二级分类" },
  { kind: "custom", name: "自定义模板", desc: "自由组合目录层级与匹配条件" },
];

const emptyConfig = (): ClassificationConfig => ({
  version: 1,
  enabled: false,
  selected_template: "media",
  templates: [],
});

const config = ref<ClassificationConfig>(emptyConfig());
const draft = ref<ClassificationConfig>(emptyConfig());
const open = ref(false);
const helpOpen = ref(false);
const saving = ref(false);
const detailTMDBID = ref("");
const detailMediaType = ref<"movie" | "tv">("movie");
const detailLoading = ref(false);
const detailResult = ref<ClassificationTMDBDetail | null>(null);

// 树状态：选中的节点（rootIdx = 一级下标，childIdx = -1 表示选中一级）
const selectedRootIdx = ref(-1);
const selectedChildIdx = ref(-1);
// 一级节点折叠状态（按一级下标）
const folded = ref<Record<number, boolean>>({});

function cloneConfig(value: ClassificationConfig): ClassificationConfig {
  return JSON.parse(JSON.stringify(value)) as ClassificationConfig;
}

function matches(title: string) {
  const query = props.searchQuery.trim().toLowerCase();
  return !query || title.toLowerCase().includes(query);
}

function templateLabel(kind: ClassificationTemplateKind) {
  return templateMeta.find((item) => item.kind === kind)?.name ?? kind;
}

const selectedTemplate = computed(() =>
  draft.value.templates.find((item) => item.kind === draft.value.selected_template),
);

// 当前选中的节点对象（用于右侧编辑绑定）
const selectedNode = computed<{ rule: ClassificationRule; parent?: ClassificationRule; level: 0 | 1 } | null>(() => {
  const template = selectedTemplate.value;
  if (!template || selectedRootIdx.value < 0 || selectedRootIdx.value >= template.rules.length) return null;
  const rule = template.rules[selectedRootIdx.value];
  if (selectedChildIdx.value < 0) return { rule, level: 0 };
  const child = rule.children?.[selectedChildIdx.value];
  if (!child) return { rule, level: 0 };
  return { rule: child, parent: rule, level: 1 };
});

// 内置模板（非 custom）的一级条件只读
const editingLocked = computed(() => {
  const node = selectedNode.value;
  if (!node || node.level !== 0) return false;
  return selectedTemplate.value?.kind !== "custom";
});

async function load() {
  try {
    config.value = await classificationApi.getConfig();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载分类整理配置失败"));
  }
}

onMounted(load);

function openSettings() {
  draft.value = cloneConfig(config.value);
  helpOpen.value = false;
  detailResult.value = null;
  selectFirst();
  open.value = true;
}

function selectFirst() {
  const template = selectedTemplate.value;
  if (template && template.rules.length) {
    selectedRootIdx.value = 0;
    selectedChildIdx.value = -1;
    collapseOthers(0);
  } else {
    selectedRootIdx.value = -1;
    selectedChildIdx.value = -1;
  }
}

function selectTemplate(kind: ClassificationTemplateKind) {
  draft.value.selected_template = kind;
  selectFirst();
}

function selectRoot(index: number) {
  selectedRootIdx.value = index;
  selectedChildIdx.value = -1;
  collapseOthers(index);
}

function selectChild(rootIdx: number, childIdx: number) {
  selectedRootIdx.value = rootIdx;
  selectedChildIdx.value = childIdx;
  collapseOthers(rootIdx);
}

// 手风琴：只展开选中的一级目录，其余收起，避免列表过长
function collapseOthers(activeIndex: number) {
  const template = selectedTemplate.value;
  if (!template) return;
  const next: Record<number, boolean> = {};
  template.rules.forEach((_, i) => {
    next[i] = i !== activeIndex;
  });
  folded.value = next;
}

function toggleFold(index: number) {
  const next = !folded.value[index];
  folded.value = { ...folded.value, [index]: next };
}

function addCustomRootRule(template: ClassificationTemplate) {
  template.rules.push({ name: "新分类", condition: "type=tv", children: [] });
  selectRoot(template.rules.length - 1);
}

function addChildRule(template: ClassificationTemplate, parent: ClassificationRule, rootIdx: number) {
  const condition = template.kind === "region"
    ? "origin_country=CN"
    : template.kind === "genre"
      ? "genres=剧情"
      : template.kind === "custom"
        ? "genres=剧情"
        : "type=movie";
  (parent.children ??= []).push({ name: "新分类", condition, children: [] });
  selectChild(rootIdx, parent.children.length - 1);
}

function removeRule(rules: ClassificationRule[], index: number) {
  rules.splice(index, 1);
}

function useFallbackDirectory(rule: ClassificationRule, enabled: boolean) {
  rule.fallback_mode = enabled ? "directory" : "self";
  if (enabled && !rule.fallback_dir?.trim()) rule.fallback_dir = "其他";
}

// 删除一级后修正选中
function removeRoot(template: ClassificationTemplate, index: number) {
  const activeRoot = template.rules[selectedRootIdx.value];
  const removedRoot = template.rules[index];
  removeRule(template.rules, index);
  if (!template.rules.length) {
    selectedRootIdx.value = -1;
    selectedChildIdx.value = -1;
    folded.value = {};
    return;
  }
  if (activeRoot && activeRoot !== removedRoot) {
    const nextIndex = template.rules.indexOf(activeRoot);
    if (nextIndex >= 0) {
      selectedRootIdx.value = nextIndex;
      collapseOthers(nextIndex);
      return;
    }
  }
  selectRoot(Math.min(index, template.rules.length - 1));
}

// 删除二级后修正选中
function removeChild(rule: ClassificationRule, childIdx: number) {
  const children = rule.children ?? [];
  const rootIdx = selectedTemplate.value?.rules.indexOf(rule) ?? -1;
  const isActiveRoot = selectedRootIdx.value === rootIdx;
  const activeChild = isActiveRoot ? children[selectedChildIdx.value] : undefined;
  const removedChild = children[childIdx];
  removeRule(rule.children ?? [], childIdx);
  if (!isActiveRoot) return;
  if (activeChild && activeChild !== removedChild) {
    selectedChildIdx.value = (rule.children ?? []).indexOf(activeChild);
    return;
  }
  selectedChildIdx.value = -1;
}

function objectStringValues(value: unknown, key: string) {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => {
      if (typeof item === "string") return item;
      if (item && typeof item === "object") {
        const raw = (item as Record<string, unknown>)[key];
        return typeof raw === "string" ? raw : "";
      }
      return "";
    })
    .filter(Boolean);
}

const detailTitle = computed(() => {
  const detail = detailResult.value;
  if (!detail) return "";
  return String(detail.title ?? detail.name ?? `TMDB ${detail.id ?? detailTMDBID.value}`);
});

const detailConditions = computed(() => {
  const detail = detailResult.value;
  if (!detail) return [];
  const conditions: Array<{ label: string; value: string }> = [
    { label: "媒体类型", value: `type=${detail.media_type ?? detailMediaType.value}` },
  ];
  const originCountries = objectStringValues(detail.origin_country, "iso_3166_1");
  const genres = objectStringValues(detail.genres, "name");
  if (originCountries.length) conditions.push({ label: "原产地区", value: `origin_country=${originCountries.join(";")}` });
  if (genres.length) conditions.push({ label: "影片类型", value: `genres=${genres.join(";")}` });
  return conditions;
});

const detailJSON = computed(() => JSON.stringify(detailResult.value, null, 2));

async function lookupTMDBDetail() {
  const tmdbID = detailTMDBID.value.trim();
  if (!/^\d{1,10}$/.test(tmdbID) || Number(tmdbID) <= 0) {
    toast.error("请输入 1～10 位有效 TMDB ID");
    return;
  }
  detailLoading.value = true;
  detailResult.value = null;
  try {
    detailResult.value = await classificationApi.lookupTMDBDetail({
      tmdb_id: tmdbID,
      media_type: detailMediaType.value,
    });
  } catch (error) {
    toast.error(getApiErrorMessage(error, "查询 TMDB 详情失败"));
  } finally {
    detailLoading.value = false;
  }
}

async function copyCondition(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    toast.success("匹配条件已复制");
  } catch {
    toast.error("复制失败，请手动选择文本");
  }
}

async function persist(next: ClassificationConfig, successMessage: string) {
  saving.value = true;
  try {
    config.value = await classificationApi.saveConfig(next);
    toast.success(successMessage);
    return true;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存分类整理配置失败"));
    return false;
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled() {
  await persist(
    { ...cloneConfig(config.value), enabled: !config.value.enabled },
    config.value.enabled ? "分类整理已停用" : "分类整理已启用",
  );
}

async function saveSettings() {
  if (await persist(cloneConfig(draft.value), "分类模板已保存")) open.value = false;
}
</script>

<template>
  <div v-show="matches('目录整理分类')">
    <CloudToolCard
      :enabled="config.enabled"
      name="目录整理分类"
      driver="移动整理 · 按模板生成分类目录"
      logo-src="/logos/classification.png"
      logo-alt="目录整理分类"
      :stat-value="templateLabel(config.selected_template)"
      :compact-stat="true"
    >
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: config.enabled }"
          :aria-label="config.enabled ? '停用分类整理' : '启用分类整理'"
          :disabled="saving"
          title="启用 / 停用"
          @click="toggleEnabled"
        >
          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
        </button>
      </template>
      移动整理时按所选模板放入分类目录；无法识别影视类型时放入目标根目录，本地重命名模式不受影响。
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="saving" @click="openSettings">分类设置</AppButton>
      </template>
    </CloudToolCard>

    <AppModal :open="open" size="lg" title="请选择分类模板" footer-divider body-flush @close="open = false">
      <div class="classification-template-tabs">
          <button
            v-for="item in templateMeta"
            :key="item.kind"
            type="button"
            class="classification-template-tab"
            :class="{ active: draft.selected_template === item.kind }"
            @click="selectTemplate(item.kind)"
          >
            <strong>{{ item.name }}</strong><span>{{ item.desc }}</span>
          </button>
        </div>

        <!-- 文件树 + 右侧编辑 -->
        <div v-if="selectedTemplate" class="cls-workspace">
          <!-- 左侧目录树 -->
          <aside class="cls-tree">
            <div class="cls-tree__cap">分类目录</div>
            <div class="cls-tree__list">
              <template v-for="(rule, index) in selectedTemplate.rules" :key="`root-${index}`">
                <div
                  class="cls-tn"
                  :class="{ active: selectedRootIdx === index && selectedChildIdx < 0 }"
                  @click="selectRoot(index)"
                >
                  <span
                    class="cls-tn__arrow"
                    :class="{ open: !folded[index], leaf: !(rule.children?.length) }"
                    @click.stop="toggleFold(index)"
                  >▶</span>
                  <span class="cls-tn__fic">{{ folded[index] ? "📁" : "📂" }}</span>
                  <b>{{ rule.name }}</b>
                  <button
                    v-if="selectedTemplate.kind !== 'media'"
                    type="button"
                    class="cls-tn__add"
                    title="添加二级分类"
                    @click.stop="addChildRule(selectedTemplate, rule, index)"
                  >＋</button>
                  <button
                    v-if="selectedTemplate.kind === 'custom'"
                    type="button"
                    class="cls-tn__del"
                    :disabled="selectedTemplate.rules.length === 1"
                    title="删除"
                    @click.stop="removeRoot(selectedTemplate, index)"
                  >×</button>
                </div>
                <div v-if="!folded[index]" class="cls-tn__children">
                  <div
                    v-for="(child, childIndex) in (rule.children ?? [])"
                    :key="`child-${index}-${childIndex}`"
                    class="cls-tn cls-tn--child"
                    :class="{ active: selectedRootIdx === index && selectedChildIdx === childIndex }"
                    @click="selectChild(index, childIndex)"
                  >
                    <span class="cls-tn__arrow leaf">▶</span>
                    <span class="cls-tn__fic">📂</span>
                    <b>{{ child.name }}</b>
                    <button type="button" class="cls-tn__del" title="删除" @click.stop="removeChild(rule, childIndex)">×</button>
                  </div>
                </div>
              </template>
              <div v-if="!selectedTemplate.rules.length" class="cls-tree__empty">还没有分类目录</div>
            </div>
            <button
              v-if="selectedTemplate.kind === 'custom'"
              type="button"
              class="cls-tree__add"
              @click="addCustomRootRule(selectedTemplate)"
            >＋ 添加一级分类</button>
          </aside>

          <!-- 右侧编辑 -->
          <section class="cls-edit">
            <template v-if="selectedNode">
              <div class="cls-edit__head">
                <div class="cls-edit__title">
                  <span class="cls-edit__fic">{{ selectedNode.level === 0 ? "📁" : "📂" }}</span>
                  <span>{{ selectedNode.rule.name || "未命名" }}</span>
                  <span class="cls-edit__lvl">{{ selectedNode.level === 0 ? "一级" : "二级" }}</span>
                </div>
              </div>

              <div class="cls-field">
                <div class="cls-field__head"><span class="cls-field__label">目录名称</span></div>
                <input v-model.trim="selectedNode.rule.name" maxlength="120" placeholder="目录名" />
              </div>

              <div class="cls-field">
                <div class="cls-field__head">
                  <span class="cls-field__label">匹配条件</span>
                  <span v-if="editingLocked" class="cls-field__lock">
                    🔒 内置固定 <span class="cls-field__k">{{ selectedNode.rule.condition }}</span>
                  </span>
                </div>
                <input
                  v-model.trim="selectedNode.rule.condition"
                  class="cls-field__mono"
                  maxlength="500"
                  :readonly="editingLocked"
                  :placeholder="selectedTemplate.kind === 'region' ? 'origin_country=CN;HK' : selectedTemplate.kind === 'custom' ? 'type=tv，genres=真人秀' : 'genres=犯罪;悬疑'"
                />
              </div>

              <div
                v-if="selectedNode.level === 0 && (selectedNode.rule.children?.length ?? 0) > 0"
                class="cls-fallback"
              >
                <div class="cls-fallback__head">
                  <span>子分类均未命中时</span>
                  <SettingsHelpTooltip title="未命中处理说明">
                    <p>已识别为电影或电视剧，但未命中二级分类时，按这里的设置放置。</p>
                    <p>影视类型也无法识别时，将放在任务目标根目录。</p>
                  </SettingsHelpTooltip>
                </div>
                <div class="cls-fallback__options">
                  <button
                    type="button"
                    class="cls-fallback__choice"
                    :class="{ active: selectedNode.rule.fallback_mode !== 'directory' }"
                    @click="useFallbackDirectory(selectedNode.rule, false)"
                  >
                    <span class="cls-fallback__radio"></span>
                    <span>放入一级分类</span>
                  </button>
                  <div
                    class="cls-fallback__choice cls-fallback__choice--custom"
                    :class="{ active: selectedNode.rule.fallback_mode === 'directory' }"
                    @click="useFallbackDirectory(selectedNode.rule, true)"
                  >
                    <span class="cls-fallback__radio"></span>
                    <span>放入指定目录</span>
                    <input
                      v-model.trim="selectedNode.rule.fallback_dir"
                      maxlength="120"
                      placeholder="其他"
                      aria-label="未命中目录名称"
                      @focus="useFallbackDirectory(selectedNode.rule, true)"
                      @click.stop
                    />
                  </div>
                </div>
              </div>
            </template>
            <div v-else class="cls-edit__empty">从左侧选择一个分类目录开始编辑</div>
          </section>
        </div>

      <template #footer>
        <AppButton class="classification-help-button" variant="secondary" @click="helpOpen = true">查看帮助</AppButton>
        <AppButton variant="primary" :disabled="saving" @click="saveSettings">{{ saving ? "保存中…" : "保存" }}</AppButton>
      </template>
    </AppModal>

    <AppModal :open="helpOpen" title="分类帮助" size="lg" nested @close="helpOpen = false">
      <template #header>
        <div class="cls-help-head">
          <h3 class="modal-help-title">分类帮助</h3>
          <span class="cls-help-head__sub">移动整理时，影片会按模板放进分类目录；二级未命中时使用对应一级目录的设置，影视类型也无法识别时才放在目标根目录。</span>
        </div>
      </template>

      <!-- 条件怎么写 -->
      <section class="cls-help-sec">
        <div class="cls-help-sec__head"><span class="cls-help-sec__ic">∑</span><span class="cls-help-sec__title">条件怎么写</span></div>
        <div class="cls-help-syntax">
          <div class="cls-help-syntax__row">
            <span class="cls-help-syntax__op">或</span>
            <span class="cls-help-syntax__ex">origin_country=CN;US</span>
            <span class="cls-help-syntax__note">同字段多值用分号，CN 或 US 都命中</span>
          </div>
          <div class="cls-help-syntax__row">
            <span class="cls-help-syntax__op">且</span>
            <span class="cls-help-syntax__ex">type=tv，genres=动画</span>
            <span class="cls-help-syntax__note">不同字段用逗号，需同时满足（仅自定义）</span>
          </div>
        </div>
        <div class="cls-help-fields">
          <span>常用字段：</span><code>type</code><code>origin_country</code><code>genres</code>
          <span class="cls-help-fields__tip">不确定真实返回值？用下面的查询工具看实际字段。</span>
        </div>
      </section>

      <!-- TMDB 字段查询 -->
      <section class="cls-help-sec">
        <div class="cls-help-sec__head"><span class="cls-help-sec__ic">🔎</span><span class="cls-help-sec__title">TMDB 字段查询</span></div>
        <div class="classification-lookup">
          <div class="classification-lookup__intro">
            <strong>查真实字段</strong>
            <span>输入 ID 查看字段值，点「复制」直接用作匹配条件</span>
          </div>
          <form class="classification-lookup__form" @submit.prevent="lookupTMDBDetail">
            <select v-model="detailMediaType" aria-label="TMDB 媒体类型">
              <option value="movie">电影</option>
              <option value="tv">电视剧</option>
            </select>
            <input v-model.trim="detailTMDBID" inputmode="numeric" maxlength="10" aria-label="TMDB ID" placeholder="TMDB ID，如 281495" />
            <AppButton type="submit" variant="secondary" :disabled="detailLoading">
              {{ detailLoading ? "查询中…" : "查询" }}
            </AppButton>
          </form>

          <div v-if="detailResult" class="classification-detail">
            <div class="classification-detail__title">
              <strong>{{ detailTitle }}</strong>
              <span>TMDB {{ detailResult.id }} · {{ detailResult.media_type === "tv" ? "电视剧" : "电影" }}</span>
            </div>
            <div class="classification-detail__conditions">
              <button
                v-for="item in detailConditions"
                :key="item.value"
                type="button"
                :title="`复制 ${item.value}`"
                @click="copyCondition(item.value)"
              >
                <span>{{ item.label }}</span><code>{{ item.value }}</code><b>复制</b>
              </button>
            </div>
            <details class="classification-detail__raw">
              <summary>查看全部 TMDB 详情字段</summary>
              <pre>{{ detailJSON }}</pre>
            </details>
          </div>
        </div>
      </section>

      <template #footer>
        <AppButton variant="primary" @click="helpOpen = false">关闭</AppButton>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.check-toggle { width: 28px; height: 28px; border-radius: 50%; border: 0; padding: 0; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; cursor: pointer; background: var(--border); color: var(--text-muted); transition: background .18s ease, color .18s ease, box-shadow .18s ease; }
.check-toggle svg { width: 14px; height: 14px; }
.check-toggle:hover { background: var(--surface-hover); }
.check-toggle.on { background: var(--success); color: #fff; box-shadow: 0 0 0 4px rgba(16, 185, 129, .16); }
.check-toggle:disabled { opacity: .5; cursor: not-allowed; }
.classification-template-tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--border); }
.classification-template-tab { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 2px; align-items: center; padding: 10px 8px 12px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--text-muted); cursor: pointer; transition: 0.15s; font-family: inherit; }
.classification-template-tab strong { font-size: 13px; font-weight: 600; }
.classification-template-tab span { color: var(--text-faint); font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }
.classification-template-tab:hover { color: var(--text); }
.classification-template-tab.active { color: var(--brand); border-bottom-color: var(--brand); }
.classification-template-tab.active span { color: var(--text-muted); }

/* ── 文件树 + 右侧编辑工作台 ── */
.cls-workspace { display: grid; grid-template-columns: 250px minmax(0, 1fr); min-height: 420px; }

/* 左侧树 */
.cls-tree { border-right: 1px solid var(--border); background: var(--surface-sunken); padding: 14px 12px; display: flex; flex-direction: column; gap: 6px; min-width: 0; }
.cls-tree__cap { font-size: 11px; color: var(--text-muted); letter-spacing: .06em; padding: 2px 6px 6px; }
.cls-tree__list { overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: 1px; padding-bottom: 10px; min-height: 0; }
.cls-tree__empty { padding: 22px 8px; text-align: center; color: var(--text-muted); font-size: 12px; }
.cls-tree__add { margin-top: auto; width: 100%; padding: 8px 12px; border-radius: 9px; border: 1px dashed var(--border2); background: transparent; color: var(--text-muted); font-size: 13px; cursor: pointer; transition: .12s; }
.cls-tree__add:hover { border-color: var(--brand); color: var(--brand); background: var(--brand-soft); }

.cls-tn { position: relative; display: flex; align-items: center; gap: 6px; padding: 6px 8px; border-radius: 8px; border: 1px solid transparent; cursor: pointer; transition: .1s; min-width: 0; user-select: none; }
.cls-tn:hover { background: var(--surface); }
.cls-tn.active { background: var(--brand-soft); border-color: color-mix(in srgb, var(--brand) 40%, transparent); }
.cls-tn__arrow { width: 14px; height: 14px; border-radius: 4px; display: flex; align-items: center; justify-content: center; font-size: 8px; color: var(--text-muted); flex-shrink: 0; transition: .12s; }
.cls-tn__arrow:hover { background: var(--surface); }
.cls-tn__arrow.open { transform: rotate(90deg); }
.cls-tn__arrow.leaf { visibility: hidden; }
.cls-tn__fic { font-size: 15px; flex-shrink: 0; }
.cls-tn b { font-size: 13px; font-weight: 500; color: var(--text); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; }
.cls-tn__add { width: 20px; height: 20px; border: 0; border-radius: 5px; background: transparent; color: var(--brand); font-size: 13px; line-height: 1; cursor: pointer; transition: .12s; flex-shrink: 0; display: flex; align-items: center; justify-content: center; padding: 0; opacity: .55; }
.cls-tn__add:hover { background: var(--brand-soft); opacity: 1; }
.cls-tn__del { width: 20px; height: 20px; border: 0; border-radius: 5px; background: transparent; color: var(--text-muted); font-size: 12px; cursor: pointer; transition: .12s; flex-shrink: 0; display: flex; align-items: center; justify-content: center; padding: 0; opacity: .55; }
.cls-tn__del:hover { background: rgba(239, 68, 68, .12); color: var(--danger); opacity: 1; }
.cls-tn__del:disabled { opacity: 0; cursor: default; }
.cls-tn--child { padding-left: 24px; }
.cls-tn--child::before { content: ""; position: absolute; left: 10px; top: 0; bottom: 0; width: 1px; background: var(--border2); }
.cls-tn--child::after { content: ""; position: absolute; left: 10px; top: 50%; width: 11px; height: 1px; background: var(--border2); }

/* 右侧编辑 */
.cls-edit { padding: 18px 24px 20px; display: flex; flex-direction: column; gap: 15px; overflow-y: auto; min-width: 0; background: var(--surface); }
.cls-edit__head { display: flex; align-items: center; }
.cls-edit__title { font-size: 15px; font-weight: 600; color: var(--text); display: flex; align-items: center; gap: 8px; }
.cls-edit__fic { font-size: 16px; }
.cls-edit__lvl { display: inline-flex; align-items: center; height: 20px; margin-left: 4px; padding: 0 9px; border-radius: 999px; background: var(--brand-soft); color: var(--brand); font-size: 11px; flex-shrink: 0; }
.cls-edit__empty { flex: 1; display: flex; align-items: center; justify-content: center; color: var(--text-muted); font-size: 13px; }

.cls-field { display: flex; flex-direction: column; gap: 7px; }
.cls-field__head { display: flex; align-items: center; gap: 6px; }
.cls-field__label { font-size: 13px; font-weight: 500; color: var(--text-regular); }
.cls-field__lock { margin-left: auto; display: inline-flex; align-items: center; gap: 4px; font-size: 11px; color: var(--text-muted); }
.cls-field__k { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10.5px; background: var(--surface-sunken); border-radius: 4px; padding: 1px 5px; color: var(--text-muted); }
.cls-field input { width: 100%; padding: 9px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface); color: var(--text); font-size: 13px; outline: none; transition: border-color .15s, box-shadow .15s; font-family: inherit; box-sizing: border-box; }
.cls-field input:focus { border-color: var(--brand); box-shadow: 0 0 0 3px var(--brand-soft); }
.cls-field input::placeholder { color: var(--text-faint); }
.cls-field input:read-only { background: var(--surface-sunken); color: var(--text-muted); cursor: default; }
.cls-field__mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; }

.cls-fallback { display: flex; flex-direction: column; gap: 7px; min-width: 0; padding: 0; }
.cls-fallback__head { display: flex; align-items: center; gap: 6px; color: var(--text-regular); font-size: 13px; font-weight: 500; white-space: nowrap; }
.cls-fallback__options { display: flex; align-items: center; gap: 7px; min-width: 0; overflow-x: auto; scrollbar-width: none; }
.cls-fallback__options::-webkit-scrollbar { display: none; }
.cls-fallback__choice { min-height: 34px; display: flex; align-items: center; gap: 7px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 8px; color: var(--text-regular); background: transparent; font-family: inherit; font-size: 12.5px; white-space: nowrap; cursor: pointer; transition: border-color .15s, color .15s, background .15s; }
.cls-fallback__choice:hover { border-color: color-mix(in srgb, var(--brand) 45%, var(--border)); }
.cls-fallback__choice.active { border-color: var(--brand); color: var(--brand); background: var(--brand-soft); }
.cls-fallback__choice--custom { flex: 1; min-width: 280px; }
.cls-fallback__radio { width: 14px; height: 14px; border: 1.5px solid var(--border2); border-radius: 50%; background: var(--surface); box-shadow: inset 0 0 0 3px var(--surface); flex-shrink: 0; }
.cls-fallback__choice.active .cls-fallback__radio { border-color: var(--brand); background: var(--brand); }
.cls-fallback__choice input { min-width: 100px; width: 100%; height: 25px; padding: 0 8px; border: 0; border-left: 1px solid var(--border); border-radius: 0; outline: none; color: var(--text); background: transparent; font-family: inherit; font-size: 12px; }
.cls-fallback__choice:not(.active) input { color: var(--text-faint); background: var(--surface-sunken); pointer-events: none; }
.cls-fallback__choice.active input:focus { border-color: var(--brand); box-shadow: 0 0 0 2px color-mix(in srgb, var(--brand) 14%, transparent); }

.classification-help-button { margin-right: auto; }

/* ── 帮助弹窗头部：标题 + 副标题 ── */
.cls-help-head { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.cls-help-head .modal-help-title { margin: 0; font-size: 15px; font-weight: 600; color: var(--text); }
.cls-help-head__sub { font-size: 12.5px; color: var(--text-muted); line-height: 1.5; }

/* ── 帮助弹窗：区块排版 ── */
.cls-help-sec { display: flex; flex-direction: column; gap: 10px; }
.cls-help-sec + .cls-help-sec { margin-top: 16px; }
.cls-help-sec__head { display: flex; align-items: center; gap: 8px; }
.cls-help-sec__ic { width: 24px; height: 24px; border-radius: 7px; background: var(--brand-soft); color: var(--brand); display: flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; }
.cls-help-sec__title { font-size: 14px; font-weight: 600; color: var(--text); }

.cls-help-syntax { display: flex; flex-direction: column; gap: 8px; }
.cls-help-syntax__row { display: grid; grid-template-columns: 56px minmax(0, 1fr) auto; gap: 10px; align-items: center; border: 1px solid var(--border); border-radius: 10px; padding: 9px 12px; background: var(--surface-sunken); }
.cls-help-syntax__op { font-size: 12px; font-weight: 600; color: var(--brand); background: var(--brand-soft); border-radius: 6px; padding: 3px 8px; text-align: center; }
.cls-help-syntax__ex { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cls-help-syntax__note { font-size: 12.5px; color: var(--muted); white-space: nowrap; }

.cls-help-fields { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 13px; color: var(--text-regular); }
.cls-help-fields code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; background: var(--surface-sunken); border-radius: 5px; padding: 2px 7px; color: var(--text); }
.cls-help-fields__tip { color: var(--text-muted); font-size: 12px; }

/* ── bare 自绘弹窗：贴边结构 ── */
.classification-lookup { margin-top: 10px; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--surface-sunken); }
.classification-lookup__intro { display: flex; align-items: baseline; gap: 8px; }
.classification-lookup__intro strong { font-size: 13px; font-weight: 600; }
.classification-lookup__intro span { color: var(--text-muted); font-size: 12.5px; }
.classification-lookup__form { display: grid; grid-template-columns: 110px minmax(180px, 1fr) auto; gap: 8px; margin-top: 9px; }
.classification-lookup__form select, .classification-lookup__form input { box-sizing: border-box; min-width: 0; width: 100%; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 8px 9px; background: var(--surface); color: var(--text); font-size: 13px; }
.classification-detail { margin-top: 11px; padding-top: 11px; border-top: 1px solid var(--border); }
.classification-detail__title { display: flex; align-items: baseline; gap: 8px; }
.classification-detail__title strong { font-size: 14px; font-weight: 600; }
.classification-detail__title span { color: var(--text-muted); font-size: 12.5px; }
.classification-detail__conditions { display: grid; gap: 6px; margin-top: 9px; }
.classification-detail__conditions button { display: grid; grid-template-columns: 72px minmax(0, 1fr) auto; gap: 8px; align-items: center; width: 100%; padding: 7px 9px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); text-align: left; cursor: pointer; }
.classification-detail__conditions button:hover { border-color: var(--brand); }
.classification-detail__conditions span { color: var(--text-muted); font-size: 12.5px; }
.classification-detail__conditions code { overflow-wrap: anywhere; white-space: normal; }
.classification-detail__conditions b { color: var(--brand); font-size: 12px; }
.classification-detail__raw { margin-top: 9px; color: var(--text-regular); font-size: 12.5px; }
.classification-detail__raw summary { cursor: pointer; color: var(--brand); }
.classification-detail__raw pre { box-sizing: border-box; max-height: 280px; overflow: auto; margin: 8px 0 0; padding: 10px; border-radius: var(--radius-sm); background: var(--surface); color: var(--text); font-size: 11.5px; line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 760px) {
  .classification-template-tabs { flex-wrap: wrap; }
  .classification-template-tab { flex: 1 1 45%; }
  .cls-workspace { grid-template-columns: 1fr; }
  .cls-tree { border-right: 0; border-bottom: 1px solid var(--border); }
  .classification-lookup__intro, .classification-detail__title { align-items: flex-start; flex-direction: column; gap: 2px; }
  .classification-lookup__form { grid-template-columns: 1fr; }
  .classification-detail__conditions button { grid-template-columns: 1fr auto; }
  .classification-detail__conditions span { grid-column: 1 / -1; }
}
@media (max-width: 480px) {
  .classification-template-tabs { flex-direction: column; align-items: stretch; }
  .classification-template-tab { border-bottom: 0; border-left: 2px solid transparent; align-items: flex-start; padding: 8px 10px; }
  .classification-template-tab.active { border-left-color: var(--brand); border-bottom-color: transparent; }
}
</style>
