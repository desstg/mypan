<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import type { UploadTask } from "@/types/upload";

const props = defineProps<{ task: UploadTask }>();

const display = ref(0);
const confirmedFloor = ref(0);
const lastSpeed = ref(0);

let rafId = 0;
let lastTs = 0;

function confirmedPercent(): number {
  const t = props.task;
  if (t.status === "success" || t.status === "skipped") return 100;
  const total = t.total_bytes ?? 0;
  const uploaded = t.uploaded_bytes ?? 0;
  if (total > 0 && uploaded >= 0) {
    const pct = (uploaded / total) * 100;
    if (t.status === "running" || t.status === "pending") {
      return Math.min(99.5, pct);
    }
    return Math.min(100, pct);
  }
  return t.progress || 0;
}

function isAnimating(): boolean {
  return props.task.status === "running" || props.task.status === "pending";
}

function estimateChunkLeadPercent(task: UploadTask): number {
  const total = task.total_bytes ?? 0;
  if (total <= 0) return 3;
  const msg = task.message || "";
  const m = msg.match(/分片[（(](\d+)\s*\/\s*(\d+)[）)]/);
  if (m) {
    const totalParts = Number(m[2]) || 0;
    if (totalParts > 0) {
      const chunkBytes = total / totalParts;
      return Math.max(1.5, (chunkBytes / total) * 100 * 1.15);
    }
  }
  return 3;
}

function syncConfirmedFloor() {
  const confirmed = confirmedPercent();
  confirmedFloor.value = Math.max(confirmedFloor.value, confirmed);
}

function resetForTask() {
  const floor = confirmedPercent();
  confirmedFloor.value = floor;
  display.value = floor;
  lastSpeed.value = props.task.speed_bytes_per_second || 0;
  lastTs = 0;
}

function tick(ts: number) {
  const dt = lastTs ? Math.min(50, ts - lastTs) / 1000 : 0.016;
  lastTs = ts;

  syncConfirmedFloor();
  const floor = confirmedFloor.value;
  const t = props.task;

  if (t.status === "success" || t.status === "skipped") {
    display.value = 100;
    rafId = requestAnimationFrame(tick);
    return;
  }

  if (t.status === "paused") {
    display.value = Math.max(display.value, floor);
    rafId = requestAnimationFrame(tick);
    return;
  }

  const speed = t.speed_bytes_per_second || 0;
  if (speed > 0) lastSpeed.value = speed;

  let next = display.value;

  if (next < floor) {
    next += (floor - next) * 0.35;
  }

  if (isAnimating()) {
    const total = t.total_bytes || 0;
    const activeSpeed = speed > 0 ? speed : lastSpeed.value;
    if (activeSpeed > 0 && total > 0) {
      const advance = (activeSpeed / total) * 100 * dt;
      const leadCap = estimateChunkLeadPercent(t);
      next = Math.min(floor + leadCap, next + advance);
    } else if (next < floor) {
      next = floor;
    }
  } else {
    next = Math.max(next, floor);
  }

  next = Math.max(floor, Math.min(t.status === "running" || t.status === "pending" ? 99.5 : 100, next));
  display.value += (next - display.value) * 0.92;

  rafId = requestAnimationFrame(tick);
}

watch(
  () => props.task.task_id,
  () => resetForTask(),
);

watch(
  () => props.task.status,
  (status, prev) => {
    if (status === "success" || status === "skipped") {
      display.value = 100;
      confirmedFloor.value = 100;
      return;
    }
    if (prev === "paused" && (status === "pending" || status === "running")) {
      syncConfirmedFloor();
      display.value = Math.max(display.value, confirmedFloor.value);
    }
  },
);

onMounted(() => {
  resetForTask();
  rafId = requestAnimationFrame(tick);
});

onUnmounted(() => {
  cancelAnimationFrame(rafId);
});
</script>

<template>
  <div class="upload-task-progress-inner" :style="{ width: `${display}%` }" />
</template>
