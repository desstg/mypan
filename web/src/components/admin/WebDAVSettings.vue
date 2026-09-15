<script setup lang="ts">
import { computed, onMounted } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchSystemConfig, updateWebDAVConfig } from "@/api/auth";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import InputActionField from "@/components/admin/InputActionField.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import { useSettingsForm } from "@/composables/useSettingsForm";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast, copyTextToClipboard } from "@/composables/useToast";

const props = withDefaults(
  defineProps<{
    accent?: string;
  }>(),
  { accent: "var(--brand)" },
);

const { loading, runLoad } = useSettingsLoad();
useAdminPageLoading("share", loading);

const { settings, isDirty, isFieldChanged, applyBaseline, revert: revertSettings } = useSettingsForm({
  webdav_enabled: false,
});

const webdavServerUrl = computed(() => {
  if (typeof window === "undefined") return "";
  return `${window.location.origin.replace(/\/$/, "")}/dav`;
});

function applySettings(data: { webdav_enabled?: boolean }) {
  applyBaseline({ webdav_enabled: data.webdav_enabled !== false });
}

async function loadSettings() {
  await runLoad(async () => {
    applySettings(await fetchSystemConfig());
  }, "加载 WebDAV 设置失败");
}

async function copyWebdavUrl() {
  await copyTextToClipboard(webdavServerUrl.value, {
    successMessage: "已复制 WebDAV 地址",
    errorMessage: "复制失败，请手动选择地址复制",
  });
}

async function saveSettings(silent = false) {
  if (!isDirty.value) return;
  try {
    await updateWebDAVConfig({
      webdav_enabled: settings.webdav_enabled,
    });
    applyBaseline({ webdav_enabled: settings.webdav_enabled });
    if (!silent) toast.success("WebDAV 设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
    throw e;
  }
}

onMounted(() => {
  loadSettings();
});

defineExpose({
  saveSettings,
  getDirty: () => isDirty.value,
  revert: revertSettings,
});
</script>

<template>
  <div class="webdav-settings">
    <SettingsCard title="服务开关" :accent="props.accent">
      <template v-if="!loading">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('webdav_enabled')">
          <template #info>
            <div class="settings-row__label">
              <span>启用 WebDAV 服务</span>
              <SettingsHelpTooltip title="WebDAV 服务开关说明">
                <p>开启后，外部客户端可以通过 WebDAV 地址访问 MyPan。</p>
                <p>关闭后，WebDAV 入口会直接拒绝访问，下面的传输设置也不会生效。</p>
                <p>访问地址为当前浏览器打开后台所用的「协议 + 主机 + 端口」后接 <code>/dav</code>，见下方「WebDAV 地址」。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.webdav_enabled" label="启用 WebDAV 服务" />
          </template>
        </SettingsRow>
      </template>
    </SettingsCard>

    <SettingsCard title="连接信息" :accent="props.accent">
      <SettingsRow>
        <template #info>
          <div class="settings-row__label">
            <span>WebDAV 地址</span>
            <SettingsHelpTooltip title="WebDAV 地址说明">
              <p>Emby、Infuse 等客户端添加 WebDAV 源时使用此地址。</p>
              <p>用户名和密码与后台登录账号相同。</p>
            </SettingsHelpTooltip>
          </div>
        </template>
        <template #control>
          <InputActionField>
            <AppInput :model-value="webdavServerUrl" readonly />
            <template #action>
              <AppButton type="button" variant="secondary" @click="copyWebdavUrl">复制</AppButton>
            </template>
          </InputActionField>
        </template>
      </SettingsRow>
    </SettingsCard>
  </div>
</template>
