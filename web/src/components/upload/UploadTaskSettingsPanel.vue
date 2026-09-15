<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchSettings, saveSettings, type SettingItem } from "@/api/settings";
import { uploadApi } from "@/api/upload";
import { toast } from "@/composables/useToast";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import LocalDirBrowserModal from "@/components/common/LocalDirBrowserModal.vue";

const OFFLINE_KEYS = {
  tempDir: "builtin_offline_temp_dir",
  maxSpeed: "builtin_offline_max_speed_mb",
  btPort: "builtin_offline_bt_port",
} as const;

type OfflineForm = {
  tempDir: string;
  maxSpeed: string | number;
  btPort: string | number;
};

const props = defineProps<{
  open: boolean;
  serverConcurrency: number;
}>();

const emit = defineEmits<{
  "update:serverConcurrency": [number];
  close: [];
}>();

const loading = ref(true);
const loadedOnce = ref(false);
const transferSaving = ref(false);
const transferConcurrency = ref(3);
const transferMin = ref(1);
const transferMax = ref(5);

const offlineSaving = ref(false);
const offlineAvailable = ref(true);
const offlineLoadError = ref("");
const offlineItems = ref(new Map<string, SettingItem>());
const resolvedTempDir = ref("");
const localDirPickerOpen = ref(false);
const offlineForm = reactive<OfflineForm>({
  tempDir: "data/builtin_offline",
  maxSpeed: "0",
  btPort: "42069",
});
const savedOfflineForm = reactive<OfflineForm>({ ...offlineForm });

function itemRange(key: string, fallbackMin: number, fallbackMax: number) {
  const item = offlineItems.value.get(key);
  return {
    min: item?.min ?? fallbackMin,
    max: item?.max ?? fallbackMax,
  };
}

const maxSpeedRange = computed(() => itemRange(OFFLINE_KEYS.maxSpeed, 0, 10240));
const btPortRange = computed(() => itemRange(OFFLINE_KEYS.btPort, 0, 65535));

function settingValue(item: SettingItem, fallback: string) {
  const value = String(item.value || item.default || "").trim();
  return value || fallback;
}

function applyOfflineItems(items: SettingItem[]) {
  const byKey = new Map(items.map((item) => [item.key, item]));
  if (Object.values(OFFLINE_KEYS).some((key) => !byKey.has(key))) {
    offlineAvailable.value = false;
    offlineLoadError.value = "当前版本的内置下载设置不完整";
    return;
  }

  offlineItems.value = byKey;
  const next: OfflineForm = {
    tempDir: settingValue(byKey.get(OFFLINE_KEYS.tempDir)!, "data/builtin_offline"),
    maxSpeed: settingValue(byKey.get(OFFLINE_KEYS.maxSpeed)!, "0"),
    btPort: settingValue(byKey.get(OFFLINE_KEYS.btPort)!, "42069"),
  };
  Object.assign(offlineForm, next);
  Object.assign(savedOfflineForm, next);
  offlineAvailable.value = true;
  offlineLoadError.value = "";
}

async function loadPanelSettings() {
  loading.value = !loadedOnce.value;
  const [runtimeResult, settingsResult] = await Promise.allSettled([
    uploadApi.getRuntime(),
    fetchSettings(),
  ]);

  if (runtimeResult.status === "fulfilled") {
    const data = runtimeResult.value;
    transferConcurrency.value = data.concurrency;
    transferMin.value = data.concurrency_min ?? 1;
    transferMax.value = data.concurrency_max ?? 5;
    resolvedTempDir.value = data.builtin_temp_dir ?? "";
    emit("update:serverConcurrency", data.concurrency);
  } else {
    transferConcurrency.value = props.serverConcurrency || 3;
  }

  if (settingsResult.status === "fulfilled") {
    applyOfflineItems(settingsResult.value.items);
  } else {
    offlineAvailable.value = false;
    offlineLoadError.value = getApiErrorMessage(settingsResult.reason, "读取内置下载设置失败");
  }

  loadedOnce.value = true;
  loading.value = false;
}

async function applyTransferConcurrency(next: number) {
  if (next < transferMin.value || next > transferMax.value || transferSaving.value) return;
  const previous = transferConcurrency.value;
  transferConcurrency.value = next;
  transferSaving.value = true;
  try {
    const data = await uploadApi.updateRuntime(next);
    transferConcurrency.value = data.concurrency;
    emit("update:serverConcurrency", data.concurrency);
    toast.success("任务并发已更新");
  } catch (error) {
    transferConcurrency.value = previous;
    toast.error(getApiErrorMessage(error, "更新任务并发失败"));
  } finally {
    transferSaving.value = false;
  }
}

function stepTransfer(delta: number) {
  void applyTransferConcurrency(transferConcurrency.value + delta);
}

function integerError(value: string | number, min: number, max: number, label: string) {
  const raw = String(value).trim();
  if (!/^\d+$/.test(raw)) return `${label}需为整数`;
  const number = Number(raw);
  if (number < min || number > max) return `${label}范围为 ${min}–${max}`;
  return "";
}

const maxSpeedError = computed(() => {
  const range = maxSpeedRange.value;
  return integerError(offlineForm.maxSpeed, range.min, range.max, "下载限速");
});
const btPortError = computed(() => {
  const range = btPortRange.value;
  return integerError(offlineForm.btPort, range.min, range.max, "磁力下载端口");
});
const tempDirDisplay = computed(
  () => resolvedTempDir.value || String(offlineForm.tempDir || "").trim(),
);

async function saveOfflineValue(
  key: (typeof OFFLINE_KEYS)[keyof typeof OFFLINE_KEYS],
  value: string,
  successMessage: string,
) {
  if (!offlineAvailable.value || offlineSaving.value) return false;
  offlineSaving.value = true;
  try {
    const payload = await saveSettings({ [key]: value });
    applyOfflineItems(payload.items);
    toast.success(successMessage);
    return true;
  } catch (error) {
    Object.assign(offlineForm, savedOfflineForm);
    toast.error(getApiErrorMessage(error, "保存任务设置失败"));
    return false;
  } finally {
    offlineSaving.value = false;
  }
}

async function commitMaxSpeed() {
  if (maxSpeedError.value) {
    toast.error(maxSpeedError.value);
    Object.assign(offlineForm, savedOfflineForm);
    return;
  }
  const value = String(Number(offlineForm.maxSpeed));
  if (value === String(savedOfflineForm.maxSpeed)) return;
  await saveOfflineValue(OFFLINE_KEYS.maxSpeed, value, "下载限速已更新");
}

async function commitBTPort() {
  if (btPortError.value) {
    toast.error(btPortError.value);
    Object.assign(offlineForm, savedOfflineForm);
    return;
  }
  const value = String(Number(offlineForm.btPort));
  if (value === String(savedOfflineForm.btPort)) return;
  await saveOfflineValue(OFFLINE_KEYS.btPort, value, "磁力下载端口已更新");
}

async function selectTempDir(path: string) {
  localDirPickerOpen.value = false;
  const value = path.trim();
  if (!value || value === tempDirDisplay.value) return;
  offlineForm.tempDir = value;
  if (await saveOfflineValue(OFFLINE_KEYS.tempDir, value, "临时下载目录已更新")) {
    resolvedTempDir.value = value;
  }
}

watch(
  () => props.open,
  (open) => {
    if (open && !loading.value && (!loadedOnce.value || !offlineAvailable.value)) {
      void loadPanelSettings();
    }
    if (!open) localDirPickerOpen.value = false;
  },
);

onMounted(() => {
  void loadPanelSettings();
});
</script>

<template>
  <Transition name="task-settings">
    <div
      v-if="open && !loading"
      class="upload-settings-panel task-settings"
      role="dialog"
      aria-label="任务设置"
      @click.stop
    >
      <header class="task-settings__head">
        <strong>任务设置</strong>
        <button type="button" aria-label="关闭" @click="emit('close')">×</button>
      </header>

      <div class="task-settings__body">
        <p v-if="!offlineAvailable" class="task-settings__error">
          {{ offlineLoadError }}，关闭后重新打开可重试。
        </p>

        <div class="task-settings__grid">
          <div class="task-settings__item task-settings__item--stepper">
            <div class="task-settings__label">
              <strong>任务并发</strong>
              <small>三个队列独立使用此上限</small>
            </div>
            <div class="task-settings__stepper">
              <button
                type="button"
                :disabled="transferSaving || transferConcurrency <= transferMin"
                aria-label="减少任务并发"
                @click="stepTransfer(-1)"
              >
                −
              </button>
              <span>{{ transferConcurrency }}</span>
              <button
                type="button"
                :disabled="transferSaving || transferConcurrency >= transferMax"
                aria-label="增加任务并发"
                @click="stepTransfer(1)"
              >
                +
              </button>
            </div>
          </div>

          <label class="task-settings__item task-settings__field">
            <span class="task-settings__label">
              <strong>下载限速</strong>
              <small :class="{ 'is-error': maxSpeedError }">{{ maxSpeedError || "0 = 不限速" }}</small>
            </span>
            <span class="task-settings__input">
              <input
                v-model="offlineForm.maxSpeed"
                type="number"
                inputmode="numeric"
                step="1"
                :min="maxSpeedRange.min"
                :max="maxSpeedRange.max"
                :disabled="!offlineAvailable || offlineSaving"
                @change="commitMaxSpeed"
                @blur="commitMaxSpeed"
                @keyup.enter="commitMaxSpeed"
              />
              <span>MB/s</span>
            </span>
          </label>

          <label class="task-settings__item task-settings__item--wide task-settings__field">
            <span class="task-settings__label">
              <strong>磁力下载端口</strong>
              <small
                :class="{ 'is-error': btPortError }"
                title="用于磁力/BT 下载连接其他节点；Docker Bridge 网络需映射同一 TCP/UDP 端口，Host 网络无需映射"
              >
                {{ btPortError || "用于连接 BT 下载节点" }}
              </small>
            </span>
            <span class="task-settings__input">
              <input
                v-model="offlineForm.btPort"
                type="number"
                inputmode="numeric"
                step="1"
                :min="btPortRange.min"
                :max="btPortRange.max"
                :disabled="!offlineAvailable || offlineSaving"
                @change="commitBTPort"
                @blur="commitBTPort"
                @keyup.enter="commitBTPort"
              />
            </span>
          </label>

          <div class="task-settings__item task-settings__item--wide task-settings__path">
            <div class="task-settings__label">
              <strong>临时下载目录</strong>
              <small>选择容器内目录</small>
            </div>
            <AccountFolderField
              :display="tempDirDisplay"
              :title="tempDirDisplay"
              placeholder="选择临时下载目录"
              browse-label="浏览"
              @browse="localDirPickerOpen = true"
            />
          </div>
        </div>
      </div>
    </div>
  </Transition>

  <LocalDirBrowserModal
    :open="localDirPickerOpen"
    :initial-path="tempDirDisplay"
    title="选择临时下载目录"
    confirm-text="使用当前目录"
    @close="localDirPickerOpen = false"
    @select="selectTempDir"
  />
</template>

<style scoped>
.task-settings {
  position: absolute;
  top: calc(100% + 10px);
  right: 0;
  display: flex;
  flex-direction: column;
  width: 720px;
  max-width: calc(100vw - 32px);
  max-height: calc(min(720px, 86vh) - 58px);
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 16px;
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  z-index: 130;
}

.task-settings-enter-active,
.task-settings-leave-active {
  transform-origin: top right;
  transition: opacity 0.14s ease, transform 0.14s ease;
}

.task-settings-enter-from,
.task-settings-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.985);
}

.task-settings__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: none;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-soft);
  background: var(--panel-head-bg, var(--surface-sunken));
}

.task-settings__head strong {
  color: var(--text);
  font-size: 18px;
}

.task-settings__head button {
  width: 30px;
  height: 30px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--text-muted);
  font-size: 25px;
  line-height: 1;
  cursor: pointer;
}

.task-settings__head button:hover {
  background: var(--surface-hover);
  color: var(--text);
}

.task-settings__body {
  flex: 1;
  min-height: 0;
  padding: 18px;
  overflow-y: auto;
}

.task-settings__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.task-settings__item {
  min-width: 0;
  min-height: 78px;
  padding: 15px 16px;
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  background: var(--surface-sunken);
}

.task-settings__item--wide {
  grid-column: 1 / -1;
}

.task-settings__item--stepper,
.task-settings__field {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.task-settings__label {
  display: grid;
  gap: 5px;
  min-width: 0;
}

.task-settings__label strong {
  color: var(--text);
  font-size: 15px;
  font-weight: 650;
  white-space: nowrap;
}

.task-settings__label small {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-settings__label small.is-error,
.task-settings__error {
  color: var(--danger);
}

.task-settings__stepper {
  display: grid;
  grid-template-columns: 38px 44px 38px;
  align-items: center;
  flex: none;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
}

.task-settings__stepper button {
  height: 38px;
  border: 0;
  background: transparent;
  color: var(--text);
  font-size: 20px;
  cursor: pointer;
}

.task-settings__stepper button:hover:not(:disabled) {
  background: var(--surface-hover);
}

.task-settings__stepper button:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.task-settings__stepper span {
  color: var(--text);
  font-size: 15px;
  font-weight: 700;
  text-align: center;
}

.task-settings__input {
  display: flex;
  align-items: center;
  width: 154px;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
}

.task-settings__input:focus-within {
  border-color: var(--brand);
}

.task-settings__input input {
  width: 100%;
  min-width: 0;
  padding: 10px 12px;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text);
  font: inherit;
  font-size: 14px;
}

.task-settings__input > span {
  flex: none;
  padding-right: 11px;
  color: var(--text-muted);
  font-size: 11px;
}

.task-settings__path {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  align-items: center;
  gap: 14px;
}

.task-settings__path :deep(.account-folder-field) {
  min-height: 42px;
}

.task-settings__error {
  margin: 0 0 14px;
  padding: 10px 12px;
  border-radius: 9px;
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  font-size: 12px;
}

:global(.upload-task-panel.is-expanded) .task-settings {
  max-height: calc(100vh - 64px);
}

@media (max-width: 768px) {
  .task-settings {
    max-height: calc(100vh - 78px);
  }

  .task-settings__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 520px) {
  .task-settings {
    max-width: calc(100vw - 20px);
  }

  .task-settings__head {
    padding: 14px 16px;
  }

  .task-settings__body {
    padding: 12px;
  }

  .task-settings__item {
    padding: 13px;
  }

  .task-settings__path {
    grid-template-columns: 1fr;
  }
}
</style>
