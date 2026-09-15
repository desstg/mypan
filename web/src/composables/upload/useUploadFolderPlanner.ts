import { toast } from "@/composables/useToast";
import { confirmUploadConflict } from "@/composables/confirmUpload";
import { filesApi } from "@/api/files";
import { getSystemUploadJunkReason, normalizeUploadRelativePath } from "@/composables/upload/uploadTaskFormatters";
import type { UploadActionsCtx } from "@/composables/upload/useUploadPanelActions";
import type { UploadTask } from "@/types/upload";

class UploadFolderPreparationCanceled extends Error {}

async function mapWithConcurrency<T>(items: T[], limit: number, run: (item: T) => Promise<void>) {
  let cursor = 0;
  let firstError: unknown = null;
  const workers = Array.from({ length: Math.min(Math.max(1, limit), Math.max(1, items.length)) }, async () => {
    while (!firstError) {
      const index = cursor++;
      if (index >= items.length) return;
      try {
        await run(items[index]);
      } catch (error) {
        firstError = error;
      }
    }
  });
  await Promise.all(workers);
  if (firstError) throw firstError;
}

export function useUploadFolderPlanner(ctx: UploadActionsCtx) {
  const { deps, store, stream, dispatcher } = ctx;
  let folderPreparationRunning = false;

  async function listRemoteEntries(parentId: string, forceRefresh = false) {
    const res = await filesApi.list(deps.selectedAccountId.value as number, parentId, { forceRefresh });
    return res.items;
  }

  async function enqueueUploadFolderFiles(selectedFiles: File[]) {
    if (!selectedFiles.length) return;
    if (folderPreparationRunning) {
      toast.warning("已有文件夹正在准备，请稍候");
      return;
    }
    folderPreparationRunning = true;
    let preparingTask: UploadTask | null = null;
    const createdRemoteFolders: { id: string; parentID: string }[] = [];
    let preparationCommitted = false;
    try {
      store.uploadTaskPanelOpen.value = true;
      store.uploadTaskPanelLoading.value = false;

      const normalized = selectedFiles
        .map((file) => ({ file, relativePath: normalizeUploadRelativePath(file) }))
        .filter((x) => x.relativePath);
      if (!normalized.length) throw new Error("未读取到可上传的文件，空文件夹暂不支持");

      const roots = [...new Set(normalized.map((x) => x.relativePath.split("/")[0]).filter(Boolean))];
      if (roots.length !== 1) throw new Error("当前仅支持一次上传一个文件夹");
      // 系统杂项不是用户上传内容，在规划阶段直接剔除，
      // 既不创建远端任务，也不进入目录任务的总数与状态统计。
      const uploadable = normalized.filter((item) => !getSystemUploadJunkReason(item.relativePath));
      if (!uploadable.length) throw new Error("所选文件夹中没有可上传的文件");
      const batchID = `folder-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      const batchName = roots[0];

      preparingTask = store.createLocalUploadTask(uploadable[0].file, {
        file_name: batchName,
        batch_id: batchID,
        batch_name: batchName,
        batch_placeholder: true,
        rel_path: "",
        rel_dir: "",
      });
      preparingTask.total_bytes = 0;
      preparingTask.uploaded_bytes = 0;
      preparingTask.message = `正在分析 ${uploadable.length} 个文件`;
      store.addLocalUploadTask(preparingTask);
      await stream.refreshUploadTaskServerConcurrency();

      const ensureNotCanceled = () => {
        if (preparingTask && store.canceledLocalUploadTaskIds.has(preparingTask.task_id)) {
          throw new UploadFolderPreparationCanceled();
        }
      };

      const folderIdMap = new Map<string, string>([["", deps.currentPath.value]]);
      const directorySnapshots = new Map<
        string,
        Promise<{ folderIDs: Map<string, string>; names: Set<string> }>
      >();
      const seedEmptyDirectory = (folderID: string) => {
        directorySnapshots.set(
          folderID,
          Promise.resolve({ folderIDs: new Map<string, string>(), names: new Set<string>() }),
        );
      };
      const getDirectorySnapshot = (
        folderID: string,
        forceRefresh = false,
      ): Promise<{ folderIDs: Map<string, string>; names: Set<string> }> => {
        if (forceRefresh) {
          directorySnapshots.delete(folderID);
        }
        const cached = directorySnapshots.get(folderID);
        if (cached) return cached;
        const request = listRemoteEntries(folderID, forceRefresh).then((entries) => ({
          folderIDs: new Map(entries.filter((entry) => entry.is_dir).map((entry) => [entry.name, entry.id])),
          names: new Set(entries.map((entry) => entry.name.toLowerCase())),
        }));
        directorySnapshots.set(folderID, request);
        request.catch(() => directorySnapshots.delete(folderID));
        return request;
      };
      const ensureRemoteFolder = async (parentID: string, folderName: string) => {
        const snapshot = await getDirectorySnapshot(parentID);
        const existingID = snapshot.folderIDs.get(folderName);
        if (existingID) return { folderID: existingID, created: false };
        try {
          const res = await filesApi.createFolder({
            account_id: deps.selectedAccountId.value as number,
            parent_id: parentID,
            name: folderName,
          });
          snapshot.folderIDs.set(folderName, res.folder_id);
          snapshot.names.add(folderName.toLowerCase());
          seedEmptyDirectory(res.folder_id);
          createdRemoteFolders.push({ id: res.folder_id, parentID });
          return { folderID: res.folder_id, created: true };
        } catch {
          // 仅在创建请求失败时等待远端索引落稳；正常复用目录不再走固定延迟。
          await new Promise((resolve) => setTimeout(resolve, 300));
          const refreshed = await getDirectorySnapshot(parentID, true);
          const hit = refreshed.folderIDs.get(folderName);
          if (hit) return { folderID: hit, created: false };
          await new Promise((resolve) => setTimeout(resolve, 700));
          const retried = await getDirectorySnapshot(parentID, true);
          const retriedHit = retried.folderIDs.get(folderName);
          if (retriedHit) return { folderID: retriedHit, created: false };
          throw new Error(`创建文件夹 "${folderName}" 失败`);
        }
      };
      const skipped: UploadTask[] = [];
      const dirs = new Set<string>();
      for (const item of uploadable) {
        const parts = item.relativePath.split("/");
        parts.pop();
        const acc: string[] = [];
        for (const p of parts) {
          acc.push(p);
          dirs.add(acc.join("/"));
        }
      }
      const sortedDirs = [...dirs].sort((a, b) => a.split("/").length - b.split("/").length);
      const dirsByDepth = new Map<number, string[]>();
      for (const rel of sortedDirs) {
        const depth = rel.split("/").length;
        const level = dirsByDepth.get(depth) || [];
        level.push(rel);
        dirsByDepth.set(depth, level);
      }
      let preparedDirs = 0;
      let batchRootID = "";
      let batchRootParentID = "";
      let batchRootOwned = false;
      const progressStep = Math.max(1, Math.floor(sortedDirs.length / 50));
      const directoryConcurrency = Math.min(3, Math.max(1, Number(store.uploadTaskServerConcurrency.value || 3)));
      for (const depth of [...dirsByDepth.keys()].sort((a, b) => a - b)) {
        await mapWithConcurrency(dirsByDepth.get(depth) || [], directoryConcurrency, async (rel) => {
          ensureNotCanceled();
          const parts = rel.split("/");
          const name = parts[parts.length - 1];
          const parentRel = parts.slice(0, -1).join("/");
          const parentID = folderIdMap.get(parentRel);
          if (parentID === undefined) throw new Error(`未找到上级目录：${parentRel || "根目录"}`);
          const { folderID, created } = await ensureRemoteFolder(parentID, name);
          folderIdMap.set(rel, folderID);
          if (rel === batchName) {
            batchRootID = folderID;
            batchRootParentID = parentID;
            batchRootOwned = created;
          }
          if (parentID === deps.currentPath.value) store.markFolderUploadRefreshPending();
          preparedDirs += 1;
          if (preparingTask && (preparedDirs === sortedDirs.length || preparedDirs % progressStep === 0)) {
            store.updateLocalUploadTask(preparingTask.task_id, {
              progress: sortedDirs.length ? Math.round((preparedDirs / sortedDirs.length) * 70) : 70,
              message: `正在准备目录 ${preparedDirs}/${sortedDirs.length}`,
            });
          }
        });
      }

      let batchConflictPolicy: string | null = null;
      const plans: {
        file: File;
        conflictPolicy: string;
        localTask: UploadTask;
        targetPath: string;
        displayName: string;
        targetDisplayPath: string;
        batchRootId: string;
        batchRootParentId: string;
        batchRootOwned: boolean;
      }[] = [];

      const fileProgressStep = Math.max(1, Math.floor(uploadable.length / 40));
      for (let fileIndex = 0; fileIndex < uploadable.length; fileIndex += 1) {
        ensureNotCanceled();
        const item = uploadable[fileIndex];
        const parts = item.relativePath.split("/");
        parts.pop();
        const relDir = parts.join("/");
        const targetPath = folderIdMap.get(relDir) || deps.currentPath.value;
        const targetDisplayPath = dispatcher.buildUploadTargetDisplayPath(relDir);
        // 新创建的目录已写入空快照，这里不会再次请求远端列表。
        const remoteNames = (await getDirectorySnapshot(targetPath)).names;
        let conflictPolicy = "overwrite";
        if (remoteNames.has(item.file.name.toLowerCase())) {
          if (!batchConflictPolicy) {
            const r = await confirmUploadConflict(item.relativePath);
            if (!r) throw new UploadFolderPreparationCanceled();
            if (r.checked) batchConflictPolicy = r.action;
            conflictPolicy = r.action;
          } else conflictPolicy = batchConflictPolicy;
          if (conflictPolicy === "skip") {
            const relPath = item.relativePath.split("/").slice(1).join("/") || item.file.name;
            skipped.push(
              store.createSkippedUploadTask(item.file, "检测到同名文件，已跳过", {
                file_name: item.relativePath,
                batch_id: batchID,
                batch_name: batchName,
                rel_path: relPath,
                rel_dir: relPath.split("/").slice(0, -1).join("/"),
                target_path: targetPath,
                target_display_path: targetDisplayPath,
              }),
            );
            continue;
          }
        }
        remoteNames.add(item.file.name.toLowerCase());
        const localTask = store.createLocalUploadTask(item.file, {
          file_name: item.relativePath,
          batch_id: batchID,
          batch_name: batchName,
          rel_path: item.relativePath.split("/").slice(1).join("/") || item.file.name,
          rel_dir: item.relativePath.split("/").slice(1, -1).join("/"),
          target_path: targetPath,
          target_display_path: targetDisplayPath,
        });
        plans.push({
          file: item.file,
          conflictPolicy,
          localTask,
          targetPath,
          displayName: item.relativePath,
          targetDisplayPath,
          batchRootId: batchRootID,
          batchRootParentId: batchRootParentID,
          batchRootOwned,
        });
        if (preparingTask && (fileIndex + 1 === uploadable.length || (fileIndex + 1) % fileProgressStep === 0)) {
          store.updateLocalUploadTask(preparingTask.task_id, {
            progress: 70 + Math.round(((fileIndex + 1) / Math.max(1, uploadable.length)) * 28),
            message: `正在整理文件 ${fileIndex + 1}/${uploadable.length}`,
          });
        }
      }

      ensureNotCanceled();
      for (const p of plans) {
        store.localUploadTaskPayloads.set(p.localTask.task_id, {
          file: p.file,
          conflictPolicy: p.conflictPolicy,
          targetPath: p.targetPath,
          displayName: p.displayName,
          targetDisplayPath: p.targetDisplayPath,
          batchRootId: p.batchRootId,
          batchRootParentId: p.batchRootParentId,
          batchRootOwned: p.batchRootOwned,
        });
        store.ensureUploadTaskDisplayOrder(p.localTask);
      }
      const localTasks = [...skipped, ...plans.map((p) => p.localTask)];
      store.removeLocalUploadTask(preparingTask.task_id);
      preparingTask = null;
      if (localTasks.length) {
        store.addLocalUploadTasks(localTasks);
      }
      if (plans.length) {
        void dispatcher.startUploadTaskScheduler();
      }
      preparationCommitted = true;
    } catch (e) {
      if (e instanceof UploadFolderPreparationCanceled) {
        if (preparingTask) {
          store.removeLocalUploadTask(preparingTask.task_id);
          store.canceledLocalUploadTaskIds.delete(preparingTask.task_id);
        }
      } else {
        const message = e instanceof Error ? e.message : "准备上传文件夹失败";
        if (preparingTask && !store.canceledLocalUploadTaskIds.has(preparingTask.task_id)) {
          store.updateLocalUploadTask(preparingTask.task_id, {
            status: "failed",
            message: "文件夹准备失败",
            error: message,
          });
        }
        toast.error(message);
      }
    } finally {
      if (!preparationCommitted && createdRemoteFolders.length) {
        // 只回收本次新建且仍为空的目录；从最深层向上处理，绝不碰复用目录。
        for (const folder of [...createdRemoteFolders].reverse()) {
          try {
            const entries = await listRemoteEntries(folder.id, true);
            if (entries.length === 0) {
              await filesApi.deleteFiles({
                account_id: deps.selectedAccountId.value as number,
                file_ids: [folder.id],
                parent_id: folder.parentID,
              });
            }
          } catch {
            // 网络异常时保留空目录比误删更安全；下次用户可自行处理。
          }
        }
      }
      folderPreparationRunning = false;
      store.uploadTaskPanelLoading.value = false;
      store.uploadTaskPanelLoadingText.value = "正在准备上传任务...";
    }
  }

  async function handleUploadFolderChange(event: Event) {
    const target = event.target as HTMLInputElement;
    try {
      await enqueueUploadFolderFiles(Array.from(target.files || []));
    } finally {
      target.value = "";
    }
  }

  return { handleUploadFolderChange, enqueueUploadFolderFiles };
}
