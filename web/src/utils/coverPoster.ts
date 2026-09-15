import "@fontsource-variable/noto-serif-sc/wght.css";

export const COVER_POSTER_WIDTH = 1000;
export const COVER_POSTER_HEIGHT = 1500;

const posterFont = '"Noto Serif SC Variable", "Songti SC", "STSong", serif';
const imageCache = new Map<string, Promise<HTMLImageElement>>();

export interface CoverPosterOptions {
  imageURL: string;
  title: string;
  packaged: boolean;
  focus?: CoverPosterFocus;
  panelColor?: string;
  panelOpacity?: number;
  textColor?: string;
  panelShape?: "slant" | "straight";
  panelHeight?: number;
  imageZoom?: number;
}

export interface CoverPosterFocus {
  x: number;
  y: number;
}

function loadImage(src: string): Promise<HTMLImageElement> {
  const cached = imageCache.get(src);
  if (cached) return cached;
  const pending = new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    image.decoding = "async";
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error("候选画面加载失败"));
    image.src = src;
  });
  imageCache.set(src, pending);
  pending.catch(() => imageCache.delete(src));
  return pending;
}

async function loadPosterFont(title: string): Promise<void> {
  if (!("fonts" in document)) return;
  const sample = title.trim() || "海报片名";
  await document.fonts.load(`800 96px ${posterFont}`, sample);
}

function resetContext(ctx: CanvasRenderingContext2D) {
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.globalAlpha = 1;
  ctx.globalCompositeOperation = "source-over";
  ctx.filter = "none";
  ctx.shadowBlur = 0;
  ctx.shadowColor = "transparent";
  ctx.shadowOffsetX = 0;
  ctx.shadowOffsetY = 0;
  ctx.textAlign = "start";
  ctx.textBaseline = "alphabetic";
}

function rgba(hex: string | undefined, alpha: number) {
  const normalized = (hex ?? "#000000").replace("#", "");
  const value = /^[0-9a-f]{6}$/i.test(normalized) ? normalized : "000000";
  const red = Number.parseInt(value.slice(0, 2), 16);
  const green = Number.parseInt(value.slice(2, 4), 16);
  const blue = Number.parseInt(value.slice(4, 6), 16);
  return `rgba(${red},${green},${blue},${Math.min(1, Math.max(0, alpha))})`;
}

function normalizeFocus(focus?: CoverPosterFocus): CoverPosterFocus {
  return {
    x: Math.min(1, Math.max(0, focus?.x ?? 0.5)),
    y: Math.min(1, Math.max(0, focus?.y ?? 0.5)),
  };
}

function drawCover(ctx: CanvasRenderingContext2D, image: HTMLImageElement, focus?: CoverPosterFocus, bleed = 0, zoom = 1) {
  drawCoverInRect(
    ctx,
    image,
    -bleed,
    -bleed,
    COVER_POSTER_WIDTH + bleed * 2,
    COVER_POSTER_HEIGHT + bleed * 2,
    focus,
    zoom,
  );
}

function drawCoverInRect(
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  x: number,
  y: number,
  width: number,
  height: number,
  focus?: CoverPosterFocus,
  zoom = 1,
) {
  // 可拖动画面预留少量裁切余量，否则比例刚好贴合的一条轴没有移动空间。
  const scale = Math.max(width / image.naturalWidth, height / image.naturalHeight) * (focus ? 1.06 : 1) * Math.min(1.5, Math.max(1, zoom));
  const drawWidth = image.naturalWidth * scale;
  const drawHeight = image.naturalHeight * scale;
  const normalized = normalizeFocus(focus);
  const drawX = x - Math.max(0, drawWidth - width) * normalized.x;
  const drawY = y - Math.max(0, drawHeight - height) * normalized.y;
  ctx.save();
  ctx.beginPath();
  ctx.rect(x, y, width, height);
  ctx.clip();
  ctx.drawImage(
    image,
    drawX,
    drawY,
    drawWidth,
    drawHeight,
  );
  ctx.restore();
}

function drawVignette(ctx: CanvasRenderingContext2D) {
  const vignette = ctx.createRadialGradient(500, 650, 280, 500, 700, 900);
  vignette.addColorStop(0, "rgba(0,0,0,0)");
  vignette.addColorStop(0.72, "rgba(0,0,0,.08)");
  vignette.addColorStop(1, "rgba(0,0,0,.58)");
  ctx.fillStyle = vignette;
  ctx.fillRect(0, 0, COVER_POSTER_WIDTH, COVER_POSTER_HEIGHT);
}

function wrapTitle(
  ctx: CanvasRenderingContext2D,
  title: string,
  maxWidth: number,
  maxLines: number,
  initialSize: number,
  minSize: number,
) {
  const characters = Array.from(title.trim() || "未命名作品");
  for (let size = initialSize; size >= minSize; size -= 4) {
    ctx.font = `800 ${size}px ${posterFont}`;
    const lines: string[] = [];
    let current = "";
    for (const character of characters) {
      const next = current + character;
      if (current && ctx.measureText(next).width > maxWidth) {
        lines.push(current.trim());
        current = character;
      } else {
        current = next;
      }
    }
    if (current.trim()) lines.push(current.trim());
    if (lines.length <= maxLines) return { lines, size };
  }

  ctx.font = `800 ${minSize}px ${posterFont}`;
  const lines: string[] = [];
  let current = "";
  for (const character of characters) {
    const next = current + character;
    if (current && ctx.measureText(next).width > maxWidth) {
      lines.push(current.trim());
      current = character;
    } else {
      current = next;
    }
  }
  if (current.trim()) lines.push(current.trim());
  const visible = lines.slice(0, maxLines);
  if (lines.length > maxLines && visible.length) {
    let last = visible[visible.length - 1];
    while (last && ctx.measureText(`${last}…`).width > maxWidth) last = last.slice(0, -1);
    visible[visible.length - 1] = `${last}…`;
  }
  return { lines: visible, size: minSize };
}

function drawTitle(
  ctx: CanvasRenderingContext2D,
  title: string,
  centerY: number,
  maxWidth: number,
  maxLines: number,
  initialSize: number,
  minSize: number,
  color = "#fffdf8",
) {
  const { lines, size } = wrapTitle(ctx, title, maxWidth, maxLines, initialSize, minSize);
  const lineHeight = size * 1.28;
  const top = centerY - ((lines.length - 1) * lineHeight) / 2;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.lineJoin = "round";
  ctx.miterLimit = 2;
  ctx.shadowColor = "rgba(0,0,0,.72)";
  ctx.shadowBlur = 22;
  ctx.shadowOffsetY = 7;
  ctx.strokeStyle = "rgba(0,0,0,.28)";
  ctx.lineWidth = Math.max(2, size * 0.045);
  ctx.fillStyle = color;
  lines.forEach((line, index) => {
    const y = top + index * lineHeight;
    ctx.strokeText(line, COVER_POSTER_WIDTH / 2, y, maxWidth);
    ctx.fillText(line, COVER_POSTER_WIDTH / 2, y, maxWidth);
  });
  ctx.shadowBlur = 0;
  ctx.shadowOffsetY = 0;
}

function drawTitlePanel(
  ctx: CanvasRenderingContext2D,
  title: string,
  panelColor: string | undefined,
  panelOpacity: number,
  textColor: string,
  panelShape: "slant" | "straight",
  panelHeight: number,
) {
  const height = COVER_POSTER_HEIGHT * Math.min(0.3, Math.max(0.15, panelHeight));
  const centerTop = COVER_POSTER_HEIGHT - height;
  const slope = panelShape === "slant" ? 50 : 0;
  const leftTop = centerTop + slope;
  const rightTop = centerTop - slope;

  ctx.beginPath();
  ctx.moveTo(0, leftTop);
  ctx.lineTo(COVER_POSTER_WIDTH, rightTop);
  ctx.lineTo(COVER_POSTER_WIDTH, COVER_POSTER_HEIGHT);
  ctx.lineTo(0, COVER_POSTER_HEIGHT);
  ctx.closePath();
  const panel = ctx.createLinearGradient(0, Math.min(leftTop, rightTop), 0, COVER_POSTER_HEIGHT);
  panel.addColorStop(0, rgba(panelColor, panelOpacity * 0.72));
  panel.addColorStop(1, rgba(panelColor, panelOpacity));
  ctx.fillStyle = panel;
  ctx.fill();

  const initialSize = Math.min(100, Math.max(68, height * 0.3));
  const minSize = Math.min(64, initialSize);
  drawTitle(ctx, title, centerTop + height * 0.58, 820, 2, initialSize, minSize, textColor);
}

function drawPortraitPackage(
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  title: string,
  focus: CoverPosterFocus | undefined,
  panelColor: string | undefined,
  panelOpacity: number,
  textColor: string,
  panelShape: "slant" | "straight",
  panelHeight: number,
  imageZoom: number,
) {
  ctx.filter = "contrast(1.04) saturate(1.04)";
  drawCover(ctx, image, focus, 0, imageZoom);
  ctx.filter = "none";
  drawVignette(ctx);

  const topShade = ctx.createLinearGradient(0, 0, 0, 430);
  topShade.addColorStop(0, "rgba(4,5,10,.34)");
  topShade.addColorStop(1, "rgba(4,5,10,0)");
  ctx.fillStyle = topShade;
  ctx.fillRect(0, 0, COVER_POSTER_WIDTH, 430);

  const bottomShade = ctx.createLinearGradient(0, 690, 0, COVER_POSTER_HEIGHT);
  bottomShade.addColorStop(0, "rgba(5,6,10,0)");
  bottomShade.addColorStop(0.58, "rgba(8,16,28,.38)");
  bottomShade.addColorStop(1, "rgba(10,18,31,.78)");
  ctx.fillStyle = bottomShade;
  ctx.fillRect(0, 690, COVER_POSTER_WIDTH, 810);

  drawTitlePanel(ctx, title, panelColor, panelOpacity, textColor, panelShape, panelHeight);
}

function drawLandscapePackage(
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  title: string,
  focus: CoverPosterFocus | undefined,
  panelColor: string | undefined,
  panelOpacity: number,
  textColor: string,
  panelShape: "slant" | "straight",
  panelHeight: number,
  imageZoom: number,
) {
  ctx.save();
  ctx.filter = "blur(42px) brightness(.76) saturate(1.08)";
  drawCover(ctx, image, focus, 90, imageZoom);
  ctx.restore();
  ctx.fillStyle = "rgba(8,14,24,.10)";
  ctx.fillRect(0, 0, COVER_POSTER_WIDTH, COVER_POSTER_HEIGHT);

  // 前景画面与包装形状是两层独立内容：固定延伸至海报底部附近，
  // 不再跟随形状高度或透明度改变，避免透明形状仍留下同高色块。
  const imageHeight = 1400;
  const foreground = document.createElement("canvas");
  foreground.width = COVER_POSTER_WIDTH;
  foreground.height = imageHeight;
  const foregroundContext = foreground.getContext("2d");
  if (!foregroundContext) throw new Error("当前浏览器不支持海报合成");
  foregroundContext.imageSmoothingEnabled = true;
  foregroundContext.imageSmoothingQuality = "high";
  foregroundContext.filter = "contrast(1.03) saturate(1.04)";
  drawCoverInRect(foregroundContext, image, 0, 0, COVER_POSTER_WIDTH, imageHeight, focus, imageZoom);
  foregroundContext.filter = "none";
  const fadeStart = 1100;
  foregroundContext.globalCompositeOperation = "destination-out";
  const imageFade = foregroundContext.createLinearGradient(0, fadeStart, 0, imageHeight);
  imageFade.addColorStop(0, "rgba(0,0,0,0)");
  imageFade.addColorStop(1, "rgba(0,0,0,1)");
  foregroundContext.fillStyle = imageFade;
  foregroundContext.fillRect(0, fadeStart, COVER_POSTER_WIDTH, imageHeight - fadeStart);
  foregroundContext.globalCompositeOperation = "source-over";
  ctx.drawImage(foreground, 0, 0);

  drawTitlePanel(ctx, title, panelColor, panelOpacity, textColor, panelShape, panelHeight);
}

export async function createCoverPoster(options: CoverPosterOptions): Promise<HTMLCanvasElement> {
  const [image] = await Promise.all([loadImage(options.imageURL), loadPosterFont(options.title)]);
  const canvas = document.createElement("canvas");
  canvas.width = COVER_POSTER_WIDTH;
  canvas.height = COVER_POSTER_HEIGHT;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("当前浏览器不支持海报合成");
  resetContext(ctx);
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  ctx.fillStyle = "#08090e";
  ctx.fillRect(0, 0, COVER_POSTER_WIDTH, COVER_POSTER_HEIGHT);
  const panelOpacity = Math.min(1, Math.max(0, options.panelOpacity ?? 0.8));
  const textColor = options.textColor ?? "#fffdf8";
  const panelShape = options.panelShape ?? "slant";
  const panelHeight = Math.min(0.3, Math.max(0.15, options.panelHeight ?? 0.22));
  const imageZoom = Math.min(1.5, Math.max(1, options.imageZoom ?? 1));

  if (!options.packaged) {
    drawCover(ctx, image, options.focus, 0, imageZoom);
  } else if (image.naturalWidth / image.naturalHeight >= 1.15) {
    drawLandscapePackage(ctx, image, options.title, options.focus, options.panelColor, panelOpacity, textColor, panelShape, panelHeight, imageZoom);
  } else {
    drawPortraitPackage(ctx, image, options.title, options.focus, options.panelColor, panelOpacity, textColor, panelShape, panelHeight, imageZoom);
  }
  resetContext(ctx);
  return canvas;
}

export function canvasToJPEG(canvas: HTMLCanvasElement, quality = 0.92): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("海报编码失败"));
    }, "image/jpeg", quality);
  });
}
