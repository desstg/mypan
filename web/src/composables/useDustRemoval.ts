import { nextTick } from "vue";

interface DustSnapshot {
  bitmap: HTMLCanvasElement;
  rect: DOMRect;
  scale: number;
}

interface DustParticle {
  x: number;
  y: number;
  red: number;
  green: number;
  blue: number;
  alpha: number;
  size: number;
  driftX: number;
  driftY: number;
  delay: number;
  duration: number;
  phase: number;
  wobble: number;
}

interface RemoveWithDustOptions {
  target: HTMLElement | null | undefined;
  container?: HTMLElement | null;
  remove: () => Promise<boolean | void>;
}

const DUST_KEY_SELECTOR = "[data-dust-key]";

function isVisibleElement(element: HTMLElement): boolean {
  const rect = element.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1 || element.getClientRects().length === 0) return false;
  const style = getComputedStyle(element);
  return style.display !== "none" && style.visibility !== "hidden";
}

function motionReduced(): boolean {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function blobToDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error("图片转换失败"));
    reader.readAsDataURL(blob);
  });
}

async function inlineImages(source: HTMLElement, clone: HTMLElement): Promise<void> {
  const sourceImages = Array.from(source.querySelectorAll("img"));
  const cloneImages = Array.from(clone.querySelectorAll("img"));
  await Promise.all(
    sourceImages.map(async (sourceImage, index) => {
      const cloneImage = cloneImages[index];
      if (!cloneImage) return;
      cloneImage.removeAttribute("srcset");
      const sourceURL = sourceImage.currentSrc || sourceImage.src;
      if (!sourceURL || sourceURL.startsWith("data:")) return;
      try {
        const response = await fetch(sourceURL, { credentials: "same-origin", cache: "force-cache" });
        if (!response.ok) throw new Error(`图片读取失败：${response.status}`);
        cloneImage.src = await blobToDataURL(await response.blob());
      } catch {
        cloneImage.removeAttribute("src");
        cloneImage.style.background = getComputedStyle(sourceImage).backgroundColor || "transparent";
      }
    }),
  );
}

function inlineComputedStyle(source: Element, clone: Element): void {
  if (!(clone instanceof HTMLElement) && !(clone instanceof SVGElement)) return;
  const computed = getComputedStyle(source);
  for (let propertyIndex = 0; propertyIndex < computed.length; propertyIndex += 1) {
    const property = computed.item(propertyIndex);
    const value = computed.getPropertyValue(property);
    if (value.includes("url(") && !value.includes("data:")) continue;
    clone.style.setProperty(property, value, computed.getPropertyPriority(property));
  }
  clone.style.setProperty("animation", "none", "important");
  clone.style.setProperty("transition", "none", "important");
  clone.style.setProperty("caret-color", "transparent", "important");
}

function inlineComputedStyles(source: HTMLElement, clone: HTMLElement): void {
  const sourceElements: Element[] = [source, ...Array.from(source.querySelectorAll("*"))];
  const cloneElements: Element[] = [clone, ...Array.from(clone.querySelectorAll("*"))];
  sourceElements.forEach((sourceElement, index) => {
    const cloneElement = cloneElements[index];
    if (cloneElement) inlineComputedStyle(sourceElement, cloneElement);
  });
}

function isOpaqueBackground(value: string): boolean {
  const color = value.trim().toLowerCase();
  if (!color || color === "transparent") return false;
  const legacyAlpha = color.match(/^rgba\([^)]*,\s*([\d.]+)\s*\)$/);
  if (legacyAlpha) return Number(legacyAlpha[1]) >= 0.99;
  const modernAlpha = color.match(/\/\s*([\d.]+)(%)?\s*\)$/);
  if (!modernAlpha) return true;
  const alpha = Number(modernAlpha[1]);
  return modernAlpha[2] ? alpha >= 99 : alpha >= 0.99;
}

function findOpaqueBackground(target: HTMLElement): string {
  let current: HTMLElement | null = target;
  while (current) {
    const background = getComputedStyle(current).backgroundColor;
    if (isOpaqueBackground(background)) return background;
    current = current.parentElement;
  }
  return "rgb(255, 255, 255)";
}

function wrapTableRowForRaster(
  source: HTMLTableRowElement,
  rowClone: HTMLTableRowElement,
  rect: DOMRect,
): HTMLTableElement {
  const sourceTable = source.closest("table");
  const table = document.createElement("table");
  if (sourceTable) inlineComputedStyle(sourceTable, table);
  table.style.setProperty("width", `${rect.width}px`, "important");
  table.style.setProperty("height", `${rect.height}px`, "important");
  table.style.setProperty("margin", "0", "important");
  table.style.setProperty("border", "0", "important");
  table.style.setProperty("border-radius", "0", "important");
  table.style.setProperty("box-shadow", "none", "important");
  table.style.setProperty("table-layout", "fixed", "important");
  table.style.setProperty("background-color", findOpaqueBackground(source), "important");

  const colgroup = document.createElement("colgroup");
  Array.from(source.cells).forEach((cell) => {
    const col = document.createElement("col");
    col.style.setProperty("width", `${cell.getBoundingClientRect().width}px`, "important");
    colgroup.appendChild(col);
  });

  const sourceSection = source.parentElement;
  const sectionTag = sourceSection?.tagName === "THEAD" ? "thead" : "tbody";
  const section = document.createElement(sectionTag);
  if (sourceSection) inlineComputedStyle(sourceSection, section);
  section.appendChild(rowClone);
  table.append(colgroup, section);
  return table;
}

async function rasterizeElement(target: HTMLElement): Promise<DustSnapshot> {
  await document.fonts.ready;
  const rect = target.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) throw new Error("移除对象没有可见尺寸");

  const scale = Math.min(window.devicePixelRatio || 1, 2);
  const clone = target.cloneNode(true) as HTMLElement;
  inlineComputedStyles(target, clone);
  await inlineImages(target, clone);
  Object.assign(clone.style, {
    width: `${rect.width}px`,
    height: `${rect.height}px`,
    margin: "0",
    transform: "none",
  });
  clone.removeAttribute("data-dust-key");

  const rasterContent = target instanceof HTMLTableRowElement
    ? wrapTableRowForRaster(target, clone as HTMLTableRowElement, rect)
    : clone;
  const markup = new XMLSerializer().serializeToString(rasterContent);
  const svg = `
    <svg xmlns="http://www.w3.org/2000/svg" width="${rect.width}" height="${rect.height}">
      <foreignObject width="100%" height="100%">
        <div xmlns="http://www.w3.org/1999/xhtml" style="width:${rect.width}px;height:${rect.height}px;overflow:hidden">
          ${markup}
        </div>
      </foreignObject>
    </svg>`;
  const imageURL = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  const image = new Image();
  await new Promise<void>((resolve, reject) => {
    image.onload = () => resolve();
    image.onerror = () => reject(new Error("移除对象栅格化失败"));
    image.src = imageURL;
  });
  const bitmap = document.createElement("canvas");
  bitmap.width = Math.max(1, Math.round(rect.width * scale));
  bitmap.height = Math.max(1, Math.round(rect.height * scale));
  const context = bitmap.getContext("2d", { willReadFrequently: true });
  if (!context) throw new Error("浏览器不支持 Canvas 2D");
  context.drawImage(image, 0, 0, bitmap.width, bitmap.height);
  context.getImageData(0, 0, 1, 1);
  return { bitmap, rect, scale };
}

function buildParticles(snapshot: DustSnapshot, padding: number): DustParticle[] {
  const context = snapshot.bitmap.getContext("2d", { willReadFrequently: true });
  if (!context) return [];
  const { width, height } = snapshot.bitmap;
  const pixels = context.getImageData(0, 0, width, height).data;
  const sampleSize = window.innerWidth < 640 ? 4 : 3;
  const sampleStep = Math.max(2, Math.round(sampleSize * snapshot.scale));
  const particles: DustParticle[] = [];

  for (let pixelY = Math.floor(sampleStep / 2); pixelY < height; pixelY += sampleStep) {
    for (let pixelX = Math.floor(sampleStep / 2); pixelX < width; pixelX += sampleStep) {
      const index = (pixelY * width + pixelX) * 4;
      const sourceAlpha = pixels[index + 3] / 255;
      if (sourceAlpha < 0.08) continue;

      let red = pixels[index];
      let green = pixels[index + 1];
      let blue = pixels[index + 2];
      let alpha = sourceAlpha;
      if ((red + green + blue) / 3 > 246) {
        red = 228;
        green = 235;
        blue = 244;
        alpha *= 0.68;
      }
      const drift = 12 + Math.random() * 29;
      particles.push({
        x: padding + pixelX / snapshot.scale,
        y: padding + pixelY / snapshot.scale,
        red,
        green,
        blue,
        alpha,
        size: sampleSize * (0.78 + Math.random() * 0.34),
        driftX: -drift * (0.76 + Math.random() * 0.34),
        driftY: -drift * (0.42 + Math.random() * 0.35),
        delay: Math.random() * 85,
        duration: 510 + Math.random() * 190,
        phase: Math.random() * Math.PI * 2,
        wobble: 0.8 + Math.random() * 2.4,
      });
    }
  }
  return particles;
}

function playDust(snapshot: DustSnapshot): Promise<void> {
  const padding = 64;
  const particles = buildParticles(snapshot, padding);
  if (!particles.length) return Promise.resolve();

  const displayScale = Math.min(window.devicePixelRatio || 1, 2);
  const width = snapshot.rect.width + padding * 2;
  const height = snapshot.rect.height + padding * 2;
  const canvas = document.createElement("canvas");
  canvas.className = "lp-dust-removal-canvas";
  canvas.width = Math.max(1, Math.round(width * displayScale));
  canvas.height = Math.max(1, Math.round(height * displayScale));
  Object.assign(canvas.style, {
    left: `${snapshot.rect.left - padding}px`,
    top: `${snapshot.rect.top - padding}px`,
    width: `${width}px`,
    height: `${height}px`,
  });
  document.body.appendChild(canvas);
  const context = canvas.getContext("2d");
  if (!context) {
    canvas.remove();
    return Promise.resolve();
  }
  context.scale(displayScale, displayScale);
  const startedAt = performance.now();

  return new Promise((resolve) => {
    const drawFrame = (timestamp: number) => {
      context.clearRect(0, 0, width, height);
      let active = false;
      for (const particle of particles) {
        const elapsed = timestamp - startedAt - particle.delay;
        const progress = Math.max(0, Math.min(1, elapsed / particle.duration));
        if (progress < 1) active = true;
        const eased = 1 - Math.pow(1 - progress, 2.35);
        const fadeProgress = Math.max(0, (progress - 0.07) / 0.93);
        const alpha = particle.alpha * (1 - Math.pow(fadeProgress, 1.18));
        if (alpha <= 0.01) continue;

        const wobble = Math.sin(progress * 11 + particle.phase) * particle.wobble * eased;
        const x = particle.x + particle.driftX * eased + wobble;
        const y = particle.y + particle.driftY * eased - wobble * 0.35;
        const size = particle.size * (1 - progress * 0.68);
        context.globalAlpha = alpha;
        context.fillStyle = `rgb(${particle.red}, ${particle.green}, ${particle.blue})`;
        context.fillRect(x - size / 2, y - size / 2, size, size);
      }
      context.globalAlpha = 1;
      if (active) {
        requestAnimationFrame(drawFrame);
      } else {
        canvas.remove();
        resolve();
      }
    };
    drawFrame(startedAt);
  });
}

function capturePositions(container: HTMLElement | null | undefined, excluded: HTMLElement | null | undefined): Map<string, DOMRect> {
  const positions = new Map<string, DOMRect>();
  container?.querySelectorAll<HTMLElement>(DUST_KEY_SELECTOR).forEach((element) => {
    const key = element.dataset.dustKey;
    if (key && element !== excluded && !positions.has(key) && isVisibleElement(element)) {
      positions.set(key, element.getBoundingClientRect());
    }
  });
  return positions;
}

function animateReflow(container: HTMLElement | null | undefined, positions: Map<string, DOMRect>): void {
  if (!container || motionReduced()) return;
  container.querySelectorAll<HTMLElement>(DUST_KEY_SELECTOR).forEach((element) => {
    const key = element.dataset.dustKey;
    const previous = key ? positions.get(key) : undefined;
    if (!previous || !isVisibleElement(element)) return;
    const current = element.getBoundingClientRect();
    const deltaX = previous.left - current.left;
    const deltaY = previous.top - current.top;
    if (Math.abs(deltaX) < 1 && Math.abs(deltaY) < 1) return;
    element.animate(
      [
        { transform: `translate(${deltaX}px, ${deltaY}px)` },
        { transform: "translate(0, 0)" },
      ],
      { duration: 360, easing: "cubic-bezier(.2,.8,.2,1)" },
    );
  });
}

export function findDustTarget(container: HTMLElement | null | undefined, key: string): HTMLElement | null {
  if (!container) return null;
  const matches = Array.from(container.querySelectorAll<HTMLElement>(DUST_KEY_SELECTOR)).filter(
    (element) => element.dataset.dustKey === key,
  );
  return matches.find(isVisibleElement) ?? matches[0] ?? null;
}

export function useDustRemoval() {
  async function removeWithDust(options: RemoveWithDustOptions): Promise<boolean> {
    const reduced = motionReduced();
    let snapshot: DustSnapshot | null = null;
    if (!reduced && options.target?.isConnected) {
      try {
        snapshot = await rasterizeElement(options.target);
      } catch (error) {
        console.warn("尘埃消散效果降级：无法栅格化目标元素", error);
        snapshot = null;
      }
    }
    const positions = capturePositions(options.container, options.target);

    const removed = await options.remove();
    if (removed === false) return false;
    if (!snapshot) return true;

    const animation = playDust(snapshot);
    options.target?.classList.add("lp-dust-removal-source");
    await nextTick();
    animateReflow(options.container, positions);
    await animation;
    return true;
  }

  return { removeWithDust };
}
