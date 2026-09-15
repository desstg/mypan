<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  spaceCleanupApi,
  type CleanupGroup,
  type CleanupItem,
  type CleanupResult,
  type CleanupRisk,
  type CleanupScanReport,
} from "@/api/spaceCleanup";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import { formatSize } from "@/utils/format";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";

interface DisplayItem {
  key: string;
  name: string;
  reason: string;
  risk: CleanupRisk;
  items: CleanupItem[];
  paths: string[];
  sizeBytes: number;
  memoryBytes: number;
  fileCount: number;
  dirCount: number;
}

interface DisplayGroup {
  key: string;
  label: string;
  description: string;
  count: number;
  sizeBytes: number;
  memoryBytes: number;
  source: CleanupGroup;
  items: DisplayItem[];
}

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const open = ref(false);
const scanning = ref(false);
const cleaning = ref(false);
const report = ref<CleanupScanReport | null>(null);
const scanError = ref("");
const selected = ref(new Set<string>());
const expanded = ref(new Set<string>());
const lastResult = ref<CleanupResult | null>(null);

const allItems = computed(() => report.value?.groups.flatMap((group) => group.items) ?? []);
const selectedItems = computed(() => allItems.value.filter((item) => selected.value.has(item.id)));
const recommendedItems = computed(() => allItems.value.filter((item) => item.default_selected));
const selectedDiskBytes = computed(() => selectedItems.value.reduce((sum, item) => sum + item.size_bytes, 0));
const selectedMemoryBytes = computed(() => selectedItems.value.reduce((sum, item) => sum + (item.memory_bytes ?? 0), 0));
const recommendedDiskBytes = computed(() => recommendedItems.value.reduce((sum, item) => sum + item.size_bytes, 0));
const recommendedMemoryBytes = computed(() => recommendedItems.value.reduce((sum, item) => sum + (item.memory_bytes ?? 0), 0));
const noteworthyResults = computed(() => lastResult.value?.results.filter((item) => item.status !== "cleaned") ?? []);

const displayGroups = computed<DisplayGroup[]>(() => {
  if (!report.value) return [];
  return report.value.groups
    .filter((group) => group.count > 0)
    .map((group) => {
      const buckets = new Map<string, DisplayItem>();
      for (const item of group.items) {
        const key = `${group.key}\u0000${item.name}\u0000${item.risk}\u0000${item.default_selected}`;
        let display = buckets.get(key);
        if (!display) {
          display = {
            key,
            name: item.name,
            reason: item.reason,
            risk: item.risk,
            items: [],
            paths: [],
            sizeBytes: 0,
            memoryBytes: 0,
            fileCount: 0,
            dirCount: 0,
          };
          buckets.set(key, display);
        }
        display.items.push(item);
        if (item.path) display.paths.push(item.path);
        display.sizeBytes += item.size_bytes;
        display.memoryBytes += item.memory_bytes ?? 0;
        display.fileCount += item.file_count ?? 0;
        display.dirCount += item.dir_count ?? 0;
      }
      return {
        key: group.key,
        label: group.label,
        description: group.description,
        count: group.count,
        sizeBytes: group.size_bytes,
        memoryBytes: group.memory_bytes ?? 0,
        source: group,
        items: [...buckets.values()],
      };
    });
});

const selectedDisplayCount = computed(() =>
  displayGroups.value.flatMap((group) => group.items).filter((item) => selectedCount(item.items) > 0).length,
);

const healthScore = computed(() => {
  const count = recommendedItems.value.length;
  if (count === 0) return 100;
  const bytes = recommendedDiskBytes.value + recommendedMemoryBytes.value;
  const mib = bytes / (1024 * 1024);
  const sizePenalty = Math.min(32, Math.ceil(Math.log2(mib + 1) * 2.5));
  const countPenalty = Math.min(10, Math.ceil(Math.log10(count + 1) * 4));
  const categoryCount = new Set(recommendedItems.value.map((item) => item.category)).size;
  const categoryPenalty = Math.min(6, categoryCount * 2);
  return Math.max(52, 100 - sizePenalty - countPenalty - categoryPenalty);
});

const scoreTone = computed(() => {
  if (healthScore.value >= 90) return "excellent";
  if (healthScore.value >= 75) return "good";
  if (healthScore.value >= 60) return "attention";
  return "warning";
});

const scoreTitle = computed(() => {
  if (healthScore.value === 100) return "本地状态非常好";
  if (healthScore.value >= 90) return "本地状态良好";
  if (healthScore.value >= 75) return "发现一些可清理内容";
  return "建议进行一次清理";
});

const scoreDescription = computed(() => {
  if (recommendedItems.value.length === 0) return "没有发现建议立即处理的本地残留";
  return `发现 ${recommendedItems.value.length} 项可安全清理内容，不会访问网盘`;
});

const scoreRingStyle = computed(() => ({ "--cleanup-score-angle": `${healthScore.value * 3.6}deg` }));
const statValue = computed(() => (report.value ? healthScore.value : "—"));
const statLabel = computed(() => (report.value ? "综合评分" : "等待体检"));

function matches(title: string) {
  const query = props.searchQuery.trim().toLowerCase();
  return !query || title.toLowerCase().includes(query);
}

function replaceSelected(next: Set<string>) {
  selected.value = next;
}

function selectedCount(items: CleanupItem[]) {
  return items.reduce((count, item) => count + (selected.value.has(item.id) ? 1 : 0), 0);
}

function selectionState(items: CleanupItem[]) {
  const count = selectedCount(items);
  if (count === 0) return "none";
  if (count === items.length) return "all";
  return "partial";
}

function toggleItems(items: CleanupItem[]) {
  const next = new Set(selected.value);
  const allSelected = items.every((item) => next.has(item.id));
  for (const item of items) {
    if (allSelected) next.delete(item.id);
    else next.add(item.id);
  }
  replaceSelected(next);
}

function toggleGroup(group: CleanupGroup) {
  toggleItems(group.items);
}

function toggleExpanded(key: string) {
  const next = new Set(expanded.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expanded.value = next;
}

function categoryIcon(category: string) {
  switch (category) {
    case "strm":
      return "fa-film";
    case "temp":
      return "fa-broom";
    case "logs":
      return "fa-file-lines";
    case "cache":
      return "fa-bolt";
    default:
      return "fa-database";
  }
}

function riskLabel(risk: CleanupRisk) {
  switch (risk) {
    case "review":
      return "需确认";
    case "rebuild":
      return "会重建";
    case "locking":
      return "会短暂占用";
    default:
      return "可安全清理";
  }
}

function displayReason(item: DisplayItem) {
  if (item.items.length === 1) return item.reason;
  switch (item.name) {
    case "系统杂项文件":
      return "多个目录中由 macOS、Windows 或 Linux 自动生成的杂项文件";
    case "失效刮削索引":
      return "对应的 STRM 任务已经不存在";
    case "未关联 STRM 目录":
      return "这些目录不属于任何现有 STRM 任务，请确认不是特意保留的内容";
    case "未关联 STRM 文件":
      return "这些文件位于 STRM 目录中，但不属于任何现有任务";
    default:
      return item.reason;
  }
}

function sizeText(sizeBytes: number, memoryBytes = 0, fallback = "可清理") {
  const parts: string[] = [];
  if (sizeBytes > 0) parts.push(formatSize(sizeBytes));
  if (memoryBytes > 0) parts.push(`内存 ${formatSize(memoryBytes)}`);
  return parts.join(" · ") || fallback;
}

function releaseText(diskBytes: number, memoryBytes: number) {
  const parts: string[] = [];
  if (diskBytes > 0) parts.push(`${formatSize(diskBytes)} 本地空间`);
  if (memoryBytes > 0) parts.push(`${formatSize(memoryBytes)} 缓存内存`);
  return parts.join("和") || "少量无效数据";
}

function recommendedText() {
  if (recommendedDiskBytes.value === 0 && recommendedMemoryBytes.value === 0) {
    return recommendedItems.value.length > 0 ? `${recommendedItems.value.length} 项无效数据` : "无需清理";
  }
  return sizeText(recommendedDiskBytes.value, recommendedMemoryBytes.value);
}

async function scan() {
  scanning.value = true;
  scanError.value = "";
  report.value = null;
  expanded.value = new Set();
  replaceSelected(new Set());
  try {
    const data = await spaceCleanupApi.scan();
    report.value = data;
    replaceSelected(
      new Set(
        data.groups
          .flatMap((group) => group.items)
          .filter((item) => item.default_selected)
          .map((item) => item.id),
      ),
    );
  } catch (error) {
    scanError.value = getApiErrorMessage(error, "本地垃圾扫描失败");
    toast.error(scanError.value);
  } finally {
    scanning.value = false;
  }
}

async function openTool() {
  open.value = true;
  lastResult.value = null;
  await scan();
}

// 页面刷新后恢复最近一次未过期扫描（静默），避免卡片回到“等待体检”。
async function restoreLatestReport() {
  try {
    const data = await spaceCleanupApi.latestReport();
    if (!data.report) return;
    report.value = data.report;
    replaceSelected(
      new Set(
        data.report.groups
          .flatMap((group) => group.items)
          .filter((item) => item.default_selected)
          .map((item) => item.id),
      ),
    );
  } catch {
    // 恢复失败保持“等待体检”，不影响使用
  }
}

onMounted(() => {
  void restoreLatestReport();
});

function closeTool() {
  if (cleaning.value) return;
  open.value = false;
}

async function executeCleanup() {
  if (!report.value || selectedItems.value.length === 0) {
    toast.info("请先选择要清理的项目");
    return;
  }
  const needsReview = selectedItems.value.some((item) => item.risk !== "safe");
  const estimatedRelease = releaseText(selectedDiskBytes.value, selectedMemoryBytes.value);
  const ok = await confirm({
    title: needsReview ? "确认清理所选项目？" : "开始清理？",
    message: needsReview
      ? `所选内容包含需确认或会重新生成的数据，预计释放 ${estimatedRelease}。程序会在删除前再次核对任务占用情况。`
      : `将清理 ${selectedDisplayCount.value} 类、${selectedItems.value.length} 项内容，预计释放 ${estimatedRelease}。`,
    confirmText: "确认清理",
    cancelText: "取消",
    danger: needsReview,
  }).catch(() => false);
  if (!ok) return;

  cleaning.value = true;
  try {
    lastResult.value = await spaceCleanupApi.execute(report.value.scan_id, [...selected.value]);
    const result = lastResult.value;
    if (result.failed_items > 0) {
      toast.warning(`已清理 ${result.cleaned_items} 项，${result.failed_items} 项失败`);
    } else {
      toast.success(`清理完成，释放 ${releaseText(result.freed_bytes, result.memory_freed_bytes)}`);
    }
    await scan();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "垃圾清理失败"));
  } finally {
    cleaning.value = false;
  }
}
</script>

<template>
  <div v-show="matches('垃圾清理工具')">
    <CloudToolCard
      :enabled="true"
      name="垃圾清理工具"
      driver="本地数据 · 扫描预览后清理"
      logo-src="/logos/cleanup.png"
      logo-alt="垃圾清理工具"
      :stat-value="statValue"
      :stat-label="statLabel"
    >
      扫描 STRM 残留、上传与下载临时文件、历史日志和缓存占用，按风险分级预览，勾选后安全清除。
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="scanning" @click="openTool">
          {{ scanning ? "扫描中…" : "开始扫描" }}
        </AppButton>
      </template>
    </CloudToolCard>

    <AppModal :open="open" title="垃圾清理" size="lg" @close="closeTool">
      <div v-if="scanning && !report" class="cleanup-loading">
        <span class="cleanup-spinner" />
        <strong>正在检查 MyPan 本地数据…</strong>
        <small>只扫描本地目录和缓存，不会访问网盘</small>
      </div>

      <template v-else-if="report">
        <section class="cleanup-hero" :class="`score-${scoreTone}`">
          <div class="cleanup-score" :style="scoreRingStyle">
            <div>
              <strong>{{ healthScore }}</strong>
              <span>综合评分</span>
            </div>
          </div>
          <div class="cleanup-hero__copy">
            <small>本地存储健康状态</small>
            <h3>{{ scoreTitle }}</h3>
            <p>{{ scoreDescription }}</p>
          </div>
          <div class="cleanup-hero__metrics">
            <div><span>建议清理</span><strong>{{ recommendedText() }}</strong></div>
            <div><span>检查结果</span><strong>{{ report.total_count.toLocaleString("zh-CN") }} 项</strong></div>
          </div>
          <AppButton size="sm" variant="secondary" :disabled="scanning || cleaning" @click="scan">
            {{ scanning ? "扫描中…" : "重新扫描" }}
          </AppButton>
        </section>

        <div v-if="lastResult" class="cleanup-result" :class="{ 'cleanup-result--warn': lastResult.failed_items > 0 }">
          <i class="fas fa-circle-check" />
          <span>
            已清理 {{ lastResult.cleaned_items }} 项，释放 {{ releaseText(lastResult.freed_bytes, lastResult.memory_freed_bytes) }}
            <template v-if="lastResult.skipped_items > 0">；{{ lastResult.skipped_items }} 项因状态变化被跳过</template>
            <template v-if="lastResult.failed_items > 0">；{{ lastResult.failed_items }} 项失败</template>
          </span>
        </div>
        <div v-if="noteworthyResults.length" class="cleanup-result-details">
          <div v-for="item in noteworthyResults" :key="item.id">
            <strong>{{ item.name }}</strong>
            <span>{{ item.status === "failed" ? "清理失败" : "已跳过" }}<template v-if="item.message">：{{ item.message }}</template></span>
          </div>
        </div>

        <div v-if="displayGroups.length" class="cleanup-groups">
          <section v-for="group in displayGroups" :key="group.key" class="cleanup-group">
            <header class="cleanup-group__head">
              <span class="cleanup-group__icon"><i class="fas" :class="categoryIcon(group.key)" /></span>
              <span class="cleanup-group__title">
                <strong>{{ group.label }}</strong>
                <small>{{ group.description }}</small>
              </span>
              <button type="button" @click="toggleGroup(group.source)">
                {{ selectionState(group.source.items) === "all" ? "取消全选" : "全部选择" }}
              </button>
            </header>

            <div class="cleanup-group__items">
              <div v-for="item in group.items" :key="item.key" class="cleanup-item">
                <input
                  type="checkbox"
                  :checked="selectionState(item.items) === 'all'"
                  :indeterminate="selectionState(item.items) === 'partial'"
                  :aria-label="`选择${item.name}`"
                  @change="toggleItems(item.items)"
                />
                <span class="cleanup-item__icon"><i class="fas" :class="categoryIcon(group.key)" /></span>
                <div class="cleanup-item__main">
                  <div class="cleanup-item__line">
                    <strong>{{ item.name }}</strong>
                    <em :class="`risk-${item.risk}`">{{ riskLabel(item.risk) }}</em>
                    <span v-if="item.items.length > 1" class="cleanup-item__count">{{ item.items.length }} 个位置</span>
                    <b>{{ sizeText(item.sizeBytes, item.memoryBytes) }}</b>
                  </div>
                  <p>{{ displayReason(item) }}</p>
                  <code v-if="item.paths.length === 1">{{ item.paths[0] }}</code>
                  <template v-else-if="item.paths.length > 1">
                    <button class="cleanup-item__paths-toggle" type="button" @click="toggleExpanded(item.key)">
                      {{ expanded.has(item.key) ? "收起路径" : `查看 ${item.paths.length} 个具体路径` }}
                      <i class="fas" :class="expanded.has(item.key) ? 'fa-chevron-up' : 'fa-chevron-down'" />
                    </button>
                    <div v-if="expanded.has(item.key)" class="cleanup-item__paths">
                      <code v-for="path in item.paths" :key="path">{{ path }}</code>
                    </div>
                  </template>
                </div>
              </div>
            </div>
          </section>
        </div>

        <div v-else class="cleanup-empty">
          <span class="cleanup-empty__icon"><i class="fas fa-shield" /></span>
          <strong>本地很干净</strong>
          <span>没有发现可清理项目</span>
        </div>
      </template>

      <div v-else class="cleanup-empty">
        <span class="cleanup-empty__icon cleanup-empty__icon--warn"><i class="fas fa-triangle-exclamation" /></span>
        <strong>扫描没有完成</strong>
        <span>{{ scanError || "请稍后重新扫描" }}</span>
        <AppButton size="sm" variant="secondary" :disabled="scanning" @click="scan">重新扫描</AppButton>
      </div>

      <template #footer>
        <span class="cleanup-selected">
          已选 {{ selectedDisplayCount }} 类 · {{ selectedItems.length }} 项
          <template v-if="selectedDiskBytes > 0"> · {{ formatSize(selectedDiskBytes) }}</template>
          <template v-if="selectedMemoryBytes > 0"> · 内存 {{ formatSize(selectedMemoryBytes) }}</template>
        </span>
        <AppButton variant="primary" :disabled="cleaning || scanning || selectedItems.length === 0" @click="executeCleanup">
          {{ cleaning ? "清理中…" : "立即清理" }}
        </AppButton>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.cleanup-loading,
.cleanup-empty {
  min-height: 300px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
}

.cleanup-loading strong,
.cleanup-empty strong {
  color: var(--text);
  font-size: 16px;
}

.cleanup-spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border);
  border-top-color: var(--primary);
  border-radius: 50%;
  animation: cleanup-spin 0.8s linear infinite;
}

@keyframes cleanup-spin { to { transform: rotate(360deg); } }

.cleanup-hero {
  --cleanup-score-color: var(--success);
  display: grid;
  grid-template-columns: auto minmax(190px, 1fr) auto auto;
  align-items: center;
  gap: 18px;
  padding: 18px;
  border: 1px solid color-mix(in srgb, var(--cleanup-score-color) 24%, var(--border));
  border-radius: var(--radius-xl);
  background: radial-gradient(circle at 0 0, color-mix(in srgb, var(--cleanup-score-color) 12%, transparent), transparent 48%), linear-gradient(135deg, var(--surface), var(--surface-sunken));
}

.cleanup-hero.score-good { --cleanup-score-color: #3b82f6; }
.cleanup-hero.score-attention { --cleanup-score-color: var(--warning); }
.cleanup-hero.score-warning { --cleanup-score-color: var(--danger); }

.cleanup-score {
  width: 106px;
  height: 106px;
  padding: 7px;
  border-radius: 50%;
  background: conic-gradient(var(--cleanup-score-color) var(--cleanup-score-angle), var(--border-soft) 0);
}

.cleanup-score > div {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  border: 5px solid color-mix(in srgb, var(--cleanup-score-color) 7%, var(--surface));
  border-radius: 50%;
  background: var(--surface);
}

.cleanup-score strong {
  font-size: 31px;
  line-height: 1;
  letter-spacing: -1px;
  color: var(--cleanup-score-color);
}

.cleanup-score span,
.cleanup-hero__copy small,
.cleanup-hero__metrics span {
  font-size: 10px;
  color: var(--text-muted);
}

.cleanup-score span { margin-top: 4px; }
.cleanup-hero__copy { min-width: 0; }
.cleanup-hero__copy h3 { margin: 3px 0 5px; font-size: 19px; color: var(--text); }
.cleanup-hero__copy p { margin: 0; font-size: 12px; color: var(--text-muted); }

.cleanup-hero__metrics {
  display: grid;
  gap: 8px;
  min-width: 126px;
}

.cleanup-hero__metrics > div {
  display: flex;
  flex-direction: column;
  padding-left: 12px;
  border-left: 2px solid color-mix(in srgb, var(--cleanup-score-color) 28%, var(--border));
}

.cleanup-hero__metrics strong { margin-top: 2px; font-size: 13px; color: var(--text); }

.cleanup-group__icon,
.cleanup-item__icon {
  flex: 0 0 auto;
  display: grid;
  place-items: center;
  color: var(--primary);
  background: color-mix(in srgb, var(--primary) 9%, var(--surface));
}

.cleanup-result {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 10px;
  padding: 9px 12px;
  border-radius: var(--radius-md);
  color: var(--success);
  background: color-mix(in srgb, var(--success) 9%, var(--surface));
  font-size: 12px;
}

.cleanup-result--warn { color: var(--warning); background: color-mix(in srgb, var(--warning) 10%, var(--surface)); }
.cleanup-result-details { display: grid; gap: 5px; margin-top: 7px; padding: 0 3px; font-size: 11px; }
.cleanup-result-details > div { display: flex; gap: 8px; color: var(--text-muted); }
.cleanup-result-details strong { flex: 0 0 auto; color: var(--text-regular); }

.cleanup-groups {
  display: block;
  max-height: min(48vh, 440px);
  overflow-y: auto;
  margin-top: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.cleanup-group {
  border: 0;
  border-radius: 0;
  background: transparent;
}

.cleanup-group + .cleanup-group { border-top: 1px solid var(--border); }

.cleanup-group__head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 10px 12px;
  background: var(--panel-head-bg);
  border-bottom: 1px solid var(--border-soft);
}

.cleanup-group__icon { width: 28px; height: 28px; border-radius: 8px; font-size: 12px; }
.cleanup-group__title { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.cleanup-group__title strong { font-size: 13px; }
.cleanup-group__title small { margin-top: 1px; font-size: 10px; color: var(--text-muted); }
.cleanup-group__head > button { border: 0; padding: 4px 7px; border-radius: var(--radius-sm); font-size: 11px; color: var(--primary); background: transparent; }
.cleanup-group__head > button:hover { background: color-mix(in srgb, var(--primary) 8%, transparent); }
.cleanup-group__items { display: grid; }

.cleanup-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px 12px;
  border-top: 1px solid var(--border-soft);
}

.cleanup-item:first-child { border-top: 0; }
.cleanup-item:hover { background: color-mix(in srgb, var(--primary) 2%, var(--surface)); }
.cleanup-item > input { width: 16px; height: 16px; margin: 7px 0 0; flex: 0 0 auto; accent-color: var(--primary); }
.cleanup-item__icon { width: 30px; height: 30px; border-radius: 9px; font-size: 12px; }
.cleanup-item__main { min-width: 0; flex: 1; }
.cleanup-item__line { min-width: 0; display: flex; align-items: center; gap: 7px; }
.cleanup-item__line strong { font-size: 13px; color: var(--text); }

.cleanup-item__line em {
  flex: 0 0 auto;
  padding: 1px 6px;
  border-radius: var(--radius-pill);
  font-size: 9px;
  font-style: normal;
  color: var(--success);
  background: color-mix(in srgb, var(--success) 10%, var(--surface));
}

.cleanup-item__line em.risk-review,
.cleanup-item__line em.risk-locking { color: var(--warning); background: color-mix(in srgb, var(--warning) 11%, var(--surface)); }
.cleanup-item__line em.risk-rebuild { color: var(--info); background: color-mix(in srgb, var(--info) 10%, var(--surface)); }
.cleanup-item__count { flex: 0 0 auto; font-size: 10px; color: var(--text-muted); }
.cleanup-item__line b { margin-left: auto; flex: 0 0 auto; font-size: 11px; color: var(--text-regular); }
.cleanup-item__main p { margin: 3px 0 0; font-size: 11px; color: var(--text-muted); }

.cleanup-item__main > code,
.cleanup-item__paths code {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono, monospace);
  font-size: 10px;
  color: var(--text-faint, var(--text-muted));
}

.cleanup-item__main > code { margin-top: 4px; }
.cleanup-item__paths-toggle { border: 0; margin-top: 4px; padding: 0; font-size: 10px; color: var(--primary); background: transparent; }
.cleanup-item__paths-toggle i { margin-left: 3px; font-size: 8px; }
.cleanup-item__paths { display: grid; gap: 3px; max-height: 112px; overflow-y: auto; margin-top: 5px; padding: 7px 9px; border-radius: var(--radius-sm); background: var(--surface-sunken); }

.cleanup-empty__icon {
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  color: var(--success);
  background: color-mix(in srgb, var(--success) 10%, var(--surface));
  font-size: 19px;
}

.cleanup-empty__icon--warn { color: var(--warning); background: color-mix(in srgb, var(--warning) 10%, var(--surface)); }
.cleanup-selected { margin-right: auto; align-self: center; font-size: 12px; color: var(--text-muted); }

@media (max-width: 760px) {
  .cleanup-hero { grid-template-columns: auto 1fr; gap: 13px; padding: 14px; }
  .cleanup-score { width: 88px; height: 88px; }
  .cleanup-score strong { font-size: 26px; }
  .cleanup-hero__metrics { grid-column: 1 / -1; grid-template-columns: repeat(2, 1fr); }
  .cleanup-hero > :deep(.btn) { grid-column: 1 / -1; }
  .cleanup-group__title small,
  .cleanup-item__main > code { display: none; }
  .cleanup-item__line { flex-wrap: wrap; }
  .cleanup-item__line b { width: 100%; margin-left: 0; }
  .cleanup-selected { display: none; }
}
</style>
