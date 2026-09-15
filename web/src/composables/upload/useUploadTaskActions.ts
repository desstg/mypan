import type { LocalUploadDispatcher } from "@/composables/upload/useLocalUploadDispatcher";
import type { UploadTaskStream } from "@/composables/upload/useUploadTaskStream";
import type { UploadTaskStore } from "@/composables/upload/useUploadTaskStore";
import { useUploadBatchActions, useUploadRelayActions } from "@/composables/upload/useUploadBatchActions";
import { useUploadFileInput } from "@/composables/upload/useUploadFileInput";
import { useUploadFolderPlanner } from "@/composables/upload/useUploadFolderPlanner";
import {
  useUploadPanelActions,
  type UploadActionsCtx,
} from "@/composables/upload/useUploadPanelActions";
import type { UploadTaskDeps } from "@/composables/upload/uploadTaskTypes";

export function useUploadTaskActions(
  deps: UploadTaskDeps,
  store: UploadTaskStore,
  stream: UploadTaskStream,
  dispatcher: LocalUploadDispatcher,
) {
  const ctx: UploadActionsCtx = { deps, store, stream, dispatcher };
  const panel = useUploadPanelActions(ctx);
  const fileInput = useUploadFileInput(ctx);
  const folderPlanner = useUploadFolderPlanner(ctx);
  const batch = useUploadBatchActions(ctx, panel.closeUploadTaskPanel);
  const relay = useUploadRelayActions(ctx);

  return {
    ...panel,
    ...fileInput,
    ...folderPlanner,
    ...batch,
    ...relay,
  };
}
