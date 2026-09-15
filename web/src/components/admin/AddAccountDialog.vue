<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { storeToRefs } from "pinia";
import { useAccountsStore } from "@/stores/accounts";
import { getApiErrorMessage } from "@/api/client";
import type { Account, FieldSchema } from "@/api/types";
import { toast } from "@/composables/useToast";
import { useOAuthAuth } from "@/composables/useOAuthAuth";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import FormField from "@/components/base/FormField.vue";
import DriverPickerStep from "./DriverPickerStep.vue";
import QrLoginModal from "./QrLoginModal.vue";
import DynamicForm from "@/components/form/DynamicForm.vue";

const props = defineProps<{ open: boolean; editing: Account | null }>();
const emit = defineEmits<{ close: []; saved: [] }>();

const store = useAccountsStore();
const { visibleDrivers, accounts } = storeToRefs(store);
const { loading: oauthLoading, run: runOAuth, cancel: cancelOAuth } = useOAuthAuth();

const step = ref<1 | 2>(1);
const driverType = ref("");
const name = ref("");
const formValues = ref<Record<string, unknown>>({});
const submitting = ref(false);

const selectedDriver = computed(() => store.driverOf(driverType.value));
const fields = computed<FieldSchema[]>(() => selectedDriver.value?.fields ?? []);
const isEdit = computed(() => props.editing !== null);
const stepTitle = computed(() =>
  isEdit.value ? "编辑账号" : step.value === 1 ? "选择网盘驱动" : "配置账号信息",
);
const supportsOAuth = computed(() => Boolean(selectedDriver.value?.supports_oauth));
const supportsQRLogin = computed(() => Boolean(selectedDriver.value?.supports_qr_login));
const qrOpen = ref(false);

function parseBooleanSelectValue(f: FieldSchema, raw: unknown) {
  if (f.type !== "select" || !(f.options ?? []).some((o) => o.value === "true" || o.value === "false")) {
    return raw;
  }
  if (typeof raw === "boolean") return raw;
  const normalized = String(raw ?? "").trim().toLowerCase();
  if (normalized === "true") return true;
  if (normalized === "false") return false;
  return raw;
}

function fieldInitialValue(f: FieldSchema, preset: Record<string, unknown>) {
  const raw = preset[f.name] ?? (f.type === "bool" ? false : (f.default ?? ""));
  return parseBooleanSelectValue(f, raw);
}

function initForm(fs: FieldSchema[], preset: Record<string, unknown> = {}) {
  const v: Record<string, unknown> = { ...preset };
  for (const f of fs) {
    v[f.name] = fieldInitialValue(f, preset);
  }
  formValues.value = v;
}

function resetDialog() {
  cancelOAuth();
  step.value = 1;
  driverType.value = "";
  name.value = "";
  formValues.value = {};
}

watch(
  () => props.open,
  (open) => {
    if (!open) {
      cancelOAuth();
      return;
    }
    store.loadDrivers();
    store.loadAccounts();
    if (props.editing) {
      driverType.value = props.editing.driver_type;
      name.value = props.editing.name;
      let preset: Record<string, unknown> = {};
      try {
        preset = JSON.parse(props.editing.config || "{}");
      } catch {
        preset = {};
      }
      initForm(store.driverOf(props.editing.driver_type)?.fields ?? [], preset);
      step.value = 2;
    } else {
      resetDialog();
    }
  },
);

function goPrevStep() {
  cancelOAuth();
  formValues.value = {};
  step.value = 1;
}

function goStep2() {
  if (!driverType.value) {
    toast.warning("请选择驱动类型");
    return;
  }
  initForm(fields.value);
  step.value = 2;
}

function buildConfig(): string {
  const cfg: Record<string, unknown> = {};
  const schemaNames = new Set(fields.value.map((f) => f.name));
  for (const f of fields.value) {
    const raw = formValues.value[f.name];
    if (f.type === "bool") {
      cfg[f.name] = Boolean(raw);
    } else if (f.type === "number") {
      const s = String(raw ?? "").trim();
      if (s) cfg[f.name] = s;
    } else if (f.type === "select") {
      const normalized = parseBooleanSelectValue(f, raw);
      if (typeof normalized === "boolean") {
        cfg[f.name] = normalized;
        continue;
      }
      const s = String(normalized ?? "").trim();
      if (s) cfg[f.name] = s;
      else if (f.default) cfg[f.name] = parseBooleanSelectValue(f, f.default);
    } else {
      const s = String(raw ?? "").trim();
      if (s) cfg[f.name] = s;
      else if (f.default) cfg[f.name] = f.default;
    }
  }
  for (const [key, raw] of Object.entries(formValues.value)) {
    if (schemaNames.has(key)) continue;
    const s = String(raw ?? "").trim();
    if (s) cfg[key] = s;
  }
  return JSON.stringify(cfg);
}

function validate(): string | null {
  const trimmed = name.value.trim();
  if (!trimmed) return "请填写账号名称";
  const dup = accounts.value.find(
    (a) =>
      a.name.toLowerCase() === trimmed.toLowerCase() &&
      (!props.editing || a.id !== props.editing.id),
  );
  if (dup) return "账号名称已存在，请使用其他名称";
  for (const f of fields.value) {
    if (!f.required) continue;
    const v = formValues.value[f.name];
    if (f.type === "bool") continue;
    if (!String(v ?? "").trim() && !f.default) return `请填写「${f.label}」`;
  }
  return null;
}

async function submit() {
  const err = validate();
  if (err) {
    toast.warning(err);
    return;
  }
  submitting.value = true;
  try {
    const payload = {
      name: name.value.trim(),
      driver_type: driverType.value,
      config: buildConfig(),
      is_active: props.editing?.is_active ?? true,
      is_default: props.editing?.is_default ?? false,
      sort_order: props.editing?.sort_order ?? 0,
    };
    if (props.editing) {
      await store.update(props.editing.id, payload);
      toast.success("账号已更新");
    } else {
      await store.create(payload);
      toast.success("账号已添加");
    }
    emit("saved");
    emit("close");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    submitting.value = false;
  }
}

async function handleOAuth() {
  if (!driverType.value) return;
  const fieldNames = fields.value.map((f) => f.name);
  try {
    const filled = await runOAuth(driverType.value, fieldNames);
    formValues.value = { ...formValues.value, ...filled };
  } catch {
    /* toast 已在 composable 内处理 */
  }
}

function openQRLogin() {
  if (!driverType.value) return;
  qrOpen.value = true;
}

function onQRSuccess(credentials: Record<string, string>) {
  formValues.value = { ...formValues.value, ...credentials };
  qrOpen.value = false;
}

function handleClose() {
  cancelOAuth();
  qrOpen.value = false;
  emit("close");
}
</script>

<template>
  <AppModal :open="open" size="account" @close="handleClose">
    <template #header>
      <div class="dialog-head">
        <span v-if="!isEdit" class="step-badge">{{ step }}</span>
        <h3 class="dialog-head__title">{{ stepTitle }}</h3>
      </div>
    </template>

    <DriverPickerStep
      v-if="step === 1 && !isEdit"
      v-model="driverType"
      :drivers="visibleDrivers"
      @next="goStep2"
    />

    <div v-else class="form">
      <FormField label="账号名称" required>
        <AppInput v-model="name" placeholder="请输入账号名称" />
      </FormField>
      <DynamicForm :fields="fields" v-model="formValues" />
    </div>

    <template v-if="step === 2" #footer>
      <div class="step-footer">
        <div class="step-footer__left">
          <AppButton
            v-if="supportsOAuth"
            variant="primary"
            :disabled="oauthLoading || submitting"
            @click="handleOAuth"
          >
            {{ oauthLoading ? "正在获取…" : isEdit ? "重新获取 Token" : "自动获取 Token" }}
          </AppButton>
          <AppButton
            v-if="supportsQRLogin"
            variant="primary"
            :disabled="submitting"
            @click="openQRLogin"
          >
            {{ isEdit ? "重新扫码获取" : "扫码获取授权" }}
          </AppButton>
        </div>
        <div class="step-footer__right">
          <AppButton v-if="!isEdit" variant="secondary" @click="goPrevStep">← 上一步</AppButton>
          <AppButton variant="primary" :disabled="submitting" @click="submit">
            {{ submitting ? "测试中…" : isEdit ? "保存修改" : "添加账号" }}
          </AppButton>
        </div>
      </div>
    </template>
  </AppModal>

  <QrLoginModal
    :open="qrOpen"
    :driver-type="driverType"
    :config="buildConfig()"
    :device-options="selectedDriver?.qr_devices ?? []"
    :device-field="selectedDriver?.qr_device_field ?? ''"
    @success="onQRSuccess"
    @close="qrOpen = false"
  />
</template>

<style scoped>
.dialog-head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.step-badge {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--brand-gradient-h);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  font-size: 12px;
  flex-shrink: 0;
}
.dialog-head__title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text);
}

.form {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-height: 420px;
  overflow-y: auto;
  padding-right: 4px;
}

.step-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  gap: 12px;
  /* 手机小屏：按钮组允许换行，避免「自动获取 Token / 上一步 / 添加账号」横向溢出被裁 */
  flex-wrap: wrap;
}
.step-footer__left {
  flex-shrink: 0;
}
.step-footer__right {
  display: flex;
  gap: 10px;
  margin-left: auto;
}

:root[data-skin="brutal"] .step-badge {
  border: var(--brutal-bw) solid var(--brutal-ink);
  border-radius: 0;
  background: var(--brand);
  color: var(--text-on-brand);
}
</style>
