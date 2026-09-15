import { ref } from "vue";
import {
  fetchAnnouncement,
  markAnnouncementRead,
  type AnnouncementItem,
  type AnnouncementResponse,
} from "@/api/announcement";

// 模块级单例：公告状态（打开后台自动检查 + 侧边栏「关于」手动查看共用）。
const open = ref(false);
const item = ref<AnnouncementItem | null>(null);

async function load(): Promise<AnnouncementResponse | null> {
  try {
    const res = await fetchAnnouncement();
    const it = res.item;
    if (!res.enabled || !it || !it.notice_version) return null;
    return res;
  } catch {
    return null;
  }
}

export function useAnnouncement() {
  // 打开后台时检查：当前公告尚未在服务端标记为已读才弹出。
  async function check(): Promise<void> {
    const res = await load();
    if (!res || res.read || !res.item) return;
    item.value = res.item;
    open.value = true;
  }

  // 手动查看（点「关于」）：拉到公告即无条件弹出，不改变已读状态。
  async function forceOpen(): Promise<boolean> {
    const res = await load();
    if (!res?.item) return false;
    item.value = res.item;
    open.value = true;
    return true;
  }

  // 关闭后将当前公告版本写入服务端；失败时静默降级，不影响后台使用。
  function dismiss(): void {
    const version = item.value?.notice_version;
    open.value = false;
    if (version) void markAnnouncementRead(version).catch(() => {});
  }

  function close(): void {
    open.value = false;
  }

  return { open, item, check, forceOpen, dismiss, close };
}
