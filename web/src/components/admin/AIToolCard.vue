<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  aiOrganizeApi,
  type AIOrganizeConfig,
  type AIOrganizeInstanceUpdate,
} from "@/api/cloudTools";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import ProxyWorkspace, { type ProxyField, type ProxyWorkspaceItem } from "@/components/admin/ProxyWorkspace.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const aiConfig = ref<AIOrganizeConfig>({ enabled: false, items: [] });
const aiOpen = ref(false);
const aiSaving = ref(false);
const aiTesting = ref(false);
const aiSelectedID = ref("");
const aiDraft = reactive<Record<string, string>>({
  name: "",
  base_url: "",
  model: "",
  api_key: "",
  default: "false",
});

const defaultInstance = computed(() => aiConfig.value.items.find((item) => item.default) || null);
const activeModel = computed(() => defaultInstance.value?.model || "");
const selectedInstance = computed(() =>
  aiConfig.value.items.find((item) => item.id === aiSelectedID.value) || null,
);

const workspaceItems = computed<ProxyWorkspaceItem[]>(() =>
  aiConfig.value.items.map((item) => ({
    id: item.id,
    name: item.name,
    running: item.default,
    subtitle: item.default ? "默认激活" : "备用",
  })),
);

const workspaceFields: ProxyField[] = [
  {
    key: "base_url",
    label: "API 地址",
    placeholder: "https://api.deepseek.com",
    helpTitle: "API 地址说明",
    helpBody: "兼容 OpenAI / Anthropic 格式的 API 地址，例如 <code>https://api.deepseek.com</code>。",
  },
  {
    key: "model",
    label: "模型名称",
    placeholder: "例如 deepseek-chat",
    helpTitle: "模型名称说明",
    helpBody: "使用的模型名称，例如 <code>deepseek-chat</code> 或 <code>gpt-4o</code>。",
  },
  {
    key: "api_key",
    label: "API Key",
    type: "password",
    placeholder: "sk-...",
    helpTitle: "API Key 说明",
    helpBody: "调用模型 API 的密钥。",
  },
  {
    key: "default",
    label: "默认激活",
    type: "switch",
    switchLabel: "默认激活",
    switchHint: "运行时只使用这条配置；切换后保存即生效。",
  },
];

function matches(title: string) {
  const q = props.searchQuery.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

function configComplete(values: Record<string, string>) {
  return Boolean(values.base_url.trim() && values.api_key.trim() && values.model.trim());
}

async function load() {
  try {
    aiConfig.value = await aiOrganizeApi.getConfig();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载 AI 配置失败"));
  }
}

onMounted(load);

function openWorkspace() {
  aiOpen.value = true;
  if (aiConfig.value.items.length) {
    selectInstance(aiConfig.value.items[0].id);
  } else {
    aiSelectedID.value = "";
    Object.assign(aiDraft, {
      name: "DeepSeek",
      base_url: "https://api.deepseek.com",
      model: "deepseek-chat",
      api_key: "",
      default: "true",
    });
  }
}

function selectInstance(id: string) {
  const item = aiConfig.value.items.find((i) => i.id === id);
  if (!item) return;
  aiSelectedID.value = id;
  Object.assign(aiDraft, {
    name: item.name,
    base_url: item.base_url,
    model: item.model,
    api_key: item.api_key,
    default: String(item.default),
  });
}

function addInstance() {
  aiSelectedID.value = "";
  Object.assign(aiDraft, {
    name: aiConfig.value.items.length ? `模型 ${aiConfig.value.items.length + 1}` : "模型 1",
    base_url: "https://api.deepseek.com",
    model: "deepseek-chat",
    api_key: "",
    default: "false",
  });
}

async function persist(items: AIOrganizeInstanceUpdate[], message: string, enabled = aiConfig.value.enabled) {
  aiSaving.value = true;
  try {
    aiConfig.value = await aiOrganizeApi.saveConfig({ enabled, items });
    toast.success(message);
    return true;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存 AI 配置失败"));
    return false;
  } finally {
    aiSaving.value = false;
  }
}

function itemsFromInstances(instances: AIOrganizeConfig["items"]): AIOrganizeInstanceUpdate[] {
  return instances.map((item) => ({
    id: item.id,
    name: item.name,
    base_url: item.base_url,
    api_key: item.api_key,
    model: item.model,
    default: item.default,
  }));
}

async function save() {
  if (!aiDraft.name.trim() || !configComplete(aiDraft)) {
    toast.error("请填写配置名称、API 地址、API Key 和模型名称");
    return;
  }
  const items = itemsFromInstances(aiConfig.value.items);
  const next: AIOrganizeInstanceUpdate = {
    name: aiDraft.name,
    base_url: aiDraft.base_url,
    api_key: aiDraft.api_key,
    model: aiDraft.model,
    default: aiDraft.default === "true",
  };
  const editing = aiConfig.value.items.find((item) => item.id === aiSelectedID.value);
  if (editing) {
    next.id = editing.id;
    const index = items.findIndex((item) => item.id === editing.id);
    items[index] = next;
  } else {
    items.push(next);
  }
  // 默认激活是单选：当前项设为默认时，其余项必须同时清除默认标记，
  // 否则后端会因多条默认标记收敛到错误的一条。
  if (next.default) {
    for (const item of items) {
      if (item.id !== next.id) item.default = false;
    }
  }
  if (await persist(items, editing ? "AI 模型配置已保存" : "AI 模型配置已添加")) {
    if (editing) {
      aiSelectedID.value = editing.id;
    } else {
      const saved = aiConfig.value.items.find((item) => item.name === next.name && item.base_url === next.base_url) || aiConfig.value.items[0];
      aiSelectedID.value = saved?.id || "";
    }
    aiOpen.value = false;
  }
}

async function test() {
  if (!configComplete(aiDraft)) {
    toast.error("请先填完 API 地址、API Key 和模型名称");
    return;
  }
  aiTesting.value = true;
  try {
    await aiOrganizeApi.testConfig({
      id: aiSelectedID.value || undefined,
      name: aiDraft.name,
      base_url: aiDraft.base_url,
      api_key: aiDraft.api_key,
      model: aiDraft.model,
    });
    toast.success("连接成功，模型已正确返回 JSON");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "连接测试失败"));
  } finally {
    aiTesting.value = false;
  }
}

async function removeInstance() {
  const editing = aiConfig.value.items.find((item) => item.id === aiSelectedID.value);
  if (!editing) return;
  const items = itemsFromInstances(aiConfig.value.items.filter((item) => item.id !== editing.id));
  if (await persist(items, "AI 模型配置已删除")) {
    if (aiConfig.value.items.length) {
      aiSelectedID.value = aiConfig.value.items[0].id;
      selectInstance(aiConfig.value.items[0].id);
    } else {
      aiSelectedID.value = "";
    }
  }
}

async function toggleEnabled() {
  const items = itemsFromInstances(aiConfig.value.items);
  if (!aiConfig.value.enabled && (!items.length || !configCompleteFromInstances(items))) {
    toast.info("请先添加并填完 AI 模型配置");
    aiOpen.value = true;
    if (!aiConfig.value.items.length) addInstance();
    return;
  }
  await persist(items, aiConfig.value.enabled ? "AI 辅助增强已停用" : "AI 辅助增强已启用", !aiConfig.value.enabled);
}

function configCompleteFromInstances(items: AIOrganizeInstanceUpdate[]) {
  const def = items.find((item) => item.default) || items[0];
  if (!def) return false;
  return Boolean(def.base_url.trim() && def.api_key.trim() && def.model.trim());
}
</script>

<template>
  <div v-show="matches('AI 辅助识别')">
    <CloudToolCard
      :enabled="aiConfig.enabled"
      name="AI 辅助识别"
      driver="目录整理 · 低置信作品补判"
      logo-src="/logos/AI.png"
      logo-alt="AI"
      :stat-value="activeModel || '待配置'"
      :compact-stat="true"
    >
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: aiConfig.enabled }"
          :aria-label="aiConfig.enabled ? '停用 AI 辅助识别' : '启用 AI 辅助识别'"
          :disabled="aiSaving"
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
      将内置规则无法识别或置信度较低的作品交给 AI 补判，调用失败时自动回退原有识别流程。
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="aiSaving" @click="openWorkspace">
          模型设置
        </AppButton>
      </template>
    </CloudToolCard>

    <ProxyWorkspace
      v-model="aiDraft"
      :open="aiOpen"
      title="AI 辅助识别 · 模型设置"
      caption="AI 模型配置"
      icon="🤖"
      :subtitle="selectedInstance ? (selectedInstance.default ? '默认激活 · 运行时使用' : '备用配置') : ''"
      :items="workspaceItems"
      :selected-id="aiSelectedID"
      :fields="workspaceFields"
      name-placeholder="例如：DeepSeek"
      :show-entry="false"
      :testing="aiTesting"
      :saving="aiSaving"
      save-label="保存"
      :deletable="Boolean(selectedInstance)"
      :addable="true"
      @select="selectInstance"
      @add="addInstance"
      @remove="removeInstance"
      @test="test"
      @save="save"
      @cancel="aiOpen = false"
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
