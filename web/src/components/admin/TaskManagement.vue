<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  nextTick,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watch,
  watchEffect,
} from "vue";
import { storeToRefs } from "pinia";
import { getApiErrorMessage } from "@/api/client";
import {
  SCAN_MODES,
  createStrmBranch,
  createStrmTask,
  deleteStrmBranch,
  deleteStrmTask,
  fetchStrmBranches,
  fetchStrmStartupRemaining,
  fetchStrmTasks,
  forceStopStrmTask,
  precheckStrmAccountRepair,
  repairStrmAccountReferences,
  runStrmTaskNow,
  toggleStrmTask,
  updateStrmBranch,
  updateStrmTask,
  type StrmBranch,
  type StrmAccountRepairPrecheck,
  type StrmRunMode,
  type StrmTask,
  type StrmTaskInput,
} from "@/api/strm";
import FormField from "@/components/base/FormField.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import TimeWindowField from "@/components/base/TimeWindowField.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import AdminTaskTabHeader from "@/components/admin/AdminTaskTabHeader.vue";
import type { AdminTaskTabStat } from "@/components/admin/adminTaskTabHeader";
// StrmSettingsPanel 保持常驻挂载：新建 STRM 任务的默认扫描间隔依赖它加载的设置。
import StrmSettingsPanel from "@/components/admin/StrmSettingsPanel.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminEnableToggle from "@/components/admin/AdminEnableToggle.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import AdminRunStatusCell from "@/components/admin/AdminRunStatusCell.vue";
import AdminStartupBanner from "@/components/admin/AdminStartupBanner.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import type { AdminRunStatusVariant } from "@/components/admin/adminRunStatus";
import AdminTableActionBtn from "@/components/admin/AdminTableActionBtn.vue";
import AdminRowActions from "@/components/admin/AdminRowActions.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
  // 重面板仅在对应 Tab 或抽屉首次打开时加载。
import type CacheRetentionPanelComponent from "@/components/admin/CacheRetentionPanel.vue";
import type MediaOrganizePanelComponent from "@/components/admin/MediaOrganizePanel.vue";
const CacheRetentionPanel = defineAsyncComponent(() => import("@/components/admin/CacheRetentionPanel.vue"));
const CacheSettingsPanel = defineAsyncComponent(() => import("@/components/admin/CacheSettingsPanel.vue"));
const AutomationPanel = defineAsyncComponent(() => import("@/components/admin/AutomationPanel.vue"));
const MediaOrganizePanel = defineAsyncComponent(() => import("@/components/admin/MediaOrganizePanel.vue"));
const MediaOrganizeSettings = defineAsyncComponent(() => import("@/components/admin/MediaOrganizeSettings.vue"));
import CacheRuntimeStats from "@/components/admin/CacheRuntimeStats.vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import { useAccountPathLabel } from "@/composables/useAccountPathLabel";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useConditionalPolling } from "@/composables/useConditionalPolling";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import { liveElapsedMs, useLiveElapsedClock } from "@/composables/useLiveElapsedClock";
import {
  applyTimeWindowFromTask,
  timeWindowPayload,
  useTimeWindowSchedule,
  type ScheduleMode,
} from "@/composables/useTimeWindowSchedule";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { readPanelSaving, type SettingsPanelExpose } from "@/composables/useSettingsForm";
import { useStartupCountdown } from "@/composables/useStartupCountdown";
import { confirm, showConfirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import { useAccountsStore } from "@/stores/accounts";
import { formatElapsedMs, formatTime } from "@/utils/format";
import "@/styles/admin-shared.css";
import "@/styles/admin-table.css";

const CACHE_TAB = "cache";
const STRM_TAB = "strm";
const ORGANIZE_TAB = "organize";
const AUTOMATION_TAB = "automation";
const DEFAULT_STRM_SCAN_INTERVAL_MINUTES = 6 * 60;
const tabs = [
  { key: STRM_TAB, label: "STRM 任务" },
  { key: CACHE_TAB, label: "缓存任务" },
  { key: ORGANIZE_TAB, label: "目录整理" },
  { key: AUTOMATION_TAB, label: "自动联动" },
];

type DrawerKind = "strm" | "organize" | "cache";

const settingsDrawerOpen = ref(false);
const drawerKind = ref<DrawerKind>("strm");
// 抽屉内设置面板按 kind 首次打开才挂载，之后保持挂载（保留已加载数据与脏状态语义）。
const drawerKindsVisited = reactive<Record<DrawerKind, boolean>>({
  strm: false,
  organize: false,
  cache: false,
});

const retentionPanelRef = ref<InstanceType<typeof CacheRetentionPanelComponent> | null>(null);
const cacheSettingsRef = ref<SettingsPanelExpose | null>(null);
const strmSettingsRef = ref<SettingsPanelExpose | null>(null);
const organizePanelRef = ref<InstanceType<typeof MediaOrganizePanelComponent> | null>(null);
const automationPanelRef = ref<{ openCreate: () => void } | null>(null);
const organizeSettingsRef = ref<SettingsPanelExpose | null>(null);

const accountsStore = useAccountsStore();
const { accounts } = storeToRefs(accountsStore);

const tasks = ref<StrmTask[]>([]);
const refreshing = ref(false);
const strmListReady = ref(false);
const strmTaskList = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();
const { remainingDisplay: startupRemainingDisplay, applyStartupRemaining } = useStartupCountdown();

const strmPanelDirty = ref(false);
const cachePanelDirty = ref(false);
const organizePanelDirty = ref(false);

watchEffect(() => {
  strmPanelDirty.value = (strmSettingsRef.value as SettingsPanelExpose | null)?.isDirty?.() ?? false;
});

watchEffect(() => {
  cachePanelDirty.value = (cacheSettingsRef.value as SettingsPanelExpose | null)?.isDirty?.() ?? false;
});

watchEffect(() => {
  organizePanelDirty.value = (organizeSettingsRef.value as SettingsPanelExpose | null)?.isDirty?.() ?? false;
});

const drawerDirty = computed(() => {
  if (drawerKind.value === "strm") return strmPanelDirty.value;
  if (drawerKind.value === "cache") return cachePanelDirty.value;
  return organizePanelDirty.value;
});

const settingsPageDirty = computed(() => settingsDrawerOpen.value && drawerDirty.value);

function revertDrawerSettings() {
  if (drawerKind.value === "strm") strmSettingsRef.value?.revert?.();
  else if (drawerKind.value === "cache") cacheSettingsRef.value?.revert?.();
  else organizeSettingsRef.value?.revert?.();
}

const { confirmDiscardChanges } = useSettingsPageDirty(settingsPageDirty, revertDrawerSettings);

const { activeTab, setActiveTab } = useSectionTabRoute(
  STRM_TAB,
  [STRM_TAB, CACHE_TAB, ORGANIZE_TAB, AUTOMATION_TAB],
  {
  beforeTabChange: async (_from, _to) => {
    if (!settingsDrawerOpen.value) return true;
    const ok = await confirmDiscardChanges(() => drawerDirty.value);
    if (!ok) return false;
    settingsDrawerOpen.value = false;
    return true;
  },
});
useAdminPageLoading(
  "tasks",
  computed(() => activeTab.value === STRM_TAB && (!strmListReady.value || refreshing.value) && !tasks.value.length),
);

  // 面板首次激活后保持挂载，避免初次进入时并发加载全部接口。
const tabsVisited = reactive<Record<string, boolean>>({});
watch(activeTab, (tab) => {
  tabsVisited[tab] = true;
}, { immediate: true });

const dialogOpen = ref(false);
const editingId = ref<number | null>(null);
const submitting = ref(false);
type StrmRepairPhase = "idle" | "prompt" | "loading" | "result";
const STRM_REPAIR_MIN_LOADING_MS = 3000;
const strmRepairPhase = ref<StrmRepairPhase>("idle");
const strmRepairLoadingTitle = ref("正在检测并关联…");
const strmRepairPrecheck = ref<StrmAccountRepairPrecheck | null>(null);
const strmRepairResult = ref<{ ok: boolean; updated?: number; message: string } | null>(null);
const pendingCreateBody = ref<StrmTaskInput | null>(null);
const showAdvanced = ref(false);
const pickerOpen = ref(false);

const branchDialogOpen = ref(false);
const branchLoading = ref(false);
const branchTask = ref<StrmTask | null>(null);
const branches = ref<StrmBranch[]>([]);
const branchPickerOpen = ref(false);
const branchForm = reactive({
  parent_id: "",
  path: "",
  branch_type: "base" as "base" | "temporary",
  recursive: true,
  retention_days: 30,
});

const branchRootAnchor = computed(() => {
  const task = branchTask.value;
  if (!task) return undefined;
  const path = (task.path || "/").replace(/\/+$/, "") || "/";
  const segs = path.split("/").filter(Boolean);
  return {
    parentId: task.parent_id || "0",
    path,
    label: segs[segs.length - 1] || "任务目录",
  };
});
const temporaryRetentionOptions = [
  { value: 10, label: "10天" },
  { value: 30, label: "30天" },
  { value: 90, label: "90天" },
  { value: 365, label: "1年" },
  { value: 0, label: "永久" },
];

type TaskForm = Omit<StrmTaskInput, "sync_metadata" | "schedule_mode"> & {
  time_window_mode: "always" | "custom";
  sync_metadata: string;
  schedule_mode: ScheduleMode;
};

const emptyForm = (): TaskForm => ({
  name: "",
  account_id: 0,
  parent_id: "0",
  path: "",
  recursive: true,
  scan_interval: DEFAULT_STRM_SCAN_INTERVAL_MINUTES,
  scan_mode: "incremental_update",
  extensions: "",
  output_folder: "",
  group_dir: "",
  api_interval: 200,
  exclude_dir_keywords: "",
  exclude_file_keywords: "",
  sync_metadata: "false",
  branch_check_enabled: false,
  time_window_enabled: false,
  time_start: "00:00",
  time_end: "23:59",
  schedule_mode: "window",
  time_window_mode: "always",
});

const form = reactive(emptyForm());

const activeAccounts = computed(() => accounts.value.filter((a) => a.is_active));

const enabledCount = computed(() => tasks.value.filter((t) => isTaskEnabled(t)).length);
const errorCount = computed(() => tasks.value.filter((t) => t.status === "error").length);

const strmTabStats = computed<AdminTaskTabStat[]>(() => [
  { icon: "fa-list", value: tasks.value.length, label: "任务总数", tone: "blue" },
  { icon: "fa-play", value: enabledCount.value, label: "已启用", tone: "purple" },
  { icon: "fa-pause", value: errorCount.value, label: "异常", tone: "amber" },
]);

const organizeTabStats = computed<AdminTaskTabStat[]>(() => [
  { icon: "fa-list", value: organizePanelRef.value?.taskCount ?? 0, label: "任务数量", tone: "blue" },
  { icon: "fa-play", value: organizePanelRef.value?.runningCount ?? 0, label: "执行中", tone: "purple" },
  {
    icon: "fa-pause",
    value: organizePanelRef.value?.errorTaskCount ?? 0,
    label: "有失败",
    tone: "amber",
  },
]);

const drawerTitle = computed(() => {
  if (drawerKind.value === "organize") return "整理设置";
  if (drawerKind.value === "cache") return "缓存设置";
  return "STRM 设置";
});
const drawerSaving = computed(() => {
  if (drawerKind.value === "strm") return readPanelSaving(strmSettingsRef.value?.saving);
  if (drawerKind.value === "cache") return readPanelSaving(cacheSettingsRef.value?.saving);
  return readPanelSaving(organizeSettingsRef.value?.saving);
});
const drawerCanSave = drawerDirty;

const metadataSyncOptions = [
  { value: "false", label: "关闭" },
  { value: "true", label: "开启" },
];

const tasksPolling = useConditionalPolling({
  intervalMs: 3000,
  tickWhen: () => activeTab.value === STRM_TAB,
  onTick: () => loadTasks(true),
  shouldPoll: () => activeTab.value === STRM_TAB && (hasScanningTasks() || tasksPolling.isActive()),
});

const elapsedClock = useLiveElapsedClock();

watchEffect(() => {
  elapsedClock.sync(activeTab.value === STRM_TAB && hasScanningTasks());
});

const baseBranches = computed(() => branches.value.filter((b) => (b.branch_type || "temporary") === "base"));
const temporaryBranches = computed(() => branches.value.filter((b) => (b.branch_type || "temporary") !== "base"));

const branchDialogTitle = computed(() => {
  const name = branchTask.value?.name?.trim();
  return name ? `分支编辑 · ${name}` : "分支编辑";
});

const branchTypeTip = computed(() =>
  branchForm.branch_type === "base"
    ? "只扫描当前层，适合电影库等常新增内容的入口"
    : "递归扫描子目录，适合连载剧集；可设自动过期",
);

const hoveredTemporaryBranchId = ref<number | null>(null);
const hoveredTemporaryBranch = computed(
  () => temporaryBranches.value.find((b) => Number(b.id) === Number(hoveredTemporaryBranchId.value)) ?? null,
);

function branchDisplayPath(branch: StrmBranch): string {
  const rel = (branch.relative_path || "").trim().replace(/^\/+|\/+$/g, "");
  if (rel) return rel;
  const taskPath = (branchTask.value?.path || "/").replace(/\/+$/, "") || "/";
  const full = (branch.path || "").replace(/\/+$/, "");
  if (!full || taskPath === "/" || !full.startsWith(taskPath)) return full || "/";
  const suffix = full.slice(taskPath.length).replace(/^\//, "");
  return suffix || "（任务目录）";
}

function retentionSummaryLabel(branch: StrmBranch): string {
  const days = Number(branch.retention_days ?? 30);
  if (days <= 0) return "永久保留";
  return `保留 ${days} 天`;
}

function setHoveredTemporaryBranch(branch: StrmBranch) {
  hoveredTemporaryBranchId.value = branch.id ?? null;
}

function clearHoveredTemporaryBranch(branch: StrmBranch) {
  if (Number(hoveredTemporaryBranchId.value) === Number(branch.id)) {
    hoveredTemporaryBranchId.value = null;
  }
}

const scanModeOptions = SCAN_MODES.map((m) => ({ value: m.value, label: m.label }));
const retentionSelectOptions = temporaryRetentionOptions.map((o) => ({ value: String(o.value), label: o.label }));

function defaultScanInterval(): number {
  return strmSettingsRef.value?.getDefaultScanInterval?.() ?? DEFAULT_STRM_SCAN_INTERVAL_MINUTES;
}

async function openSettingsDrawer(kind?: DrawerKind) {
  drawerKind.value =
    kind ??
    (activeTab.value === ORGANIZE_TAB
      ? "organize"
      : activeTab.value === CACHE_TAB
        ? "cache"
        : "strm");
  drawerKindsVisited[drawerKind.value] = true;
  settingsDrawerOpen.value = true;
  if (drawerKind.value === "organize") {
    await nextTick();
    if (organizeSettingsRef.value && !organizeSettingsRef.value.isDirty?.()) {
      await organizeSettingsRef.value.reload?.();
    }
  } else if (drawerKind.value === "cache") {
    await nextTick();
    if (cacheSettingsRef.value && !cacheSettingsRef.value.isDirty?.()) {
      await cacheSettingsRef.value.reload?.();
    }
  } else {
    await nextTick();
    if (strmSettingsRef.value && !strmSettingsRef.value.isDirty?.()) {
      await strmSettingsRef.value.reload?.();
    }
  }
}

async function closeSettingsDrawer() {
  if (!(await confirmDiscardChanges(() => drawerDirty.value))) return;
  settingsDrawerOpen.value = false;
}

async function handleDrawerSave() {
  if (drawerKind.value === "strm") await strmSettingsRef.value?.save?.();
  else if (drawerKind.value === "cache") await cacheSettingsRef.value?.save?.();
  else await organizeSettingsRef.value?.save?.();
}

const { timeWindowDisplay, timePickerMode, onTimeWheelConfirm } = useTimeWindowSchedule(form, {
  allowManual: true,
});

const editingTask = computed(() => {
  if (!editingId.value) return null;
  return tasks.value.find((t) => t.id === editingId.value) ?? null;
});

const strmScheduleLockedReason = computed(() => {
  if (!editingTask.value?.automation_managed) return "";
  return "该 STRM 任务当前正被自动联动接管，不能直接改回全天或时间段；如需恢复定时，请先从相关自动联动中移除后再调整。";
});

const { display: sourceDirDisplay, title: sourceDirTitle } = useAccountPathLabel({
  accountId: computed(() => form.account_id),
  path: computed(() => form.path),
  accounts,
  showLeafOnly: true,
});

function isTaskEnabled(task: StrmTask): boolean {
  return task.status === "active" || task.status === "running";
}

function formatFileCount(count: number): string {
  if (!count) return "0";
  return count.toLocaleString();
}

function scanPhasePrimaryText(task: StrmTask): string {
  switch (task.scan_phase) {
    case "comparing_metadata":
      return "正在比对元数据";
    case "syncing_metadata":
      return "正在下载元数据";
    case "uploading_metadata":
      return "正在上传元数据";
    case "cleaning_metadata":
      return "正在清理本地元数据";
    default:
      return "正在扫描并生成 STRM";
  }
}

function isMetadataScanPhase(task: StrmTask): boolean {
  return task.scan_phase !== undefined && task.scan_phase !== "scanning";
}

function isTaskScanning(task: StrmTask): boolean {
  return Boolean(task.is_scanning);
}

function scanElapsedText(task: StrmTask): string {
  if (!isTaskScanning(task)) return "";
  void elapsedClock.tick.value;
  const fromStart = liveElapsedMs(task.started_at, elapsedClock.tick.value);
  if (fromStart > 0) return formatElapsedMs(fromStart);
  const fallback = Number(task.current_duration_ms || 0);
  if (fallback > 0) return formatElapsedMs(fallback);
  return formatElapsedMs(1000);
}

function displayTaskName(name: string): string {
  const chars = Array.from(name || "");
  if (chars.length <= 7) return name || "";
  return `${chars.slice(0, 7).join("")}...`;
}

function formatLastScan(value?: string): string {
  if (!value) return "未执行";
  const formatted = formatTime(value);
  return formatted === "-" ? "未执行" : formatted;
}

function scanStatusVariant(task: StrmTask): AdminRunStatusVariant {
  if (isTaskScanning(task)) return "running";
  if (task.last_scan_status === "ok" || task.last_scan_status === "success") return "success";
  if (task.last_scan_status === "failed" || task.last_scan_status === "error" || task.last_scan_status === "protected") return "error";
  return "pending";
}

function lastScanPrimaryText(task: StrmTask): string {
  if (isTaskScanning(task)) {
    const phase = scanPhasePrimaryText(task);
    const label = task.current_label?.trim();
    if (label) return `${phase} — ${label}`;
    return `${phase}…`;
  }
  return formatLastScan(task.last_scan);
}

function lastScanSummary(task: StrmTask): string {
  if (isTaskScanning(task)) {
    const parts: string[] = [];
    if (isMetadataScanPhase(task)) {
      const total = Number(task.metadata_total || 0);
      const done = Number(task.metadata_done || 0);
      if (total > 0) {
        parts.push(`${done} / ${total}`);
      }
    } else {
      const dirs = Number(task.scanned_dirs || 0);
      const files = Number(task.scanned_files || 0);
      if (dirs > 0 || files > 0) {
        parts.push(`已扫 ${dirs} 目录 / ${formatFileCount(files)} 文件`);
      }
    }
    const elapsed = scanElapsedText(task);
    if (elapsed) parts.push(elapsed);
    return parts.length ? parts.join(" · ") : "处理中…";
  }
  if (!task.last_scan) return "";
  if (task.last_scan_status === "failed" || task.last_scan_status === "error") {
    return task.error_message?.trim() || "执行失败";
  }
  if (task.last_scan_status === "protected") {
    return task.error_message?.trim() || "安全保护阻止清理";
  }
  const parts: string[] = [];
  const created = Number(task.generated_count || 0);
  const updated = Number(task.updated_count || 0);
  const deleted = Number(task.removed_count || 0);
  if (created > 0) parts.push(`新增 ${created}`);
  if (updated > 0) parts.push(`改写 ${updated}`);
  if (deleted > 0) parts.push(`删除 ${deleted}`);
  if (!parts.length) parts.push("无变更");
  return parts.join(" · ");
}

function lastScanTitle(task: StrmTask): string {
  const parts = [lastScanPrimaryText(task)];
  const summary = lastScanSummary(task);
  if (summary) parts.push(summary);
  return parts.join("\n");
}

function hasScanningTasks(): boolean {
  return tasks.value.some((t) => isTaskScanning(t));
}

function syncTasksPolling() {
  tasksPolling.sync();
}

async function loadTasks(quiet = false) {
  if (!quiet) refreshing.value = true;
  try {
    tasks.value = await fetchStrmTasks();
    void loadStartupRemaining();
    syncTasksPolling();
  } catch (e) {
    if (!quiet) toast.error(getApiErrorMessage(e, "加载 STRM 任务失败"));
  } finally {
    if (!quiet) refreshing.value = false;
    strmListReady.value = true;
  }
}

async function loadStartupRemaining() {
  try {
    const startup = await fetchStrmStartupRemaining();
    applyStartupRemaining(startup.startup_remaining ?? 0);
  } catch {
    /* 启动退避提示失败不阻断任务列表 */
  }
}

async function refreshAll() {
  await Promise.all([loadTasks(), accountsStore.loadAccounts()]);
}

function resetForm() {
  Object.assign(form, emptyForm());
  form.scan_interval = defaultScanInterval();
  showAdvanced.value = false;
}

function resetStrmRepairFlow() {
  strmRepairPhase.value = "idle";
  strmRepairLoadingTitle.value = "正在检测并关联…";
  strmRepairPrecheck.value = null;
  strmRepairResult.value = null;
  pendingCreateBody.value = null;
}

function sleep(ms: number) {
  return new Promise<void>((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function closeTaskDialog() {
  if (strmRepairPhase.value === "loading") return;
  dialogOpen.value = false;
  resetStrmRepairFlow();
}

function openCreate() {
  editingId.value = null;
  resetForm();
  resetStrmRepairFlow();
  dialogOpen.value = true;
}

function openEdit(task: StrmTask) {
  if (isTaskScanning(task)) {
    toast.info("当前任务正在进行，请停止后再修改设置");
    return;
  }
  editingId.value = task.id ?? null;
  form.name = task.name;
  form.account_id = task.account_id;
  form.parent_id = task.parent_id;
  form.path = task.path;
  form.recursive = task.recursive;
  form.scan_interval = task.scan_interval || defaultScanInterval();
  form.scan_mode = task.scan_mode;
  form.extensions = task.extensions ?? "";
  form.output_folder = task.output_folder ?? "";
  form.group_dir = task.group_dir ?? "";
  form.api_interval = task.api_interval ?? 200;
  form.exclude_dir_keywords = task.exclude_dir_keywords ?? "";
  form.exclude_file_keywords = task.exclude_file_keywords ?? "";
  form.sync_metadata = task.sync_metadata ? "true" : "false";
  form.branch_check_enabled = !!task.branch_check_enabled;
  applyTimeWindowFromTask(form, task);
  showAdvanced.value = false;
  dialogOpen.value = true;
}

function onFolderPicked(payload: { accountId: number; parentId: string; path: string }) {
  form.account_id = payload.accountId;
  form.parent_id = payload.parentId;
  form.path = payload.path || "/";
  pickerOpen.value = false;
}

function buildTaskPayload(): StrmTaskInput {
  const name = form.name.trim();
  return {
    name,
    account_id: form.account_id,
    parent_id: form.parent_id,
    path: form.path,
    recursive: form.recursive,
    scan_interval: form.scan_interval,
    scan_mode: form.scan_mode,
    extensions: form.extensions,
    output_folder: form.output_folder.trim() || name,
    group_dir: form.group_dir.trim(),
    api_interval: Number(form.api_interval) || 0,
    exclude_dir_keywords: form.exclude_dir_keywords.trim(),
    exclude_file_keywords: form.exclude_file_keywords.trim(),
    sync_metadata: form.sync_metadata === "true",
    branch_check_enabled: form.branch_check_enabled,
    ...timeWindowPayload(form),
    schedule_mode: form.schedule_mode,
  };
}

async function finishCreateTask(body: StrmTaskInput) {
  await createStrmTask(body);
  dialogOpen.value = false;
  resetStrmRepairFlow();
  await loadTasks();
  toast.success("任务已创建");
}

async function runStrmRepairCheck() {
  const body = pendingCreateBody.value;
  const pre = strmRepairPrecheck.value;
  if (!body || !pre) return;

  const oldID = pre.old_account_id ?? 0;
  if (oldID <= 0) {
    strmRepairResult.value = {
      ok: false,
      message: "无法与当前账号关联。",
    };
    strmRepairPhase.value = "result";
    return;
  }

  strmRepairLoadingTitle.value = "正在检测并关联…";
  strmRepairPhase.value = "loading";
  try {
    const [repaired] = await Promise.all([
      repairStrmAccountReferences({
        account_id: body.account_id,
        old_account_id: oldID,
        parent_id: body.parent_id,
        recursive: body.recursive,
        output_folder: body.group_dir ? `${body.group_dir}/${body.output_folder}` : body.output_folder,
      }),
      sleep(STRM_REPAIR_MIN_LOADING_MS),
    ]);
    if (repaired.updated > 0) {
      strmRepairResult.value = {
        ok: true,
        updated: repaired.updated,
        message: `已成功关联并修复 ${repaired.updated} 个 STRM 文件。`,
      };
    } else {
      strmRepairResult.value = {
        ok: false,
        message: "未能修复 STRM 文件，请检查输出目录或稍后再试。",
      };
    }
    strmRepairPhase.value = "result";
  } catch (e) {
    strmRepairResult.value = {
      ok: false,
      message: getApiErrorMessage(e, "无法与当前账号关联"),
    };
    strmRepairPhase.value = "result";
  }
}

async function skipStrmRepairAndCreate() {
  const body = pendingCreateBody.value;
  if (!body) return;
  submitting.value = true;
  try {
    await finishCreateTask(body);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "创建任务失败"));
  } finally {
    submitting.value = false;
  }
}

async function confirmAfterStrmRepair() {
  const body = pendingCreateBody.value;
  if (!body) return;
  submitting.value = true;
  try {
    await finishCreateTask(body);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "创建任务失败"));
  } finally {
    submitting.value = false;
  }
}

async function submitTask() {
  const name = form.name.trim();
  if (!name) {
    toast.error("请填写任务名称");
    return;
  }
  if (!form.account_id) {
    toast.error("请选择账号及目录");
    return;
  }
  if (!form.path.trim()) {
    toast.error("请选择源目录");
    return;
  }
  if (strmRepairPhase.value !== "idle") return;
  submitting.value = true;
  try {
    const body = buildTaskPayload();
    if (editingId.value) {
      await updateStrmTask(editingId.value, body);
      toast.success("任务已更新");
      dialogOpen.value = false;
      await loadTasks();
      return;
    }
    pendingCreateBody.value = body;
    strmRepairLoadingTitle.value = "正在检查输出目录…";
    strmRepairPhase.value = "loading";
    const pre = await precheckStrmAccountRepair({
      account_id: body.account_id,
      parent_id: body.parent_id,
      recursive: body.recursive,
      output_folder: body.group_dir ? `${body.group_dir}/${body.output_folder}` : body.output_folder,
    });
    if (!pre.needs_prompt) {
      await finishCreateTask(body);
      return;
    }
    strmRepairPrecheck.value = pre;
    strmRepairResult.value = null;
    strmRepairPhase.value = "prompt";
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存任务失败"));
  } finally {
    submitting.value = false;
  }
}

async function setTaskEnabled(task: StrmTask, enabled: boolean) {
  if (!task.id) return;
  if (enabled === isTaskEnabled(task)) return;
  try {
    const updated = await toggleStrmTask(task.id);
    const idx = tasks.value.findIndex((t) => t.id === task.id);
    if (idx >= 0) tasks.value[idx] = updated;
    toast.success(updated.status === "paused" ? "任务已禁用" : "任务已启用");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "切换状态失败"));
  }
}

function handleRunButtonClick(task: StrmTask) {
  if (task.branch_check_enabled) return;
  void handleRun(task, "full");
}

async function handleRun(task: StrmTask, mode: StrmRunMode = "auto") {
  if (!task.id) return;
  try {
    await runStrmTaskNow(task.id, mode);
    toast.success("已触发执行");
    await loadTasks(true);
    tasksPolling.start();
  } catch (e) {
    const msg = getApiErrorMessage(e, "执行失败");
    if (msg.includes("已加入执行队列") || msg.includes("启动退避")) {
      toast.info(msg);
      await loadTasks(true);
      return;
    }
    toast.error(msg);
  }
}

async function handleForceStop(task: StrmTask) {
  if (!task.id) return;
  tasksPolling.stop();
  try {
    await forceStopStrmTask(task.id);
    toast.success("任务已强制停止，下次调度不受影响");
    await loadTasks(true);
    if (hasScanningTasks()) tasksPolling.start();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "强制停止失败"));
    if (hasScanningTasks()) tasksPolling.start();
  }
}

async function handleDelete(task: StrmTask) {
  if (!task.id) return;
  const taskID = task.id;
  const result = await showConfirm({
    title: "删除 STRM 任务",
    message: `确定删除任务「${task.name}」吗？`,
    hint: "如需同时删除本地 STRM 文件，请勾选下方选项。",
    checkboxLabel: "同时删除本地 STRM 文件",
    icon: "trash",
    confirmText: "删除",
    danger: true,
  }).catch(() => null);
  if (!result || result.action !== "confirm") return;
  const deleteStrmFiles = !!result.checked;
  try {
    await removeWithDust({
      target: findDustTarget(strmTaskList.value, `strm-task-${taskID}`),
      container: strmTaskList.value,
      remove: async () => {
        await deleteStrmTask(taskID, deleteStrmFiles);
        tasks.value = tasks.value.filter((t) => t.id !== taskID);
      },
    });
    syncTasksPolling();
    toast.success(deleteStrmFiles ? "任务和 STRM 文件已删除" : "任务已删除");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "删除失败"));
  }
}

function resetBranchForm() {
  Object.assign(branchForm, {
    parent_id: "",
    path: "",
    branch_type: "base",
    recursive: true,
    retention_days: 30,
  });
}

const branchTypeOptions = [
  { value: "base", label: "入口分支" },
  { value: "temporary", label: "监控分支" },
];

function setBranchType(type: "base" | "temporary") {
  branchForm.branch_type = type;
  if (type === "base") {
    branchForm.recursive = false;
  } else {
    branchForm.recursive = true;
    branchForm.retention_days = branchForm.retention_days ?? 30;
  }
}

async function loadBranches() {
  if (!branchTask.value?.id) return;
  branchLoading.value = true;
  try {
    branches.value = await fetchStrmBranches(branchTask.value.id);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载分支失败"));
  } finally {
    branchLoading.value = false;
  }
}

async function openBranchDialog() {
  if (!editingId.value) return;
  const task = tasks.value.find((t) => t.id === editingId.value);
  if (!task) return;
  branchTask.value = task;
  resetBranchForm();
  branchDialogOpen.value = true;
  await loadBranches();
}

function closeBranchDialog() {
  branchDialogOpen.value = false;
  branchTask.value = null;
  branches.value = [];
  hoveredTemporaryBranchId.value = null;
  resetBranchForm();
}

async function onBranchFolderPicked(payload: { accountId: number; parentId: string; path: string }) {
  if (!branchTask.value?.id) return;
  const taskPath = (branchTask.value.path || "/").replace(/\/+$/, "") || "/";
  const picked = (payload.path || "/").replace(/\/+$/, "") || "/";
  if (picked !== taskPath && !picked.startsWith(`${taskPath}/`)) {
    toast.error("分支目录必须在任务源目录下");
    branchPickerOpen.value = false;
    return;
  }
  branchPickerOpen.value = false;
  const branchType = branchForm.branch_type;
  try {
    await createStrmBranch(branchTask.value.id, {
      account_id: branchTask.value.account_id,
      parent_id: payload.parentId,
      path: picked,
      branch_type: branchType,
      recursive: branchType === "temporary",
      retention_days: branchType === "temporary" ? 30 : 0,
    });
    toast.success("分支已添加");
    await loadBranches();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "添加分支失败"));
  }
}

async function updateBranchRetention(branch: StrmBranch) {
  if (!branchTask.value?.id || !branch.id) return;
  try {
    await updateStrmBranch(branchTask.value.id, branch.id, {
      retention_days: Number(branch.retention_days ?? 30),
    });
  } catch (e) {
    toast.error(getApiErrorMessage(e, "更新分支失败"));
    await loadBranches();
  }
}

async function removeBranch(branch: StrmBranch) {
  if (!branchTask.value?.id || !branch.id) return;
  try {
    await confirm({
      title: "删除分支",
      message: `确定删除分支「${branch.path}」吗？`,
      icon: "trash",
      confirmText: "删除",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await deleteStrmBranch(branchTask.value.id, branch.id);
    toast.success("分支已删除");
    await loadBranches();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "删除分支失败"));
  }
}

let pageActive = false;
let activatedOnce = false;

function activatePage() {
  pageActive = true;
  document.addEventListener("visibilitychange", onVisibilityChange);
  if (activatedOnce && strmListReady.value) void loadTasks(true);
  activatedOnce = true;
  if (activeTab.value === STRM_TAB) tasksPolling.sync();
}

function deactivatePage() {
  pageActive = false;
  tasksPolling.stop();
  document.removeEventListener("visibilitychange", onVisibilityChange);
    // 离开 KeepAlive 任务页时收回 Teleport 抽屉，避免覆盖新页面。
  if (settingsDrawerOpen.value) {
    if (drawerDirty.value) revertDrawerSettings();
    settingsDrawerOpen.value = false;
  }
}

onMounted(async () => {
  await refreshAll();
  if (pageActive && activeTab.value === STRM_TAB) tasksPolling.sync();
});

onActivated(activatePage);
onDeactivated(deactivatePage);
onUnmounted(deactivatePage);

function onVisibilityChange() {
  if (pageActive && !document.hidden && activeTab.value === STRM_TAB) {
    void loadTasks(true);
    tasksPolling.sync();
  }
}

watch(activeTab, (tab) => {
  if (!pageActive) return;
  if (tab === STRM_TAB) {
    if (!refreshing.value) void loadTasks(true);
    tasksPolling.sync();
  } else {
    tasksPolling.stop();
  }
});
</script>

<template>
  <div class="settings strm-page">
    <SectionTabBar :model-value="activeTab" :tabs="tabs" @update:model-value="setActiveTab">
      <template #actions>
        <AppButton
          v-if="activeTab === CACHE_TAB"
          type="button"
          variant="primary"
          @click="retentionPanelRef?.openCreate()"
        >
          添加任务
        </AppButton>
        <AppButton v-else-if="activeTab === STRM_TAB" type="button" variant="primary" @click="openCreate">
          添加任务
        </AppButton>
        <AppButton
          v-else-if="activeTab === ORGANIZE_TAB"
          type="button"
          variant="primary"
          @click="organizePanelRef?.openCreate()"
        >
          添加任务
        </AppButton>
        <AppButton
          v-else-if="activeTab === AUTOMATION_TAB"
          type="button"
          variant="primary"
          @click="automationPanelRef?.openCreate()"
        >
          新增联动
        </AppButton>
      </template>
    </SectionTabBar>

    <div v-if="tabsVisited[CACHE_TAB]" v-show="activeTab === CACHE_TAB">
      <AdminTaskTabHeader
        settings-title="缓存设置"
        settings-hint="通用缓存 · WebDAV"
        @open-settings="openSettingsDrawer('cache')"
      >
        <CacheRuntimeStats />
      </AdminTaskTabHeader>
      <CacheRetentionPanel ref="retentionPanelRef" hide-stats />
    </div>

    <div v-show="activeTab === STRM_TAB" class="strm-task-panel">
      <AdminTaskTabHeader
        :stats="strmTabStats"
        :refreshing="refreshing"
        settings-title="STRM 设置"
        settings-hint="扫描规则 · 播放地址"
        @refresh="loadTasks()"
        @open-settings="openSettingsDrawer('strm')"
      />

      <AdminStartupBanner :seconds="startupRemainingDisplay" />

      <AdminEmptyState
        v-if="strmListReady && !refreshing && !tasks.length"
        icon="🎬"
        title="还没有 STRM 任务"
        description="添加任务后，系统会定期扫描网盘目录并生成本地 .strm 播放链接文件。"
      >
        <AppButton type="button" variant="primary" @click="openCreate">添加第一个任务</AppButton>
      </AdminEmptyState>

      <div v-else-if="tasks.length" class="admin-panel-table-wrap strm-task-table-wrap">
        <table class="admin-table strm-task-table">
          <thead>
            <tr>
              <th>任务</th>
              <th class="strm-task-table__path-col">目录</th>
              <th>最后扫描</th>
              <th class="strm-task-table__actions">操作</th>
            </tr>
          </thead>
          <tbody ref="strmTaskList">
            <tr v-for="task in tasks" :key="task.id" class="strm-task-row" :data-dust-key="`strm-task-${task.id}`">
              <td>
                <div class="strm-task-main" :title="task.name">
                  <div class="strm-task-name">
                    <span class="strm-task-name__text">{{ displayTaskName(task.name) }}</span>
                    <AdminStatusPill :tone="isTaskEnabled(task) ? 'success' : 'warning'">
                      {{ isTaskEnabled(task) ? "已启用" : "已禁用" }}
                    </AdminStatusPill>
                  </div>
                </div>
              </td>
              <td class="strm-task-path strm-task-table__path-col" :title="task.path">{{ task.path }}</td>
              <td>
                <AdminRunStatusCell
                  :title="lastScanTitle(task)"
                  :primary="lastScanPrimaryText(task)"
                  :summary="lastScanSummary(task)"
                  :variant="scanStatusVariant(task)"
                  :live="isTaskScanning(task)"
                  :primary-tone="isTaskScanning(task) ? 'default' : 'muted'"
                />
              </td>
              <td class="admin-table__actions">
                <AdminRowActions>
                  <div class="strm-task-actions">
                    <AdminEnableToggle
                      :enabled="isTaskEnabled(task)"
                      aria-label="任务启用切换"
                      @enable="setTaskEnabled(task, $event)"
                    />
                    <AdminTableActionBtn
                      v-if="isTaskScanning(task)"
                      icon="stop"
                      title="强制停止"
                      danger
                      @click="handleForceStop(task)"
                    />
                    <div v-else class="strm-run-menu-wrap">
                      <AdminTableActionBtn
                        icon="play"
                        title="立即执行"
                        :no-tip="task.branch_check_enabled"
                        @click="handleRunButtonClick(task)"
                      />
                      <div v-if="task.branch_check_enabled" class="strm-run-menu">
                        <button type="button" @click="handleRun(task, 'full')">全部执行</button>
                        <button type="button" @click="handleRun(task, 'branch')">分支执行</button>
                      </div>
                    </div>
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
                      v-if="isTaskScanning(task)"
                      type="button"
                      class="admin-row-actions__item admin-row-actions__item--danger"
                      @click="handleForceStop(task)"
                    >
                      强制停止
                    </button>
                    <template v-else-if="task.branch_check_enabled">
                      <button type="button" class="admin-row-actions__item" @click="handleRun(task, 'full')">
                        全部执行
                      </button>
                      <button type="button" class="admin-row-actions__item" @click="handleRun(task, 'branch')">
                        分支执行
                      </button>
                    </template>
                    <button v-else type="button" class="admin-row-actions__item" @click="handleRun(task, 'full')">
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
    </div>

    <div v-if="tabsVisited[ORGANIZE_TAB]" v-show="activeTab === ORGANIZE_TAB">
      <AdminTaskTabHeader
        :stats="organizeTabStats"
        :refreshing="organizePanelRef?.refreshing ?? false"
        settings-title="整理设置"
        settings-hint="TMDB · 命名规则"
        @refresh="organizePanelRef?.loadTasks()"
        @open-settings="openSettingsDrawer('organize')"
      />
      <MediaOrganizePanel ref="organizePanelRef" hide-stats />
    </div>

    <div v-if="tabsVisited[AUTOMATION_TAB]" v-show="activeTab === AUTOMATION_TAB">
      <AutomationPanel ref="automationPanelRef" />
    </div>

    <AdminSettingsDrawer
      :open="settingsDrawerOpen"
      :title="drawerTitle"
      :saving="drawerSaving"
      :can-save="drawerCanSave"
      @close="closeSettingsDrawer"
      @cancel="closeSettingsDrawer"
      @save="handleDrawerSave"
    >
      <StrmSettingsPanel v-show="drawerKind === 'strm'" ref="strmSettingsRef" />
      <CacheSettingsPanel v-if="drawerKindsVisited.cache" v-show="drawerKind === 'cache'" ref="cacheSettingsRef" />
      <MediaOrganizeSettings v-if="drawerKindsVisited.organize" v-show="drawerKind === 'organize'" ref="organizeSettingsRef" />
    </AdminSettingsDrawer>

    <AppModal
      :open="dialogOpen"
      size="account"
      :title="editingId ? '编辑 STRM 任务' : '添加 STRM 任务'"
      @close="closeTaskDialog"
    >
      <div class="strm-form" :class="{ 'strm-form--repair-active': strmRepairPhase !== 'idle' }">
        <div class="strm-form__row">
          <FormField label="任务名称">
            <AppInput v-model="form.name" placeholder="例如：电影库" />
          </FormField>
          <FormField label="扫描方式">
            <AppSelect v-model="form.scan_mode" :options="scanModeOptions" />
          </FormField>
        </div>

        <div class="strm-form__row">
          <FormField label="分组目录">
            <template #help>
              <SettingsHelpTooltip title="分组目录说明">
                <p>STRM 文件生成到哪个父文件夹下，用来把多个任务归进同一个媒体库目录。</p>
                <p>留空 = 直接生成在 STRM 根目录；填「电影」= 生成到 /app/strm/电影/任务名/，支持多级「电影/港台」。</p>
                <p>示例：两个任务都填「电影」，任务名「电影1」「电影2」，会得到 /app/strm/电影/电影1 和 /app/strm/电影/电影2，Emby 只需勾选「电影」文件夹。</p>
              </SettingsHelpTooltip>
            </template>
            <AppInput v-model="form.group_dir" placeholder="留空则生成在根目录，如：电影" />
          </FormField>
          <FormField label="选择账号及目录">
            <AccountFolderField
              :display="sourceDirDisplay"
              :title="sourceDirTitle"
              @browse="pickerOpen = true"
            />
          </FormField>
        </div>

        <div class="strm-form__row">
          <FormField label="API额外补偿间隔(毫秒)">
            <AppInput v-model="form.api_interval" type="number" min="0" max="5000" />
          </FormField>
          <FormField label="执行计划">
            <TimeWindowField
              :display="timeWindowDisplay"
              :start-time="form.time_start"
              :end-time="form.time_end"
              :all-day="form.time_window_mode === 'always'"
              :allow-manual="true"
              :mode="timePickerMode"
              :manual-locked="Boolean(strmScheduleLockedReason)"
              :manual-locked-reason="strmScheduleLockedReason || undefined"
              @confirm="onTimeWheelConfirm"
            />
          </FormField>
        </div>

        <button type="button" class="strm-advanced-toggle" @click="showAdvanced = !showAdvanced">
          {{ showAdvanced ? "收起选项" : "更多选项" }}
          <svg class="strm-advanced-toggle__icon" :class="{ 'strm-advanced-toggle__icon--open': showAdvanced }" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <path d="M4 6l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>

        <template v-if="showAdvanced">
          <div class="strm-form__row">
            <FormField label="排除目录关键词">
              <AppInput v-model="form.exclude_dir_keywords" placeholder="例如：sample;预告;花絮" />
            </FormField>
            <FormField label="排除文件关键词">
              <AppInput v-model="form.exclude_file_keywords" placeholder="例如：sample;trailer;预告" />
            </FormField>
          </div>

          <div class="strm-form__row">
            <FormField label="同步元数据">
              <AppSelect
                v-model="form.sync_metadata"
                :options="metadataSyncOptions"
                placeholder="请选择是否同步元数据"
              />
            </FormField>
            <FormField label="分支检查">
              <div class="strm-branch-control" :class="{ 'strm-branch-control--on': form.branch_check_enabled }">
                <button
                  type="button"
                  class="strm-branch-toggle"
                  @click="form.branch_check_enabled = !form.branch_check_enabled"
                >
                  <span class="strm-branch-toggle__dot" />
                  <span class="strm-branch-toggle__text">{{ form.branch_check_enabled ? "开启" : "关闭" }}</span>
                </button>
                <button
                  type="button"
                  class="strm-branch-edit"
                  title="编辑分支"
                  :disabled="!editingId"
                  @click="openBranchDialog"
                >
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 4h10" />
                    <path d="M3 8h10" />
                    <path d="M3 12h6" />
                    <path d="M11.5 10.5v3" />
                    <path d="M10 12h3" />
                  </svg>
                </button>
              </div>
            </FormField>
          </div>
        </template>

        <div v-if="strmRepairPhase === 'idle'" class="modal-form__footer">
          <AppButton type="button" variant="primary" :disabled="submitting" @click="submitTask">
            {{ submitting ? "保存中…" : "保存" }}
          </AppButton>
        </div>

        <div v-if="strmRepairPhase !== 'idle'" class="strm-repair-overlay">
          <div v-if="strmRepairPhase === 'prompt'" class="strm-repair-panel">
            <p class="strm-repair-panel__message">
              输出目录「{{ pendingCreateBody?.output_folder }}」已存在，是否检测与当前账号能否关联？
            </p>
            <div class="strm-repair-panel__actions">
              <AppButton type="button" variant="secondary" :disabled="submitting" @click="skipStrmRepairAndCreate">
                忽略，直接创建
              </AppButton>
              <AppButton type="button" variant="primary" @click="runStrmRepairCheck">检测，尝试关联</AppButton>
            </div>
          </div>

          <div v-else-if="strmRepairPhase === 'loading'" class="strm-repair-loading">
            <BusySpinner variant="notch" :size="42" color="var(--brand)" />
            <div class="strm-repair-loading__title">{{ strmRepairLoadingTitle }}</div>
          </div>

          <div v-else-if="strmRepairPhase === 'result'" class="strm-repair-panel">
            <p
              class="strm-repair-panel__result"
              :class="strmRepairResult?.ok ? 'strm-repair-panel__result--ok' : 'strm-repair-panel__result--fail'"
            >
              {{ strmRepairResult?.message }}
            </p>
            <div class="strm-repair-panel__actions">
              <AppButton
                type="button"
                variant="primary"
                :disabled="submitting"
                @click="confirmAfterStrmRepair"
              >
                {{ submitting ? "处理中…" : strmRepairResult?.ok ? "确定" : "仍要创建" }}
              </AppButton>
            </div>
          </div>
        </div>
      </div>
    </AppModal>

    <AppModal :open="branchDialogOpen" size="branch" :title="branchDialogTitle" @close="closeBranchDialog">
      <div class="strm-branch-dialog">
        <section class="strm-branch-add-card">
          <div class="strm-branch-add-card__head">
            <span class="strm-branch-add-card__title">添加分支</span>
            <span class="strm-branch-add-card__tip">{{ branchTypeTip }}</span>
          </div>

          <div class="strm-branch-add-card__toolbar">
            <SettingsSegment
              :model-value="branchForm.branch_type"
              label="分支类型"
              :options="branchTypeOptions"
              @update:model-value="setBranchType($event as 'base' | 'temporary')"
            />

            <AccountFolderField
              wrapper-class="strm-branch-add-card__path"
              placeholder="选择分支目录"
              title="请选择任务目录下的分支"
              @browse="branchPickerOpen = true"
            />
          </div>
        </section>

        <AppStateBlock v-if="branchLoading" message="加载分支中…" loading min-height="160px" />

        <div v-else class="strm-branch-columns">
          <section class="strm-branch-column">
            <div class="strm-branch-column__head">
              <div class="strm-branch-column__title">
                <span>入口分支</span>
                <span class="strm-branch-column__badge">{{ baseBranches.length }}</span>
              </div>
              <span class="strm-branch-column__desc">只扫一层，老目录跳过</span>
            </div>
            <div v-if="!baseBranches.length" class="strm-branch-empty">
              <p>暂无入口分支</p>
              <span>例如把「电影」目录添为入口，只关注新增内容</span>
            </div>
            <ul v-else class="strm-branch-list">
              <li v-for="branch in baseBranches" :key="branch.id" class="strm-branch-row">
                <div class="strm-branch-row__main">
                  <span class="strm-branch-row__path" :title="branch.path">{{ branchDisplayPath(branch) }}</span>
                  <span v-if="branch.source === 'auto'" class="strm-branch-tag">自动</span>
                </div>
                <span class="strm-branch-row__slot" aria-hidden="true" />
                <button type="button" class="strm-branch-row__delete" title="删除分支" @click="removeBranch(branch)">
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                    <path d="M3 4h10" stroke-linecap="round" />
                    <path d="M6 4V3.2c0-.7.5-1.2 1.2-1.2h1.6c.7 0 1.2.5 1.2 1.2V4" />
                    <path d="M5 6v7c0 .6.4 1 1 1h4c.6 0 1-.4 1-1V6" />
                  </svg>
                </button>
              </li>
            </ul>
          </section>

          <section class="strm-branch-column">
            <div class="strm-branch-column__head">
              <div class="strm-branch-column__title">
                <span>监控分支</span>
                <span class="strm-branch-column__badge">{{ temporaryBranches.length }}</span>
              </div>
              <span
                class="strm-branch-column__meta"
                :class="{ 'strm-branch-column__meta--preview': hoveredTemporaryBranch }"
              >
                {{
                  hoveredTemporaryBranch
                    ? retentionSummaryLabel(hoveredTemporaryBranch)
                    : "递归扫描，可设保留期"
                }}
              </span>
            </div>
            <div v-if="!temporaryBranches.length" class="strm-branch-empty">
              <p>暂无监控分支</p>
              <span>适合连载剧集；入口下发现新剧集目录也会自动加入</span>
            </div>
            <ul v-else class="strm-branch-list">
              <li v-for="branch in temporaryBranches" :key="branch.id" class="strm-branch-row">
                <div class="strm-branch-row__main">
                  <span class="strm-branch-row__path" :title="branch.path">{{ branchDisplayPath(branch) }}</span>
                  <span v-if="branch.source === 'auto'" class="strm-branch-tag">自动</span>
                </div>
                <AppSelect
                  class="strm-branch-retention-select"
                  :model-value="String(branch.retention_days ?? 30)"
                  :options="retentionSelectOptions"
                  @update:model-value="
                    branch.retention_days = Number($event);
                    updateBranchRetention(branch);
                  "
                  @mouseenter="setHoveredTemporaryBranch(branch)"
                  @mouseleave="clearHoveredTemporaryBranch(branch)"
                  @focus="setHoveredTemporaryBranch(branch)"
                  @blur="clearHoveredTemporaryBranch(branch)"
                />
                <button type="button" class="strm-branch-row__delete" title="删除分支" @click="removeBranch(branch)">
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                    <path d="M3 4h10" stroke-linecap="round" />
                    <path d="M6 4V3.2c0-.7.5-1.2 1.2-1.2h1.6c.7 0 1.2.5 1.2 1.2V4" />
                    <path d="M5 6v7c0 .6.4 1 1 1h4c.6 0 1-.4 1-1V6" />
                  </svg>
                </button>
              </li>
            </ul>
          </section>
        </div>
      </div>
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

    <FolderPickerModal
      v-if="branchTask"
      :open="branchPickerOpen"
      :account-id="branchTask.account_id"
      :accounts="activeAccounts"
      :root-anchor="branchRootAnchor"
      title="选择分支目录"
      confirm-text="选择该分支"
      @close="branchPickerOpen = false"
      @resolve="onBranchFolderPicked"
    />
  </div>
</template>

<style scoped>
.settings {
  padding-bottom: 24px;
}

.strm-task-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.strm-task-panel :deep(.admin-stats-grid) {
  margin-bottom: 0;
}

.strm-task-table-wrap {
  overflow: visible;
}

.strm-task-table {
  table-layout: fixed;
}

.strm-task-row {
  position: relative;
}

.strm-task-row:hover {
  z-index: 30;
}

.strm-task-table th:nth-child(1),
.strm-task-table td:nth-child(1) {
  width: 14%;
}

.strm-task-table th:nth-child(2),
.strm-task-table td:nth-child(2) {
  width: 16%;
}

.strm-task-table th:nth-child(3),
.strm-task-table td:nth-child(3) {
  width: 48%;
  min-width: 280px;
}

.strm-task-table td:first-child,
.strm-task-table td:nth-child(2) {
  overflow: hidden;
  max-width: 0;
}

.strm-task-table td:nth-child(3) {
  overflow: hidden;
  min-width: 0;
}

.strm-task-table th:last-child,
.strm-task-table td:last-child {
  width: 22%;
  text-align: center;
}

.strm-task-row {
  position: relative;
  transition: background-color 0.18s ease;
}

.strm-task-row:hover {
  background: color-mix(in srgb, var(--brand) 3%, var(--surface));
  z-index: 20;
}

.strm-task-main {
  min-width: 0;
}

.strm-task-name {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.strm-task-name__text {
  flex: 0 1 auto;
  min-width: 0;
  font-weight: 700;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.strm-task-path {
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.strm-task-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 8px;
}

.strm-run-menu-wrap {
  position: relative;
  display: inline-flex;
}

.strm-run-menu-wrap::after {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  top: 100%;
  height: 14px;
  z-index: 24;
}

.strm-run-menu {
  position: absolute;
  right: 0;
  top: calc(100% + 12px);
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 112px;
  padding: 6px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  opacity: 0;
  pointer-events: none;
  transform: translateY(-4px);
  transition: opacity 0.16s ease, transform 0.16s ease;
  white-space: normal;
  z-index: 30;
}

.strm-run-menu-wrap:hover .strm-run-menu {
  opacity: 1;
  pointer-events: auto;
  transform: translateY(0);
}

.strm-run-menu button {
  display: block;
  width: 100%;
  box-sizing: border-box;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 13px;
  padding: 8px 10px;
  text-align: left;
  white-space: nowrap;
}

.strm-run-menu button:hover {
  background: var(--surface-sunken);
  color: var(--brand);
}

@media (max-width: 720px) {
  .strm-form__row,
  .strm-branch-columns {
    grid-template-columns: 1fr;
  }

  .strm-task-table__path-col {
    display: none;
  }

  .strm-task-table th,
  .strm-task-table td {
    padding: 10px 8px;
  }

  .strm-task-table th:nth-child(1),
  .strm-task-table td:nth-child(1) {
    width: 42%;
  }

  .strm-task-table th:nth-child(3),
  .strm-task-table td:nth-child(3) {
    width: auto;
    min-width: 160px;
  }

  .strm-task-table th:last-child,
  .strm-task-table td:last-child {
    width: 48px;
    text-align: right;
  }
}

.strm-form {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.strm-form--repair-active > :not(.strm-repair-overlay) {
  visibility: hidden;
  pointer-events: none;
}

.strm-repair-overlay {
  position: absolute;
  inset: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 16px;
  background: var(--surface);
}

.strm-repair-panel {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
  width: 100%;
  max-width: 420px;
  text-align: center;
}

.strm-repair-panel__message {
  margin: 0;
  font-size: 15px;
  line-height: 1.6;
  color: var(--text);
}

.strm-repair-panel__result {
  margin: 0;
  font-size: 15px;
  line-height: 1.6;
}

.strm-repair-panel__result--ok {
  color: var(--success);
}

.strm-repair-panel__result--fail {
  color: var(--warning, var(--text));
}

.strm-repair-panel__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  justify-content: center;
}

.strm-repair-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  min-height: 220px;
  padding: 32px 16px;
}

.strm-repair-loading__title {
  font-size: 15px;
  color: var(--text);
}

.strm-form__row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.strm-advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0;
  border: none;
  background: none;
  font-size: 13px;
  color: #94a3b8;
  cursor: pointer;
  user-select: none;
  transition: color 0.2s;
}

.strm-advanced-toggle:hover {
  color: #3b82f6;
}

.strm-advanced-toggle__icon {
  width: 16px;
  height: 16px;
  transition: transform 0.2s;
}

.strm-advanced-toggle__icon--open {
  transform: rotate(180deg);
}

.strm-branch-control {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 42px;
  align-items: stretch;
  min-height: 40px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--surface);
  transition: border-color 0.15s, box-shadow 0.15s;
}

.strm-branch-control--on {
  border-color: color-mix(in srgb, var(--brand) 35%, var(--border));
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--brand) 10%, transparent);
}

.strm-branch-toggle,
.strm-branch-edit {
  margin: 0;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}

.strm-branch-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  padding: 0 12px;
  text-align: left;
}

.strm-branch-toggle:hover {
  background: color-mix(in srgb, var(--surface-sunken) 70%, transparent);
}

.strm-branch-toggle__dot {
  position: relative;
  flex-shrink: 0;
  width: 28px;
  height: 16px;
  border-radius: 999px;
  background: var(--border-soft);
  transition: background 0.2s ease;
}

.strm-branch-toggle__dot::after {
  content: "";
  position: absolute;
  top: 2px;
  left: 2px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.18);
  transition: transform 0.2s ease;
}

.strm-branch-control--on .strm-branch-toggle__dot {
  background: var(--brand);
}

.strm-branch-control--on .strm-branch-toggle__dot::after {
  transform: translateX(12px);
}

.strm-branch-toggle__text {
  font-size: 13px;
  color: var(--text);
  line-height: 1;
}

.strm-branch-edit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-left: 1px solid var(--border-soft) !important;
}

.strm-branch-edit svg {
  width: 16px;
  height: 16px;
}

.strm-branch-edit:hover:not(:disabled) {
  color: var(--brand);
  background: color-mix(in srgb, var(--surface-sunken) 70%, transparent);
}

.strm-branch-edit:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.strm-branch-dialog {
  display: flex;
  flex-direction: column;
  gap: 14px;
  flex: 1;
  min-height: 0;
}

.strm-branch-add-card {
  padding: 14px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface);
}

.strm-branch-add-card__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.strm-branch-add-card__title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.strm-branch-add-card__tip {
  font-size: 12px;
  color: var(--text-muted);
  text-align: right;
}

.strm-branch-add-card__toolbar {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 10px;
  align-items: center;
}

.strm-branch-add-card__path {
  min-width: 0;
}

.strm-branch-retention-select {
  width: 100%;
  min-width: 0;
}

.strm-branch-columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
  flex: 1;
  min-height: 0;
}

.strm-branch-column {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 280px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface);
  overflow: hidden;
}

.strm-branch-column__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-soft);
  background: color-mix(in srgb, var(--surface-sunken) 65%, var(--surface));
}

.strm-branch-column__title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}

.strm-branch-column__badge {
  min-width: 22px;
  padding: 0 7px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--brand) 12%, var(--surface));
  color: var(--brand);
  font-size: 12px;
  font-weight: 600;
  text-align: center;
}

.strm-branch-column__desc,
.strm-branch-column__meta {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 46%;
}

.strm-branch-column__meta {
  padding: 2px 8px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--brand) 10%, transparent);
  color: var(--brand);
  font-weight: 500;
}

.strm-branch-column__meta--preview {
  background: color-mix(in srgb, var(--success) 12%, transparent);
  color: var(--success);
}

.strm-branch-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 28px 16px;
  text-align: center;
}

.strm-branch-empty p {
  margin: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-muted);
}

.strm-branch-empty span {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
  max-width: 220px;
}

.strm-branch-list {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow: auto;
  flex: 1;
  min-height: 0;
}

.strm-branch-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 112px auto;
  gap: 8px;
  align-items: center;
  min-height: 42px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--border-soft);
}

.strm-branch-row__slot {
  min-width: 0;
}

.strm-branch-row :deep(.select__trigger) {
  padding: 4px 8px;
  font-size: 12px;
  min-height: 32px;
}

.strm-branch-row:last-child {
  border-bottom: none;
}

.strm-branch-row__main {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.strm-branch-row__path {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.strm-branch-tag {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 4px;
  background: color-mix(in srgb, var(--info) 12%, var(--surface));
  color: var(--info);
  font-size: 11px;
  font-weight: 600;
}

.strm-branch-row__delete {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.strm-branch-row__delete svg {
  width: 16px;
  height: 16px;
}

.strm-branch-row__delete:hover {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  color: var(--danger);
}

</style>
