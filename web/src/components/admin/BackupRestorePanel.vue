<script setup lang="ts">
import { computed, onActivated, onMounted, reactive, ref } from "vue";
import {
  backupRestoreApi,
  type BackupRecord,
  type BackupRestoreStatus,
} from "@/api/backupRestore";
import { getApiErrorMessage } from "@/api/client";
import AppBadge from "@/components/base/AppBadge.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import FormField from "@/components/base/FormField.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { useConfirm } from "@/composables/useConfirm";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import { toast } from "@/composables/useToast";
import { formatSize, formatTime } from "@/utils/format";

const { showConfirm } = useConfirm();

const props = withDefaults(defineProps<{ bootstrap?: boolean }>(), { bootstrap: false });

const loading = ref(false);
const records = ref<BackupRecord[]>([]);
const restoreStatus = ref<BackupRestoreStatus>({ state: "idle" });
const loaded = ref(false);
const backupList = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();

const createOpen = ref(false);
const creating = ref(false);
const createForm = reactive({
  note: "",
  password: "",
  confirmPassword: "",
  includeAccounts: false,
});

const fileInput = ref<HTMLInputElement | null>(null);
const importOpen = ref(false);
const importing = ref(false);
const importFile = ref<File | null>(null);
const importPassword = ref("");

const restoreOpen = ref(false);
const preparingRestore = ref(false);
const restoreRecord = ref<BackupRecord | null>(null);
const restorePassword = ref("");
const restorePasswordInherited = ref(false);
const restoreAdmin = ref(false);

const restarting = ref(false);
const restartMessage = ref("");
const restartTimedOut = ref(false);

const hasPendingRestore = computed(() => restoreStatus.value.state === "waiting_restart");
const statusTone = computed<"info" | "success" | "danger">(() => {
  if (restoreStatus.value.state === "restore_success") return "success";
  if (restoreStatus.value.state === "restore_rollback") return "danger";
  return "info";
});

function scopeLabel(record: BackupRecord): string {
  return record.scope === "full" ? "完整备份" : "仅设置";
}

function resetCreateForm() {
  createForm.note = "";
  createForm.password = "";
  createForm.confirmPassword = "";
  createForm.includeAccounts = false;
}

function openCreate() {
  resetCreateForm();
  createOpen.value = true;
}

function openImport() {
  if (!props.bootstrap && hasPendingRestore.value) {
    toast.warning("已有备份等待重启恢复，请先完成或取消");
    return;
  }
  fileInput.value?.click();
}

async function load(silent = false) {
  if (props.bootstrap) return;
  if (!silent) loading.value = true;
  try {
    const [list, status] = await Promise.all([backupRestoreApi.list(), backupRestoreApi.status()]);
    records.value = list ?? [];
    restoreStatus.value = status ?? { state: "idle" };
    loaded.value = true;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载备份列表失败"));
  } finally {
    loading.value = false;
  }
}

async function submitCreate() {
  if (createForm.password.length < 8) {
    toast.error("备份密码至少 8 位");
    return;
  }
  if (createForm.password !== createForm.confirmPassword) {
    toast.error("两次输入的密码不一致");
    return;
  }
  creating.value = true;
  try {
    await backupRestoreApi.create({
      note: createForm.note.trim(),
      password: createForm.password,
      include_accounts: createForm.includeAccounts,
    });
    createOpen.value = false;
    resetCreateForm();
    toast.success("备份创建成功，请下载到其他设备妥善保存");
    await load(true);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "创建备份失败"));
  } finally {
    creating.value = false;
  }
}

function onImportFileSelected(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0] ?? null;
  input.value = "";
  if (!file) return;
  if (!file.name.toLowerCase().endsWith(".lpb")) {
    toast.error("请选择 .lpb 备份文件");
    return;
  }
  importFile.value = file;
  importPassword.value = "";
  importOpen.value = true;
}

async function submitImport() {
  if (!importFile.value) return;
  if (importPassword.value.length < 8) {
    toast.error("请输入创建备份时设置的密码");
    return;
  }
  importing.value = true;
  const password = importPassword.value;
  try {
    const summary = await backupRestoreApi.import(importFile.value, password);
    importOpen.value = false;
    importFile.value = null;
    if (props.bootstrap) {
      toast.success("备份已通过完整性校验");
      openRestore(summary.record, password);
    } else {
      toast.success("备份已导入并通过完整性校验");
      await load(true);
    }
  } catch (error) {
    toast.error(getApiErrorMessage(error, "导入备份失败"));
  } finally {
    importing.value = false;
  }
}

function openRestore(record: BackupRecord, knownPassword = "") {
  if (!props.bootstrap && hasPendingRestore.value) {
    toast.warning("已有备份等待重启恢复，请先完成或取消");
    return;
  }
  restoreRecord.value = record;
  restorePassword.value = knownPassword;
  restorePasswordInherited.value = knownPassword.length > 0;
  restoreAdmin.value = props.bootstrap;
  restoreOpen.value = true;
}

async function submitRestore() {
  const record = restoreRecord.value;
  if (!record) return;
  if (restorePassword.value.length < 8) {
    toast.error("请输入创建备份时设置的密码");
    return;
  }
  preparingRestore.value = true;
  try {
    const summary = await backupRestoreApi.prepareRestore(record.id, restorePassword.value, restoreAdmin.value);
    restoreOpen.value = false;
    restorePassword.value = "";
    if (!props.bootstrap) await load(true);
    if (summary.secret_from_env && record.scope === "full") {
      toast.warning("当前使用 LITEPAN_SECRET_KEY 环境变量，恢复后仍以环境变量中的密钥为准");
    }
    await restartNow();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "准备恢复失败"));
  } finally {
    preparingRestore.value = false;
  }
}

async function currentBootID(): Promise<string> {
  try {
    const response = await fetch("/api/health", { cache: "no-store", credentials: "include" });
    const payload = await response.json();
    return String(payload?.data?.boot_id ?? "");
  } catch {
    return "";
  }
}

async function restartNow() {
  const oldBootID = await currentBootID();
  restarting.value = true;
  restartTimedOut.value = false;
  restartMessage.value = "MyPan 正在安全关闭，请不要关闭此页面……";
  try {
    await backupRestoreApi.restart();
  } catch (error) {
    restarting.value = false;
    toast.error(getApiErrorMessage(error, "发起重启失败，请手动重启 MyPan"));
    return;
  }
  restartMessage.value = "正在应用备份并等待服务重新启动……";
  await pollRestart(oldBootID);
}

async function pollRestart(oldBootID: string) {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 1200));
    const bootID = await currentBootID();
    if (bootID && oldBootID && bootID !== oldBootID) {
      restartMessage.value = "服务已恢复，正在刷新页面……";
      await new Promise((resolve) => window.setTimeout(resolve, 500));
      window.location.reload();
      return;
    }
  }
  restartTimedOut.value = true;
  restartMessage.value = "尚未检测到服务重新启动。当前部署可能没有自动重启策略，请手动启动 MyPan 后重新连接。";
}

async function retryConnection() {
  const bootID = await currentBootID();
  if (bootID) {
    window.location.reload();
    return;
  }
  toast.info("服务仍未启动");
}

function download(record: BackupRecord) {
  const link = document.createElement("a");
  link.href = backupRestoreApi.downloadURL(record.id);
  link.download = "";
  document.body.appendChild(link);
  link.click();
  link.remove();
}

async function removeRecord(record: BackupRecord) {
  try {
    await showConfirm({
      title: "删除备份",
      message: `确定删除「${record.note || formatTime(record.created_at)}」吗？服务器上的该备份文件将无法恢复。`,
      icon: "trash",
      confirmText: "删除",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await removeWithDust({
      target: findDustTarget(backupList.value, `backup-${record.id}`),
      container: backupList.value,
      remove: async () => {
        await backupRestoreApi.remove(record.id);
        records.value = records.value.filter((item) => item.id !== record.id);
      },
    });
    toast.success("备份已删除");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "删除备份失败"));
  }
}

async function cancelPending() {
  try {
    await showConfirm({
      title: "取消待恢复操作",
      message: "将删除已经准备好的临时恢复数据，原备份文件仍保留在列表中。",
      icon: "warning",
      confirmText: "取消恢复",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await backupRestoreApi.cancelPending();
    toast.success("已取消待恢复操作");
    await load(true);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "取消失败"));
  }
}

async function acknowledgeStatus() {
  try {
    await backupRestoreApi.acknowledgeStatus();
    restoreStatus.value = { state: "idle" };
  } catch (error) {
    toast.error(getApiErrorMessage(error, "确认恢复结果失败"));
  }
}

defineExpose({ openCreate, openImport });

onMounted(() => {
  if (!props.bootstrap) void load();
});
onActivated(() => {
  if (!props.bootstrap && loaded.value) void load(true);
});
</script>

<template>
  <section class="backup-panel" :class="{ 'backup-panel--bootstrap': bootstrap }">
    <input ref="fileInput" class="backup-panel__file-input" type="file" accept=".lpb,application/octet-stream" @change="onImportFileSelected" />

    <template v-if="!bootstrap">
    <div v-if="restoreStatus.state !== 'idle'" class="restore-status" :class="`restore-status--${statusTone}`" aria-live="polite">
      <div class="restore-status__icon">
        <SvgIcon :name="restoreStatus.state === 'restore_success' ? 'fa-database' : 'fa-exclamation-triangle'" :size="22" />
      </div>
      <div class="restore-status__content">
        <strong>
          {{ restoreStatus.state === "waiting_restart" ? "等待重启恢复" : restoreStatus.state === "restore_success" ? "恢复成功" : "恢复失败，已自动回滚" }}
        </strong>
        <span>{{ restoreStatus.message }}</span>
      </div>
      <div class="restore-status__actions">
        <template v-if="restoreStatus.state === 'waiting_restart'">
          <AppButton size="sm" variant="secondary" @click="cancelPending">取消恢复</AppButton>
          <AppButton size="sm" variant="primary" @click="restartNow">立即重启</AppButton>
        </template>
        <AppButton v-else size="sm" variant="secondary" @click="acknowledgeStatus">知道了</AppButton>
      </div>
    </div>

    <div class="backup-entry-grid">
      <button type="button" class="backup-entry backup-entry--create" @click="openCreate">
        <span class="backup-entry__icon"><SvgIcon name="fa-database" :size="27" /></span>
        <span class="backup-entry__copy">
          <strong>创建备份</strong>
          <small>备份当前设置，可选择包含账号和任务</small>
        </span>
        <span class="backup-entry__arrow">→</span>
      </button>
      <button type="button" class="backup-entry" :disabled="hasPendingRestore" @click="openImport">
        <span class="backup-entry__icon"><SvgIcon name="upload" :size="27" /></span>
        <span class="backup-entry__copy">
          <strong>导入备份</strong>
          <small>上传并校验保存在其他设备的 .lpb 文件</small>
        </span>
        <span class="backup-entry__arrow">→</span>
      </button>
    </div>

    <AppStateBlock v-if="loading && !loaded" message="正在加载备份列表……" loading min-height="220px" />
    <AppStateBlock v-else-if="records.length === 0" message="暂无备份" min-height="220px" />

    <div v-else ref="backupList" class="backup-list">
      <article v-for="record in records" :key="record.id" class="backup-card" :data-dust-key="`backup-${record.id}`">
        <div class="backup-card__main">
          <strong>{{ record.note || "未填写备注" }}</strong>
          <span>创建时版本：{{ record.app_version }}</span>
        </div>
        <div class="backup-card__scope">
          <AppBadge :tone="record.scope === 'full' ? 'warning' : 'info'">{{ scopeLabel(record) }}</AppBadge>
        </div>
        <div class="backup-card__meta">
          <span>{{ formatTime(record.created_at) }}</span>
          <small>{{ formatSize(record.size) }}</small>
        </div>
        <div class="backup-card__actions">
          <AppButton size="sm" variant="secondary" @click="download(record)">下载</AppButton>
          <AppButton size="sm" variant="secondary" :disabled="hasPendingRestore" @click="openRestore(record)">恢复</AppButton>
          <AppButton size="sm" variant="danger" :disabled="restoreStatus.backup_id === record.id" @click="removeRecord(record)">删除</AppButton>
        </div>
      </article>
    </div>
    </template>

    <AppModal :open="createOpen" title="创建备份" size="sm" @close="creating ? undefined : (createOpen = false)">
      <div class="backup-form">
        <FormField label="备份备注">
          <AppInput v-model="createForm.note" maxlength="200" placeholder="可选，例如：升级前" />
        </FormField>
        <p class="backup-form__help">由于备份可能包含管理员登录信息、API 密钥、网盘账号数据等敏感信息，必须使用密码加密。</p>
        <FormField label="备份密码" required>
          <AppInput v-model="createForm.password" type="password" autocomplete="new-password" placeholder="至少 8 位；忘记后无法恢复" />
        </FormField>
        <FormField label="确认密码" required>
          <AppInput v-model="createForm.confirmPassword" type="password" autocomplete="new-password" placeholder="再次输入备份密码" />
        </FormField>
        <label class="backup-option">
          <input v-model="createForm.includeAccounts" type="checkbox" />
          <span>
            <strong>包含网盘账号、登录凭据和关联任务</strong>
            <small>勾选后创建完整备份；不勾选时只备份设置，不包含网盘账号和任何任务。</small>
          </span>
        </label>
      </div>
      <template #footer>
        <AppButton variant="primary" :disabled="creating" @click="submitCreate">{{ creating ? "正在创建……" : "创建备份" }}</AppButton>
      </template>
    </AppModal>

    <AppModal :open="importOpen" title="导入备份" size="sm" @close="importing ? undefined : (importOpen = false)">
      <div class="backup-form">
        <div class="selected-backup-file">
          <SvgIcon name="fa-database" :size="22" />
          <div><strong>{{ importFile?.name }}</strong><span>{{ formatSize(importFile?.size || 0) }}</span></div>
        </div>
        <FormField label="备份密码" required>
          <AppInput v-model="importPassword" type="password" autocomplete="current-password" placeholder="输入创建该备份时设置的密码" />
        </FormField>
        <p class="backup-form__help">文件会先完成格式、加密载荷、校验值和数据库完整性检查，通过后才加入备份列表。</p>
      </div>
      <template #footer>
        <AppButton variant="primary" :disabled="importing" @click="submitImport">{{ importing ? "正在校验……" : "导入并校验" }}</AppButton>
      </template>
    </AppModal>

    <AppModal :open="restoreOpen" :title="bootstrap ? '从备份恢复' : '恢复备份'" size="sm" @close="preparingRestore ? undefined : (restoreOpen = false)">
      <div v-if="restoreRecord" class="backup-form">
        <div class="restore-target">
          <div><span>备份</span><strong>{{ restoreRecord.note || formatTime(restoreRecord.created_at) }}</strong></div>
          <AppBadge :tone="restoreRecord.scope === 'full' ? 'warning' : 'info'">{{ scopeLabel(restoreRecord) }}</AppBadge>
        </div>
        <div v-if="restoreRecord.scope === 'full'" class="restore-summary">
          <span><strong>{{ restoreRecord.account_count }}</strong> 个网盘账号</span>
          <span><strong>{{ restoreRecord.task_count }}</strong> 个关联任务</span>
        </div>
        <FormField v-if="!restorePasswordInherited" label="备份密码" required>
          <AppInput v-model="restorePassword" type="password" autocomplete="current-password" placeholder="输入创建该备份时设置的密码" />
        </FormField>
        <label v-if="!bootstrap" class="backup-option">
          <input v-model="restoreAdmin" type="checkbox" />
          <span>
            <strong>同时恢复备份中的管理员登录信息</strong>
            <small>默认保留当前管理员用户名和密码；勾选后，恢复完成需使用备份中的账号重新登录。</small>
          </span>
        </label>
        <div v-else class="bootstrap-admin-note">
          <SvgIcon name="notify-info" :size="18" />
          <span>恢复时会一并带回备份中的管理员登录信息，完成后请使用原账号登录。</span>
        </div>
      </div>
      <template #footer>
        <AppButton variant="danger" :disabled="preparingRestore" @click="submitRestore">{{ preparingRestore ? "正在准备……" : "恢复并重启" }}</AppButton>
      </template>
    </AppModal>

    <AppModal :open="restarting" title="正在恢复备份" size="sm">
      <div class="restart-progress" aria-live="polite">
        <span class="restart-progress__spinner" :class="{ 'restart-progress__spinner--stopped': restartTimedOut }" />
        <p>{{ restartMessage }}</p>
      </div>
      <template v-if="restartTimedOut" #footer>
        <AppButton variant="primary" @click="retryConnection">重新连接</AppButton>
      </template>
    </AppModal>
  </section>
</template>

<style scoped>
.backup-panel { display: grid; gap: 16px; padding-bottom: 24px; }
.backup-panel--bootstrap { display: contents; }
.backup-panel__file-input { display: none; }
.backup-entry-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.backup-entry { min-width: 0; display: grid; grid-template-columns: 48px minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 18px; border: 1px solid var(--border-soft); border-radius: var(--radius-md); background: color-mix(in srgb, var(--surface) 94%, transparent); color: var(--text); text-align: left; cursor: pointer; transition: border-color .18s ease; }
.backup-entry:hover:not(:disabled) { border-color: color-mix(in srgb, var(--brand) 38%, var(--border-soft)); }
.backup-entry:disabled { cursor: not-allowed; opacity: .55; }
.backup-entry__icon { display: grid; place-items: center; width: 48px; height: 48px; border-radius: 15px; color: var(--brand); background: color-mix(in srgb, var(--brand) 11%, var(--surface)); }
.backup-entry--create .backup-entry__icon { color: var(--success); background: color-mix(in srgb, var(--success) 11%, var(--surface)); }
.backup-entry__copy { min-width: 0; display: grid; gap: 5px; }
.backup-entry__copy strong { font-size: 15px; }
.backup-entry__copy small { color: var(--text-muted); font-size: 12px; line-height: 1.5; }
.backup-entry__arrow { color: var(--text-muted); font-size: 20px; }
.restore-status { display: flex; align-items: center; gap: 12px; padding: 13px 16px; border: 1px solid; border-radius: var(--radius-md); background: var(--surface); }
.restore-status--info { border-color: color-mix(in srgb, var(--info) 35%, var(--border-soft)); background: color-mix(in srgb, var(--info) 7%, var(--surface)); }
.restore-status--success { border-color: color-mix(in srgb, var(--success) 35%, var(--border-soft)); background: color-mix(in srgb, var(--success) 7%, var(--surface)); }
.restore-status--danger { border-color: color-mix(in srgb, var(--danger) 35%, var(--border-soft)); background: color-mix(in srgb, var(--danger) 7%, var(--surface)); }
.restore-status__icon { color: var(--brand); }
.restore-status__content { min-width: 0; display: grid; gap: 3px; flex: 1; }
.restore-status__content strong { color: var(--text); font-size: 14px; }
.restore-status__content span { color: var(--text-muted); font-size: 12px; line-height: 1.5; }
.restore-status__actions { display: flex; gap: 8px; flex: 0 0 auto; }
.backup-list { display: grid; gap: 10px; }
.backup-card { display: grid; grid-template-columns: minmax(220px, 1fr) 110px 170px auto; align-items: center; gap: 16px; padding: 14px 16px; border: 1px solid var(--border-soft); border-radius: var(--radius-md); background: var(--surface); }
.backup-card__main { min-width: 0; display: grid; gap: 5px; }
.backup-card__main strong { overflow: hidden; color: var(--text); font-size: 14px; text-overflow: ellipsis; white-space: nowrap; }
.backup-card__main span, .backup-card__meta span, .backup-card__meta small { color: var(--text-muted); font-size: 12px; }
.backup-card__meta { display: grid; gap: 4px; }
.backup-card__actions { display: flex; justify-content: flex-end; gap: 6px; }
.backup-form { display: grid; gap: 15px; }
.backup-option { display: flex; align-items: flex-start; gap: 10px; cursor: pointer; }
.backup-option input { width: 16px; height: 16px; margin: 2px 0 0; accent-color: var(--brand); }
.backup-option span { display: grid; gap: 4px; }
.backup-option strong { color: var(--text); font-size: 13px; line-height: 1.45; }
.backup-option small, .backup-form__help { color: var(--text-muted); font-size: 12px; line-height: 1.55; }
.restore-summary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.restore-summary span { padding: 10px 12px; border-radius: var(--radius-sm); background: var(--surface-sunken); color: var(--text-muted); font-size: 12px; }
.restore-summary strong { color: var(--text); font-size: 16px; }
.bootstrap-admin-note { display: flex; align-items: flex-start; gap: 9px; padding: 10px 12px; border-radius: var(--radius-sm); background: color-mix(in srgb, var(--brand) 8%, var(--surface)); color: var(--text-regular); font-size: 12px; line-height: 1.6; }
.bootstrap-admin-note .lp-svg-icon { margin-top: 1px; color: var(--brand); }
.selected-backup-file, .restore-target { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px; border: 1px solid var(--border-soft); border-radius: var(--radius-sm); background: var(--surface-sunken); }
.selected-backup-file { justify-content: flex-start; color: var(--brand); }
.selected-backup-file div, .restore-target div { min-width: 0; display: grid; gap: 3px; }
.selected-backup-file strong, .restore-target strong { overflow: hidden; color: var(--text); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.selected-backup-file span, .restore-target span { color: var(--text-muted); font-size: 12px; }
.backup-form__help { margin: 0; }
.restart-progress { display: grid; place-items: center; gap: 14px; padding: 18px 8px; text-align: center; }
.restart-progress p { margin: 0; color: var(--text-regular); font-size: 13px; line-height: 1.65; }
.restart-progress__spinner { width: 34px; height: 34px; border: 3px solid color-mix(in srgb, var(--brand) 18%, transparent); border-top-color: var(--brand); border-radius: 50%; animation: backup-spin .8s linear infinite; }
.restart-progress__spinner--stopped { animation: none; border-color: var(--warning); }
@keyframes backup-spin { to { transform: rotate(360deg); } }

@media (max-width: 900px) {
  .backup-card { grid-template-columns: minmax(0, 1fr) auto; gap: 10px 14px; }
  .backup-card__scope { justify-self: end; }
  .backup-card__meta { grid-column: 1; display: flex; gap: 12px; }
  .backup-card__actions { grid-column: 2; grid-row: 2; }
}
@media (max-width: 640px) {
  .backup-entry-grid { grid-template-columns: minmax(0, 1fr); }
  .backup-entry { padding: 15px; }
  .restore-status { align-items: flex-start; flex-wrap: wrap; }
  .restore-status__actions { width: 100%; justify-content: flex-end; }
  .backup-card { grid-template-columns: minmax(0, 1fr); padding: 14px; }
  .backup-card__scope { grid-row: 1; grid-column: 1; justify-self: end; }
  .backup-card__main { padding-right: 96px; grid-row: 1; grid-column: 1; }
  .backup-card__meta, .backup-card__actions { grid-column: 1; grid-row: auto; }
  .backup-card__actions { justify-content: flex-start; flex-wrap: wrap; }
}
:global([data-skin="brutal"]) .backup-entry,
:global([data-skin="brutal"]) .backup-card,
:global([data-skin="brutal"]) .restore-status,
:global([data-skin="brutal"]) .selected-backup-file,
:global([data-skin="brutal"]) .restore-target { border: var(--brutal-border-width, 2px) solid var(--text); border-radius: 0; box-shadow: var(--brutal-shadow, 3px 3px 0 var(--text)); }
</style>
