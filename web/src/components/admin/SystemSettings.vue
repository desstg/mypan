<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchSystemConfig,
  updateCredentials,
} from "@/api/auth";
import {
  fetchSettings,
  saveSettings,
  type SettingCategory,
  type SettingItem,
} from "@/api/settings";
import { toast } from "@/composables/useToast";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { useAuthStore } from "@/stores/auth";
import { useAccountsStore } from "@/stores/accounts";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import ApiKeySettings from "@/components/admin/ApiKeySettings.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { isCacheSettingKey } from "@/constants/cacheSettings";
import { getSkinPref, previewSkin, restoreSavedSkin, setSkinPref, type SkinPref } from "@/utils/theme";

const BackupRestorePanel = defineAsyncComponent(() => import("@/components/admin/BackupRestorePanel.vue"));

const props = withDefaults(
  defineProps<{
    forcePasswordChange?: boolean;
    passwordChangeReason?: string;
  }>(),
  { forcePasswordChange: false, passwordChangeReason: "" },
);

const emit = defineEmits<{ "password-updated": []; "admin-ui-updated": [] }>();

const SECURITY_TAB = "security";
const HOMEPAGE_TAB = "homepage";
const SERVICE_TAB = "services";
const API_KEYS_TAB = "apiKeys";

const TASK_PANEL_SETTING_KEYS = new Set([
  "upload_task_concurrency",
  "builtin_offline_temp_dir",
  "builtin_offline_max_speed_mb",
  "builtin_offline_bt_port",
]);

const SKIN_OPTIONS: { id: SkinPref; label: string; desc: string }[] = [
  { id: "default", label: "经典主题", desc: "现行品牌风格，支持深色模式与顶栏光效。" },
  { id: "brutal", label: "野兽风格", desc: "粗黑边 + 硬阴影 + 直角的高对比风格，不随深色模式变化。" },
];
const skinDraft = ref<SkinPref>(getSkinPref());
const skinSaved = ref<SkinPref>(getSkinPref());

function changeSkin(id: SkinPref) {
  skinDraft.value = id;
  previewSkin(id);
}

function commitSkinDraft() {
  if (skinDraft.value === skinSaved.value) return;
  setSkinPref(skinDraft.value);
  skinSaved.value = skinDraft.value;
}

function revertSkinDraft() {
  if (skinDraft.value === skinSaved.value) return;
  skinDraft.value = skinSaved.value;
  restoreSavedSkin();
}

const ACCENTS = ["var(--brand)", "#f59e0b", "#10b981", "#6366f1", "#ec4899"];

const auth = useAuthStore();
const accountsStore = useAccountsStore();
const backupRestoreRef = ref<{ openImport: () => void } | null>(null);
const canBootstrapRestore = computed(
  () => props.forcePasswordChange && props.passwordChangeReason === "default_credentials",
);

const ACCOUNT_DISPLAY_SETTING_KEYS = new Set([
  "account_show_profile",
  "account_show_membership",
]);

const { loading, runLoad } = useSettingsLoad();
useAdminPageLoading("settings", loading);
const saving = ref(false);
const categories = ref<SettingCategory[]>([]);
const items = ref<SettingItem[]>([]);
const settingsLoaded = ref(false);
const form = reactive<Record<string, string>>({});
const original = reactive<Record<string, string>>({});

const newPassword = ref("");
const confirmPassword = ref("");
const securityForm = reactive({
  admin_username: "admin",
  session_timeout: "2",
  public_index_enabled: false,
});
const securityOriginal = reactive({
  admin_username: "admin",
  session_timeout: "2",
  public_index_enabled: false,
});
const homepageForm = reactive({
  index_account_switch_mode: "dropdown" as "dropdown" | "floating",
  compact_home_enabled: false,
  admin_home_return_mode: "top_icon" as "sidebar" | "top_icon",
  header_effects_enabled: true,
  index_strm_auto_detect_enabled: true,
});
const homepageOriginal = reactive({
  index_account_switch_mode: "dropdown" as "dropdown" | "floating",
  compact_home_enabled: false,
  admin_home_return_mode: "top_icon" as "sidebar" | "top_icon",
  header_effects_enabled: true,
  index_strm_auto_detect_enabled: true,
});
const apiKeySettingsRef = ref<InstanceType<typeof ApiKeySettings> | null>(null);
const apiKeyToolbar = reactive({ loading: true, keyCount: 0, maxKeys: 10 });
const apiKeyAddDisabled = computed(
  () => apiKeyToolbar.loading || apiKeyToolbar.keyCount >= apiKeyToolbar.maxKeys,
);

const tabs = computed(() => [
  { key: SECURITY_TAB, label: "账号安全" },
  { key: HOMEPAGE_TAB, label: "首页设置" },
  { key: SERVICE_TAB, label: "其他设置", disabled: props.forcePasswordChange },
  { key: API_KEYS_TAB, label: "API 秘钥", disabled: props.forcePasswordChange },
]);

const systemItems = computed(() => items.value.filter((it) => it.category === "system"));
const accountDisplayItems = computed(() => items.value.filter((it) => it.category === "account_display"));
const announcementItems = computed(() => items.value.filter((it) => it.category === "announcement"));

// 代理密码单独管：它是 Sensitive，后端回传的是掩码 ******，
// 通用脏判断会把掩码当成「用户改过了」，一保存就把字面量 ****** 写进库。
// 所以它不进 form，改用一个 draft + 「留空表示不修改」的约定。
const PROXY_PASSWORD_KEY = "proxy_password";
const proxyItems = computed(() => items.value.filter((it) => it.category === "proxy"));
const proxyItemsForForm = computed(() => proxyItems.value.filter((it) => it.key !== PROXY_PASSWORD_KEY));
const proxyPasswordDraft = ref("");
const proxyPasswordSet = computed(() =>
  proxyItems.value.some((it) => it.key === PROXY_PASSWORD_KEY && it.value !== ""),
);

const otherSettingsItems = computed(() => [
  ...systemItems.value,
  ...accountDisplayItems.value,
  ...proxyItemsForForm.value,
  ...announcementItems.value,
]);
const systemChangedKeys = computed(() => otherSettingsItems.value.filter((it) => isChanged(it)).map((it) => it.key));
const systemChangedCount = computed(() => systemChangedKeys.value.length);
// 「网络代理」排在「后台公告」前面 —— 用户要的位置就是公告地址上面。
const serviceSettingsCards = computed(() => [
  { key: "auth-log", title: "授权与日志", items: systemItems.value },
  { key: "account-display", title: "网盘账号显示", items: accountDisplayItems.value },
  { key: "proxy", title: "网络代理", items: proxyItems.value },
  { key: "announcement", title: "后台公告", items: announcementItems.value },
].filter((group) => group.items.length > 0));

const passwordsMismatch = computed(
  () =>
    Boolean(newPassword.value && confirmPassword.value) &&
    newPassword.value !== confirmPassword.value,
);

const securityDirty = computed(
  () =>
    securityForm.admin_username !== securityOriginal.admin_username ||
    securityForm.session_timeout !== securityOriginal.session_timeout ||
    securityForm.public_index_enabled !== securityOriginal.public_index_enabled ||
    newPassword.value !== "" ||
    confirmPassword.value !== "",
);

const homepageDirty = computed(
  () =>
    homepageForm.index_account_switch_mode !== homepageOriginal.index_account_switch_mode ||
    homepageForm.compact_home_enabled !== homepageOriginal.compact_home_enabled ||
    homepageForm.admin_home_return_mode !== homepageOriginal.admin_home_return_mode ||
    homepageForm.header_effects_enabled !== homepageOriginal.header_effects_enabled ||
    homepageForm.index_strm_auto_detect_enabled !== homepageOriginal.index_strm_auto_detect_enabled ||
    skinDraft.value !== skinSaved.value,
);

const servicesDirty = computed(() => systemChangedCount.value > 0 || proxyPasswordDraft.value !== "");

function revertSecurityDraft() {
  securityForm.admin_username = securityOriginal.admin_username;
  securityForm.session_timeout = securityOriginal.session_timeout;
  newPassword.value = "";
  confirmPassword.value = "";
}

function revertHomepageDraft() {
  Object.assign(homepageForm, homepageOriginal);
  revertSkinDraft();
}

function revertServicesDraft() {
  for (const it of otherSettingsItems.value) form[it.key] = original[it.key];
  proxyPasswordDraft.value = "";
}

function isTabDirty(tab: string): boolean {
  if (tab === SECURITY_TAB) return securityDirty.value;
  if (tab === HOMEPAGE_TAB) return homepageDirty.value;
  if (tab === SERVICE_TAB) return servicesDirty.value;
  return false;
}

function revertCurrentTab(tab = activeTab.value) {
  if (tab === SECURITY_TAB) revertSecurityDraft();
  else if (tab === HOMEPAGE_TAB) revertHomepageDraft();
  else if (tab === SERVICE_TAB) revertServicesDraft();
}

const settingsDirty = computed(
  () => securityDirty.value || homepageDirty.value || servicesDirty.value,
);
const { activeTab, setActiveTab } = useSectionTabRoute(
  SECURITY_TAB,
  [SECURITY_TAB, HOMEPAGE_TAB, SERVICE_TAB, API_KEYS_TAB],
  {
    beforeTabChange: async (from, to) => {
      if (props.forcePasswordChange && to !== SECURITY_TAB) return false;
      if (!isTabDirty(from)) return true;
      return confirmDiscardChanges(() => isTabDirty(from));
    },
  },
);
const { confirmDiscardChanges } = useSettingsPageDirty(settingsDirty, revertCurrentTab);

const isSecurityTab = computed(() => activeTab.value === SECURITY_TAB);
const isHomepageTab = computed(() => activeTab.value === HOMEPAGE_TAB);
const isServicesTab = computed(() => activeTab.value === SERVICE_TAB);
const isApiKeysTab = computed(() => activeTab.value === API_KEYS_TAB);
const accentColor = computed(() => {
  if (isSecurityTab.value) return ACCENTS[0];
  if (isHomepageTab.value) return ACCENTS[1];
  if (isServicesTab.value) return ACCENTS[2];
  if (isApiKeysTab.value) return ACCENTS[3];
  return ACCENTS[0];
});

const canSave = computed(() => {
  if (isSecurityTab.value) return securityDirty.value && !passwordsMismatch.value;
  if (isHomepageTab.value) return homepageDirty.value;
  if (isServicesTab.value) return servicesDirty.value;
  return false;
});

function isChanged(it: SettingItem): boolean {
  return form[it.key] !== original[it.key];
}

function displayLabel(it: SettingItem): string {
  if (it.type === "int" && it.unit) return `${it.label}（${it.unit}）`;
  return it.label;
}

function filterOutCacheSettings(payload: {
  categories: SettingCategory[];
  items: SettingItem[];
}) {
  const visibleItems = (payload.items ?? []).filter(
    (it) => !isCacheSettingKey(it.key) && !TASK_PANEL_SETTING_KEYS.has(it.key),
  );
  const visibleCatIds = new Set(visibleItems.map((it) => it.category));
  const visibleCategories = (payload.categories ?? []).filter((c) => visibleCatIds.has(c.id));
  return { categories: visibleCategories, items: visibleItems };
}

function applyPayload(payload: { categories: SettingCategory[]; items: SettingItem[] }) {
  const filtered = filterOutCacheSettings(payload);
  categories.value = filtered.categories;
  items.value = filtered.items;
  settingsLoaded.value = true;
  for (const it of items.value) {
    // 代理密码是 Sensitive，后端回传的是掩码；塞进 form 会被通用脏判断当成用户改动。
    // 它由 proxyPasswordDraft 单独管（见上面的 PROXY_PASSWORD_KEY）。
    if (it.key === PROXY_PASSWORD_KEY) continue;
    form[it.key] = it.value;
    original[it.key] = it.value;
  }
}

function applySystemConfig(config: {
  admin_username: string;
  session_timeout: number;
  public_index_enabled: boolean;
  index_account_switch_mode?: string;
  compact_home_enabled?: boolean;
  admin_home_return_mode?: string;
  header_effects_enabled?: boolean;
  index_strm_auto_detect_enabled?: boolean;
}) {
  securityForm.admin_username = config.admin_username || "admin";
  securityForm.session_timeout = String(config.session_timeout || 2);
  securityOriginal.admin_username = securityForm.admin_username;
  securityOriginal.session_timeout = securityForm.session_timeout;
  securityForm.public_index_enabled = config.public_index_enabled ?? false;
  securityOriginal.public_index_enabled = securityForm.public_index_enabled;
  const mode = config.index_account_switch_mode === "floating" ? "floating" : "dropdown";
  homepageForm.index_account_switch_mode = mode;
  homepageOriginal.index_account_switch_mode = mode;
  homepageForm.compact_home_enabled = config.compact_home_enabled ?? false;
  homepageOriginal.compact_home_enabled = homepageForm.compact_home_enabled;
  const homeReturn = config.admin_home_return_mode === "sidebar" ? "sidebar" : "top_icon";
  homepageForm.admin_home_return_mode = homeReturn;
  homepageOriginal.admin_home_return_mode = homeReturn;
  homepageForm.header_effects_enabled = config.header_effects_enabled ?? true;
  homepageOriginal.header_effects_enabled = homepageForm.header_effects_enabled;
  homepageForm.index_strm_auto_detect_enabled = config.index_strm_auto_detect_enabled ?? true;
  homepageOriginal.index_strm_auto_detect_enabled = homepageForm.index_strm_auto_detect_enabled;
}

async function loadSystemConfig() {
  const config = await fetchSystemConfig();
  applySystemConfig(config);
  if (props.forcePasswordChange || config.must_change_password) {
    activeTab.value = SECURITY_TAB;
  }
}

async function loadSettings() {
  applyPayload(await fetchSettings());
}

async function load() {
  await runLoad(async () => {
    if (props.forcePasswordChange) {
      await loadSystemConfig();
    } else {
      await Promise.all([loadSettings(), loadSystemConfig()]);
    }
  }, "加载设置失败");
}

onMounted(load);

watch(
  () => props.forcePasswordChange,
  async (locked, prevLocked) => {
    if (locked) {
      activeTab.value = SECURITY_TAB;
      return;
    }
    if (prevLocked && !settingsLoaded.value) {
      await runLoad(async () => {
        await loadSettings();
      }, "加载设置失败");
    }
  },
);
onBeforeUnmount(() => {
  revertSkinDraft();
});

async function saveSecurity() {
  if (props.forcePasswordChange && !newPassword.value) {
    toast.error("当前管理员密码需要升级，请先设置新密码");
    return;
  }
  if (newPassword.value && !confirmPassword.value) {
    toast.error("请再次输入新密码进行确认");
    return;
  }
  if (passwordsMismatch.value) {
    toast.error("两次输入的密码不一致");
    return;
  }
  const sessionTimeout = Number.parseFloat(securityForm.session_timeout);
  if (!Number.isFinite(sessionTimeout) || sessionTimeout < 0.5 || sessionTimeout > 24) {
    toast.error("会话超时时间必须在 0.5-24 小时之间");
    return;
  }

  saving.value = true;
  try {
    await updateCredentials({
      admin_username: securityForm.admin_username.trim(),
      admin_password: newPassword.value || undefined,
      session_timeout: sessionTimeout,
      public_index_enabled: securityForm.public_index_enabled,
    });
    const passwordUpdated = Boolean(newPassword.value);
    newPassword.value = "";
    confirmPassword.value = "";
    toast.success("账号与安全设置已保存");
    await loadSystemConfig();
    if (passwordUpdated) emit("password-updated");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function saveHomepage() {
  saving.value = true;
  try {
    await updateCredentials({
      admin_username: securityForm.admin_username.trim(),
      index_account_switch_mode: homepageForm.index_account_switch_mode,
      compact_home_enabled: homepageForm.compact_home_enabled,
      admin_home_return_mode: homepageForm.admin_home_return_mode,
      header_effects_enabled: homepageForm.header_effects_enabled,
      index_strm_auto_detect_enabled: homepageForm.index_strm_auto_detect_enabled,
    });
    localStorage.setItem("litepan:index:compact-home-enabled", homepageForm.compact_home_enabled ? "1" : "0");
    commitSkinDraft();
    toast.success("首页设置已保存");
    await loadSystemConfig();
    await auth.load();
    emit("admin-ui-updated");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function saveServices() {
  if (!servicesDirty.value) return;
  saving.value = true;
  try {
    const changed: Record<string, string> = {};
    for (const key of systemChangedKeys.value) changed[key] = form[key];
    // 代理密码只在用户真的敲了新值时才提交 —— 留空表示保持原值。
    // 掩码 ****** 永远不会走到这里，因为它压根没进 form。
    const proxyPassword = proxyPasswordDraft.value.trim();
    if (proxyPassword !== "") changed[PROXY_PASSWORD_KEY] = proxyPassword;
    const accountDisplayChanged = Object.keys(changed).some((key) => ACCOUNT_DISPLAY_SETTING_KEYS.has(key));
    if (Object.keys(changed).length > 0) {
      applyPayload(await saveSettings(changed));
      proxyPasswordDraft.value = "";
    }
    if (accountDisplayChanged) await accountsStore.loadAccounts();
    toast.success("其他设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function submit() {
  if (isSecurityTab.value) {
    await saveSecurity();
    return;
  }
  if (isHomepageTab.value) {
    await saveHomepage();
    return;
  }
  if (isServicesTab.value) {
    await saveServices();
  }
}
</script>

<template>
  <div class="settings">
    <SectionTabBar :model-value="activeTab" :tabs="tabs" @update:model-value="setActiveTab">
      <template #actions>
        <AppButton
          v-if="isApiKeysTab"
          type="button"
          variant="primary"
          :disabled="apiKeyAddDisabled"
          @click="apiKeySettingsRef?.openCreate()"
        >
          新增秘钥
        </AppButton>
        <AppButton
          v-else
          type="button"
          variant="primary"
          :disabled="!canSave || saving"
          @click="submit"
        >
          {{ saving ? "保存中…" : "保存改动" }}
        </AppButton>
      </template>
    </SectionTabBar>

    <template v-if="!loading">
      <div v-if="isSecurityTab && canBootstrapRestore" class="bootstrap-restore-card">
        <div class="bootstrap-restore-card__icon">
          <SvgIcon name="fa-database" :size="24" />
        </div>
        <div class="bootstrap-restore-card__copy">
          <strong>已有 MyPan 备份？</strong>
          <span>可以直接导入旧备份并恢复，无需先修改默认密码。</span>
        </div>
        <AppButton type="button" variant="secondary" @click="backupRestoreRef?.openImport()">
          导入备份
        </AppButton>
      </div>
      <BackupRestorePanel
        v-if="canBootstrapRestore"
        ref="backupRestoreRef"
        bootstrap
      />

      <SettingsCard v-if="isSecurityTab" title="账号安全" :accent="accentColor">
        <SettingsRow
          :show-changed-badge="true"
          :changed="securityForm.admin_username !== securityOriginal.admin_username"
        >
          <template #info>
            <div class="settings-row__label">
              <span>管理员用户名</span>
            </div>
          </template>
          <template #control>
            <div class="field-text">
              <AppInput
                v-model="securityForm.admin_username"
                placeholder="admin"
                autocomplete="username"
              />
            </div>
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="Boolean(newPassword)">
          <template #info>
            <div class="settings-row__label">
              <span>新密码</span>
            </div>
          </template>
          <template #control>
            <div class="field-text">
              <AppInput
                v-model="newPassword"
                type="password"
                placeholder="留空表示不修改"
                autocomplete="new-password"
                ignore-autofill
              />
            </div>
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="Boolean(newPassword)">
          <template #info>
            <div class="settings-row__label">
              <span>确认新密码</span>
            </div>
          </template>
          <template #control>
            <div class="field-text">
              <AppInput
                v-model="confirmPassword"
                type="password"
                placeholder="再次输入新密码"
                autocomplete="new-password"
                ignore-autofill
              />
              <p v-if="passwordsMismatch" class="field-error">两次输入的密码不一致</p>
            </div>
          </template>
        </SettingsRow>

        <SettingsRow
          :show-changed-badge="true"
          :changed="securityForm.session_timeout !== securityOriginal.session_timeout"
        >
          <template #info>
            <div class="settings-row__label">
              <span>会话超时时间（小时）</span>
            </div>
          </template>
          <template #control>
            <div class="field-num">
              <AppInput v-model="securityForm.session_timeout" type="number" placeholder="2" />
            </div>
          </template>
        </SettingsRow>

        <SettingsRow
          :show-changed-badge="true"
          :changed="securityForm.public_index_enabled !== securityOriginal.public_index_enabled"
        >
          <template #info>
            <div class="settings-row__label">
              <span>允许匿名访问文件列表</span>
              <SettingsHelpTooltip title="匿名文件列表访问说明">
                <p>开启后，访客无需登录即可访问首页并浏览文件列表。</p>
                <p>关闭后，未登录用户访问首页会自动跳转到登录页，匿名公开接口也会同时拒绝访问。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment
              v-model="securityForm.public_index_enabled"
              label="允许匿名访问文件列表"
              off-label="不允许"
              on-label="允许"
            />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard v-if="isHomepageTab" title="STRM" :accent="accentColor">
        <SettingsRow
          :show-changed-badge="true"
          :changed="homepageForm.index_strm_auto_detect_enabled !== homepageOriginal.index_strm_auto_detect_enabled"
        >
          <template #info>
            <div class="settings-row__label">
              <span>自动检测 STRM 变化</span>
              <SettingsHelpTooltip title="自动检测 STRM 变化说明">
                <p>开启后，管理员进入已配置 STRM 任务的目录时，会自动检测本地未生成的 STRM 与元数据，并在文件列表上方提示是否立即生成。</p>
                <p>关闭后，进入目录不再发起检测请求，可节省开销；如需同步，请依赖 STRM 定时任务或在 STRM 管理页手动触发扫描。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment
              v-model="homepageForm.index_strm_auto_detect_enabled"
              label="自动检测 STRM 变化"
              off-label="关闭"
              on-label="打开"
            />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard v-if="isHomepageTab" title="界面显示" :accent="accentColor">
        <SettingsRow
          :show-changed-badge="true"
          :changed="homepageForm.compact_home_enabled !== homepageOriginal.compact_home_enabled"
        >
          <template #info>
            <div class="settings-row__label">
              <span>简约首页</span>
              <SettingsHelpTooltip title="简约首页说明">
                <p>开启后，首页文件列表导航栏和工具栏合并，且工具栏改为图标简约样式，性能和任务面板移至底部。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment
              v-model="homepageForm.compact_home_enabled"
              label="简约首页"
              off-label="关闭"
              on-label="开启"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="skinDraft !== skinSaved">
          <template #info>
            <div class="settings-row__label">
              <span>主题风格</span>
              <SettingsHelpTooltip title="主题风格说明">
                <p>切换整站视觉风格，保存后生效并记忆在本机。</p>
                <p><strong>经典主题</strong>：现行品牌风格，支持深色模式与顶栏光效。</p>
                <p><strong>野兽风格</strong>：粗黑边 + 硬阴影 + 直角的高对比风格，不随深色模式变化。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsSegment
              :model-value="skinDraft"
              label="主题风格"
              :options="SKIN_OPTIONS.map((opt) => ({ value: opt.id, label: opt.label }))"
              @update:model-value="changeSkin($event as SkinPref)"
            />
          </template>
        </SettingsRow>

        <SettingsRow
          :show-changed-badge="true"
          :changed="homepageForm.index_account_switch_mode !== homepageOriginal.index_account_switch_mode"
        >
          <template #info>
            <div class="settings-row__label">
              <span>账号切换方式</span>
            </div>
          </template>
          <template #control>
            <SettingsSegment
              v-model="homepageForm.index_account_switch_mode"
              label="账号切换方式"
              :options="[
                { value: 'dropdown', label: '顶栏切换' },
                { value: 'floating', label: '悬浮切换' },
              ]"
            />
          </template>
        </SettingsRow>

        <SettingsRow
          :show-changed-badge="true"
          :changed="homepageForm.header_effects_enabled !== homepageOriginal.header_effects_enabled"
        >
          <template #info>
            <div class="settings-row__label">
              <span>顶栏动效</span>
              <SettingsHelpTooltip title="顶栏动效说明">
                <p>开启后，前台首页顶栏会根据主题显示日光、飞机、粒子或星空流星效果。</p>
                <p>关闭后，顶栏只保留静态渐变背景，不渲染任何装饰动效。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment
              v-model="homepageForm.header_effects_enabled"
              label="顶栏动效"
              off-label="关闭"
              on-label="开启"
            />
          </template>
        </SettingsRow>

        <SettingsRow
          :show-changed-badge="true"
          :changed="homepageForm.admin_home_return_mode !== homepageOriginal.admin_home_return_mode"
        >
          <template #info>
            <div class="settings-row__label">
              <span>首页返回方式</span>
              <SettingsHelpTooltip title="首页返回方式说明">
                <p>控制从后台返回前台首页的入口位置。</p>
                <p><strong>左侧菜单</strong>：在侧栏导航底部显示「返回首页」。</p>
                <p><strong>顶栏图标</strong>：在顶栏右侧显示房子图标。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsSegment
              v-model="homepageForm.admin_home_return_mode"
              label="首页返回方式"
              :options="[
                { value: 'top_icon', label: '顶栏图标' },
                { value: 'sidebar', label: '左侧菜单' },
              ]"
            />
          </template>
        </SettingsRow>
      </SettingsCard>

      <template v-else-if="isServicesTab">
        <SettingsCard
          v-for="group in serviceSettingsCards"
          :key="group.key"
          :title="group.title"
          :accent="accentColor"
        >
          <SettingsRow
            v-for="it in group.items"
            :key="it.key"
            :show-changed-badge="true"
            :changed="isChanged(it)"
          >
            <template #info>
              <div class="settings-row__label">
                <span>{{ displayLabel(it) }}</span>
                <SettingsHelpTooltip
                  v-if="it.key === 'oauth_server_url'"
                  title="OAuth 代理服务地址说明"
                >
                  <p>用于主程序对接 OAuth 认证代理服务，会影响相关驱动的授权、刷新和回调地址处理。</p>
                  <p>添加账号时「自动获取 Token」经此服务转发。留空或无效地址将回落默认值。</p>
                  <p>示例：<strong>https://oauth.litepan.top</strong></p>
                </SettingsHelpTooltip>
                <SettingsHelpTooltip
                  v-else-if="it.key === 'auth_active_refresh_enabled'"
                  title="主动认证刷新说明"
                >
                  <p>程序根据各网盘过期时间提前刷新 token，减少访问时遇到认证过期的概率。</p>
                  <div class="settings-help__section">推荐开启</div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--on" />
                    <span>NAS / Docker / 服务器 24 小时常驻：token 始终健康，缓存、STRM、反代等后台任务更稳定</span>
                  </div>
                  <div class="settings-help__section">推荐关闭</div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--off" />
                    <span>桌面端临时使用、用完即关：无需后台维护 token，访问时被动刷新即可</span>
                  </div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--off" />
                    <span>同一账号还在其他挂载工具或脚本里使用：避免多个程序争抢 refresh_token，尤其是 115 网盘</span>
                  </div>
                </SettingsHelpTooltip>
                <SettingsHelpTooltip
                  v-else-if="it.key === 'announcement_url'"
                  title="公告地址说明"
                >
                  <p>后台公告 JSON 的地址，用于打开后台时的更新通告和侧边栏「关于」弹窗。</p>
                  <div class="settings-help__section">填写要求</div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--on" />
                    <span>由 <b>服务端</b> 拉取，不需要公网可达，内网地址或本机地址都行</span>
                  </div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--off" />
                    <span>必须免认证可访问：程序只发一个普通 GET，不支持自定义请求头或 Token</span>
                  </div>
                  <div class="settings-help__item">
                    <span class="settings-help__dot settings-help__dot--off" />
                    <span>不支持 <code>file://</code>，自签名证书会失败，单文件上限 512KB</span>
                  </div>
                  <p>留空则使用内置默认地址。保存后即刻生效，无需重启。</p>
                </SettingsHelpTooltip>
                <SettingsHelpTooltip
                  v-else-if="it.description"
                  :title="`${displayLabel(it)}说明`"
                >
                  <p>{{ it.description }}</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <div class="settings-row__field">
                <SettingsBoolSegment
                  v-if="it.type === 'bool'"
                  :model-value="form[it.key] === 'true'"
                  :label="displayLabel(it)"
                  @update:model-value="form[it.key] = $event ? 'true' : 'false'"
                />

                <div v-else-if="it.type === 'select'" class="field-select">
                  <AppSelect
                    :model-value="form[it.key]"
                    :options="it.options || []"
                    @update:model-value="form[it.key] = String($event)"
                  />
                </div>

                <div v-else-if="it.type === 'int'" class="field-num">
                  <AppInput v-model="form[it.key]" type="number" :placeholder="it.default" />
                </div>

                <!-- 代理密码：后端只回传掩码，这里按「留空表示不修改」处理，
                     不走通用分支 —— 否则会把 ****** 当成值提交回去。 -->
                <div v-else-if="it.key === PROXY_PASSWORD_KEY" class="field-text">
                  <AppInput
                    v-model="proxyPasswordDraft"
                    type="password"
                    ignore-autofill
                    :placeholder="proxyPasswordSet ? '已设置，留空不修改' : '无认证可留空'"
                  />
                </div>

                <div v-else class="field-text">
                  <AppInput v-model="form[it.key]" :placeholder="it.default" autocomplete="off" />
                </div>
              </div>
            </template>
          </SettingsRow>
        </SettingsCard>
      </template>

      <ApiKeySettings
        v-else-if="isApiKeysTab"
        ref="apiKeySettingsRef"
        :accent="accentColor"
        @toolbar-state="Object.assign(apiKeyToolbar, $event)"
      />
    </template>
  </div>
</template>

<style scoped>
.settings {
  padding-bottom: 24px;
}

.bootstrap-restore-card {
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr) auto;
  align-items: center;
  gap: 14px;
  margin-bottom: 16px;
  padding: 16px 18px;
  border: 1px solid color-mix(in srgb, var(--brand) 24%, var(--border-soft));
  border-radius: var(--radius-md);
  background: linear-gradient(115deg, color-mix(in srgb, var(--brand) 8%, var(--surface)), var(--surface));
}

.bootstrap-restore-card__icon {
  display: grid;
  place-items: center;
  width: 46px;
  height: 46px;
  border-radius: 14px;
  color: var(--brand);
  background: color-mix(in srgb, var(--brand) 12%, var(--surface));
}

.bootstrap-restore-card__copy {
  min-width: 0;
  display: grid;
  gap: 4px;
}

.bootstrap-restore-card__copy strong {
  color: var(--text);
  font-size: 14px;
}

.bootstrap-restore-card__copy span {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.55;
}

.field-error {
  margin: 6px 0 0;
  font-size: 12px;
  color: #dc2626;
  text-align: right;
}

.field-text,
.field-num,
.field-select {
  width: 100%;
}

@media (max-width: 640px) {
  .bootstrap-restore-card {
    grid-template-columns: 42px minmax(0, 1fr);
    padding: 14px;
  }

  .bootstrap-restore-card__icon {
    width: 42px;
    height: 42px;
  }

  .bootstrap-restore-card .btn {
    grid-column: 1 / -1;
    width: 100%;
  }
}

:global([data-skin="brutal"]) .bootstrap-restore-card {
  border: var(--brutal-border-width, 2px) solid var(--text);
  border-radius: 0;
  box-shadow: var(--brutal-shadow, 3px 3px 0 var(--text));
}
</style>
