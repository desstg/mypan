import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { accountsApi } from "@/api/accounts";
import { publicApi } from "@/api/public";
import { driversApi } from "@/api/drivers";
import { useAuthStore } from "@/stores/auth";
import { useDeveloperUnlock } from "@/composables/useDeveloperUnlock";
import type { Account, AccountPayload, DriverInfo } from "@/api/types";

export const useAccountsStore = defineStore("accounts", () => {
  const accounts = ref<Account[]>([]);
  const drivers = ref<DriverInfo[]>([]);
  const { unlocked: devUnlocked, init: devUnlockInit } = useDeveloperUnlock();
  // 可选驱动列表：内部实验性驱动需解锁开发模式后才展示。
  const visibleDrivers = computed(() =>
    devUnlocked.value ? drivers.value : drivers.value.filter((d) => !d.internal_experimental),
  );
  const loading = ref(false);
  // 进行中去重：多个面板同时挂载时共享同一次 /accounts 请求，避免并发重复拉取。
  let inflightLoad: Promise<void> | null = null;

  function loadAccounts(): Promise<void> {
    if (inflightLoad) return inflightLoad;
    loading.value = true;
    inflightLoad = (async () => {
      try {
        const auth = useAuthStore();
        accounts.value = auth.isAdmin
          ? await accountsApi.list()
          : await publicApi.listAccounts();
      } finally {
        loading.value = false;
        inflightLoad = null;
      }
    })();
    return inflightLoad;
  }

  async function loadDrivers() {
    void devUnlockInit();
    if (drivers.value.length) return;
    drivers.value = await driversApi.list();
  }

  async function create(payload: AccountPayload) {
    await accountsApi.create(payload);
    await loadAccounts();
  }

  async function update(id: number, payload: AccountPayload) {
    await accountsApi.update(id, payload);
    await loadAccounts();
  }

  async function remove(id: number) {
    await accountsApi.remove(id);
    await loadAccounts();
  }

  async function toggle(id: number) {
    const updated = await accountsApi.toggle(id);
    const idx = accounts.value.findIndex((a) => a.id === id);
    if (idx >= 0) accounts.value[idx] = updated;
  }

  async function setDefault(id: number) {
    await accountsApi.setDefault(id);
    await loadAccounts();
  }

  function driverOf(name: string): DriverInfo | undefined {
    return drivers.value.find((d) => d.name === name);
  }

  return {
    accounts,
    drivers,
    visibleDrivers,
    loading,
    loadAccounts,
    loadDrivers,
    create,
    update,
    remove,
    toggle,
    setDefault,
    driverOf,
  };
});
