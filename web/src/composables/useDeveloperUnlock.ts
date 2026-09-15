import { ref } from "vue";
import { devApi } from "@/api/dev";

// 模块级单例：解锁状态与服务端内存同步，重启后自动归零。
const unlocked = ref(false);
const loaded = ref(false);

export function useDeveloperUnlock() {
  async function init() {
    if (loaded.value) return;
    loaded.value = true;
    try {
      const res = await devApi.state();
      unlocked.value = Boolean(res?.unlocked);
    } catch {
      // 拉取失败保持默认隐藏，不阻塞业务。
    }
  }

  async function unlock(code: string): Promise<boolean> {
    const res = await devApi.unlock(code);
    if (!res?.unlocked) return false;
    unlocked.value = true;
    return true;
  }
  return { unlocked, init, unlock };
}
