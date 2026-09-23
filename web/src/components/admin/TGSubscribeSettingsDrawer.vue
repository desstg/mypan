<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import TGChannelPanel from "@/components/admin/TGChannelPanel.vue";
import TGQualityRulesPanel from "@/components/admin/TGQualityRulesPanel.vue";
import TGMatchHistoryPanel from "@/components/admin/TGMatchHistoryPanel.vue";
import TGSettingsPanel from "@/components/admin/TGSettingsPanel.vue";
import JavSettingsPanel from "@/components/admin/JavSettingsPanel.vue";
import JavRecordPanel from "@/components/admin/JavRecordPanel.vue";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { readPanelSaving, type SettingsPanelExpose } from "@/composables/useSettingsForm";

// 齿轮打开的配置面板。
//
// 形态与「任务管理 → 目录整理 → 整理设置」一致：右侧滑出的设置抽屉 + 卡片的
// 设置行布局，不是一个居中弹窗。保存/取消在抽屉底部统一处理。

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: []; changed: [] }>();

const TAB_CHANNELS = "channels";
const TAB_QUALITY = "quality";
const TAB_RECORDS = "records";
const TAB_SETTINGS = "settings";
const TAB_JAV = "jav";
const TAB_DOWNLOADS = "downloads";

const TABS = [
  { key: TAB_CHANNELS, label: "TG 频道" },
  { key: TAB_QUALITY, label: "画质规则" },
  { key: TAB_RECORDS, label: "匹配历史" },
  { key: TAB_SETTINGS, label: "推送设置" },
  { key: TAB_JAV, label: "番号相关设置" },
  { key: TAB_DOWNLOADS, label: "下载记录" },
];

/** 需要草稿 + 保存的 tab。频道是即时增删改，匹配历史只读，都不参与保存。 */
// 番号设置也要草稿 + 保存；下载记录是只读表格，不参与。
// 两者都不在这里：它们各自有自己的加载与保存入口（JavSettingsPanel 自己管理草稿）。
const SAVEABLE_TABS = new Set<string>([TAB_QUALITY, TAB_SETTINGS, TAB_JAV]);

const tab = ref(TAB_CHANNELS);
const visited = ref<Record<string, boolean>>({ [TAB_CHANNELS]: true });

const qualityRef = ref<SettingsPanelExpose | null>(null);
const settingsRef = ref<SettingsPanelExpose | null>(null);
const javRef = ref<SettingsPanelExpose | null>(null);

/** 当前 tab 对应的面板实例（只有可保存的 tab 才有）。 */
const activePanel = computed<SettingsPanelExpose | null>(() => {
  if (tab.value === TAB_QUALITY) return qualityRef.value;
  if (tab.value === TAB_SETTINGS) return settingsRef.value;
  if (tab.value === TAB_JAV) return javRef.value;
  return null;
});

const activeDirty = computed(() => activePanel.value?.isDirty?.() ?? false);
const saving = computed(() => readPanelSaving(activePanel.value?.saving));
const canSave = computed(() => SAVEABLE_TABS.has(tab.value) && activeDirty.value);

// 任何 tab 里有未保存改动都要拦住离开，不只当前 tab —— 用户可能在画质规则里改了
// 之后切到匹配历史看热闹，这时关掉面板同样会丢改动。
const anyDirty = computed(
  () =>
    (qualityRef.value?.isDirty?.() ?? false) ||
    (settingsRef.value?.isDirty?.() ?? false) ||
    (javRef.value?.isDirty?.() ?? false),
);

function revertAll() {
  qualityRef.value?.revert?.();
  settingsRef.value?.revert?.();
  javRef.value?.revert?.();
}

const { confirmDiscardChanges } = useSettingsPageDirty(anyDirty, revertAll);

async function close() {
  if (!(await confirmDiscardChanges(() => anyDirty.value))) return;
  emit("close");
}

async function saveActive() {
  await activePanel.value?.save?.();
  emit("changed");
}

function selectTab(next: string) {
  tab.value = next;
  visited.value[next] = true;
}

// 每次打开都回到第一个 tab —— 上次停在「匹配历史」而这次想配 Bot 的话，
// 记住上次的位置反而是干扰。
watch(
  () => props.open,
  (open) => {
    if (open) tab.value = TAB_CHANNELS;
  },
);
</script>

<template>
  <AdminSettingsDrawer
    :open="open"
    title="TG 订阅设置"
    :saving="saving"
    :can-save="canSave"
    :hide-foot="!SAVEABLE_TABS.has(tab)"
    @close="close"
    @cancel="close"
    @save="saveActive"
  >
    <div class="tg-settings">
      <SectionTabBar :model-value="tab" :tabs="TABS" @update:model-value="selectTab" />

      <!-- v-if 负责首次访问时再挂载（省掉没访问过的 tab 的一次请求），挂载后靠
           v-show 切换 —— 否则切走会把未保存的草稿一起带走。 -->
      <div v-if="visited[TAB_CHANNELS]" v-show="tab === TAB_CHANNELS">
        <TGChannelPanel @changed="emit('changed')" />
      </div>
      <div v-if="visited[TAB_QUALITY]" v-show="tab === TAB_QUALITY">
        <TGQualityRulesPanel ref="qualityRef" @changed="emit('changed')" />
      </div>
      <div v-if="visited[TAB_RECORDS]" v-show="tab === TAB_RECORDS">
        <TGMatchHistoryPanel />
      </div>
      <div v-if="visited[TAB_SETTINGS]" v-show="tab === TAB_SETTINGS">
        <TGSettingsPanel ref="settingsRef" @changed="emit('changed')" />
      </div>
      <div v-if="visited[TAB_JAV]" v-show="tab === TAB_JAV">
        <JavSettingsPanel ref="javRef" @changed="emit('changed')" />
      </div>
      <div v-if="visited[TAB_DOWNLOADS]" v-show="tab === TAB_DOWNLOADS">
        <JavRecordPanel />
      </div>
    </div>
  </AdminSettingsDrawer>
</template>

<style scoped>
/* 卡片自带 margin-bottom:16px、TabBar 自带 margin-bottom:18px，容器不再叠加间距。 */
.tg-settings {
  display: flex;
  flex-direction: column;
}
</style>
