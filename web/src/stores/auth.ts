import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { fetchAuthStatus, type AuthStatus, type LoginResult } from "@/api/auth";

function syncAdminBodyClass(isAdmin: boolean) {
  if (isAdmin) {
    document.body.classList.add("admin-mode");
  } else {
    document.body.classList.remove("admin-mode");
  }
}

export const useAuthStore = defineStore("auth", () => {
  const sessionAdmin = ref(false);
  const username = ref("");
  const mustChangePassword = ref(false);
  const passwordChangeReason = ref("");
  const publicIndexEnabled = ref(false);
  const loaded = ref(false);

  const isAdmin = computed(() => sessionAdmin.value && !mustChangePassword.value);

  function applyStatus(data: AuthStatus) {
    sessionAdmin.value = Boolean(data.is_admin);
    username.value = data.username ?? "";
    mustChangePassword.value = Boolean(data.must_change_password);
    passwordChangeReason.value = data.password_change_reason ?? "";
    publicIndexEnabled.value = data.public_index_enabled ?? false;
    syncAdminBodyClass(isAdmin.value);
    loaded.value = true;
  }

  async function applyLogin(result: LoginResult) {
    sessionAdmin.value = Boolean(result.is_admin);
    username.value = result.username ?? "";
    mustChangePassword.value = Boolean(result.must_change_password);
    passwordChangeReason.value = result.password_change_reason ?? "";
    loaded.value = true;
    syncAdminBodyClass(isAdmin.value);
  }

  function clear() {
    sessionAdmin.value = false;
    username.value = "";
    mustChangePassword.value = false;
    passwordChangeReason.value = "";
    syncAdminBodyClass(false);
  }

  async function load() {
    try {
      applyStatus(await fetchAuthStatus());
    } catch {
      clear();
    } finally {
      loaded.value = true;
    }
  }

  return {
    sessionAdmin,
    username,
    mustChangePassword,
    passwordChangeReason,
    publicIndexEnabled,
    isAdmin,
    loaded,
    load,
    applyStatus,
    applyLogin,
    clear,
  };
});
