<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, onUnmounted, ref, watch } from "vue";
import { useAuthStore } from "@/stores/auth";
import { APP_NAME, APP_VERSION } from "@/version";
import { useDeveloperUnlock } from "@/composables/useDeveloperUnlock";
import { useAnnouncement } from "@/composables/useAnnouncement";
import { toast } from "@/composables/useToast";
import AppModal from "@/components/base/AppModal.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppButton from "@/components/base/AppButton.vue";

const props = defineProps<{
  compact?: boolean;
}>();

const emit = defineEmits<{ logout: [] }>();

const auth = useAuthStore();
const { unlocked: devUnlocked, init: devUnlockInit, unlock } = useDeveloperUnlock();
const announcement = useAnnouncement();
const open = ref(false);
const wrapRef = ref<HTMLElement | null>(null);
const menuPos = ref({ left: 0, bottom: 0 });
const unlockOpen = ref(false);
const unlockCode = ref("");
const aboutClicks = ref(0);
let aboutClickTimer: ReturnType<typeof setTimeout> | null = null;

const displayName = computed(() => auth.username || "admin");
const avatarLetter = computed(() => displayName.value.charAt(0).toUpperCase() || "A");

const compactMenuStyle = computed(() =>
  props.compact
    ? {
        left: `${menuPos.value.left}px`,
        bottom: `${menuPos.value.bottom}px`,
      }
    : undefined,
);

function updateMenuPos() {
  const el = wrapRef.value;
  if (!el || !props.compact) return;
  const rect = el.getBoundingClientRect();
  menuPos.value = {
    left: rect.right + 8,
    bottom: window.innerHeight - rect.bottom,
  };
}

function toggleMenu() {
  open.value = !open.value;
  if (open.value && props.compact) {
    void nextTick(updateMenuPos);
  }
}

function closeMenu() {
  open.value = false;
}

function handleLogout() {
  closeMenu();
  emit("logout");
}

// 点「关于」：关闭菜单并弹出公告（已读过也可再次查看，手动查看不改变已读状态）。
function handleAboutClick() {
  closeMenu();
  void announcement.forceOpen().then((ok) => {
    if (!ok) toast.info("暂无公告");
  });
}

// 点「关于」前面的图标：1.8s 内连点 5 次触发开发者解锁（隐藏入口）。
function handleAboutIconClick() {
  aboutClicks.value += 1;
  if (aboutClickTimer) clearTimeout(aboutClickTimer);
  aboutClickTimer = setTimeout(() => {
    aboutClicks.value = 0;
  }, 1800);
  if (aboutClicks.value < 5) return;
  aboutClicks.value = 0;
  if (devUnlocked.value) {
    toast.info("开发模式已解锁，实验性驱动可见");
    return;
  }
  unlockCode.value = "";
  unlockOpen.value = true;
}

async function confirmUnlock() {
  try {
    const ok = await unlock(unlockCode.value);
    if (!ok) {
      toast.error("解锁码错误");
      return;
    }
    toast.success("开发模式已解锁，实验性驱动可见");
    unlockOpen.value = false;
  } catch {
    toast.error("解锁失败，请稍后重试");
  }
}

function handleDocumentClick(e: MouseEvent) {
  const el = e.target as HTMLElement | null;
  if (!open.value || !el) return;
  if (el.closest(".acct-wrap") || el.closest(".acct-menu")) return;
  closeMenu();
}

function handleViewportChange() {
  if (open.value && props.compact) updateMenuPos();
}

watch(
  () => props.compact,
  () => {
    if (open.value && props.compact) void nextTick(updateMenuPos);
  },
);

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  window.addEventListener("resize", handleViewportChange);
  window.addEventListener("scroll", handleViewportChange, true);
  void devUnlockInit();
});

onUnmounted(() => {
  document.removeEventListener("click", handleDocumentClick);
});

onBeforeUnmount(() => {
  window.removeEventListener("resize", handleViewportChange);
  window.removeEventListener("scroll", handleViewportChange, true);
});
</script>

<template>
  <div ref="wrapRef" class="acct-wrap">
    <button
      class="acct-chip"
      :class="{ 'acct-chip--compact': compact }"
      type="button"
      :aria-expanded="open"
      @click.stop="toggleMenu"
    >
      <span class="acct-chip__avatar">{{ avatarLetter }}</span>
      <span class="acct-chip__name">
        {{ displayName }}
        <small class="acct-chip__sub">管理员</small>
      </span>
      <span class="acct-chip__chev" aria-hidden="true">
        <svg viewBox="0 0 24 24">
          <path d="m6 9 6 6 6-6" />
        </svg>
      </span>
    </button>

    <Teleport to="body" :disabled="!compact">
      <div
        v-if="open"
        class="acct-menu"
        :class="{ 'acct-menu--compact': compact }"
        :style="compactMenuStyle"
        @click.stop
      >
        <div class="acct-menu__section acct-menu__item acct-menu__head">
          <span class="acct-menu__avatar">{{ avatarLetter }}</span>
          <span class="acct-menu__content">
            <span class="acct-menu__name">{{ displayName }}</span>
            <span class="acct-menu__role">管理员账户</span>
          </span>
        </div>

        <div
          class="acct-menu__section acct-menu__item acct-menu__about"
          role="button"
          tabindex="0"
          @click="handleAboutClick"
          @keydown.enter="handleAboutClick"
        >
          <span
            class="acct-menu__icon"
            role="button"
            tabindex="-1"
            aria-label="关于 MyPan"
            title="关于 MyPan"
            @click.stop="handleAboutIconClick"
          >
            <svg viewBox="0 0 24 24">
              <circle cx="12" cy="12" r="9" />
              <path d="M12 10v6" />
              <path d="M12 7h.01" />
            </svg>
          </span>
          <span class="acct-menu__content">
            <span class="acct-menu__label">关于 {{ APP_NAME }}</span>
            <span class="acct-menu__meta">{{ APP_VERSION }}</span>
          </span>
        </div>

        <button class="acct-menu__section acct-menu__item acct-menu__action" type="button" @click="handleLogout">
          <span class="acct-menu__icon" aria-hidden="true">
            <svg viewBox="0 0 24 24">
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <path d="M16 17l5-5-5-5" />
              <path d="M21 12H9" />
            </svg>
          </span>
          <span class="acct-menu__label">退出登录</span>
        </button>
      </div>
    </Teleport>
  </div>

  <AppModal :open="unlockOpen" title="解锁开发模式" size="sm" @close="unlockOpen = false">
    <div class="unlock-body">
      <p class="unlock-tip">解锁后可查看实验性驱动。该功能仅供开发人员内部测试使用。</p>
      <AppInput
        v-model="unlockCode"
        type="password"
        placeholder="请输入解锁码"
        autocomplete="off"
        @keydown.enter="confirmUnlock"
      />
      <div class="unlock-actions">
        <AppButton variant="primary" @click="confirmUnlock">解锁</AppButton>
      </div>
    </div>
  </AppModal>
</template>

<style scoped>
.acct-wrap {
  position: relative;
  flex-shrink: 0;
}

.acct-chip {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 8px 10px;
  border: none;
  background: rgba(255, 255, 255, 0.14);
  border-radius: var(--radius-md);
  color: #fff;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s;
}

.acct-chip:hover {
  background: rgba(255, 255, 255, 0.24);
}

.acct-chip__avatar {
  width: 30px;
  height: 30px;
  flex: none;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.25);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  font-weight: 700;
}

.acct-chip__name {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.acct-chip__sub {
  display: block;
  font-size: 10px;
  font-weight: 400;
  opacity: 0.7;
}

.acct-chip__chev {
  flex: none;
  width: 14px;
  height: 14px;
  opacity: 0.7;
}

.acct-chip__chev svg {
  width: 14px;
  height: 14px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.acct-menu {
  position: absolute;
  left: 0;
  right: 0;
  bottom: calc(100% + 6px);
  width: auto;
  min-width: 0;
  background: color-mix(in srgb, var(--surface) 94%, transparent);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-pop);
  padding: 0;
  overflow: hidden;
  z-index: 130;
}

.acct-menu--compact {
  position: fixed;
  right: auto;
  width: 220px;
  min-width: 220px;
}

.acct-menu__section {
  display: block;
  width: 100%;
  padding: 10px 12px;
}

.acct-menu__section + .acct-menu__section {
  border-top: 1px solid var(--border-soft);
}

.acct-menu__item {
  display: flex;
  align-items: center;
  gap: 10px;
}

.acct-menu__avatar,
.acct-menu__icon {
  width: 34px;
  height: 34px;
  flex: none;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.acct-menu__avatar {
  background: color-mix(in srgb, var(--brand) 14%, var(--surface));
  color: var(--text);
  font-size: 14px;
  font-weight: 700;
}

.acct-menu__icon {
  background: var(--surface-hover);
  color: var(--text-muted);
}

.acct-menu__icon svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.acct-menu__content {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.acct-menu__name {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  line-height: 1.35;
}

.acct-menu__role {
  display: block;
  margin-top: 1px;
  font-size: 11px;
  color: var(--text-muted);
  line-height: 1.35;
}

.acct-menu__label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}

.acct-menu__meta {
  display: block;
  min-width: 0;
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.acct-menu__action {
  border: none;
  background: transparent;
  color: var(--text);
  cursor: pointer;
  text-align: left;
}

.acct-chip--compact {
  justify-content: center;
  padding: 8px 4px;
}

.acct-chip--compact .acct-chip__name,
.acct-chip--compact .acct-chip__chev {
  display: none;
}

.acct-menu__action:hover {
  background: var(--surface-hover);
}

.acct-menu__action:hover .acct-menu__icon,
.acct-menu__action:hover .acct-menu__label {
  color: var(--brand);
}

.acct-menu__about {
  cursor: pointer;
}

.acct-menu__about:hover {
  background: var(--surface-hover);
}

.unlock-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 4px 0;
}

.unlock-tip {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.5;
}

.unlock-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
