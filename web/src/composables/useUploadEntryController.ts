import { ref } from "vue";
import { localUploadApi } from "@/api/cloudTools";

type UploadKind = "file" | "folder";

type UseUploadEntryControllerOptions = {
  ensureUploadNoticeConfirmed: () => Promise<boolean>;
  handleTerminalUploadFile: () => Promise<void>;
  handleTerminalUploadFolder: () => Promise<void>;
  handleTerminalUploadFileChange: (event: Event) => Promise<void>;
  handleTerminalUploadFolderChange: (event: Event) => Promise<void>;
};

export function useUploadEntryController(options: UseUploadEntryControllerOptions) {
  const localUploadPanelOpen = ref(false);
  const localUploadKind = ref<UploadKind>("file");

  async function localUploadEnabledNow(): Promise<boolean> {
    try {
      const cfg = await localUploadApi.getConfig();
      return cfg.enabled;
    } catch {
      return false;
    }
  }

  function closeLocalUploadPanel() {
    localUploadPanelOpen.value = false;
  }

  async function openUploadEntry(kind: UploadKind) {
    if (await localUploadEnabledNow()) {
      if (!(await options.ensureUploadNoticeConfirmed())) return;
      localUploadKind.value = kind;
      localUploadPanelOpen.value = true;
      return;
    }
    if (kind === "folder") {
      await options.handleTerminalUploadFolder();
      return;
    }
    await options.handleTerminalUploadFile();
  }

  async function handleUploadFile() {
    await openUploadEntry("file");
  }

  async function handleUploadFolder() {
    await openUploadEntry("folder");
  }

  async function handleUploadFileChange(event: Event) {
    await options.handleTerminalUploadFileChange(event);
    closeLocalUploadPanel();
  }

  async function handleUploadFolderChange(event: Event) {
    await options.handleTerminalUploadFolderChange(event);
    closeLocalUploadPanel();
  }

  return {
    localUploadPanelOpen,
    localUploadKind,
    closeLocalUploadPanel,
    handleUploadFile,
    handleUploadFolder,
    handleUploadFileChange,
    handleUploadFolderChange,
  };
}
