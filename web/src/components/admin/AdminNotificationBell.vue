<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import AppModal from "@/components/base/AppModal.vue";
import { ackRetentionScopeWarn } from "@/api/cacheRetention";
import {
  deleteAllNotifications,
  deleteNotification,
  fetchNotifications,
  fetchUnreadCount,
  isCacheScopeWarnNotification,
  isStrmScanWarnNotification,
  isStrmScrapeWarnNotification,
  markAllNotificationsRead,
  markNotificationRead,
  parseStrmScanFailures,
  parseStrmScrapeFailures,
  strmScanFailureKindLabel,
  strmScrapeFailureStageLabel,
  type NotificationItem,
} from "@/api/notifications";
import { confirm } from "@/composables/useConfirm";
import { formatTimeShort } from "@/utils/format";

const props = withDefaults(
  defineProps<{
    variant?: "main" | "sidebar";
  }>(),
  { variant: "main" },
);

const open = ref(false);
const loading = ref(false);
const unreadCount = ref(0);
const items = ref<NotificationItem[]>([]);
const detailItem = ref<NotificationItem | null>(null);
const detailOpen = ref(false);
const detailBusy = ref(false);
let pollTimer: ReturnType<typeof setInterval> | undefined;

const isMain = computed(() => props.variant === "main");

const badgeText = computed(() => {
  if (unreadCount.value <= 0) return "";
  return unreadCount.value > 99 ? "99+" : String(unreadCount.value);
});

const detailCanDismissScope = computed(() =>
  detailItem.value ? isCacheScopeWarnNotification(detailItem.value) : false,
);

interface NotificationFailureRow {
  label: string;
  name: string;
  path: string;
  reason: string;
}

const detailFailures = computed(() => {
  const item = detailItem.value;
  if (!item) {
    return { enabled: false, summary: "", items: [] as NotificationFailureRow[] };
  }
  if (isStrmScanWarnNotification(item)) {
    const parsed = parseStrmScanFailures(item.message);
    return {
      enabled: true,
      summary: parsed.summary,
      items: parsed.items.map((row) => ({
        label: strmScanFailureKindLabel(row.kind),
        name: "",
        path: row.path,
        reason: row.reason,
      })),
    };
  }
  if (isStrmScrapeWarnNotification(item)) {
    const parsed = parseStrmScrapeFailures(item.message);
    return {
      enabled: true,
      summary: parsed.summary,
      items: parsed.items.map((row) => ({
        label: strmScrapeFailureStageLabel(row.stage),
        name: row.name,
        path: row.path,
        reason: row.reason,
      })),
    };
  }
  return { enabled: false, summary: "", items: [] as NotificationFailureRow[] };
});

function levelIcon(level: string): string {
  switch (level) {
    case "error":
      return "✕";
    case "warning":
      return "!";
    case "success":
      return "✓";
    default:
      return "i";
  }
}

async function refreshUnread() {
  try {
    const data = await fetchUnreadCount();
    unreadCount.value = data.count ?? 0;
  } catch {}
}

async function loadList() {
  loading.value = true;
  try {
    const data = await fetchNotifications({ limit: 40 });
    items.value = data.items ?? [];
  } catch {
    items.value = [];
  } finally {
    loading.value = false;
  }
}

async function toggleOpen() {
  open.value = !open.value;
  if (open.value) {
    await Promise.all([loadList(), refreshUnread()]);
  }
}

async function handleMarkAll() {
  try {
    await markAllNotificationsRead();
    unreadCount.value = 0;
    items.value = items.value.map((it) => ({ ...it, is_read: true }));
  } catch {}
}

async function handleClearAll() {
  if (items.value.length === 0) return;
  try {
    await confirm({
      title: "清空全部通知？",
      message: "将删除全部通知，且不可恢复。",
      confirmText: "清空",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await deleteAllNotifications();
    items.value = [];
    unreadCount.value = 0;
    if (detailOpen.value) closeDetail();
  } catch {}
}

function closeDetail() {
  detailOpen.value = false;
  detailItem.value = null;
  detailBusy.value = false;
}

async function openDetail(item: NotificationItem) {
  detailItem.value = item;
  detailOpen.value = true;
  if (!item.is_read) {
    try {
      await markNotificationRead(item.id);
      item.is_read = true;
      unreadCount.value = Math.max(0, unreadCount.value - 1);
    } catch {}
  }
}

function removeItem(id: number) {
  const wasUnread = items.value.find((it) => it.id === id && !it.is_read);
  items.value = items.value.filter((it) => it.id !== id);
  if (wasUnread) {
    unreadCount.value = Math.max(0, unreadCount.value - 1);
  }
  if (detailItem.value?.id === id) {
    closeDetail();
  }
}

async function handleDeleteItem(item: NotificationItem, e?: Event) {
  e?.stopPropagation();
  try {
    await confirm({
      title: "删除这条通知？",
      message: item.title,
      confirmText: "删除",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  try {
    await deleteNotification(item.id);
    removeItem(item.id);
  } catch {}
}

async function handleDetailDelete() {
  if (!detailItem.value || detailBusy.value) return;
  try {
    await confirm({
      title: "删除这条通知？",
      message: detailItem.value.title,
      confirmText: "删除",
      cancelText: "取消",
      danger: true,
    });
  } catch {
    return;
  }
  detailBusy.value = true;
  try {
    await deleteNotification(detailItem.value.id);
    removeItem(detailItem.value.id);
  } catch {
    detailBusy.value = false;
  }
}

async function handleDismissScopeWarn() {
  const item = detailItem.value;
  if (!item || detailBusy.value || !isCacheScopeWarnNotification(item)) return;
  const taskId = item.ref_id ?? 0;
  if (taskId <= 0) return;
  detailBusy.value = true;
  try {
    await ackRetentionScopeWarn(taskId);
    items.value = items.value.filter(
      (it) => !(isCacheScopeWarnNotification(it) && it.ref_id === taskId),
    );
    closeDetail();
    await refreshUnread();
  } catch {
    detailBusy.value = false;
  }
}

function notifyListMessage(item: NotificationItem): string {
  if (isStrmScanWarnNotification(item)) {
    return parseStrmScanFailures(item.message).summary || item.message;
  }
  if (isStrmScrapeWarnNotification(item)) {
    return parseStrmScrapeFailures(item.message).summary || item.message;
  }
  return item.message;
}

function handleDocumentClick(e: MouseEvent) {
  const el = e.target as HTMLElement | null;
  if (!open.value || !el) return;
  if (el.closest(".notify-wrap")) return;
  open.value = false;
}

onMounted(() => {
  refreshUnread();
  pollTimer = setInterval(refreshUnread, 30000);
  document.addEventListener("click", handleDocumentClick);
});

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer);
  document.removeEventListener("click", handleDocumentClick);
});
</script>

<template>
  <div class="notify-wrap">
    <button
      type="button"
      :class="
        isMain
          ? { 'icon-btn': true, active: open || unreadCount > 0 }
          : { 'bell-btn': true, 'bell-btn--sidebar': true, 'bell-btn--active': open }
      "
      title="通知"
      @click.stop="toggleOpen"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 7h18s-3 0-3-7" />
        <path d="M13.73 21a2 2 0 0 1-3.46 0" />
      </svg>
      <span v-if="badgeText" :class="isMain ? 'badge' : 'notify-badge'">{{ badgeText }}</span>
    </button>

    <div
      v-if="open"
      class="notify-panel"
      :class="{ 'notify-panel--main': isMain, 'notify-panel--sidebar': !isMain }"
      @click.stop
    >
      <div class="notify-panel__head">
        <span class="notify-panel__title">通知</span>
        <div class="notify-panel__actions">
          <button
            v-if="unreadCount > 0"
            class="notify-panel__link"
            type="button"
            @click="handleMarkAll"
          >
            全部已读
          </button>
          <button
            v-if="items.length > 0"
            class="notify-panel__link notify-panel__link--danger"
            type="button"
            @click="handleClearAll"
          >
            清空
          </button>
        </div>
      </div>

      <div v-if="loading" class="notify-panel__empty">加载中…</div>
      <div v-else-if="items.length === 0" class="notify-panel__empty">暂无通知</div>
      <ul v-else class="notify-list">
        <li
          v-for="item in items"
          :key="item.id"
          class="notify-item"
          :class="[`notify-item--${item.level}`, { 'notify-item--unread': !item.is_read }]"
          @click="openDetail(item)"
        >
          <span class="notify-item__icon">{{ levelIcon(item.level) }}</span>
          <div class="notify-item__body">
            <div class="notify-item__title">{{ item.title }}</div>
            <div class="notify-item__msg">{{ notifyListMessage(item) }}</div>
            <div class="notify-item__time">{{ formatTimeShort(item.created_at) }}</div>
          </div>
          <button
            class="notify-item__delete"
            type="button"
            title="删除"
            aria-label="删除通知"
            @click="handleDeleteItem(item, $event)"
          >
            ×
          </button>
        </li>
      </ul>
    </div>

    <AppModal :open="detailOpen" size="md" @close="closeDetail">
      <template v-if="detailItem" #header>
        <div class="notify-detail__head">
          <span
            class="notify-detail__icon"
            :class="`notify-detail__icon--${detailItem.level}`"
          >
            {{ levelIcon(detailItem.level) }}
          </span>
          <h3 class="notify-detail__title">{{ detailItem.title }}</h3>
        </div>
      </template>

      <div v-if="detailItem" class="notify-detail__body">
        <p v-if="detailFailures.enabled" class="notify-detail__message">
          {{ detailFailures.summary || detailItem.message }}
        </p>
        <p v-else class="notify-detail__message">{{ detailItem.message }}</p>

        <div v-if="detailFailures.enabled && detailFailures.items.length" class="notify-detail__failures">
          <div class="notify-detail__failures-head">失败明细（{{ detailFailures.items.length }}）</div>
          <ul class="notify-detail__failure-list">
            <li v-for="(row, idx) in detailFailures.items" :key="`${row.path}-${idx}`">
              <span class="notify-detail__failure-kind">{{ row.label }}</span>
              <span class="notify-detail__failure-path" :title="row.path">
                {{ row.name ? `${row.name} · ${row.path}` : row.path }}
              </span>
              <span class="notify-detail__failure-reason">{{ row.reason }}</span>
            </li>
          </ul>
        </div>

        <div class="notify-detail__meta">{{ formatTimeShort(detailItem.created_at) }}</div>
      </div>

      <template v-if="detailItem" #footer>
        <button class="btn btn--ghost" type="button" :disabled="detailBusy" @click="closeDetail">
          关闭
        </button>
        <button
          v-if="detailCanDismissScope"
          class="btn btn--secondary"
          type="button"
          :disabled="detailBusy"
          @click="handleDismissScopeWarn"
        >
          不再提示
        </button>
        <button
          class="btn btn--danger"
          type="button"
          :disabled="detailBusy"
          @click="handleDetailDelete"
        >
          删除
        </button>
      </template>
    </AppModal>
  </div>
</template>

<style scoped>
.notify-wrap {
  position: relative;
}

.bell-btn {
  position: relative;
  width: 34px;
  height: 34px;
  border: none;
  border-radius: var(--radius-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: var(--transition);
}

.bell-btn svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.bell-btn--sidebar {
  background: rgba(255, 255, 255, 0.16);
  color: #fff;
}

.bell-btn--sidebar:hover,
.bell-btn--sidebar.bell-btn--active {
  background: rgba(255, 255, 255, 0.3);
}

.notify-badge {
  position: absolute;
  top: -2px;
  right: -2px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 999px;
  background: #ef4444;
  color: #fff;
  font-size: 10px;
  font-weight: 700;
  line-height: 16px;
  text-align: center;
}

.bell-btn--sidebar .notify-badge {
  border: 2px solid transparent;
}

.notify-panel {
  position: absolute;
  right: 0;
  width: 320px;
  max-height: 420px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  z-index: 30;
}

.notify-panel--main {
  top: calc(100% + 8px);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-pop);
}

.notify-panel--sidebar {
  left: 0;
  right: auto;
  bottom: calc(100% + 10px);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-pop);
}

.notify-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border-soft);
}

.notify-panel__title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
}

.notify-panel__actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.notify-panel__link {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  padding: 2px 4px;
}

.notify-panel__link:hover {
  color: var(--brand);
}

.notify-panel__link--danger:hover {
  color: #ef4444;
}

.notify-panel__empty {
  padding: 28px 14px;
  text-align: center;
  font-size: 13px;
  color: var(--text-muted);
}

.notify-list {
  list-style: none;
  margin: 0;
  padding: 6px 0;
  overflow-y: auto;
}

.notify-item {
  display: flex;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
  transition: background 0.15s;
  align-items: flex-start;
}

.notify-item:hover {
  background: var(--border-soft);
}

.notify-item--unread {
  background: color-mix(in srgb, var(--brand) 5%, transparent);
}

.notify-item__icon {
  flex: none;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  margin-top: 2px;
}

.notify-item--error .notify-item__icon {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}
.notify-item--warning .notify-item__icon {
  background: rgba(245, 158, 11, 0.15);
  color: #d97706;
}
.notify-item--success .notify-item__icon {
  background: rgba(16, 185, 129, 0.15);
  color: #059669;
}
.notify-item--info .notify-item__icon,
.notify-item:not(.notify-item--error):not(.notify-item--warning):not(.notify-item--success)
  .notify-item__icon {
  background: rgba(59, 130, 246, 0.15);
  color: #2563eb;
}

.notify-item__body {
  min-width: 0;
  flex: 1;
}

.notify-item__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  line-height: 1.35;
}

.notify-item__msg {
  margin-top: 3px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--text-muted);
  word-break: break-word;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.notify-item__time {
  margin-top: 4px;
  font-size: 11px;
  color: var(--text-muted);
  opacity: 0.85;
}

.notify-item__delete {
  flex: none;
  width: 22px;
  height: 22px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s, color 0.15s, background 0.15s;
}

.notify-item:hover .notify-item__delete {
  opacity: 1;
}

.notify-item__delete:hover {
  color: #ef4444;
  background: rgba(239, 68, 68, 0.08);
}

.notify-detail__head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.notify-detail__icon {
  flex: none;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 700;
}

.notify-detail__icon--error {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}
.notify-detail__icon--warning {
  background: rgba(245, 158, 11, 0.15);
  color: #d97706;
}
.notify-detail__icon--success {
  background: rgba(16, 185, 129, 0.15);
  color: #059669;
}
.notify-detail__icon--info,
.notify-detail__icon:not(.notify-detail__icon--error):not(.notify-detail__icon--warning):not(
    .notify-detail__icon--success
  ) {
  background: rgba(59, 130, 246, 0.15);
  color: #2563eb;
}

.notify-detail__title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  line-height: 1.35;
}

.notify-detail__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.notify-detail__message {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}

.notify-detail__meta {
  font-size: 12px;
  color: var(--text-muted);
}

.notify-detail__failures {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.notify-detail__failures-head {
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  background: var(--surface-sunken);
  border-bottom: 1px solid var(--border);
}

.notify-detail__failure-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 280px;
  overflow: auto;
}

.notify-detail__failure-list li {
  display: grid;
  grid-template-columns: 52px minmax(0, 1fr);
  gap: 4px 10px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border-soft, var(--border));
  font-size: 12px;
  line-height: 1.45;
}

.notify-detail__failure-list li:last-child {
  border-bottom: none;
}

.notify-detail__failure-kind {
  grid-row: span 2;
  align-self: start;
  font-weight: 700;
  color: var(--text-muted);
}

.notify-detail__failure-path {
  color: var(--text);
  word-break: break-all;
}

.notify-detail__failure-reason {
  grid-column: 2;
  color: #d97706;
  word-break: break-word;
}

@media (max-width: 768px) {
  .notify-panel {
    width: min(320px, calc(100vw - 48px));
  }

  .notify-item__delete {
    opacity: 1;
  }
}
</style>
