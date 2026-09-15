import { http } from "./client";

export const NOTIFICATION_CATEGORY_CACHE_SCOPE_WARN = "cache_scope_warn";
export const NOTIFICATION_CATEGORY_STRM_SCAN_WARN = "strm_scan_warn";
export const NOTIFICATION_CATEGORY_STRM_SCRAPE_WARN = "strm_scrape_warn";

const STRM_SCAN_DETAIL_SEP = "\n---detail---\n";

export interface NotificationItem {
  id: number;
  level: string;
  category: string;
  title: string;
  message: string;
  account_id?: number;
  ref_id?: number;
  is_read: boolean;
  created_at: string;
}

export interface StrmScanFailureItem {
  kind: string;
  path: string;
  reason: string;
}

export interface StrmScrapeFailureItem {
  stage: string;
  name: string;
  path: string;
  reason: string;
}

export async function fetchNotifications(params?: { limit?: number; offset?: number }) {
  return http.get<{ items: NotificationItem[] }>("/admin/notifications", params);
}

export async function fetchUnreadCount() {
  return http.get<{ count: number }>("/admin/notifications/unread-count");
}

export async function markNotificationRead(id: number) {
  await http.post<Record<string, never>>(`/admin/notifications/${id}/read`);
}

export async function markAllNotificationsRead() {
  return http.post<{ marked: number }>("/admin/notifications/read-all");
}

export async function deleteNotification(id: number) {
  await http.del<Record<string, never>>(`/admin/notifications/${id}`);
}

export async function deleteAllNotifications() {
  return http.del<{ deleted: number }>("/admin/notifications");
}

export function isCacheScopeWarnNotification(item: NotificationItem): boolean {
  if (item.category === NOTIFICATION_CATEGORY_CACHE_SCOPE_WARN && (item.ref_id ?? 0) > 0) {
    return true;
  }
  return item.category === "cache" && item.title.includes("范围过大") && (item.ref_id ?? 0) > 0;
}

export function isStrmScanWarnNotification(item: NotificationItem): boolean {
  return item.category === NOTIFICATION_CATEGORY_STRM_SCAN_WARN;
}

export function isStrmScrapeWarnNotification(item: NotificationItem): boolean {
  return item.category === NOTIFICATION_CATEGORY_STRM_SCRAPE_WARN;
}

function parseFailureDetails<T>(message: string): { summary: string; items: T[] } {
  const text = (message ?? "").trim();
  const idx = text.indexOf(STRM_SCAN_DETAIL_SEP);
  if (idx < 0) return { summary: text, items: [] };
  const summary = text.slice(0, idx).trim();
  const payload = text.slice(idx + STRM_SCAN_DETAIL_SEP.length).trim();
  if (!payload) return { summary, items: [] };
  try {
    const items = JSON.parse(payload) as T[];
    return { summary, items: Array.isArray(items) ? items : [] };
  } catch {
    return { summary, items: [] };
  }
}

export function parseStrmScanFailures(message: string): {
  summary: string;
  items: StrmScanFailureItem[];
} {
  return parseFailureDetails<StrmScanFailureItem>(message);
}

export function parseStrmScrapeFailures(message: string): {
  summary: string;
  items: StrmScrapeFailureItem[];
} {
  return parseFailureDetails<StrmScrapeFailureItem>(message);
}

export function strmScanFailureKindLabel(kind: string): string {
  if (kind === "metadata") return "元数据";
  return "STRM";
}

export function strmScrapeFailureStageLabel(stage: string): string {
  if (stage === "match") return "TMDB 匹配";
  if (stage === "write") return "元数据写入";
  return "刮削";
}
