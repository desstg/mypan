import { onBeforeUnmount, watch, type WatchSource } from "vue";
import { useUnsavedChanges } from "@/composables/useUnsavedChanges";

export function useSettingsPageDirty(isDirty: WatchSource<boolean>, onRevert?: () => void) {
  const source = Symbol("settings-page-dirty");
  const {
    dirty,
    confirmLeave,
    discardChanges,
    updateDirtySource,
    removeDirtySource,
  } = useUnsavedChanges();

  watch(
    isDirty,
    (v) => {
      updateDirtySource(source, v, onRevert);
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    removeDirtySource(source);
  });

  async function confirmDiscardChanges(hasChanges: () => boolean = () => dirty.value): Promise<boolean> {
    if (!hasChanges()) return true;
    const ok = await confirmLeave();
    if (!ok) return false;
    discardChanges();
    return true;
  }

  return { confirmDiscardChanges };
}
