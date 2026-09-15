<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { accountsApi } from "@/api/accounts";
import type { Account } from "@/api/types";
import { enqueueCrossTransferPlain, type CrossTransferPlainEnqueueResult } from "@/api/crossTransfer";
import type { FolderSelection } from "@/components/file/FolderSelector.vue";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import "@/styles/cross-transfer.css";

/**
 * 跨盘普传：不探测指纹、不尝试秒传，直接「源盘下载到服务器临时目录 → 上传目标盘」。
 * 服务端一次性枚举源目录并创建持久化 relay 任务（POST /cross-transfer/plain-enqueue），
 * 入队即返回、进度在任务面板查看，关闭页面不影响。
 * 外壳样式与「跨盘秒传」共用 styles/cross-transfer.css（根节点挂 xfer-ui）。
 */

type Conflict = "skip" | "rename" | "overwrite";

interface SourceSel {
  parentId: string;
  name: string;
  path: string;
  ancestorIds: string[];
}

interface DstSel {
  accId: number;
  accName: string;
  accDriverType: string;
  parentId: string;
  path: string;
}

const accounts = ref<Account[]>([]);
const src = ref<{ accId: number; accName: string; accDriverType: string; sources: SourceSel[] } | null>(null);
const dst = ref<DstSel | null>(null);
const conflict = ref<Conflict>("skip");
const running = ref(false);
const phaseText = ref("");

const pickerOpen = ref(false);
const pickerMode = ref<"src" | "dst">("src");
const pickerAccounts = ref<Account[]>([]);
const pickerInitialAccountId = ref<number | null>(null);
const pickerInitialPath = ref("");
const pickerMulti = ref(false);
const pickerLocationMode = ref<"root" | "preserve">("root");

const settingsOpen = ref(false);
const settingsMenuRef = ref<HTMLElement | null>(null);

function toggleSettings() {
  settingsOpen.value = !settingsOpen.value;
}

function onDocPointerDown(e: PointerEvent) {
  const root = settingsMenuRef.value;
  if (settingsOpen.value && root && !root.contains(e.target as Node)) {
    settingsOpen.value = false;
  }
}

onMounted(() => document.addEventListener("pointerdown", onDocPointerDown));
onUnmounted(() => document.removeEventListener("pointerdown", onDocPointerDown));

const accountsLoaded = ref(false);

const srcCount = computed(() => src.value?.sources.length || 0);
const srcLabel = computed(() => {
  if (!src.value) return "选择源账号 · 目录";
  const names = src.value.sources.map((s) => s.name).join("、");
  return srcCount.value <= 1
    ? `${src.value.accName} · ${names || src.value.sources[0]?.path || "根目录"}`
    : `${src.value.accName} · ${srcCount.value} 个目录`;
});
const dstLabel = computed(() =>
  dst.value ? `${dst.value.accName} · ${dst.value.path}` : "选择目标账号 · 目录",
);

async function loadAccounts(force = false) {
  if (accountsLoaded.value && !force) return;
  try {
    accounts.value = await accountsApi.list();
    accountsLoaded.value = true;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载账号列表失败"));
  }
}
void loadAccounts();

function accountById(id: number | null) {
  return accounts.value.find((a) => a.id === id) || null;
}

function openPicker(mode: "src" | "dst") {
  const list = accounts.value.filter((a) => a.is_active !== false);
  if (!list.length) {
    toast.warning("没有可用账号，请先到「存储管理」添加");
    return;
  }
  pickerMode.value = mode;
  pickerAccounts.value = list;
  pickerMulti.value = mode === "src";
  pickerLocationMode.value = mode === "src" ? "root" : "preserve";
  if (mode === "src") {
    pickerInitialAccountId.value = src.value?.accId ?? list[0]?.id ?? null;
    pickerInitialPath.value = "";
  } else {
    pickerInitialAccountId.value = dst.value?.accId ?? list[0]?.id ?? null;
    pickerInitialPath.value = dst.value?.path || "";
  }
  pickerOpen.value = true;
}

function onPickerResolve(payload: {
  accountId: number;
  accountName: string;
  parentId: string;
  path: string;
  selections?: FolderSelection[];
}) {
  pickerOpen.value = false;
  const acc = accountById(payload.accountId);
  if (pickerMode.value === "src") {
    const selected: SourceSel[] =
      Array.isArray(payload.selections) && payload.selections.length
        ? payload.selections.map((item) => ({
            parentId: item.id,
            name: item.name || item.path?.split("/").filter(Boolean).pop() || "根目录",
            path: item.path || "/",
            ancestorIds: [...(item.ancestorIds || [])],
          }))
        : [{
            parentId: payload.parentId,
            name: payload.path.split("/").filter(Boolean).pop() || "根目录",
            path: payload.path,
            ancestorIds: [],
          }];
    src.value = {
      accId: payload.accountId,
      accName: payload.accountName || acc?.name || String(payload.accountId),
      accDriverType: acc?.driver_type || "",
      sources: selected,
    };
    toast.success(`已选择 ${selected.length} 个源目录`);
  } else {
    dst.value = {
      accId: payload.accountId,
      accName: payload.accountName || acc?.name || String(payload.accountId),
      accDriverType: acc?.driver_type || "",
      parentId: payload.parentId, // 根目录时为空串（后端按目标盘根处理）
      path: payload.path || "/",
    };
  }
}

function swapSides() {
  if (!src.value || !dst.value) {
    toast.warning("请先分别选择来源与目标");
    return;
  }
  const s = src.value;
  const d = dst.value;
  src.value = {
    accId: d.accId,
    accName: d.accName,
    accDriverType: d.accDriverType,
    sources: [{ parentId: d.parentId, name: d.path.split("/").filter(Boolean).pop() || "根目录", path: d.path, ancestorIds: [] }],
  };
  dst.value = { accId: s.accId, accName: s.accName, accDriverType: s.accDriverType, parentId: s.sources[0]?.parentId || "", path: s.sources[0]?.path || "/" };
}

const canStart = computed(() => {
  if (running.value) return false;
  if (!src.value?.sources.length) return false;
  // 目标允许为网盘根目录（parentId 为空串时后端按根处理），只要已选账号即可。
  return Boolean(dst.value?.accId);
});

async function startTransfer() {
  if (!canStart.value || !src.value || !dst.value) return;
  running.value = true;
  phaseText.value = "正在枚举源目录并创建任务…";
  try {
    const res: CrossTransferPlainEnqueueResult = await enqueueCrossTransferPlain({
      source_account_id: src.value.accId,
      source_account_name: src.value.accName,
      source_driver_type: src.value.accDriverType,
      target_account_id: dst.value.accId,
      target_account_name: dst.value.accName,
      target_driver_type: dst.value.accDriverType,
      target_parent_id: dst.value.parentId,
      target_display_path: dst.value.path,
      sources: src.value.sources.map((item) => ({
        parent_id: item.parentId,
        display_path: item.path || "/",
        ancestor_ids: item.ancestorIds,
      })),
      conflict: conflict.value,
    });
    const parts = [
      res.enqueued > 0 ? `已入队 ${res.enqueued} 个任务` : "",
      res.skipped > 0 ? `跳过 ${res.skipped} 个同名` : "",
      res.failed > 0 ? `${res.failed} 个失败` : "",
    ].filter(Boolean);
    toast.success((parts.length ? parts.join("，") + "；" : "") + "可在任务面板查看进度");
    if (res.truncated) {
      toast.warning(res.message || "枚举未完整（超出数量限制），已完成部分入队");
    } else if (res.failed > 0) {
      toast.warning(
        res.failed_name
          ? `失败示例：${res.failed_name} — ${res.failed_message}`
          : `${res.failed} 个文件入队失败`,
      );
    }
  } catch (e) {
    toast.error(getApiErrorMessage(e, "创建跨盘普传任务失败"));
  } finally {
    running.value = false;
    phaseText.value = "";
  }
}
</script>

<template>
  <div class="ct-plain xfer-ui">
    <div class="transfer-shell">
      <!-- 顶部：源 / 目标 名称条 -->
      <div class="transfer-topbar">
        <div class="tb-side tb-src">
          <span class="logo-chip s26"><i class="fas fa-hdd"></i></span>
          <div class="tb-title">
            <span>{{ src?.accName || "源网盘" }}</span>
            <small>下载到服务器临时目录</small>
          </div>
          <span class="panel-role">源</span>
        </div>
        <div class="tb-mid">
          <button type="button" class="tb-swap" title="交换来源与目标" @click="swapSides">
            <i class="fas fa-right-left"></i>
          </button>
          <span class="tb-swap-hint">可交换</span>
        </div>
        <div class="tb-side tb-dst">
          <span class="panel-role dst-role">目标</span>
          <div class="tb-title tb-title-dst">
            <span>{{ dst?.accName || "目标网盘" }}</span>
            <small>由服务器中转上传</small>
          </div>
          <span class="logo-chip s26"><i class="fas fa-upload"></i></span>
        </div>
      </div>

      <!-- 主体：左右两栏 -->
      <div class="transfer-body">
        <div class="panel src">
          <div class="panel-pick">
            <button class="combo" @click="openPicker('src')">
              <span class="c-ic"><i class="fas fa-hdd"></i></span>
              <span class="c-text" :class="{ placeholder: !src }">{{ srcLabel }}</span>
              <span class="c-caret"><i class="fas fa-chevron-down"></i></span>
            </button>
          </div>
          <div class="tree tree-host">
            <div v-if="src?.sources?.length" class="src-selected">
              <div v-for="(item, i) in src.sources" :key="i" class="src-item">
                <i class="fas fa-folder"></i>
                <span class="src-item-path" :title="item.path">{{ item.path || "/" }}</span>
              </div>
              <p class="src-tip"><i class="fas fa-circle-info"></i> 将保留目录结构、包含全部子目录与文件</p>
            </div>
            <div v-else class="tree-empty">选择源目录后，将按目录整树传输，不探测秒传指纹</div>
          </div>
        </div>

        <div class="panel dst">
          <div class="panel-pick">
            <button class="combo" @click="openPicker('dst')">
              <span class="c-ic"><i class="fas fa-hdd"></i></span>
              <span class="c-text" :class="{ placeholder: !dst }">{{ dstLabel }}</span>
              <span class="c-caret"><i class="fas fa-chevron-down"></i></span>
            </button>
          </div>
          <div class="tree">
            <div v-if="dst" class="src-selected">
              <div class="src-item"><i class="fas fa-folder"></i><span class="src-item-path" :title="dst.path">{{ dst.path || "/" }}</span></div>
              <p class="src-tip"><i class="fas fa-circle-info"></i> 上传前会自动创建缺失的目标子目录</p>
            </div>
            <div v-else class="tree-empty">选择目标目录，媒体上传后在此目录下按来源结构存放</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 底部操作：状态 + 设置齿轮 + 开始按钮 -->
    <div class="ct-plain-footer">
      <div class="ft-left">
        <span v-if="running" class="ft-running">
          <BusySpinner :size="16" color="var(--brand)" />
          {{ phaseText }}
        </span>
        <span v-else-if="srcCount" class="ft-ready">
          <i class="fas fa-circle-check"></i> 已选 {{ srcCount }} 个源目录，共传输其中全部文件
        </span>
        <span v-else class="ft-ready muted"><i class="fas fa-circle-info"></i> 先选择源目录与目标目录</span>
      </div>
      <div class="footer-island">
        <div ref="settingsMenuRef" class="ct-settings-menu">
          <button
            type="button"
            class="ct-settings-trigger"
            title="传输设置"
            :aria-expanded="settingsOpen"
            @click="toggleSettings"
          >
            <i class="fas fa-sliders"></i>
          </button>
          <div v-if="settingsOpen" class="ct-settings-dropdown ct-settings-pop">
            <div class="ct-settings-panel">
              <div class="ct-settings-block">
                <div class="ct-settings-label">同名文件处理</div>
                <div class="ct-settings-seg" role="group" aria-label="同名文件处理">
                  <button
                    type="button"
                    class="ct-settings-opt"
                    :class="{ active: conflict === 'skip' }"
                    @click="conflict = 'skip'"
                  >跳过</button>
                  <button
                    type="button"
                    class="ct-settings-opt"
                    :class="{ active: conflict === 'rename' }"
                    @click="conflict = 'rename'"
                  >重命名</button>
                  <button
                    type="button"
                    class="ct-settings-opt"
                    :class="{ active: conflict === 'overwrite' }"
                    @click="conflict = 'overwrite'"
                  >覆盖</button>
                </div>
                <p class="ct-settings-fallback-hint">默认读取系统设置；目标盘不支持覆盖时自动降级为重命名。</p>
              </div>
            </div>
          </div>
        </div>
        <button class="ct-btn ct-btn-go" :disabled="!canStart" @click="startTransfer">
          <i :class="running ? 'fas fa-spinner fa-spin' : 'fas fa-cloud-arrow-down'"></i>
          {{ running ? "正在入队…" : "开始传输" }}
        </button>
      </div>
    </div>

    <FolderPickerModal
      :open="pickerOpen"
      selectable-account
      :accounts="pickerAccounts"
      :account-id="pickerInitialAccountId"
      :initial-path="pickerInitialPath"
      :multi-select="pickerMulti"
      :initial-location-mode="pickerLocationMode"
      :selection-restore-mode="pickerMode === 'src' ? 'reset' : 'preserve'"
      :title="pickerMode === 'src' ? '选择源目录（可多选）' : '选择目标目录'"
      :confirm-text="pickerMode === 'src' ? '确认选择' : '选择当前目录'"
      show-refresh
      @close="pickerOpen = false"
      @resolve="onPickerResolve"
    />
  </div>
</template>

<style scoped>
/* 布局与专属样式；外壳/面板/combo/按钮等复用 styles/cross-transfer.css（.xfer-ui） */
.ct-plain {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 普传没有顶部 flow 线路条，去掉共享样式里为秒传 flow 预留的外壳上边距，与其它页面 Tab 内容对齐 */
.ct-plain .transfer-shell {
  margin-top: 0;
}

.ct-plain .logo-chip.s26 {
  color: var(--text-muted);
  font-size: 15px;
}

/* 设置弹层（从齿轮向上弹出，与秒传同款外观，仅定位方式不同） */
.ct-plain .ct-settings-pop {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  width: 320px;
  max-width: 84vw;
  z-index: 40;
}

/* 已选目录展示（树区域内） */
.src-selected { display: flex; flex-direction: column; gap: 4px; }
.src-item { display: flex; align-items: center; gap: 8px; padding: 5px 8px; font-size: 13px; }
.src-item > i { color: #f59e0b; }
.src-item-path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.src-tip { margin-top: 8px; font-size: 12px; color: var(--text-secondary); display: flex; gap: 6px; align-items: center; }

/* 底部操作条 */
.ct-plain-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-soft);
  padding: 10px 14px;
}
.ft-left { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-regular); }
.ft-ready i { color: var(--success); }
.ft-ready.muted i { color: var(--text-secondary); }
.ft-running { display: flex; align-items: center; gap: 8px; color: var(--text-regular); }
</style>
