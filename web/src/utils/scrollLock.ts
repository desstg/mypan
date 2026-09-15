let lockCount = 0;
let savedBodyOverflow = "";
let savedBodyPaddingRight = "";
let savedHtmlOverflow = "";
let savedHtmlPaddingRight = "";

function scrollbarWidth(): number {
  return Math.max(0, window.innerWidth - document.documentElement.clientWidth);
}

export function lockPageScroll(): void {
  lockCount += 1;
  if (lockCount > 1) return;

  const body = document.body;
  const html = document.documentElement;
  savedBodyOverflow = body.style.overflow;
  savedBodyPaddingRight = body.style.paddingRight;
  savedHtmlOverflow = html.style.overflow;
  savedHtmlPaddingRight = html.style.paddingRight;

  const gutter = scrollbarWidth();
  html.style.overflow = "hidden";
  body.style.overflow = "hidden";
  if (gutter > 0) {
    // 只给 body 补一次滚动条宽度，避免 html/body 同时收窄导致页面横向偏移放大。
    body.style.paddingRight = `${gutter}px`;
  }
}

export function unlockPageScroll(): void {
  if (lockCount <= 0) return;
  lockCount -= 1;
  if (lockCount > 0) return;

  const body = document.body;
  const html = document.documentElement;
  html.style.overflow = savedHtmlOverflow;
  html.style.paddingRight = savedHtmlPaddingRight;
  body.style.overflow = savedBodyOverflow;
  body.style.paddingRight = savedBodyPaddingRight;
}
