<script setup lang="ts">
import { onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  CONFLICT_POLICIES,
  fetchStrmSettings,
  replaceStrmBaseURL,
  saveStrmSettings,
  type StrmSettings,
} from "@/api/strm";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import InputActionField from "@/components/admin/InputActionField.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import { confirm } from "@/composables/useConfirm";
import { bindSettingsPanelExpose, useSettingsForm } from "@/composables/useSettingsForm";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast } from "@/composables/useToast";
import { parseSettingNumber } from "@/utils/settingsDirty";
import "@/styles/admin-shared.css";

const STRM_SETTINGS_ACCENT = "#7c3aed";
const MINUTES_PER_HOUR = 60;
const DEFAULT_SCAN_INTERVAL_MINUTES = 6 * MINUTES_PER_HOUR;
const metaTmdbTipOpen = ref(false);

type StrmSettingsForm = Pick<
  StrmSettings,
  | "base_url"
  | "signature_enabled"
  | "default_scan_interval"
  | "default_extensions"
  | "iso_filename_enabled"
  | "min_file_size_mb"
  | "conflict_policy"
  | "task_concurrency"
  | "metadata_extensions"
  | "metadata_max_size_mb"
  | "metadata_parent_enabled"
  | "metadata_sync_mode"
>;

const { loading, loaded, runLoad } = useSettingsLoad(false);
const saving = ref(false);
const replacingBaseURL = ref(false);

const numericSettingKeys = new Set<keyof StrmSettingsForm>([
  "default_scan_interval",
  "min_file_size_mb",
  "task_concurrency",
  "metadata_max_size_mb",
]);

const {
  settings,
  isDirty: settingsChanged,
  isFieldChanged: isSettingChanged,
  applyBaseline,
  snapshotBaseline,
  revert: revertSettings,
} = useSettingsForm<StrmSettingsForm>(
  {
    base_url: "",
    signature_enabled: false,
    default_scan_interval: DEFAULT_SCAN_INTERVAL_MINUTES,
    default_extensions: "",
    iso_filename_enabled: false,
    min_file_size_mb: 0,
    conflict_policy: "size_desc",
    task_concurrency: 3,
    metadata_extensions: "",
    metadata_max_size_mb: 10,
    metadata_parent_enabled: true,
    metadata_sync_mode: "local_primary",
  },
  {
    compareField: (key, cur, orig) => {
      if (numericSettingKeys.has(key)) {
        return parseSettingNumber(cur) !== parseSettingNumber(orig);
      }
      return cur !== orig;
    },
  },
);

const conflictPolicyOptions = CONFLICT_POLICIES.map((p) => ({ value: p.value, label: p.label }));
const metadataSyncModeOptions = [
  { value: "cloud_primary", label: "网盘元数据为主" },
  { value: "local_primary", label: "本地元数据补缺" },
  { value: "bidirectional", label: "本地与云端互补" },
];

function setNumberSetting(key: "min_file_size_mb" | "task_concurrency" | "metadata_max_size_mb", raw: string) {
  settings[key] = parseSettingNumber(raw);
}

function setDefaultScanIntervalHours(raw: string) {
  settings.default_scan_interval = Math.round(parseSettingNumber(raw) * MINUTES_PER_HOUR);
}

function defaultScanIntervalHours(): number {
  return Math.round((settings.default_scan_interval / MINUTES_PER_HOUR) * 100) / 100;
}

function applySettings(data: Awaited<ReturnType<typeof fetchStrmSettings>>) {
  applyBaseline({
    base_url: data.base_url ?? "",
    signature_enabled: !!data.signature_enabled,
    default_scan_interval: parseSettingNumber(data.default_scan_interval) || DEFAULT_SCAN_INTERVAL_MINUTES,
    default_extensions: data.default_extensions ?? "",
    iso_filename_enabled: !!data.iso_filename_enabled,
    min_file_size_mb: parseSettingNumber(data.min_file_size_mb),
    conflict_policy: data.conflict_policy || "size_desc",
    task_concurrency: parseSettingNumber(data.task_concurrency) || 3,
    metadata_extensions: data.metadata_extensions ?? "",
    metadata_max_size_mb: parseSettingNumber(data.metadata_max_size_mb) || 10,
    metadata_parent_enabled: !!data.metadata_parent_enabled,
    metadata_sync_mode: data.metadata_sync_mode || "local_primary",
  });
}

async function loadSettings(options?: { silent?: boolean }) {
  await runLoad(async () => {
    applySettings(await fetchStrmSettings());
  }, "加载 STRM 设置失败", options);
}

async function saveSettings() {
  if (!settingsChanged.value) return;
  if (isSettingChanged("metadata_sync_mode") && settings.metadata_sync_mode === "cloud_primary") {
    try {
      await confirm({
        title: "启用网盘元数据为主",
        message: "今后运行已开启元数据同步的 STRM 任务时，会删除本地存在但网盘没有的元数据文件。仅处理配置的后缀和完整扫描成功的目录，是否继续？",
        confirmText: "启用",
      });
    } catch {
      return;
    }
  }
  saving.value = true;
  try {
    const data = await saveStrmSettings({
      base_url: settings.base_url,
      signature_enabled: settings.signature_enabled,
      default_scan_interval: settings.default_scan_interval,
      default_extensions: settings.default_extensions,
      iso_filename_enabled: settings.iso_filename_enabled,
      min_file_size_mb: settings.min_file_size_mb,
      conflict_policy: settings.conflict_policy,
      task_concurrency: settings.task_concurrency,
      metadata_extensions: settings.metadata_extensions,
      metadata_max_size_mb: settings.metadata_max_size_mb,
      metadata_parent_enabled: settings.metadata_parent_enabled,
      metadata_sync_mode: settings.metadata_sync_mode,
    });
    applySettings(data);
    toast.success("STRM 设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存设置失败"));
  } finally {
    saving.value = false;
  }
}

async function handleReplaceBaseURL() {
  const baseURL = settings.base_url.trim();
  if (!baseURL) {
    toast.error("请先填写对外基址");
    return;
  }
  try {
    await confirm({
      title: "一键替换播放基址",
      message: "将把本地所有 .strm 文件第一行 URL 的站点部分替换为当前输入框中的对外基址，并同步保存该设置。是否继续？",
      confirmText: "替换",
    });
  } catch {
    return;
  }
  replacingBaseURL.value = true;
  try {
    const data = await replaceStrmBaseURL(baseURL);
    settings.base_url = data.base_url ?? baseURL;
    snapshotBaseline();
    toast.success(`替换完成：${data.updated}/${data.total}`);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "替换失败"));
  } finally {
    replacingBaseURL.value = false;
  }
}

function getDefaultScanInterval(): number {
  return settings.default_scan_interval || DEFAULT_SCAN_INTERVAL_MINUTES;
}

onMounted(() => {
  void loadSettings({ silent: true });
});

defineExpose(
  bindSettingsPanelExpose(
    {
      isDirty: settingsChanged,
      saving,
      save: saveSettings,
      reload: () => loadSettings({ silent: loaded.value }),
      revert: revertSettings,
    },
    { getDefaultScanInterval },
  ),
);
</script>

<template>
  <div class="strm-settings">
    <div v-if="loading" class="settings-card__loading">加载中…</div>

    <template v-else>
      <SettingsCard title="播放与调度" :accent="STRM_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('base_url')">
          <template #info>
            <div class="settings-row__label">
              <span>对外基址</span>
              <SettingsHelpTooltip title="对外基址说明">
                <p>生成 .strm 内完整 URL 时使用，例如 https://pan.example.com。留空则使用当前服务地址。</p>
                <p>右侧「一键替换」会批量改写已有 .strm 文件里的站点部分，并保存此基址。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <InputActionField>
              <AppInput v-model="settings.base_url" placeholder="https://your-host" />
              <template #action>
                <AppButton
                  type="button"
                  variant="secondary"
                  :disabled="replacingBaseURL"
                  @click="handleReplaceBaseURL"
                >
                  {{ replacingBaseURL ? "替换中…" : "一键替换" }}
                </AppButton>
              </template>
            </InputActionField>
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('signature_enabled')">
          <template #info>
            <div class="settings-row__label">
              <span>URL 签名</span>
              <SettingsHelpTooltip title="URL 签名说明">
                <p>开启后播放链接末尾追加签名段，防止 token 泄露后被滥用。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.signature_enabled" label="URL 签名" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('default_scan_interval')">
          <template #info>
            <div class="settings-row__label">
              <span>默认扫描间隔（小时）</span>
              <SettingsHelpTooltip title="默认扫描间隔说明">
                <p>用于新建 STRM 任务，默认 6 小时扫描一次；已有任务的扫描间隔不会改变。</p>
                <p>不建议设置得过短，以免频繁访问网盘触发风控。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput
              :model-value="defaultScanIntervalHours()"
              type="number"
              min="1"
              max="24"
              step="1"
              @update:model-value="setDefaultScanIntervalHours"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('default_extensions')">
          <template #info>
            <div class="settings-row__label">
              <span>默认同步文件类型</span>
              <SettingsHelpTooltip title="默认同步文件类型说明">
                <p>STRM 任务统一按这里的媒体扩展名扫描，用英文分号分隔。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.default_extensions" placeholder="mp4;mkv;avi;…" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('task_concurrency')">
          <template #info>
            <div class="settings-row__label">
              <span>任务并发数</span>
              <SettingsHelpTooltip title="任务并发数说明">
                <p>全局最多同时运行的 STRM 任务数，默认 3 个。</p>
                <p>同一账号始终串行；不同账号可以并发，避免同时扫描过多任务占用服务器资源。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput
              :model-value="settings.task_concurrency"
              type="number"
              min="1"
              max="10"
              @update:model-value="setNumberSetting('task_concurrency', $event)"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('iso_filename_enabled')">
          <template #info>
            <div class="settings-row__label">
              <span>ISO 使用 .iso.strm 文件名</span>
              <SettingsHelpTooltip title="ISO STRM 文件名说明">
                <p>开启后，影片.iso 会生成影片.iso.strm，便于 Infuse 识别 ISO 类型；其他格式命名不变。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.iso_filename_enabled" label="ISO 使用 .iso.strm 文件名" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('min_file_size_mb')">
          <template #info>
            <div class="settings-row__label">
              <span>小文件过滤（MB）</span>
              <SettingsHelpTooltip title="小文件过滤说明">
                <p>生成 .strm 时忽略小于该大小的媒体文件，填 0 表示不过滤。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput
              :model-value="settings.min_file_size_mb"
              type="number"
              min="0"
              @update:model-value="setNumberSetting('min_file_size_mb', $event)"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('conflict_policy')">
          <template #info>
            <div class="settings-row__label">
              <span>同名冲突策略</span>
              <SettingsHelpTooltip title="同名冲突策略说明">
                <p>同目录存在同名不同后缀文件时，只生成一个 .strm，此处决定保留哪一个。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppSelect v-model="settings.conflict_policy" :options="conflictPolicyOptions" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="元数据同步" :accent="STRM_SETTINGS_ACCENT">
        <template #head-aside>
          <AppDropdown v-model:open="metaTmdbTipOpen" trigger="click" align="left" :min-width="360">
            <template #trigger="{ open, toggle }">
              <button
                type="button"
                class="strm-meta-tip"
                :class="{ 'strm-meta-tip--open': open }"
                :aria-expanded="open"
                @click.stop="toggle"
              >
                能访问 TMDB 时？
              </button>
            </template>
            <template #panel>
              <div class="strm-meta-tip-panel">
                <div class="strm-meta-tip-panel__title">尽量少从网盘同步海报 / nfo</div>
                <p class="strm-meta-tip-panel__lead">
                  若本机或容器能访问 TMDB，建议在扩展名里去掉
                  <code>nfo</code> 和图片格式，只保留字幕等小文件；海报与 nfo 交给「STRM 刮削」本地生成。
                </p>
                <p class="strm-meta-tip-panel__foot">
                  这样能少下一批网盘文件，降低因频繁访问 API 触发风控的几率。通不了 TMDB 时，再保留网盘同步海报 / nfo 即可。
                </p>
              </div>
            </template>
          </AppDropdown>
        </template>
        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('metadata_sync_mode')">
          <template #info>
            <div class="settings-row__label">
              <span>元数据同步策略</span>
              <SettingsHelpTooltip title="元数据同步策略说明">
                <p><strong>网盘元数据为主：</strong>下载网盘缺到本地的文件，并清理本地多出的元数据。</p>
                <p><strong>本地元数据补缺：</strong>只从网盘补齐本地缺少的元数据，本地多出的元数据继续保留。</p>
                <p><strong>本地与云端互补：</strong>网盘缺的上传、本地缺的下载，两边同名文件均不覆盖。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppSelect v-model="settings.metadata_sync_mode" :options="metadataSyncModeOptions" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('metadata_extensions')">
          <template #info>
            <div class="settings-row__label">
              <span>元数据扩展名</span>
              <SettingsHelpTooltip title="元数据扩展名说明">
                <p>任务开启同步元数据后，会按这里的扩展名同步同目录文件，用英文分号分隔。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.metadata_extensions" placeholder="srt;ass;ssa;sub;sup;idx;vtt;nfo;jpg;png;bmp;gif" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('metadata_max_size_mb')">
          <template #info>
            <div class="settings-row__label">
              <span>元数据大小上限（MB）</span>
              <SettingsHelpTooltip title="元数据大小上限说明">
                <p>只同步不超过该大小的元数据文件，避免误下载大文件。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput
              :model-value="settings.metadata_max_size_mb"
              type="number"
              min="1"
              @update:model-value="setNumberSetting('metadata_max_size_mb', $event)"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('metadata_parent_enabled')">
          <template #info>
            <div class="settings-row__label">
              <span>父目录元数据同步</span>
              <SettingsHelpTooltip title="父目录元数据同步说明">
                <p>开启后，若子目录中存在影片，也会同步该目录下的海报、nfo 等小文件。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.metadata_parent_enabled" label="父目录元数据同步" />
          </template>
        </SettingsRow>
      </SettingsCard>
    </template>
  </div>
</template>

<style scoped>
.strm-meta-tip {
  border: none;
  padding: 0;
  background: transparent;
  color: var(--brand);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.strm-meta-tip:hover,
.strm-meta-tip--open {
  color: var(--brand-strong);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.strm-meta-tip-panel {
  padding: 12px 14px;
  max-width: 380px;
}
.strm-meta-tip-panel__title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
  margin-bottom: 6px;
}
.strm-meta-tip-panel__lead,
.strm-meta-tip-panel__foot {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-muted);
}
.strm-meta-tip-panel__foot {
  margin-top: 10px;
}
.strm-meta-tip-panel code {
  padding: 1px 4px;
  border-radius: 4px;
  background: var(--border-soft);
  font-size: 11px;
}
</style>
