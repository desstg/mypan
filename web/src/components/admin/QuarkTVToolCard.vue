<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  quarkTVApi,
  type QuarkTVAccount,
  type QuarkTVBinding,
  type QuarkTVStatus,
} from "@/api/cloudTools";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import ProxyWorkspace, { type ProxyField, type ProxyWorkspaceItem } from "@/components/admin/ProxyWorkspace.vue";
import QuarkTVBindModal from "@/components/admin/QuarkTVBindModal.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const qtvStatus = ref<QuarkTVStatus>({ enabled: false, available: false, play_mode: "adaptive", client_list_mode: "proxy_list", proxy_clients: "vidhub", bindings: [] });
const qtvSaving = ref(false);
const qtvAccounts = ref<QuarkTVAccount[]>([]);
const qtvBindOpen = ref(false);
const qtvWorkspaceOpen = ref(false);
const qtvSavingSettings = ref(false);
const qtvSelectedID = ref("");
const qtvForm = reactive({
  preferred_resolution: "4k",
  allow_dolby: "false",
  play_mode: "adaptive",
  client_list_mode: "proxy_list",
  proxy_clients: "vidhub",
});

const settingsChanged = computed(() => {
  const binding = selectedBinding.value;
  if (!binding) return false;
  return (
    qtvForm.preferred_resolution !== normalizeResolutionForUI(binding.preferred_resolution || "auto") ||
    qtvForm.allow_dolby !== String(!!binding.allow_dolby) ||
    qtvForm.play_mode !== qtvStatus.value.play_mode ||
    qtvForm.client_list_mode !== qtvStatus.value.client_list_mode ||
    normalizeClientKeywords(qtvForm.proxy_clients) !== normalizeClientKeywords(qtvStatus.value.proxy_clients)
  );
});

// 非 SVIP 会员且杜比未开启时，不允许新开启杜比视界（已开启的可继续使用）。
const dolbyControlDisabled = computed(() => {
  const binding = selectedBinding.value;
  if (!binding) return false;
  return !supportsAdvancedQuality(binding.membership) && qtvForm.allow_dolby !== "true";
});

const settingsResolutionOptions = computed(() => {
  const binding = selectedBinding.value;
  const advancedDisabled = binding ? !supportsAdvancedQuality(binding.membership) : false;
  return [
    { value: "4k", label: "4K", disabled: advancedDisabled, tag: "SVIP" },
    { value: "super", label: "超清", disabled: advancedDisabled, tag: "SVIP" },
    { value: "high", label: "高清", disabled: advancedDisabled, tag: "SVIP" },
    { value: "low", label: "流畅" },
  ];
});

const selectedBinding = computed<QuarkTVBinding | null>(
  () => qtvStatus.value.bindings.find((b) => String(b.account_id) === qtvSelectedID.value) || null,
);

const workspaceItems = computed<ProxyWorkspaceItem[]>(() =>
  qtvStatus.value.bindings.map((b) => ({
    id: String(b.account_id),
    name: b.account_name,
    running: true,
    subtitle: `TV · ${b.tv_nickname || "未知"} · ${b.membership?.trim() || "普通"}`,
  })),
);

const workspaceFields = computed<ProxyField[]>(() => {
  const fields: ProxyField[] = [
    {
      key: "preferred_resolution",
      label: "清晰度偏好",
      type: "select",
      options: settingsResolutionOptions.value,
    },
    {
      key: "allow_dolby",
      label: "杜比视界",
      type: "switch",
      switchLabel: "杜比视界",
      switchTag: "SVIP 限额",
      switchHint: "开启后优先尝试杜比视界；不可用时会自动降级到上面的清晰度偏好。",
      disabled: dolbyControlDisabled.value,
    },
  ];
  fields.push({
    key: "play_mode",
    label: "接管模式",
    type: "select",
    helpTitle: "接管模式",
    helpBody: "<p><strong>策略分流：</strong>可选择「直连名单」或「代理名单」，再按播放器名称分别走夸克 TV 或本机代理。</p><p><strong>智能变轨：</strong>普通视频走夸克 TV，遇到 HLS 视频自动改走本机代理。</p><p><strong>强制直连：</strong>所有视频都走夸克 TV，不兼容时可能无法播放。</p>",
    options: [
      { value: "split", label: "策略分流" },
      { value: "adaptive", label: "智能变轨" },
      { value: "direct", label: "强制直连" },
    ],
  });
  fields.push({
    key: "proxy_clients",
    label: "客户端分流",
    type: "segmented-text",
    segmentKey: "client_list_mode",
    options: [
      { value: "direct_list", label: "直连名单" },
      { value: "proxy_list", label: "代理名单" },
    ],
    helpTitle: "客户端分流",
    helpBody: "直连名单：名单里的播放器走夸克 TV，其他走本机代理。<br>代理名单：名单里的播放器走本机代理，其他走夸克 TV。<br>多个名称用分号分隔，不区分大小写。Emby 等由服务端 FFmpeg 拉流时，无法识别原播放器名称。",
    placeholder: "例如：vidhub",
    hidden: qtvForm.play_mode !== "split",
  });
  return fields;
});

function matches(title: string) {
  const q = props.searchQuery.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

async function load() {
  qtvStatus.value = await quarkTVApi.status().catch(() => ({ enabled: false, available: false, play_mode: "adaptive" as const, client_list_mode: "proxy_list" as const, proxy_clients: "vidhub", bindings: [] }));
}

onMounted(() => {
  void load();
});

async function toggleEnabled() {
  if (!qtvStatus.value.enabled && qtvStatus.value.bindings.length === 0) {
    await openBind();
    toast.info("请先选择夸克账号并扫码绑定 TV 账号");
    return;
  }
  qtvSaving.value = true;
  const next = !qtvStatus.value.enabled;
  try {
    await quarkTVApi.setEnabled(next);
    qtvStatus.value.enabled = next;
    toast.success(next ? "已启用：夸克播放请求改走 TV 302 直链" : "已停用：夸克播放恢复夸克驱动本机代理");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "修改开关失败"));
  } finally {
    qtvSaving.value = false;
  }
}

function openWorkspace() {
  qtvWorkspaceOpen.value = true;
  if (qtvStatus.value.bindings.length) {
    selectBinding(String(qtvStatus.value.bindings[0].account_id));
  } else {
    qtvSelectedID.value = "";
  }
}

function selectBinding(id: string) {
  const binding = qtvStatus.value.bindings.find((b) => String(b.account_id) === id);
  if (!binding) return;
  qtvSelectedID.value = id;
  qtvForm.preferred_resolution = normalizeResolutionForUI(binding.preferred_resolution || "auto");
  qtvForm.allow_dolby = String(!!binding.allow_dolby);
  qtvForm.play_mode = qtvStatus.value.play_mode || "adaptive";
  qtvForm.client_list_mode = qtvStatus.value.client_list_mode || "proxy_list";
  qtvForm.proxy_clients = qtvStatus.value.proxy_clients;
}

function displayMembership(binding: QuarkTVBinding | null) {
  if (!binding) return "未知";
  return binding.membership?.trim() || "未知";
}

function supportsAdvancedQuality(membership: string) {
  const value = membership.trim().toUpperCase();
  return value === "SVIP" || value === "SVIP+" || value === "88VIP";
}

function normalizeResolutionForUI(value: string) {
  switch ((value || "").trim().toLowerCase()) {
    case "4k":
    case "auto":
      return "4k";
    case "2k":
    case "super":
      return "super";
    case "high":
      return "high";
    case "normal":
    case "low":
    default:
      return "low";
  }
}

function normalizeClientKeywords(value: string) {
  const seen = new Set<string>();
  return value
    .split(/[;；]/)
    .map((item) => item.trim())
    .filter((item) => {
      const key = item.toLowerCase();
      if (!key || seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .join(";");
}

async function openBind() {
  try {
    const res = await quarkTVApi.accounts();
    const boundIDs = new Set(qtvStatus.value.bindings.map((b) => b.account_id));
    qtvAccounts.value = res.accounts.filter((a) => !boundIDs.has(a.id));
    if (qtvAccounts.value.length === 0) {
      toast.error(res.accounts.length === 0 ? "请先添加并启用夸克网盘账号" : "所有夸克账号均已绑定");
      return;
    }
    qtvBindOpen.value = true;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载夸克账号失败"));
  }
}

function closeBind() {
  qtvBindOpen.value = false;
}

async function onBound() {
  qtvBindOpen.value = false;
  const st = await quarkTVApi.status().catch(() => ({ enabled: false, available: false, play_mode: "adaptive" as const, client_list_mode: "proxy_list" as const, proxy_clients: "vidhub", bindings: [] }));
  qtvStatus.value = st;
  if (st.bindings.length) {
    selectBinding(String(st.bindings[st.bindings.length - 1].account_id));
  }
  if (!st.enabled) {
    qtvSaving.value = true;
    try {
      await quarkTVApi.setEnabled(true);
      qtvStatus.value.enabled = true;
      toast.success("已启用夸克 STRM 接管");
    } catch (e) {
      toast.error(getApiErrorMessage(e, "绑定成功但启用失败，请手动开启"));
    } finally {
      qtvSaving.value = false;
    }
  } else {
    toast.success("绑定成功");
  }
}

async function unbind() {
  const binding = selectedBinding.value;
  if (!binding) return;
  const ok = await confirm({
    title: "解绑夸克 TV？",
    message: `将解绑「${binding.account_name}」的夸克 TV 绑定，该账号播放恢复夸克驱动本机代理。`,
    confirmText: "确认解绑",
    cancelText: "取消",
    danger: true,
  }).catch(() => false);
  if (!ok) return;
  try {
    await quarkTVApi.unbind(binding.account_id);
    await load();
    if (qtvStatus.value.bindings.length) {
      selectBinding(String(qtvStatus.value.bindings[0].account_id));
    } else {
      qtvSelectedID.value = "";
    }
    toast.success("已解绑");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "解绑失败"));
  }
}

async function saveSettings() {
  const binding = selectedBinding.value;
  if (!binding) return;
  qtvSavingSettings.value = true;
  try {
    const result = await quarkTVApi.updateBindingSettings({
      account_id: binding.account_id,
      preferred_resolution: qtvForm.preferred_resolution,
      allow_dolby: qtvForm.allow_dolby === "true",
		play_mode: qtvForm.play_mode as "split" | "adaptive" | "direct",
		client_list_mode: qtvForm.client_list_mode as "direct_list" | "proxy_list",
      proxy_clients: qtvForm.proxy_clients,
    });
    qtvStatus.value.bindings = qtvStatus.value.bindings.map((item) =>
      item.account_id === result.binding.account_id ? result.binding : item,
    );
    qtvStatus.value.proxy_clients = result.proxy_clients;
		qtvStatus.value.play_mode = result.play_mode;
		qtvStatus.value.client_list_mode = result.client_list_mode;
    qtvForm.proxy_clients = result.proxy_clients;
    toast.success("播放设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存播放设置失败"));
  } finally {
    qtvSavingSettings.value = false;
  }
}
</script>

<template>
  <div v-show="matches('夸克 STRM 接管')">
    <CloudToolCard
      :enabled="qtvStatus.enabled"
      name="夸克 STRM 接管"
      driver="夸克网盘 · TV 版 302 直链"
      logo-src="/logos/quark.png"
      logo-alt="夸克"
      :tags="[{ label: '实验性', variant: 'warn' }]"
      :stat-value="qtvStatus.bindings.length"
      stat-label="个绑定账号"
    >
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: qtvStatus.enabled }"
          :aria-label="qtvStatus.enabled ? '停用夸克 STRM 接管' : '启用夸克 STRM 接管'"
          :disabled="qtvSaving || !qtvStatus.available"
          title="启用 / 停用"
          @click="toggleEnabled"
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M3.5 8.5 6.5 11.5 12.5 4.5"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
      </template>
      让夸克 STRM 改走 TV 版 302 直链；转码画质和字幕受影响且部分第三方播放器不兼容。
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="qtvSaving" @click="openWorkspace">
          账号绑定
        </AppButton>
      </template>
    </CloudToolCard>

    <ProxyWorkspace
      v-model="qtvForm"
      :open="qtvWorkspaceOpen"
      title="夸克 STRM 接管 · 账号绑定"
      caption="已绑定账号"
      icon="☁️"
      :subtitle="selectedBinding ? `TV 账号：${selectedBinding.tv_nickname || '未知'} · 会员：${displayMembership(selectedBinding)}` : ''"
      :items="workspaceItems"
      :selected-id="qtvSelectedID"
      :fields="workspaceFields"
      :name-editable="false"
      :show-entry="false"
      :show-test="false"
      :saving="qtvSavingSettings"
      :save-disabled="!settingsChanged"
      save-label="保存设置"
      add-label="＋ 添加绑定"
      remove-label="解绑"
      :deletable="Boolean(selectedBinding)"
      :addable="true"
      @select="selectBinding"
      @add="openBind"
      @remove="unbind"
      @save="saveSettings"
      @cancel="qtvWorkspaceOpen = false"
    />

    <QuarkTVBindModal
      :open="qtvBindOpen"
      :accounts="qtvAccounts"
      @close="closeBind"
      @bound="onBound"
    />
  </div>
</template>

<style scoped>
.check-toggle {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 0;
  padding: 0;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  background: var(--border);
  color: var(--text-muted);
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}

.check-toggle svg {
  width: 14px;
  height: 14px;
}

.check-toggle:hover {
  background: var(--surface-hover);
}

.check-toggle.on {
  background: var(--success);
  color: #fff;
  box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16);
}

.check-toggle.on:hover {
  background: color-mix(in srgb, var(--success) 88%, #000);
}

.check-toggle:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
