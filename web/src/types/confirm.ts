export type ConfirmIcon = "warning" | "trash" | "error" | "question" | "info";

export type ConfirmSize = "sm" | "md";

export type ConfirmPreset = "upload-notice" | "upload-conflict" | "cross-transfer-probe-notice";

export type ConfirmActionVariant = "cancel" | "primary" | "danger";

export interface ConfirmAction {
  id: string;
  label: string;
  variant?: ConfirmActionVariant;
}

export interface ConfirmDialogResult {
  action: string;
  checked: boolean;
}

export interface ConfirmOptions {
  title: string;
  message?: string;
  icon?: ConfirmIcon;
  confirmText?: string;
  cancelText?: string;
  /** 确认按钮是否使用危险样式，默认 true */
  danger?: boolean;
  /** sm=450px，md=560px */
  size?: ConfirmSize;
  /** 正文下方补充说明 */
  hint?: string;
  /** 显示勾选项及文案 */
  checkboxLabel?: string;
  checkboxDefault?: boolean;
  /** 自定义底部按钮；未设置时使用「取消 + 确认」 */
  actions?: ConfirmAction[];
  /** 是否显示默认取消按钮，默认 true */
  showCancel?: boolean;
  /** 内置正文模板 */
  preset?: ConfirmPreset;
  presetData?: Record<string, unknown>;
}

export interface ConfirmState extends ConfirmOptions {
  open: boolean;
  loading: boolean;
}

export const CONFIRM_ICON_SVG: Record<ConfirmIcon, string> = {
  warning: "notify-warning",
  trash: "confirm-trash",
  error: "notify-error",
  question: "notify-info",
  info: "notify-info",
};

export const CONFIRM_ICON_TONE: Record<ConfirmIcon, "warning" | "danger" | "info"> = {
  warning: "warning",
  trash: "danger",
  error: "danger",
  question: "info",
  info: "info",
};
