import { onUnmounted, watch, type Ref } from "vue";

export function useDismissOnOutside(
  active: Ref<boolean>,
  refs: Ref<(HTMLElement | null)[]>,
  onDismiss: () => void,
) {
  function onDocClick(e: MouseEvent) {
    if (!active.value) return;
    const target = e.target as Node;
    if (refs.value.some((el) => el?.contains(target))) return;
    onDismiss();
  }

  watch(active, (open) => {
    if (open) document.addEventListener("click", onDocClick);
    else document.removeEventListener("click", onDocClick);
  });

  onUnmounted(() => document.removeEventListener("click", onDocClick));
}
