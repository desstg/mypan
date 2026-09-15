<script lang="ts">
import { ref } from "vue";
import type { LogEntry, LogStats } from "@/api/logs";
import "@/styles/system-logs.css";

  // 会话缓存保留日志和筛选，重新进入时先显示旧结果再后台刷新。
const logs = ref<LogEntry[]>([]);
const stats = ref<LogStats | null>(null);
const level = ref<string | number>("");
const module = ref("");
const period = ref("all");
const keyword = ref("");
const page = ref(1);
const hasMore = ref(false);
</script>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  LOG_LEVELS,
  LOG_MODULE_GROUPS,
  LOG_PERIODS,
  logsApi,
} from "@/api/logs";
import AppBadge from "@/components/base/AppBadge.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import StatCard from "@/components/base/StatCard.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import { formatTime } from "@/utils/format";

const props = withDefaults(
  defineProps<{
    presetLevel?: string | number;
    presetSeq?: number;
  }>(),
  {
    presetLevel: "",
    presetSeq: 0,
  },
);
const emit = defineEmits<{
  ackedErrors: [stats: LogStats];
}>();

const api = logsApi();

const loading = ref(false);
const cleaningKeepToday = ref(false);
const cleaningAll = ref(false);
const acknowledgingRecentErrors = ref(false);
const expanded = ref<Set<number>>(new Set());
const logsPanelRef = ref<HTMLElement | null>(null);

const PAGE_SIZE = 50;

const activeModuleCount = computed(() =>
  stats.value?.by_module ? Object.keys(stats.value.by_module).length : 0,
);
const recentErrorCount = computed(() => stats.value?.recent_errors ?? 0);
const recentUnacknowledgedErrorCount = computed(() => stats.value?.recent_unacknowledged_errors ?? 0);
const canAcknowledgeRecentErrors = computed(() => recentUnacknowledgedErrorCount.value > 0);

let searchTimer: ReturnType<typeof setTimeout> | undefined;
let loadSequence = 0;

function levelClass(lv: number): string {
  if (lv >= 40) return "error";
  if (lv >= 30) return "warning";
  if (lv >= 20) return "info";
  return "debug";
}

function levelTone(lv: number): "neutral" | "info" | "warning" | "danger" {
  if (lv >= 40) return "danger";
  if (lv >= 30) return "warning";
  if (lv >= 20) return "info";
  return "neutral";
}

function periodRange(): { start_time?: string } {
  const now = new Date();
  if (period.value === "today") {
    const start = new Date(now);
    start.setHours(0, 0, 0, 0);
    return { start_time: start.toISOString() };
  }
  if (period.value === "24h") {
    return { start_time: new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString() };
  }
  if (period.value === "7d") {
    return { start_time: new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString() };
  }
  return {};
}

function buildQuery() {
  const q: Record<string, string | number | undefined> = {
    ...periodRange(),
    // 多取一条仅用于判断是否还有下一页，页面仍只渲染 50 条。
    limit: PAGE_SIZE + 1,
    offset: (page.value - 1) * PAGE_SIZE,
  };
  if (level.value !== "") q.level = Number(level.value);
  if (module.value) q.module = module.value;
  const kw = keyword.value.trim();
  if (kw) q.keyword = kw;
  return q;
}

async function loadLogs(): Promise<boolean> {
  const sequence = ++loadSequence;
  // 仅在当前无内容时显示整块加载占位；有缓存时后台静默刷新，列表不空屏。
  loading.value = true;
  try {
    const result = await api.list(buildQuery());
    if (sequence !== loadSequence) return false;
    hasMore.value = result.length > PAGE_SIZE;
    logs.value = result.slice(0, PAGE_SIZE);
    return true;
  } catch (e) {
    if (sequence === loadSequence) {
      toast.error(getApiErrorMessage(e, "加载日志失败"));
    }
    return false;
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
}

async function loadStats() {
  try {
    stats.value = await api.stats();
  } catch {
    /* 统计失败不阻断列表 */
  }
}

async function refreshAll() {
  resetPage();
  await Promise.all([loadStats(), loadLogs()]);
}

async function loadInitialPage() {
  const returningFromOlderPage = page.value > 1;
  resetPage();
  if (returningFromOlderPage) logs.value = [];
  await loadLogs();
  // 先返回首屏日志；全量统计使用缓存并在列表之后刷新，不阻塞日志展示。
  void loadStats();
}

async function cleanupKeepToday() {
  try {
    await confirm({
      title: "清理今天之外的日志？",
      message: "将保留今天的日志，删除今天之前的全部旧日志文件。",
      confirmText: "清理",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  cleaningKeepToday.value = true;
  try {
    const result = await api.cleanupKeepToday();
    toast.success(
      result.deleted_files > 0 ? `已清理 ${result.deleted_files} 个今天之外的旧日志文件` : "无需清理，当前只有今天的日志",
    );
    await refreshAll();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清理日志失败"));
  } finally {
    cleaningKeepToday.value = false;
  }
}

async function cleanupAllLogs() {
  try {
    await confirm({
      title: "清理全部日志？",
      message: "将删除全部日志文件，包括今天的日志。后续新日志会重新生成。",
      confirmText: "全部清理",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  cleaningAll.value = true;
  try {
    const result = await api.cleanupAll();
    toast.success(result.deleted_files > 0 ? `已清理 ${result.deleted_files} 个日志文件` : "当前没有可清理的日志文件");
    await refreshAll();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清理全部日志失败"));
  } finally {
    cleaningAll.value = false;
  }
}

async function ackRecentErrors() {
  if (!canAcknowledgeRecentErrors.value) return;
  acknowledgingRecentErrors.value = true;
  try {
    const next = await api.ackRecentErrors();
    stats.value = next;
    emit("ackedErrors", next);
    toast.success("已知晓当前错误，后续新错误会再次提醒");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "确认当前错误失败"));
  } finally {
    acknowledgingRecentErrors.value = false;
  }
}

function onFilterChange() {
  resetPage();
  void loadLogs();
}

function onKeywordInput() {
  clearTimeout(searchTimer);
  resetPage();
  searchTimer = setTimeout(() => void loadLogs(), 350);
}

function resetFilters() {
  level.value = "";
  module.value = "";
  period.value = "all";
  keyword.value = "";
  void refreshAll();
}

function resetPage() {
  page.value = 1;
  hasMore.value = false;
  expanded.value = new Set();
}

function applyPresetFilter(triggerRefresh: boolean) {
  if (props.presetLevel === "" || props.presetLevel === undefined || props.presetLevel === null) return;
  level.value = props.presetLevel;
  module.value = "";
  period.value = "24h";
  keyword.value = "";
  if (triggerRefresh) void refreshAll();
}

async function changePage(target: number) {
  if (loading.value || target < 1 || (target > page.value && !hasMore.value)) return;
  const previous = page.value;
  page.value = target;
  expanded.value = new Set();
  if (!(await loadLogs())) {
    page.value = previous;
    return;
  }
  logsPanelRef.value?.scrollIntoView({ behavior: "smooth", block: "start" });
}

function toggleDetails(id: number) {
  const next = new Set(expanded.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expanded.value = next;
}

function canShowDetails(log: LogEntry): boolean {
  return log.level >= 40 && !!log.details && Object.keys(log.details).length > 0;
}

function detailsText(log: LogEntry): string {
  return formatDetails(log.details ?? {});
}

function formatDetails(details: Record<string, unknown>) {
  return JSON.stringify(details, null, 2);
}

watch(
  () => props.presetSeq,
  (next, prev) => {
    if (next > 0 && next !== prev) applyPresetFilter(true);
  },
);

onMounted(() => {
  if (props.presetSeq > 0) applyPresetFilter(false);
  void loadInitialPage();
});
onUnmounted(() => clearTimeout(searchTimer));
</script>

<template>
  <div class="logs-page">
    <div v-if="stats" class="logs-stats">
      <StatCard icon="fa-file-alt" :value="stats.total" label="总日志数" tone="blue" />
      <StatCard icon="fa-exclamation-triangle" :value="recentErrorCount" label="近 24 小时错误" tone="red" />
      <StatCard icon="fa-cubes" :value="activeModuleCount" label="活跃模块" tone="purple" />
      <StatCard icon="fa-list" :value="logs.length" label="本页结果数" tone="amber" />
    </div>

    <div v-if="canAcknowledgeRecentErrors" class="logs-ack-banner">
      <div class="logs-ack-banner__copy">
        <strong>当前有 {{ recentUnacknowledgedErrorCount }} 条未确认错误</strong>
        <span>确认后“运行概况”会恢复正常，后续新错误仍会再次提醒。</span>
      </div>
      <button
        type="button"
        class="logs-ack-banner__action"
        :disabled="acknowledgingRecentErrors"
        @click="ackRecentErrors"
      >
        {{ acknowledgingRecentErrors ? "处理中..." : "已知晓当前错误" }}
      </button>
    </div>

    <div class="logs-toolbar">
      <div class="logs-filters">
        <div class="logs-filter">
          <label for="log-level">级别</label>
          <AppSelect
            id="log-level"
            v-model="level"
            :options="LOG_LEVELS.map((o) => ({ value: o.value, label: o.label }))"
            @update:model-value="onFilterChange"
          />
        </div>
        <div class="logs-filter">
          <label for="log-module">模块</label>
          <AppSelect
            id="log-module"
            v-model="module"
            :options="LOG_MODULE_GROUPS.map((o) => ({ value: o.value, label: o.label }))"
            @update:model-value="onFilterChange"
          />
        </div>
        <div class="logs-filter">
          <label for="log-period">时间范围</label>
          <AppSelect
            id="log-period"
            v-model="period"
            :options="LOG_PERIODS.map((o) => ({ value: o.value, label: o.label }))"
            @update:model-value="onFilterChange"
          />
        </div>
        <div class="logs-filter">
          <label for="log-keyword">关键词</label>
          <AppInput
            id="log-keyword"
            v-model="keyword"
            placeholder="搜索消息内容…"
            @update:model-value="onKeywordInput"
          />
        </div>
      </div>
      <div class="logs-actions">
        <button
          type="button"
          class="logs-action-btn logs-action-btn--primary"
          :disabled="loading"
          title="刷新"
          aria-label="刷新"
          @click="refreshAll"
        >
          <span class="logs-action-btn__icon">
            <SvgIcon :name="'fa-sync-alt'" :size="18" :class-name="loading ? 'logs-action-btn__icon-spin' : ''" />
          </span>
        </button>
        <button
          type="button"
          class="logs-action-btn"
          title="重置"
          aria-label="重置"
          @click="resetFilters"
        >
          <span class="logs-action-btn__icon"><SvgIcon name="fa-undo-alt" :size="18" /></span>
        </button>
        <button
          type="button"
          class="logs-action-btn logs-action-btn--warning"
          :disabled="cleaningKeepToday"
          title="清理今天之外的"
          aria-label="清理今天之外的"
          @click="cleanupKeepToday"
        >
          <span class="logs-action-btn__icon">
            <SvgIcon
              :name="cleaningKeepToday ? 'fa-sync-alt' : 'fa-eraser'"
              :size="18"
              :class-name="cleaningKeepToday ? 'logs-action-btn__icon-spin' : ''"
            />
          </span>
        </button>
        <button
          type="button"
          class="logs-action-btn logs-action-btn--danger"
          :disabled="cleaningAll"
          title="清理所有"
          aria-label="清理所有"
          @click="cleanupAllLogs"
        >
          <span class="logs-action-btn__icon">
            <SvgIcon
              :name="cleaningAll ? 'fa-sync-alt' : 'fa-trash-alt'"
              :size="18"
              :class-name="cleaningAll ? 'logs-action-btn__icon-spin' : ''"
            />
          </span>
        </button>
      </div>
    </div>

    <div ref="logsPanelRef" class="logs-panel">
      <AppStateBlock v-if="loading && logs.length === 0" message="正在加载日志…" loading min-height="360px" />
      <AppStateBlock v-else-if="logs.length === 0" message="当前筛选条件下暂无日志" min-height="360px" />
      <template v-else>
        <div class="logs-list" :class="{ 'logs-list--loading': loading }">
          <article
            v-for="log in logs"
            :key="log.id"
            class="log-card"
            :class="`log-card--${levelClass(log.level)}`"
          >
            <header class="log-card__head">
              <div class="log-card__tags">
                <AppBadge :tone="levelTone(log.level)">
                  {{ log.level_emoji }} {{ log.level_name }}
                </AppBadge>
                <span
                  class="log-badge log-badge--module"
                  :style="{ '--module-color': log.module_color }"
                >
                  {{ log.module_name }}
                </span>
              </div>
              <time class="log-card__time">{{ formatTime(log.timestamp) }}</time>
            </header>

            <div class="log-card__message">{{ log.message }}</div>

            <div v-if="log.driver_name || log.account_id" class="log-card__meta">
              <span v-if="log.driver_name" class="log-meta-chip">驱动 {{ log.driver_name }}</span>
              <span v-if="log.account_id" class="log-meta-chip">账号 {{ log.account_id }}</span>
            </div>

            <div v-if="canShowDetails(log)" class="log-card__details">
              <button type="button" class="log-card__details-toggle" @click="toggleDetails(log.id)">
                <span>{{ expanded.has(log.id) ? "收起详细信息" : "查看详细信息" }}</span>
                <span>{{ expanded.has(log.id) ? "▲" : "▼" }}</span>
              </button>
              <pre v-if="expanded.has(log.id)" class="log-card__details-pre">{{ detailsText(log) }}</pre>
            </div>
          </article>
        </div>
        <nav v-if="page > 1 || hasMore" class="logs-pagination" aria-label="日志分页">
          <button
            type="button"
            class="logs-pagination__button"
            :disabled="page <= 1 || loading"
            @click="changePage(page - 1)"
          >
            上一页
          </button>
          <span class="logs-pagination__current" aria-live="polite">第 {{ page }} 页</span>
          <button
            type="button"
            class="logs-pagination__button"
            :disabled="!hasMore || loading"
            @click="changePage(page + 1)"
          >
            下一页
          </button>
        </nav>
      </template>
    </div>
  </div>
</template>

<style scoped>
.logs-ack-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
  border: 1px solid color-mix(in srgb, var(--warning) 24%, var(--border));
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--warning) 8%, var(--surface));
}

.logs-ack-banner__copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.logs-ack-banner__copy strong {
  color: var(--text);
  font-size: 13px;
}

.logs-ack-banner__copy span {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}

.logs-ack-banner__action {
  height: 34px;
  flex: 0 0 auto;
  padding: 0 14px;
  border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}

.logs-ack-banner__action:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

@media (max-width: 760px) {
  .logs-ack-banner {
    flex-direction: column;
    align-items: stretch;
  }

  .logs-ack-banner__action {
    width: 100%;
  }
}
</style>
