<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from "vue";
import { useRoute } from "vue-router";
import { ApiError } from "@/api/client";
import AppButton from "@/components/base/AppButton.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import WebDAVSettings from "@/components/admin/WebDAVSettings.vue";
// 本地挂载面板较大且非默认 tab，按需加载；type-only import 仅用于 ref 类型，不引入代码。
import type FuseManagementComponent from "@/components/admin/FuseManagement.vue";
const FuseManagement = defineAsyncComponent(() => import("@/components/admin/FuseManagement.vue"));
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { toast } from "@/composables/useToast";

const WEBDAV_TAB = "webdav";
const FUSE_TAB = "fuse";
const VALID_TABS = [WEBDAV_TAB, FUSE_TAB] as const;

const tabs = [
  { key: WEBDAV_TAB, label: "WebDAV" },
  { key: FUSE_TAB, label: "本地挂载" },
];

const route = useRoute();
const webdavSettingsRef = ref<InstanceType<typeof WebDAVSettings> | null>(null);
const fuseMgmtRef = ref<InstanceType<typeof FuseManagementComponent> | null>(null);
const savingWebdav = ref(false);

const webdavDirty = computed(() => Boolean(webdavSettingsRef.value?.getDirty?.()));
const fuseDirty = computed(() => Boolean(fuseMgmtRef.value?.getDirty?.()));

function normalizeShareTab(tab: string): string {
  const trimmed = tab.trim();
  if (!trimmed) return WEBDAV_TAB;
  return VALID_TABS.includes(trimmed as (typeof VALID_TABS)[number]) ? trimmed : WEBDAV_TAB;
}

function currentShareTab(): string {
  return normalizeShareTab(String(route.query.tab ?? WEBDAV_TAB));
}

const pageDirty = computed(() => {
  const tab = currentShareTab();
  if (tab === WEBDAV_TAB) return webdavDirty.value;
  if (tab === FUSE_TAB) return fuseDirty.value;
  return false;
});

function revertPageSettings() {
  const tab = currentShareTab();
  if (tab === WEBDAV_TAB) {
    webdavSettingsRef.value?.revert?.();
    return;
  }
  if (tab === FUSE_TAB) {
    fuseMgmtRef.value?.revertDrawer?.();
    fuseMgmtRef.value?.closeSettingsDrawerSilent?.();
  }
}

const { confirmDiscardChanges } = useSettingsPageDirty(pageDirty, revertPageSettings);

const { activeTab, setActiveTab } = useSectionTabRoute(WEBDAV_TAB, VALID_TABS, {
  beforeTabChange: async (from, to) => {
    if (from === to) return true;
    const leavingDirty =
      (from === WEBDAV_TAB && webdavDirty.value) ||
      (from === FUSE_TAB && fuseDirty.value);
    if (!leavingDirty) return true;
    return confirmDiscardChanges(() => leavingDirty);
  },
});

async function saveWebdav() {
  if (!webdavDirty.value || !webdavSettingsRef.value) return;
  savingWebdav.value = true;
  try {
    await webdavSettingsRef.value.saveSettings(false);
  } catch (e) {
    if (!(e instanceof ApiError)) {
      toast.error("保存失败");
    }
  } finally {
    savingWebdav.value = false;
  }
}
</script>

<template>
  <div class="settings file-share-page">
    <SectionTabBar :model-value="activeTab" :tabs="tabs" @update:model-value="setActiveTab">
      <template #actions>
        <AppButton
          v-if="activeTab === WEBDAV_TAB"
          type="button"
          variant="primary"
          :disabled="!webdavDirty || savingWebdav"
          @click="saveWebdav"
        >
          {{ savingWebdav ? "保存中…" : "保存设置" }}
        </AppButton>
        <AppButton v-else-if="activeTab === FUSE_TAB" type="button" variant="primary" @click="fuseMgmtRef?.openCreate()">
          添加挂载点
        </AppButton>
      </template>
    </SectionTabBar>

    <keep-alive>
      <WebDAVSettings v-if="activeTab === WEBDAV_TAB" ref="webdavSettingsRef" accent="#10b981" />

      <FuseManagement v-else-if="activeTab === FUSE_TAB" ref="fuseMgmtRef" />
    </keep-alive>
  </div>
</template>

<style scoped>
.file-share-page {
  display: flex;
  flex-direction: column;
}
</style>
