import type { Ref } from "vue";
import type { Account, FileItem } from "@/api/types";

export const UPLOAD_NOTICE_KEY = "litepan:index:upload-server-transfer-notice-hidden";

export type UploadCrumb = { id: string; name: string };
export type UploadTaskDeps = {
  selectedAccountId: Ref<number | null>;
  selectedAccountName: Ref<string>;
  accounts: Ref<Account[]>;
  currentPath: Ref<string>;
  breadcrumbItems: Ref<UploadCrumb[]>;
  selectedFilesList: Ref<FileItem[]>;
  files: Ref<FileItem[]>;
  uploadFileInput: Ref<HTMLInputElement | null>;
  uploadFolderInput: Ref<HTMLInputElement | null>;
  removeFilesLocally: (ids: string[]) => void;
  markDeletingFiles: (rowKeys: string[]) => void;
  clearDeletingFiles: (rowKeys: string[]) => void;
  refreshFiles: (force?: boolean) => Promise<void>;
  loadFiles: (opts?: { forceRefresh?: boolean; silent?: boolean }) => Promise<void>;
  openDirectory: (
    accountId: number,
    crumbs: UploadCrumb[],
    opts?: { forceRefresh?: boolean; silent?: boolean },
  ) => Promise<void>;
  selectAccount: (account: Account) => Promise<void>;
  getRootId: (config: Record<string, unknown>) => string;
  getCurrentBreadcrumbNameParts: () => string[];
  refreshOfflineTasks: (refresh?: boolean, quiet?: boolean) => Promise<void>;
};

export type LocalUploadPayload = {
  file: File;
  conflictPolicy: string;
  targetPath?: string;
  displayName?: string;
  targetDisplayPath?: string;
  batchRootId?: string;
  batchRootParentId?: string;
  batchRootOwned?: boolean;
};

export type UploadRuntimeHooks = {
  startScheduler: () => Promise<void>;
  fetchTasks: () => Promise<void>;
  startPolling: () => void;
  stopPolling: () => void;
  connectStream: () => void;
  disconnectStream: () => void;
  closePanel: () => void;
};
