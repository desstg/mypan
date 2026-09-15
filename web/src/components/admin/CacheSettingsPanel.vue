<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchSettings, saveSettings, type SettingItem } from "@/api/settings";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsFormRow from "@/components/admin/SettingsFormRow.vue";
import { bindSettingsPanelExpose, useSettingsKVForm } from "@/composables/useSettingsForm";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast } from "@/composables/useToast";
import { CACHE_SETTING_KEYS, WEBDAV_CACHE_SETTING_KEY } from "@/constants/cacheSettings";
import "@/styles/admin-shared.css";

const CACHE_SETTINGS_ACCENT = "#4c74df";

const { loading, loaded, runLoad } = useSettingsLoad(false);
const saving = ref(false);

const items = ref<SettingItem[]>([]);
const { values: form, assignEntry, revertEntries, isEntryChanged } = useSettingsKVForm();

const cacheItems = computed(() => items.value.filter((it) => CACHE_SETTING_KEYS.has(it.key)));

const generalCacheItems = computed(() =>
  cacheItems.value.filter((it) => it.key !== WEBDAV_CACHE_SETTING_KEY),
);

const webdavCacheItems = computed(() =>
  cacheItems.value.filter((it) => it.key === WEBDAV_CACHE_SETTING_KEY),
);

function isChanged(it: SettingItem): boolean {
  return isEntryChanged(it.key, it.type);
}

function isGlobalCacheTTLItem(it: SettingItem): boolean {
  return it.key === "cache_ttl";
}

const settingsChanged = computed(() => cacheItems.value.some((it) => isChanged(it)));

function revertSettings() {
  revertEntries(cacheItems.value.map((it) => it.key));
}

async function loadSettings(options?: { silent?: boolean }) {
  await runLoad(async () => {
    const payload = await fetchSettings();
    items.value = payload.items ?? [];
    for (const it of cacheItems.value) {
      assignEntry(it.key, it.value, it.type);
    }
  }, "加载缓存设置失败", options);
}

async function save() {
  if (!settingsChanged.value) return;
  const changed: Record<string, string> = {};
  for (const it of cacheItems.value) {
    if (isChanged(it)) changed[it.key] = form[it.key];
  }
  saving.value = true;
  try {
    const payload = await saveSettings(changed);
    items.value = payload.items ?? [];
    for (const it of cacheItems.value) {
      assignEntry(it.key, it.value, it.type);
    }
    toast.success("缓存设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    saving.value = false;
  }
}

onMounted(() => {
  void loadSettings({ silent: true });
});

defineExpose(
  bindSettingsPanelExpose({
    isDirty: settingsChanged,
    saving,
    save,
    reload: () => loadSettings({ silent: loaded.value }),
    revert: revertSettings,
  }),
);
</script>

<template>
  <div class="cache-settings">
    <div v-if="loading" class="settings-card__loading">加载中…</div>

    <template v-else>
      <SettingsCard v-if="generalCacheItems.length" title="通用缓存" :accent="CACHE_SETTINGS_ACCENT">
        <SettingsFormRow
          v-for="it in generalCacheItems"
          :key="it.key"
          :item="it"
          :model-value="form[it.key]"
          :changed="isChanged(it)"
          :help-title="isGlobalCacheTTLItem(it) ? '默认缓存时间' : undefined"
          @update:model-value="form[it.key] = $event"
        >
          <template v-if="isGlobalCacheTTLItem(it)" #help>
            <p>当账号未设置缓存时间时，系统将使用此时间作为默认值。每个账号可以在账号管理中单独设置缓存时间。</p>
            <div class="settings-help__section">优先级说明</div>
            <div class="settings-help__item">
              <span class="settings-help__dot settings-help__dot--on" />
              <span>账号设置 &gt; 全局默认值</span>
            </div>
            <div class="settings-help__item">
              <span class="settings-help__dot settings-help__dot--off" />
              <span>账号TTL=0：完全禁用该账号缓存</span>
            </div>
            <div class="settings-help__item">
              <span class="settings-help__dot settings-help__dot--on" />
              <span>账号TTL&gt;0：使用账号设置的时间</span>
            </div>
            <div class="settings-help__item">
              <span class="settings-help__dot settings-help__dot--off" />
              <span>账号未设置：使用此全局默认值</span>
            </div>
          </template>
        </SettingsFormRow>
      </SettingsCard>

      <SettingsCard v-if="webdavCacheItems.length" title="WebDAV 专用缓存" :accent="CACHE_SETTINGS_ACCENT">
        <SettingsFormRow
          v-for="it in webdavCacheItems"
          :key="it.key"
          :item="it"
          :model-value="form[it.key]"
          :changed="isChanged(it)"
          @update:model-value="form[it.key] = $event"
        />
      </SettingsCard>
    </template>
  </div>
</template>
