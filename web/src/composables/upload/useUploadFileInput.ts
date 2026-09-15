import { confirmUploadConflict } from "@/composables/confirmUpload";
import {
  getSystemUploadJunkReason,
  normalizeUploadRelativePath,
} from "@/composables/upload/uploadTaskFormatters";
import type { UploadActionsCtx } from "@/composables/upload/useUploadPanelActions";
import type { UploadTask } from "@/types/upload";

export function useUploadFileInput(ctx: UploadActionsCtx) {
  const { deps, store, stream, dispatcher } = ctx;

  async function handleUploadFileChange(event: Event) {
    const target = event.target as HTMLInputElement;
    const selectedFiles = Array.from(target.files || []);
    if (!selectedFiles.length) return;
    try {
      store.uploadTaskPanelOpen.value = true;
      await stream.refreshUploadTaskServerConcurrency();
      let batchConflictPolicy: string | null = null;
      const existing = new Set(deps.files.value.map((f) => f.name.toLowerCase()));
      const plans: { file: File; conflictPolicy: string; localTask: UploadTask }[] = [];
      const skipped: UploadTask[] = [];

      for (const file of selectedFiles) {
        const skipReason = getSystemUploadJunkReason(normalizeUploadRelativePath(file));
        if (skipReason) {
          skipped.push(store.createSkippedUploadTask(file, skipReason));
          continue;
        }
        let conflictPolicy = "overwrite";
        if (existing.has(file.name.toLowerCase())) {
          if (!batchConflictPolicy) {
            const r = await confirmUploadConflict(file.name);
            if (!r) break;
            if (r.checked) batchConflictPolicy = r.action;
            conflictPolicy = r.action;
          } else {
            conflictPolicy = batchConflictPolicy;
          }
          if (conflictPolicy === "skip") {
            skipped.push(store.createSkippedUploadTask(file, "检测到同名文件，已跳过"));
            continue;
          }
        }
        plans.push({ file, conflictPolicy, localTask: store.createLocalUploadTask(file) });
      }

      for (const p of plans) {
        store.localUploadTaskPayloads.set(p.localTask.task_id, {
          file: p.file,
          conflictPolicy: p.conflictPolicy,
        });
        store.ensureUploadTaskDisplayOrder(p.localTask);
      }
      const localTasks = [...skipped, ...plans.map((p) => p.localTask)];
      if (localTasks.length) {
        store.addLocalUploadTasks(localTasks);
      }
      if (plans.length) {
        void dispatcher.startUploadTaskScheduler();
      }
    } finally {
      target.value = "";
    }
  }

  return { handleUploadFileChange };
}
