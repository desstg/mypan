import type { DropdownPlacement } from "@/types/menu";

type PositionStyle = Record<string, string>;

export function computeDropdownPosition(
  rect: DOMRect,
  opts: { placement: DropdownPlacement; minWidth: number; gap?: number; menuHeight?: number },
): PositionStyle {
  const gap = opts.gap ?? 6;
  const menuW = opts.minWidth;
  const menuH = opts.menuHeight ?? 180;
  let left = rect.left;
  let top = rect.bottom + gap;
  let transform = "none";

  if (opts.placement === "bottom-center" || opts.placement === "top-center") {
    left = rect.left + rect.width / 2 - menuW / 2;
  } else if (opts.placement === "bottom-end" || opts.placement === "top-end") {
    left = rect.right - menuW;
  }

  if (opts.placement === "top-center" || opts.placement === "top-end") {
    top = rect.top - gap;
    transform = "translateY(-100%)";
  }

  if (left < 8) left = 8;
  if (left + menuW > window.innerWidth - 8) left = window.innerWidth - menuW - 8;

  if (transform === "none" && top + menuH > window.innerHeight - 8) {
    top = rect.top - gap;
    transform = "translateY(-100%)";
  }

  return {
    left: `${left}px`,
    top: `${top}px`,
    transform,
    minWidth: `${menuW}px`,
  };
}
