<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { fetchStrmScrapeScopeDirectories } from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import FolderSelector, { type FolderSelection } from "@/components/file/FolderSelector.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = defineProps<{
  open: boolean;
  taskId: number;
  taskName?: string;
  excludedDirs: string[];
}>();

const emit = defineEmits<{
  close: [];
  save: [excludedDirs: string[]];
}>();

const selections = ref<FolderSelection[]>([]);
const searchValue = ref("");

function selectionFromPath(path: string): FolderSelection {
  const parts = path.split("/").filter(Boolean);
  const ancestors: string[] = [];
  for (let index = 0; index < parts.length - 1; index += 1) {
    ancestors.push(parts.slice(0, index + 1).join("/"));
  }
  return {
    id: parts.join("/"),
    name: parts[parts.length - 1] || path,
    path: `/${parts.join("/")}`,
    ancestorIds: ancestors,
  };
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    selections.value = props.excludedDirs.map(selectionFromPath);
    searchValue.value = "";
  },
  { immediate: true },
);

const selectedCount = computed(() => selections.value.length);
const rootAnchor = computed(() => ({
  parentId: "",
  path: "/",
  label: props.taskName || "任务目录",
}));

async function loadDirs(parentId: string) {
  return fetchStrmScrapeScopeDirectories(props.taskId, parentId);
}

function resolve(payload: { selections?: FolderSelection[] } = {}) {
  const dirs = (payload.selections ?? selections.value).map((item) => item.id).filter(Boolean);
  emit("save", dirs);
}
</script>

<template>
  <AppModal :open="open" bare nested @close="emit('close')">
    <div class="scrape-scope-picker">
      <header class="scrape-scope-picker__header">
        <h3>刮削范围</h3>
        <label class="scrape-scope-picker__search">
          <SvgIcon name="search" :size="15" />
          <input v-model="searchValue" type="search" placeholder="筛选当前目录文件夹" />
          <button v-if="searchValue" type="button" aria-label="清空筛选" @click="searchValue = ''">×</button>
        </label>
        <button type="button" aria-label="关闭" @click="emit('close')">×</button>
      </header>

      <FolderSelector
        :key="taskId"
        :loader="loadDirs"
        :account-id="0"
        :root-anchor="rootAnchor"
        :multi-select="true"
        :inverse-selection-display="true"
        :selected-items="selections"
        :show-close="false"
        :show-search="false"
        :search-value="searchValue"
        title=""
        confirm-text="保存范围"
        @update:selected-items="selections = $event"
        @update:search-value="searchValue = $event"
        @resolve="resolve"
        @cancel="emit('close')"
      />

      <footer class="scrape-scope-picker__footer">
        <span>{{ selectedCount ? `已排除 ${selectedCount} 个目录` : "未排除目录，将刮削全部内容" }}</span>
        <AppButton variant="primary" @click="resolve()">保存</AppButton>
      </footer>
    </div>
  </AppModal>
</template>

<style scoped>
.scrape-scope-picker {
  width: min(90vw, 680px);
  height: min(86vh, 590px);
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.scrape-scope-picker__header {
  display: flex;
  align-items: center;
  padding: 20px 24px 14px;
  gap: 16px;
}
.scrape-scope-picker__header h3 { margin: 0; color: var(--text); font-size: 18px; flex: 1; }
.scrape-scope-picker__header > button {
  border: 0; background: none; color: var(--text-muted); font-size: 22px; cursor: pointer;
}
.scrape-scope-picker__search {
  width: min(260px, 42vw);
  height: 36px;
  padding: 0 11px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-muted);
  display: flex;
  align-items: center;
  gap: 8px;
  box-sizing: border-box;
}
.scrape-scope-picker__search input {
  flex: 1; min-width: 0; border: 0; outline: 0; background: transparent;
  appearance: none; -webkit-appearance: none;
  color: var(--text); font: inherit; font-size: 13px;
}
.scrape-scope-picker__search input::-webkit-search-cancel-button,
.scrape-scope-picker__search input::-webkit-search-decoration {
  display: none;
  appearance: none;
  -webkit-appearance: none;
}
.scrape-scope-picker__search input::placeholder { color: var(--text-muted); }
.scrape-scope-picker__search button {
  width: 18px; height: 18px; padding: 0; border: 0; border-radius: 50%;
  background: transparent; color: var(--text-muted); font-size: 16px; cursor: pointer;
}
.scrape-scope-picker :deep(.folder-selector) { flex: 1 1 0; height: auto; min-height: 0; overflow: hidden; }
.scrape-scope-picker :deep(.folder-selector__header) { padding-top: 0; }
.scrape-scope-picker :deep(.folder-selector__footer) { display: none; }
.scrape-scope-picker__footer {
  flex: 0 0 auto; display: flex; align-items: center; justify-content: space-between; border-top: 1px solid var(--border);
  padding: 14px 24px; color: var(--text-muted); font-size: 13px;
}
@media (max-width: 640px) {
  .scrape-scope-picker { width: 94vw; height: min(88vh, 620px); }
  .scrape-scope-picker__header { padding-inline: 18px; }
  .scrape-scope-picker__search { width: min(210px, 52vw); }
}
</style>
