<script setup lang="ts">
import { computed } from "vue";
import type { Account, DriverInfo } from "@/api/types";
import { formatTime } from "@/utils/format";
import DriverIcon from "@/components/driver/DriverIcon.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import type { DropdownMenuItem } from "@/types/menu";

const props = defineProps<{ account: Account; driver?: DriverInfo }>();
const emit = defineEmits<{
  edit: [Account];
  remove: [Account];
  toggle: [Account];
  setDefault: [Account];
  refreshProfile: [Account];
}>();

const driverLabel = computed(() => props.driver?.display_name || props.account.driver_type);
const color = computed(() => props.driver?.card_color || "");
const logo = computed(() => props.driver?.card_logo || "");
const createdAt = computed(() =>
  props.account.created_at ? formatTime(props.account.created_at).replace(/:\d{2}$/, "") : "",
);
const nickname = computed(() => props.account.profile_nickname || "");
const vip = computed(() => props.account.profile_vip || "");
const authStatus = computed(() => (props.account.auth_status || "").trim().toLowerCase());
const hasAuthError = computed(() =>
  props.account.is_active && ["token_expired", "failed"].includes(authStatus.value),
);
const isCooldown = computed(() => props.account.is_active && authStatus.value === "cooldown");
const statusClass = computed(() => {
  if (hasAuthError.value) return "is-auth-error";
  if (isCooldown.value) return "is-cooldown";
  return props.account.is_active ? "is-active" : "is-inactive";
});

const menuItems = computed<DropdownMenuItem[]>(() => {
  const items: DropdownMenuItem[] = [
    { key: "edit", label: "编辑账号", type: "action" },
    {
      key: "toggle",
      label: props.account.is_active ? "禁用账号" : "启用账号",
      type: "action",
    },
  ];
  if (!props.account.is_default) {
    items.push({ key: "setDefault", label: "设为默认", type: "action" });
  } else {
    items.push({ key: "defaultHint", label: "当前默认", type: "hint" });
  }
  items.push({ key: "refreshProfile", label: "刷新信息", type: "action" });
  items.push({ key: "divider", label: "", type: "divider" });
  items.push({ key: "remove", label: "删除账号", type: "action", danger: true });
  return items;
});

function onMenuSelect(key: string) {
  switch (key) {
    case "edit":
      emit("edit", props.account);
      break;
    case "toggle":
      emit("toggle", props.account);
      break;
    case "setDefault":
      emit("setDefault", props.account);
      break;
    case "refreshProfile":
      emit("refreshProfile", props.account);
      break;
    case "remove":
      emit("remove", props.account);
      break;
  }
}
</script>

<template>
  <div class="acc-card" :class="{ 'is-auth-error': hasAuthError, 'is-cooldown': isCooldown }">
    <span class="acc-card__bar" :class="statusClass" />

    <div class="acc-card__menu">
      <AppDropdown
        :items="menuItems"
        trigger="click"
        align="right"
        :min-width="148"
        density="compact"
        @select="onMenuSelect"
      >
        <template #trigger="{ toggle }">
          <button
            type="button"
            class="acc-card__menu-btn"
            aria-label="更多操作"
            @click.stop="toggle"
          >
            ⋯
          </button>
        </template>
      </AppDropdown>
    </div>

    <div class="acc-card__head">
      <DriverIcon :name="driverLabel" :color="color" :logo="logo" :size="48" />
      <div class="acc-card__meta">
        <h4 class="acc-card__name">
          {{ account.name }}
          <span v-if="vip" class="acc-card__vip-tag">{{ vip }}</span>
          <span v-if="account.is_default" class="acc-card__default-tag">默认</span>
          <span v-if="hasAuthError" class="acc-card__status-pill">失效</span>
        </h4>
        <p class="acc-card__time">
          {{ nickname || (createdAt ? `创建于 ${createdAt}` : "") }}
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.acc-card {
  position: relative;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: 20px;
  min-height: 120px;
  display: flex;
  align-items: center;
  transition: var(--transition);
  overflow: hidden;
}
.acc-card:hover {
  box-shadow: var(--shadow-card);
}
.acc-card.is-auth-error {
  border-color: color-mix(in srgb, var(--danger) 48%, var(--border));
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--danger) 14%, transparent);
}
.acc-card.is-cooldown {
  border-color: color-mix(in srgb, var(--warning) 42%, var(--border));
}

.acc-card__bar {
  position: absolute;
  z-index: 2;
  top: 0;
  right: 0;
  bottom: 0;
  width: 4px;
}
.acc-card__bar.is-active {
  background: linear-gradient(180deg, var(--success), #059669);
}
.acc-card__bar.is-cooldown {
  background: linear-gradient(180deg, var(--warning), #d97706);
}
.acc-card__bar.is-auth-error {
  background: linear-gradient(180deg, var(--danger), #dc2626);
}
.acc-card__bar.is-inactive {
  background: linear-gradient(180deg, #9ca3af, #6b7280);
}

.acc-card__menu {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 2;
}
.acc-card__menu-btn {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 18px;
  line-height: 1;
  padding: 6px 8px;
  border-radius: var(--radius-sm);
}
.acc-card__menu-btn:hover {
  background: var(--border-soft);
  color: var(--text);
}

.acc-card__head {
	position: relative;
	z-index: 1;
  display: flex;
  align-items: center;
  gap: 14px;
  padding-right: 28px;
}
.acc-card__name {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 0;
  flex-wrap: wrap;
}
.acc-card__default-tag {
	margin-left: 6px;
  font-size: 11px;
  font-weight: 500;
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: var(--info-soft);
  color: var(--info);
}
.acc-card__status-pill {
	margin-left: 6px;
  font-size: 11px;
  font-weight: 500;
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--danger) 12%, var(--surface));
  color: var(--danger);
}
.acc-card__time {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--text-muted);
}
.acc-card__vip-tag {
  display: inline-flex;
  align-items: center;
  height: 16px;
  margin-left: 6px;
  padding: 0 4px;
  border: 1px solid rgba(237, 207, 131, 0.24);
  border-radius: 3px;
  background: #24211c;
  color: #edcf83;
  font-size: 9px;
  font-weight: 400;
  letter-spacing: 0.1px;
  line-height: 1;
  transform: translateY(-1px);
}

:root[data-skin="brutal"] .acc-card__menu-btn {
  border-radius: 0;
}

:root[data-skin="brutal"] .acc-card__menu-btn:hover {
  background: var(--brutal-yellow);
  color: var(--brutal-ink);
}

:root[data-skin="brutal"] .acc-card__default-tag {
  border-radius: 0;
}

:root[data-skin="brutal"] .acc-card__status-pill {
  border-radius: 0;
}
</style>
