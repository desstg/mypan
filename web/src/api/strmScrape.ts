import { http } from "./client";
import type { FileItem } from "./types";

export type StrmScrapeWriteMode = "missing_only" | "overwrite";
export type StrmScrapeItemStatus = "ok" | "miss" | "doubt";
export type StrmScrapeTVState = "ended" | "updating";
export type StrmScrapeItemListSort =
  | "title_asc"
  | "year_desc"
  | "year_asc"
  | "added_desc"
  | "added_asc";

export interface StrmScrapeItem {
  id: string;
  rel_dir: string;
  strm_name?: string;
  title: string;
  year?: number;
  media_type: string;
  status: StrmScrapeItemStatus;
  has_nfo: boolean;
  has_poster: boolean;
  has_pending?: boolean;
  manual_done?: boolean;
  tmdb_id?: string;
  poster_url?: string;
  folder_name?: string;
  file_count: number;
  ep_local?: number;
  ep_tmdb?: number;
  ep_scraped?: number;
  tv_state?: StrmScrapeTVState | string;
  added_at?: string;
}

export interface StrmScrapeProgress {
  running: boolean;
  strm_task_id: number;
  total: number;
  done: number;
  skipped: number;
  failed: number;
  message: string;
  error?: string;
  started_at?: string;
  current_item_id: string;
  item_revision: number;
  updated_item?: StrmScrapeItem;
}

export interface StrmScrapeRematchResult {
  item: StrmScrapeItem;
  started: boolean;
  progress: StrmScrapeProgress;
}

export interface StrmScrapeSettings {
  write_mode: StrmScrapeWriteMode;
  tmdb_api_key: string;
  tmdb_language: string;
  tmdb_api_host: string;
  tmdb_image_host: string;
  tmdb_request_interval_ms: number;
  proxy_enabled: boolean;
  proxy_url: string;
  proxy_username: string;
  proxy_password: string;
}

export interface StrmScrapeItemListQuery {
  offset?: number;
  limit?: number;
  keyword?: string;
  status?: StrmScrapeItemStatus | "";
  media_type?: "movie" | "tv" | "";
  tv_state?: StrmScrapeTVState | "";
  sort?: StrmScrapeItemListSort;
}

export interface StrmScrapeItemListStats {
  total: number;
  ok: number;
  miss: number;
  doubt: number;
}

export interface StrmScrapeItemListResult {
  items: StrmScrapeItem[];
  total: number;
  offset: number;
  limit: number;
  has_more: boolean;
  stats: StrmScrapeItemListStats;
}

export interface StrmScrapeScope {
  strm_task_id: number;
  excluded_dirs: string[];
}

export function fetchStrmScrapeScope(strmTaskId: number) {
  return http.get<StrmScrapeScope>("/admin/strm-scrape/scope", { strm_task_id: strmTaskId });
}

export function saveStrmScrapeScope(strmTaskId: number, excludedDirs: string[]) {
  return http.put<StrmScrapeScope>("/admin/strm-scrape/scope", {
    strm_task_id: strmTaskId,
    excluded_dirs: excludedDirs,
  });
}

export async function fetchStrmScrapeScopeDirectories(strmTaskId: number, parent = "") {
  const items = await http.get<Array<{ id: string; name: string; mod_time?: string }>>(
    "/admin/strm-scrape/scope/directories",
    { strm_task_id: strmTaskId, parent },
  );
  return items.map<FileItem>((item) => ({
    id: item.id,
    name: item.name,
    size: 0,
    is_dir: true,
    mod_time: item.mod_time,
  }));
}

export function fetchStrmScrapeSettings() {
  return http.get<StrmScrapeSettings>("/admin/strm-scrape/settings");
}

export function saveStrmScrapeSettings(settings: Partial<StrmScrapeSettings>) {
  return http.put<StrmScrapeSettings>("/admin/strm-scrape/settings", settings);
}

export function runStrmScrape(strmTaskId: number, writeMode?: StrmScrapeWriteMode) {
  return http.post<StrmScrapeProgress>("/admin/strm-scrape/run", {
    strm_task_id: strmTaskId,
    write_mode: writeMode,
  });
}

export function stopStrmScrape() {
  return http.post<StrmScrapeProgress>("/admin/strm-scrape/stop");
}

export function fetchStrmScrapeProgress() {
  return http.get<StrmScrapeProgress>("/admin/strm-scrape/progress");
}

export function fetchStrmScrapeItems(strmTaskId: number, query: StrmScrapeItemListQuery = {}) {
  return http.get<StrmScrapeItemListResult>("/admin/strm-scrape/items", {
    strm_task_id: strmTaskId,
    ...query,
  });
}

export function refreshStrmScrapeIndex(strmTaskId: number, query: StrmScrapeItemListQuery = {}) {
  return http.post<StrmScrapeItemListResult>("/admin/strm-scrape/refresh-index", {
    strm_task_id: strmTaskId,
    ...query,
  });
}

export function rematchStrmScrapeItem(input: {
  strm_task_id: number;
  item_id: string;
  tmdb_id: string;
  media_type: string;
  title?: string;
  year?: number;
}) {
  return http.post<StrmScrapeRematchResult>("/admin/strm-scrape/rematch", input);
}

export function markStrmScrapeNormal(input: {
  strm_task_id: number;
  item_id: string;
  media_type?: string;
  clear_match?: boolean;
}) {
  return http.post<StrmScrapeItem>("/admin/strm-scrape/mark-normal", input);
}

export function rescrapeStrmScrapeItem(input: { strm_task_id: number; item_id: string }) {
  return http.post<StrmScrapeRematchResult>("/admin/strm-scrape/rescrape", input);
}
