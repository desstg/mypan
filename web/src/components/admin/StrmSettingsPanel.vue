<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  CONFLICT_POLICIES,
  fetchStrmReplaceBaseURLProgress,
  fetchStrmSettings,
  replaceStrmBaseURL,
  saveStrmSettings,
  type ReplaceStrmBaseURLResult,
  type StrmSettings,
} from "@/api/strm";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import InputActionField from "@/components/admin/InputActionField.vue";
import LocalDirBrowserModal from "@/components/common/LocalDirBrowserModal.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsCheckboxRow from "@/components/admin/SettingsCheckboxRow.vue";
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

// 「番号元数据」六个开关。
//
// 键顺序是**契约**：必须与后端 `settings.JavMetaItems` 的字段顺序逐字一致
// （subtitle → preview → thumb → poster → fanart → nfo）。面板要序列化出一模一样的
// 字符串，后端回读的也是同一份 —— 顺序一变，字符串比较就会把「没改过」判成「改过」，
// 一打开设置页就显示「已修改」。
const JAV_META_KEYS = ["subtitle", "preview", "thumb", "poster", "fanart", "nfo"] as const;
const DEFAULT_JAV_META_ITEMS = '{"subtitle":true,"preview":true,"thumb":true,"poster":true,"fanart":true,"nfo":true}';
const JAV_META_OPTIONS = [
  { key: "subtitle", label: "字幕" },
  { key: "preview", label: "剧照" },
  { key: "thumb", label: "thumb缩略图" },
  { key: "poster", label: "poster海报" },
  { key: "fanart", label: "fanart图" },
  { key: "nfo", label: "NFO文件" },
];

type StrmSettingsForm = Pick<
  StrmSettings,
  | "base_url"
  | "signature_enabled"
  | "default_scan_interval"
  | "default_extensions"
  | "iso_filename_enabled"
  | "min_file_size_mb"
  | "conflict_policy"
  | "episode_upgrade"
  | "task_concurrency"
  | "metadata_extensions"
  | "metadata_max_size_mb"
  | "metadata_parent_enabled"
  | "metadata_sync_mode"
  | "jav_metadata_items"
  | "jav_watermark_enabled"
  | "jav_watermark_dir"
  | "jav_watermark_scale"
  | "jav_watermark_margin"
>;

const { loading, loaded, runLoad } = useSettingsLoad(false);
const saving = ref(false);
const replacingBaseURL = ref(false);

// 水印图标目录的目录选择器（容器内浏览）。
//
// 「容器内」不是「浏览器所在那台机器」：列的是**跑 MyPan 那个进程能看到的路径**。
// 部署在群晖容器里时，那是容器里的挂载点 —— 与海报墙「本地目录」弹窗同一个口径
// （见 StrmWallLocalDirDialog 里那段说明），所以这里也用同一个选择器，不另写一套。
const watermarkDirPickerOpen = ref(false);

function onWatermarkDirPicked(path: string) {
  watermarkDirPickerOpen.value = false;
  settings.jav_watermark_dir = path;
}

const numericSettingKeys = new Set<keyof StrmSettingsForm>([
  "default_scan_interval",
  "min_file_size_mb",
  "task_concurrency",
  "metadata_max_size_mb",
  "jav_watermark_scale",
  "jav_watermark_margin",
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
    episode_upgrade: false,
    task_concurrency: 3,
    metadata_extensions: "",
    metadata_max_size_mb: 10,
    metadata_parent_enabled: true,
    metadata_sync_mode: "local_primary",
    jav_metadata_items: DEFAULT_JAV_META_ITEMS,
    jav_watermark_enabled: false,
    jav_watermark_dir: "",
    // 24 与后端同源（jav/config.go 的 watermarkDefaultScale、strm/service.go 的
    // defaultWatermarkScale、registry.go 的 Default）—— 改一处要四处一起改。
    jav_watermark_scale: 24,
    jav_watermark_margin: 2,
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

/** 解析成「键 → 是否勾选」。空串 / 坏 JSON / 缺项按**开**（与后端同一套兜底）。 */
function parseJavMeta(raw: string): Record<string, boolean> {
  let parsed: Record<string, unknown> = {};
  try {
    parsed = JSON.parse(raw || "{}");
  } catch {
    parsed = {};
  }
  const out: Record<string, boolean> = {};
  for (const key of JAV_META_KEYS) out[key] = parsed[key] !== false;
  return out;
}

/** 序列化成固定顺序的 JSON 串（与后端 Encode 逐字一致）。 */
function encodeJavMeta(items: Record<string, boolean>): string {
  const ordered: Record<string, boolean> = {};
  for (const key of JAV_META_KEYS) ordered[key] = items[key] !== false;
  return JSON.stringify(ordered);
}

const javMetaItems = computed(() => parseJavMeta(settings.jav_metadata_items));

function setJavMetaItems(next: Record<string, boolean>) {
  settings.jav_metadata_items = encodeJavMeta(next);
}

function setNumberSetting(
  key: "min_file_size_mb" | "task_concurrency" | "metadata_max_size_mb" | "jav_watermark_scale" | "jav_watermark_margin",
  raw: string | number,
) {
  settings[key] = parseSettingNumber(raw);
}

function setDefaultScanIntervalHours(raw: string | number) {
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
    episode_upgrade: !!data.episode_upgrade,
    task_concurrency: parseSettingNumber(data.task_concurrency) || 3,
    metadata_extensions: data.metadata_extensions ?? "",
    metadata_max_size_mb: parseSettingNumber(data.metadata_max_size_mb) || 10,
    metadata_parent_enabled: !!data.metadata_parent_enabled,
    metadata_sync_mode: data.metadata_sync_mode || "local_primary",
    jav_metadata_items: data.jav_metadata_items || DEFAULT_JAV_META_ITEMS,
    jav_watermark_enabled: !!data.jav_watermark_enabled,
    jav_watermark_dir: data.jav_watermark_dir ?? "",
    jav_watermark_scale: parseSettingNumber(data.jav_watermark_scale) || 24,
    jav_watermark_margin: parseSettingNumber(data.jav_watermark_margin) || 0,
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
      episode_upgrade: settings.episode_upgrade,
      task_concurrency: settings.task_concurrency,
      metadata_extensions: settings.metadata_extensions,
      metadata_max_size_mb: settings.metadata_max_size_mb,
      metadata_parent_enabled: settings.metadata_parent_enabled,
      metadata_sync_mode: settings.metadata_sync_mode,
      jav_metadata_items: settings.jav_metadata_items,
      jav_watermark_enabled: settings.jav_watermark_enabled,
      jav_watermark_dir: settings.jav_watermark_dir,
      jav_watermark_scale: settings.jav_watermark_scale,
      jav_watermark_margin: settings.jav_watermark_margin,
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
    // 后台任务：起任务就返回，结果从进度轮询读（见 waitReplaceBaseURL）。
    // ⚠️ **不能 await 到底**：要遍历整个 STRM 目录树，大库几分钟起步，
    // 浏览器 90 秒就 abort —— 而前面那些文件已经改完了，报「失败」是假的。
    await replaceStrmBaseURL(baseURL);
    const done = await waitReplaceBaseURL();
    settings.base_url = done.base_url ?? baseURL;
    snapshotBaseline();
    if (done.error) {
      toast.error(done.error);
    } else {
      toast.success(`替换完成：${done.updated}/${done.total}`);
    }
  } catch (e) {
    toast.error(getApiErrorMessage(e, "替换失败"));
  } finally {
    replacingBaseURL.value = false;
  }
}

/**
 * 轮询「一键替换」的进度，返回收尾那一拍。
 *
 * ⚠️ **收尾只看进度里的 `running`**，不看起任务那条请求成没成 ——
 * 请求被浏览器超时 abort 恰恰是「任务还在跑」，按请求成败收尾会把它判死。
 *
 * 超时给得宽（10 分钟）且只作兜底：正常路径是 `running` 变 false。
 */
async function waitReplaceBaseURL(): Promise<ReplaceStrmBaseURLResult> {
  const deadline = Date.now() + 10 * 60_000;
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 1000));
    let progress: ReplaceStrmBaseURLResult;
    try {
      progress = await fetchStrmReplaceBaseURLProgress();
    } catch {
      continue; // 网络抖一下不该判死，下一轮继续问
    }
    if (!progress.running) return progress;
  }
  return { base_url: "", total: 0, updated: 0 };
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
                <p>
                  每一条 <code>.strm</code> 里 URL 的站点部分。留空则用本机监听地址
                  （回环地址，外部播放器取不到）。
                </p>
                <p>
                  <b>取这个地址的是 Emby / Jellyfin 那台服务器，不是你的播放设备</b>
                  —— 所以通常应当填<b>内网地址</b>（如 http://192.168.1.10:5211）。
                </p>
                <p>
                  填成外网域名时，内网的媒体服务器要绕出去再回来（很多路由器不支持回环
                  NAT），典型表现是「海报正常、一播放就提示没有兼容的流」。外网访问媒体
                  服务器是另一码事，交给你的反代即可，与本项无关。
                </p>
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

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('episode_upgrade')">
          <template #info>
            <div class="settings-row__label">
              <span>同集洗版</span>
              <SettingsHelpTooltip title="同集洗版说明">
                <p>网盘上同一集常有几份不同画质的文件（1080p / 2160p / 不同音轨）。
                  开启后每集<b>只生成一个 .strm</b>，保留最清楚的那份。</p>
                <p>关闭时也是每集只留一个，但<b>先到先得</b>：本地已经有的那份不动，
                  只在第一次生成时挑最清楚的 —— 换掉意味着 Emby 里那一集重新识别一遍。</p>
                <p>开启后上游出现更好的画质会替换：旧的 .strm 会在同一次扫描的清理步骤里删掉。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.episode_upgrade" label="同集洗版" />
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
                <p>任务开启「同步元数据」后，会按这里的扩展名同步同目录文件，用英文分号分隔。</p>
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

        <!-- 番号那几项（元数据 / 海报水印）与上面的通用元数据设置是两回事，
             加一条显眼的分隔线断一下。 -->
        <div class="strm-settings-divider" role="separator" aria-label="番号相关设置"></div>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('jav_metadata_items')">
          <template #info>
            <div class="settings-row__label">
              <span>番号元数据</span>
              <SettingsHelpTooltip title="番号元数据说明">
                <p>
                  媒体类型选「番号影片」、且任务开了「刮削元数据」时，会在 <code>.strm</code>
                  同层按这里的勾选生成元数据；侧车 json 会先同步到本地，读的是本地那份。
                </p>
                <p>
                  <b>thumb缩略图</b> 是封面原图（横图）；<b>poster海报</b> 从它按 2:3 裁
                  （有码取右侧，其余按人脸）；<b>fanart图</b> 是它的副本；
                  <b>剧照</b> 放进 <code>extrafanart/</code>。
                </p>
                <p>
                  <b>字幕</b> 会按番号（搜不到就用标题）去迅雷看看的字幕源找一份最优的，
                  写成 <code>&lt;主干&gt;.zh-CN.srt</code> 这样 <b>Emby 认得出</b>的名字
                  （语言码取自字幕正文，简繁分别写 zh-CN / zh-TW）。
                  目录里<b>已经有字幕就不动</b> —— 你手改过的那份不会被覆盖；
                  想换一份，用海报墙上的「重刮」。
                </p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsCheckboxRow
              :model-value="javMetaItems"
              :options="JAV_META_OPTIONS"
              :disabled="saving"
              @update:model-value="setJavMetaItems"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isSettingChanged('jav_watermark_enabled')">
          <template #info>
            <div class="settings-row__label">
              <span>海报水印</span>
              <SettingsHelpTooltip title="海报水印说明">
                <p>
                  开启后，**重刮**与**扫描生成海报**时会按影片属性自动贴水印
                  （4K / 破解 / LTX / 中字），贴在四角。破解与 LTX 贴同一个角
                  （右下），因此**同时命中时只贴 LTX** —— LTX 那张图标本身写的
                  就是「LTX破解」。
                </p>
                <p>
                  默认<b>关</b> —— 关着时只有你在海报墙的编辑页里手动勾选才会贴
                  （那一页的勾选始终有效，与本开关无关）。
                </p>
                <p>
                  图标默认用内置那套；想换成自己的，在「番号相关设置」里填图标目录，
                  文件名要与内置同名（<code>4k</code> / <code>8k</code> /
                  <code>leak</code> / <code>sub</code> / <code>umr</code> /
                  <code>ltx</code>）。
                  大小与边距也在那里调。
                </p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <div class="jav-wm-settings">
              <SettingsBoolSegment v-model="settings.jav_watermark_enabled" label="海报水印" />
              <!-- 开关开着才显示这三项：关着时它们没有任何作用，露出来只会让人
                   以为"调了会生效"。 -->
              <div v-if="settings.jav_watermark_enabled" class="jav-wm-settings__detail">
                <label class="jav-wm-settings__item jav-wm-settings__item--dir">
                  图标目录
                  <span class="jav-wm-settings__dir">
                    <AppInput
                      v-model="settings.jav_watermark_dir"
                      class="jav-wm-settings__dir-input"
                      placeholder="留空 = 用内置那套"
                    />
                    <button
                      type="button"
                      class="jav-wm-settings__browse"
                      title="浏览目录（容器内路径，即跑 MyPan 的进程能看到的目录）"
                      aria-label="浏览图标目录"
                      @click.prevent="watermarkDirPickerOpen = true"
                    >
                      <SvgIcon name="folder-open" :size="16" />
                    </button>
                  </span>
                </label>
                <label class="jav-wm-settings__item">
                  大小 %
                  <AppInput
                    :model-value="String(settings.jav_watermark_scale)"
                    type="number"
                    min="5"
                    max="50"
                    style="width: 70px"
                    @update:model-value="setNumberSetting('jav_watermark_scale', $event)"
                  />
                </label>
                <label class="jav-wm-settings__item">
                  边距 %
                  <AppInput
                    :model-value="String(settings.jav_watermark_margin)"
                    type="number"
                    min="0"
                    max="20"
                    style="width: 70px"
                    @update:model-value="setNumberSetting('jav_watermark_margin', $event)"
                  />
                </label>
              </div>
            </div>
          </template>
        </SettingsRow>

      </SettingsCard>
    </template>
  </div>

  <!-- 图标目录选择器：走「容器内路径」那条既有弹窗（与海报墙「本地目录」同一个），
       不另写一套面包屑/错误态。nested 是因为它开在任务抽屉之上。 -->
  <LocalDirBrowserModal
    :open="watermarkDirPickerOpen"
    :initial-path="settings.jav_watermark_dir"
    title="选择水印图标目录（容器内）"
    confirm-text="使用当前目录"
    @close="watermarkDirPickerOpen = false"
    @select="onWatermarkDirPicked"
  />
</template>

<style scoped>
/* 图标目录那一格：输入框 + 右侧「浏览」图标键。
 *
 * 这一组（`.jav-wm-settings*`）本来没有任何样式 —— 全靠 label 的默认行内排布，
 * 所以这里也只做两件事：竖直居中，撑满同一行高。高度交给 `align-items: stretch`
 * 自动对齐输入框，不写死数字（AppInput 一改 padding 就又对不上了）。 */
.jav-wm-settings__dir {
  display: inline-flex;
  align-items: stretch;
  gap: 6px;
  vertical-align: middle;
}
.jav-wm-settings__dir-input {
  width: 230px;
}
/* 三项（图标目录 / 大小 / 边距）本来靠 label 行内流式排布 —— 桌面上一行放得下，
 * 手机上一行放不下，会从**某一项中间**折行（"大" 与 "小 % 24" 落到两行）。 */
.jav-wm-settings__detail {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 14px;
  margin-top: 8px;
}
.jav-wm-settings__item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.jav-wm-settings__browse {
  flex: 0 0 auto;
  width: 34px;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: var(--transition);
}
.jav-wm-settings__browse:hover {
  border-color: var(--brand);
  color: var(--brand);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}
/* 手机上三项排不下：让「图标目录」独占一行、输入框吃掉剩余宽度。
 * 不这么写就是 230px 定宽把按钮挤到下一行，而右边那两颗数字框又被挤成两段。 */
@media (max-width: 640px) {
  .jav-wm-settings__item--dir {
    width: 100%;
  }
  .jav-wm-settings__dir {
    flex: 1;
    min-width: 0;
  }
  .jav-wm-settings__dir .jav-wm-settings__dir-input {
    width: 100%;
    min-width: 0;
  }
}

/* 番号相关设置与通用元数据之间的分隔线。
 *
 * 用**两像素的品牌色实线**而不是常见的 1px 灰线：这里不是「同组里的分节」，
 * 而是「换了一类设置」（番号那几项只在媒体类型选「番号影片」时才生效）。
 * 1px 灰线在一屏表单里根本看不见，用户会以为番号那几项属于上面那一组。
 * 上下各留 18px，把它从两侧的 SettingsRow 里"顶"出来。 */
.strm-settings-divider {
  height: 2px;
  margin: 18px 0;
  background: var(--brand);
  opacity: 0.55;
  border-radius: 1px;
}

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
.strm-meta-tip--open {  color: var(--brand-strong);
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
