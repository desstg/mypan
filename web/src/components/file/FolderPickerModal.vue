<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { filesApi } from "@/api/files";
import type { Account, BrowserFavoriteItem } from "@/api/types";
import type { Crumb } from "@/stores/browser";
import AppModal from "@/components/base/AppModal.vue";
import DriverIcon from "@/components/driver/DriverIcon.vue";
import FolderSelector from "./FolderSelector.vue";
import type { FolderSelection } from "./FolderSelector.vue";

const props = withDefaults(
  defineProps<{
    open: boolean;
    title?: string;
    confirmText?: string;
    // 是否在左侧显示账号选择面板（跨盘场景）；关闭时使用固定 accountId。
    selectableAccount?: boolean;
    accountId?: number | null;
    accounts?: Account[];
    excludedFolderIds?: string[];
    allowCreateFolder?: boolean;
    showRefresh?: boolean;
    // 初始定位面包屑，仅对初始 accountId 生效。
    initialBreadcrumb?: Crumb[];
    initialPath?: string;
    rootAnchor?: { parentId: string; path: string; label?: string };
    multiSelect?: boolean;
    initialSelections?: FolderSelection[];
    initialLocationMode?: "preserve" | "root";
    selectionRestoreMode?: "preserve" | "reset";
  }>(),
  {
    title: "选择目录",
    confirmText: "选择当前目录",
    selectableAccount: false,
    accountId: null,
    accounts: () => [],
    excludedFolderIds: () => [],
    allowCreateFolder: false,
    showRefresh: true,
    initialBreadcrumb: () => [],
    initialPath: "",
    rootAnchor: undefined,
    multiSelect: false,
    initialSelections: () => [],
    initialLocationMode: "preserve",
    selectionRestoreMode: "preserve",
  },
);

const emit = defineEmits<{
  close: [];
  resolve: [payload: {
    accountId: number;
    accountName: string;
    parentId: string;
    path: string;
    selections?: FolderSelection[];
  }];
}>();

const selAccount = ref<number | null>(props.accountId);
const selectedItems = ref<FolderSelection[]>([]);
const favorites = ref<BrowserFavoriteItem[]>([]);
const favoritesLoading = ref(false);
const accountListRef = ref<HTMLElement | null>(null);
const accountListScrollable = ref(false);
const accountListAtTop = ref(true);
const accountListAtBottom = ref(true);
let favoritesSeq = 0;
let accountScrollDelay: ReturnType<typeof setTimeout> | null = null;
let accountScrollRepeat: ReturnType<typeof setInterval> | null = null;

function updateAccountScrollState() {
  const el = accountListRef.value;
  if (!el) {
    accountListScrollable.value = false;
    accountListAtTop.value = true;
    accountListAtBottom.value = true;
    return;
  }
  const maxScroll = Math.max(0, el.scrollHeight - el.clientHeight);
  accountListScrollable.value = maxScroll > 2;
  accountListAtTop.value = el.scrollTop <= 2;
  accountListAtBottom.value = el.scrollTop >= maxScroll - 2;
}

function scrollAccountList(direction: -1 | 1, smooth = true) {
  const el = accountListRef.value;
  if (!el) return;
  el.scrollBy({ top: direction * 64, behavior: smooth ? "smooth" : "auto" });
  window.setTimeout(updateAccountScrollState, smooth ? 180 : 0);
}

function stopAccountScroll() {
  if (accountScrollDelay) clearTimeout(accountScrollDelay);
  if (accountScrollRepeat) clearInterval(accountScrollRepeat);
  accountScrollDelay = null;
  accountScrollRepeat = null;
}

function startAccountScroll(direction: -1 | 1) {
  stopAccountScroll();
  accountScrollDelay = setTimeout(() => {
    accountScrollRepeat = setInterval(() => {
      if ((direction < 0 && accountListAtTop.value) || (direction > 0 && accountListAtBottom.value)) {
        stopAccountScroll();
        return;
      }
      scrollAccountList(direction, false);
    }, 90);
  }, 360);
}

function initAccount() {
  if (props.accountId != null) {
    selAccount.value = props.accountId;
    return;
  }
  selAccount.value = props.accounts[0]?.id ?? null;
}

function initSelections() {
  selectedItems.value = props.multiSelect && props.selectionRestoreMode === "preserve"
    ? props.initialSelections.map((item) => ({
      ...item,
      ancestorIds: [...(item.ancestorIds ?? [])],
    }))
    : [];
}

function resetFavorites() {
  favorites.value = [];
  favoritesLoading.value = false;
}

const effectiveInitialBreadcrumb = computed(() =>
  props.initialLocationMode === "root" ? [] : props.initialBreadcrumb,
);

const effectiveInitialPath = computed(() =>
  props.initialLocationMode === "root" ? "" : props.initialPath,
);

const effectiveRootAnchor = computed(() =>
  props.initialLocationMode === "root" ? undefined : props.rootAnchor,
);

watch(
  () => props.open,
  (open) => {
    if (open) {
      initAccount();
      initSelections();
      void nextTick(updateAccountScrollState);
      return;
    }
    favoritesSeq += 1;
    resetFavorites();
  },
  { immediate: true },
);

watch(
  () => props.accounts.length,
  () => void nextTick(updateAccountScrollState),
);

onMounted(() => window.addEventListener("resize", updateAccountScrollState));
onBeforeUnmount(() => {
  stopAccountScroll();
  window.removeEventListener("resize", updateAccountScrollState);
});

watch(
  () => [props.open, selAccount.value] as const,
  ([open, accountId]) => {
    if (!open) return;
    void loadFavorites(accountId);
  },
  { immediate: true },
);

const accountName = computed(
  () => props.accounts.find((a) => a.id === selAccount.value)?.name ?? "账号",
);

function accountDriverLabel(account: Account) {
  return account.driver_card_name?.trim() || account.driver_type;
}

function selectAccount(accountId: number) {
  if (selAccount.value === accountId) return;
  selAccount.value = accountId;
  selectedItems.value = [];
}

async function loadFavorites(accountId: number | null) {
  const requestSeq = ++favoritesSeq;
  if (accountId == null) {
    resetFavorites();
    return;
  }
  favoritesLoading.value = true;
  try {
    const data = await filesApi.getFavorites(accountId);
    if (requestSeq !== favoritesSeq || !props.open || selAccount.value !== accountId) return;
    favorites.value = data.items;
  } catch {
    if (requestSeq !== favoritesSeq || !props.open || selAccount.value !== accountId) return;
    favorites.value = [];
  } finally {
    if (requestSeq === favoritesSeq) {
      favoritesLoading.value = false;
    }
  }
}

function onResolve(folder: {
  accountId: number;
  parentId: string;
  path: string;
  selections?: FolderSelection[];
}) {
  emit("resolve", { ...folder, accountName: accountName.value });
}
</script>

<template>
  <AppModal :open="open" bare @close="emit('close')">
    <div class="folder-picker" :class="{ 'folder-picker--accounts': selectableAccount }">
      <aside v-if="selectableAccount" class="folder-picker__accounts">
        <div class="folder-picker__accounts-head">
          <div class="folder-picker__accounts-title">选择账号</div>
          <div v-if="accountListScrollable" class="folder-picker__accounts-scroll-actions" aria-label="滚动账号列表">
            <button
              type="button"
              class="folder-picker__accounts-scroll-btn folder-picker__accounts-scroll-btn--up"
              aria-label="向上滚动账号列表"
              :disabled="accountListAtTop"
              @click="scrollAccountList(-1)"
              @pointerdown="startAccountScroll(-1)"
              @pointerup="stopAccountScroll"
              @pointercancel="stopAccountScroll"
              @pointerleave="stopAccountScroll"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m6 9 6 6 6-6" />
              </svg>
            </button>
            <button
              type="button"
              class="folder-picker__accounts-scroll-btn folder-picker__accounts-scroll-btn--down"
              aria-label="向下滚动账号列表"
              :disabled="accountListAtBottom"
              @click="scrollAccountList(1)"
              @pointerdown="startAccountScroll(1)"
              @pointerup="stopAccountScroll"
              @pointercancel="stopAccountScroll"
              @pointerleave="stopAccountScroll"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m6 9 6 6 6-6" />
              </svg>
            </button>
          </div>
        </div>
        <div
          ref="accountListRef"
          class="folder-picker__accounts-list"
          :class="{ 'is-scrollable': accountListScrollable }"
          @scroll.passive="updateAccountScrollState"
        >
          <div v-if="!accounts.length" class="folder-picker__accounts-empty">
            没有可用账号<br />请先到「存储管理」添加
          </div>
          <button
            v-for="a in accounts"
            :key="a.id"
            type="button"
            class="folder-picker__account"
            :class="{ active: a.id === selAccount }"
            @click="selectAccount(a.id)"
          >
            <DriverIcon
              :name="accountDriverLabel(a)"
              :color="a.driver_card_color"
              :logo="a.driver_card_logo"
              :size="28"
            />
            <span class="folder-picker__account-body">
              <span class="folder-picker__account-name">{{ a.name }}</span>
              <span class="folder-picker__account-sub">{{ accountDriverLabel(a) }}</span>
            </span>
          </button>
        </div>
      </aside>

      <div class="folder-picker__main">
        <FolderSelector
          v-if="selAccount != null"
          :key="selAccount"
          :account-id="selAccount"
          :title="title"
          :confirm-text="confirmText"
          :excluded-folder-ids="excludedFolderIds"
          :allow-create-folder="allowCreateFolder"
          :show-refresh="showRefresh"
          :initial-breadcrumb="selAccount === accountId ? effectiveInitialBreadcrumb : []"
          :initial-path="selAccount === accountId ? effectiveInitialPath : ''"
          :root-anchor="selAccount === accountId ? effectiveRootAnchor : undefined"
          :multi-select="multiSelect"
          :selected-items="multiSelect ? selectedItems : undefined"
          :favorites="favorites"
          :favorites-loading="favoritesLoading"
          @update:selected-items="selectedItems = $event"
          @resolve="onResolve"
          @cancel="emit('close')"
        />
        <div v-else class="folder-picker__placeholder">请选择左侧账号</div>
      </div>
    </div>
  </AppModal>
</template>

<style scoped>
.folder-picker {
  display: flex;
  width: min(90vw, 680px);
  height: min(86vh, 570px);
  min-height: 0;
}
.folder-picker--accounts {
  width: min(94vw, 900px);
}

.folder-picker__accounts {
  width: 220px;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 18px 12px 18px 18px;
  border-right: 1px solid var(--border);
}
.folder-picker__accounts-head {
  min-height: 30px;
  padding: 0 6px 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.folder-picker__accounts-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
}
.folder-picker__accounts-scroll-actions {
  display: inline-flex;
  align-items: center;
  gap: 1px;
}
.folder-picker__accounts-scroll-btn {
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  display: inline-grid;
  place-items: center;
  cursor: pointer;
  transition: color 0.15s ease, background 0.15s ease;
  user-select: none;
  -webkit-user-select: none;
  touch-action: none;
}
.folder-picker__accounts-scroll-btn svg {
  width: 13px;
  height: 13px;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.folder-picker__accounts-scroll-btn--up svg {
  transform: rotate(180deg);
}
.folder-picker__accounts-scroll-btn:hover:not(:disabled) {
  background: var(--surface-hover);
  color: var(--brand);
}
.folder-picker__accounts-scroll-btn:disabled {
  opacity: 0.28;
  cursor: default;
}
.folder-picker__accounts-list {
  flex: 1;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 6px 0;
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.folder-picker__accounts-list.is-scrollable {
  -webkit-mask-image: linear-gradient(
    to bottom,
    transparent 0,
    #000 8px,
    #000 calc(100% - 8px),
    transparent 100%
  );
  mask-image: linear-gradient(
    to bottom,
    transparent 0,
    #000 8px,
    #000 calc(100% - 8px),
    transparent 100%
  );
}
.folder-picker__accounts-list::-webkit-scrollbar {
  display: none;
}
.folder-picker__account {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}
.folder-picker__account-body {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.folder-picker__account:hover {
  background: var(--surface-sunken);
}
.folder-picker__account.active {
  background: var(--info-soft);
}
.folder-picker__account-name {
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.folder-picker__account.active .folder-picker__account-name {
  color: var(--brand);
}
.folder-picker__account-sub {
  font-size: 12px;
  color: var(--text-muted);
}
.folder-picker__accounts-empty {
  padding: 22px 14px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.7;
}

.folder-picker__main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.folder-picker__placeholder {
  margin: auto;
  color: var(--text-muted);
  font-size: 13px;
}

@media (max-width: 640px) {
  .folder-picker,
  .folder-picker--accounts {
    flex-direction: column;
    width: 94vw;
  }
  .folder-picker__accounts {
    width: 100%;
    border-right: none;
    border-bottom: 1px solid var(--border);
    max-height: 160px;
  }
}
</style>
