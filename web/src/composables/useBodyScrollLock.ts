import { onMounted, onUnmounted } from "vue";
import { lockPageScroll, unlockPageScroll } from "@/utils/scrollLock";

/** 全屏预览打开时锁定后台页面滚动，关闭后恢复原状态。 */
export function useBodyScrollLock() {
  onMounted(() => {
    lockPageScroll();
  });

  onUnmounted(() => {
    unlockPageScroll();
  });
}
