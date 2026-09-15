import { computed, type Ref } from "vue";
import type { FileItem } from "@/api/types";

export function fileKey(file: FileItem) {
  return file.id || file.name;
}

export function useFileSelection(files: Ref<FileItem[]>, selectedIds: Ref<string[]>) {
  const selectedCount = computed(() => selectedIds.value.length);

  const selectedFiles = computed(() => {
    const set = new Set(selectedIds.value);
    return files.value.filter((f) => set.has(fileKey(f)));
  });

  function clear() {
    selectedIds.value = [];
  }

  return { selectedCount, selectedFiles, clear };
}
