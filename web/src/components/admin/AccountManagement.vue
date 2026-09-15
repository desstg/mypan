<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { useAccountsStore } from "@/stores/accounts";
import { getApiErrorMessage } from "@/api/client";
import { accountsApi } from "@/api/accounts";
import type { Account } from "@/api/types";
import { toast } from "@/composables/useToast";
import { confirm } from "@/composables/useConfirm";
import { useAdminPageLoading } from "@/composables/useAdminLoadingBar";
import { findDustTarget, useDustRemoval } from "@/composables/useDustRemoval";
import AccountCard from "./AccountCard.vue";
import AddAccountDialog from "./AddAccountDialog.vue";

const store = useAccountsStore();
const { accounts, loading } = storeToRefs(store);

const dialogOpen = ref(false);
const editing = ref<Account | null>(null);
const accountsGrid = ref<HTMLElement | null>(null);
const { removeWithDust } = useDustRemoval();
const initialLoading = computed(() => loading.value && !accounts.value.length);
useAdminPageLoading("accounts", initialLoading);

function openCreate() {
  editing.value = null;
  dialogOpen.value = true;
}

function openEdit(account: Account) {
  editing.value = account;
  dialogOpen.value = true;
}

async function remove(account: Account) {
  try {
    await confirm({
      title: "确认删除",
      message: `确定要删除账号「${account.name}」吗？此操作不可撤销。`,
      icon: "trash",
      confirmText: "删除",
      danger: true,
      size: "md",
    });
  } catch {
    return;
  }
  try {
    await removeWithDust({
      target: findDustTarget(accountsGrid.value, `account-${account.id}`),
      container: accountsGrid.value,
      remove: () => store.remove(account.id),
    });
    toast.success("账号已删除");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "删除失败"));
  }
}

async function toggle(account: Account) {
  const nextActive = !account.is_active;
  try {
    await store.toggle(account.id);
    toast.success(`账号「${account.name}」已${nextActive ? "启用" : "禁用"}`);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "状态切换失败"));
  }
}

async function setDefault(account: Account) {
  try {
    await store.setDefault(account.id);
    toast.success(`已将「${account.name}」设为默认账号`);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "设置默认失败"));
  }
}

async function refreshProfile(account: Account) {
  try {
    await accountsApi.refreshProfile(account.id);
    await store.loadAccounts();
    toast.success("账号信息已刷新");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "刷新失败"));
  }
}

let refreshTimer: number | undefined;
onMounted(async () => {
  await store.loadAccounts();
  store.loadDrivers();
  refreshTimer = window.setTimeout(() => store.loadAccounts(), 1200);
});
onBeforeUnmount(() => window.clearTimeout(refreshTimer));
</script>

<template>
  <section class="accounts">
    <div v-if="!initialLoading" ref="accountsGrid" class="accounts__grid">
      <AccountCard
        v-for="acc in accounts"
        :key="acc.id"
        :data-dust-key="`account-${acc.id}`"
        :account="acc"
        :driver="store.driverOf(acc.driver_type)"
        @edit="openEdit"
        @remove="remove"
        @toggle="toggle"
        @set-default="setDefault"
        @refresh-profile="refreshProfile"
      />

      <button class="add-card" data-dust-key="account-add" @click="openCreate">
        <span class="add-card__plus">＋</span>
        <span>添加账号</span>
      </button>
    </div>

    <AddAccountDialog
      :open="dialogOpen"
      :editing="editing"
      @close="dialogOpen = false"
      @saved="store.loadAccounts"
    />
  </section>
</template>

<style scoped>
.accounts__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 16px;
}
.add-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 120px;
  border: 2px dashed var(--border);
  border-radius: var(--radius-xl);
  background: transparent;
  color: var(--text-muted);
  transition: var(--transition);
}
.add-card:hover {
  border-color: var(--brand);
  color: var(--brand);
}
.add-card__plus {
  font-size: 28px;
}
</style>
