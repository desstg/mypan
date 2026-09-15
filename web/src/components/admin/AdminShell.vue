<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AdminAccountChip from "@/components/admin/AdminAccountChip.vue";
import AdminGlobalActions from "@/components/admin/AdminGlobalActions.vue";
import AdminNavIcon from "@/components/admin/AdminNavIcon.vue";
import { useAdminLoadingBar } from "@/composables/useAdminLoadingBar";

interface NavItem {
  key: string;
  label: string;
  icon: string;
}

const SIDEBAR_COLLAPSED_KEY = "litepan-admin-sidebar-collapsed";
const MOBILE_BREAKPOINT = 768;

withDefaults(
  defineProps<{
    nav: NavItem[];
    modelValue: string;
    pageTitle?: string;
    crumbs?: Array<{ label: string; to?: { page: string; tab?: string } }>;
    lockedKeys?: string[];
    homeReturnMode?: "sidebar" | "top_icon";
  }>(),
  { homeReturnMode: "top_icon" },
);
const emit = defineEmits<{
  "update:modelValue": [string];
  preload: [string];
  logout: [];
  goHome: [];
  navigate: [{ page: string; tab?: string }];
}>();

const sidebarCollapsed = ref(false);
const mobileDrawerOpen = ref(false);
const isMobile = ref(false);
const { visible: pageLoadingVisible } = useAdminLoadingBar();

const sidebarCompact = computed(() => !isMobile.value && sidebarCollapsed.value);

const sidebarToggleLabel = computed(() => {
  if (isMobile.value) return mobileDrawerOpen.value ? "关闭菜单" : "打开菜单";
  return sidebarCollapsed.value ? "展开侧栏" : "收起侧栏";
});

function readCollapsedPref() {
  try {
    sidebarCollapsed.value = localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "1";
  } catch {
    sidebarCollapsed.value = false;
  }
}

function persistCollapsedPref() {
  try {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, sidebarCollapsed.value ? "1" : "0");
  } catch {}
}

function syncViewport() {
  isMobile.value = window.innerWidth <= MOBILE_BREAKPOINT;
  if (!isMobile.value) mobileDrawerOpen.value = false;
}

function syncSidebarWidthVar() {
  const width = isMobile.value ? "0px" : sidebarCollapsed.value ? "64px" : "220px";
  document.documentElement.style.setProperty("--sidebar-width", width);
}

function toggleSidebar() {
  if (isMobile.value) {
    mobileDrawerOpen.value = !mobileDrawerOpen.value;
    return;
  }
  sidebarCollapsed.value = !sidebarCollapsed.value;
  persistCollapsedPref();
}

function closeMobileDrawer() {
  mobileDrawerOpen.value = false;
}

function selectNav(key: string) {
  emit("update:modelValue", key);
  closeMobileDrawer();
}

function goHomeFromSidebar() {
  emit("goHome");
  closeMobileDrawer();
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Escape" && mobileDrawerOpen.value) closeMobileDrawer();
}

onMounted(() => {
  readCollapsedPref();
  syncViewport();
  syncSidebarWidthVar();
  window.addEventListener("resize", syncViewport);
  window.addEventListener("keydown", onKeydown);
});

watch([sidebarCollapsed, isMobile], () => {
  syncSidebarWidthVar();
});

watch(mobileDrawerOpen, (open) => {
  if (typeof document === "undefined") return;
  document.body.style.overflow = open && isMobile.value ? "hidden" : "";
});

onBeforeUnmount(() => {
  window.removeEventListener("resize", syncViewport);
  window.removeEventListener("keydown", onKeydown);
  document.body.style.overflow = "";
  document.documentElement.style.removeProperty("--sidebar-width");
});
</script>

<template>
  <div
    class="admin"
    :class="{
      'admin--collapsed': sidebarCompact,
      'admin--drawer-open': isMobile && mobileDrawerOpen,
      'admin--mobile': isMobile,
    }"
  >
    <div
      v-if="isMobile"
      class="sidebar-backdrop"
      :class="{ 'sidebar-backdrop--visible': mobileDrawerOpen }"
      aria-hidden="true"
      @click="closeMobileDrawer"
    />

    <aside class="sidebar">
      <header class="sidebar__header">
        <!-- 点击 logo 展开/收缩侧栏（桌面端）；hover 显示自定义 tooltip 提示 -->
        <span
          class="sidebar__logo-wrap"
          :class="{ 'sidebar__logo-wrap--clickable': !isMobile }"
          @click="!isMobile && toggleSidebar()"
        >
          <img
            :src="sidebarCompact ? '/static/img/logo-l.png' : '/static/img/logo.png'"
            alt="MyPan"
            class="sidebar__logo"
          />
          <span v-if="!isMobile" class="sidebar-logo-tip" role="tooltip">
            {{ sidebarCollapsed ? "展开侧栏" : "收缩侧栏" }}
          </span>
        </span>
      </header>

      <nav class="sidebar__nav">
        <button
          v-for="item in nav"
          :key="item.key"
          class="nav-item"
          :class="{
            'nav-item--active': item.key === modelValue,
            'nav-item--locked': lockedKeys?.includes(item.key),
          }"
          :disabled="lockedKeys?.includes(item.key)"
          @pointerenter="emit('preload', item.key)"
          @focus="emit('preload', item.key)"
          @click="selectNav(item.key)"
        >
          <AdminNavIcon :name="item.icon" class="nav-item__icon" />
          <span class="nav-item__label">{{ item.label }}</span>
        </button>
        <button
          v-if="homeReturnMode === 'sidebar'"
          type="button"
          class="nav-item nav-item--home"
          @click="goHomeFromSidebar"
        >
          <AdminNavIcon name="home" class="nav-item__icon" />
          <span class="nav-item__label">返回首页</span>
        </button>
      </nav>

      <footer class="sidebar__footer">
        <AdminAccountChip :compact="sidebarCompact" @logout="emit('logout')" />
      </footer>
    </aside>

    <header class="global-chrome">
      <!-- 移动端：汉堡按钮打开抽屉（桌面端收缩入口移到侧栏边缘按钮） -->
      <button
        v-if="isMobile"
        type="button"
        class="sidebar-toggle"
        :class="{ 'sidebar-toggle--active': mobileDrawerOpen }"
        :aria-label="sidebarToggleLabel"
        :aria-expanded="mobileDrawerOpen"
        @click="toggleSidebar"
      >
        <svg v-if="mobileDrawerOpen" viewBox="0 0 24 24" aria-hidden="true">
          <path d="m6 6 12 12M18 6 6 18" />
        </svg>
        <svg v-else viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4 7h16M4 12h16M4 17h16" />
        </svg>
      </button>

      <!-- 真面包屑：后台 / 页面 / 当前 tab（可点击项跳转，当前项高亮） -->
      <div v-if="crumbs?.length" class="global-chrome__context global-chrome__context--crumbs">
        <template v-for="(crumb, i) in crumbs" :key="`${crumb.label}-${i}`">
          <button
            v-if="crumb.to"
            type="button"
            class="global-chrome__crumb-link"
            @click="emit('navigate', crumb.to)"
          >
            {{ crumb.label }}
          </button>
          <span v-else class="global-chrome__crumb-current">{{ crumb.label }}</span>
          <span v-if="i < crumbs.length - 1" class="global-chrome__sep">/</span>
        </template>
      </div>
      <div v-else-if="pageTitle" class="global-chrome__context">
        <span class="global-chrome__crumb">后台</span>
        <span class="global-chrome__sep">/</span>
        <span class="global-chrome__title">{{ pageTitle }}</span>
      </div>
      <div class="global-chrome__spacer" />
      <AdminGlobalActions
        :show-home-return="homeReturnMode === 'top_icon'"
        @go-home="emit('goHome')"
      />
      <Transition name="admin-loading-bar">
        <div v-if="pageLoadingVisible" class="global-loading-bar" aria-hidden="true">
          <span />
        </div>
      </Transition>
    </header>

    <main class="admin__body">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.admin {
  --admin-chrome-h: 44px;
  --sidebar-width: 220px;
  display: grid;
  grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
  grid-template-rows: var(--admin-chrome-h) minmax(0, 1fr);
  height: 100vh;
  overflow: hidden;
  background: var(--bg);
}

.admin--collapsed {
  --sidebar-width: 64px;
}

.admin--mobile {
  --sidebar-width: 0px;
  grid-template-columns: minmax(0, 1fr);
}

.sidebar-backdrop {
  display: none;
}

.sidebar {
  grid-column: 1;
  grid-row: 1 / -1;
  z-index: 120;
  position: relative;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--admin-sidebar-bg);
  border-right: 1px solid var(--admin-sidebar-border);
  box-shadow: var(--admin-sidebar-shadow);
  color: #fff;
  border-top-right-radius: var(--radius-lg);
  transition: transform 0.28s ease, box-shadow 0.28s ease;
}

/* 点击 logo 展开/收缩侧栏（桌面端）；hover 显示自定义 tooltip */
.sidebar__header {
  position: relative;
}

.sidebar__logo-wrap {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  width: 100%;
}

.sidebar__logo-wrap--clickable {
  cursor: pointer;
}

/* 自定义 tooltip：黑底白字，浮在 logo 下方、分割线上方（header 内），深浅色主题通用 */
.sidebar-logo-tip {
  position: absolute;
  top: calc(50% + 28px);
  left: 50%;
  transform: translateX(-50%);
  z-index: 220;
  padding: 3px 9px;
  border-radius: 5px;
  background: #18181b;
  color: #fff;
  font-size: 11px;
  line-height: 1.35;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.3);
}

.sidebar__logo-wrap:hover .sidebar-logo-tip,
.sidebar-logo-tip:focus-visible {
  opacity: 1;
}

.sidebar__header {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 98px;
  padding: 0;
  border-bottom: 1px solid rgba(255, 255, 255, 0.15);
}

.sidebar__logo {
  max-width: 128px;
  max-height: 52px;
  width: auto;
  height: auto;
  object-fit: contain;
  object-position: center;
  transition: max-width 0.2s ease, max-height 0.2s ease;
}

.sidebar__nav {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 16px 16px;
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: rgba(255, 255, 255, 0.35) transparent;
}

.sidebar__nav::-webkit-scrollbar {
  width: 6px;
}

.sidebar__nav::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.3);
  border-radius: 3px;
}

.sidebar__nav::-webkit-scrollbar-track {
  background: transparent;
}

.sidebar__footer {
  flex-shrink: 0;
  padding: 16px;
  border-top: 1px solid var(--admin-footer-border);
}

.nav-item {
  display: flex;
  align-items: center;
  height: 50px;
  padding: 0 20px;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  color: rgba(255, 255, 255, 0.85);
  border-radius: 12px;
  font-size: 14px;
  font-weight: 500;
  transition: all 0.2s ease;
  cursor: pointer;
}

.nav-item:hover:not(.nav-item--active):not(:disabled) {
  background: var(--admin-nav-hover-bg);
  color: #fff;
}

.nav-item--active,
.nav-item--active:hover {
  background: var(--admin-nav-active-bg);
  color: var(--admin-nav-active-color);
  font-weight: 600;
  box-shadow: var(--admin-nav-active-shadow);
}

.nav-item--home {
  text-decoration: none;
  margin-top: 4px;
}

.nav-item--locked,
.nav-item:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.nav-item:disabled:hover {
  background: transparent;
  color: rgba(255, 255, 255, 0.85);
}

.nav-item__icon {
  margin-right: 24px;
  flex-shrink: 0;
}

.nav-item__label {
  min-width: 0;
}

.admin--collapsed .sidebar__header {
  height: 98px;
  padding: 0;
}

.admin--collapsed .sidebar__logo {
  max-width: 28px;
  max-height: 34px;
}

.admin--collapsed .sidebar__nav {
  padding: 10px 8px 12px;
}

.admin--collapsed .sidebar__footer {
  padding: 8px;
}

.admin--collapsed .nav-item {
  justify-content: center;
  padding: 0;
  height: 50px;
}

.admin--collapsed .nav-item__icon {
  margin-right: 0;
}

.admin--collapsed .nav-item__label {
  display: none;
}

.sidebar-toggle {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: var(--transition);
}

.sidebar-toggle:hover,
.sidebar-toggle--active {
  color: var(--brand);
  background: var(--surface-sunken);
}

.sidebar-toggle svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.global-chrome {
  grid-column: 1 / -1;
  grid-row: 1;
  position: relative;
  /* 顶栏（含铃铛下拉面板）必须高于内容区里 position+z-index 的元素（如账号卡片的菜单/色条 z-index:2），
     否则通知面板会被内容覆盖；sidebar(z:120) 与移动端遮罩(z:110) 仍在其上。 */
  z-index: 50;
  height: var(--admin-chrome-h);
  display: flex;
  align-items: center;
  gap: 10px;
  /* 顶栏左侧内边距对齐正文（24px），面包屑不顶着侧栏 */
  padding-left: calc(var(--sidebar-width) + 24px);
  padding-right: 22px;
  background: var(--surface);
  border-bottom: 1px solid var(--border-soft);
  box-shadow: var(--shadow-card);
}

.global-chrome__context {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 13px;
}

/* 真面包屑：可点击项 / 当前项 */
.global-chrome__crumb-link {
  border: none;
  background: transparent;
  padding: 2px 4px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    color 0.15s ease,
    background 0.15s ease;
}

.global-chrome__crumb-link:hover {
  color: var(--brand);
  background: var(--surface-sunken);
}

.global-chrome__crumb-current {
  color: var(--text);
  font-weight: 700;
  white-space: nowrap;
}

.global-chrome__crumb {
  flex-shrink: 0;
  color: var(--text-muted);
}

.global-chrome__sep {
  flex-shrink: 0;
  color: var(--border);
}

.global-chrome__title {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--text);
  font-weight: 700;
}

.global-chrome__spacer {
  flex: 1;
  min-width: 12px;
}

.global-loading-bar {
  position: absolute;
  left: var(--sidebar-width);
  right: 0;
  bottom: -1px;
  height: 2px;
  overflow: hidden;
  pointer-events: none;
}

.global-loading-bar span {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    90deg,
    transparent 0%,
    var(--brand-start) 45%,
    var(--brand-end) 55%,
    transparent 100%
  );
  transform: translateX(-100%);
  animation: admin-loading-slide 0.9s ease-in-out infinite;
}

.admin-loading-bar-enter-active,
.admin-loading-bar-leave-active {
  transition: opacity 0.16s ease;
}

.admin-loading-bar-enter-from,
.admin-loading-bar-leave-to {
  opacity: 0;
}

@keyframes admin-loading-slide {
  to {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .global-loading-bar span {
    animation: none;
    transform: none;
    background: var(--brand);
  }
}

.admin__body {
  grid-column: 2;
  grid-row: 2;
  min-height: 0;
  padding: 24px;
  overflow-x: clip;
  overflow-y: auto;
  background: var(--bg);
}

@media (max-width: 768px) {
  .sidebar-backdrop {
    display: block;
    position: fixed;
    inset: 0;
    z-index: 110;
    background: rgba(15, 23, 42, 0.35);
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.22s ease;
  }

  .sidebar-backdrop--visible {
    opacity: 1;
    pointer-events: auto;
  }

  .sidebar {
    position: fixed;
    top: 0;
    left: 0;
    width: min(260px, 82vw);
    height: 100vh;
    transform: translateX(-100%);
    border-top-right-radius: 0;
  }

  .admin--drawer-open .sidebar {
    transform: translateX(0);
    box-shadow: 2px 0 16px rgba(15, 23, 42, 0.18);
  }

  .global-chrome {
    --admin-chrome-h: 42px;
    grid-column: 1;
    padding-left: 14px;
    padding-right: 14px;
  }

  .global-chrome__context {
    display: none;
  }

  .global-loading-bar {
    left: 0;
  }

  .admin__body {
    grid-column: 1;
    padding: 16px 14px 20px;
  }
}
</style>
