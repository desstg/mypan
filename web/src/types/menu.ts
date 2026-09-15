export type DropdownMenuItem = {
  key: string;
  label: string;
  icon?: string;
  danger?: boolean;
  disabled?: boolean;
  type?: "action" | "divider" | "hint";
};

/** 下拉菜单相对触发器的水平对齐：左 / 中 / 右 */
export type DropdownAlign = "left" | "center" | "right";

export type DropdownPlacement = "bottom-start" | "bottom-center" | "bottom-end" | "top-center" | "top-end";

export function alignToPlacement(align: DropdownAlign): DropdownPlacement {
  if (align === "left") return "bottom-start";
  if (align === "right") return "bottom-end";
  return "bottom-center";
}
