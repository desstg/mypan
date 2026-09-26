<script setup lang="ts">
import { reactive } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import JavStringListEditor from "@/components/admin/JavStringListEditor.vue";
import type { MediaOrganizeJavClassifyRule } from "@/api/mediaOrganize";

// 「分类移动」编辑器：一条规则一张卡，可加可删可调优先级。
//
// 规则顺序即优先级 —— 首个命中者胜，所以每条卡上都有上移/下移。

const props = defineProps<{
  modelValue: MediaOrganizeJavClassifyRule[];
  disabled?: boolean;
}>();

const emit = defineEmits<{ "update:modelValue": [MediaOrganizeJavClassifyRule[]] }>();

const modeOptions = [
  { value: "includes", label: "关键词" },
  { value: "pattern", label: "正则" },
  { value: "nocode", label: "无番号" },
];

// 折叠状态是**纯界面状态**，不进数据、不参与脏判断 —— 它只是让你把规则收起来，
// 避免十几条规则堆成一面墙。
//
// 默认**收起**：规则一多，一屏就能看全「优先级 + 规则名 → 目标目录」的脉络，
// 要改哪条再展开哪条。收起时卡片头那行摘要就是为此存在的。
//
// 按「下标」而不是按对象引用来记：patch() 每次编辑都产生新对象，按引用记的话
// 一敲键盘折叠状态就丢；而规则本身没有稳定 id（数据模型里只有 name/target_name/…）。
// 代价是上下移动规则时，折叠状态跟着「位置」走而不是跟着规则走 —— 可以接受。
const collapsed = reactive<Record<number, boolean>>({});

// 只有被显式展开过（记成 false）才算展开，没记录过的默认收起
function isCollapsed(index: number): boolean {
  return collapsed[index] !== false;
}

function toggleCollapsed(index: number) {
  collapsed[index] = !isCollapsed(index);
}

function update(next: MediaOrganizeJavClassifyRule[]) {
  // 规则数变少时把尾巴上的折叠状态清掉，免得下次加规则时「继承」一个莫名其妙的折叠态
  for (const key of Object.keys(collapsed)) {
    if (Number(key) >= next.length) delete collapsed[Number(key)];
  }
  emit("update:modelValue", next);
}

function patch(index: number, changes: Partial<MediaOrganizeJavClassifyRule>) {
  update(props.modelValue.map((r, i) => (i === index ? { ...r, ...changes } : r)));
}

/**
 * 当前规则用哪种匹配方式。以显式的 mode 为准（后端 EffectiveMode 同一套逻辑），
 * 老数据没有 mode 时才按旧规则反推。
 */
function modeOf(rule: MediaOrganizeJavClassifyRule): string {
  if (rule.mode === "nocode" || rule.mode === "pattern" || rule.mode === "includes") return rule.mode;
  if (rule.nocode) return "nocode";
  if (rule.pattern) return "pattern";
  if (rule.includes?.length) return "includes";
  return "includes";
}

/**
 * 这条规则是不是「国产」那一档。
 *
 * 只用来决定提示文案：国产那档的**关键词**同时还是「国产厂牌表」—— 后端的改名
 * 与认侧车都会读它（无连字符的形态 `MD0292` 也认，会自动补成 `MD-0292`）。
 * 判据与后端 javrules.isCNClassifyRule 一致：目标目录或规则名以「国产」开头。
 * 两个字段都看，是因为这两个字段用户都能改 —— 只看目标目录的话，改个名这个
 * 提示就没了。
 */
function isCNRule(rule: MediaOrganizeJavClassifyRule): boolean {
  const target = (rule.target_name ?? "").trim();
  const name = (rule.name ?? "").trim();
  return target.startsWith("国产") || name.startsWith("国产");
}

/**
 * 关键词那一栏的底部说明。
 *
 * 国产那档要多说一句：那里的词不只是分类关键词，还是**厂牌表** —— 遇到没收录的
 * 国产厂牌，填在这里就够了，不必等发版。
 */
function includesHint(rule: MediaOrganizeJavClassifyRule): string {
  const base = "名字里出现任意一个就算命中，不区分大小写。";
  if (!isCNRule(rule)) return base;
  return (
    base +
    "国产厂牌也填这里：分类、认侧车、改名都会认（无连字符的 MD0292 也认，会自动补成 MD-0292）。" +
    "别填太短 —— 关键词是子串匹配，填 SS 会连 SSIS-001 一起命中。"
  );
}

/**
 * 切换匹配方式**只改 mode**，不动另外两种方式的输入内容。
 *
 * 之前是「切过去就把另一种清空」，后果很糟：点一下「正则」，关键词整批消失；
 * 而正则还没填内容时会被反推回「关键词」，按钮看起来像坏的。
 * 现在来回切不丢东西，草稿留着切回去就能用。
 */
function setMode(index: number, mode: string) {
  // SettingsSegment 只回传 string，收窄成联合类型再落进数据
  if (mode !== "nocode" && mode !== "pattern" && mode !== "includes") return;
  patch(index, { mode });
}

function addRule() {
  const next = [
    ...props.modelValue,
    { name: `规则 ${props.modelValue.length + 1}`, target_name: "", includes: [] },
  ];
  // 新加的一条显式展开：规则默认是收起的，不特判的话刚点完「添加」
  // 就得再点一次展开才能填内容。
  collapsed[next.length - 1] = false;
  update(next);
}

function removeRule(index: number) {
  const next = [...props.modelValue];
  next.splice(index, 1);
  update(next);
}

function move(index: number, delta: number) {
  const next = [...props.modelValue];
  const target = index + delta;
  if (target < 0 || target >= next.length) return;
  [next[index], next[target]] = [next[target], next[index]];
  update(next);
}
</script>

<template>
  <div class="jav-classify">
    <div v-if="!modelValue.length" class="jav-classify__empty">暂无分类规则</div>

    <div v-for="(rule, index) in modelValue" :key="`classify-${index}`" class="jav-classify__card">
      <div class="jav-classify__head">
        <button
          type="button"
          class="jav-classify__collapse"
          :aria-expanded="!isCollapsed(index)"
          :title="isCollapsed(index) ? '展开' : '收起'"
          @click="toggleCollapsed(index)"
        >
          {{ isCollapsed(index) ? "▸" : "▾" }}
        </button>
        <span class="jav-classify__order">优先级 {{ index + 1 }}</span>
        <!-- 收起时给一行摘要，否则折叠后只剩一排「优先级 N」，认不出哪条是哪条 -->
        <span v-if="isCollapsed(index)" class="jav-classify__summary">
          {{ rule.name || "（未命名）" }}
          <template v-if="rule.target_name">→ {{ rule.target_name }}</template>
        </span>
        <div class="jav-classify__head-actions">
          <button
            type="button"
            class="jav-classify__btn"
            title="上移（优先级更高）"
            :disabled="disabled || index === 0"
            @click="move(index, -1)"
          >
            ↑
          </button>
          <button
            type="button"
            class="jav-classify__btn"
            title="下移"
            :disabled="disabled || index === modelValue.length - 1"
            @click="move(index, 1)"
          >
            ↓
          </button>
          <button
            type="button"
            class="jav-classify__btn jav-classify__btn--remove"
            title="删除这条规则"
            :disabled="disabled"
            @click="removeRule(index)"
          >
            ×
          </button>
        </div>
      </div>

      <template v-if="!isCollapsed(index)">
        <div class="jav-classify__field">
          <label class="jav-classify__label">规则名称</label>
          <AppInput
            :model-value="rule.name"
            placeholder="例如：国外"
            :disabled="disabled"
            @update:model-value="patch(index, { name: $event })"
          />
        </div>

        <div class="jav-classify__field">
          <label class="jav-classify__label">目标目录</label>
          <AppInput
            :model-value="rule.target_name"
            placeholder="移动到目标根目录下的哪个子目录，例如：国外AV"
            :disabled="disabled"
            @update:model-value="patch(index, { target_name: $event })"
          />
        </div>

        <div class="jav-classify__field">
          <label class="jav-classify__label">
            匹配方式
            <SettingsHelpTooltip title="匹配方式说明">
              <p>判断按这个顺序：<b>无番号 → 正则 → 关键词</b>，先过「排除词」。</p>
              <p><b>关键词</b>：名字里出现任意一个词就命中（不区分大小写）。适合固定前缀，如 FC2PPV、MD-。</p>
              <p><b>正则</b>：整名匹配，例如 ^[A-Za-z]&#123;2,6&#125;-\d&#123;2,5&#125; 抓标准番号。</p>
              <p><b>无番号</b>：兜底用，没识别出番号的都归到这里。建议放在最后一条。</p>
            </SettingsHelpTooltip>
          </label>
          <SettingsSegment
            :model-value="modeOf(rule)"
            :options="modeOptions"
            :label="`第 ${index + 1} 条规则的匹配方式`"
            @update:model-value="setMode(index, $event)"
          />
        </div>

        <div v-if="modeOf(rule) === 'pattern'" class="jav-classify__field">
          <label class="jav-classify__label">正则</label>
          <AppInput
            :model-value="rule.pattern ?? ''"
            placeholder="例如 ^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}"
            :disabled="disabled"
            @update:model-value="patch(index, { pattern: $event })"
          />
        </div>

        <div v-else-if="modeOf(rule) === 'includes'" class="jav-classify__field jav-classify__field--stack">
          <label class="jav-classify__label">关键词</label>
          <JavStringListEditor
            :model-value="rule.includes ?? []"
            placeholder="输入关键词后回车"
            empty-hint="至少加一个关键词，否则这条规则永远不会命中"
            :input-hint="includesHint(rule)"
            :disabled="disabled"
            @update:model-value="patch(index, { includes: $event })"
          />
        </div>

        <div class="jav-classify__field jav-classify__field--stack">
          <label class="jav-classify__label">
            排除词
            <SettingsHelpTooltip title="排除词说明">
              <p>名字里出现任意一个排除词就跳过这条规则，继续往下试别的规则。可以留空。</p>
            </SettingsHelpTooltip>
          </label>
          <JavStringListEditor
            :model-value="rule.excludes ?? []"
            placeholder="输入排除词后回车"
            empty-hint="无"
            :disabled="disabled"
            @update:model-value="patch(index, { excludes: $event })"
          />
        </div>
      </template>
    </div>

    <div>
      <AppButton type="button" variant="secondary" size="sm" :disabled="disabled" @click="addRule">
        + 添加分类规则
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.jav-classify {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.jav-classify__empty {
  font-size: 13px;
  color: var(--text-muted);
}

/* 每条规则一张卡，嵌套卡片沿用设置页的圆角/边框/阴影语言 */
.jav-classify__card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
  background: var(--surface-sunken);
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-sm);
}

.jav-classify__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 折叠箭头：观感对齐计划预览里的 ▾/▸（MediaOrganizePanel 的 organize-plan-skipped-chevron） */
.jav-classify__collapse {
  flex: none;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  transition: var(--transition);
}

.jav-classify__collapse:hover {
  color: var(--brand);
  background: color-mix(in srgb, var(--brand) 12%, transparent);
}

.jav-classify__order {
  flex: none;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}

/* 收起后的一行摘要：否则折叠完整排都是「优先级 N」，认不出哪条是哪条。
   min-width:0 + ellipsis 让长规则名不会把右边的 ↑↓× 挤走。 */
.jav-classify__summary {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text);
}

/* margin-left: auto 把 ↑↓× 顶到右边；收起时摘要占满剩余宽度，这条不生效也无妨 */
.jav-classify__head-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
}

.jav-classify__btn {
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  transition: var(--transition);
}

.jav-classify__btn:hover:not(:disabled) {
  color: var(--brand);
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border));
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}

.jav-classify__btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.jav-classify__btn--remove:hover:not(:disabled) {
  color: var(--danger, #ef4444);
  border-color: color-mix(in srgb, var(--danger, #ef4444) 40%, var(--border));
  background: color-mix(in srgb, var(--danger, #ef4444) 8%, var(--surface));
}

.jav-classify__field {
  display: grid;
  grid-template-columns: 96px 1fr;
  gap: 10px;
  align-items: center;
}

.jav-classify__field--stack {
  align-items: start;
}

.jav-classify__field--stack .jav-classify__label {
  padding-top: 8px;
}

.jav-classify__label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--text-regular, var(--text));
}

@media (max-width: 640px) {
  .jav-classify__field {
    grid-template-columns: 1fr;
  }

  .jav-classify__field--stack .jav-classify__label {
    padding-top: 0;
  }
}
</style>
