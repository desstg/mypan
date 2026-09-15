import { ref } from "vue";

const STORAGE_KEY = "litepan:toolbar-performance-expanded";

export function usePerformancePanel() {
  const expanded = ref(readExpanded());

  function readExpanded() {
    try {
      return localStorage.getItem(STORAGE_KEY) === "true";
    } catch {
      return false;
    }
  }

  function toggle() {
    expanded.value = !expanded.value;
    try {
      localStorage.setItem(STORAGE_KEY, String(expanded.value));
    } catch {}
  }

  return { expanded, toggle };
}
