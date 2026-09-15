import { reactive, readonly } from "vue";

export type ToastKind = "success" | "error" | "info" | "warning";

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

const toasts = reactive<Toast[]>([]);
let seq = 0;

function push(kind: ToastKind, message: string, duration = 2000): void {
  const id = ++seq;
  toasts.push({ id, kind, message });
  window.setTimeout(() => remove(id), duration);
}

function remove(id: number): void {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

// 全局单例 toast，组件内 import 即用。
export const toast = {
  success: (m: string) => push("success", m),
  error: (m: string) => push("error", m, 3500),
  info: (m: string) => push("info", m),
  warning: (m: string) => push("warning", m),
};

interface CopyTextOptions {
  successMessage?: string;
  errorMessage?: string;
}

export async function copyTextToClipboard(text: string, opts: CopyTextOptions = {}): Promise<boolean> {
  if (!text) return false;
  const successMessage = opts.successMessage ?? "已复制";
  const errorMessage = opts.errorMessage ?? "复制失败";
  try {
    await navigator.clipboard.writeText(text);
    toast.success(successMessage);
    return true;
  } catch {
    try {
      const el = document.createElement("textarea");
      el.value = text;
      el.style.position = "fixed";
      el.style.left = "-9999px";
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      toast.success(successMessage);
      return true;
    } catch {
      toast.error(errorMessage);
      return false;
    }
  }
}

export function useToasts() {
  return { toasts: readonly(toasts), remove };
}
