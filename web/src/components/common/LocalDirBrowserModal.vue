<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { http, getApiErrorMessage } from "@/api/client";
import type { Crumb } from "@/stores/browser";
import AppModal from "@/components/base/AppModal.vue";
import AppButton from "@/components/base/AppButton.vue";
import BreadcrumbNav from "@/components/file/BreadcrumbNav.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

export interface LocalBrowseDir {
  name: string;
  path: string;
}

export interface LocalBrowseResult {
  path: string;
  parent: string | null;
  dirs: LocalBrowseDir[];
  exists: boolean;
  writable?: boolean;
}

const props = withDefaults(
  defineProps<{
    open: boolean;
    initialPath?: string;
    title?: string;
    confirmText?: string;
  }>(),
  {
    title: "选择容器内目录",
    confirmText: "选择当前目录",
  },
);
const emit = defineEmits<{ close: []; select: [path: string] }>();

const loading = ref(false);
const error = ref("");
const dirs = ref<LocalBrowseDir[]>([]);
const currentPath = ref("");

const quickPaths = ["/app/strm", "/app/data", "/app/mounts", "/app", "/mnt", "/media", "/"];

const breadcrumb = computed<Crumb[]>(() => {
  const path = currentPath.value.trim() || "/";
  if (path === "/") return [{ id: "/", name: "根目录" }];
  const parts = path.split("/").filter(Boolean);
  const crumbs: Crumb[] = [{ id: "/", name: "根目录" }];
  let acc = "";
  for (const part of parts) {
    acc += `/${part}`;
    crumbs.push({ id: acc, name: part });
  }
  return crumbs;
});

async function load(path: string) {
  loading.value = true;
  error.value = "";
  try {
    const data = await http.get<LocalBrowseResult>("/admin/local-fs/browse", path ? { path } : undefined);
    currentPath.value = data.path;
    dirs.value = data.dirs ?? [];
  } catch (e) {
    error.value = getApiErrorMessage(e, "加载失败");
    dirs.value = [];
  } finally {
    loading.value = false;
  }
}

function go(path: string) {
  const next = path.trim();
  if (!next) return;
  void load(next);
}

function goToCrumb(index: number) {
  const crumb = breadcrumb.value[index];
  if (!crumb) return;
  go(crumb.id);
}

function openDir(dir: LocalBrowseDir) {
  go(dir.path);
}

function confirm() {
  const finalPath = currentPath.value.trim();
  if (!finalPath) return;
  emit("select", finalPath);
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    void load(props.initialPath?.trim() || "");
  },
);
</script>

<template>
  <AppModal :open="open" bare nested @close="emit('close')">
    <div class="local-dir-picker">
      <div class="folder-selector">
        <div class="folder-selector__header">
          <h3 class="folder-selector__title">{{ title }}</h3>
          <button
            type="button"
            class="folder-selector__close"
            aria-label="关闭"
            @click="emit('close')"
          >
            ×
          </button>
        </div>

        <div class="folder-selector__content">
          <BreadcrumbNav :items="breadcrumb" compact @navigate="goToCrumb" />

          <div class="local-dir-picker__quick">
            <span class="local-dir-picker__quick-label">常用</span>
            <button
              v-for="p in quickPaths"
              :key="p"
              type="button"
              class="local-dir-picker__chip"
              :class="{ active: currentPath === p }"
              @click="go(p)"
            >
              {{ p }}
            </button>
          </div>

          <div class="file-list folder-selector__list">
            <div class="folder-table-header" role="row">
              <div class="folder-table-heading col-name">
                <span>名称</span>
              </div>
            </div>

            <div class="folder-table-body">
              <div v-if="loading" class="folder-state">加载中…</div>
              <div v-else-if="error" class="folder-state error">{{ error }}</div>
              <div v-else-if="!dirs.length" class="folder-state">没有子目录</div>
              <template v-else>
                <div
                  v-for="dir in dirs"
                  :key="dir.path"
                  class="folder-table-row"
                  @click="openDir(dir)"
                  @dblclick="emit('select', dir.path)"
                >
                  <div class="folder-name-cell">
                    <span class="folder-name-icon"><SvgIcon name="folder" :size="18" /></span>
                    <span class="folder-name-text" :title="dir.name">{{ dir.name }}</span>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </div>

        <div class="folder-selector__footer">
          <AppButton
            variant="primary"
            class="folder-selector__confirm"
            :disabled="loading || !currentPath"
            @click="confirm"
          >
            {{ confirmText }}
          </AppButton>
        </div>
      </div>
    </div>
  </AppModal>
</template>

<style scoped>
.local-dir-picker {
  display: flex;
  width: min(90vw, 680px);
  height: min(86vh, 570px);
  min-height: 0;
  overflow: hidden;
  border-radius: var(--radius-md);
}

.folder-selector {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
}

.folder-selector__header {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 20px 24px 0;
}
.folder-selector__title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text);
  flex: 1;
  min-width: 0;
}
.folder-selector__close {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--text-muted);
  font-size: 20px;
  line-height: 1;
  width: 24px;
  height: 24px;
  padding: 0;
  cursor: pointer;
}
.folder-selector__close:hover {
  color: var(--text);
}

.folder-selector__content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 14px 24px 12px;
  box-sizing: border-box;
  overflow: visible;
}
.folder-selector__content :deep(.breadcrumb) {
  flex: none;
}

.local-dir-picker__quick {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-top: 10px;
}
.local-dir-picker__quick-label {
  font-size: 12px;
  color: var(--text-muted);
  margin-right: 2px;
}
.local-dir-picker__chip {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.local-dir-picker__chip:hover,
.local-dir-picker__chip.active {
  color: var(--brand);
  border-color: color-mix(in srgb, var(--brand) 35%, var(--border));
  background: var(--info-soft);
}

.folder-selector__list {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  margin-top: 6px;
  overflow: hidden;
}

.folder-table-header,
.folder-table-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-items: center;
}
.folder-table-header {
  flex-shrink: 0;
  min-height: 46px;
  margin: 0 0 6px;
  padding: 0 12px;
  background: var(--surface-muted);
  border-radius: var(--radius-md);
}
.folder-table-heading {
  min-width: 0;
  height: 100%;
  color: var(--text-muted);
  display: inline-flex;
  align-items: center;
  font-size: 14px;
  font-weight: 500;
}

.folder-table-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: 2px;
}
.folder-table-row {
  padding: 12px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background 0.15s ease;
}
.folder-table-row:hover {
  background: var(--surface-sunken);
}
.folder-name-cell {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
}
.folder-name-icon {
  flex-shrink: 0;
  line-height: 0;
  display: inline-flex;
}
.folder-name-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  color: var(--text-regular);
}
.folder-state {
  padding: 36px 16px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
.folder-state.error {
  color: var(--danger);
}

.folder-selector__footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  padding: 0 24px 24px;
}
.folder-selector__confirm {
  flex-shrink: 0;
  height: 38px;
  min-width: 140px;
  padding: 0 16px;
  font-size: 14px;
}
</style>
