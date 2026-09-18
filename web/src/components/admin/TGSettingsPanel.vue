<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
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
  testTGConnection,
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
  auto_push: false,
  default_account_id: 0,
  default_parent_id: "",
  default_display_path: "",
  default_quality_profile_id: 0,
  collect_window_min: 5,
  max_push_per_hour: 20,
  poll_interval_sec: 600,
  backfill_pages: 1,
});

const pickerOpen = ref(false);

/** 脏判断只看用户能编辑、且会回传的字段。 */
function snapshot() {
  return JSON.stringify({
    enabled: draft.enabled,
    auto_push: draft.auto_push,
    default_account_id: draft.default_account_id,
    default_parent_id: draft.default_parent_id,
    default_display_path: draft.default_display_path,
    default_quality_profile_id: draft.default_quality_profile_id,
    collect_window_min: draft.collect_window_min,
    max_push_per_hour: draft.max_push_per_hour,
    poll_interval_sec: draft.poll_interval_sec,
    backfill_pages: draft.backfill_pages,
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
  draft.auto_push = cfg.auto_push;
  draft.default_account_id = cfg.default_account_id;
  draft.default_parent_id = cfg.default_parent_id;
  draft.default_display_path = cfg.default_display_path;
  draft.default_quality_profile_id = cfg.default_quality_profile_id;
  draft.collect_window_min = cfg.collect_window_min;
  draft.max_push_per_hour = cfg.max_push_per_hour;
  draft.poll_interval_sec = cfg.poll_interval_sec || 600;
  draft.backfill_pages = cfg.backfill_pages;
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
    const { detail } = await testTGConnection();
    toast.success(`连接正常：${detail}`);
    status.value = await fetchTGConfig();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "连接 t.me 失败"));
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

const fetchStatusText = computed(() => {
  const s = status.value?.status;
  if (s === "ok") {
    const at = status.value?.last_poll_at;
    return at ? `正常（上次抓取 ${timeAgo(at)}）` : "正常";
  }
  if (s === "error") return "抓取异常";
  return "未检测";
});

/** 把 RFC3339 时间戳说成「几分钟前」。 */
function timeAgo(iso: string): string {
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return "刚刚";
  const mins = Math.floor((Date.now() - ts) / 60000);
  if (mins < 1) return "刚刚";
  if (mins < 60) return `${mins} 分钟前`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

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
      <SettingsCard title="抓取设置" :accent="ACCENT">
        <template #head-aside>
          <span>{{ fetchStatusText }}</span>
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
              label="抓取间隔（秒）"
              help-title="抓取间隔"
              help-text="每个频道多久抓一次 Telegram 网页预览。t.me 不是给程序用的接口，填太小有被限流的风险，建议 300–900。频道多的时候会自动放大实际间隔。"
            />
          </template>
          <template #control>
            <AppInput v-model="draft.poll_interval_sec" type="number" placeholder="600" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="首次订阅回填页数" help-title="回填页数">
              <p>新加频道时往回翻多少页历史，每页 20 条。</p>
              <p>填 1 表示回填最近 20 条；填 0 表示只从最新一条开始追新。</p>
              <p>
                回填出来的历史帖同样会参与匹配。若已打开「自动推送」，它们也会被推送 ——
                受每小时推送上限约束，建议先看过匹配历史再开自动推送。
              </p>
              <p v-if="status?.effective_interval_sec">
                当前共 {{ status.channel_count }} 个频道，实际每
                {{ Math.round(status.effective_interval_sec / 60) }} 分钟抓完一轮。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model="draft.backfill_pages" type="number" placeholder="1" />
          </template>
        </SettingsRow>

        <p class="tg-form__hint">
          抓取走「系统设置 → 其他设置 → 网络代理」里的全局代理；国内直连 t.me 通常不通，建议先去那里配好。
        </p>
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
