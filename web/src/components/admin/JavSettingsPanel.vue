<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsRowLabel from "@/components/admin/SettingsRowLabel.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  addJavServer,
  deleteJavServer,
  fetchJavConfig,
  fetchJavLibraryStats,
  fetchJavServers,
  fetchJavSyncSchedule,
  loginJav,
  saveJavConfig,
  syncJavAll,
  testJavConnection,
  testJavNodes,
  testJavServer,
} from "@/api/jav";
import { useAccountsStore } from "@/stores/accounts";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { toast } from "@/composables/useToast";
import type { JavConfig, JavServer, JavLibraryStats } from "@/types/jav";

/**
 * 「番号相关设置」面板。
 *
 * 对照 emby.png 与源码的设置页，分四个子 tab：
 *   番号开关 / 数据源 / 媒体服务器 / 下载记录（记录在另一个组件里，由抽屉挂载）。
 *
 * 保存走抽屉底部的统一按钮（bindSettingsPanelExpose），子 tab 里不各放一个。
 */
const emit = defineEmits<{ changed: [] }>();

const accountsStore = useAccountsStore();
const pickerOpen = ref(false);

/** 默认推送目标的展示串：账号名 + 目录。 */
const defaultTargetText = computed(() => {
  if (!draft.default_account_id) return "";
  const account = accountsStore.accounts.find((a) => a.id === draft.default_account_id);
  return `${account?.name ?? `账号 ${draft.default_account_id}`} · ${draft.default_display_path || "/"}`;
});

function onTargetPicked(payload: { accountId: number; parentId: string; path: string }) {
  draft.default_account_id = payload.accountId;
  draft.default_parent_id = payload.parentId;
  draft.default_display_path = payload.path || "/";
  pickerOpen.value = false;
}

const TAB_SWITCH = "switch";
const TAB_SOURCE = "source";
const TAB_MEDIA = "media";

const TABS = [
  { key: TAB_SWITCH, label: "番号开关" },
  { key: TAB_SOURCE, label: "数据源" },
  { key: TAB_MEDIA, label: "媒体服务器" },
];

const tab = ref(TAB_SWITCH);
const visited = ref<Record<string, boolean>>({ [TAB_SWITCH]: true, [TAB_SOURCE]: true, [TAB_MEDIA]: true });

const loading = ref(false);
const saving = ref(false);
const testing = ref(false);
const loggingIn = ref(false);
const loaded = ref(false);
const baseline = ref("");

const config = ref<JavConfig | null>(null);

/**
 * 草稿的本地类型：所有字段必填。
 *
 * 不直接用 JavConfigInput —— 它的字段全是可选的（那是 PATCH 语义：没传就是不改），
 * 直接拿来当表单绑定的类型会让每个 v-model 都变成 `T | undefined`，
 * 而输入框要的是「有值」。
 */
type JavDraft = {
  enabled: boolean;
  username: string;
  password: string;
  token: string;
  api_base: string;
  javbus_base: string;
  use_proxy: boolean;
  min_interval_ms: number;
  request_gap_ms: number;
  timeout_sec: number;
  retry: number;
  sub_check_enabled: boolean;
  sub_daily_times: string[];
  sub_check_interval_min: number;
  sub_sync_enabled: boolean;
  sub_sync_times: string[];
  sub_push_batch: number;
  sub_concurrency: number;
  sub_retry_enabled: boolean;
  sub_interval_min_sec: number;
  sub_interval_max_sec: number;
  sub_timeout_sec: number;
  sidecar_enabled: boolean;
  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_push_provider: string;
  library_sync_enabled: boolean;
  library_cron: string;
};

const draft = reactive<JavDraft>({
  enabled: true,
  username: "",
  password: "",
  token: "",
  api_base: "",
  javbus_base: "",
  use_proxy: true,
  min_interval_ms: 500,
  request_gap_ms: 1000,
  timeout_sec: 20,
  retry: 3,
  sub_check_enabled: false,
  sub_daily_times: [],
  sub_check_interval_min: 120,
  sub_sync_enabled: false,
  sub_sync_times: [],
  sub_push_batch: 5,
  sub_concurrency: 2,
  sub_retry_enabled: true,
  sub_interval_min_sec: 3,
  sub_interval_max_sec: 10,
  sub_timeout_sec: 30,
  sidecar_enabled: true,
  default_account_id: 0,
  default_parent_id: "",
  default_display_path: "",
  default_push_provider: "auto",
  library_sync_enabled: false,
  library_cron: "0 */6 * * *",
});

/** 脏判断只看用户能编辑的字段。密码/token 从不回传，也就不参与比较。 */
function snapshot() {
  return JSON.stringify({
    enabled: draft.enabled,
    username: draft.username,
    api_base: draft.api_base,
    javbus_base: draft.javbus_base,
    use_proxy: draft.use_proxy,
    min_interval_ms: draft.min_interval_ms,
    request_gap_ms: draft.request_gap_ms,
    timeout_sec: draft.timeout_sec,
    retry: draft.retry,
    sub_check_enabled: draft.sub_check_enabled,
    sub_daily_times: draft.sub_daily_times,
    sub_check_interval_min: draft.sub_check_interval_min,
    sub_sync_enabled: draft.sub_sync_enabled,
    sub_sync_times: draft.sub_sync_times,
    sub_push_batch: draft.sub_push_batch,
    sub_concurrency: draft.sub_concurrency,
    sub_retry_enabled: draft.sub_retry_enabled,
    sub_interval_min_sec: draft.sub_interval_min_sec,
    sub_interval_max_sec: draft.sub_interval_max_sec,
    sub_timeout_sec: draft.sub_timeout_sec,
    sidecar_enabled: draft.sidecar_enabled,
    default_account_id: draft.default_account_id,
    default_parent_id: draft.default_parent_id,
    default_display_path: draft.default_display_path,
    default_push_provider: draft.default_push_provider,
    library_sync_enabled: draft.library_sync_enabled,
    library_cron: draft.library_cron,
    // 凭据只在用户**填了**的时候才算改动 —— 空串是「不修改」的意思。
    password: draft.password ? "changed" : "",
    token: draft.token ? "changed" : "",
  });
}

const isDirty = computed(() => loaded.value && snapshot() !== baseline.value);

function applyConfig(cfg: JavConfig) {
  config.value = cfg;
  draft.enabled = cfg.enabled;
  draft.username = cfg.username;
  draft.password = "";
  draft.token = "";
  draft.api_base = cfg.api_base;
  draft.javbus_base = cfg.javbus_base;
  draft.use_proxy = cfg.use_proxy;
  draft.min_interval_ms = cfg.min_interval_ms;
  draft.request_gap_ms = cfg.request_gap_ms;
  draft.timeout_sec = cfg.timeout_sec;
  draft.retry = cfg.retry;
  draft.sub_check_enabled = cfg.sub_check_enabled;
  draft.sub_daily_times = [...cfg.sub_daily_times];
  draft.sub_check_interval_min = cfg.sub_check_interval_min;
  draft.sub_sync_enabled = cfg.sub_sync_enabled;
  draft.sub_sync_times = [...cfg.sub_sync_times];
  draft.sub_push_batch = cfg.sub_push_batch;
  draft.sub_concurrency = cfg.sub_concurrency;
  draft.sub_retry_enabled = cfg.sub_retry_enabled;
  draft.sub_interval_min_sec = cfg.sub_interval_min_sec;
  draft.sub_interval_max_sec = cfg.sub_interval_max_sec;
  draft.sub_timeout_sec = cfg.sub_timeout_sec;
  draft.sidecar_enabled = cfg.sidecar_enabled;
  draft.default_account_id = cfg.default_account_id;
  draft.default_parent_id = cfg.default_parent_id;
  draft.default_display_path = cfg.default_display_path;
  draft.default_push_provider = cfg.default_push_provider;
  draft.library_sync_enabled = cfg.library_sync_enabled;
  draft.library_cron = cfg.library_cron;
  baseline.value = snapshot();
}

async function load() {
  loading.value = true;
  try {
    applyConfig(await fetchJavConfig());
    loaded.value = true;
  } catch (err) {
    toast.error(getApiErrorMessage(err, "番号配置加载失败"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    // 凭据留空 = 不修改：把空串原样发过去会被后端当成「没传」，
    // 但显式不带这两个字段语义更清楚，也免得将来后端改成「空串即清空」时踩坑。
    const { password, token, ...rest } = draft;
    applyConfig(
      await saveJavConfig({
        ...rest,
        ...(password ? { password } : {}),
        ...(token ? { token } : {}),
      }),
    );
    toast.success("番号配置已保存");
    // 定时刷新计划改的就是这次保存的两个键，保存后重取一次让「下次触发」跟上。
    await loadServers();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "保存失败"));
  } finally {
    saving.value = false;
  }
}

function revert() {
  if (config.value) applyConfig(config.value);
}

// —— 登录与探活 ——

async function doLogin() {
  loggingIn.value = true;
  try {
    const res = await loginJav(draft.username, draft.password);
    toast.success(`登录成功（token ${res.masked}）`);
    draft.password = "";
    await load();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "登录失败"));
  } finally {
    loggingIn.value = false;
  }
}

async function doTest() {
  testing.value = true;
  try {
    const res = await testJavConnection();
    const parts: string[] = [];
    parts.push(`JAVDB：${res.javdb.ok ? res.javdb.message : "失败 · " + res.javdb.message}`);
    parts.push(`JAVBUS：${res.javbus.ok ? res.javbus.message : "失败 · " + res.javbus.message}`);
    const ok = res.javdb.ok || res.javbus.ok;
    // 两边分开报：一个通一个不通很常见（比如 JAVDB 节点被墙、JAVBUS 还活着），
    // 合并成一句「连接失败」会让人不知道该修哪一边。
    toast[ok ? "success" : "error"](parts.join(" ｜ "));
    if (res.logged_in) await load();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "测试失败"));
  } finally {
    testing.value = false;
  }
}

async function doTestNodes() {
  testing.value = true;
  try {
    const res = await testJavNodes();
    const parts = res.results.map((r, i) => {
      const node = config.value?.api_nodes[i];
      const name = node?.name ?? `节点 ${i + 1}`;
      return `${name}：${r.ok ? r.latency_ms + "ms" : "失败"}`;
    });
    toast.info(parts.join(" ｜ "));
  } catch (err) {
    toast.error(getApiErrorMessage(err, "节点测速失败"));
  } finally {
    testing.value = false;
  }
}

// —— 时间胶囊 ——

const timeDraft = reactive({ daily: "", sync: "" });

function addTime(kind: "daily" | "sync") {
  const raw = (kind === "daily" ? timeDraft.daily : timeDraft.sync).trim();
  if (!/^\d{1,2}:\d{2}$/.test(raw)) {
    toast.error("时间格式应为 HH:MM");
    return;
  }
  const [h, m] = raw.split(":");
  const value = `${h.padStart(2, "0")}:${m}`;
  const list = kind === "daily" ? draft.sub_daily_times! : draft.sub_sync_times!;
  if (!list.includes(value)) list.push(value);
  list.sort();
  if (kind === "daily") timeDraft.daily = "";
  else timeDraft.sync = "";
}

function removeTime(kind: "daily" | "sync", value: string) {
  const list = kind === "daily" ? draft.sub_daily_times! : draft.sub_sync_times!;
  const idx = list.indexOf(value);
  if (idx >= 0) list.splice(idx, 1);
}

// —— 媒体服务器 ——

const servers = ref<JavServer[]>([]);
const stats = ref<JavLibraryStats | null>(null);
const syncing = ref(false);
/**
 * 定时刷新计划只读的那一半：下次/上次触发由服务端算好。
 *
 * 可写的那一半（开关与 cron）在 `draft.library_*` 里 —— 那里才是抽屉底部
 * 「保存设置」提交的东西。写成两份状态就是之前那个 bug。
 */
const schedule = reactive({ nextRunAt: "", lastRunAt: "" });
const serverForm = reactive({ name: "", url: "", api_key: "", type: "emby" });

const CRON_PRESETS = [
  { label: "每天 00:30", cron: "30 0 * * *" },
  { label: "每天 01:20", cron: "20 1 * * *" },
  { label: "每周日 02:00", cron: "0 2 * * 0" },
  { label: "每月 2 号 23:00", cron: "0 23 2 * *" },
  { label: "每 6 小时", cron: "0 */6 * * *" },
  { label: "每 12 小时", cron: "0 */12 * * *" },
];

async function loadServers() {
  try {
    const [srvRes, statRes, schedRes] = await Promise.all([
      fetchJavServers(),
      fetchJavLibraryStats(),
      fetchJavSyncSchedule(),
    ]);
    servers.value = srvRes.items ?? [];
    stats.value = statRes;
    schedule.nextRunAt = schedRes.next_run_at;
    schedule.lastRunAt = schedRes.last_run_at;
  } catch (err) {
    toast.error(getApiErrorMessage(err, "媒体服务器加载失败"));
  }
}

async function addServer() {
  if (!serverForm.name || !serverForm.url || !serverForm.api_key) {
    toast.error("名称、地址、API Key 都要填");
    return;
  }
  try {
    await addJavServer({ ...serverForm });
    toast.success("已添加服务器");
    serverForm.name = "";
    serverForm.url = "";
    serverForm.api_key = "";
    await loadServers();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "添加失败"));
  }
}

async function removeServer(srv: JavServer) {
  if (!window.confirm(`确认删除服务器「${srv.name}」？`)) return;
  try {
    await deleteJavServer(srv.id);
    await loadServers();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "删除失败"));
  }
}

async function pingServer(srv: JavServer) {
  try {
    const res = await testJavServer(srv.id);
    toast[res.ok ? "success" : "error"](res.ok ? `连通成功${res.detail ? " · " + res.detail : ""}` : res.message);
  } catch (err) {
    toast.error(getApiErrorMessage(err, "测试失败"));
  }
}

async function doSyncAll() {
  syncing.value = true;
  try {
    const res = await syncJavAll();
    toast[res.ok ? "success" : "info"](res.message);
    await loadServers();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "同步失败"));
  } finally {
    syncing.value = false;
  }
}

const providerOptions = [
  { value: "auto", label: "自动" },
  { value: "native", label: "网盘离线" },
  { value: "builtin", label: "内置下载器" },
];

const nodeOptions = computed(() =>
  (config.value?.api_nodes ?? []).map((n) => ({ value: n.base, label: n.name })),
);

/** 下次触发时间的展示值。算不出来时明确说「不会触发」，而不是留空。 */
const nextRunText = computed(() => {
  if (!schedule.nextRunAt) return "该表达式在可预见的时间内不会触发";
  return schedule.nextRunAt.slice(0, 16).replace("T", " ");
});

// 脏状态登记到全局的「未保存改动」集合里，抽屉关闭时统一拦截。
// 子 tab 之间用 v-show 切换，草稿不会丢，所以不需要额外的拦截。
useSettingsPageDirty(isDirty, revert);

defineExpose(
  bindSettingsPanelExpose({
    isDirty,
    saving,
    save,
    reload: load,
    revert,
  }),
);

onMounted(async () => {
  await Promise.all([load(), loadServers(), accountsStore.loadAccounts()]);
});
</script>

<template>
  <div>
    <SectionTabBar :model-value="tab" :tabs="TABS" @update:model-value="(k: string) => ((tab = k), (visited[k] = true))" />

    <div v-if="loading" class="jav-empty">加载中…</div>

    <template v-else>
      <!-- 番号开关 -->
      <div v-show="tab === TAB_SWITCH">
        <SettingsCard title="番号菜单">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="显示番号菜单"
                help-title="显示番号菜单"
                help-text="关掉之后，「番号」这个分组以及它下面的榜单、影库、订阅会一起隐藏。默认开启。"
              />
            </template>
            <template #control>
              <SettingsBoolSegment v-model="draft.enabled" label="显示番号菜单" />
            </template>
          </SettingsRow>
        </SettingsCard>
      </div>

      <!-- 数据源 -->
      <div v-show="tab === TAB_SOURCE">
        <SettingsCard title="JAVDB 账号">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="账号 / 密码"
                help-title="账号 / 密码"
                help-text="Top250 与演员榜需要登录。填好后点「登录」换 token，之后不用再填密码。"
              />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px; flex-wrap: wrap">
              <AppInput v-model="draft.username" placeholder="账号" style="width: 150px" />
              <AppInput v-model="draft.password" type="password" placeholder="密码" style="width: 150px" />
              <AppButton size="sm" :disabled="loggingIn" @click="doLogin">
                {{ loggingIn ? "登录中…" : "登录" }}
              </AppButton>
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="登录状态"
                :help-text="
                  config?.has_token
                    ? `已配置 token，上次登录 ${config?.last_login_at?.slice(0, 16).replace('T', ' ') || '未知'}`
                    : '还没有 token，Top250 与演员榜不可用'
                "
              />
            </template>
            <template #control>
              <span
              class="jav-pill"
              :class="config?.login_status === 'ok' ? 'jav-pill--active' : 'jav-pill--completed'"
            >
              {{ config?.login_status === "ok" ? "正常" : config?.has_token ? "已配置" : "未配置" }}
            </span>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="测试连接" />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px">
              <AppButton size="sm" variant="ghost" :disabled="testing" @click="doTest">
                {{ testing ? "测试中…" : "测试连接" }}
              </AppButton>
            </div>
            </template>
          </SettingsRow>
        </SettingsCard>

        <SettingsCard title="网络">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="API 节点" />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px; align-items: center">
              <AppSelect v-model="draft.api_base" :options="nodeOptions" style="width: 220px" />
              <AppButton size="sm" variant="ghost" :disabled="testing" @click="doTestNodes">测速</AppButton>
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="JAVBUS 域名"
                help-text="磁链的备用来源。JAVDB 的磁链接口已经覆盖有码/无码/欧美/FC2 四档，JAVBUS 是日式有码站的库、只对那一档有补充；两个来源的结果会合并去重。"
              />
            </template>
            <template #control>
              <AppInput v-model="draft.javbus_base" placeholder="https://www.javbus.com" style="width: 260px" />
            </template>
            </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="走全局代理"
                :help-text="
                  draft.use_proxy && !config?.proxy_ready
                    ? '已开启，但「系统设置 → 网络代理」里的代理是关着的，实际不会走代理。'
                    : 'JAVDB 与 JAVBUS 都在境外，建议开启。代理地址在系统设置里配。'
                "
              />
            </template>
            <template #control>
              <div style="display: flex; align-items: center; gap: 10px">
                <SettingsBoolSegment v-model="draft.use_proxy" label="走全局代理" />
                <span style="font-size: 11.5px; color: var(--text-muted)">
                  实际：{{ config?.effective_proxy || "—" }}
                </span>
              </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="抓取间隔（毫秒）" />
            </template>
            <template #control>
              <AppInput v-model.number="draft.min_interval_ms" type="number" style="width: 100px" />
            </template>
            </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="请求间隔（毫秒）" />
            </template>
            <template #control>
              <AppInput v-model.number="draft.request_gap_ms" type="number" style="width: 100px" />
            </template>
            </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="超时 / 重试" />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px; align-items: center">
              <AppInput v-model.number="draft.timeout_sec" type="number" style="width: 80px" />
              <span style="font-size: 12px; color: var(--text-muted)">秒</span>
              <AppInput v-model.number="draft.retry" type="number" style="width: 80px" />
              <span style="font-size: 12px; color: var(--text-muted)">次</span>
            </div>
            </template>
          </SettingsRow>
        </SettingsCard>

        <SettingsCard title="订阅调度">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="订阅配置"
                help-title="订阅配置"
                help-text="到点检查全部订阅，只刷新命中数据、不推送。设了「每日检查时间」后，检查间隔会被忽略。"
              />
            </template>
            <template #control>
              <SettingsBoolSegment v-model="draft.sub_check_enabled" label="订阅配置" />
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="每日检查时间" />
            </template>
            <template #control>
              <div class="jav-time-chips">
              <span v-for="t in draft.sub_daily_times" :key="t" class="jav-time-chip">
                {{ t }}
                <i class="jav-time-chip__x" @click="removeTime('daily', t)">✕</i>
              </span>
              <AppInput v-model="timeDraft.daily" placeholder="输入时间" style="width: 100px" @keyup.enter="addTime('daily')" />
              <AppButton size="sm" variant="ghost" @click="addTime('daily')">＋</AppButton>
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="检查间隔（分钟）" />
            </template>
            <template #control>
              <AppInput v-model.number="draft.sub_check_interval_min" type="number" style="width: 100px" />
            </template>
            </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="自动同步在线订阅"
                help-title="自动同步在线订阅"
                help-text="到点推送（等价于订阅页的「执行订阅」）。与上面的检查分开：检查便宜，推送会真的占网盘配额。"
              />
            </template>
            <template #control>
              <SettingsBoolSegment v-model="draft.sub_sync_enabled" label="自动同步在线订阅" />
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="同步时间表" />
            </template>
            <template #control>
              <div class="jav-time-chips">
              <span v-for="t in draft.sub_sync_times" :key="t" class="jav-time-chip">
                {{ t }}
                <i class="jav-time-chip__x" @click="removeTime('sync', t)">✕</i>
              </span>
              <AppInput v-model="timeDraft.sync" placeholder="输入时间" style="width: 100px" @keyup.enter="addTime('sync')" />
              <AppButton size="sm" variant="ghost" @click="addTime('sync')">＋</AppButton>
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="每轮推送部数"
                help-title="每轮推送部数"
                help-text="每个订阅到点一次推几部。默认 5 —— 一部的话，一个 43 部的演员订阅按一天两次要 21 天才推得完。部与部之间仍按下面的「间隔范围」随机停顿，提交速率还是被摊开的；设成 1 就是原来一轮一部的行为。"
              />
            </template>
            <template #control>
              <AppInput v-model.number="draft.sub_push_batch" type="number" style="width: 80px" />
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="并发数量"
                help-title="并发数量"
                help-text="同时往网盘提交的离线任务数。默认 2 —— 提交太多正是触发风控的典型姿势。"
              />
            </template>
            <template #control>
              <div style="display: flex; gap: 12px; align-items: center">
              <AppInput v-model.number="draft.sub_concurrency" type="number" style="width: 80px" />
              <label style="display: inline-flex; gap: 5px; align-items: center; font-size: 12.5px">
                <input v-model="draft.sub_retry_enabled" type="checkbox" />
                启用重试
              </label>
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="间隔范围（秒）" />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px; align-items: center">
              <AppInput v-model.number="draft.sub_interval_min_sec" type="number" style="width: 80px" />
              <span>—</span>
              <AppInput v-model.number="draft.sub_interval_max_sec" type="number" style="width: 80px" />
            </div>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="请求超时（秒）" />
            </template>
            <template #control>
              <AppInput v-model.number="draft.sub_timeout_sec" type="number" style="width: 100px" />
            </template>
            </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="推送时写入元数据文件"
                help-title="推送时写入元数据文件"
                help-text="下载完成后，在资源所在的那一层目录里写一个 `<番号>.json`，含番号/演员/片商/标签/画质标记，以及封面、剧照、预告片的原始地址和落盘时的真实文件名。以后想建 nfo、改名、给 Emby 补封面图与剧照，读这个文件就有全部起点，不必再去抓一遍 JAVDB。它不额外请求上游，也不改推送行为；写失败只记日志，不会影响「已推送」这个结论。"
              />
            </template>
            <template #control>
              <label style="display: inline-flex; gap: 5px; align-items: center; font-size: 12.5px">
                <input v-model="draft.sidecar_enabled" type="checkbox" />
                启用
              </label>
            </template>
          </SettingsRow>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel
                label="默认推送目标"
                help-title="默认推送目标"
                help-text="订阅自身没指定网盘与目录时推到这里。必须选一个 —— 不选的话推送会直接报「没有可用的推送目标」。推送时会在该目录下按番号自动建子目录。"
              />
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
                label="默认推送通道"
                help-title="默认推送通道"
                help-text="订阅自身没指定时用它。自动 = 网盘支持离线下载就走网盘，否则用内置下载器。"
              />
            </template>
            <template #control>
              <AppSelect v-model="draft.default_push_provider" :options="providerOptions" style="width: 160px" />
            </template>
          </SettingsRow>
        </SettingsCard>
      </div>

      <!-- 媒体服务器 -->
      <div v-show="tab === TAB_MEDIA">
        <SettingsCard title="媒体服务器（Emby / Jellyfin）">
          <template #actions>
            <AppButton size="sm" :disabled="syncing" @click="doSyncAll">
              {{ syncing ? "同步中…" : "立即同步全库" }}
            </AppButton>
          </template>

          <div v-if="servers.length === 0" class="jav-empty">还没有配置媒体服务器。</div>
          <div v-for="srv in servers" :key="srv.id" class="jav-server-card">
            <div style="min-width: 0">
              <div class="jav-server-card__name">
                {{ srv.name }}
                <span class="jav-chip jav-chip--dark">{{ srv.type }}</span>
                <span
                  class="jav-pill"
                  :class="srv.last_status === 'ok' ? 'jav-pill--active' : 'jav-pill--completed'"
                >
                  {{ srv.last_status === "ok" ? "正常" : srv.last_status === "error" ? "异常" : "未同步" }}
                </span>
              </div>
              <div class="jav-server-card__url">{{ srv.url }}</div>
              <div v-if="srv.last_error" class="jav-candidate__reasons">{{ srv.last_error }}</div>
            </div>
            <div class="jav-server-card__actions">
              <AppButton size="sm" variant="ghost" @click="pingServer(srv)">测试</AppButton>
              <AppButton size="sm" variant="ghost" @click="removeServer(srv)">删除</AppButton>
            </div>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1.4fr 1fr 0.8fr auto; gap: 8px; margin-top: 12px">
            <AppInput v-model="serverForm.name" placeholder="名称，如 客厅Emby" />
            <AppInput v-model="serverForm.url" placeholder="http://192.168.1.10:8096" />
            <AppInput v-model="serverForm.api_key" placeholder="API Key" />
            <AppSelect
              v-model="serverForm.type"
              :options="[
                { value: 'emby', label: 'Emby' },
                { value: 'jellyfin', label: 'Jellyfin' },
              ]"
            />
            <AppButton size="sm" @click="addServer">添加</AppButton>
          </div>
          <div class="jav-hint" style="margin-top: 6px">
            API Key 在 Emby/Jellyfin 的「设置 → API 密钥」里生成。
          </div>
        </SettingsCard>

        <SettingsCard title="入库缓存设置">
          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="定时刷新计划" />
            </template>
            <template #control>
              <div style="display: flex; gap: 8px; align-items: center">
              <AppInput v-model="draft.library_cron" placeholder="0 */6 * * *" style="width: 180px" />
              <SettingsBoolSegment v-model="draft.library_sync_enabled" label="定时刷新计划" />
            </div>
            </template>
          </SettingsRow>

          <!-- 这里原先自带一个「保存计划」按钮，写的是 PUT /jav/sync/schedule；
               而抽屉底部的「保存设置」写的是同一组设置键
               （jav_library_sync_enabled / jav_library_cron）。
               两套按钮 = 两份互不相干的状态：改这里的开关，draft 没动，
               底部保存按钮永远不亮（看起来就是「保存不了」）；反过来一旦它亮了，
               又会拿开面板时加载的旧值把这边的改动覆盖回去。
               现在统一走 draft，只留底部那一个保存入口。 -->
          <div class="jav-preset-row">
            <button
              v-for="preset in CRON_PRESETS"
              :key="preset.cron"
              type="button"
              class="jav-sort-chip"
              @click="draft.library_cron = preset.cron"
            >
              {{ preset.label }}
            </button>
          </div>

          <div class="jav-hint">
            下次触发：{{ nextRunText }}
            <span v-if="schedule.lastRunAt"> · 上次：{{ schedule.lastRunAt.slice(0, 16).replace("T", " ") }}</span>
          </div>

          <SettingsRow>
            <template #info>
              <SettingsRowLabel label="手动刷新缓存" />
            </template>
            <template #control>
              <AppButton size="sm" variant="ghost" :disabled="syncing" @click="doSyncAll">
              {{ syncing ? "同步中…" : "立即刷新" }}
            </AppButton>
            </template>
          </SettingsRow>

          <div style="margin-top: 14px">
            <div style="font-size: 12.5px; color: var(--text-regular); margin-bottom: 8px">媒体库影片数量</div>
            <div class="jav-stat-tiles">
              <!-- 第一格是「已入库」：数的是本地影片里番号能命中媒体库的那些，
                   也就是卡片上角标真的会标成「已入库」的部数。 -->
              <div class="jav-stat-tile">
                <div class="jav-stat-tile__num">{{ stats?.in_library_movies ?? 0 }}</div>
                <div class="jav-stat-tile__label">已入库</div>
              </div>
              <div v-for="srv in stats?.servers ?? []" :key="srv.server_id" class="jav-stat-tile">
                <div class="jav-stat-tile__num">{{ srv.item_count }}</div>
                <div class="jav-stat-tile__label">{{ srv.name }}</div>
              </div>
              <div class="jav-stat-tile">
                <div class="jav-stat-tile__num">{{ stats?.total_distinct_codes ?? 0 }}</div>
                <div class="jav-stat-tile__label">去重番号</div>
              </div>
              <div class="jav-stat-tile">
                <div class="jav-stat-tile__num">{{ stats?.total_items ?? 0 }}</div>
                <div class="jav-stat-tile__label">总条目</div>
              </div>
            </div>
            <div v-if="(stats?.movies_without_code ?? 0) > 0" class="jav-hint">
              其中 {{ stats?.movies_without_code }} 条提不出番号 —— 这些文件不会参与「已入库」判定。
            </div>
          </div>
        </SettingsCard>
      </div>
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
