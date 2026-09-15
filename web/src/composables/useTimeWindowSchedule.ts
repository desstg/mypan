import { computed, type Ref } from "vue";

export type TimeWindowMode = "always" | "custom";
export type ScheduleMode = "manual" | "window";

export interface TimeWindowFields {
  time_window_mode: TimeWindowMode;
  time_start: string;
  time_end: string;
  time_window_enabled?: boolean;
  schedule_mode?: ScheduleMode;
}

export interface TimeWindowWheelPayload {
  mode?: string;
  startTime: string;
  endTime: string;
  allDay: boolean;
}

export function formatTimeWindowDisplay(input: {
  timeWindowMode: TimeWindowMode;
  timeStart: string;
  timeEnd: string;
  scheduleMode?: ScheduleMode;
  manualLabel?: string;
}): string {
  if (input.scheduleMode === "manual") {
    return input.manualLabel ?? "不再执行（仅手动 / 联动）";
  }
  if (input.timeWindowMode === "always") return "全天";
  const [sh, sm] = input.timeStart.split(":").map(Number);
  const [eh, em] = input.timeEnd.split(":").map(Number);
  const sMin = sh * 60 + sm;
  const eMin = eh * 60 + em;
  if (sMin < eMin) return `${input.timeStart}-${input.timeEnd}`;
  if (sMin === eMin) return "全天";
  return `${input.timeStart}-次日${input.timeEnd}`;
}

export function applyTimeWindowFromTask(
  form: TimeWindowFields,
  task: {
    time_window_enabled: boolean;
    time_start?: string;
    time_end?: string;
    schedule_mode?: string;
  },
) {
  form.time_window_mode = task.time_window_enabled ? "custom" : "always";
  form.time_start = task.time_start || "00:00";
  form.time_end = task.time_end || "23:59";
  if (form.schedule_mode !== undefined) {
    form.schedule_mode = task.schedule_mode === "manual" ? "manual" : "window";
  }
  if (form.time_window_enabled !== undefined) {
    form.time_window_enabled = task.time_window_enabled;
  }
}

export function timeWindowPayload(form: TimeWindowFields): {
  time_window_enabled: boolean;
  time_start: string;
  time_end: string;
  schedule_mode?: string;
} {
  const payload = {
    time_window_enabled: form.time_window_mode === "custom",
    time_start: form.time_start,
    time_end: form.time_end,
  };
  if (form.schedule_mode !== undefined) {
    return { ...payload, schedule_mode: form.schedule_mode };
  }
  return payload;
}

export function useTimeWindowSchedule(
  form: TimeWindowFields,
  options?: {
    allowManual?: boolean;
    pickerVisible?: Ref<boolean>;
  },
) {
  const timeWindowDisplay = computed(() =>
    formatTimeWindowDisplay({
      timeWindowMode: form.time_window_mode,
      timeStart: form.time_start,
      timeEnd: form.time_end,
      scheduleMode: options?.allowManual ? form.schedule_mode : undefined,
    }),
  );

  const timePickerMode = computed(() => {
    if (!options?.allowManual) return undefined;
    if (form.schedule_mode === "manual") return "manual";
    if (form.time_window_mode === "always") return "allday";
    return "window";
  });

  function onTimeWheelConfirm(payload: TimeWindowWheelPayload) {
    if (options?.allowManual && payload.mode === "manual") {
      form.schedule_mode = "manual";
      form.time_window_mode = "always";
    } else if (payload.allDay) {
      if (form.schedule_mode !== undefined) form.schedule_mode = "window";
      form.time_window_mode = "always";
    } else {
      if (form.schedule_mode !== undefined) form.schedule_mode = "window";
      form.time_window_mode = "custom";
      form.time_start = payload.startTime;
      form.time_end = payload.endTime;
    }
    if (form.time_window_enabled !== undefined) {
      form.time_window_enabled = form.time_window_mode === "custom";
    }
    options?.pickerVisible && (options.pickerVisible.value = false);
  }

  return {
    timeWindowDisplay,
    timePickerMode,
    onTimeWheelConfirm,
  };
}
