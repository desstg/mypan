<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchMediaOrganizeSettings,
  saveMediaOrganizeSettings,
  testMediaOrganizeTmdb,
  type MediaOrganizeSettings,
} from "@/api/mediaOrganize";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import TmdbHostsHelpTip from "@/components/admin/TmdbHostsHelpTip.vue";
import MediaOrganizeJavRulesTab from "@/components/admin/MediaOrganizeJavRulesTab.vue";
import { useSettingsForm, bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast } from "@/composables/useToast";
import "@/styles/admin-shared.css";

const ORGANIZE_SETTINGS_ACCENT = "#10b981";

const TMDB_TAB = "tmdb";
const JAV_TAB = "jav";
const THROTTLE_TAB = "throttle";
const RULES_TAB = "rules";
const TAGS_TAB = "tags";
const tabs = [
  { key: TMDB_TAB, label: "TMDB 设置" },
  // 番号跟在 TMDB 后面：两个匹配方案是平级的二选一，放一起才看得出关系
  { key: JAV_TAB, label: "番号匹配规则设置" },
  { key: THROTTLE_TAB, label: "API 请求节流" },
  { key: RULES_TAB, label: "文件识别与整理规则" },
  { key: TAGS_TAB, label: "媒体信息标签排序" },
];

const ALL_TAG_KEYS = ["screen_size", "frame_rate", "video_codec", "audio_codec", "audio_channels"] as const;
const TAG_LABELS: Record<string, string> = {
  screen_size: "分辨率",
  frame_rate: "帧率",
  video_codec: "视频编码",
  audio_codec: "音频编码",
  audio_channels: "声道数",
};

const tmdbLanguageOptions = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁体中文" },
  { value: "en-US", label: "English" },
];

const conflictPolicyOptions = [
  { value: "skip", label: "跳过（推荐）" },
  { value: "overwrite", label: "覆盖" },
];

const { loading, loaded, runLoad } = useSettingsLoad();
const saving = ref(false);
const tmdbTesting = ref(false);
const draggingTagIndex = ref<number | null>(null);
const insertIndex = ref<number | null>(null);
const tagEditorRef = ref<HTMLElement | null>(null);

const tagGhost = reactive({
  visible: false,
  x: 0,
  y: 0,
  width: 0,
  height: 0,
  label: "",
});

let pendingTagDrag: {
  index: number;
  startX: number;
  startY: number;
  offsetX: number;
  offsetY: number;
  width: number;
  height: number;
} | null = null;

const tagGhostStyle = computed(() => ({
  left: `${tagGhost.x}px`,
  top: `${tagGhost.y}px`,
  width: `${tagGhost.width}px`,
  height: `${tagGhost.height}px`,
}));

const {
  settings,
  isDirty: settingsChanged,
  isFieldChanged,
  snapshotBaseline,
  revert: revertToBaseline,
} = useSettingsForm<MediaOrganizeSettings>({
  proxy_enabled: false,
  proxy_url: "",
  proxy_username: "",
  proxy_password: "",
  tmdb_api_key: "",
  tmdb_language: "zh-CN",
  tmdb_api_host: "https://api.themoviedb.org",
  tmdb_image_host: "https://image.tmdb.org",
  api_request_interval_ms: 300,
  tmdb_request_interval_ms: 250,
  file_extensions: "",
  metadata_extensions: "",
  media_tag_order: "",
  align_media_tags: false,
  max_works_per_run: 50,
  overwrite_existing: false,
});
const tagOrder = reactive<string[]>([...ALL_TAG_KEYS]);

/** 每个 Tab 覆盖的设置项，用来判断该 Tab 内是否有未保存改动。 */
const TAB_FIELDS: Record<string, Array<keyof MediaOrganizeSettings>> = {
  [TMDB_TAB]: ["tmdb_api_key", "tmdb_language", "tmdb_api_host", "tmdb_image_host"],
  [THROTTLE_TAB]: ["api_request_interval_ms", "tmdb_request_interval_ms"],
  [RULES_TAB]: ["file_extensions", "metadata_extensions", "max_works_per_run", "overwrite_existing"],
  [TAGS_TAB]: ["align_media_tags", "media_tag_order"],
};

// 标签页状态只存在组件内部：这个面板挂在任务管理抽屉里，写 route.query 会和页面级的 ?tab= 打架。
const activeTab = ref<string>(TMDB_TAB);

// 番号规则是独立端点 + 独立草稿（嵌套对象没法用 useSettingsForm 的 !== 比较跟踪），
// 所以它的脏状态由子组件自己报上来。
const javTabRef = ref<InstanceType<typeof MediaOrganizeJavRulesTab> | null>(null);
const javRulesDirty = ref(false);

// 番号 tab **首次访问后保持挂载**（v-if 只管首次创建，之后用 v-show 切换）。
//
// 之前是纯 v-if，切到别的 tab 就把组件卸载了，后果有两个：
//  1. 里面所有本地状态（规则草稿、试跑输入）直接丢失，切回来是从服务端重新拉的旧值 ——
//     用户改了规则去看一眼别的 tab，改动就没了；
//  2. 脏标记还留在父组件上、保存按钮还亮着，但 ref 已经为 null，
//     此时点保存是**静默不执行**的。
const javTabVisited = ref(false);
watch(activeTab, (tab) => {
  if (tab === JAV_TAB) javTabVisited.value = true;
}, { immediate: true });

const tabsWithState = computed(() =>
  tabs.map((tab) => ({
    ...tab,
    changed:
      (TAB_FIELDS[tab.key] ?? []).some((key) => isFieldChanged(key)) ||
      (tab.key === JAV_TAB && javRulesDirty.value),
  })),
);

const disabledTags = computed(() => ALL_TAG_KEYS.filter((k) => !tagOrder.includes(k)));

const conflictPolicy = computed({
  get: () => (settings.overwrite_existing ? "overwrite" : "skip"),
  set: (val: string) => {
    settings.overwrite_existing = val === "overwrite";
  },
});

function parseMediaTagOrder(raw: MediaOrganizeSettings["media_tag_order"]): string[] | null {
  if (Array.isArray(raw)) return raw.filter((k) => ALL_TAG_KEYS.includes(k as (typeof ALL_TAG_KEYS)[number]));
  if (typeof raw === "string" && raw.trim()) {
    try {
      let parsed: unknown = JSON.parse(raw);
      if (typeof parsed === "string") parsed = JSON.parse(parsed);
      if (Array.isArray(parsed)) {
        return parsed.filter((k) => ALL_TAG_KEYS.includes(k as (typeof ALL_TAG_KEYS)[number]));
      }
    } catch {
      return null;
    }
  }
  return null;
}

function syncTagsFromSettings() {
  const order = parseMediaTagOrder(settings.media_tag_order);
  tagOrder.splice(0, tagOrder.length, ...(order ?? [...ALL_TAG_KEYS]));
}

function flushTagOrderToSettings() {
  settings.media_tag_order = JSON.stringify([...tagOrder]);
}

function removeTag(key: string) {
  const idx = tagOrder.indexOf(key);
  if (idx >= 0) tagOrder.splice(idx, 1);
  flushTagOrderToSettings();
}

function addTag(key: string) {
  if (!tagOrder.includes(key)) tagOrder.push(key);
  flushTagOrderToSettings();
}

function startTagPointerDrag(index: number, e: PointerEvent) {
  if (e.button !== 0 || draggingTagIndex.value !== null) return;
  if ((e.target as HTMLElement | null)?.closest(".tag-chip__remove")) return;
  const chip = e.currentTarget as HTMLElement;
  const rect = chip.getBoundingClientRect();
  pendingTagDrag = {
    index,
    startX: e.clientX,
    startY: e.clientY,
    offsetX: e.clientX - rect.left,
    offsetY: e.clientY - rect.top,
    width: rect.width,
    height: rect.height,
  };
  document.addEventListener("pointermove", handleTagPointerMove);
  document.addEventListener("pointerup", finishTagPointerDrag);
  document.addEventListener("pointercancel", cancelTagPointerDrag);
}

function beginTagDrag(e: PointerEvent) {
  if (!pendingTagDrag) return;
  const index = pendingTagDrag.index;
  draggingTagIndex.value = index;
  insertIndex.value = index;
  tagGhost.visible = true;
  tagGhost.label = TAG_LABELS[tagOrder[index]] ?? tagOrder[index];
  tagGhost.width = pendingTagDrag.width;
  tagGhost.height = pendingTagDrag.height;
  tagGhost.x = e.clientX - pendingTagDrag.offsetX;
  tagGhost.y = e.clientY - pendingTagDrag.offsetY;
  document.body.classList.add("mo-tag-dragging");
}

function handleTagPointerMove(e: PointerEvent) {
  if (!pendingTagDrag) return;
  const dx = Math.abs(e.clientX - pendingTagDrag.startX);
  const dy = Math.abs(e.clientY - pendingTagDrag.startY);
  if (draggingTagIndex.value === null) {
    if (dx < 4 && dy < 4) return;
    beginTagDrag(e);
  }
  e.preventDefault();
  tagGhost.x = e.clientX - pendingTagDrag.offsetX;
  tagGhost.y = e.clientY - pendingTagDrag.offsetY;
  updateInsertIndex(e.clientX, e.clientY);
}

function updateInsertIndex(clientX: number, clientY: number) {
  const root = tagEditorRef.value;
  if (!root || draggingTagIndex.value === null) return;

  const chips = Array.from(root.querySelectorAll<HTMLElement>(".tag-chip[data-tag-index]"));
  if (!chips.length) {
    insertIndex.value = 0;
    return;
  }

  const rowChips = chips.filter((el) => {
    const rect = el.getBoundingClientRect();
    return clientY >= rect.top - 12 && clientY <= rect.bottom + 12;
  });
  const targets = (rowChips.length ? rowChips : chips).sort(
    (a, b) => a.getBoundingClientRect().left - b.getBoundingClientRect().left,
  );

  for (const el of targets) {
    const rect = el.getBoundingClientRect();
    const tagIndex = Number(el.dataset.tagIndex);
    if (Number.isNaN(tagIndex)) continue;
    if (clientX < rect.left + rect.width / 2) {
      insertIndex.value = tagIndex;
      return;
    }
  }

  const lastIndex = Number(targets[targets.length - 1].dataset.tagIndex);
  insertIndex.value = Number.isNaN(lastIndex) ? tagOrder.length : lastIndex + 1;
}

function applyTagMove(from: number, insert: number) {
  if (insert === from || insert === from + 1) return;
  const item = tagOrder.splice(from, 1)[0];
  const target = from < insert ? insert - 1 : insert;
  tagOrder.splice(target, 0, item);
  flushTagOrderToSettings();
}

function finishTagPointerDrag() {
  if (draggingTagIndex.value !== null && insertIndex.value !== null) {
    applyTagMove(draggingTagIndex.value, insertIndex.value);
  }
  endTagDrag();
  cleanupTagPointerListeners();
}

function cancelTagPointerDrag() {
  endTagDrag();
  cleanupTagPointerListeners();
}

function cleanupTagPointerListeners() {
  pendingTagDrag = null;
  document.removeEventListener("pointermove", handleTagPointerMove);
  document.removeEventListener("pointerup", finishTagPointerDrag);
  document.removeEventListener("pointercancel", cancelTagPointerDrag);
}

function endTagDrag() {
  draggingTagIndex.value = null;
  insertIndex.value = null;
  tagGhost.visible = false;
  document.body.classList.remove("mo-tag-dragging");
}

// 拖拽监听挂在 document 上，切走「媒体信息标签排序」标签页时要收尾，避免幽灵标签残留。
watch(activeTab, (tab) => {
  if (tab === TAGS_TAB) return;
  cleanupTagPointerListeners();
  endTagDrag();
});

onBeforeUnmount(() => {
  cleanupTagPointerListeners();
  endTagDrag();
});

async function loadSettings(options?: { silent?: boolean }) {
  await runLoad(async () => {
    const data = await fetchMediaOrganizeSettings();
    Object.assign(settings, data);
    if (settings.max_works_per_run == null) settings.max_works_per_run = 50;
    syncTagsFromSettings();
    snapshotBaseline();
  }, "加载整理设置失败", options);
}

// saving 由 savePanel 统一管理（它要覆盖两个请求），这里不再自己开关。
async function saveSettings() {
  if (!settingsChanged.value) return;
  flushTagOrderToSettings();
  const data = await saveMediaOrganizeSettings({ ...settings });
  Object.assign(settings, data);
  syncTagsFromSettings();
  snapshotBaseline();
  toast.success("整理设置已保存");
}

async function testTmdb() {
  tmdbTesting.value = true;
  try {
    const result = await testMediaOrganizeTmdb({
      tmdb_api_key: settings.tmdb_api_key,
      tmdb_language: settings.tmdb_language,
      tmdb_api_host: settings.tmdb_api_host,
      tmdb_image_host: settings.tmdb_image_host,
      proxy_enabled: settings.proxy_enabled,
      proxy_url: settings.proxy_url,
      proxy_username: settings.proxy_username,
      proxy_password: settings.proxy_password,
    });
    const apiOK = result.api_ok ?? result.ok;
    const imageOK = result.image_ok ?? true;
    if (apiOK && imageOK) {
      toast.success("TMDB 连通正常：API ✓ 图片 ✓");
    } else if (apiOK && !imageOK) {
      toast.error("TMDB 部分异常：API ✓ 图片 ×");
    } else if (!apiOK && imageOK) {
      toast.error("TMDB 部分异常：API × 图片 ✓");
    } else {
      toast.error("TMDB 全部异常：API × 图片 ×");
    }
  } catch (e) {
    toast.error(getApiErrorMessage(e, "TMDB 测试失败"));
  } finally {
    tmdbTesting.value = false;
  }
}

onMounted(() => {
  void loadSettings();
});

function revertPanelSettings() {
  revertToBaseline();
  syncTagsFromSettings();
  javTabRef.value?.revert();
}

// 抽屉底部的「保存设置」一次提交整个面板。番号规则走独立端点，所以这里发两个请求，
// 并且**分别**报告失败 —— 否则用户只会看到「保存失败」，不知道该去改哪儿。
async function savePanel() {
  saving.value = true;
  try {
    if (settingsChanged.value) {
      try {
        await saveSettings();
      } catch (e) {
        toast.error(getApiErrorMessage(e, "保存整理设置失败"));
        return; // 一个失败就停，别让用户以为另一半也存上了
      }
    }
    if (javRulesDirty.value) {
      // 子组件内部已经把失败原因 toast 出来了（含后端的具体校验信息）
      await javTabRef.value?.save();
    }
  } finally {
    saving.value = false;
  }
}

const panelDirty = computed(() => settingsChanged.value || javRulesDirty.value);

defineExpose(
  bindSettingsPanelExpose(
    {
      isDirty: panelDirty,
      saving,
      save: savePanel,
      reload: () => loadSettings({ silent: loaded.value }),
      revert: revertPanelSettings,
    },
  ),
);
</script>

<template>
  <div class="mo-settings">
    <div v-if="loading" class="settings-card__loading">加载中…</div>

    <template v-else>
      <SettingsCard title="代理设置" :accent="ORGANIZE_SETTINGS_ACCENT">
        <template #head-aside>
          <TmdbHostsHelpTip />
        </template>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_enabled')">
          <template #info>
            <div class="settings-row__label"><span>启用代理</span></div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.proxy_enabled" label="启用代理" />
          </template>
        </SettingsRow>

        <template v-if="settings.proxy_enabled">
          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_url')">
            <template #info>
              <div class="settings-row__label"><span>代理地址</span></div>
            </template>
            <template #control>
              <AppInput v-model="settings.proxy_url" placeholder="http://127.0.0.1:1080 或 socks5://127.0.0.1:1080" />
            </template>
          </SettingsRow>

          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_username')">
            <template #info>
              <div class="settings-row__label"><span>代理用户名</span></div>
            </template>
            <template #control>
              <AppInput
                v-model="settings.proxy_username"
                autocomplete="off"
                placeholder="可选"
              />
            </template>
          </SettingsRow>

          <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('proxy_password')">
            <template #info>
              <div class="settings-row__label"><span>代理密码</span></div>
            </template>
            <template #control>
              <AppInput v-model="settings.proxy_password" type="password" autocomplete="new-password" placeholder="可选" />
            </template>
          </SettingsRow>
        </template>
      </SettingsCard>

      <SectionTabBar
        :model-value="activeTab"
        :tabs="tabsWithState"
        @update:model-value="activeTab = $event"
      />

      <!-- 番号规则自带卡片布局与草稿，所以整块交给子组件；其余 tab 仍是设置卡片。
           v-if 只负责首次访问时再挂载（省掉没访问过的用户的一次请求），
           挂载之后就靠 v-show 切换 —— 否则切走会把未保存的草稿一起带走。

           这层 wrapper 是必须的，别为了"少一层 div"去掉：
           v-show 靠给根元素写 display:none 生效，而 MediaOrganizeJavRulesTab 是
           多根节点（loading 的 div + v-else 的 template，加载完共 7 个根），
           Vue 找不到该写在哪，指令会**静默失效**——表现就是切走 tab 后面板不消失，
           后面 tab 的卡片被挤到它下方。wrapper 提供一个真实元素来承接 v-show。 -->
      <div v-if="javTabVisited" v-show="activeTab === JAV_TAB">
        <MediaOrganizeJavRulesTab
          ref="javTabRef"
          @dirty-change="javRulesDirty = $event"
        />
      </div>

      <SettingsCard v-if="activeTab === TMDB_TAB" :accent="ORGANIZE_SETTINGS_ACCENT">
        <template #head-actions>
          <AppButton type="button" variant="secondary" size="sm" :disabled="tmdbTesting" @click="testTmdb">
            {{ tmdbTesting ? "测试中…" : "测试连通性" }}
          </AppButton>
        </template>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_key')">
          <template #info>
            <div class="settings-row__label"><span>TMDB API Key</span></div>
          </template>
          <template #control>
            <AppInput
              v-model="settings.tmdb_api_key"
              type="password"
              placeholder="请填写 TMDB API Key（必填）"
              :ignore-autofill="true"
            />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_language')">
          <template #info>
            <div class="settings-row__label"><span>TMDB 语言（影响搜索和命名）</span></div>
          </template>
          <template #control>
            <AppSelect v-model="settings.tmdb_language" :options="tmdbLanguageOptions" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB API 主域名</span>
              <SettingsHelpTooltip title="TMDB API 主域名说明">
                <p>自建反代时填写主域名，程序自动补 /3；默认使用官方地址。</p>
                <p>国内网络可尝试填写 https://api.tmdb.org（与官方域名解析到不同节点，部分地区可直连，效果因网络环境而异）。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_api_host" placeholder="https://api.themoviedb.org" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_image_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB 图片主域名</span>
              <SettingsHelpTooltip title="TMDB 图片主域名说明">
                <p>自建反代时填写主域名，程序自动补 /t/p；默认使用官方地址。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_image_host" placeholder="https://image.tmdb.org" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard v-else-if="activeTab === THROTTLE_TAB" :accent="ORGANIZE_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('api_request_interval_ms')">
          <template #info>
            <div class="settings-row__label"><span>API 额外补偿间隔（毫秒）</span></div>
          </template>
          <template #control>
            <AppInput v-model="settings.api_request_interval_ms" type="number" min="100" max="10000" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_request_interval_ms')">
          <template #info>
            <div class="settings-row__label"><span>TMDB 请求间隔（毫秒）</span></div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_request_interval_ms" type="number" min="100" max="5000" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard v-else-if="activeTab === RULES_TAB" :accent="ORGANIZE_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('file_extensions')">
          <template #info>
            <div class="settings-row__label"><span>媒体文件后缀（分号分隔）</span></div>
          </template>
          <template #control>
            <AppInput v-model="settings.file_extensions" placeholder="mkv;mp4;avi;ts;mov…" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('metadata_extensions')">
          <template #info>
            <div class="settings-row__label"><span>元数据文件后缀（分号分隔）</span></div>
          </template>
          <template #control>
            <AppInput v-model="settings.metadata_extensions" placeholder="nfo;ass;srt;sub…" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('max_works_per_run')">
          <template #info>
            <div class="settings-row__label">
              <span>每次最多整理作品数</span>
              <SettingsHelpTooltip title="分批整理说明">
                <p>每次生成计划最多包含这么多部作品（一部电影或一部剧集算 1 部），达到上限后停止扫描。</p>
                <p>已整理过的（带 tmdb 标识）不计入此数。</p>
                <p>执行完后再次生成计划即可处理剩余作品。0 表示不限制（不推荐用于大库）。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.max_works_per_run" type="number" min="0" max="10000" placeholder="50" />
          </template>
        </SettingsRow>

        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('overwrite_existing')">
          <template #info>
            <div class="settings-row__label">
              <span>同名冲突处理</span>
              <SettingsHelpTooltip title="同名冲突处理说明">
                <p>执行整理时，若目标目录已存在同名文件：</p>
                <p><b>跳过</b>：保留目标已有文件，跳过该项（推荐，更安全）</p>
                <p><b>覆盖</b>：先删除目标已有同名文件，再写入新文件</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppSelect v-model="conflictPolicy" :options="conflictPolicyOptions" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard v-else-if="activeTab === TAGS_TAB" :accent="ORGANIZE_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('align_media_tags')">
          <template #info>
            <div class="settings-row__label">
              <span>强迫症模式</span>
              <SettingsHelpTooltip title="强迫症模式说明">
                <p>开启后，同一后缀文件将保持媒体信息一致。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.align_media_tags" label="强迫症模式" />
          </template>
        </SettingsRow>

        <div class="mo-tag-row">
          <SettingsRow
            :show-changed-badge="true"
            :changed="isFieldChanged('media_tag_order')"
          >
            <template #info>
              <div class="settings-row__label">
                <span>媒体信息顺序</span>
                <SettingsHelpTooltip title="媒体信息顺序说明">
                  <p>拖拽标签调整顺序，点击 × 移除。文件名按此顺序生成媒体信息。</p>
                </SettingsHelpTooltip>
              </div>
            </template>
            <template #control>
              <div class="mo-tag-editor">
                <div
                  ref="tagEditorRef"
                  class="mo-tag-editor__active"
                  :class="{ 'mo-tag-editor__active--dragging': draggingTagIndex !== null }"
                >
                  <template v-if="draggingTagIndex === null">
                    <span
                      v-for="(key, index) in tagOrder"
                      :key="key"
                      class="tag-chip"
                      :data-tag-index="index"
                      @pointerdown="startTagPointerDrag(index, $event)"
                    >
                      <span class="tag-chip__text">{{ TAG_LABELS[key] }}</span>
                      <span class="tag-chip__remove" @click.stop="removeTag(key)">×</span>
                    </span>
                    <span v-if="tagOrder.length === 0" class="mo-tag-editor__placeholder">点击下方标签添加</span>
                  </template>
                  <template v-else>
                    <template v-for="slot in tagOrder.length + 1" :key="`tag-slot-${slot - 1}`">
                      <span
                        v-if="insertIndex === slot - 1"
                        class="tag-insert-preview"
                        :data-insert-index="slot - 1"
                      />
                      <span
                        v-if="slot - 1 < tagOrder.length && slot - 1 !== draggingTagIndex"
                        :key="tagOrder[slot - 1]"
                        class="tag-chip"
                        :data-tag-index="slot - 1"
                      >
                        <span class="tag-chip__text">{{ TAG_LABELS[tagOrder[slot - 1]] }}</span>
                        <span class="tag-chip__remove" @click.stop="removeTag(tagOrder[slot - 1])">×</span>
                      </span>
                    </template>
                  </template>
                </div>
                <Teleport to="body">
                  <span
                    v-if="tagGhost.visible"
                    class="tag-chip tag-chip--ghost"
                    :style="tagGhostStyle"
                  >
                    <span class="tag-chip__text">{{ tagGhost.label }}</span>
                    <span class="tag-chip__remove tag-chip__remove--ghost" aria-hidden="true">×</span>
                  </span>
                </Teleport>
                <div v-if="disabledTags.length" class="mo-tag-editor__pool">
                  <span
                    v-for="key in disabledTags"
                    :key="key"
                    class="tag-chip tag-chip--add"
                    @click="addTag(key)"
                  >
                    <span class="tag-chip__addon">+</span>
                    <span class="tag-chip__text">{{ TAG_LABELS[key] }}</span>
                  </span>
                </div>
              </div>
            </template>
          </SettingsRow>
        </div>
      </SettingsCard>
    </template>
  </div>
</template>

<style scoped>
/* 卡片自带 margin-bottom:16px、TabBar 自带 margin-bottom:18px，容器不再叠加间距。 */
.mo-settings {
  display: flex;
  flex-direction: column;
}

.mo-tag-row :deep(.settings-row) {
  align-items: flex-start;
}

.mo-tag-row :deep(.settings-row__info) {
  padding-top: 4px;
}

.mo-tag-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.mo-tag-editor__active {
  --tag-chip-width: calc(4em + 40px);
  --tag-chip-height: 32px;
}

.mo-tag-editor__active,
.mo-tag-editor__pool {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.mo-tag-editor__active--dragging {
  flex-wrap: nowrap;
}

:global(body.mo-tag-dragging) {
  cursor: grabbing;
  user-select: none;
}

.mo-tag-editor__placeholder {
  font-size: 13px;
  color: var(--text-muted);
}

.tag-chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  box-sizing: border-box;
  flex: 0 0 var(--tag-chip-width, calc(4em + 40px));
  width: var(--tag-chip-width, calc(4em + 40px));
  min-height: var(--tag-chip-height, 32px);
  padding: 6px 10px;
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  border: 1px solid color-mix(in srgb, var(--brand) 25%, var(--border));
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.2;
  color: var(--brand);
  cursor: grab;
  user-select: none;
  touch-action: none;
}

.tag-chip:active {
  cursor: grabbing;
}

.tag-insert-preview {
  display: inline-flex;
  box-sizing: border-box;
  flex: 0 0 var(--tag-chip-width, calc(4em + 40px));
  width: var(--tag-chip-width, calc(4em + 40px));
  height: var(--tag-chip-height, 32px);
  min-height: var(--tag-chip-height, 32px);
  border: 1px dashed var(--brand);
  border-radius: 6px;
  background: color-mix(in srgb, var(--brand) 6%, transparent);
}

.tag-chip--ghost {
  position: fixed;
  z-index: 10000;
  margin: 0;
  pointer-events: none;
  cursor: grabbing;
  box-sizing: border-box;
  box-shadow: 0 8px 20px color-mix(in srgb, var(--brand) 22%, transparent);
}

.tag-chip__text {
  flex: 0 0 4em;
  width: 4em;
  text-align: center;
  font-weight: 500;
}

.tag-chip__addon {
  flex: 0 0 12px;
  width: 12px;
  text-align: center;
  font-weight: 500;
}

.tag-chip__remove {
  flex: 0 0 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  font-size: 12px;
  cursor: pointer;
}

.tag-chip__remove:hover {
  background: color-mix(in srgb, var(--brand) 20%, transparent);
}

.tag-chip__remove--ghost {
  visibility: hidden;
  pointer-events: none;
}

.tag-chip--add {
  cursor: pointer;
  flex: 0 0 calc(4em + 28px);
  width: calc(4em + 28px);
  min-height: var(--tag-chip-height, 32px);
  background: var(--surface-sunken);
  border: 1px dashed var(--border-soft);
  color: var(--text-muted);
}

.tag-chip--add .tag-chip__text {
  color: var(--text-muted);
}

.tag-chip--add:hover {
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border));
  color: var(--brand);
}
</style>
