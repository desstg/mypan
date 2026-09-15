<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { localUploadApi, type LocalUploadMapping } from "@/api/cloudTools";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import ProxyWorkspace, { type ProxyField, type ProxyWorkspaceItem } from "@/components/admin/ProxyWorkspace.vue";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });

const localEnabled = ref(false);
const localMappings = ref<LocalUploadMapping[]>([]);
const localSaving = ref(false);
const mappingOpen = ref(false);
const selectedName = ref("");
const localForm = reactive<Record<string, string>>({
  name: "",
  path: "",
});

const workspaceItems = computed<ProxyWorkspaceItem[]>(() =>
  localMappings.value.map((m) => ({
    id: m.name,
    name: m.name,
    running: false,
    subtitle: m.path,
  })),
);

const workspaceFields: ProxyField[] = [
  {
    key: "path",
    label: "容器内路径",
    placeholder: "/app/data/updatefiles",
    helpTitle: "容器内路径说明",
    helpBody: "docker-compose 中映射到容器的路径（容器内那一侧），例如 <code>/app/data/updatefiles</code>。<br>前台从服务器上传时按左侧标签浏览，不会暴露服务器其他路径。",
  },
];

function matches(title: string) {
  const q = props.searchQuery.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

async function load() {
  try {
    const res = await localUploadApi.getConfig();
    localEnabled.value = res.enabled;
    localMappings.value = res.mappings;
  } catch {
    localEnabled.value = false;
    localMappings.value = [];
  }
}

onMounted(load);

async function toggleEnabled() {
  localSaving.value = true;
  const next = !localEnabled.value;
  try {
    const res = await localUploadApi.saveConfig({ enabled: next, mappings: localMappings.value });
    localEnabled.value = res.enabled;
    toast.success(
      res.enabled
        ? "已启用：前台「新建 → 上传」将提供从服务器上传"
        : "已停用：前台上传恢复原有方式",
    );
  } catch (e) {
    toast.error(getApiErrorMessage(e, "修改开关失败"));
  } finally {
    localSaving.value = false;
  }
}

function openMappings() {
  mappingOpen.value = true;
  if (localMappings.value.length) {
    selectMapping(localMappings.value[0].name);
  } else {
    selectedName.value = "";
    Object.assign(localForm, { name: "", path: "" });
  }
}

function selectMapping(name: string) {
  const mapping = localMappings.value.find((m) => m.name === name);
  if (!mapping) return;
  selectedName.value = name;
  Object.assign(localForm, { name: mapping.name, path: mapping.path });
}

function addMapping() {
  selectedName.value = "";
  Object.assign(localForm, {
    name: localMappings.value.length ? `映射 ${localMappings.value.length + 1}` : "映射 1",
    path: "",
  });
}

async function persist(message: string) {
  localSaving.value = true;
  try {
    const res = await localUploadApi.saveConfig({ enabled: localEnabled.value, mappings: localMappings.value });
    localMappings.value = res.mappings;
    toast.success(message);
    return true;
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存映射目录失败"));
    return false;
  } finally {
    localSaving.value = false;
  }
}

async function saveMappings() {
  const name = localForm.name.trim();
  const path = localForm.path.trim();
  if (!name || !path.startsWith("/")) {
    toast.error("请填写标签名和以 / 开头的容器内路径");
    return;
  }
  const editing = localMappings.value.find((m) => m.name === selectedName.value);
  if (!editing && localMappings.value.some((m) => m.name === name)) {
    toast.error(`标签「${name}」已存在`);
    return;
  }
  const next: LocalUploadMapping = { name, path };
  if (editing) {
    const index = localMappings.value.findIndex((m) => m.name === selectedName.value);
    localMappings.value[index] = next;
  } else {
    localMappings.value.push(next);
  }
  if (await persist(editing ? "映射目录已保存" : "映射目录已添加")) {
    selectedName.value = name;
    mappingOpen.value = false;
  }
}

async function removeMapping() {
  const editing = localMappings.value.find((m) => m.name === selectedName.value);
  if (!editing) return;
  localMappings.value = localMappings.value.filter((m) => m.name !== editing.name);
  if (await persist("映射目录已删除")) {
    if (localMappings.value.length) {
      selectMapping(localMappings.value[0].name);
    } else {
      selectedName.value = "";
    }
  }
}
</script>

<template>
  <div v-show="matches('从服务器上传')">
    <CloudToolCard
      :enabled="localEnabled"
      name="从服务器上传"
      driver="全部网盘 · 服务器目录上传"
      logo-src="/logos/localup.png"
      logo-alt="本机"
      :stat-value="localMappings.length"
      stat-label="映射目录"
    >
      <template #toggle>
        <button
          class="check-toggle"
          type="button"
          :class="{ on: localEnabled }"
          :aria-label="localEnabled ? '停用从服务器上传' : '启用从服务器上传'"
          :disabled="localSaving"
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
      从服务器映射目录选择文件或文件夹上传到网盘，并保留原文件夹结构，不影响从本机上传。
      <template #actions>
        <AppButton size="sm" variant="secondary" :disabled="localSaving" @click="openMappings">
          目录映射
        </AppButton>
      </template>
    </CloudToolCard>

    <ProxyWorkspace
      v-model="localForm"
      :open="mappingOpen"
      title="从服务器上传 · 目录映射设置"
      caption="映射目录"
      icon="📁"
      :subtitle="selectedName ? `容器内路径 · ${localForm.path || '未填写'}` : ''"
      :items="workspaceItems"
      :selected-id="selectedName"
      :fields="workspaceFields"
      name-placeholder="标签名，如：媒体库"
      :show-entry="false"
      :show-test="false"
      :saving="localSaving"
      save-label="保存"
      :deletable="Boolean(selectedName)"
      :addable="true"
      @select="selectMapping"
      @add="addMapping"
      @remove="removeMapping"
      @save="saveMappings"
      @cancel="mappingOpen = false"
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
