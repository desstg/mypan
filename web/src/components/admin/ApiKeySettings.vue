<script setup lang="ts">
import { computed, onMounted, reactive, ref, watchEffect } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  createApiKey,
  deleteApiKey,
  fetchApiKeys,
  rotateStrmKey,
  toggleApiKey,
  updateApiKey,
  type ApiKeyRecord,
  type StrmKeyInfo,
} from "@/api/apiKeys";
import AppButton from "@/components/base/AppButton.vue";
import AppBadge from "@/components/base/AppBadge.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import FormField from "@/components/base/FormField.vue";
import AppInput from "@/components/base/AppInput.vue";
import AdminEnableToggle from "@/components/admin/AdminEnableToggle.vue";
import AdminRowActions from "@/components/admin/AdminRowActions.vue";
import AdminTableActionBtn from "@/components/admin/AdminTableActionBtn.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import "@/styles/admin-shared.css"; /* 使用 modal-form / modal-form__footer 布局，需显式引入共享样式 */
import { useConfirm } from "@/composables/useConfirm";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import { toast, copyTextToClipboard } from "@/composables/useToast";
import "@/styles/admin-table.css";

const props = withDefaults(
  defineProps<{
    accent?: string;
  }>(),
  { accent: "var(--brand)" },
);

const emit = defineEmits<{
  "toolbar-state": [state: { loading: boolean; keyCount: number; maxKeys: number }];
}>();

const { showConfirm } = useConfirm();

const loading = ref(false);
const saving = ref(false);
const dialogOpen = ref(false);
const createdDialogOpen = ref(false);
const createdKeyValue = ref("");
const createdKeyRecord = ref<ApiKeyRecord | null>(null);
const editingKey = ref<ApiKeyRecord | null>(null);
const keys = ref<ApiKeyRecord[]>([]);
const apiKeyList = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();
const maxKeys = ref(10);
const strmKey = reactive<StrmKeyInfo>({
  name: "STRM 媒体访问",
  key: "",
  key_preview: "",
  format: "new",
  status: "active",
  system: true,
  deletable: false,
  disableable: false,
});

const form = reactive({
  name: "",
  key_type: "task",
  expires_days: "90",
  status: "active",
});

const keyTypeOptions = [
  { value: "task", label: "任务执行" },
  { value: "readonly", label: "只读查询" },
];

const statusOptions = [
  { value: "active", label: "启用" },
  { value: "disabled", label: "禁用" },
];

const expiryOptions = computed(() => {
  const base = [
    { value: "30", label: "30 天" },
    { value: "90", label: "90 天" },
    { value: "365", label: "365 天" },
    { value: "0", label: "永不过期" },
  ];
  return editingKey.value ? [{ value: "-1", label: "保持不变" }, ...base] : base;
});

const keyCount = computed(() => keys.value.length);

watchEffect(() => {
  emit("toolbar-state", {
    loading: loading.value,
    keyCount: keys.value.length,
    maxKeys: maxKeys.value,
  });
});

function keyTypeLabel(type: string): string {
  return type === "readonly" ? "只读查询" : "任务执行";
}

function csvEscape(value: unknown): string {
  const text = String(value ?? "");
  return `"${text.replace(/"/g, '""')}"`;
}

function downloadCreatedApiKeyCsv() {
  const record: Partial<ApiKeyRecord> = createdKeyRecord.value ?? {};
  const rows = [
    ["名称", "秘钥", "类型", "状态", "有效期", "创建时间"],
    [
      record.name || "",
      createdKeyValue.value || "",
      keyTypeLabel(record.key_type || "task"),
      record.status === "disabled" ? "已禁用" : "启用",
      formatExpiry(record.expires_at),
      formatExpiry(record.created_at),
    ],
  ];
  const csv = rows.map((row) => row.map(csvEscape).join(",")).join("\n");
  const blob = new Blob([`\uFEFF${csv}`], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  const safeName = String(record.name || "api-key").replace(/[\\/:*?"<>|]/g, "_");
  link.href = url;
  link.download = `${safeName}.csv`;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}

function formatExpiry(value?: string): string {
  if (!value) return "永不过期";
  return value.replace("T", " ").replace("Z", "").slice(0, 16);
}

async function load() {
  loading.value = true;
  try {
    const data = await fetchApiKeys();
    keys.value = data.keys ?? [];
    maxKeys.value = data.max_keys ?? 10;
    Object.assign(strmKey, data.strm_key ?? {});
  } catch (e) {
    toast.error(getApiErrorMessage(e, "加载 API 秘钥失败"));
  } finally {
    loading.value = false;
  }
}

function resetForm() {
  form.name = "";
  form.key_type = "task";
  form.expires_days = "90";
  form.status = "active";
}

function openCreate() {
  editingKey.value = null;
  resetForm();
  dialogOpen.value = true;
}

function openEdit(key: ApiKeyRecord) {
  editingKey.value = key;
  form.name = key.name;
  form.key_type = key.key_type || "task";
  form.status = key.status || "active";
  form.expires_days = "-1";
  dialogOpen.value = true;
}

async function saveKey() {
  const name = form.name.trim();
  if (!name) {
    toast.error("请填写名称");
    return;
  }
  saving.value = true;
  try {
    const expiresDays = Number(form.expires_days);
    const payload = {
      name,
      key_type: form.key_type,
      status: form.status,
      expires_days: Number.isFinite(expiresDays) && expiresDays >= 0 ? expiresDays : undefined,
    };
    if (editingKey.value?.id) {
      await updateApiKey(editingKey.value.id, {
        ...payload,
        expires_days: expiresDays < 0 ? -1 : payload.expires_days,
      });
      toast.success("秘钥已更新");
      dialogOpen.value = false;
      await load();
    } else {
      const created = await createApiKey(payload);
      dialogOpen.value = false;
      createdKeyValue.value = created.key || "";
      createdKeyRecord.value = created;
      createdDialogOpen.value = true;
      await load();
    }
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存秘钥失败"));
  } finally {
    saving.value = false;
  }
}

async function handleToggle(key: ApiKeyRecord) {
  if (!key.id) return;
  try {
    await toggleApiKey(key.id);
    await load();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "切换状态失败"));
  }
}

async function handleDelete(key: ApiKeyRecord) {
  if (!key.id) return;
  const keyID = key.id;
  try {
    await showConfirm({
      title: "删除 API 秘钥",
      message: `确定删除「${key.name}」吗？删除后无法恢复。`,
      icon: "trash",
      confirmText: "删除",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await removeWithDust({
      target: findDustTarget(apiKeyList.value, `api-key-${keyID}`),
      container: apiKeyList.value,
      remove: async () => {
        await deleteApiKey(keyID);
        keys.value = keys.value.filter((item) => item.id !== keyID);
      },
    });
    toast.success("秘钥已删除");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "删除失败"));
  }
}

async function handleRotateStrm() {
  try {
    const result = await showConfirm({
      title: "更换 STRM 媒体访问 Key",
      message: "更换后旧 Key 将无法继续用于播放鉴权。是否同时将新 Key 应用到已有 .strm 文件？",
      icon: "question",
      confirmText: "更换",
      danger: false,
      checkboxLabel: "将新 Key 应用到已有 strm 中",
      checkboxDefault: true,
    });
    const apply = result.checked !== false;
    const data = await rotateStrmKey(apply);
    Object.assign(strmKey, data.strm_key);
    const rep = data.replace_result;
    if (rep) {
      toast.success(`STRM Key 已更换，已更新 ${rep.updated}/${rep.matched} 个匹配文件`);
    } else {
      toast.success("STRM Key 已更换");
    }
  } catch (e) {
    if (e instanceof Error && e.message === "Modal closed") return;
    const msg = getApiErrorMessage(e, "");
    if (msg) toast.error(msg);
  }
}

async function copyText(text: string) {
  await copyTextToClipboard(text);
}

onMounted(load);

defineExpose({ openCreate });
</script>

<template>
  <SettingsCard title="秘钥列表" :accent="props.accent">
    <p class="api-keys__meta">
      STRM 媒体访问 Key 为系统内置播放鉴权令牌；普通秘钥供后续自动联动 Webhook 等外部调用。
      <span class="api-keys__meta-count">普通秘钥 {{ keyCount }}/{{ maxKeys }}</span>
    </p>

    <AppStateBlock v-if="loading" message="加载秘钥中…" loading min-height="180px" />

    <div v-else class="api-keys__table-scroll">
      <table class="api-keys__table">
        <thead>
          <tr>
            <th>名称</th>
            <th>Key</th>
            <th>类型</th>
            <th>状态</th>
            <th>有效期</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody ref="apiKeyList">
          <tr class="api-keys__row">
            <td>
              <div class="api-keys__name">
                <span class="api-keys__name-text">STRM 媒体访问</span>
                <AppBadge tone="info">系统内置</AppBadge>
              </div>
            </td>
            <td><code class="api-keys__token">{{ strmKey.key_preview || "—" }}</code></td>
            <td><AppBadge tone="info">播放鉴权</AppBadge></td>
            <td><AppBadge tone="success">启用</AppBadge></td>
            <td class="api-keys__muted">永不过期</td>
            <td>
              <AdminRowActions>
                <div class="api-keys__actions">
                  <AdminTableActionBtn icon="copy" title="复制" @click="copyText(strmKey.key)" />
                  <AdminTableActionBtn
                    icon="rotate"
                    title="更换 Key"
                    :disabled="loading"
                    @click="handleRotateStrm"
                  />
                </div>
                <template #menu>
                  <button type="button" class="admin-row-actions__item" @click="copyText(strmKey.key)">复制</button>
                  <button type="button" class="admin-row-actions__item" :disabled="loading" @click="handleRotateStrm">更换 Key</button>
                </template>
              </AdminRowActions>
            </td>
          </tr>
          <tr v-for="key in keys" :key="key.id" class="api-keys__row" :data-dust-key="`api-key-${key.id}`">
            <td>
              <span class="api-keys__name-text">{{ key.name }}</span>
            </td>
            <td><code class="api-keys__token">{{ key.key_preview }}</code></td>
            <td><AppBadge tone="warning">{{ keyTypeLabel(key.key_type) }}</AppBadge></td>
            <td>
              <AppBadge :tone="key.status === 'active' ? 'success' : 'neutral'">
                {{ key.status === "active" ? "启用" : "已禁用" }}
              </AppBadge>
            </td>
            <td class="api-keys__muted">{{ formatExpiry(key.expires_at) }}</td>
            <td>
              <AdminRowActions>
                <div class="api-keys__actions">
                  <AdminEnableToggle
                    :enabled="key.status === 'active'"
                    aria-label="API 秘钥启用切换"
                    @enable="handleToggle(key)"
                  />
                  <AdminTableActionBtn icon="edit" title="编辑" @click="openEdit(key)" />
                  <AdminTableActionBtn icon="delete" title="删除" danger @click="handleDelete(key)" />
                </div>
                <template #menu>
                  <button type="button" class="admin-row-actions__item" @click="handleToggle(key)">
                    {{ key.status === "active" ? "禁用秘钥" : "启用秘钥" }}
                  </button>
                  <button type="button" class="admin-row-actions__item" @click="openEdit(key)">编辑</button>
                  <button type="button" class="admin-row-actions__item admin-row-actions__item--danger" @click="handleDelete(key)">
                    删除
                  </button>
                </template>
              </AdminRowActions>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal :open="dialogOpen" size="account" :title="editingKey ? '修改 API 秘钥' : '新增 API 秘钥'" @close="dialogOpen = false">
      <div class="api-keys__form">
        <div class="api-keys__form-row">
          <FormField label="名称 / 用途">
            <AppInput v-model="form.name" placeholder="例如：自动联动 Webhook" />
          </FormField>
          <FormField label="类型">
            <AppSelect v-model="form.key_type" :options="keyTypeOptions" />
          </FormField>
        </div>
        <div class="api-keys__form-row">
          <FormField label="有效期">
            <AppSelect v-model="form.expires_days" :options="expiryOptions" />
          </FormField>
          <FormField label="状态">
            <AppSelect v-model="form.status" :options="statusOptions" />
          </FormField>
        </div>
      <div class="modal-form__footer">
          <AppButton type="button" variant="primary" :disabled="saving" @click="saveKey">
            {{ saving ? "保存中…" : editingKey ? "保存秘钥" : "创建秘钥" }}
          </AppButton>
        </div>
      </div>
    </AppModal>

    <AppModal :open="createdDialogOpen" size="account" title="秘钥已创建" @close="createdDialogOpen = false">
      <div class="api-keys__created">
        <p>请立即复制保存，关闭后将无法再次查看完整 Key。</p>
        <code class="api-keys__created-key">{{ createdKeyValue }}</code>
      <div class="modal-form__footer">
          <AppButton type="button" variant="primary" @click="copyText(createdKeyValue)">复制 Key</AppButton>
          <AppButton type="button" variant="secondary" @click="downloadCreatedApiKeyCsv">下载 CSV</AppButton>
        </div>
      </div>
    </AppModal>
  </SettingsCard>
</template>

<style scoped>
.api-keys__meta {
  margin: 0 0 4px;
  padding-bottom: 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border-soft);
}

.api-keys__meta-count {
  display: inline-block;
  margin-left: 8px;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  font-size: 12px;
  font-weight: 600;
  color: var(--text);
}

.api-keys__table-scroll {
  overflow-x: auto;
  margin: 0 -22px;
  padding-bottom: 16px;
}

.api-keys__table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 13px;
}

.api-keys__table th:nth-child(1),
.api-keys__table td:nth-child(1) {
  width: 18%;
}

.api-keys__table th:nth-child(2),
.api-keys__table td:nth-child(2) {
  width: 22%;
}

.api-keys__table th:nth-child(3),
.api-keys__table td:nth-child(3) {
  width: 12%;
}

.api-keys__table th:nth-child(4),
.api-keys__table td:nth-child(4) {
  width: 10%;
}

.api-keys__table th:nth-child(5),
.api-keys__table td:nth-child(5) {
  width: 18%;
}

.api-keys__table th:last-child,
.api-keys__table td:last-child {
  width: 20%;
  text-align: center;
}

.api-keys__table th,
.api-keys__table td {
  padding: 12px 22px;
  text-align: left;
  vertical-align: middle;
  border-bottom: 1px solid var(--border-soft);
}

.api-keys__table th {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  background: transparent;
}

.api-keys__table tbody tr:last-child td {
  border-bottom: none;
}

.api-keys__row {
  transition: background-color 0.18s ease;
}

.api-keys__row:hover {
  background: color-mix(in srgb, var(--brand) 4%, transparent);
}

.api-keys__name {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
}

.api-keys__name-text {
  font-weight: 700;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.api-keys__token {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--text-muted);
  word-break: break-all;
}

.api-keys__muted {
  color: var(--text-muted);
}

.api-keys__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
  gap: 8px;
}

.api-keys__form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.api-keys__form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.api-keys__created {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.api-keys__created p {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}

.api-keys__created-key {
  display: block;
  padding: 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  font-size: 12px;
  word-break: break-all;
}

@media (max-width: 720px) {
  .api-keys__form-row {
    grid-template-columns: 1fr;
  }
}
</style>
