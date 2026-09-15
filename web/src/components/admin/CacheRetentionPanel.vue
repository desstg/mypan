<script setup lang="ts">
import {
  computed,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watchEffect,
} from "vue";
import { storeToRefs } from "pinia";
import { getApiErrorMessage } from "@/api/client";
import {
  SCAN_DEPTH_OPTIONS,
  createCacheRetentionTask,
  deleteCacheRetentionTask,
  fetchCacheRetentionDefaults,
  fetchCacheRetentionStats,
  fetchCacheRetentionTasks,
  forceStopCacheRetentionTask,
  refreshCacheRetentionTask,
  toggleCacheRetentionTask,
  updateCacheRetentionTask,
  type CacheRetentionTask,
  type CacheRetentionTaskInput,
  type RetentionRunResult,
} from "@/api/cacheRetention";
import FormField from "@/components/base/FormField.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppIconButton from "@/components/base/AppIconButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import StatCard from "@/components/base/StatCard.vue";
import TimeWindowField from "@/components/base/TimeWindowField.vue";
import AdminStatsGrid from "@/components/admin/AdminStatsGrid.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminEnableToggle from "@/components/admin/AdminEnableToggle.vue";
import AdminRunStatusCell from "@/components/admin/AdminRunStatusCell.vue";
import AdminStartupBanner from "@/components/admin/AdminStartupBanner.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import AdminTableActionBtn from "@/components/admin/AdminTableActionBtn.vue";
import AdminRowActions from "@/components/admin/AdminRowActions.vue";
import type { AdminRunStatusVariant } from "@/components/admin/adminRunStatus";
import { normalizeRunStatusVariant } from "@/components/admin/adminRunStatus";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import { useAccountPathLabel } from "@/composables/useAccountPathLabel";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useConditionalPolling } from "@/composables/useConditionalPolling";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import { liveElapsedMs, useLiveElapsedClock } from "@/composables/useLiveElapsedClock";
import {
  applyTimeWindowFromTask,
  timeWindowPayload,
  useTimeWindowSchedule,
} from "@/composables/useTimeWindowSchedule";
import { confirm } from "@/composables/useConfirm";
import { useStartupCountdown } from "@/composables/useStartupCountdown";
import { toast } from "@/composables/useToast";
import { useAccountsStore } from "@/stores/accounts";
import { formatCompactDuration, formatElapsedMs, formatRelativeTimeAgo } from "@/utils/format";
import "@/styles/admin-table.css";

const MAX_CONFIGS = 6;

withDefaults(
  defineProps<{
    hideStats?: boolean;
  }>(),
  { hideStats: false },
);

const accountsStore = useAccountsStore();
const { accounts } = storeToRefs(accountsStore);

const tasks = ref<CacheRetentionTask[]>([]);
const retentionTaskList = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();
const refreshing = ref(false);
const listReady = ref(false);
useAdminPageLoading(
  "tasks",
  computed(() => (!listReady.value || refreshing.value) && !tasks.value.length),
);
const { remainingDisplay: startupRemainingDisplay, applyStartupRemaining } = useStartupCountdown();
const executingIds = ref<number[]>([]);
const pendingIds = ref<number[]>([]);

const statsTotal = computed(() => tasks.value.length);
const statsRunning = computed(() => tasks.value.filter((t) => t.status === "running").length);
const statsPaused = computed(() => tasks.value.filter((t) => t.status !== "running").length);

const activeAccounts = computed(() => accounts.value.filter((a) => a.is_active));

const dialogOpen = ref(false);
const editingId = ref<number | null>(null);
const submitting = ref(false);
const pickerOpen = ref(false);

const defaults = reactive({
  api_interval: 200,
  refresh_interval: 60,
  scan_depth: 4,
});

type TaskForm = CacheRetentionTaskInput & {
  time_window_mode: "always" | "custom";
};

const emptyForm = (): TaskForm => ({
  account_id: 0,
  parent_id: "0",
  path: "",
  scan_depth: defaults.scan_depth,
  api_interval: defaults.api_interval,
  refresh_interval: defaults.refresh_interval,
  time_window_enabled: false,
  time_start: "00:00",
  time_end: "23:59",
  time_window_mode: "always",
});

const form = reactive(emptyForm());

const scanDepthOptions = SCAN_DEPTH_OPTIONS.map((o) => ({ value: o.value, label: o.label }));

const { display: sourceDirDisplay, title: sourceDirTitle } = useAccountPathLabel({
  accountId: computed(() => form.account_id),
  path: computed(() => form.path),
  accounts,
});

const { timeWindowDisplay, onTimeWheelConfirm } = useTimeWindowSchedule(form);

function isRetentionBusy() {
  return (
    executingIds.value.length > 0 ||
    pendingIds.value.length > 0 ||
    tasks.value.some((t) => t.is_running || t.is_pending)
  );
}

const listPolling = useConditionalPolling({
  intervalMs: 2500,
  onTick: () => refreshAll(true),
  shouldPoll: isRetentionBusy,
});

const elapsedClock = useLiveElapsedClock();

watchEffect(() => {
  elapsedClock.sync(isRetentionBusy());
});

function isTaskEnabled(task: CacheRetentionTask): boolean {
  return task.status === "running";
}

function isTaskRunning(task: CacheRetentionTask): boolean {
  if (!task.id) return false;
  return Boolean(task.is_running) || executingIds.value.includes(task.id);
}

function isTaskQueued(task: CacheRetentionTask): boolean {
  if (!task.id || isTaskRunning(task)) return false;
  return Boolean(task.is_pending) || pendingIds.value.includes(task.id);
}

function scanDepthLabel(depth: number): string {
  return SCAN_DEPTH_OPTIONS.find((o) => o.value === depth)?.label ?? `${depth} 层`;
}

function formatInterval(minutes: number): string {
  if (minutes < 60) return `${minutes} 分钟`;
  if (minutes < 1440) {
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    return rest ? `${hours} 小时 ${rest} 分钟` : `${hours} 小时`;
  }
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  return hours ? `${days} 天 ${hours} 小时` : `${days} 天`;
}

function formatFileCount(count: number): string {
  if (!count) return "-";
  return count.toLocaleString();
}

function taskMeta(task: CacheRetentionTask): string {
  const fileText = task.last_refresh ? `${formatFileCount(task.file_count)} 个文件` : "待统计";
  return [
    task.account_name || "未知账号",
    formatInterval(task.refresh_interval || 0),
    scanDepthLabel(task.scan_depth),
    fileText,
  ].join(" · ");
}

function refreshStatusClass(task: CacheRetentionTask): string {
  if (isTaskRunning(task) || isTaskQueued(task)) return "executing";
  return task.last_refresh_status || "pending";
}

function formatRetryAfter(seconds?: number): string {
  if (!seconds || seconds <= 0) return "稍后";
  if (seconds < 60) return `${seconds} 秒`;
  const min = Math.floor(seconds / 60);
  const sec = seconds % 60;
  if (sec === 0) return `${min} 分钟`;
  return `${min} 分 ${sec} 秒`;
}

function refreshStatusVariant(task: CacheRetentionTask): AdminRunStatusVariant {
  return normalizeRunStatusVariant(refreshStatusClass(task));
}

function refreshPrimaryText(task: CacheRetentionTask): string {
  if (isTaskRunning(task)) {
    const dir = task.current_dir?.trim();
    if (dir) return `正在扫描「${dir}」`;
    return "正在扫描…";
  }
  if (isTaskQueued(task)) {
    if (startupRemainingDisplay.value > 0) {
      return `排队中 — 启动退避 ${startupRemainingDisplay.value}s`;
    }
    return "排队中 — 等待执行";
  }
  return formatRelativeTimeAgo(task.last_refresh, "从未刷新");
}

function refreshSummary(task: CacheRetentionTask): string {
  if (isTaskRunning(task)) {
    const dirs = Number(task.scanned_dirs || 0);
    const files = Number(task.scanned_files || 0);
    const parts: string[] = [];
    if (dirs > 0 || files > 0) {
      parts.push(`已扫 ${dirs} 目录 / ${formatFileCount(files)} 文件`);
    }
    void elapsedClock.tick.value;
    const fromStart = liveElapsedMs(task.started_at, elapsedClock.tick.value);
    const elapsed = formatElapsedMs(fromStart || task.current_duration_ms);
    if (elapsed) parts.push(elapsed);
    return parts.length ? parts.join(" · ") : "正在刷新…";
  }
  if (isTaskQueued(task)) return "即将开始扫描…";
  if (!task.last_refresh) return "待执行";
  const parts = [formatFileCount(task.file_count) + " 个文件"];
  if (task.last_duration_ms > 0) parts.push(formatCompactDuration(task.last_duration_ms));
  parts.push(formatInterval(task.refresh_interval || 0));
  return parts.join(" · ");
}

function refreshTitle(task: CacheRetentionTask): string {
  return [refreshPrimaryText(task), refreshSummary(task), taskMeta(task)].join("\n");
}

function showRetentionRunToast(result?: RetentionRunResult) {
  switch (result?.state) {
    case "queued_startup":
      toast.info(`已加入执行队列，启动退避结束后（约 ${result.startup_remaining} 秒）自动执行`);
      break;
    case "already_running":
      toast.info("任务已在执行中");
      break;
    case "blocked_by_strm":
      toast.info("同账号有其他任务正在占用（STRM 或媒体整理），已加入队列，占用结束后自动执行");
      break;
    case "cache_disabled":
      toast.warning("该账号目录缓存已关闭（TTL=0），缓存保持任务无法生效");
      break;
    case "too_soon":
      toast.info(
        result.cache_ttl_minutes && result.retry_after_seconds
          ? `账号缓存 ${result.cache_ttl_minutes} 分钟，约 ${formatRetryAfter(result.retry_after_seconds)} 后可再试`
          : "缓存冷却中，请稍后再试",
      );
      break;
    default:
      toast.success("已触发执行");
  }
}

function showRetentionCreateToast(result?: RetentionRunResult) {
  switch (result?.state) {
    case "queued_startup":
      toast.success(`配置已创建，启动退避结束后（约 ${result.startup_remaining} 秒）自动执行`);
      break;
    case "blocked_by_strm":
      toast.success("配置已创建，同账号任务占用结束后自动执行");
      break;
    case "running":
      toast.success("配置已创建，已触发执行");
      break;
    default:
      toast.success("配置已创建");
  }
}

async function loadDefaults() {
  try {
    const data = await fetchCacheRetentionDefaults();
    defaults.api_interval = data.api_interval || 200;
    defaults.refresh_interval = data.refresh_interval || 60;
    defaults.scan_depth = data.scan_depth || 4;
    applyStartupRemaining(data.startup_remaining ?? 0);
  } catch {
    /* 使用本地兜底 */
  }
}

async function loadTasks(silent = false) {
  if (!silent) refreshing.value = true;
  try {
    const data = await fetchCacheRetentionTasks();
    tasks.value = data.items ?? [];
    applyStartupRemaining(data.startup_remaining ?? 0);
  } catch (e) {
    if (!silent) toast.error(getApiErrorMessage(e, "加载缓存保持任务失败"));
  } finally {
    if (!silent) refreshing.value = false;
    listReady.value = true;
  }
}

async function loadStats() {
  try {
    const data = await fetchCacheRetentionStats();
    executingIds.value = data.executing_task_ids ?? [];
    pendingIds.value = data.pending_task_ids ?? [];
    applyStartupRemaining(data.startup_remaining ?? 0);
  } catch {
    /* 统计失败不阻断列表 */
  }
}

async function refreshAll(silent = false) {
  await Promise.all([loadTasks(silent), loadStats(), accountsStore.loadAccounts()]);
}

function syncPolling() {
  listPolling.sync();
}

function resetForm() {
  Object.assign(form, emptyForm());
}

function openCreate() {
  if (tasks.value.length >= MAX_CONFIGS) {
    toast.warning(`最多只能添加 ${MAX_CONFIGS} 个配置`);
    return;
  }
  editingId.value = null;
  resetForm();
  dialogOpen.value = true;
}

function openEdit(task: CacheRetentionTask) {
  editingId.value = task.id ?? null;
  form.account_id = task.account_id;
  form.parent_id = task.parent_id;
  form.path = task.path;
  form.scan_depth = task.scan_depth || defaults.scan_depth;
  form.api_interval = task.api_interval ?? defaults.api_interval;
  form.refresh_interval = task.refresh_interval || defaults.refresh_interval;
  applyTimeWindowFromTask(form, task);
  dialogOpen.value = true;
}

function onFolderPicked(payload: { accountId: number; parentId: string; path: string }) {
  form.account_id = payload.accountId;
  form.parent_id = payload.parentId;
  form.path = payload.path || "/";
  pickerOpen.value = false;
}

function buildPayload(): CacheRetentionTaskInput {
  return {
    account_id: form.account_id,
    parent_id: form.parent_id,
    path: form.path,
    scan_depth: Number(form.scan_depth) || defaults.scan_depth,
    api_interval: Number(form.api_interval) || 0,
    refresh_interval: Number(form.refresh_interval) || defaults.refresh_interval,
    ...timeWindowPayload(form),
  };
}

async function submitTask() {
  if (!form.account_id) {
    toast.error("请选择账号及目录");
    return;
  }
  if (!form.path.trim()) {
    toast.error("请选择目录");
    return;
  }
  const apiInterval = Number(form.api_interval);
  if (apiInterval < 0 || apiInterval > 5000) {
    toast.error("API 额外补偿间隔必须在 0-5000 毫秒之间");
    return;
  }
  const refreshInterval = Number(form.refresh_interval);
  if (!refreshInterval || refreshInterval < 1 || refreshInterval > 1440) {
    toast.error("刷新间隔必须在 1-1440 分钟之间");
    return;
  }
  submitting.value = true;
  try {
    const body = buildPayload();
    if (editingId.value) {
      await updateCacheRetentionTask(editingId.value, body);
      toast.success("配置已更新");
    } else {
      const created = await createCacheRetentionTask(body);
      showRetentionCreateToast(created.run);
    }
    dialogOpen.value = false;
    await refreshAll();
    syncPolling();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存配置失败"));
  } finally {
    submitting.value = false;
  }
}

async function setTaskEnabled(task: CacheRetentionTask, enabled: boolean) {
  if (!task.id || enabled === isTaskEnabled(task)) return;
  try {
    const updated = await toggleCacheRetentionTask(task.id);
    const idx = tasks.value.findIndex((t) => t.id === task.id);
    if (idx >= 0) tasks.value[idx] = updated;
    toast.success(updated.status === "running" ? "已启用" : "已禁用");
    await loadStats();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "切换状态失败"));
  }
}

async function handleRun(task: CacheRetentionTask) {
  if (!task.id) return;
  try {
    const result = await refreshCacheRetentionTask(task.id);
    showRetentionRunToast(result);
    if (result?.state !== "too_soon" && result?.state !== "cache_disabled") {
      listPolling.start();
    }
    await refreshAll(true);
    syncPolling();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "执行失败"));
  }
}

async function handleForceStop(task: CacheRetentionTask) {
  if (!task.id) return;
  try {
    await forceStopCacheRetentionTask(task.id);
    toast.success("任务已强制停止，下次调度不受影响");
    await refreshAll(true);
    syncPolling();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "强制停止失败"));
  }
}

async function handleDelete(task: CacheRetentionTask) {
  if (!task.id) return;
  const taskID = task.id;
  try {
    await confirm({
      title: "确认删除",
      message: "确定要删除这个缓存保持配置吗？\n\n删除后该配置将停止缓存刷新，但已缓存的目录数据会保留。",
      confirmText: "删除",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await removeWithDust({
      target: findDustTarget(retentionTaskList.value, `retention-task-${taskID}`),
      container: retentionTaskList.value,
      remove: async () => {
        await deleteCacheRetentionTask(taskID);
        tasks.value = tasks.value.filter((item) => item.id !== taskID);
        executingIds.value = executingIds.value.filter((id) => id !== taskID);
        pendingIds.value = pendingIds.value.filter((id) => id !== taskID);
      },
    });
    toast.success("配置已删除");
    syncPolling();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "删除失败"));
  }
}

onMounted(async () => {
  await loadDefaults();
  await refreshAll();
  syncPolling();
});

let activatedOnce = false;

onActivated(() => {
  if (activatedOnce) void refreshAll(true);
  activatedOnce = true;
  syncPolling();
});

function stopPageActivity() {
  listPolling.stop();
}

onDeactivated(stopPageActivity);
onUnmounted(stopPageActivity);

defineExpose({
  openCreate,
});
</script>

<template>
  <div class="retention-panel">
    <AdminStartupBanner :seconds="startupRemainingDisplay" />

    <div v-if="tasks.length >= MAX_CONFIGS" class="retention-limit-banner">
      已达到最大配置数量（{{ MAX_CONFIGS }} 个）。如需覆盖更多目录，请提高扫描层级或删除不需要的配置。
    </div>

    <AdminStatsGrid v-if="!hideStats">
      <StatCard icon="fa-folder" :value="statsTotal" label="配置目录" tone="blue" />
      <StatCard icon="fa-play" :value="statsRunning" label="已启用" tone="purple" />
      <StatCard icon="fa-pause" :value="statsPaused" label="已暂停" tone="amber">
        <template #actions>
          <AppIconButton
            icon="fa-sync-alt"
            label="刷新"
            variant="secondary"
            size="xs"
            :disabled="refreshing"
            title="刷新任务列表"
            @click="() => refreshAll()"
          />
        </template>
      </StatCard>
    </AdminStatsGrid>

    <AdminEmptyState
      v-if="listReady && !refreshing && !tasks.length"
      icon="🔥"
      title="还没有缓存任务"
      description="添加目录后，系统会定期预热列表缓存，减少浏览时的 API 请求。"
    >
      <AppButton type="button" variant="primary" @click="openCreate">添加第一个任务</AppButton>
    </AdminEmptyState>

    <template v-else-if="tasks.length">
      <div class="admin-panel-table-wrap retention-table-wrap">
        <table class="admin-table retention-table">
          <colgroup>
            <col class="retention-col-info" />
            <col class="retention-col-refresh" />
            <col class="retention-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th>目录信息</th>
              <th>最后刷新</th>
              <th class="retention-table__actions">操作</th>
            </tr>
          </thead>
          <tbody ref="retentionTaskList">
            <tr v-for="task in tasks" :key="task.id" class="retention-row" :data-dust-key="`retention-task-${task.id}`">
              <td>
                <div class="retention-main" :title="taskMeta(task)">
                  <div class="retention-name">
                    <span class="retention-path">{{ task.path }}</span>
                    <AdminStatusPill :tone="isTaskEnabled(task) ? 'success' : 'warning'">
                      {{ isTaskEnabled(task) ? "已启用" : "已禁用" }}
                    </AdminStatusPill>
                  </div>
                  <div class="retention-meta">{{ taskMeta(task) }}</div>
                </div>
              </td>
              <td>
                <AdminRunStatusCell
                  :title="refreshTitle(task)"
                  :primary="refreshPrimaryText(task)"
                  :summary="refreshSummary(task)"
                  :variant="refreshStatusVariant(task)"
                  :live="isTaskRunning(task) || isTaskQueued(task)"
                  primary-tone="default"
                />
              </td>
              <td class="admin-table__actions retention-table__action-cell">
                <AdminRowActions>
                  <div class="retention-actions">
                    <AdminEnableToggle
                      :enabled="isTaskEnabled(task)"
                      aria-label="缓存保持启用切换"
                      @enable="setTaskEnabled(task, $event)"
                    />
                    <AdminTableActionBtn
                      v-if="isTaskRunning(task)"
                      icon="stop"
                      title="强制停止"
                      danger
                      @click="handleForceStop(task)"
                    />
                    <AdminTableActionBtn
                      v-else
                      icon="play"
                      title="立即执行"
                      @click="handleRun(task)"
                    />
                    <AdminTableActionBtn icon="edit" title="修改" @click="openEdit(task)" />
                    <AdminTableActionBtn icon="delete" title="删除" danger @click="handleDelete(task)" />
                  </div>
                  <template #menu>
                    <button
                      type="button"
                      class="admin-row-actions__item"
                      @click="setTaskEnabled(task, !isTaskEnabled(task))"
                    >
                      {{ isTaskEnabled(task) ? "禁用任务" : "启用任务" }}
                    </button>
                    <button
                      v-if="isTaskRunning(task)"
                      type="button"
                      class="admin-row-actions__item admin-row-actions__item--danger"
                      @click="handleForceStop(task)"
                    >
                      强制停止
                    </button>
                    <button v-else type="button" class="admin-row-actions__item" @click="handleRun(task)">
                      立即执行
                    </button>
                    <button type="button" class="admin-row-actions__item" @click="openEdit(task)">修改</button>
                    <button
                      type="button"
                      class="admin-row-actions__item admin-row-actions__item--danger"
                      @click="handleDelete(task)"
                    >
                      删除
                    </button>
                  </template>
                </AdminRowActions>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

    </template>

    <AppModal
      :open="dialogOpen"
      size="account"
      :title="editingId ? '修改目录' : '添加目录'"
      @close="dialogOpen = false"
    >
      <div class="retention-form">
        <FormField label="选择账号及目录">
          <AccountFolderField
            :display="sourceDirDisplay"
            :title="sourceDirTitle"
            @browse="pickerOpen = true"
          />
        </FormField>

        <div class="retention-form__row">
          <FormField label="扫描层级">
            <AppSelect v-model="form.scan_depth" :options="scanDepthOptions" />
          </FormField>
          <FormField label="API 额外补偿间隔（毫秒）">
            <AppInput v-model="form.api_interval" type="number" min="0" max="5000" />
          </FormField>
        </div>

        <div class="retention-form__row">
          <FormField label="刷新间隔（分钟）">
            <AppInput v-model="form.refresh_interval" type="number" min="1" max="1440" />
          </FormField>
          <FormField label="执行时间段">
            <TimeWindowField
              :display="timeWindowDisplay"
              :start-time="form.time_start"
              :end-time="form.time_end"
              :all-day="form.time_window_mode === 'always'"
              @confirm="onTimeWheelConfirm"
            />
          </FormField>
        </div>
      </div>

      <template #footer>
        <AppButton type="button" variant="primary" :disabled="submitting" @click="submitTask">
          {{ submitting ? "保存中…" : editingId ? "更新配置" : "保存配置" }}
        </AppButton>
      </template>
    </AppModal>

    <FolderPickerModal
      :open="pickerOpen"
      selectable-account
      :accounts="activeAccounts"
      :account-id="form.account_id || null"
      :initial-path="form.path"
      @close="pickerOpen = false"
      @resolve="onFolderPicked"
    />
  </div>
</template>

<style scoped>
.retention-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.retention-limit-banner {
  padding: 12px 16px;
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.5;
  background: color-mix(in srgb, #f59e0b 10%, var(--surface));
  border: 1px solid color-mix(in srgb, #f59e0b 24%, var(--border-soft));
  color: #92400e;
}

.retention-table-wrap {
  overflow: visible;
}

.retention-table {
  table-layout: fixed;
  width: 100%;
}

.retention-col-info {
  width: 25%;
}

.retention-col-refresh {
  width: auto;
}

.retention-col-actions {
  width: 220px;
}

.retention-table td:first-child {
  overflow: hidden;
  max-width: 0;
}

.retention-table td:nth-child(2) {
  overflow: hidden;
  min-width: 0;
}

.retention-table__actions {
  width: 220px;
}

.retention-main {
  min-width: 0;
}

.retention-name {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  overflow: hidden;
}

.retention-path {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
  font-weight: 600;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.retention-meta {
  margin-top: 4px;
  color: var(--text-muted);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.retention-actions {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

.retention-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.retention-form__row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

@media (max-width: 720px) {
  .retention-col-info {
    width: auto;
  }

  .retention-col-refresh {
    width: 42%;
  }

  .retention-col-actions {
    width: 48px;
  }

  .retention-table th,
  .retention-table td {
    padding: 10px 8px;
  }

  .retention-table__actions,
  .retention-table__action-cell {
    width: 48px;
    text-align: right;
  }

  .retention-table__action-cell {
    padding-right: 8px;
  }

  .retention-name {
    gap: 6px;
  }

  .retention-meta {
    display: none;
  }

  .retention-form__row {
    grid-template-columns: 1fr;
  }
}
</style>
