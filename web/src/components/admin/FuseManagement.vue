<script setup lang="ts">
import { computed, onDeactivated, onMounted, reactive, ref } from "vue";
import { storeToRefs } from "pinia";
import { getApiErrorMessage } from "@/api/client";
import {
  createFuseMount,
  deleteFuseMount,
  clearFuseReadCache,
  fetchFuseReadCache,
  fetchFuseMounts,
  fetchFuseStatus,
  mountFuse,
  unmountFuse,
  updateFuseConfig,
  updateFuseMount,
  updateFuseReadCache,
  type FuseMount,
  type FuseReadCacheConfig,
  type FuseStatus,
} from "@/api/fuse";
import AppButton from "@/components/base/AppButton.vue";
import AppCardActionButton from "@/components/base/AppCardActionButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppModal from "@/components/base/AppModal.vue";
import StatCard from "@/components/base/StatCard.vue";
import FormField from "@/components/base/FormField.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import AdminTaskTabHeader from "@/components/admin/AdminTaskTabHeader.vue";
import AdminStatusPill, { type AdminStatusPillTone } from "@/components/admin/AdminStatusPill.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import InputActionField from "@/components/admin/InputActionField.vue";
import WarningBanner from "@/components/admin/WarningBanner.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminEnableToggle from "@/components/admin/AdminEnableToggle.vue";
import AdminTableActionBtn from "@/components/admin/AdminTableActionBtn.vue";
import AdminRowActions from "@/components/admin/AdminRowActions.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import { useAccountPathLabel } from "@/composables/useAccountPathLabel";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import { confirm } from "@/composables/useConfirm";
import { useSettingsForm } from "@/composables/useSettingsForm";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast } from "@/composables/useToast";
import { useAccountsStore } from "@/stores/accounts";
import { formatSize } from "@/utils/format";
import "@/styles/admin-shared.css";
import "@/styles/admin-table.css";

const accountsStore = useAccountsStore();
const { accounts } = storeToRefs(accountsStore);

const { loading: settingsLoading, loaded: settingsLoaded, runLoad: runSettingsLoad } = useSettingsLoad();
const listLoading = ref(false);
const submitting = ref(false);
const togglingAutoMountId = ref<number | null>(null);
const mounts = ref<FuseMount[]>([]);
const fuseMountList = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();
useAdminPageLoading("share", computed(() => listLoading.value && !mounts.value.length));
const status = ref<FuseStatus | null>(null);
const settingsDrawerOpen = ref(false);
const drawerSaving = ref(false);
const readCacheClearing = ref(false);

type DrawerForm = {
  service_enabled: boolean;
  mount_root: string;
  read_cache_enabled: boolean;
  max_gb: number;
  retention_days: number;
  eviction_policy: "lru" | "large_file";
};

const {
  settings: drawerForm,
  isDirty: drawerDirty,
  isFieldChanged: isDrawerFieldChanged,
  snapshotBaseline: snapshotDrawerForm,
  revert: revertDrawerForm,
} = useSettingsForm<DrawerForm>({
  service_enabled: false,
  mount_root: "",
  read_cache_enabled: false,
  max_gb: 10,
  retention_days: 7,
  eviction_policy: "lru",
});

const readCacheStats = reactive({
  used_bytes: 0,
  limit_bytes: 0,
  block_count: 0,
  root_path: "",
});

const dialogOpen = ref(false);
const pickerOpen = ref(false);
const showAdvanced = ref(false);
const editingId = ref<number | null>(null);

const form = reactive({
  name: "",
  account_id: 0,
  root_item_id: "",
  root_path: "",
  auto_mount: true,
  uid: 0,
  gid: 0,
  dir_mode: "0755",
  file_mode: "0644",
  enabled: true,
});

const mountRoot = computed(() => status.value?.mount_root || "/app/mounts");

const readCacheUsageText = computed(() => {
  return `${formatSize(readCacheStats.used_bytes)} / ${formatSize(readCacheStats.limit_bytes)}`;
});

const readCacheRootPath = computed(() => readCacheStats.root_path || "/app/data/fuse_read_cache");

const mountedCount = computed(() => mounts.value.filter((m) => m.state === "mounted").length);

const drawerCanSave = computed(() => drawerDirty.value && !settingsLoading.value);

const evictionPolicyOptions = [
  { value: "lru", label: "最近最少使用（LRU）" },
  { value: "large_file", label: "大文件优先" },
];

const settingsDrawerPageDirty = computed(() => settingsDrawerOpen.value && drawerDirty.value);
const { confirmDiscardChanges: confirmDiscardDrawerChanges } = useSettingsPageDirty(
  settingsDrawerPageDirty,
  revertDrawerForm,
);

function applyReadCache(cfg: FuseReadCacheConfig) {
  drawerForm.read_cache_enabled = cfg.enabled;
  drawerForm.max_gb = cfg.max_gb;
  drawerForm.retention_days = cfg.retention_days;
  drawerForm.eviction_policy = cfg.eviction_policy === "large_file" ? "large_file" : "lru";
  readCacheStats.used_bytes = cfg.used_bytes;
  readCacheStats.limit_bytes = cfg.limit_bytes;
  readCacheStats.block_count = cfg.block_count;
  readCacheStats.root_path = cfg.root_path || "";
}

async function loadReadCache(options?: { silent?: boolean }) {
  try {
    applyReadCache(await fetchFuseReadCache());
  } catch (e) {
    if (!options?.silent) {
      toast.error(`加载读缓存设置失败：${getApiErrorMessage(e, "请稍后重试")}`);
    }
  }
}

async function openSettingsDrawer() {
  if (!settingsLoading.value && !drawerDirty.value) {
    await loadAll({ silent: settingsLoaded.value });
  }
  settingsDrawerOpen.value = true;
}

async function closeSettingsDrawer() {
  if (!(await confirmDiscardDrawerChanges())) return;
  settingsDrawerOpen.value = false;
}

// 本地挂载面板也会被 KeepAlive 缓存，切换文件共享 tab 时同样收回抽屉。
onDeactivated(() => {
  if (!settingsDrawerOpen.value) return;
  if (drawerDirty.value) revertDrawerForm();
  settingsDrawerOpen.value = false;
});

async function saveDrawerSettings() {
  drawerSaving.value = true;
  try {
    const mountRootChanged = isDrawerFieldChanged("mount_root");
    if (isDrawerFieldChanged("service_enabled") || mountRootChanged) {
      const body: { enabled: boolean; mount_root?: string } = { enabled: drawerForm.service_enabled };
      if (mountRootChanged) body.mount_root = drawerForm.mount_root.trim();
      applyServiceStatus(await updateFuseConfig(body), mountRootChanged);
    }
    applyReadCache(
      await updateFuseReadCache({
        enabled: drawerForm.read_cache_enabled,
        max_gb: Number(drawerForm.max_gb),
        retention_days: Number(drawerForm.retention_days),
        eviction_policy: drawerForm.eviction_policy,
      }),
    );
    snapshotDrawerForm();
    toast.success(mountRootChanged ? "设置已保存，挂载根目录将在重启后生效" : "设置已保存");
  } catch (e) {
    toast.error(`保存本地挂载设置失败：${getApiErrorMessage(e, "请稍后重试")}`);
  } finally {
    drawerSaving.value = false;
  }
}

async function clearReadCacheDisk() {
  if (!(await confirm({
    title: "清空 FUSE 读缓存",
    message: "将删除本地磁盘上已缓存的全部文件块，不影响云盘文件。确定继续？",
    icon: "trash",
    confirmText: "清空",
    danger: true,
  }))) return;
  readCacheClearing.value = true;
  try {
    await clearFuseReadCache();
    await loadReadCache({ silent: true });
    toast.success("读缓存已清空");
  } catch (e) {
    toast.error(`清空读缓存失败：${getApiErrorMessage(e, "请稍后重试")}`);
  } finally {
    readCacheClearing.value = false;
  }
}

const namePlaceholder = computed(() => `名称即 ${mountRoot.value} 下的目录名`);

const { display: sourceDirDisplay, title: sourceDirTitle } = useAccountPathLabel({
  accountId: computed(() => form.account_id),
  path: computed(() => form.root_path),
  accounts,
  ready: computed(() => Boolean(form.root_item_id)),
});

function normalizeFuseRootItemId(parentId: string): string {
  const id = (parentId || "").trim();
  return id === "" ? "0" : id;
}

const stateLabel: Record<string, string> = {
  unmounted: "未挂载",
  mounting: "挂载中",
  mounted: "已挂载",
  error: "错误",
};

function mountStatusTone(state?: string): AdminStatusPillTone {
  switch (state) {
    case "mounted":
      return "success";
    case "mounting":
      return "brand";
    case "error":
      return "danger";
    default:
      return "muted";
  }
}

function isActiveMountState(state?: string) {
  return state === "mounted" || state === "mounting" || state === "error";
}

function canDeleteMount(row: FuseMount) {
  return row.state === "unmounted";
}

function resetForm() {
  editingId.value = null;
  form.name = "";
  form.account_id = accounts.value[0]?.id ?? 0;
  form.root_item_id = "";
  form.root_path = "";
  form.auto_mount = true;
  form.uid = 0;
  form.gid = 0;
  form.dir_mode = "0755";
  form.file_mode = "0644";
  form.enabled = true;
  showAdvanced.value = false;
}

function openCreate() {
  resetForm();
  dialogOpen.value = true;
}

function openEdit(row: FuseMount) {
  editingId.value = row.id ?? null;
  form.name = row.name;
  form.account_id = row.account_id;
  form.root_item_id =
    (row.root_item_id || "").trim() ||
    (((row.root_path || "/").replace(/\/+$/, "") || "/") === "/" ? "0" : "");
  form.root_path = row.root_path;
  form.auto_mount = row.auto_mount;
  form.uid = row.uid;
  form.gid = row.gid;
  form.dir_mode = row.dir_mode || "0755";
  form.file_mode = row.file_mode || "0644";
  form.enabled = row.enabled;
  dialogOpen.value = true;
}

function mountPointForName(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "";
  return `${mountRoot.value}/${trimmed}`;
}

function applyServiceStatus(st: FuseStatus, keepMountRoot = false) {
  status.value = st;
  drawerForm.service_enabled = st.enabled;
  if (!keepMountRoot) drawerForm.mount_root = st.mount_root;
}

async function loadServiceSettings(options?: { silent?: boolean }) {
  await runSettingsLoad(async () => {
    applyServiceStatus(await fetchFuseStatus());
  }, "加载服务设置失败", options);
}

async function loadMounts(options?: { silent?: boolean }) {
  const silent = options?.silent === true;
  if (!silent || !mounts.value.length) listLoading.value = true;
  try {
    mounts.value = await fetchFuseMounts();
  } catch (e) {
    toast.error(`加载挂载列表失败：${getApiErrorMessage(e, "请稍后重试")}`);
  } finally {
    listLoading.value = false;
  }
}

async function loadAll(options?: { silent?: boolean }) {
  await Promise.all([loadServiceSettings(options), loadReadCache(options), loadMounts(options)]);
  if (!options?.silent || !settingsLoaded.value) {
    snapshotDrawerForm();
  }
}

function fuseActionErrorMessage(action: string, e: unknown, name?: string) {
  const detail = getApiErrorMessage(e, `${action}失败`);
  const target = name ? `「${name}」` : "";
  return `${action}${target}失败：${detail}`;
}

function ensureMountAvailableForAction() {
  if (status.value?.compile_support === false) {
    toast.error("当前运行实例未编译 FUSE 支持，请重新构建镜像后再挂载");
    return false;
  }
  if (status.value && !status.value.enabled) {
    toast.warning("本地挂载服务尚未启用，请先在右侧设置中开启");
    return false;
  }
  return true;
}

async function submitForm() {
  if (!form.name.trim()) {
    toast.error("请填写挂载名称");
    return;
  }
  if (!form.root_item_id) {
    toast.error("请选择源目录");
    return;
  }
  const mountPoint = mountPointForName(form.name);
  if (!mountPoint) {
    toast.error("请填写挂载名称");
    return;
  }
  submitting.value = true;
  try {
    const body = {
      name: form.name.trim(),
      account_id: form.account_id,
      root_item_id: form.root_item_id,
      root_path: form.root_path,
      mount_point: mountPoint,
      read_only: true,
      auto_mount: form.auto_mount,
      uid: Number(form.uid),
      gid: Number(form.gid),
      dir_mode: form.dir_mode,
      file_mode: form.file_mode,
      enabled: form.enabled,
    };
    if (editingId.value) {
      await updateFuseMount(editingId.value, body);
      toast.success(`挂载点「${form.name.trim()}」已更新`);
    } else {
      await createFuseMount(body);
      toast.success(`挂载点「${form.name.trim()}」已添加`);
    }
    dialogOpen.value = false;
    await loadMounts({ silent: true });
  } catch (e) {
    toast.error(fuseActionErrorMessage("保存挂载点", e, form.name.trim() || undefined));
  } finally {
    submitting.value = false;
  }
}

async function doMount(row: FuseMount) {
  if (!row.id) return;
  if (!ensureMountAvailableForAction()) return;
  try {
    await mountFuse(row.id);
    toast.success(`挂载点「${row.name}」已挂载`);
    await loadMounts({ silent: true });
  } catch (e) {
    toast.error(fuseActionErrorMessage("挂载", e, row.name));
  }
}

async function doUnmount(row: FuseMount) {
  if (!row.id) return;
  try {
    await unmountFuse(row.id);
    toast.success(`挂载点「${row.name}」已卸载`);
    await loadMounts({ silent: true });
  } catch (e) {
    toast.error(fuseActionErrorMessage("卸载", e, row.name));
  }
}

function mountPayload(row: FuseMount, autoMount?: boolean) {
  return {
    name: row.name,
    account_id: row.account_id,
    root_item_id: row.root_item_id,
    root_path: row.root_path,
    mount_point: row.mount_point,
    read_only: row.read_only,
    auto_mount: autoMount ?? row.auto_mount,
    uid: row.uid,
    gid: row.gid,
    dir_mode: row.dir_mode,
    file_mode: row.file_mode,
    enabled: row.enabled,
  };
}

async function setAutoMount(row: FuseMount, autoMount: boolean) {
  if (!row.id || row.auto_mount === autoMount) return;
  togglingAutoMountId.value = row.id;
  try {
    const updated = await updateFuseMount(row.id, mountPayload(row, autoMount));
    const idx = mounts.value.findIndex((m) => m.id === row.id);
    if (idx >= 0) mounts.value[idx] = updated;
    toast.success(autoMount ? `已为「${row.name}」开启自动挂载` : `已为「${row.name}」关闭自动挂载`);
  } catch (e) {
    toast.error(fuseActionErrorMessage("更新自动挂载", e, row.name));
  } finally {
    togglingAutoMountId.value = null;
  }
}

async function doDelete(row: FuseMount) {
  if (!row.id) return;
  const mountID = row.id;
  if (!canDeleteMount(row)) {
    toast.error("请先卸载后再删除");
    return;
  }
  if (!(await confirm({
    title: "删除挂载点",
    message: `确定删除挂载点「${row.name}」？将同时删除挂载目录。`,
    icon: "trash",
    confirmText: "删除",
    danger: true,
  }))) return;
  try {
    await removeWithDust({
      target: findDustTarget(fuseMountList.value, `fuse-mount-${mountID}`),
      container: fuseMountList.value,
      remove: async () => {
        await deleteFuseMount(mountID);
        mounts.value = mounts.value.filter((item) => item.id !== mountID);
      },
    });
    toast.success(`挂载点「${row.name}」已删除`);
  } catch (e) {
    toast.error(fuseActionErrorMessage("删除挂载点", e, row.name));
  }
}

function onFolderPicked(payload: {
  accountId: number;
  parentId: string;
  path: string;
}) {
  form.account_id = payload.accountId;
  form.root_item_id = normalizeFuseRootItemId(payload.parentId);
  form.root_path = payload.path || "/";
  pickerOpen.value = false;
}

onMounted(async () => {
  await accountsStore.loadAccounts();
  await loadAll();
});

defineExpose({
  openCreate,
  getDirty: () => settingsDrawerOpen.value && drawerDirty.value,
  revertDrawer: revertDrawerForm,
  closeSettingsDrawerSilent: () => {
    settingsDrawerOpen.value = false;
  },
});
</script>

<template>
  <div class="fuse-mgmt">
    <WarningBanner v-if="!settingsLoading && status && !status.compile_support">
      当前运行实例未编译 FUSE 支持，请使用 <code>-tags fuse</code> 构建镜像后再挂载。
    </WarningBanner>

    <AdminTaskTabHeader
      settings-title="本地挂载设置"
      settings-hint="服务开关 · 读缓存"
      @open-settings="openSettingsDrawer"
    >
      <StatCard icon="fa-folder" :value="mounts.length" label="挂载点" tone="blue" />
      <StatCard icon="fa-link" :value="mountedCount" label="已挂载" tone="purple" />
      <StatCard icon="fa-database" :value="readCacheUsageText" label="读缓存占用" tone="amber">
        <template #actions>
          <AppCardActionButton
            icon-class="fas fa-trash-can"
            label="清空读缓存"
            variant="danger"
            icon-only
            :disabled="readCacheClearing"
            title="清空读缓存"
            @click="clearReadCacheDisk"
          />
        </template>
      </StatCard>
    </AdminTaskTabHeader>

    <AdminEmptyState
      v-if="!listLoading && !mounts.length"
      icon="📂"
      title="还没有挂载点"
      description="将云盘目录映射到容器内路径，宿主机 volume 映射后即可本地访问。"
    >
      <AppButton type="button" variant="primary" @click="openCreate">添加第一个挂载点</AppButton>
    </AdminEmptyState>

    <div v-else-if="mounts.length" class="admin-panel-table-wrap fuse-mount-table-wrap">
      <table class="admin-table fuse-mount-table">
        <thead>
          <tr>
            <th>挂载点</th>
            <th class="fuse-mount-table__source-col">源目录</th>
            <th class="fuse-mount-table__path-col">容器路径</th>
            <th class="fuse-mount-table__auto-col">自动挂载</th>
            <th class="fuse-mount-table__actions">操作</th>
          </tr>
        </thead>
        <tbody ref="fuseMountList">
          <tr v-for="row in mounts" :key="row.id" class="fuse-mount-row" :data-dust-key="`fuse-mount-${row.id}`">
            <td>
              <div class="fuse-mount-main">
                <div class="fuse-mount-name">
                  <span class="fuse-mount-name__text">{{ row.name }}</span>
                  <AdminStatusPill :tone="mountStatusTone(row.state)">
                    {{ stateLabel[row.state] || row.state }}
                  </AdminStatusPill>
                </div>
                <div v-if="row.last_error" class="fuse-error">{{ row.last_error }}</div>
              </div>
            </td>
            <td class="fuse-mount-source fuse-mount-table__source-col" :title="`${row.account_name || row.account_id} ${row.root_path || row.root_item_id}`">
              <span class="fuse-mount-source__account">{{ row.account_name || row.account_id }}</span>
              <span class="fuse-mount-source__path">{{ row.root_path || row.root_item_id }}</span>
            </td>
            <td class="fuse-mount-table__path-col"><code class="fuse-mount-path">{{ row.mount_point }}</code></td>
            <td class="fuse-mount-auto-cell">
              <AdminEnableToggle
                :enabled="row.auto_mount"
                aria-label="自动挂载"
                on-label="是"
                off-label="否"
                on-title="开启自动挂载"
                off-title="关闭自动挂载"
                :disabled="togglingAutoMountId === row.id"
                @enable="setAutoMount(row, $event)"
              />
            </td>
            <td class="admin-table__actions">
              <AdminRowActions>
                <div class="fuse-mount-actions">
                  <AdminTableActionBtn
                    v-if="!isActiveMountState(row.state)"
                    icon="play"
                    title="挂载"
                    @click="doMount(row)"
                  />
                  <AdminTableActionBtn
                    v-else
                    icon="stop"
                    title="卸载"
                    danger
                    @click="doUnmount(row)"
                  />
                  <AdminTableActionBtn icon="edit" title="编辑" @click="openEdit(row)" />
                  <AdminTableActionBtn
                    icon="delete"
                    title="删除"
                    danger
                    :disabled="!canDeleteMount(row)"
                    @click="doDelete(row)"
                  />
                </div>
                <template #menu>
                  <button
                    v-if="!isActiveMountState(row.state)"
                    type="button"
                    class="admin-row-actions__item"
                    @click="doMount(row)"
                  >
                    挂载
                  </button>
                  <button
                    v-else
                    type="button"
                    class="admin-row-actions__item admin-row-actions__item--danger"
                    @click="doUnmount(row)"
                  >
                    卸载
                  </button>
                  <button type="button" class="admin-row-actions__item" @click="openEdit(row)">编辑</button>
                  <button
                    type="button"
                    class="admin-row-actions__item admin-row-actions__item--danger"
                    :disabled="!canDeleteMount(row)"
                    @click="doDelete(row)"
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

    <AppModal
      :open="dialogOpen"
      :title="editingId ? '编辑挂载点' : '添加挂载点'"
      @close="dialogOpen = false"
    >
      <div class="fuse-form">
        <FormField label="挂载名称">
          <AppInput v-model="form.name" :placeholder="namePlaceholder" />
        </FormField>

        <FormField label="源目录">
          <AccountFolderField
            :display="sourceDirDisplay"
            :title="sourceDirTitle"
            @browse="pickerOpen = true"
          />
        </FormField>

        <p class="fuse-form__note">当前支持同网盘内新建文件夹、重命名、移动、删除及本地文件上传；复制(上传)成功后请去前台查看上传进度</p>

        <FormField label="启动时自动挂载">
          <SettingsBoolSegment v-model="form.auto_mount" label="自动挂载" />
        </FormField>

        <button type="button" class="fuse-advanced-toggle" @click="showAdvanced = !showAdvanced">
          {{ showAdvanced ? "收起高级设置" : "高级设置" }}
        </button>

        <template v-if="showAdvanced">
          <div class="fuse-form__row">
            <FormField label="UID">
              <AppInput v-model="form.uid" type="number" />
            </FormField>
            <FormField label="GID">
              <AppInput v-model="form.gid" type="number" />
            </FormField>
          </div>
          <div class="fuse-form__row">
            <FormField label="目录权限">
              <AppInput v-model="form.dir_mode" placeholder="0755" />
            </FormField>
            <FormField label="文件权限">
              <AppInput v-model="form.file_mode" placeholder="0644" />
            </FormField>
          </div>
        </template>

        <div class="modal-form__footer">
          <AppButton variant="primary" :loading="submitting" @click="submitForm">
            {{ editingId ? "保存" : "添加挂载点" }}
          </AppButton>
        </div>
      </div>
    </AppModal>

    <FolderPickerModal
      :open="pickerOpen"
      selectable-account
      :accounts="accounts"
      @close="pickerOpen = false"
      @resolve="onFolderPicked"
    />

    <AdminSettingsDrawer
      :open="settingsDrawerOpen"
      title="本地挂载设置"
      :saving="drawerSaving"
      :can-save="drawerCanSave"
      @close="closeSettingsDrawer"
      @cancel="closeSettingsDrawer"
      @save="saveDrawerSettings"
    >
      <div v-if="settingsLoading" class="settings-card__loading">加载中…</div>
      <template v-else>
        <SettingsCard title="服务设置" accent="var(--brand)">
          <SettingsRow
            :show-changed-badge="true"
            :changed="isDrawerFieldChanged('service_enabled')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>启用本地挂载</span>
                <SettingsHelpTooltip title="本地挂载说明">
                  <p>通过 Linux FUSE 将云盘目录映射到容器内路径，宿主机映射 volume 后可在 NAS 本地访问。</p>
                  <p>当前版本已支持：<strong>新建目录、同挂载点内移动、重命名、移动并重命名、删除、本地文件复制进挂载目录后转普通上传任务</strong>。</p>
                  <p>上传任务会出现在前台传输面板；暂不支持覆盖写入。</p>
                  <p>需使用 <code>-tags fuse</code> 构建的 Docker 镜像；未编译 FUSE 时无法实际挂载。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <SettingsBoolSegment v-model="drawerForm.service_enabled" label="启用本地挂载" />
            </template>
          </SettingsRow>
          <SettingsRow :show-changed-badge="true" :changed="isDrawerFieldChanged('mount_root')">
            <template #info>
              <div class="settings-row__label">
                <span>挂载根目录</span>
                <SettingsHelpTooltip title="挂载根目录说明">
                  <p>所有挂载点必须位于此目录之下，当前为 <code>{{ mountRoot }}</code>。</p>
                  <p>留空则使用环境变量 <code>LITEPAN_MOUNT_ROOT</code> 或默认值；填写后以此为准。</p>
                  <p>修改后需重启程序生效：Docker 重启容器，本机/飞牛重启进程。</p>
                  <p>Docker 请在 Compose 中映射该路径（建议 <code>:shared</code> 挂载传播）；飞牛请在应用设置中添加文件权限。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <InputActionField>
                <AppInput
                  v-model="drawerForm.mount_root"
                  class="fuse-mount-root-input"
                  placeholder="留空则使用环境变量或默认值"
                />
              </InputActionField>
            </template>
          </SettingsRow>
        </SettingsCard>

        <SettingsCard title="读缓存" accent="var(--brand)">
          <SettingsRow
            :show-changed-badge="true"
            :changed="isDrawerFieldChanged('read_cache_enabled')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>启用读缓存</span>
                <SettingsHelpTooltip title="FUSE 读缓存说明">
                  <p>将 FUSE 读取过的文件块写入独立磁盘目录，与元数据缓存无关。</p>
                  <p>请在 Docker 中将 <code>{{ readCacheRootPath }}</code> 映射到宿主机目录。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <SettingsBoolSegment v-model="drawerForm.read_cache_enabled" label="启用读缓存" />
            </template>
          </SettingsRow>
          <SettingsRow
            :show-changed-badge="true"
            :changed="isDrawerFieldChanged('max_gb')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>容量上限（GB）</span>
                <SettingsHelpTooltip title="容量上限说明">
                  <p>读缓存可占用的磁盘空间上限，达到后会按淘汰策略释放空间。</p>
                  <p>与元数据缓存无关，仅缓存 FUSE 实际读取过的文件块。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <InputActionField>
                <AppInput v-model="drawerForm.max_gb" type="number" min="1" max="500" />
              </InputActionField>
            </template>
          </SettingsRow>
          <SettingsRow
            :show-changed-badge="true"
            :changed="isDrawerFieldChanged('retention_days')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>保留时间（天）</span>
                <SettingsHelpTooltip title="保留时间说明">
                  <p>缓存块超过该天数后会被自动删除，即使尚未占满容量上限。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <InputActionField>
                <AppInput v-model="drawerForm.retention_days" type="number" min="1" max="90" />
              </InputActionField>
            </template>
          </SettingsRow>
          <SettingsRow
            :show-changed-badge="true"
            :changed="isDrawerFieldChanged('eviction_policy')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>淘汰策略</span>
                <SettingsHelpTooltip title="淘汰策略说明">
                  <p>读缓存占满容量上限时，按此规则选择要删除的缓存块。</p>
                  <p><strong>LRU</strong>：优先删除最久未被读取的块。</p>
                  <p><strong>大文件优先</strong>：优先删除占用空间较大的块，以便更快腾出空间。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <div class="field-select">
                <AppSelect v-model="drawerForm.eviction_policy" :options="evictionPolicyOptions" />
              </div>
            </template>
          </SettingsRow>
          <SettingsRow>
            <template #info>
              <div class="settings-row__label">
                <span>缓存目录</span>
                <SettingsHelpTooltip title="缓存目录说明">
                  <p>读缓存块写入的容器内路径，默认为 <code>{{ readCacheRootPath }}</code>。</p>
                  <p>请在 Docker Compose 中将该目录映射到宿主机，以便持久化并在需要时手动清理。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <InputActionField>
                <AppInput class="fuse-mount-root-input" :model-value="readCacheRootPath" readonly />
              </InputActionField>
            </template>
          </SettingsRow>
        </SettingsCard>
      </template>
    </AdminSettingsDrawer>
  </div>
</template>

<style scoped>
.fuse-mgmt {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.fuse-mount-root-input {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.fuse-mount-table-wrap {
  overflow: visible;
}

.fuse-mount-table {
  table-layout: fixed;
}

.fuse-mount-table th:nth-child(1),
.fuse-mount-table td:nth-child(1) {
  width: 20%;
}

.fuse-mount-table th:nth-child(2),
.fuse-mount-table td:nth-child(2) {
  width: 30%;
}

.fuse-mount-table th:nth-child(3),
.fuse-mount-table td:nth-child(3) {
  width: 24%;
}

.fuse-mount-table th:nth-child(4),
.fuse-mount-table td:nth-child(4) {
  width: 10%;
  text-align: center;
}

.fuse-mount-table th:last-child,
.fuse-mount-table td:last-child {
  width: 16%;
  text-align: center;
}

.fuse-mount-row {
  transition: background-color 0.18s ease;
}

.fuse-mount-row:hover {
  background: color-mix(in srgb, var(--brand) 3%, var(--surface));
}

.fuse-mount-main {
  min-width: 0;
}

.fuse-mount-name {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.fuse-mount-name__text {
  font-weight: 700;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fuse-mount-source {
  min-width: 0;
}

.fuse-mount-source__account {
  display: block;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fuse-mount-source__path {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fuse-mount-path {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  word-break: break-all;
}

.fuse-mount-auto-cell {
  text-align: center;
}

.fuse-mount-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 6px;
}

.fuse-error {
  margin-top: 6px;
  font-size: 12px;
  color: var(--danger);
  word-break: break-word;
}

.fuse-mount-actions .admin-table__action-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

@media (max-width: 720px) {
  .fuse-mount-table__source-col,
  .fuse-mount-table__path-col {
    display: none;
  }

  .fuse-mount-table th,
  .fuse-mount-table td {
    padding: 10px 8px;
  }

  .fuse-mount-table th:nth-child(1),
  .fuse-mount-table td:nth-child(1) {
    width: auto;
  }

  .fuse-mount-table th:nth-child(4),
  .fuse-mount-table td:nth-child(4) {
    width: 82px;
    text-align: center;
  }

  .fuse-mount-table th:last-child,
  .fuse-mount-table td:last-child {
    width: 48px;
    text-align: right;
  }
}

.fuse-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 420px;
}

.fuse-form__note {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted);
}

.fuse-form__row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.fuse-advanced-toggle {
  background: none;
  border: none;
  color: var(--brand);
  cursor: pointer;
  padding: 0;
  text-align: left;
  font-size: 13px;
}

@media (max-width: 720px) {
  .fuse-form {
    /* 手机小屏去除固定最小宽度，避免表单横向溢出被裁 */
    min-width: 0;
  }

  .fuse-form__row {
    grid-template-columns: 1fr;
  }
}
</style>
