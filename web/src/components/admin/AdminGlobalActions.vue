<script setup lang="ts">
import { computed, ref } from "vue";
import AdminNotificationBell from "@/components/admin/AdminNotificationBell.vue";
import {
  getNextThemePref,
  getThemePref,
  getThemeToggleTitle,
  setThemePref,
  supportsThemeToggle,
  type ThemePref,
} from "@/utils/theme";

withDefaults(
  defineProps<{
    showHomeReturn?: boolean;
  }>(),
  { showHomeReturn: true },
);

const emit = defineEmits<{ goHome: [] }>();

const theme = ref<ThemePref>(getThemePref());
const themeToggleTitle = computed(() => getThemeToggleTitle(theme.value));
const showThemeToggle = computed(() => supportsThemeToggle());

function toggleTheme() {
  theme.value = getNextThemePref(theme.value);
  setThemePref(theme.value);
}
</script>

<template>
  <div class="global-actions" aria-label="全局操作区">
    <button
      v-if="showThemeToggle"
      class="icon-btn"
      type="button"
      :title="themeToggleTitle"
      :aria-label="themeToggleTitle"
      @click="toggleTheme"
    >
      <svg v-if="theme === 'light'" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2" />
        <path d="M12 20v2" />
        <path d="m4.93 4.93 1.41 1.41" />
        <path d="m17.66 17.66 1.41 1.41" />
        <path d="M2 12h2" />
        <path d="M20 12h2" />
        <path d="m6.34 17.66-1.41 1.41" />
        <path d="m19.07 4.93-1.41 1.41" />
      </svg>
      <svg v-else-if="theme === 'dark'" viewBox="0 0 24 24" aria-hidden="true">
        <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
      </svg>
      <svg v-else viewBox="0 0 24 24" aria-hidden="true">
        <rect x="2" y="3" width="20" height="14" rx="2" />
        <path d="M8 21h8" />
        <path d="M12 17v4" />
      </svg>
    </button>
    <AdminNotificationBell variant="main" />
    <button
      v-if="showHomeReturn"
      type="button"
      class="icon-btn"
      title="返回前台"
      aria-label="返回前台"
      @click="emit('goHome')"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M3 10.5 12 3l9 7.5" />
        <path d="M5 10v10h14V10" />
        <path d="M9 20v-6h6v6" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.global-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.global-actions :deep(.icon-btn) {
  position: relative;
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  background: transparent;
  cursor: pointer;
  text-decoration: none;
  transition: var(--transition);
}

.global-actions :deep(.icon-btn:hover),
.global-actions :deep(.icon-btn.active) {
  color: var(--brand);
  background: var(--surface-sunken);
}

.global-actions :deep(.icon-btn svg) {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.global-actions :deep(.icon-btn .badge) {
  position: absolute;
  top: 3px;
  right: 3px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border: 2px solid var(--surface);
  border-radius: 999px;
  color: #fff;
  background: var(--danger);
  font-size: 10px;
  font-weight: 800;
  line-height: 12px;
  text-align: center;
}
</style>
