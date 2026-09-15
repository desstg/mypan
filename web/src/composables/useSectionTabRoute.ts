import { ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

export function useSectionTabRoute(
  defaultTab: string,
  validTabs: readonly string[],
  options?: {
    beforeTabChange?: (from: string, to: string) => Promise<boolean>;
  },
) {
  const route = useRoute();
  const router = useRouter();

  function normalizeTabId(tab: string): string {
    const trimmed = tab.trim();
    if (!trimmed) return defaultTab;
    return validTabs.includes(trimmed) ? trimmed : defaultTab;
  }

  const activeTab = ref(normalizeTabId(String(route.query.tab ?? "")));
  let syncingFromRoute = false;

  async function setActiveTab(next: string): Promise<boolean> {
    const target = normalizeTabId(next);
    const from = activeTab.value;
    if (target === from) return true;
    if (options?.beforeTabChange) {
      const ok = await options.beforeTabChange(from, target);
      if (!ok) return false;
    }
    activeTab.value = target;
    return true;
  }

  watch(activeTab, (val) => {
    if (syncingFromRoute) return;
    const normalized = normalizeTabId(val);
    if (normalized !== val) {
      activeTab.value = normalized;
      return;
    }
    if (!val || String(route.query.tab ?? "") === val) return;
    // 用户主动切换 Tab 应写入浏览器历史，使前进/后退能还原上一个视图。
    void router.push({ query: { ...route.query, tab: val } });
  });

  watch(
    () => route.query.tab,
    (q) => {
      const target = normalizeTabId(String(q ?? ""));
      if (target === activeTab.value) return;
      syncingFromRoute = true;
      void (async () => {
        const changed = await setActiveTab(target);
        syncingFromRoute = false;
        if (!changed && String(route.query.tab ?? "") !== activeTab.value) {
          await router.replace({ query: { ...route.query, tab: activeTab.value } });
        }
      })();
    },
  );

  return { activeTab, setActiveTab };
}
