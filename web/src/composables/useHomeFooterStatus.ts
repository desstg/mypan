import { reactive, readonly } from "vue";

// 简约首页：FileBrowser 把性能与传输任务状态推送到此单例，
// 全局 AppFooter 读取并渲染（放在 footer 里），避免跨组件层层传 props。
// 非简约模式不使用该通道，FileToolbar 直接展示同源数据。

export interface HomeFooterStatus {
  responseTime: string;
  cacheRate: string;
  uploadTaskActive: boolean;
  uploadTaskFailed: boolean;
  uploadTaskSuccess: boolean;
  uploadTaskCount: number;
  uploadTaskLabel: string;
}

const status = reactive<HomeFooterStatus>({
  responseTime: "",
  cacheRate: "",
  uploadTaskActive: false,
  uploadTaskFailed: false,
  uploadTaskSuccess: false,
  uploadTaskCount: 0,
  uploadTaskLabel: "",
});

// 打开上传任务面板的入口由 FileBrowser 注册（任务面板在 FileBrowser 内渲染）。
let openTaskPanelFn: (() => void) | null = null;

export function useHomeFooterStatus() {
  function update(next: Partial<HomeFooterStatus>): void {
    Object.assign(status, next);
  }

  function onOpenTaskPanel(fn: (() => void) | null): void {
    openTaskPanelFn = fn;
  }

  function openTaskPanel(): void {
    openTaskPanelFn?.();
  }

  return {
    status: readonly(status),
    update,
    onOpenTaskPanel,
    openTaskPanel,
  };
}
