import { http } from "./client";

export type CleanupRisk = "safe" | "review" | "rebuild" | "locking";

export interface CleanupItem {
  id: string;
  category: string;
  name: string;
  path?: string;
  reason: string;
  size_bytes: number;
  memory_bytes?: number;
  file_count?: number;
  dir_count?: number;
  default_selected: boolean;
  risk: CleanupRisk;
}

export interface CleanupGroup {
  key: string;
  label: string;
  description: string;
  count: number;
  size_bytes: number;
  memory_bytes?: number;
  items: CleanupItem[];
}

export interface CleanupScanReport {
  scan_id: string;
  scanned_at: string;
  expires_at: string;
  total_count: number;
  total_size_bytes: number;
  total_memory_bytes: number;
  groups: CleanupGroup[];
}

export interface CleanupItemResult {
  id: string;
  name: string;
  status: "cleaned" | "skipped" | "failed";
  message?: string;
  freed_bytes?: number;
  memory_bytes?: number;
  files?: number;
  dirs?: number;
}

export interface CleanupResult {
  cleaned_items: number;
  skipped_items: number;
  failed_items: number;
  freed_bytes: number;
  memory_freed_bytes: number;
  removed_files: number;
  removed_dirs: number;
  results: CleanupItemResult[];
}

export const spaceCleanupApi = {
  scan: () => http.post<CleanupScanReport>("/admin/tools/cleanup/scan"),
  execute: (scanId: string, itemIds: string[]) =>
    http.post<CleanupResult>("/admin/tools/cleanup/execute", {
      scan_id: scanId,
      item_ids: itemIds,
    }),
  // 页面刷新后恢复最近一次未过期扫描，避免卡片回到“等待体检”。
  latestReport: () =>
    http.get<{ report: CleanupScanReport | null }>("/admin/tools/cleanup/report"),
};
