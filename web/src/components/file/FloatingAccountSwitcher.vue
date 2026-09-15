<script setup lang="ts">
import type { Account } from "@/api/types";

const props = defineProps<{
  accounts: Account[];
  modelValue: number | null;
}>();
const emit = defineEmits<{ "update:modelValue": [number] }>();

function accountText(account: Account): string {
  const cardName = String(account.driver_card_name || "").trim();
  if (cardName) return cardName;
  const driverType = String(account.driver_type || "").trim();
  return driverType ? driverType.slice(0, 2).toUpperCase() : "盘";
}

function accountColor(account: Account): string {
  return account.driver_card_color || "#6366f1";
}
</script>

<template>
  <div class="floating-switcher" aria-label="账号切换">
    <button
      v-for="account in props.accounts"
      :key="account.id"
      type="button"
      class="floating-switcher__btn"
      :class="{
        'floating-switcher__btn--active': modelValue === account.id,
        'floating-switcher__btn--logo': !!account.driver_card_logo,
      }"
      :style="{ '--driver-color': accountColor(account) }"
      @click="emit('update:modelValue', account.id)"
    >
      <img
        v-if="account.driver_card_logo"
        :src="account.driver_card_logo"
        class="floating-switcher__logo"
        :alt="account.driver_card_name || account.name"
      />
      <span v-else class="floating-switcher__text">{{ accountText(account) }}</span>
      <span class="floating-switcher__tooltip">{{ account.name }}</span>
    </button>
  </div>
</template>

<style scoped>
.floating-switcher {
  position: fixed;
  left: 14px;
  /* 与右侧第一栏（FileToolbar / 导航行）顶部对齐：header 100px + 内容区顶部 20px */
  top: 120px;
  z-index: 80;
  display: flex;
  flex-direction: column;
  gap: 11px;
}
.floating-switcher__btn {
  position: relative;
  width: 38px;
  height: 38px;
  border: 0;
  border-radius: 10px;
  background: #fff;
  color: var(--driver-color, #6366f1);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  box-shadow: 0 6px 16px rgba(15, 23, 42, 0.1);
  transition: all 0.18s ease;
}
.floating-switcher__btn:hover {
  transform: translateX(2px) scale(1.03);
  box-shadow:
    0 0 0 2px color-mix(in srgb, var(--driver-color, #6366f1) 28%, transparent),
    0 10px 22px rgba(15, 23, 42, 0.16);
}
.floating-switcher__btn--active {
  background: var(--driver-color, #6366f1);
  color: #fff;
  transform: scale(1.06);
  box-shadow:
    0 0 0 2px #fff,
    0 0 0 5px color-mix(in srgb, var(--driver-color, #6366f1) 35%, transparent),
    0 10px 24px color-mix(in srgb, var(--driver-color, #6366f1) 36%, transparent);
}
.floating-switcher__text {
  max-width: 30px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 700;
  line-height: 1;
}
.floating-switcher__logo {
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
  border-radius: 10px;
  background: #fff;
}
.floating-switcher__btn--logo {
  overflow: visible;
  background: #fff;
}
.floating-switcher__btn--logo.floating-switcher__btn--active .floating-switcher__logo {
  box-shadow: 0 0 0 2px var(--driver-color, #6366f1);
}
.floating-switcher__tooltip {
  position: absolute;
  left: calc(100% + 10px);
  top: 50%;
  transform: translateY(-50%) translateX(-4px);
  padding: 6px 10px;
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.92);
  color: #fff;
  font-size: 12px;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.floating-switcher__btn:hover .floating-switcher__tooltip {
  opacity: 1;
  transform: translateY(-50%) translateX(0);
}
@media (max-width: 768px) {
  .floating-switcher {
    left: 50%;
    top: auto;
    /* footer 固定于视口底部，横条抬高到其上方，避免盖住 footer 里的性能/任务图标 */
    bottom: calc(88px + 10px);
    transform: translateX(-50%);
    z-index: 90;
    flex-direction: row;
    max-width: calc(100vw - 28px);
    overflow-x: auto;
    gap: 8px;
    padding: 8px;
    border: 1px solid var(--border-soft);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--surface) 92%, transparent);
    box-shadow: var(--shadow-pop);
    backdrop-filter: blur(10px);
  }

  .floating-switcher__btn {
    width: 34px;
    height: 34px;
    border-radius: var(--radius-sm);
    flex: 0 0 auto;
  }

  .floating-switcher__logo {
    border-radius: var(--radius-sm);
  }

  .floating-switcher__tooltip {
    display: none;
  }
}

:global(:root[data-skin="brutal"]) .floating-switcher {
  border-radius: 0;
}

:global(:root[data-skin="brutal"]) .floating-switcher__btn,
:global(:root[data-skin="brutal"]) .floating-switcher__logo {
  border-radius: 0;
}
</style>
