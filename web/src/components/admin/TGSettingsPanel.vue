<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsRowLabel from "@/components/admin/SettingsRowLabel.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchTGConfig,
  fetchTGQualityProfiles,
  saveTGConfig,
  testTGBot,
} from "@/api/tgSubscribe";
import { useAccountsStore } from "@/stores/accounts";
import { confirm } from "@/composables/useConfirm";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { toast } from "@/composables/useToast";
import type { TGConfig, TGConfigInput, TGQualityProfile } from "@/types/tg-subscribe";

const emit = defineEmits<{ changed: [] }>();

const accountsStore = useAccountsStore();

const ACCENT = "var(--brand)";

const loading = ref(false);
const saving = ref(false);
const testing = ref(false);
const profiles = ref<TGQualityProfile[]>([]);
const status = ref<TGConfig | null>(null);
const loaded = ref(false);

/** 服务端基线，用于脏判断与「还原」。 */
const baseline = ref("");

const draft = reactive<TGConfigInput>({
  enabled: false,
  token: "",
  api_host: "",
  proxy_enabled: false,
  proxy_url: "",
  proxy_username: "",
  proxy_password: "",
  auto_push: false,
  default_account_id: 0,
  default_parent_id: "",
  default_display_path: "",
  default_quality_profile_id: 0,
  collect_window_min: 5,
  max_push_per_hour: 20,
});

const pickerOpen = ref(false);

/**
 * 脏判断只看用户能编辑、且会回传的字段。
 *
 * Token 与代理密码是「留空表示不修改」，所以单独比较草稿值 —— 用户只是打开
 * 面板再关掉时它们都是空串，不会误判成有未保存改动。
 */
function snapshot() {
  return JSON.stringify({
    enabled: draft.enabled,
    api_host: draft.api_host,
    proxy_enabled: draft.proxy_enabled,
    proxy_url: draft.proxy_url,
    proxy_username: draft.proxy_username,
    auto_push: draft.auto_push,
    default_account_id: draft.default_account_id,
    default_parent_id: draft.default_parent_id,
    default_display_path: draft.default_display_path,
    default_quality_profile_id: draft.default_quality_profile_id,
    collect_window_min: draft.collect_window_min,
    max_push_per_hour: draft.max_push_per_hour,
    token: draft.token,
    proxy_password: draft.proxy_password,
  });
}

const isDirty = computed(() => loaded.value && snapshot() !== baseline.value);

const profileOptions = computed(() => [
  { value: 0, label: "跟随默认方案" },
  ...profiles.value.map((p) => ({
    value: p.id,
    label: p.is_default ? `${p.name}（默认）` : p.name,
  })),
]);

/** 账号名 + 目录的展示串。 */
const defaultTargetText = computed(() => {
  if (!draft.default_account_id) return "";
  const account = accountsStore.accounts.find((a) => a.id === draft.default_account_id);
  return `${account?.name ?? `账号 ${draft.default_account_id}`} · ${draft.default_display_path || "/"}`;
});

function applyConfig(cfg: TGConfig) {
  status.value = cfg;
  draft.enabled = cfg.enabled;
  // Token 与代理密码不回传明文，留空即「不改」。
  draft.token = "";
  draft.proxy_password = "";
  draft.api_host = cfg.api_host;
  draft.proxy_enabled = cfg.proxy_enabled;
  draft.proxy_url = cfg.proxy_url;
  draft.proxy_username = cfg.proxy_username;
  draft.auto_push = cfg.auto_push;
  draft.default_account_id = cfg.default_account_id;
  draft.default_parent_id = cfg.default_parent_id;
  draft.default_display_path = cfg.default_display_path;
  draft.default_quality_profile_id = cfg.default_quality_profile_id;
  draft.collect_window_min = cfg.collect_window_min;
  draft.max_push_per_hour = cfg.max_push_per_hour;
  baseline.value = snapshot();
  loaded.value = true;
}

async function load() {
  loading.value = true;
  try {
    const [cfg, list] = await Promise.all([fetchTGConfig(), fetchTGQualityProfiles()]);
    profiles.value = list;
    applyConfig(cfg);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载配置失败"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (!isDirty.value) return;
  saving.value = true;
  try {
    applyConfig(await saveTGConfig({ ...draft }));
    toast.success("配置已保存");
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存配置失败"));
  } finally {
    saving.value = false;
  }
}

function revert() {
  if (status.value) applyConfig(status.value);
}

async function runTest() {
  testing.value = true;
  try {
    const { bot_name } = await testTGBot();
    toast.success(`连接成功：${bot_name}`);
    status.value = await fetchTGConfig();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "连接 Telegram 失败"));
  } finally {
    testing.value = false;
  }
}

function onTargetPicked(payload: { accountId: number; parentId: string; path: string }) {
  draft.default_account_id = payload.accountId;
  draft.default_parent_id = payload.parentId;
  draft.default_display_path = payload.path || "/";
  pickerOpen.value = false;
}

async function toggleAutoPush(next: boolean) {
  // 打开自动推送是「真的会往网盘写东西」的开关，值得确认一次。
  if (next) {
    try {
      await confirm({
        title: "开启自动推送",
        message:
          "开启后，匹配到的资源会按画质方案自动推送到网盘并开始下载。建议先在观察模式下看过匹配历史再打开。",
        confirmText: "确认开启",
        danger: false,
        icon: "warning",
      });
    } catch {
      return;
    }
  }
  draft.auto_push = next;
}

const botStatusText = computed(() => {
  const s = status.value?.status;
  if (s === "ok") return status.value?.bot_name || "已连接";
  if (s === "error") return "连接异常";
  return "未检测";
});

watch(
  () => draft.proxy_enabled,
  (enabled) => {
    if (!enabled) {
      draft.proxy_url = "";
      draft.proxy_username = "";
      draft.proxy_password = "";
    }
  },
);

// 改了没保存就切页时要拦一下，和其余设置页一致。
useSettingsPageDirty(isDirty, revert);

onMounted(async () => {
  await accountsStore.loadAccounts();
  if (!loaded.value) await load();
});

defineExpose(
  bindSettingsPanelExpose({
    isDirty,
    saving,
    save,
    reload: load,
    revert,
  }),
);
</script>

<template>
  <div class="tg-panel">
    <div v-if="loading" class="settings-card__loading">加载中…</div>

    <template v-else>
      <SettingsCard title="Bot 连接" :accent="ACCENT">
        <template #head-aside>
          <span>{{ botStatusText }}</span>
        </template>
        <template #head-actions>
          <AppButton type="button" variant="secondary" size="sm" :disabled="testing" @click="runTest">
            {{ testing ? "测试中…" : "测试连接" }}
          </AppButton>
        </template>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="启用 TG 订阅" />
          </template>
          <template #control>
            <SettingsBoolSegment v-model="draft.enabled" label="启用 TG 订阅" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="Bot Token"
              help-title="Bot Token"
              help-text="在 Telegram 里找 @BotFather 创建 Bot，把拿到的 Token 填在这里。留空表示不修改已保存的值。"
            />
          </template>
          <template #control>
            <AppInput
              v-model="draft.token"
              type="password"
              ignore-autofill
              :placeholder="status?.token_set ? '已设置，留空不修改' : '123456:ABC-DEF...'"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="API 地址"
              help-title="API 地址"
              help-text="留空走官方 api.telegram.org。国内直连不通，可以填自建反代的域名。"
            />
          </template>
          <template #control>
            <AppInput v-model="draft.api_host" placeholder="https://api.telegram.org" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="使用代理"
              help-title="代理说明"
              help-text="Telegram API 与网盘无关，只影响 Bot 连接。"
            />
          </template>
          <template #control>
            <SettingsBoolSegment v-model="draft.proxy_enabled" label="使用代理" />
          </template>
        </SettingsRow>

        <template v-if="draft.proxy_enabled">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="代理地址" help-title="代理地址" help-text="支持 http / https / socks5。" />
            </template>
            <template #control>
              <AppInput v-model="draft.proxy_url" placeholder="http://127.0.0.1:7890" />
            </template>
          </SettingsRow>
          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="代理用户名" help-title="代理用户名" help-text="无认证可留空。" />
            </template>
            <template #control>
              <AppInput v-model="draft.proxy_username" placeholder="留空表示无认证" />
            </template>
          </SettingsRow>
          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="代理密码" help-title="代理密码" help-text="留空表示不修改已保存的值。" />
            </template>
            <template #control>
              <AppInput
                v-model="draft.proxy_password"
                type="password"
                ignore-autofill
                :placeholder="status?.proxy_password_set ? '已设置，留空不修改' : '留空表示无认证'"
              />
            </template>
          </SettingsRow>
        </template>
      </SettingsCard>

      <SettingsCard title="推送" :accent="ACCENT">
        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="自动推送" help-title="自动推送说明">
              <p>关闭时是「观察模式」：只记录匹配历史，不往网盘推任何东西。</p>
              <p>建议先观察一段时间，确认匹配判定符合预期再打开。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <SettingsBoolSegment
              :model-value="draft.auto_push"
              label="自动推送"
              off-label="观察模式"
              @update:model-value="toggleAutoPush"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="默认目标目录" help-title="默认目标目录说明">
              <p>单条订阅没指定目标时用它。</p>
              <p>推送时会在该目录下自动创建「片名 (年份)」子目录，多个版本不会混在一起。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AccountFolderField
              :display="defaultTargetText"
              title="选择默认推送到的网盘目录"
              placeholder="点击选择网盘与目录"
              @browse="pickerOpen = true"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="默认画质方案"
              help-title="默认画质方案说明"
              help-text="新订阅没指定方案时用它。"
            />
          </template>
          <template #control>
            <AppSelect v-model="draft.default_quality_profile_id" :options="profileOptions" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="聚合窗口" help-title="聚合窗口说明">
              <p>命中后先等这么久，把窗口内收到的所有版本一起比较，只推最优的一条。</p>
              <p>填 0 表示收到即推 —— 但那就变成「先到的先推」，先来的 720p 会把后面的 2160p 挤掉。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model.number="draft.collect_window_min" type="number" placeholder="5" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="每小时推送上限"
              help-title="每小时推送上限说明"
              help-text="防止刚打开开关时聚合窗口里堆了几百条候选，一拥而上把网盘 API 打爆。"
            />
          </template>
          <template #control>
            <AppInput v-model.number="draft.max_push_per_hour" type="number" placeholder="20" />
          </template>
        </SettingsRow>
      </SettingsCard>
    </template>

    <FolderPickerModal
      :open="pickerOpen"
      title="选择默认推送目录"
      confirm-text="用这个目录"
      selectable-account
      :accounts="accountsStore.accounts"
      :account-id="draft.default_account_id || null"
      allow-create-folder
      @close="pickerOpen = false"
      @resolve="onTargetPicked"
    />
  </div>
</template>
