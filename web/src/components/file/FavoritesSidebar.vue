<script setup lang="ts">
import { ref } from "vue";
import type { BrowserFavoriteItem } from "@/api/types";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = defineProps<{
  items: BrowserFavoriteItem[];
  currentCrumbIds: string[];
  currentFolderFavorited: boolean;
  dragActive?: boolean;
  activeDropTargetId?: string;
  canDropOnFavorite?: (item: BrowserFavoriteItem) => boolean;
}>();

const emit = defineEmits<{
  "add-current": [];
  open: [item: BrowserFavoriteItem];
  rename: [item: BrowserFavoriteItem];
  remove: [folderId: string];
  move: [folderId: string, direction: -1 | 1];
  "drag-enter": [item: BrowserFavoriteItem];
  "drag-leave": [item: BrowserFavoriteItem];
  drop: [item: BrowserFavoriteItem];
}>();

const editing = ref(false);

function formatFavoritePath(item: BrowserFavoriteItem) {
  const names = item.crumbs
    .map((crumb) => crumb.name)
    .filter((name) => name && name !== "根目录");
  return names.length ? `/${names.join("/")}` : "/";
}

function isFavoriteActive(item: BrowserFavoriteItem, currentCrumbIds: string[]) {
  const favoriteCrumbIds = item.crumbs.map((crumb) => crumb.id);
  return (
    favoriteCrumbIds.length === currentCrumbIds.length &&
    favoriteCrumbIds.every((id, index) => id === currentCrumbIds[index])
  );
}

function isDropTarget(item: BrowserFavoriteItem) {
  return props.activeDropTargetId === item.id;
}

function canDrop(item: BrowserFavoriteItem) {
  return Boolean(props.dragActive) && !editing.value && (props.canDropOnFavorite?.(item) ?? false);
}

function handleDragEnter(event: DragEvent, item: BrowserFavoriteItem) {
  if (!canDrop(item)) return;
  event.preventDefault();
  emit("drag-enter", item);
}

function handleDragOver(event: DragEvent, item: BrowserFavoriteItem) {
  if (!canDrop(item)) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
  emit("drag-enter", item);
}

function handleDragLeave(event: DragEvent, item: BrowserFavoriteItem) {
  if (!props.dragActive) return;
  const currentTarget = event.currentTarget as Node | null;
  const relatedTarget = event.relatedTarget as Node | null;
  if (currentTarget && relatedTarget && currentTarget.contains(relatedTarget)) return;
  emit("drag-leave", item);
}

function handleDrop(event: DragEvent, item: BrowserFavoriteItem) {
  if (!props.dragActive) return;
  event.preventDefault();
  emit("drop", item);
}
</script>

<template>
  <aside class="favorites-sidebar">
    <table class="favorites-sidebar__table">
      <thead>
        <tr>
          <th>
            <div class="favorites-sidebar__head">
              <span class="favorites-sidebar__title">收藏夹</span>
              <span class="favorites-sidebar__head-actions">
                <button
                  v-if="items.length"
                  type="button"
                  class="favorites-sidebar__edit"
                  :class="{ active: editing }"
                  :title="editing ? '退出编辑' : '编辑收藏夹'"
                  :aria-label="editing ? '退出编辑' : '编辑收藏夹'"
                  @click="editing = !editing"
                >
                  <span class="favorites-sidebar__edit-icon" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  class="favorites-sidebar__add"
                  :class="{ active: currentFolderFavorited }"
                  :title="currentFolderFavorited ? '当前文件夹已收藏' : '收藏当前文件夹'"
                  :aria-label="currentFolderFavorited ? '当前文件夹已收藏' : '收藏当前文件夹'"
                  @click="emit('add-current')"
                />
              </span>
            </div>
          </th>
        </tr>
      </thead>
      <tbody v-if="items.length">
        <tr
          v-for="item in items"
          :key="item.id"
          class="favorites-sidebar__row"
          :class="{
            active: isFavoriteActive(item, currentCrumbIds),
            editing,
            'drop-target': isDropTarget(item),
          }"
          :title="formatFavoritePath(item)"
          @dragenter="handleDragEnter($event, item)"
          @dragover="handleDragOver($event, item)"
          @dragleave="handleDragLeave($event, item)"
          @drop="handleDrop($event, item)"
        >
          <td class="favorites-sidebar__cell">
            <component
              :is="editing ? 'div' : 'button'"
              :type="editing ? undefined : 'button'"
              class="favorites-sidebar__main"
              @click="!editing && emit('open', item)"
            >
              <span class="favorites-sidebar__label">{{ item.name }}</span>
              <span v-if="!editing" class="favorites-sidebar__path">{{ formatFavoritePath(item) }}</span>
              <span v-else class="favorites-sidebar__actions-row">
                <button
                  type="button"
                  class="favorites-sidebar__action favorites-sidebar__action--rename"
                  title="重命名收藏"
                  aria-label="重命名收藏"
                  @click.stop="emit('rename', item)"
                >
                  <span class="favorites-sidebar__rename-icon" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  class="favorites-sidebar__action favorites-sidebar__action--up"
                  title="上移"
                  aria-label="上移"
                  :disabled="items[0]?.id === item.id"
                  @click.stop="emit('move', item.id, -1)"
                >
                  <SvgIcon name="chevron-down" :size="12" class-name="favorites-sidebar__action-icon favorites-sidebar__action-icon--up" />
                </button>
                <button
                  type="button"
                  class="favorites-sidebar__action favorites-sidebar__action--down"
                  title="下移"
                  aria-label="下移"
                  :disabled="items[items.length - 1]?.id === item.id"
                  @click.stop="emit('move', item.id, 1)"
                >
                  <SvgIcon name="chevron-down" :size="12" class-name="favorites-sidebar__action-icon" />
                </button>
                <button
                  type="button"
                  class="favorites-sidebar__action favorites-sidebar__action--remove"
                  title="移除收藏"
                  aria-label="移除收藏"
                  @click.stop="emit('remove', item.id)"
                />
              </span>
            </component>
          </td>
        </tr>
        <tr class="favorites-sidebar__filler" aria-hidden="true">
          <td />
        </tr>
      </tbody>
      <tbody v-else>
        <tr class="favorites-sidebar__empty-row">
          <td class="favorites-sidebar__empty">暂无收藏</td>
        </tr>
      </tbody>
    </table>
  </aside>
</template>

<style scoped>
.favorites-sidebar {
  min-width: 0;
  padding: 0;
  border-right: 1px solid var(--border-soft);
  background: var(--surface);
}

.favorites-sidebar__table {
  width: 100%;
  height: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.favorites-sidebar__table th,
.favorites-sidebar__table td {
  box-sizing: border-box;
}

.favorites-sidebar__table th {
  height: 52px;
  padding: 0;
  background: var(--surface-muted);
  text-align: left;
  font-weight: 500;
  font-size: 13px;
  color: var(--text-muted);
  vertical-align: middle;
  border-bottom: none;
  box-shadow: inset 0 -1px 0 var(--border-soft);
}

.favorites-sidebar__table td {
  height: 54px;
  padding: 0;
  vertical-align: middle;
  background: var(--surface);
}

.favorites-sidebar__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 12px 0 16px;
}

.favorites-sidebar__head-actions {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
}

.favorites-sidebar__title {
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 500;
  line-height: 1;
}

.favorites-sidebar__add,
.favorites-sidebar__edit {
  appearance: none;
  width: 20px;
  height: 20px;
  position: relative;
  border: none;
  background: transparent;
  outline: none;
  color: var(--text-muted);
  flex: 0 0 auto;
  transition: all 0.2s ease;
}

.favorites-sidebar__edit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.favorites-sidebar__edit-icon {
  position: relative;
  width: 12px;
  height: 12px;
  transform: rotate(-45deg);
}

.favorites-sidebar__edit-icon::before,
.favorites-sidebar__edit-icon::after {
  content: "";
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  border-radius: 999px;
  background: currentColor;
}

.favorites-sidebar__edit-icon::before {
  top: 1px;
  width: 4px;
  height: 8px;
  border: 1.4px solid currentColor;
  background: transparent;
  box-sizing: border-box;
}

.favorites-sidebar__edit-icon::after {
  bottom: 0;
  width: 5px;
  height: 1.4px;
}

.favorites-sidebar__add::before,
.favorites-sidebar__add::after {
  content: "";
  position: absolute;
  left: 50%;
  top: 50%;
  background: currentColor;
  border-radius: 999px;
  transform: translate(-50%, -50%);
}

.favorites-sidebar__add::before {
  width: 10px;
  height: 1.25px;
}

.favorites-sidebar__add::after {
  width: 1.25px;
  height: 10px;
}

.favorites-sidebar__add:hover,
.favorites-sidebar__add.active,
.favorites-sidebar__edit:hover,
.favorites-sidebar__edit.active {
  color: var(--brand);
}

.favorites-sidebar__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  padding: 0 16px;
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
}

.favorites-sidebar__row {
  background: var(--surface);
}

.favorites-sidebar__table td.favorites-sidebar__cell {
  height: 54px;
  padding: 8px 12px 8px 16px;
  border-left: 2px solid transparent;
  background: transparent;
  transition: all 0.2s ease;
}

.favorites-sidebar__row:not(.editing):hover .favorites-sidebar__cell,
.favorites-sidebar__row.active .favorites-sidebar__cell {
  background: var(--surface-hover);
}

.favorites-sidebar__row:not(.editing):hover .favorites-sidebar__cell {
  border-left-color: var(--brand);
}

.favorites-sidebar__row.active .favorites-sidebar__cell {
  border-left-color: var(--brand);
}

.favorites-sidebar__row.drop-target .favorites-sidebar__cell {
  background:
    linear-gradient(90deg, color-mix(in srgb, var(--brand) 12%, var(--surface)) 0%, transparent 100%),
    var(--info-soft);
  border-left-color: var(--brand);
  box-shadow:
    inset 0 0 0 1px color-mix(in srgb, var(--brand) 40%, transparent),
    0 10px 24px color-mix(in srgb, var(--brand) 14%, transparent);
  transform: translateY(-1px);
}

.favorites-sidebar__row.drop-target .favorites-sidebar__main {
  position: relative;
}

.favorites-sidebar__row.drop-target .favorites-sidebar__main::after {
  content: "";
  position: absolute;
  inset: -4px -6px;
  border-radius: 12px;
  border: 1px dashed color-mix(in srgb, var(--brand) 44%, transparent);
  pointer-events: none;
}

.favorites-sidebar__filler td {
  height: 100%;
  padding: 0;
  border-bottom: none;
  background: var(--surface);
}

.favorites-sidebar__empty-row td {
  height: 100%;
  padding: 0;
  border-bottom: none;
  background: var(--surface);
}

.favorites-sidebar__main {
  display: flex;
  appearance: none;
  width: 100%;
  height: 100%;
  padding: 0;
  border: none;
  background: transparent;
  text-align: left;
  color: inherit;
  min-width: 0;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
}

.favorites-sidebar__main:disabled {
  cursor: default;
}

.favorites-sidebar__label {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  line-height: 1.2;
}

.favorites-sidebar__path {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.2;
}

.favorites-sidebar__actions-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 20px;
  padding-top: 1px;
}

.favorites-sidebar__action {
  appearance: none;
  width: 20px;
  height: 20px;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid color-mix(in srgb, var(--border) 90%, var(--surface));
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--surface) 96%, transparent);
  outline: none;
  color: var(--text-regular);
  flex: 0 0 auto;
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--surface) 72%, transparent);
  transition: all 0.2s ease;
}

.favorites-sidebar__action:hover:not(:disabled) {
  color: var(--brand);
  border-color: color-mix(in srgb, var(--brand) 34%, var(--border));
  background: var(--surface);
}

.favorites-sidebar__action:disabled {
  cursor: not-allowed;
  opacity: 0.38;
}

.favorites-sidebar__action--remove::before,
.favorites-sidebar__action--remove::after {
  content: "";
  position: absolute;
  left: 50%;
  top: 50%;
  width: 9px;
  height: 1.2px;
  border-radius: 999px;
  background: currentColor;
  transform-origin: center;
}

.favorites-sidebar__action--remove::before {
  transform: translate(-50%, -50%) rotate(45deg);
}

.favorites-sidebar__action--remove::after {
  transform: translate(-50%, -50%) rotate(-45deg);
}

.favorites-sidebar__action-icon {
  color: currentColor;
  display: block;
  line-height: 0;
}

.favorites-sidebar__action-icon--up {
  transform: rotate(180deg);
}

.favorites-sidebar__rename-icon {
  position: relative;
  display: block;
  width: 10px;
  height: 10px;
  transform: rotate(-45deg);
}

.favorites-sidebar__rename-icon::before,
.favorites-sidebar__rename-icon::after {
  content: "";
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  border-radius: 999px;
  background: currentColor;
}

.favorites-sidebar__rename-icon::before {
  top: 1px;
  width: 4px;
  height: 7px;
  border: 1.2px solid currentColor;
  background: transparent;
  box-sizing: border-box;
}

.favorites-sidebar__rename-icon::after {
  bottom: 0;
  width: 4px;
  height: 1.2px;
}

:root[data-theme="dark"] .favorites-sidebar__row.active .favorites-sidebar__cell,
:root[data-theme="dark"] .favorites-sidebar__row:not(.editing):hover .favorites-sidebar__cell {
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
}

:root[data-theme="dark"] .favorites-sidebar__action {
  border-color: color-mix(in srgb, var(--border) 88%, var(--surface-muted));
  background: color-mix(in srgb, var(--surface) 92%, var(--surface-muted));
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.04);
}

:root[data-theme="dark"] .favorites-sidebar__action:hover:not(:disabled) {
  background: color-mix(in srgb, var(--surface) 86%, var(--brand));
}

:root[data-skin="brutal"] .favorites-sidebar {
  border-right: var(--brutal-bw) solid var(--brutal-ink);
}

:root[data-skin="brutal"] .favorites-sidebar__head {
  position: static;
}

:root[data-skin="brutal"] .favorites-sidebar__table th {
  border-bottom: var(--brutal-bw) solid var(--brutal-ink);
}

:root[data-skin="brutal"] .favorites-sidebar__row:not(.editing):hover .favorites-sidebar__cell,
:root[data-skin="brutal"] .favorites-sidebar__row.active .favorites-sidebar__cell {
  background: #fff;
}

:root[data-skin="brutal"] .favorites-sidebar__row:not(.editing):hover .favorites-sidebar__cell {
  border-left-color: var(--brutal-ink);
}

:root[data-skin="brutal"] .favorites-sidebar__action {
  border: var(--brutal-bw) solid var(--brutal-ink);
  background: #fff;
  box-shadow: none;
}

:root[data-skin="brutal"] .favorites-sidebar__action:hover:not(:disabled) {
  background: var(--brutal-yellow);
  border-color: var(--brutal-ink);
  color: var(--brutal-ink);
}

@media (max-width: 1100px) {
  .favorites-sidebar {
    border-right: none;
    border-bottom: 1px solid var(--border-soft);
  }

  :root[data-skin="brutal"] .favorites-sidebar {
    border-bottom: var(--brutal-bw) solid var(--brutal-ink);
  }
}
</style>
