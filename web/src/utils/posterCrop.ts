// 海报裁剪的纯换算。抽出来是为了让「屏幕上拖的框」与「原图像素」之间的换算
// 只有一处实现 —— 这类算错的表现是「保存出来的图和看到的框对不上」，
// 而且只在某些缩放下才出现（最难查）。

import type { JavCropRect } from "@/api/strmJavWall";

/** 海报比例 2:3（选框锁定这个比例，自由比例会被 Emby letterbox）。 */
export const POSTER_ASPECT = 2 / 3;

/** 把窗口夹进图片边界内（宽高至少 1 像素）。与服务端的 ClampRect 同一套语义。 */
export function clampRect(rect: JavCropRect, width: number, height: number): JavCropRect {
  const w = Math.max(1, Math.min(Math.round(rect.w), width));
  const h = Math.max(1, Math.min(Math.round(rect.h), height));
  const x = Math.max(0, Math.min(Math.round(rect.x), width - w));
  const y = Math.max(0, Math.min(Math.round(rect.y), height - h));
  return { x, y, w, h };
}

/**
 * 按 2:3 锁比例、并夹进图片里地移动窗口。
 *
 * 高度吃满（与生成器的横切一致），所以只有横向的自由度；拖动时只改 x。
 * 图片比 2:3 更窄时（竖版封面）改成宽度吃满、纵向滑动 —— 与服务端
 * `PosterCropWindow` 的两种取窗方式保持一致。
 */
export function moveRect(rect: JavCropRect, dx: number, dy: number, width: number, height: number): JavCropRect {
  if (width / height > POSTER_ASPECT) {
    return clampRect({ ...rect, x: rect.x + dx }, width, height);
  }
  return clampRect({ ...rect, y: rect.y + dy }, width, height);
}

/**
 * 缩放窗口：按 2:3 锁比例，**右边固定、向左扩**（生成器的默认窗口就是贴右的，
 * 用户「往左扩一点」是最自然的操作）。
 */
export function resizeRect(rect: JavCropRect, deltaW: number, width: number, height: number): JavCropRect {
  const right = rect.x + rect.w;
  let w = Math.max(40, rect.w + deltaW);
  let h = Math.round(w / POSTER_ASPECT);
  if (h > height) {
    h = height;
    w = Math.round(h * POSTER_ASPECT);
  }
  w = Math.min(w, width);
  const x = Math.max(0, right - w);
  return clampRect({ x, y: rect.y, w, h }, width, height);
}

/** 原图像素 → 屏幕像素（按显示宽度缩放）。 */
export function nativeToDisplay(rect: JavCropRect, displayWidth: number, nativeWidth: number): JavCropRect {
  const scale = nativeWidth > 0 ? displayWidth / nativeWidth : 1;
  return {
    x: rect.x * scale,
    y: rect.y * scale,
    w: rect.w * scale,
    h: rect.h * scale,
  };
}

/**
 * 屏幕像素 → 原图像素（拖动之后换算回原生尺寸再发给服务端）。
 *
 * **必须把非有限值兜成 0**：`JSON.stringify(NaN)` 会变成 `null`，而后端那几个字段是
 * `int` —— 解不动就整条请求报「请求体解析失败」（用户实测撞见过）。NaN 的来源是
 * 「框还没量到尺寸就点了保存」（displayWidth 为 0 或 rect 还没初始化）。
 */
export function displayToNative(rect: JavCropRect, displayWidth: number, nativeWidth: number): JavCropRect {
  const scale = displayWidth > 0 && nativeWidth > 0 ? nativeWidth / displayWidth : 1;
  return {
    x: finite(rect.x * scale),
    y: finite(rect.y * scale),
    w: finite(rect.w * scale),
    h: finite(rect.h * scale),
  };
}

function finite(value: number): number {
  return Number.isFinite(value) ? Math.round(value) : 0;
}
