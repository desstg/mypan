import { onBeforeUnmount, ref } from "vue";

export function useStartupCountdown() {
  const remainingDisplay = ref(0);
  let readyAtMs = 0;
  let timer: number | null = null;

  function tick() {
    if (!readyAtMs) {
      remainingDisplay.value = 0;
      return;
    }
    const remaining = Math.ceil((readyAtMs - Date.now()) / 1000);
    remainingDisplay.value = Math.max(0, remaining);
    if (remainingDisplay.value <= 0) {
      readyAtMs = 0;
      stop();
    }
  }

  function start() {
    if (timer) return;
    timer = window.setInterval(tick, 1000);
  }

  function stop() {
    if (timer) {
      window.clearInterval(timer);
      timer = null;
    }
  }

  function applyStartupRemaining(seconds: number) {
    const sec = Math.max(0, Math.floor(seconds));
    if (sec <= 0) {
      readyAtMs = 0;
      remainingDisplay.value = 0;
      stop();
      return;
    }
    const localRemaining = readyAtMs
      ? Math.max(0, Math.ceil((readyAtMs - Date.now()) / 1000))
      : 0;
    if (!readyAtMs || sec > localRemaining + 1) {
      readyAtMs = Date.now() + sec * 1000;
    }
    tick();
    start();
  }

  onBeforeUnmount(stop);

  return { remainingDisplay, applyStartupRemaining, stopStartupCountdown: stop };
}
