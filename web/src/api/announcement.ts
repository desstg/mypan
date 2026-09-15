import { http } from "./client";

export interface AnnouncementSection {
  title: string;
  body: string;
}

export interface AnnouncementItem {
  /** 判重版本：日期字符串（如 2026-08-20）或内容哈希 */
  notice_version: string;
  badge: string;
  dialog_title: string;
  /** 黄色警示区（纯文字） */
  banner: string;
  /** 特别说明区：banner 之下、正文之上，仅展示纯文本 */
  special: string;
  lead: string;
  issues: AnnouncementSection[];
  footnote: string;
  fetched_at: string;
}

export interface AnnouncementResponse {
  enabled: boolean;
  item: AnnouncementItem | null;
  /** 当前公告版本是否已在服务端标记为已读 */
  read: boolean;
}

// 后台公告：enabled=false 或 item 为 null 时不弹窗。
export async function fetchAnnouncement() {
  return http.get<AnnouncementResponse>("/admin/announcement");
}

export async function markAnnouncementRead(noticeVersion: string): Promise<void> {
  await http.post<{ notice_version: string }>("/admin/announcement/read", {
    notice_version: noticeVersion,
  });
}
