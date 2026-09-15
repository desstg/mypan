<script setup lang="ts">
import AppButton from "@/components/base/AppButton.vue";

defineProps<{
  open: boolean;
  title: string;
  saving?: boolean;
  canSave?: boolean;
}>();

const emit = defineEmits<{
  close: [];
  cancel: [];
  save: [];
}>();
</script>

<template>
  <Teleport to="body">
    <div class="admin-settings-drawer" :class="{ 'admin-settings-drawer--open': open }">
      <div
        class="admin-settings-drawer__backdrop"
        aria-hidden="true"
        @click="emit('close')"
      />
      <aside
        class="admin-settings-drawer__panel"
        role="dialog"
        :aria-hidden="!open"
        :aria-label="title"
      >
        <div class="admin-settings-drawer__scroll">
          <div class="admin-settings-drawer__head">
            <button type="button" class="admin-settings-drawer__close" aria-label="返回" title="返回" @click="emit('close')">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M5 12h14m-6-6 6 6-6 6" />
              </svg>
            </button>
          </div>
          <div class="admin-settings-drawer__body">
            <slot />
          </div>
          <div class="admin-settings-drawer__foot">
            <AppButton type="button" variant="secondary" @click="emit('cancel')">取消</AppButton>
            <AppButton type="button" variant="primary" :disabled="!canSave || saving" @click="emit('save')">
              {{ saving ? "保存中…" : "保存设置" }}
            </AppButton>
          </div>
        </div>
      </aside>
    </div>
  </Teleport>
</template>

<style scoped>
.admin-settings-drawer {
  position: fixed;
  top: var(--admin-chrome-h, 44px);
  right: 0;
  bottom: 0;
  left: var(--sidebar-width, 220px);
  pointer-events: none;
  z-index: 120;
}

.admin-settings-drawer--open {
  pointer-events: auto;
}

.admin-settings-drawer__backdrop {
  position: absolute;
  inset: 0;
  background: rgba(15, 23, 42, 0.35);
  opacity: 0;
  transition: opacity 0.22s ease;
}

.admin-settings-drawer--open .admin-settings-drawer__backdrop {
  opacity: 1;
}

.admin-settings-drawer__panel {
  position: absolute;
  inset: 0;
  background: var(--surface);
  box-shadow: var(--shadow-pop, 0 12px 40px rgba(15, 23, 42, 0.12));
  transform: translateX(100%);
  transition: transform 0.24s ease;
  overflow: hidden;
}

.admin-settings-drawer--open .admin-settings-drawer__panel {
  transform: translateX(0);
}

.admin-settings-drawer__scroll {
  height: 100%;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.admin-settings-drawer__head {
  position: sticky;
  z-index: 2;
  top: 0;
  display: flex;
  justify-content: flex-start;
  padding: 10px 12px 4px;
  background: var(--surface);
}

.admin-settings-drawer__head::before {
  position: absolute;
  z-index: 0;
  top: 14px;
  left: 16px;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: #4c74df;
  content: "";
  opacity: 0;
  pointer-events: none;
  transform: scale(0.7);
}

.admin-settings-drawer__close {
  position: relative;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: #ffffff;
  cursor: pointer;
}

.admin-settings-drawer__close::before {
  position: absolute;
  z-index: 0;
  inset: 5px;
  border-radius: 50%;
  background: #4c74df;
  content: "";
}

.admin-settings-drawer__close svg {
  position: relative;
  z-index: 1;
  width: 14px;
  height: 14px;
}

.admin-settings-drawer__close:hover {
  color: #ffffff;
  background: transparent;
}

.admin-settings-drawer__close:hover::before {
  background: #4c74df;
}

.admin-settings-drawer__close:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--brand) 58%, transparent);
  outline-offset: 2px;
}

.admin-settings-drawer--open .admin-settings-drawer__head::before {
  animation: admin-settings-drawer-back-halo 1s ease-out infinite;
}

@keyframes admin-settings-drawer-back-halo {
  0% {
    opacity: 0.78;
    transform: scale(0.7);
  }
  70% {
    opacity: 0.28;
    transform: scale(1.55);
  }
  100% {
    opacity: 0;
    transform: scale(1.55);
  }
}

.admin-settings-drawer__body {
  padding: 4px 16px 12px;
}

.admin-settings-drawer__foot {
  display: flex;
  justify-content: center;
  gap: 8px;
  padding: 8px 16px 20px;
}

@media (max-width: 768px) {
  .admin-settings-drawer {
    top: var(--admin-chrome-h, 42px);
    left: 0;
  }
}
</style>
