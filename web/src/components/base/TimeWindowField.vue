<script setup lang="ts">
import { ref } from "vue";
import TimeWheelPicker from "@/components/base/TimeWheelPicker.vue";

/** TimeWheelPicker 确认回调载荷（与 useTimeWindowSchedule 的 TimeWindowWheelPayload 对应）。 */
export interface TimeWindowFieldPayload {
  mode?: string;
  startTime: string;
  endTime: string;
  allDay: boolean;
}

withDefaults(
  defineProps<{
    /** 触发框展示文案（由父组件用 useTimeWindowSchedule 计算，如「全天」「10:00-12:00」） */
    display: string;
    startTime?: string;
    endTime?: string;
    allDay?: boolean;
    allowDaily?: boolean;
    allowManual?: boolean;
    dailyOnly?: boolean;
    mode?: string;
    manualLocked?: boolean;
    manualLockedReason?: string;
  }>(),
  {
    startTime: "00:00",
    endTime: "00:00",
    allDay: false,
    allowDaily: false,
    allowManual: false,
    dailyOnly: false,
    mode: "",
    manualLocked: false,
    manualLockedReason: "",
  },
);

const emit = defineEmits<{
  confirm: [payload: TimeWindowFieldPayload];
  cancel: [];
}>();

const open = ref(false);

function onConfirm(payload: TimeWindowFieldPayload) {
  open.value = false;
  emit("confirm", payload);
}
</script>

<template>
  <span class="time-window-field">
    <button
      type="button"
      class="time-window-field__trigger"
      :aria-haspopup="true"
      @click="open = true"
    >
      <span class="time-window-field__text">{{ display }}</span>
      <svg class="time-window-field__icon" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="12" cy="12" r="8.5" />
        <path d="M12 7.5v5l3.2 2" />
      </svg>
    </button>
    <TimeWheelPicker
      :visible="open"
      :start-time="startTime"
      :end-time="endTime"
      :all-day="allDay"
      :allow-daily="allowDaily"
      :allow-manual="allowManual"
      :daily-only="dailyOnly"
      :mode="mode"
      :manual-locked="manualLocked"
      :manual-locked-reason="manualLockedReason"
      @confirm="onConfirm"
      @cancel="open = false"
    />
  </span>
</template>

<style scoped>
.time-window-field {
  display: inline-flex;
  width: 100%;
}
.time-window-field__trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 9px 12px; /* 与 AppInput 盒模型一致，并排时高度相同 */
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 14px;
  cursor: pointer;
  box-sizing: border-box;
  transition: border-color 0.2s;
}
.time-window-field__trigger:hover {
  border-color: var(--brand);
}
.time-window-field__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.time-window-field__icon {
  width: 16px;
  height: 16px;
  flex: none;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
}
</style>
