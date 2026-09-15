import { reactive, readonly, ref, watch } from "vue";
import type { ConfirmDialogResult, ConfirmOptions, ConfirmState } from "@/types/confirm";

type Resolver = (value: ConfirmDialogResult) => void;
type Rejecter = (reason?: unknown) => void;

const defaultState = (): ConfirmState & {
  _resolve?: Resolver;
  _reject?: Rejecter;
} => ({
  open: false,
  loading: false,
  title: "",
  message: "",
  icon: "warning",
  confirmText: "确定",
  cancelText: "取消",
  danger: true,
  size: "md",
  hint: undefined,
  checkboxLabel: undefined,
  checkboxDefault: false,
  actions: undefined,
  showCancel: true,
  preset: undefined,
  presetData: undefined,
});

const state = reactive(defaultState());
export const confirmChecked = ref(false);

function reset() {
  Object.assign(state, defaultState());
  confirmChecked.value = false;
}

function close() {
  if (state.loading) return;
  state._reject?.(new Error("Modal closed"));
  reset();
}

function finish(result: ConfirmDialogResult) {
  state._resolve?.(result);
  reset();
}

function confirmAction() {
  finish({ action: "confirm", checked: confirmChecked.value });
}

function emitAction(actionId: string) {
  finish({ action: actionId, checked: confirmChecked.value });
}

function setLoading(loading: boolean) {
  state.loading = loading;
}

function applyOptions(options: ConfirmOptions) {
  Object.assign(state, {
    open: true,
    loading: false,
    title: options.title,
    message: options.message ?? "",
    icon: options.icon ?? "warning",
    confirmText: options.confirmText ?? "确定",
    cancelText: options.cancelText ?? "取消",
    danger: options.danger ?? true,
    size: options.size ?? "md",
    hint: options.hint,
    checkboxLabel: options.checkboxLabel,
    checkboxDefault: options.checkboxDefault ?? false,
    actions: options.actions,
    showCancel: options.showCancel ?? true,
    preset: options.preset,
    presetData: options.presetData,
  });
  confirmChecked.value = options.checkboxDefault ?? false;
}

export function showConfirm(options: ConfirmOptions): Promise<ConfirmDialogResult> {
  return new Promise((resolve, reject) => {
    applyOptions(options);
    state._resolve = resolve;
    state._reject = reject;
  });
}

export function confirm(options: ConfirmOptions): Promise<boolean> {
  return showConfirm(options).then(
    (result) => {
      if (result.action === "confirm") return true;
      throw new Error("cancelled");
    },
    (err) => Promise.reject(err),
  );
}

watch(
  () => state.open,
  (open) => {
    if (!open) confirmChecked.value = false;
  },
);

export function useConfirm() {
  return {
    state: readonly(state),
    confirmChecked,
    close,
    confirmAction,
    emitAction,
    setLoading,
    confirm,
    showConfirm,
  };
}

export { state as confirmState };
export { setLoading as setConfirmLoading };
