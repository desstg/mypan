import { onActivated, onDeactivated, onUnmounted, ref } from "vue";

export function liveElapsedMs(startedAt?: string, tick = 0): number {
  void tick;
  const raw = (startedAt || "").trim();
  if (!raw) return 0;
  const started = new Date(raw).getTime();
  if (!Number.isFinite(started)) return 0;
  const ms = Date.now() - started;
  return ms > 0 ? ms : 0;
}

export function useLiveElapsedClock() {
  const tick = ref(0);
  let timer: number | null = null;
  let scopeActive = true;
  let shouldRun = false;

  function start() {
    if (!scopeActive || timer !== null) return;
    timer = window.setInterval(() => {
      tick.value += 1;
    }, 1000);
  }

  function stop() {
    if (timer === null) return;
    window.clearInterval(timer);
    timer = null;
  }

  function sync(nextShouldRun: boolean) {
    shouldRun = nextShouldRun;
    if (shouldRun) start();
    else stop();
  }

  onActivated(() => {
    scopeActive = true;
    if (shouldRun) start();
  });
  onDeactivated(() => {
    scopeActive = false;
    stop();
  });
  onUnmounted(stop);

  return { tick, sync };
}
