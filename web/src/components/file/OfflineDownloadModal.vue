<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { offlineDownloadApi } from "@/api/offlineDownload";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import { formatSize } from "@/utils/format";
import type { OfflineDownloadCapabilities, OfflineDownloadTask, OfflineTorrentPreparation } from "@/types/offline-download";
import type { Crumb } from "@/stores/browser";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import FolderPickerModal from "./FolderPickerModal.vue";

const props = defineProps<{
  open: boolean;
  accountId: number | null;
  accountName: string;
  capability: OfflineDownloadCapabilities | null;
  currentParentId: string;
  currentDisplayPath: string;
  breadcrumb: Crumb[];
}>();

const emit = defineEmits<{
  close: [];
  created: [tasks: OfflineDownloadTask[]];
}>();

const sourceMode = ref<"url" | "bt">("url");
const providerKind = ref<"native" | "builtin">("native");
const urlText = ref("");
const fileName = ref("");
const targetParentId = ref("");
const targetDisplayPath = ref("/");
const folderPickerOpen = ref(false);
const submitting = ref(false);
const parsingTorrent = ref(false);
const torrentPreparation = ref<OfflineTorrentPreparation | null>(null);
const selectedTorrentIndexes = ref<number[]>([]);
const torrentInput = ref<HTMLInputElement | null>(null);

const nativeSupportsUrls = computed(() => Boolean(props.capability?.supports_urls));
const supportsBuiltin = computed(() => Boolean(props.capability?.builtin_enabled));
const showProviderPicker = computed(() => supportsBuiltin.value && nativeSupportsUrls.value);
const supportsTorrent = computed(() => providerKind.value === "native" && Boolean(props.capability?.supports_torrent));
const availableSourceModes = computed<("url" | "bt")[]>(() => {
  const modes: ("url" | "bt")[] = [];
  if (providerKind.value === "builtin" || nativeSupportsUrls.value) modes.push("url");
  if (supportsTorrent.value) modes.push("bt");
  return modes.length ? modes : ["url"];
});
const showModeRail = computed(() => availableSourceModes.value.length > 1);
const supportsBatchUrls = computed(() => (providerKind.value === "builtin" ? true : Boolean(props.capability?.supports_batch_urls)));
const supportedSchemes = computed(() =>
  providerKind.value === "builtin"
    ? ((props.capability?.builtin_url_schemes?.length ? props.capability.builtin_url_schemes : ["http", "https"]) ?? ["http", "https"])
    : (props.capability?.url_schemes ?? []),
);
const supportedSchemeText = computed(() =>
  supportedSchemes.value.map((item) => String(item).trim().toUpperCase()).filter(Boolean).join(" / "),
);
const urlLines = computed(() =>
  urlText.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean),
);
const selectedTorrentFiles = computed(() => {
  const selected = new Set(selectedTorrentIndexes.value);
  return torrentPreparation.value?.files.filter((file) => selected.has(file.index)) ?? [];
});
const selectedTorrentSize = computed(() =>
  selectedTorrentFiles.value.reduce((sum, file) => sum + file.size, 0),
);
const allTorrentSelected = computed(
  () => Boolean(torrentPreparation.value?.files.length) && selectedTorrentFiles.value.length === torrentPreparation.value?.files.length,
);
const submitDisabled = computed(() => {
  if (submitting.value || !props.accountId) return true;
  if (sourceMode.value === "bt") return !torrentPreparation.value || selectedTorrentIndexes.value.length === 0;
  return urlLines.value.length === 0;
});

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    providerKind.value = supportsBuiltin.value && !nativeSupportsUrls.value ? "builtin" : "native";
    sourceMode.value = "url";
    urlText.value = "";
    fileName.value = "";
    torrentPreparation.value = null;
    selectedTorrentIndexes.value = [];
    initTarget();
  },
  { immediate: true },
);

watch(availableSourceModes, (modes) => {
  if (!modes.includes(sourceMode.value)) {
    sourceMode.value = modes[0] ?? "url";
  }
}, { immediate: true });

function initTarget() {
  setTarget(props.currentParentId, props.currentDisplayPath);
}

function isRootTarget(parentId: string) {
  return !parentId || parentId === "0";
}

function nativeUsesDefaultRoot(parentId: string) {
  return providerKind.value === "native" && isRootTarget(parentId) && Boolean(props.capability && !props.capability.root_target_allowed);
}

function setTarget(parentId: string, displayPath: string) {
  if (nativeUsesDefaultRoot(parentId)) {
    targetParentId.value = "";
    targetDisplayPath.value = "来自:离线下载（网盘默认目录）";
    return;
  }
  targetParentId.value = parentId;
  targetDisplayPath.value = isRootTarget(parentId) ? "/" : displayPath || "/";
}

function targetResolved(payload: { parentId: string; path: string }) {
  folderPickerOpen.value = false;
  setTarget(payload.parentId, payload.path);
}

watch(providerKind, () => {
  if (isRootTarget(targetParentId.value)) setTarget(targetParentId.value, "/");
});

async function prepareTorrent(file: File | undefined) {
  if (!file || !props.accountId) return;
  if (!file.name.toLowerCase().endsWith(".torrent")) {
    toast.info("请选择 .torrent 种子文件");
    return;
  }
  parsingTorrent.value = true;
  try {
    const result = await offlineDownloadApi.prepareTorrent(props.accountId, file);
    torrentPreparation.value = result;
    selectedTorrentIndexes.value = result.files.filter((item) => item.wanted).map((item) => item.index);
    if (!selectedTorrentIndexes.value.length) {
      selectedTorrentIndexes.value = result.files.map((item) => item.index);
    }
  } catch (error) {
    toast.error(getApiErrorMessage(error, "BT 种子解析失败"));
  } finally {
    parsingTorrent.value = false;
    if (torrentInput.value) torrentInput.value.value = "";
  }
}

function onTorrentInput(event: Event) {
  void prepareTorrent((event.target as HTMLInputElement).files?.[0]);
}

function onTorrentDrop(event: DragEvent) {
  void prepareTorrent(event.dataTransfer?.files?.[0]);
}

function toggleTorrentFile(index: number) {
  const selected = new Set(selectedTorrentIndexes.value);
  if (selected.has(index)) selected.delete(index);
  else selected.add(index);
  selectedTorrentIndexes.value = [...selected];
}

function toggleAllTorrentFiles() {
  if (!torrentPreparation.value) return;
  selectedTorrentIndexes.value = allTorrentSelected.value
    ? []
    : torrentPreparation.value.files.map((item) => item.index);
}

function selectSourceMode(mode: "url" | "bt") {
  if (mode === "bt" && !supportsTorrent.value) return;
  sourceMode.value = mode;
}

async function submit() {
  if (!props.accountId || submitDisabled.value) return;
  const accountId = props.accountId;
  const mode = sourceMode.value;
  const nextProviderKind = providerKind.value;
  const nextTargetParentId = targetParentId.value;
  const nextTargetDisplayPath = targetDisplayPath.value;
  const nextFileName = fileName.value.trim() || undefined;
  const nextURLs = [...urlLines.value];
  const nextTorrentPreparation = torrentPreparation.value;
  const nextWanted = [...selectedTorrentIndexes.value];
  submitting.value = true;
  emit("close");
  toast.info("正在提交离线任务，可继续操作");
  try {
    if (mode === "bt") {
      const prep = nextTorrentPreparation;
      if (!prep) return;
      const task = await offlineDownloadApi.addTorrent({
        account_id: accountId,
        preparation_id: prep.preparation_id,
        wanted: nextWanted,
        target_parent_id: nextTargetParentId,
        target_display_path: nextTargetDisplayPath,
      });
      emit("created", [task]);
      toast.success("BT 离线下载任务已提交");
    } else {
      const tasks = await offlineDownloadApi.addURLs({
        account_id: accountId,
        provider_kind: nextProviderKind,
        urls: nextURLs,
        file_name: nextFileName,
        target_parent_id: nextTargetParentId,
        target_display_path: nextTargetDisplayPath,
      });
      emit("created", tasks);
      const failed = tasks.filter((task) => task.status === "failed").length;
      if (failed) toast.warning(`${tasks.length - failed} 个任务提交成功，${failed} 个失败`);
      else toast.success(`${tasks.length} 个离线下载任务已提交`);
    }
  } catch (error) {
    toast.error(getApiErrorMessage(error, "离线下载任务提交失败"));
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <AppModal :open="open" title="新建离线下载" size="lg" @close="emit('close')">
    <div class="offline-download-form">
      <div class="offline-capability">
        <span class="offline-capability__icon"><SvgIcon name="cloud" :size="24" /></span>
        <span class="offline-capability__body">
          <strong v-if="providerKind === 'builtin'">{{ accountName }}可使用内置下载器处理 HTTP/HTTPS 链接</strong>
          <strong v-else-if="supportsTorrent">{{ accountName }}支持链接和 BT 种子任务</strong>
          <strong v-else>{{ accountName }}支持 HTTP/HTTPS 离线下载</strong>
          <small v-if="providerKind === 'builtin'">下载完成后会自动交给上传任务继续入盘。</small>
          <small v-else-if="supportsTorrent">链接可以批量提交；BT 种子解析后可选择下载内容。</small>
          <small v-else>官方接口一次创建一个任务，根目录会使用网盘默认的“来自:离线下载”。</small>
        </span>
      </div>

      <div v-if="showProviderPicker" class="offline-source-tabs offline-source-tabs--provider">
        <button type="button" :class="{ active: providerKind === 'native' }" @click="providerKind = 'native'; sourceMode = 'url'">
          <SvgIcon name="cloud" :size="15" /> 原生下载器
        </button>
        <button type="button" :class="{ active: providerKind === 'builtin' }" @click="providerKind = 'builtin'; sourceMode = 'url'">
          <SvgIcon name="download" :size="15" /> 内置下载器
        </button>
      </div>

      <div class="offline-body" :class="{ 'offline-body--single': !showModeRail }">
        <div v-if="showModeRail" class="offline-mode-rail">
          <button
            type="button"
            class="offline-mode-btn"
            :class="{ active: sourceMode === 'url' }"
            title="链接任务"
            @click="selectSourceMode('url')"
          >
            <span class="offline-mode-btn__vertical">链接任务</span>
          </button>
          <button
            type="button"
            class="offline-mode-btn"
            :class="{ active: sourceMode === 'bt' }"
            title="BT 种子"
            @click="selectSourceMode('bt')"
          >
            <span class="offline-mode-btn__vertical">BT种子</span>
          </button>
        </div>

        <div class="offline-mode-panel">
          <div class="offline-mode-panel__head">
            <div class="offline-mode-panel__title-row">
              <strong>{{ sourceMode === "url" ? "下载链接" : "BT 种子" }}</strong>
              <button
                v-if="sourceMode === 'bt' && torrentPreparation"
                type="button"
                class="offline-mode-panel__action"
                @click="torrentPreparation = null; selectedTorrentIndexes = []"
              >
                重新选择
              </button>
            </div>
            <small v-if="sourceMode === 'url'">
              支持 {{ supportedSchemeText }}
              <template v-if="supportsBatchUrls">，当前 {{ urlLines.length }} 条</template>
              <template v-if="providerKind === 'builtin'">，下载完成后自动转入上传</template>
            </small>
            <div v-else class="offline-mode-panel__meta-row">
              <small>{{ torrentPreparation ? "已解析种子，可选择下载内容" : "上传种子文件后解析下载内容" }}</small>
              <small v-if="torrentPreparation">
                已选择 {{ selectedTorrentFiles.length }} 个文件，共 {{ formatSize(selectedTorrentSize) }}
              </small>
            </div>
          </div>

          <div class="offline-mode-panel__content" :class="{ 'is-bt': sourceMode === 'bt', 'is-url': sourceMode === 'url' }">
            <template v-if="sourceMode === 'url'">
              <label class="offline-field">
                <textarea
                  v-model="urlText"
                  class="offline-textarea"
                  :placeholder="supportsBatchUrls ? '一行一个链接，可批量提交' : '请输入一个 HTTP/HTTPS 下载链接'"
                  :rows="supportsBatchUrls ? 5 : 3"
                />
              </label>
              <label v-if="providerKind === 'native' && !supportsBatchUrls" class="offline-field">
                <span class="offline-field__label">自定义文件名 <em>可选</em></span>
                <AppInput v-model="fileName" placeholder="留空时由 123 云盘识别文件名；自定义时请手动填写后缀名" />
              </label>
            </template>

            <template v-else>
              <div
                v-if="!torrentPreparation"
                class="offline-torrent-drop"
                :class="{ loading: parsingTorrent }"
                @dragover.prevent
                @drop.prevent="onTorrentDrop"
                @click="!parsingTorrent && torrentInput?.click()"
              >
                <SvgIcon name="file" :size="30" />
                <strong>{{ parsingTorrent ? "正在上传并解析种子…" : "选择 .torrent 种子文件" }}</strong>
                <small>也可以把种子文件拖到这里，最大 16 MiB</small>
                <input ref="torrentInput" type="file" accept=".torrent,application/x-bittorrent" hidden @change="onTorrentInput" />
              </div>

              <div v-else class="offline-torrent-result">
                <div class="offline-torrent-files">
                  <label class="offline-torrent-file offline-torrent-file--head">
                    <input type="checkbox" :checked="allTorrentSelected" @change="toggleAllTorrentFiles" />
                    <span>选择下载内容</span><span>大小</span>
                  </label>
                  <label v-for="file in torrentPreparation.files" :key="file.index" class="offline-torrent-file">
                    <input
                      type="checkbox"
                      :checked="selectedTorrentIndexes.includes(file.index)"
                      @change="toggleTorrentFile(file.index)"
                    />
                    <span :title="file.path">{{ file.path }}</span><span>{{ formatSize(file.size) }}</span>
                  </label>
                </div>
              </div>
            </template>
          </div>
        </div>
      </div>

      <div class="offline-target">
        <span class="offline-target__icon"><SvgIcon name="folder" :size="22" /></span>
        <span class="offline-target__body"><small>保存位置</small><strong :title="targetDisplayPath">{{ targetDisplayPath }}</strong></span>
        <AppButton size="sm" @click="folderPickerOpen = true">更改目录</AppButton>
      </div>
    </div>

    <template #footer>
      <AppButton variant="primary" :disabled="submitDisabled" @click="submit">
        <SvgIcon name="cloud" :size="17" />
        {{ submitting ? "正在提交…" : "开始离线下载" }}
      </AppButton>
    </template>
  </AppModal>

  <FolderPickerModal
    :open="folderPickerOpen"
    title="选择离线下载目录"
    confirm-text="保存到当前目录"
    :account-id="accountId"
    :allow-create-folder="true"
    :show-refresh="false"
    :initial-breadcrumb="breadcrumb"
    @resolve="targetResolved"
    @close="folderPickerOpen = false"
  />
</template>

<style scoped>
.offline-download-form { display: flex; flex-direction: column; gap: 12px; }
.offline-capability { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border: 1px solid var(--tab-active-border); border-radius: 10px; background: var(--info-soft); }
.offline-capability__icon { width: 38px; height: 38px; display: inline-flex; align-items: center; justify-content: center; border-radius: 9px; background: var(--surface); color: var(--brand); }
.offline-capability__body { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.offline-capability__body strong { color: var(--text); font-size: 14px; }
.offline-capability__body small, .offline-field small { color: var(--text-muted); font-size: 12px; }
.offline-source-tabs { display: flex; gap: 8px; border-bottom: 1px solid var(--border-soft); }
.offline-source-tabs button { display: inline-flex; align-items: center; gap: 6px; padding: 9px 12px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--text-muted); font-weight: 600; cursor: pointer; }
.offline-source-tabs button.active { border-bottom-color: var(--brand); color: var(--brand); }
.offline-body {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr);
  gap: 0;
  align-items: stretch;
  min-height: 248px;
  height: 248px;
  border: 1px solid var(--border);
  border-radius: 11px;
  background: var(--surface);
  overflow: hidden;
}
.offline-body--single {
  grid-template-columns: 1fr;
  min-height: 248px;
  height: 248px;
  border: 0;
  border-radius: 0;
  background: transparent;
  overflow: hidden;
}
.offline-body--single .offline-mode-panel {
  min-height: 248px;
  height: 248px;
  padding: 0;
  background: transparent;
}
.offline-body--single .offline-mode-panel__head {
  min-height: 34px;
  padding: 0;
}
.offline-body--single .offline-mode-panel__content { gap: 14px; }
.offline-body--single .offline-field:first-child { margin-top: 0; }
.offline-body--single .offline-textarea { min-height: 168px; }
.offline-mode-rail {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0;
  padding: 0;
  background: transparent;
}
.offline-mode-btn {
  position: relative;
  flex: 1;
  display: flex; align-items: center; justify-content: center; width: 100%;
  min-height: 102px; padding: 8px 4px; border: 0;
  border-radius: 0;
  background: var(--surface-sunken);
  color: var(--text-regular);
  text-align: center;
  cursor: pointer;
}
.offline-mode-btn:first-child { border-radius: 11px 0 0 0; }
.offline-mode-btn:last-child { border-radius: 0 0 0 11px; }
.offline-mode-btn + .offline-mode-btn { border-top: 1px solid var(--border-soft); }
.offline-mode-btn.active {
  background: var(--surface);
  color: var(--brand);
  box-shadow: inset 2px 0 0 var(--brand);
}
.offline-mode-btn.active::after {
  content: "";
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 1px;
  background: var(--surface);
}
.offline-mode-btn.disabled { opacity: .52; cursor: not-allowed; }
.offline-mode-btn__vertical {
  writing-mode: vertical-rl;
  text-orientation: upright;
  letter-spacing: 1px;
  font-size: 12px;
  line-height: 1.15;
  font-weight: 700;
  color: inherit;
}
.offline-mode-panel {
  display: flex; flex-direction: column; gap: 10px;
  min-height: 248px; height: 248px; padding: 10px 12px; border: 0; border-radius: 0; background: transparent; box-sizing: border-box;
}
.offline-mode-panel__head { display: flex; flex-direction: column; gap: 3px; min-height: 34px; }
.offline-mode-panel__title-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.offline-mode-panel__head strong { color: var(--text); font-size: 14px; }
.offline-mode-panel__action { flex-shrink: 0; border: 0; padding: 0; background: transparent; color: var(--brand); font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; }
.offline-mode-panel__head small { color: var(--text-muted); font-size: 12px; line-height: 1.5; }
.offline-mode-panel__meta-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.offline-mode-panel__meta-row small:last-child { flex-shrink: 0; text-align: right; }
.offline-mode-panel__content { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 8px; overflow: hidden; }
.offline-mode-panel__content.is-bt { justify-content: flex-start; }
.offline-mode-panel__content.is-url .offline-field:first-child { flex: 1; min-height: 0; }
.offline-mode-panel__content.is-url .offline-field:first-child .offline-textarea { flex: 1; min-height: 0; height: 100%; }
.offline-field { display: flex; flex-direction: column; gap: 6px; }
.offline-field__label { color: var(--text); font-size: 13px; font-weight: 600; }
.offline-field__label em { color: var(--text-muted); font-size: 11px; font-style: normal; font-weight: 400; }
.offline-textarea { width: 100%; resize: none; min-height: 116px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); font: inherit; line-height: 1.6; box-sizing: border-box; }
.offline-textarea:focus { outline: none; border-color: var(--brand); }
.offline-torrent-drop { min-height: 0; flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 9px; border: 1px dashed var(--brand); border-radius: 11px; background: var(--surface); color: var(--brand); cursor: pointer; }
.offline-torrent-drop strong { color: var(--text); }
.offline-torrent-drop small { color: var(--text-muted); }
.offline-torrent-drop.loading { cursor: wait; opacity: .75; }
.offline-torrent-result { display: flex; flex-direction: column; gap: 9px; min-height: 0; flex: 1; overflow: hidden; }
.offline-torrent-files { flex: 1; min-height: 0; max-height: none; overflow: auto; background: transparent; }
.offline-torrent-file { display: grid; grid-template-columns: 24px minmax(0, 1fr) 88px; align-items: center; gap: 8px; min-height: 40px; padding: 0 4px 0 11px; border-bottom: 1px solid var(--border-soft); color: var(--text-regular); font-size: 12px; }
.offline-torrent-file > span:nth-child(2) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.offline-torrent-file > span:last-child { text-align: right; color: var(--text-muted); }
.offline-torrent-file--head { position: sticky; top: 0; z-index: 1; background: var(--modal-bg, var(--surface)); color: var(--text-muted); }
.offline-target { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-sunken); }
.offline-target__icon { color: var(--brand); }
.offline-target__body { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 2px; }
.offline-target__body small { color: var(--text-muted); }
.offline-target__body strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font-size: 13px; }
@media (max-width: 640px) {
  .offline-body { grid-template-columns: 1fr; min-height: auto; height: auto; }
  .offline-body--single { min-height: auto; height: auto; overflow: visible; }
  .offline-mode-panel,
  .offline-body--single .offline-mode-panel { min-height: auto; height: auto; }
  .offline-body--single .offline-mode-panel { padding: 0; }
  .offline-body--single .offline-mode-panel__head { padding: 0; }
  .offline-mode-rail { flex-direction: row; gap: 0; padding: 0; border-right: 0; border-bottom: 1px solid var(--border-soft); }
  .offline-mode-btn { flex: 1; min-width: 0; min-height: 56px; }
  .offline-mode-btn { width: 100%; border-radius: 0; }
  .offline-mode-btn:first-child { border-radius: 11px 11px 0 0; }
  .offline-mode-btn:last-child { border-radius: 0; }
  .offline-mode-btn + .offline-mode-btn { border-left: 1px solid var(--border-soft); }
  .offline-mode-btn.active::after { top: auto; left: 0; right: 0; bottom: -1px; width: auto; height: 1px; }
  .offline-mode-btn__vertical { writing-mode: initial; text-orientation: mixed; letter-spacing: 0; }
  .offline-mode-panel__meta-row { flex-wrap: wrap; }
  .offline-mode-panel__meta-row small:last-child { width: 100%; text-align: left; }
  .offline-target { align-items: flex-start; flex-wrap: wrap; }
  .offline-target .btn { width: 100%; }
  .offline-torrent-file { grid-template-columns: 22px minmax(0, 1fr) 70px; }
}
</style>
