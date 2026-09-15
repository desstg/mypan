import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";
import { fetchAuthStatus } from "@/api/auth";
import { useAuthStore } from "@/stores/auth";

const routes: RouteRecordRaw[] = [
  {
    path: "/",
    name: "home",
    component: () => import("@/views/IndexView.vue"),
    meta: { title: "文件浏览" },
  },
  {
    path: "/login",
    name: "login",
    component: () => import("@/views/LoginView.vue"),
    meta: { title: "管理员登录", guestOnly: true },
  },
  {
    path: "/admin",
    name: "admin",
    component: () => import("@/views/AdminView.vue"),
    meta: { title: "管理后台", requiresAuth: true },
  },
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

router.beforeEach(async (to) => {
  if (to.meta.title) {
    document.title = `${String(to.meta.title)} - MyPan`;
  }

  const auth = useAuthStore();

  if (to.meta.requiresAuth) {
    if (auth.loaded && auth.sessionAdmin) {
      return true;
    }
    try {
      const status = await fetchAuthStatus();
      if (!status.is_admin) {
        return { path: "/login", query: { redirect: to.fullPath } };
      }
      auth.applyStatus(status);
      return true;
    } catch {
      return { path: "/login", query: { redirect: to.fullPath } };
    }
  }

  if (to.meta.guestOnly) {
    if (auth.loaded && auth.sessionAdmin) return "/admin";
    try {
      const status = await fetchAuthStatus();
      if (status.is_admin) {
        auth.applyStatus(status);
        return "/admin";
      }
    } catch {
      /* 未登录，继续访问登录页 */
    }
  }

  if (to.name === "home") {
    if (auth.loaded && auth.sessionAdmin) return true;
    try {
      const status = await fetchAuthStatus();
      auth.applyStatus(status);
      if (!status.public_index_enabled && !status.is_admin) {
        return { path: "/login", query: { redirect: to.fullPath } };
      }
    } catch {
      /* 网络异常时仍允许访问首页 */
    }
  }

  return true;
});
