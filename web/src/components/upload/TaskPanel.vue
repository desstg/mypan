<template>
  <div ref="panelRoot" class="upload-task-panel" :class="{ 'is-expanded': panelExpanded }">
    <div class="upload-task-panel-header">
      <div class="panel-title">任务面板</div>
      <div class="panel-head-actions">
        <AppIconButton
          icon="settings"
          label="设置"
          variant="ghost"
          size="sm"
          title="设置"
          class="head-icon head-icon-btn"
          @click.stop="settingsOpen = !settingsOpen"
        />
        <button class="head-icon" type="button" title="说明" aria-label="说明" @click="openUploadHelp">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <circle cx="8" cy="8" r="5.4"></circle>
            <path d="M8 6.2V8.35"></path>
            <circle cx="8" cy="10.9" r="0.55" fill="currentColor" stroke="none"></circle>
          </svg>
        </button>
        <span class="head-divider" aria-hidden="true"></span>
        <button class="head-icon" type="button" :title="panelExpanded ? '还原' : '最大化'" :aria-label="panelExpanded ? '还原' : '最大化'" @click="panelExpanded = !panelExpanded">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <rect x="2.25" y="2.25" width="11.5" height="11.5" rx="1.75"></rect>
          </svg>
        </button>
        <button class="head-icon" type="button" title="关闭" aria-label="关闭" @click="closeUploadTaskPanel">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M3.5 3.5L12.5 12.5"></path>
            <path d="M12.5 3.5L3.5 12.5"></path>
          </svg>
        </button>
        <div class="panel-settings-wrap">
          <UploadTaskSettingsPanel
            :open="settingsOpen"
            :server-concurrency="uploadTaskServerConcurrency"
            @update:server-concurrency="onConcurrencyUpdated"
            @close="settingsOpen = false"
          />
        </div>
      </div>
    </div>

    <div class="upload-task-panel-body">
      <aside class="panel-sidebar">
        <div class="sidebar-nav">
          <div v-for="category in navCategories" :key="category.key" class="nav-group">
            <button
              type="button"
              class="nav-item"
              :class="{ 'is-active': taskPanelCategory === category.key }"
              @click="taskPanelCategory = category.key"
            >
              <span class="nav-label">
                <span class="nav-glyph">
                  <SvgIcon :name="category.icon" :size="14" />
                </span>
                <span class="nav-text">{{ category.label }}</span>
              </span>
              <span v-if="category.count > 0" class="nav-count">{{ category.count }}</span>
            </button>

            <div v-if="taskPanelCategory === category.key" class="nav-sub">
              <button
                v-for="state in category.states"
                :key="state.key"
                type="button"
                class="nav-sub-item"
                :class="{ 'is-active': state.active }"
                @click="state.onClick"
              >
                <span class="nav-sub-label">
                  <span class="nav-sub-text">{{ state.label }}</span>
                </span>
                <span v-if="state.count > 0" class="nav-sub-count">{{ state.count }}</span>
              </button>
            </div>
          </div>
        </div>
      </aside>

      <section class="panel-content">
        <div class="content-top">
          <label class="search-box">
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <circle cx="7" cy="7" r="4.5"></circle>
              <path d="M10.6 10.6L13 13"></path>
            </svg>
            <input v-model.trim="keyword" type="text" placeholder="搜索任务" />
          </label>

          <div class="top-actions">
            <button class="action-btn" type="button" :disabled="visibleRows.length === 0" @click="toggleSelectAll">
              {{ allVisibleSelected ? "清选" : "全选" }}
            </button>
            <button class="action-btn" type="button" :disabled="!canToggleSelected" @click="handleSelectedToggle">
              {{ selectedToggleLabel }}
            </button>
            <button class="action-btn warn" type="button" :disabled="!canDeleteSelected" @click="handleSelectedDelete">
              {{ deleteButtonLabel }}
            </button>
          </div>
        </div>

        <AppStateBlock
          v-if="showLoading"
          :message="loadingText"
          loading
          min-height="220px"
        />

        <template v-else>
          <div v-if="taskPanelCategory === 'upload' && currentBatchId" class="task-path-bar">
            <button type="button" class="task-path-back" @click="leaveTaskFolder()">上传列表</button>
            <span class="task-path-separator">/</span>
            <button type="button" class="task-path-crumb" @click="goTaskFolder('')">{{ currentBatchName }}</button>
            <template v-for="crumb in taskFolderCrumbs" :key="crumb.path">
              <span class="task-path-separator">/</span>
              <button type="button" class="task-path-crumb" @click="goTaskFolder(crumb.path)">{{ crumb.name }}</button>
            </template>
          </div>
          <div v-if="visibleRows.length > 0" class="table-head">
            <div>文件名</div>
            <div>{{ taskPanelCategory === "offline" ? "下载器" : "来源" }}</div>
            <div>状态</div>
            <div>{{ tailColumnLabel }}</div>
          </div>

          <div v-if="visibleRows.length > 0" ref="taskListRef" class="task-list" @scroll.passive="onTaskListScroll">
            <div v-if="virtualTopSpace > 0" class="task-virtual-space" :style="{ height: `${virtualTopSpace}px` }"></div>
            <div
              v-for="row in renderedRows"
              :key="row.id"
              class="task-row"
              :class="{ 'is-selected': selectedTaskIds.has(row.id) }"
              @click="handleRowClick($event, row.id)"
            >
              <div class="task-row-main">
                <div class="file-cell">
                  <span v-if="row.isFolder" class="folder-task-chip"><SvgIcon name="folder" :size="20" /></span>
                  <DriverIcon
                    v-else
                    class="driver-chip"
                    :logo="row.badgeLogo"
                    :color="row.badgeColor"
                    :name="row.badgeName"
                    :size="32"
                  />
                  <button
                    v-if="row.isFolder"
                    type="button"
                    class="file-name file-name-folder"
                    :disabled="row.isPreparing"
                    @click.stop="openTaskFolder(row)"
                  >
                    {{ row.name }}
                  </button>
                  <div v-else class="file-name">{{ row.name }}</div>
                </div>
                <div class="source-cell">{{ taskPanelCategory === "offline" ? row.provider || row.source : row.source }}</div>
                <div class="status-cell" :class="row.statusClass">
                  <span v-if="showStatusPulse(row)" class="status-pulse" :class="statusPulseClass(row)" aria-hidden="true"></span>
                  <span class="status-text">{{ row.status }}</span>
                </div>
                <div class="tail-cell" :class="{ 'is-active': row.tailActive }">
                  <button
                    v-if="row.actionLabel"
                    type="button"
                    class="row-action-btn"
                    @click.stop="handleRowAction(row)"
                  >
                    {{ row.actionLabel }}
                  </button>
                  <span v-else>{{ row.tail }}</span>
                </div>
              </div>
              <div v-if="row.showProgress" class="row-progress-line">
                <UploadProgressInner
                  v-if="useSmoothUploadProgress(row)"
                  :task="row.raw as UploadTask"
                  class="task-row-progress-inner"
                />
                <span v-else :class="row.progressClass" :style="{ width: `${clampProgress(row.progress)}%` }"></span>
              </div>
            </div>
            <div v-if="virtualBottomSpace > 0" class="task-virtual-space" :style="{ height: `${virtualBottomSpace}px` }"></div>
          </div>

          <AppStateBlock v-else :message="emptyText" min-height="220px" />

          <div v-if="currentRows.length > 0 && detailBarVisible" class="status-bar">
            <span>{{ detailPrimary }}</span>
            <span v-if="detailSecondary" class="accent">{{ detailSecondary }}</span>
            <span v-if="detailExtra">{{ detailExtra }}</span>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import DriverIcon from "@/components/driver/DriverIcon.vue";
import AppIconButton from "@/components/base/AppIconButton.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import UploadProgressInner from "@/components/upload/UploadProgressInner.vue";
import UploadTaskSettingsPanel from "@/components/upload/UploadTaskSettingsPanel.vue";
import { getUploadTaskStableKey } from "@/composables/upload/uploadTaskFormatters";
import { buildUploadTaskLevel, uploadTaskPathParts, type UploadTaskTreeNode } from "@/composables/upload/uploadTaskTree";
import { toast } from "@/composables/useToast";
import { formatSize } from "@/utils/format";
import type { UploadTask } from "@/types/upload";
import type { useUploadTasks } from "@/composables/useUploadTasks";
import type { useOfflineDownloads } from "@/composables/useOfflineDownloads";

type CategoryKey = "upload" | "relay" | "offline";
type StateKey = "active" | "done" | "failed";
type RelayStateKey = "active" | "failed";

type PanelRow = {
  id: string;
  kind: CategoryKey;
  raw: any;
  name: string;
  source: string;
  provider?: string;
  status: string;
  statusDetail: string;
  statusClass: string;
  tail: string;
  tailActive: boolean;
  progress: number;
  progressClass: string;
  showProgress: boolean;
  actionLabel?: string;
  sortOrder: number;
  badgeLogo: string;
  badgeName: string;
  badgeColor: string;
  searchText: string;
  state: StateKey;
  isFolder?: boolean;
  batchId?: string;
  folderPath?: string;
  tasks?: UploadTask[];
  isPreparing?: boolean;
};

const props = defineProps<{
  uploadApi: ReturnType<typeof useUploadTasks>;
  offline: ReturnType<typeof useOfflineDownloads>;
}>();

const api = props.uploadApi;
const offline = props.offline;

const {
  displayUploadTasks,
  uploadTaskPanelLoading,
  uploadTaskPanelLoadingText,
  getUploadTaskDriverBadge,
  getUploadTaskDisplayStatus,
  getUploadTaskPhaseLabel,
  getUploadTaskSpeedText,
  getUploadTaskStatusText,
  handleDeleteUploadTasks,
  handleUploadTaskPrimaryAction,
  handleToggleUploadTasks,
  openUploadNoticeFromPanel,
  closeUploadTaskPanel,
  refreshUploadTaskServerConcurrency,
  formatUploadPart,
  getRelayTaskDriverBadge,
  handleDeleteRelayTasks,
  formatRelaySpeed,
  formatRelayPart,
} = api;

const taskPanelCategory = computed({
  get: () => api.taskPanelCategory.value as CategoryKey,
  set: (v: CategoryKey) => {
    api.taskPanelCategory.value = v;
  },
});

const uploadTaskServerConcurrency = computed({
  get: () => api.uploadTaskServerConcurrency.value as number,
  set: (v: number) => {
    api.uploadTaskServerConcurrency.value = v;
  },
});

const keyword = ref("");
const settingsOpen = ref(false);
const panelExpanded = ref(false);
const selectedTaskIds = ref<Set<string>>(new Set());
const uploadStateFilter = ref<StateKey>("active");
const relayStateFilter = ref<RelayStateKey>("active");
const offlineStateFilter = ref<StateKey>("active");
const panelRoot = ref<HTMLElement | null>(null);
const taskListRef = ref<HTMLElement | null>(null);
const taskListScrollTop = ref(0);
const taskListViewportHeight = ref(480);
const virtualRowHeight = ref(53);
const currentBatchId = ref("");
const currentBatchName = ref("");
const currentFolderPath = ref("");

function useFullscreenPanelLayout() {
  if (typeof window === "undefined") return false;
  return panelExpanded.value || window.innerWidth <= 768;
}

function updatePanelLeft() {
  if (typeof window === "undefined") return;
  if (useFullscreenPanelLayout()) {
    if (panelRoot.value) panelRoot.value.style.left = "";
    return;
  }
  if (panelRoot.value) {
    panelRoot.value.style.left = `${document.documentElement.clientWidth / 2}px`;
  }
}

function onWindowResize() {
  updatePanelLeft();
  updateTaskListViewport();
}

function openUploadHelp() {
  settingsOpen.value = false;
  if (panelRoot.value && !useFullscreenPanelLayout()) {
    const rect = panelRoot.value.getBoundingClientRect();
    panelRoot.value.style.left = `${rect.left + rect.width / 2}px`;
  }
  openUploadNoticeFromPanel();
}

function onConcurrencyUpdated(value: number) {
  uploadTaskServerConcurrency.value = value;
}

function onDocumentClick(event: MouseEvent) {
  if (!settingsOpen.value) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest(".panel-head-actions")) return;
  if (target?.closest(".overlay--nested")) return;
  settingsOpen.value = false;
}

const uploadTasks = computed<UploadTask[]>(() =>
  (displayUploadTasks?.value || []).filter(
    (task: UploadTask) => !(task.source_type === "cross_transfer" && task.phase === "downloading"),
  ),
);
const relayTasks = computed<UploadTask[]>(() => api.relayTasks.value || []);
const offlineTasks = computed<any[]>(() => offline.tasks?.value || []);

function uploadStateOf(task: UploadTask): StateKey {
  const status = uploadDisplayStatus(task);
  if (status === "success" || status === "skipped") return "done";
  if (status === "failed" || status === "canceled") return "failed";
  return "active";
}

function relayStateOf(task: UploadTask): StateKey {
  const status = relayDisplayStatus(task);
  if (status === "failed" || status === "canceled") return "failed";
  return "active";
}

function offlineStateOf(task: any): StateKey {
  if (task.status === "success") return "done";
  if (task.status === "failed") return "failed";
  return "active";
}

function uploadStatusLabel(task: UploadTask) {
  const displayStatus = typeof getUploadTaskDisplayStatus === "function" ? getUploadTaskDisplayStatus(task) : task.status;
  const progress = preciseUploadProgress(task).toFixed(1);
  const message = String(task.message || "").trim();
  const stageLabel = uploadStageLabel(message);
  const phaseLabel = typeof getUploadTaskPhaseLabel === "function" ? getUploadTaskPhaseLabel(task) : "";
  if (displayStatus === "running") {
    if (stageLabel) return stageLabel;
    return `上传中 ${progress}%`;
  }
  if (displayStatus === "paused") return progress !== "0.0" ? `已暂停 ${progress}%` : "已暂停";
  if (displayStatus === "pending") {
    if (isLocalDispatchMessage(message)) return stageLabel || message;
    if (phaseLabel && phaseLabel !== "等待继续") return phaseLabel;
    return "等待上传";
  }
  if (displayStatus === "failed") return progress !== "0.0" ? `失败 ${progress}%` : "失败";
  return getUploadTaskStatusText(displayStatus);
}

function isLocalDispatchMessage(message: string) {
  if (!message) return false;
  if (message.includes("投递到 MyPan 服务器")) return true;
  if (message.includes("投递成功")) return true;
  if (message.includes("创建任务中")) return true;
  return false;
}

function isUploadStageMessage(message: string) {
  if (!message) return false;
  if (message.includes("SHA-256")) return true;
  if (message.includes("校验")) return true;
  if (message.includes("发起上传")) return true;
  if (message.includes("准备上传")) return true;
  if (message.includes("预上传")) return true;
  if (message.includes("获取上传凭证")) return true;
  if (message.includes("创建上传会话")) return true;
  if (message.includes("完成上传")) return true;
  if (message.includes("写入网盘")) return true;
  return false;
}

function uploadStageLabel(message: string) {
  if (!message) return "";
  if (message.includes("投递成功") || message.includes("创建任务中")) return "创建任务中";
  if (message.includes("投递到 MyPan 服务器")) return message;
  if (message.includes("SHA-256")) return "计算校验中";
  if (message.includes("校验")) return "校验中";
  if (message.includes("发起上传") || message.includes("准备上传") || message.includes("预上传")) return "准备上传中";
  if (message.includes("获取上传凭证") || message.includes("创建上传会话")) return "获取凭证中";
  if (message.includes("完成上传") || message.includes("写入网盘")) return "提交上传中";
  return "";
}

function relayStatusLabel(task: UploadTask, status = String(task.status || "")) {
  if (status === "running") return `跨盘下载中（${clampProgress(task.progress || 0).toFixed(1)}%）`;
  if (status === "pending") return "等待下载";
  if (status === "paused") return "已暂停";
  if (status === "failed") return "下载失败";
  if (status === "canceled") return "已取消";
  return "等待下载";
}

function uploadDisplayStatus(task: UploadTask) {
  return String(typeof getUploadTaskDisplayStatus === "function" ? getUploadTaskDisplayStatus(task) : task.status || "");
}

function uploadResumePending(task: UploadTask) {
  if (uploadDisplayStatus(task) !== "pending") return false;
  const phaseLabel = typeof getUploadTaskPhaseLabel === "function" ? getUploadTaskPhaseLabel(task) : "";
  if (phaseLabel === "等待继续") return true;
  return String(task.message || "").includes("准备继续");
}

function relayDisplayStatus(task: UploadTask) {
  return uploadDisplayStatus(task);
}

function relayResumePending(task: UploadTask) {
  return uploadResumePending(task);
}

function relayStatusClass(task: UploadTask) {
  const status = relayDisplayStatus(task);
  if (status === "running") return "downloading";
  if (status === "pending") return "pending";
  if (status === "paused") return "paused";
  if (status === "failed") return "failed";
  if (status === "canceled") return "canceled";
  return "downloaded";
}

function stripChunkDetail(message: string) {
  return String(message || "")
    .replace(/[，,、]\s*分片[（(]\s*\d+\s*\/\s*\d+\s*[)）]\s*$/u, "")
    .replace(/\s*分片[（(]\s*\d+\s*\/\s*\d+\s*[)）]\s*$/u, "")
    .trim();
}

function uploadStatusDetail(task: UploadTask) {
  const details: string[] = [];
  if (task.error) details.push(String(task.error));
  const message = stripChunkDetail(String(task.message || "").trim());
  if (message && !details.includes(message)) details.push(message);
  const part = details.length === 0 && typeof formatUploadPart === "function" ? formatUploadPart(task) : "";
  if (part && !details.includes(part)) details.push(part);
  return details.join(" · ");
}

function relayStatusDetail(task: UploadTask) {
  const details: string[] = [];
  if (task.error) details.push(String(task.error));
  const message = stripChunkDetail(String(task.message || "").trim());
  if (message && !details.includes(message)) details.push(message);
  const part = details.length === 0 && typeof formatRelayPart === "function" ? formatRelayPart(task) : "";
  if (part && !details.includes(part)) details.push(part);
  return details.join(" · ");
}

function buildUploadRow(task: UploadTask): PanelRow {
  const badge = getUploadTaskDriverBadge(task);
  const completed = uploadStateOf(task) === "done";
  const displayStatus = uploadDisplayStatus(task);
  const retryable = displayStatus === "failed" || displayStatus === "canceled";
  const speed = getUploadTaskSpeedText(task) || "---";
  const progress = preciseUploadProgress(task);
  const message = String(task.message || "").trim();
  const localDispatching = displayStatus === "pending" && isLocalDispatchMessage(message);
  const stageRunning = displayStatus === "running" && isUploadStageMessage(message);
  return {
    id: getUploadTaskStableKey(task),
    kind: "upload",
    raw: task,
    name: task.file_name,
    source:
      task.source_type === "cross_transfer"
        ? "跨盘接棒"
        : task.source_type === "offline_handoff"
          ? "离线接棒"
          : task.source_type === "server_local"
            ? "服务器上传"
            : "手动上传",
    status: uploadStatusLabel(task),
    statusDetail: uploadStatusDetail(task),
    statusClass: displayStatus,
    tail: completed ? "" : speed,
    tailActive: !completed && speed !== "---",
    progress,
    progressClass: "uploading",
    showProgress: (displayStatus === "running" && progress > 0 && !stageRunning) || (displayStatus === "pending" && progress > 0 && !localDispatching),
    actionLabel: completed ? "打开" : retryable ? "重试" : undefined,
    sortOrder: taskOrder(task),
    badgeLogo: badge.logo || "",
    badgeName: String(badge.name || "网盘").slice(0, 2),
    badgeColor: badge.color || "#4b63e9",
    searchText: [task.file_name, task.account_name, task.target_display_path, task.message, task.error].join(" "),
    state: uploadStateOf(task),
  };
}

function batchDisplayStatus(tasks: UploadTask[]) {
  const statuses = tasks.map(uploadDisplayStatus);
  if (statuses.some((status) => status === "running")) return "running";
  if (statuses.some((status) => status === "pending")) return "pending";
  if (statuses.some((status) => status === "paused")) return "paused";
  if (statuses.some((status) => status === "failed" || status === "canceled")) return "failed";
  return "success";
}

function buildUploadNodeRow(node: UploadTaskTreeNode): PanelRow {
  if (!node.isFolder && node.tasks.length === 1) {
    const row = buildUploadRow(node.tasks[0]);
    row.id = node.id;
    row.name = node.name;
    row.tasks = node.tasks;
    return row;
  }
  const tasks = node.tasks;
  const representative = tasks[0];
  const badge = getUploadTaskDriverBadge(representative);
  const preparing = tasks.find((task) => task.batch_placeholder);
  if (preparing) {
    const failed = uploadStateOf(preparing) === "failed";
    return {
      id: node.id,
      kind: "upload",
      raw: preparing,
      name: node.name,
      source: "文件夹上传",
      status: failed ? "准备失败" : "正在准备",
      statusDetail: String(preparing.error || preparing.message || "正在准备远端目录"),
      statusClass: failed ? "error" : "pending",
      tail: "---",
      tailActive: false,
      progress: Number(preparing.progress || 0),
      progressClass: "uploading",
      showProgress: !failed && Number(preparing.progress || 0) > 0,
      sortOrder: taskOrder(preparing),
      badgeLogo: badge.logo || "",
      badgeName: "夹",
      badgeColor: badge.color || "#4b63e9",
      searchText: [node.name, preparing.message, preparing.error].join(" "),
      state: failed ? "failed" : "active",
      isFolder: true,
      isPreparing: true,
      batchId: node.batchId,
      folderPath: node.path,
      tasks,
    };
  }
  const statusClass = batchDisplayStatus(tasks);
  const done = tasks.filter((task) => uploadStateOf(task) === "done").length;
  const skipped = tasks.filter((task) => String(task.status) === "skipped").length;
  const failed = tasks.filter((task) => uploadStateOf(task) === "failed").length;
  const totalBytes = tasks.reduce((sum, task) => sum + Math.max(0, Number(task.total_bytes || 0)), 0);
  const transferred = tasks.reduce((sum, task) => sum + Math.max(0, uploadTransferredBytes(task)), 0);
  const fallbackProgress = tasks.reduce((sum, task) => sum + preciseUploadProgress(task), 0) / Math.max(1, tasks.length);
  const progress = totalBytes > 0 ? clampProgress((transferred * 100) / totalBytes) : fallbackProgress;
  const speed = tasks.reduce((sum, task) => sum + Math.max(0, Number(task.speed_bytes_per_second || 0)), 0);
  const state = tasks.some((task) => uploadStateOf(task) === "active")
    ? "active"
    : tasks.some((task) => uploadStateOf(task) === "failed")
      ? "failed"
      : "done";
  const status = state === "done"
    ? skipped === tasks.length
      ? `已跳过 ${skipped}/${tasks.length}`
      : skipped > 0
        ? `完成 ${done - skipped} · 跳过 ${skipped}/${tasks.length}`
        : `已完成 ${done}/${tasks.length}`
    : state === "failed"
      ? `失败 ${failed} · 完成 ${done}/${tasks.length}`
      : `传输中 ${done}/${tasks.length}`;
  return {
    id: node.id,
    kind: "upload",
    raw: representative,
    name: node.name,
    source: representative.source_type === "server_local" ? "服务器上传" : "文件夹上传",
    status,
    statusDetail: `${tasks.length} 个文件${failed > 0 ? ` · ${failed} 个失败` : ""}`,
    statusClass,
    tail: state === "done" ? "" : speed > 0 ? `${formatSize(speed)}/s` : "---",
    tailActive: state !== "done" && speed > 0,
    progress,
    progressClass: "uploading",
    showProgress: state === "active" && progress > 0,
    sortOrder: tasks.reduce((min, task) => Math.min(min, taskOrder(task)), Number.MAX_SAFE_INTEGER),
    badgeLogo: badge.logo || "",
    badgeName: "夹",
    badgeColor: badge.color || "#4b63e9",
    searchText: node.name,
    state,
    isFolder: true,
    batchId: node.batchId,
    folderPath: node.path,
    tasks,
  };
}

function buildRelayRow(task: UploadTask): PanelRow {
  const badge = getRelayTaskDriverBadge(task);
  const speed = formatRelaySpeed(task) || "---";
  const status = relayDisplayStatus(task);
  return {
    id: task.task_id,
    kind: "relay",
    raw: task,
    name: task.file_name,
    source: task.source_account_name || "源盘",
    status: relayStatusLabel(task, status),
    statusDetail: relayStatusDetail(task),
    statusClass: relayStatusClass(task),
    tail: speed,
    tailActive: speed !== "---",
    progress: Number(task.progress || 0),
    progressClass: "downloading",
    showProgress: ["pending", "running", "paused"].includes(status) && Number(task.progress || 0) > 0,
    sortOrder: relayTaskOrder(task),
    badgeLogo: badge.logo || "",
    badgeName: String(badge.name || "网盘").slice(0, 2),
    badgeColor: badge.color || "#7b8697",
    searchText: [task.file_name, task.source_account_name, task.account_name, task.target_display_path, task.message, task.error].join(" "),
    state: relayStateOf(task),
  };
}

function buildOfflineRow(task: any): PanelRow {
  const badge = getUploadTaskDriverBadge(task);
  const speedText = offline.speedText(task);
  const detailText = offline.detailText(task);
  return {
    id: task.task_id,
    kind: "offline",
    raw: task,
    name: task.name,
    source: offline.sourceLabel(task),
    provider: offline.providerLabel(task),
    status: offline.statusText(task),
    statusDetail: detailText,
    statusClass: task.status === "success" ? "success" : task.status === "failed" ? "error" : "downloading",
    tail: speedText,
    tailActive: speedText !== "-",
    progress: Number(task.progress || 0),
    progressClass: task.status === "failed" ? "error" : "downloading",
    showProgress: ["pending", "running", "retrying"].includes(task.status),
    actionLabel: task.status === "success" && task.provider_kind !== "builtin" ? "打开" : undefined,
    sortOrder: offlineTaskOrder(task),
    badgeLogo: badge.logo || "",
    badgeName: String(badge.name || "任务").slice(0, 2),
    badgeColor: task.provider_kind === "builtin" ? "#4f7cff" : (badge.color || "#5d6673"),
    searchText: [task.name, task.source, task.target_display_path, task.error, offline.providerLabel(task), detailText].join(" "),
    state: offlineStateOf(task),
  };
}

const uploadRootRows = computed(() => buildUploadTaskLevel(uploadTasks.value).map(buildUploadNodeRow));
const uploadRows = computed(() =>
  currentBatchId.value
    ? buildUploadTaskLevel(uploadTasks.value, currentBatchId.value, currentFolderPath.value).map(buildUploadNodeRow)
    : uploadRootRows.value,
);
const relayRows = computed(() => relayTasks.value.map(buildRelayRow));
const offlineRows = computed(() => offlineTasks.value.map(buildOfflineRow));

function stateFilterOf(category: CategoryKey) {
  if (category === "upload") return uploadStateFilter.value;
  if (category === "relay") return relayStateFilter.value;
  return offlineStateFilter.value;
}

const baseRows = computed(() => {
  if (taskPanelCategory.value === "upload") return uploadRows.value;
  if (taskPanelCategory.value === "relay") return relayRows.value;
  return offlineRows.value;
});

const currentRows = computed(() =>
  [...baseRows.value]
    .filter((row) => {
      const filter = stateFilterOf(taskPanelCategory.value);
      if (taskPanelCategory.value === "upload") {
        return row.state === filter;
      }
      if (taskPanelCategory.value === "relay" && relayStateOf(row.raw) !== filter) return false;
      if (taskPanelCategory.value === "offline") {
        if (offlineStateOf(row.raw) !== filter) return false;
      }
      return true;
    })
    .sort((a, b) => a.sortOrder - b.sortOrder),
);

const visibleRows = computed(() => {
  const query = keyword.value.toLowerCase();
  if (!query) return currentRows.value;
  return currentRows.value.filter((row) => row.searchText.toLowerCase().includes(query));
});

const virtualOverscan = 8;
const virtualStart = computed(() => Math.max(0, Math.floor(taskListScrollTop.value / virtualRowHeight.value) - virtualOverscan));
const virtualCount = computed(() => Math.ceil(taskListViewportHeight.value / virtualRowHeight.value) + virtualOverscan * 2);
const renderedRows = computed(() => visibleRows.value.slice(virtualStart.value, virtualStart.value + virtualCount.value));
const virtualTopSpace = computed(() => virtualStart.value * virtualRowHeight.value);
const virtualBottomSpace = computed(() =>
  Math.max(0, (visibleRows.value.length - virtualStart.value - renderedRows.value.length) * virtualRowHeight.value),
);

function updateTaskListViewport() {
  taskListViewportHeight.value = taskListRef.value?.clientHeight || 480;
  virtualRowHeight.value = typeof window !== "undefined" && window.innerWidth <= 768 ? 82 : 53;
}

function onTaskListScroll() {
  taskListScrollTop.value = taskListRef.value?.scrollTop || 0;
}

watch(visibleRows, (rows) => {
  const visibleIds = new Set(rows.map((row) => row.id));
  selectedTaskIds.value = new Set([...selectedTaskIds.value].filter((id) => visibleIds.has(id)));
  requestAnimationFrame(updateTaskListViewport);
}, { immediate: true });

watch(taskPanelCategory, () => {
  if (taskPanelCategory.value !== "upload") leaveTaskFolder();
  requestAnimationFrame(updateTaskListViewport);
});

watch(uploadStateFilter, () => {
  selectedTaskIds.value = new Set();
  taskListScrollTop.value = 0;
  if (taskListRef.value) taskListRef.value.scrollTop = 0;
  requestAnimationFrame(updateTaskListViewport);
});

const selectedRows = computed(() => visibleRows.value.filter((row) => selectedTaskIds.value.has(row.id)));
const allVisibleSelected = computed(() => visibleRows.value.length > 0 && visibleRows.value.every((row) => selectedTaskIds.value.has(row.id)));
const detailRow = computed(() => (selectedRows.value.length === 1 ? selectedRows.value[0] : null));
const detailBarVisible = computed(() => Boolean(detailRow.value));

function resolveUploadTaskForRow(row: PanelRow): UploadTask | null {
  if (row.kind === "upload") return row.raw as UploadTask;
  if (row.kind === "relay") {
    return row.raw as UploadTask;
  }
  return null;
}

function uploadTasksForRow(row: PanelRow) {
  if (row.kind !== "upload" && row.kind !== "relay") return [];
  return row.tasks?.length ? row.tasks : [row.raw as UploadTask];
}

function focusUploadTask(task: UploadTask) {
  taskPanelCategory.value = "upload";
  uploadStateFilter.value = uploadStateOf(task);
  currentBatchId.value = "";
  currentBatchName.value = "";
  currentFolderPath.value = "";
  selectedTaskIds.value = new Set([`task:${task.task_id}`]);
}

const selectedToggleTasks = computed(() =>
  selectedRows.value
    .flatMap(uploadTasksForRow)
    .filter((task): task is UploadTask => {
      if (!task) return false;
      if (task.batch_placeholder) return false;
      return ["pending", "running", "paused", "failed", "canceled"].includes(uploadDisplayStatus(task));
    }),
);

const canToggleSelected = computed(() => {
  if (!selectedToggleTasks.value.length) return false;
  return taskPanelCategory.value !== "offline";
});

const selectedToggleLabel = computed(() => {
  if (!selectedToggleTasks.value.length) return "暂停";
  return selectedToggleTasks.value.every((task) => ["paused", "failed", "canceled"].includes(uploadDisplayStatus(task))) ? "继续" : "暂停";
});

const tailColumnLabel = computed(() => {
  if (taskPanelCategory.value === "upload" && uploadStateFilter.value === "done") return "操作";
  if (taskPanelCategory.value === "upload" && uploadStateFilter.value === "failed") return "操作";
  if (taskPanelCategory.value === "offline" && offlineStateFilter.value === "done") return "操作";
  if (taskPanelCategory.value === "offline") return "速度";
  return "速度";
});

const emptyText = computed(() => {
  if (taskPanelCategory.value === "upload") {
    return uploadStateFilter.value === "done" ? "暂无已完成上传任务" : uploadStateFilter.value === "failed" ? "暂无失败上传任务" : "暂无进行中的上传任务";
  }
  if (taskPanelCategory.value === "relay") {
    return relayStateFilter.value === "failed" ? "暂无失败跨盘任务" : "暂无进行中的跨盘任务";
  }
  return offlineStateFilter.value === "done" ? "暂无已完成离线任务" : offlineStateFilter.value === "failed" ? "暂无失败离线任务" : "暂无进行中的离线任务";
});

const showLoading = computed(() =>
  (taskPanelCategory.value === "upload" && uploadTaskPanelLoading?.value) ||
  (taskPanelCategory.value === "offline" && offline.loading?.value),
);
const loadingText = computed(() =>
  taskPanelCategory.value === "upload" ? uploadTaskPanelLoadingText?.value || "正在加载上传任务..." : "正在加载离线任务...",
);

function countByState(category: CategoryKey, state: StateKey) {
  if (category === "upload") return uploadRootRows.value.filter((row) => row.state === state).length;
  const rows = category === "relay" ? relayRows.value : offlineRows.value;
  return rows.filter((row) => {
    if (category === "relay") return relayStateOf(row.raw) === state;
    return offlineStateOf(row.raw) === state;
  }).length;
}

const navCategories = computed(() => [
  {
    key: "upload" as const,
    label: "上传列表",
    icon: "upload",
    count: countByState("upload", "active"),
    states: [
      { key: "active", label: "进行中", count: countByState("upload", "active"), active: uploadStateFilter.value === "active", onClick: () => { uploadStateFilter.value = "active"; } },
      { key: "done", label: "已完成", count: countByState("upload", "done"), active: uploadStateFilter.value === "done", onClick: () => { uploadStateFilter.value = "done"; } },
      { key: "failed", label: "失　败", count: countByState("upload", "failed"), active: uploadStateFilter.value === "failed", onClick: () => { uploadStateFilter.value = "failed"; } },
    ],
  },
  {
    key: "relay" as const,
    label: "跨盘下载",
    icon: "relay",
    count: countByState("relay", "active"),
    states: [
      { key: "active", label: "进行中", count: countByState("relay", "active"), active: relayStateFilter.value === "active", onClick: () => { relayStateFilter.value = "active"; } },
      { key: "failed", label: "失　败", count: countByState("relay", "failed"), active: relayStateFilter.value === "failed", onClick: () => { relayStateFilter.value = "failed"; } },
    ],
  },
  {
    key: "offline" as const,
    label: "离线任务",
    icon: "cloud",
    count: countByState("offline", "active"),
    states: [
      { key: "active", label: "进行中", count: countByState("offline", "active"), active: offlineStateFilter.value === "active", onClick: () => { offlineStateFilter.value = "active"; } },
      { key: "done", label: "已完成", count: countByState("offline", "done"), active: offlineStateFilter.value === "done", onClick: () => { offlineStateFilter.value = "done"; } },
      { key: "failed", label: "失　败", count: countByState("offline", "failed"), active: offlineStateFilter.value === "failed", onClick: () => { offlineStateFilter.value = "failed"; } },
    ],
  },
]);

const detailPrimary = computed(() => {
  const row = detailRow.value;
  if (!row) return "";
  if (row.statusDetail) return row.statusDetail;
  return row.status;
});

const detailSecondary = computed(() => {
  const row = detailRow.value;
  if (!row) return "";
  if (row.kind === "offline") {
    return row.tail && row.tail !== "---" ? row.tail : "";
  }
  return row.tailActive && row.tail !== "---" ? row.tail : "";
});

const detailExtra = computed(() => {
  const row = detailRow.value;
  if (!row) return "";
  const details: string[] = [];
  if (row.kind === "upload") {
    if (row.isFolder) return `${row.tasks?.length || 0} 个文件`;
    const task = row.raw as UploadTask;
    const part = typeof formatUploadPart === "function" ? formatUploadPart(task) : "";
    if (part) details.push(part);
    const total = Number(task.total_bytes || 0);
    const transferred = uploadTransferredBytes(task);
    if (total > 0 && transferred > 0) {
      details.push(`${formatSize(transferred)} / ${formatSize(total)}`);
    }
    return details.join(" · ");
  }
  if (row.kind === "relay") {
    const targetPath = String(row.raw?.target_display_path || "").trim();
    if (targetPath) details.push(`目标 ${targetPath}`);
    const part = typeof formatRelayPart === "function" ? formatRelayPart(row.raw) : "";
    if (part) details.push(part);
    const downloaded = Number(row.raw?.downloaded_bytes || 0);
    const total = Number(row.raw?.total_bytes || 0);
    if (total > 0 && downloaded > 0) {
      details.push(`${formatSize(downloaded)} / ${formatSize(total)}`);
    }
    return details.join(" · ");
  }
  if (row.kind === "offline") {
    const task = row.raw;
    const downloaded = Number(task?.downloaded_bytes || 0);
    const total = Number(task?.size || 0);
    if (task?.provider_kind === "builtin" && total > 0) {
      details.push(`${formatSize(downloaded)} / ${formatSize(total)}`);
    } else if (total > 0) {
      details.push(`大小 ${formatSize(total)}`);
    }
    return details.join(" · ");
  }
  return details.join(" · ");
});

function handleRowClick(event: MouseEvent, rowId: string) {
  if (event.metaKey || event.ctrlKey) {
    const next = new Set(selectedTaskIds.value);
    if (next.has(rowId)) next.delete(rowId);
    else next.add(rowId);
    selectedTaskIds.value = next;
    return;
  }
  selectedTaskIds.value = new Set([rowId]);
}

function openTaskFolder(row: PanelRow) {
  if (!row.isFolder || !row.batchId || row.isPreparing) return;
  currentBatchId.value = row.batchId;
  if (!currentBatchName.value) currentBatchName.value = String(row.raw?.batch_name || row.name || "文件夹上传");
  currentFolderPath.value = row.folderPath || "";
  selectedTaskIds.value = new Set();
  keyword.value = "";
  requestAnimationFrame(() => {
    if (taskListRef.value) taskListRef.value.scrollTop = 0;
    onTaskListScroll();
  });
}

function leaveTaskFolder() {
  if (!currentBatchId.value) return;
  currentFolderPath.value = "";
  selectedTaskIds.value = new Set();
  currentBatchId.value = "";
  currentBatchName.value = "";
}

function goTaskFolder(path: string) {
  currentFolderPath.value = path;
  selectedTaskIds.value = new Set();
  requestAnimationFrame(() => {
    if (taskListRef.value) taskListRef.value.scrollTop = 0;
    onTaskListScroll();
  });
}

const taskFolderCrumbs = computed(() => uploadTaskPathParts(currentFolderPath.value));

function showStatusPulse(row: PanelRow) {
  if (row.kind === "upload") {
    return row.statusClass === "pending" || row.statusClass === "running";
  }
  if (row.kind === "relay") {
    return row.statusClass === "pending" || row.statusClass === "downloading";
  }
  if (row.kind === "offline") {
    return ["pending", "running", "retrying"].includes(String(row.raw?.status || ""));
  }
  return false;
}

function statusPulseClass(row: PanelRow) {
  if (row.kind === "upload" && row.statusClass === "pending") {
    return "is-uploading";
  }
  return "";
}

function toggleSelectAll() {
  if (allVisibleSelected.value) {
    selectedTaskIds.value = new Set();
    return;
  }
  selectedTaskIds.value = new Set(visibleRows.value.map((row) => row.id));
}

function canDeleteOfflineTask(task: any) {
  if (task?.provider_kind === "builtin") return true;
  const status = String(task?.status || "");
  const active = ["pending", "running", "retrying"].includes(status);
  return !active || Boolean(task?.remote_delete);
}

const deletableSelectedRows = computed(() => {
  if (taskPanelCategory.value !== "offline") return selectedRows.value;
  return selectedRows.value.filter((row) => canDeleteOfflineTask(row.raw));
});

const canDeleteSelected = computed(() => deletableSelectedRows.value.length > 0);
const deleteButtonLabel = computed(() => {
  if (taskPanelCategory.value !== "offline") return "删除";
  const total = selectedRows.value.length;
  const deletable = deletableSelectedRows.value.length;
  if (!total || deletable === total) return "删除";
  if (deletable === 0) return "删除";
  return `删除(${deletable}/${total})`;
});

async function handleSelectedToggle() {
  const tasks = [...selectedToggleTasks.value];
  if (!tasks.length) return;
  if (taskPanelCategory.value === "upload") {
    await handleToggleUploadTasks(tasks);
    return;
  }
  for (const task of tasks) await handleUploadTaskPrimaryAction(task);
}

async function handleSelectedDelete() {
  const rows = [...selectedRows.value];
  if (!rows.length) return;
  if (taskPanelCategory.value === "upload") {
    const tasks = rows.flatMap(uploadTasksForRow);
    const unique = [...new Map(tasks.map((task) => [task.task_id, task])).values()];
    const preferBatchRootDelete =
      !currentBatchId.value &&
      rows.every((row) => row.isFolder && Boolean(row.batchId) && !row.folderPath && !row.isPreparing);
    await handleDeleteUploadTasks(unique, {
      preferBatchRootDelete,
      folderTaskCount: preferBatchRootDelete ? rows.length : 0,
    });
  } else if (taskPanelCategory.value === "relay") {
    await handleDeleteRelayTasks(rows.map((row) => row.id));
  } else {
    const deletableRows = rows.filter((row) => canDeleteOfflineTask(row.raw));
    if (!deletableRows.length) {
      toast.info("当前选中的离线任务里，没有可删除或可取消的项目");
      return;
    }
    await offline.deleteTasks(deletableRows.map((row) => row.raw));
    if (deletableRows.length < rows.length) {
      toast.info(`已跳过 ${rows.length - deletableRows.length} 个当前网盘不支持取消的进行中离线任务`);
    }
  }
  selectedTaskIds.value = new Set();
}

async function handleRowAction(row: PanelRow) {
  const task = resolveUploadTaskForRow(row);
  if (task) {
    if (row.kind === "relay" && row.raw?.phase !== "downloading") {
      focusUploadTask(task);
      return;
    }
    await handleUploadTaskPrimaryAction(task);
    return;
  }
  if (row.kind === "offline" && typeof offline.handlePrimaryAction === "function") {
    const opened = await offline.handlePrimaryAction(row.raw);
    if (opened) {
      closeUploadTaskPanel();
    }
  }
}

function clampProgress(value: number) {
  return Math.max(0, Math.min(100, Number(value || 0)));
}

function uploadTransferredBytes(task: UploadTask) {
  if (task.source_type === "cross_transfer") {
    if (task.phase === "downloading") {
      return Number(task.downloaded_bytes || 0);
    }
    return Number(task.uploaded_bytes || 0);
  }
  return Number(task.uploaded_bytes || 0);
}

function useSmoothUploadProgress(row: PanelRow) {
  if (row.kind !== "upload") return false;
  if (row.isFolder) return false;
  const task = row.raw as UploadTask;
  if (task.source_type === "cross_transfer") return false;
  return Number(task.total_bytes || 0) > 0 && ["pending", "running", "paused"].includes(task.status);
}

function preciseUploadProgress(task: UploadTask) {
  const total = Number(task.total_bytes || 0);
  if (total > 0) {
    const transferred = uploadTransferredBytes(task);
    if (transferred > 0) {
      return clampProgress((transferred * 100) / total);
    }
  }
  return clampProgress(Number(task.progress || 0));
}

function taskOrder(task: UploadTask) {
  const displayStatus = uploadDisplayStatus(task);
  const rank = uploadStatusRank(task);
  const order = Number(task.queue_order || 0);
  const created = Number(task.created_at || 0);
  const updated = Number(task.updated_at || created || 0);
  if (uploadResumePending(task)) {
    return rank * 1_000_000_000_000 - updated;
  }
  if (displayStatus === "paused") {
    return rank * 1_000_000_000_000 + updated;
  }
  return rank * 1_000_000_000_000 + (order > 0 ? order : created);
}

function relayTaskOrder(task: any) {
  const rank = relayStatusRank(task);
  const order = Number(task.queue_order || 0);
  const created = Number(task.created_at || 0);
  const updated = Number(task.updated_at || created || 0);
  if (relayResumePending(task)) {
    return rank * 1_000_000_000_000 - updated;
  }
  if (relayDisplayStatus(task) === "paused") {
    return rank * 1_000_000_000_000 + updated;
  }
  return rank * 1_000_000_000_000 + (order > 0 ? order : created);
}

function offlineTaskOrder(task: any) {
  const rank = offlineStatusRank(task.status);
  const created = Number(task.created_at || task.updated_at || 0);
  return rank * 1_000_000_000_000 + created;
}

function uploadStatusRank(task: UploadTask) {
  const status = uploadDisplayStatus(task);
  if (status === "running") return 0;
  if (uploadResumePending(task)) return 1;
  if (status === "paused") return 2;
  if (status === "pending") return 3;
  if (status === "failed") return 4;
  if (status === "canceled") return 5;
  if (status === "success") return 6;
  if (status === "skipped") return 7;
  return 9;
}

function relayStatusRank(task: any) {
  const status = relayDisplayStatus(task);
  if (status === "running") return 0;
  if (relayResumePending(task)) return 1;
  if (status === "paused") return 2;
  if (status === "pending") return 3;
  if (status === "failed") return 4;
  if (status === "canceled") return 5;
  return 6;
}

function offlineStatusRank(status: string) {
  if (status === "running") return 0;
  if (status === "pending" || status === "retrying") return 1;
  if (status === "failed") return 2;
  if (status === "success") return 3;
  return 9;
}

onMounted(() => {
  updatePanelLeft();
  window.addEventListener("resize", onWindowResize);
  document.addEventListener("click", onDocumentClick);
  void refreshUploadTaskServerConcurrency?.();
  requestAnimationFrame(updateTaskListViewport);
});

watch(panelExpanded, () => {
  updatePanelLeft();
});

onUnmounted(() => {
  window.removeEventListener("resize", onWindowResize);
  document.removeEventListener("click", onDocumentClick);
});
</script>

<style scoped src="@/styles/upload-task-panel.css"></style>
