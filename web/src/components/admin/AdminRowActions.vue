<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";

withDefaults(
  defineProps<{
    label?: string;
  }>(),
  {
    label: "更多操作",
  },
);

const open = ref(false);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});

function updateMenuPosition() {
  const trigger = triggerRef.value;
  if (!trigger) return;
  const rect = trigger.getBoundingClientRect();
  const menuHeight = menuRef.value?.offsetHeight ?? 0;
  const menuWidth = 156;
  const margin = 12;
  const left = Math.min(
    Math.max(margin, rect.right - menuWidth),
    window.innerWidth - menuWidth - margin,
  );
  const top = Math.max(margin, rect.top - menuHeight - 8);
  menuStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    minWidth: `${menuWidth}px`,
  };
}

async function toggleMenu(event: MouseEvent) {
  event.stopPropagation();
  open.value = !open.value;
  if (open.value) {
    await nextTick();
    updateMenuPosition();
  }
}

function closeMenu() {
  open.value = false;
}

function handleDocumentClick(event: MouseEvent) {
  if (!open.value) return;
  const target = event.target as Node;
  if (triggerRef.value?.contains(target) || menuRef.value?.contains(target)) return;
  closeMenu();
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") closeMenu();
}

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  window.addEventListener("resize", updateMenuPosition);
  window.addEventListener("scroll", updateMenuPosition, true);
  document.addEventListener("keydown", handleKeydown);
});

onBeforeUnmount(() => {
  document.removeEventListener("click", handleDocumentClick);
  window.removeEventListener("resize", updateMenuPosition);
  window.removeEventListener("scroll", updateMenuPosition, true);
  document.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <div class="admin-row-actions">
    <div class="admin-row-actions__desktop">
      <slot />
    </div>
    <div class="admin-row-actions__mobile">
      <button
        ref="triggerRef"
        type="button"
        class="admin-row-actions__trigger"
        :aria-label="label"
        :aria-expanded="open"
        @click="toggleMenu"
      >
        <span></span>
        <span></span>
        <span></span>
      </button>
      <Teleport to="body">
        <div
          v-if="open"
          ref="menuRef"
          class="admin-row-actions__menu"
          :style="menuStyle"
          @click="closeMenu"
        >
          <slot name="menu" />
        </div>
      </Teleport>
    </div>
  </div>
</template>

<style scoped>
.admin-row-actions {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.admin-row-actions__desktop {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

.admin-row-actions__mobile {
  display: none;
}

.admin-row-actions__trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  gap: 3px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}

.admin-row-actions__trigger span {
  width: 4px;
  height: 4px;
  border-radius: 999px;
  background: currentColor;
}

.admin-row-actions__trigger:hover,
.admin-row-actions__trigger[aria-expanded="true"] {
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border-soft));
  color: var(--brand);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}

.admin-row-actions__menu {
  position: fixed;
  z-index: 2200;
  display: grid;
  gap: 4px;
  padding: 8px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface);
  box-shadow: var(--shadow-popover, 0 14px 34px rgb(15 23 42 / 16%));
}

:slotted(.admin-row-actions__item) {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  width: 100%;
  min-height: 34px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-main);
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  text-align: left;
  cursor: pointer;
}

:slotted(.admin-row-actions__item:hover) {
  background: color-mix(in srgb, var(--brand) 8%, transparent);
  color: var(--brand);
}

:slotted(.admin-row-actions__item--danger) {
  color: var(--danger);
}

:slotted(.admin-row-actions__item:disabled) {
  opacity: 0.45;
  cursor: not-allowed;
}

:slotted(.admin-row-actions__item:disabled:hover) {
  background: transparent;
  color: var(--text-main);
}

@media (max-width: 720px) {
  .admin-row-actions__desktop {
    display: none;
  }

  .admin-row-actions__mobile {
    display: inline-flex;
  }
}

:global([data-skin="brutal"]) .admin-row-actions__trigger,
:global([data-skin="brutal"]) .admin-row-actions__menu {
  border: var(--brutal-border-width, 2px) solid var(--text-main);
  border-radius: 0;
  box-shadow: var(--brutal-shadow, 3px 3px 0 var(--text-main));
}

:global([data-skin="brutal"]) .admin-row-actions__trigger span {
  border-radius: 0;
}

:global([data-skin="brutal"]) :slotted(.admin-row-actions__item) {
  border-radius: 0;
}
</style>
