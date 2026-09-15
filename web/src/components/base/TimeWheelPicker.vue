<template>
  <Teleport to="body">
    <div v-if="visible" class="twp-overlay">
      <div class="twp-modal" @click.stop>
        <div class="twp-body">
          <div class="twp-all-day-row" v-if="!dailyOnly && !allowDaily">
            <div v-if="allowManual" class="twp-mode-seg">
              <button
                type="button"
                class="twp-mode-btn"
                :class="{ active: isAllDay && mode !== 'manual' }"
                :disabled="isManualLocked"
                @click="setAllDay"
              >全天</button>
              <button type="button" class="twp-mode-btn" :class="{ active: mode === 'manual' }" @click="setModeManual">不再执行</button>
            </div>
            <button
              v-else
              type="button"
              class="twp-all-day-btn"
              :class="{ active: isAllDay }"
              @click="setAllDay"
            >全天</button>
          </div>
          <div class="twp-all-day-row" v-else-if="!dailyOnly">
            <div class="twp-mode-seg">
              <button type="button" class="twp-mode-btn" :class="{ active: mode === 'allday' }" :disabled="isManualLocked" @click="setModeAllday">全天</button>
              <button type="button" class="twp-mode-btn" :class="{ active: mode === 'window' }" :disabled="isManualLocked" @click="setModeWindow">时间段</button>
              <button v-if="allowDaily" type="button" class="twp-mode-btn" :class="{ active: mode === 'daily' }" @click="setModeDaily">每天定时</button>
              <button v-if="allowManual" type="button" class="twp-mode-btn" :class="{ active: mode === 'manual' }" @click="setModeManual">不再执行</button>
            </div>
          </div>

          <div v-if="mode === 'manual'" class="twp-manual-hint">
            <div class="twp-manual-title">不再自动执行</div>
            <div class="twp-manual-desc">该任务不参与定时调度，仅在手动点击执行或被自动联动调用时运行。</div>
            <div v-if="isManualLocked" class="twp-manual-lock">{{ manualLockedReasonText }}</div>
          </div>

          <div class="twp-wheels-section" v-show="mode !== 'manual'">
            <div class="twp-labels-row">
              <template v-if="mode === 'daily'">
                <span class="twp-label twp-label-single">每天执行时间</span>
              </template>
              <template v-else>
                <span class="twp-label">开始</span>
                <span class="twp-label">结束</span>
              </template>
            </div>
            <div class="twp-wheels-row" :class="{ 'twp-daily': mode === 'daily' }">
              <div class="twp-wheel" @mouseenter="hoveredWheel = 'startHour'" @mouseleave="clearHover('startHour')">
                <button v-show="isWheelActive('startHour')" type="button" class="twp-step twp-step-up" :disabled="!canStep('startHour', -1)" @mousedown.stop.prevent="startStepHold('startHour', -1)" @touchstart.stop.prevent="startStepHold('startHour', -1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 14.5 12 8l6.5 6.5" /></svg></button>
                <button v-show="isWheelActive('startHour')" type="button" class="twp-step twp-step-down" :disabled="!canStep('startHour', 1)" @mousedown.stop.prevent="startStepHold('startHour', 1)" @touchstart.stop.prevent="startStepHold('startHour', 1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 9.5 12 16l6.5-6.5" /></svg></button>
                <div v-for="h in 24" :key="'sh'+h" class="twp-item" :class="{ 'twp-sel': isCenter('startHour', h-1) }" :style="itemStyle('startHour', h-1)">{{ String(h-1).padStart(2,'0') }}</div>
              </div>
              <div class="twp-wheel" @mouseenter="hoveredWheel = 'startMin'" @mouseleave="clearHover('startMin')">
                <button v-show="isWheelActive('startMin')" type="button" class="twp-step twp-step-up" :disabled="!canStep('startMin', -1)" @mousedown.stop.prevent="startStepHold('startMin', -1)" @touchstart.stop.prevent="startStepHold('startMin', -1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 14.5 12 8l6.5 6.5" /></svg></button>
                <button v-show="isWheelActive('startMin')" type="button" class="twp-step twp-step-down" :disabled="!canStep('startMin', 1)" @mousedown.stop.prevent="startStepHold('startMin', 1)" @touchstart.stop.prevent="startStepHold('startMin', 1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 9.5 12 16l6.5-6.5" /></svg></button>
                <div v-for="m in 60" :key="'sm'+m" class="twp-item" :class="{ 'twp-sel': isCenter('startMin', m-1) }" :style="itemStyle('startMin', m-1)">{{ String(m-1).padStart(2,'0') }}</div>
              </div>
              <div class="twp-wheel" v-show="mode !== 'daily'" @mouseenter="hoveredWheel = 'endHour'" @mouseleave="clearHover('endHour')">
                <button v-show="isWheelActive('endHour')" type="button" class="twp-step twp-step-up" :disabled="!canStep('endHour', -1)" @mousedown.stop.prevent="startStepHold('endHour', -1)" @touchstart.stop.prevent="startStepHold('endHour', -1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 14.5 12 8l6.5 6.5" /></svg></button>
                <button v-show="isWheelActive('endHour')" type="button" class="twp-step twp-step-down" :disabled="!canStep('endHour', 1)" @mousedown.stop.prevent="startStepHold('endHour', 1)" @touchstart.stop.prevent="startStepHold('endHour', 1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 9.5 12 16l6.5-6.5" /></svg></button>
                <div v-for="h in 24" :key="'eh'+h" class="twp-item" :class="{ 'twp-sel': isCenter('endHour', h-1) }" :style="itemStyle('endHour', h-1)">{{ String(h-1).padStart(2,'0') }}</div>
              </div>
              <div class="twp-wheel" v-show="mode !== 'daily'" @mouseenter="hoveredWheel = 'endMin'" @mouseleave="clearHover('endMin')">
                <button v-show="isWheelActive('endMin')" type="button" class="twp-step twp-step-up" :disabled="!canStep('endMin', -1)" @mousedown.stop.prevent="startStepHold('endMin', -1)" @touchstart.stop.prevent="startStepHold('endMin', -1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 14.5 12 8l6.5 6.5" /></svg></button>
                <button v-show="isWheelActive('endMin')" type="button" class="twp-step twp-step-down" :disabled="!canStep('endMin', 1)" @mousedown.stop.prevent="startStepHold('endMin', 1)" @touchstart.stop.prevent="startStepHold('endMin', 1)"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5.5 9.5 12 16l6.5-6.5" /></svg></button>
                <div v-for="m in 60" :key="'em'+m" class="twp-item" :class="{ 'twp-sel': isCenter('endMin', m-1) }" :style="itemStyle('endMin', m-1)">{{ String(m-1).padStart(2,'0') }}</div>
              </div>
              <div class="twp-lines">
                <div class="twp-line twp-line-top"></div>
                <div class="twp-line twp-line-bot"></div>
              </div>
              <div class="twp-fade"></div>
            </div>
          </div>
        </div>

        <div class="twp-footer">
          <button type="button" class="twp-btn twp-btn-cancel" @click="$emit('cancel')">取消</button>
          <button type="button" class="twp-btn twp-btn-confirm" @click="confirm">确定</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { computed, ref, watch, onBeforeUnmount } from 'vue'

const props = defineProps({
  visible: { type: Boolean, default: false },
  startTime: { type: String, default: '00:00' },
  endTime: { type: String, default: '00:00' },
  allDay: { type: Boolean, default: false },
  allowDaily: { type: Boolean, default: false },
  allowManual: { type: Boolean, default: false },
  dailyOnly: { type: Boolean, default: false },
  mode: { type: String, default: '' },  // 'allday' | 'window' | 'daily' | 'manual'，仅 allowDaily 时生效
  manualLocked: { type: Boolean, default: false },
  manualLockedReason: { type: String, default: '' },
})

const emit = defineEmits(['confirm', 'cancel'])

const ITEM_H = 34
const VISIBLE_RADIUS = 1

const startH = ref(0)
const startM = ref(0)
const endH = ref(0)
const endM = ref(0)
const isAllDay = ref(false)
const mode = ref('window')

const displayStartH = ref(0)
const displayStartM = ref(0)
const displayEndH = ref(0)
const displayEndM = ref(0)
const hoveredWheel = ref('')
const activeColor = ref('#3b82f6')
const inactiveColor = ref('#9aa6b6')
const isManualLocked = computed(() => props.allowManual && props.manualLocked && mode.value === 'manual')
const manualLockedReasonText = computed(() => (
  props.manualLockedReason || '当前任务处于不再自动执行状态，不能直接改回全天或时间段；如需恢复定时，请先解除相关自动联动后再调整。'
))

let stepHoldTimer = null
let stepHoldInterval = null
const stepHoldKey = ref('')

function parse(t) {
  try {
    const [h, m] = String(t || '00:00').split(':').map(Number)
    return { h: Math.max(0, Math.min(23, h || 0)), m: Math.max(0, Math.min(59, m || 0)) }
  } catch { return { h: 0, m: 0 } }
}

function getWheel(key) {
  if (key === 'startHour') return { max: 23, val: startH, disp: displayStartH, setVal: (v) => { startH.value = v }, setDisp: (v) => { displayStartH.value = v } }
  if (key === 'startMin') return { max: 59, val: startM, disp: displayStartM, setVal: (v) => { startM.value = v }, setDisp: (v) => { displayStartM.value = v } }
  if (key === 'endHour') return { max: 23, val: endH, disp: displayEndH, setVal: (v) => { endH.value = v }, setDisp: (v) => { displayEndH.value = v } }
  if (key === 'endMin') return { max: 59, val: endM, disp: displayEndM, setVal: (v) => { endM.value = v }, setDisp: (v) => { displayEndM.value = v } }
  return null
}

function checkAllDay() {
  isAllDay.value = (startH.value === 0 && startM.value === 0 && endH.value === 23 && endM.value === 59)
}

function isCenter(key, value) {
  const w = getWheel(key)
  return w ? value === w.disp.value : false
}

function itemStyle(key, value) {
  const w = getWheel(key)
  if (!w) return {}
  const diff = value - w.disp.value
  const abs = Math.abs(diff)
  const hidden = abs > VISIBLE_RADIUS
  const scale = abs === 0 ? 1 : 0.88
  const opacity = hidden ? 0 : abs === 0 ? 1 : abs === 1 ? 0.58 : 0.26
  const color = abs === 0 ? activeColor.value : inactiveColor.value

  return {
    transform: `translateY(calc(-50% + ${diff * ITEM_H}px)) scale(${scale})`,
    opacity,
    color,
    pointerEvents: 'none',
    zIndex: VISIBLE_RADIUS + 1 - abs
  }
}

function canStep(key, dir) {
  const w = getWheel(key)
  if (!w) return false
  const next = w.val.value + dir
  return next >= 0 && next <= w.max
}

function stepValue(key, dir) {
  const w = getWheel(key)
  if (!w) return
  const next = Math.max(0, Math.min(w.val.value + dir, w.max))
  if (next === w.val.value) return
  w.setVal(next)
  w.setDisp(next)
  checkAllDay()
}

function isWheelActive(key) {
  return hoveredWheel.value === key || stepHoldKey.value === key
}

function clearHover(key) {
  if (hoveredWheel.value === key && stepHoldKey.value !== key) {
    hoveredWheel.value = ''
  }
}

function startStepHold(key, dir) {
  stopStepHold()
  stepHoldKey.value = key
  stepValue(key, dir)

  stepHoldTimer = window.setTimeout(() => {
    stepHoldInterval = window.setInterval(() => {
      if (!canStep(key, dir)) {
        stopStepHold()
        return
      }
      stepValue(key, dir)
    }, 70)
  }, 280)

  window.addEventListener('mouseup', stopStepHold)
  window.addEventListener('touchend', stopStepHold)
  window.addEventListener('touchcancel', stopStepHold)
}

function stopStepHold() {
  if (stepHoldTimer) {
    clearTimeout(stepHoldTimer)
    stepHoldTimer = null
  }
  if (stepHoldInterval) {
    clearInterval(stepHoldInterval)
    stepHoldInterval = null
  }
  stepHoldKey.value = ''
  window.removeEventListener('mouseup', stopStepHold)
  window.removeEventListener('touchend', stopStepHold)
  window.removeEventListener('touchcancel', stopStepHold)
}

function onKeydown(event) {
  if (event.key === 'Escape') {
    event.preventDefault?.()
    event.stopPropagation?.()
    event.stopImmediatePropagation?.()
    emit('cancel')
  }
}

onBeforeUnmount(() => {
  stopStepHold()
  window.removeEventListener('keydown', onKeydown, true)
})

watch(() => props.visible, (v) => {
  if (v) {
    const isDark = document?.documentElement?.dataset?.theme === 'dark'
    activeColor.value = isDark ? '#60a5fa' : '#3b82f6'
    inactiveColor.value = isDark ? 'rgba(148, 163, 184, 0.78)' : '#9aa6b6'
    window.addEventListener('keydown', onKeydown, true)
    if (props.dailyOnly || (props.allowDaily && props.mode === 'daily')) {
      const s = parse(props.startTime)
      startH.value = s.h; startM.value = s.m
      displayStartH.value = s.h; displayStartM.value = s.m
      endH.value = 23; endM.value = 59
      displayEndH.value = 23; displayEndM.value = 59
      isAllDay.value = false
      mode.value = 'daily'
    } else if (props.allDay) {
      startH.value = 0; startM.value = 0
      endH.value = 23; endM.value = 59
      displayStartH.value = 0; displayStartM.value = 0
      displayEndH.value = 23; displayEndM.value = 59
      isAllDay.value = true
      mode.value = 'allday'
    } else {
      const s = parse(props.startTime)
      const e = parse(props.endTime)
      startH.value = s.h; startM.value = s.m
      endH.value = e.h; endM.value = e.m
      displayStartH.value = s.h; displayStartM.value = s.m
      displayEndH.value = e.h; displayEndM.value = e.m
      checkAllDay()
      mode.value = 'window'
    }
    if (props.allowManual && props.mode === 'manual') {
      mode.value = 'manual'
    }
  } else {
    stopStepHold()
    window.removeEventListener('keydown', onKeydown, true)
  }
})

function setAllDay() {
  if (isManualLocked.value) return
  if (mode.value === 'manual') mode.value = 'allday'
  isAllDay.value = true
  startH.value = 0; startM.value = 0
  endH.value = 23; endM.value = 59
  displayStartH.value = 0; displayStartM.value = 0
  displayEndH.value = 23; displayEndM.value = 59
}

function setModeAllday() {
  if (isManualLocked.value) return
  mode.value = 'allday'
  setAllDay()
}

function setModeWindow() {
  if (isManualLocked.value) return
  mode.value = 'window'
  // 从全天切回时间段时给个非全天的默认区间，避免又被判成全天
  if (startH.value === 0 && startM.value === 0 && endH.value === 23 && endM.value === 59) {
    endH.value = 0; endM.value = 0
    displayEndH.value = 0; displayEndM.value = 0
  }
  checkAllDay()
}

function setModeDaily() {
  mode.value = 'daily'
}

function setModeManual() {
  mode.value = 'manual'
}

function confirm() {
  const fmt = (h, m) => `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
  if (props.allowManual && mode.value === 'manual') {
    emit('confirm', { mode: 'manual', allDay: false, startTime: fmt(startH.value, startM.value), endTime: fmt(endH.value, endM.value) })
    return
  }
  if (props.dailyOnly || (props.allowDaily && mode.value === 'daily')) {
    const t = fmt(startH.value, startM.value)
    emit('confirm', { mode: 'daily', allDay: false, startTime: t, endTime: t })
    return
  }
  const allDay = startH.value === 0 && startM.value === 0 && endH.value === 23 && endM.value === 59
  emit('confirm', { mode: allDay ? 'allday' : 'window', allDay, startTime: fmt(startH.value, startM.value), endTime: fmt(endH.value, endM.value) })
}
</script>

<style scoped>
.twp-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 200000;
  backdrop-filter: blur(4px);
}

.twp-modal {
  background: var(--surface);
  color: var(--text);
  border-radius: 18px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.2);
  width: 420px;
  overflow: hidden;
  animation: twpIn 0.25s ease-out;
  font-family: inherit;
}

@keyframes twpIn {
  from { opacity: 0; transform: scale(0.92) translateY(20px); }
  to { opacity: 1; transform: scale(1) translateY(0); }
}

.twp-body {
  padding: 20px 20px 8px;
}

.twp-all-day-row {
  display: flex;
  justify-content: center;
  margin-bottom: 14px;
}

.twp-all-day-btn {
  padding: 7px 24px;
  border: 1.5px solid var(--border);
  border-radius: 20px;
  background: var(--surface);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.2s;
}

.twp-all-day-btn:hover {
  border-color: #3b82f6;
  color: #3b82f6;
}

.twp-all-day-btn.active {
  background: #3b82f6;
  border-color: #3b82f6;
  color: #fff;
}

.twp-mode-seg {
  display: inline-flex;
  background: var(--surface-sunken);
  border: 1.5px solid var(--border);
  border-radius: 22px;
  padding: 3px;
}

.twp-mode-btn {
  padding: 6px 18px;
  border: 0;
  background: transparent;
  border-radius: 18px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
}

.twp-mode-btn:hover {
  color: #3b82f6;
}

.twp-mode-btn.active {
  background: #3b82f6;
  color: #fff;
  box-shadow: 0 2px 6px rgba(59, 130, 246, 0.35);
}

.twp-mode-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.twp-mode-btn:disabled:hover {
  color: var(--text-muted);
}

.twp-label-single {
  letter-spacing: 2px;
}

.twp-manual-hint {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  height: 188px;
  justify-content: center;
  background: var(--surface-sunken);
  border-radius: 14px;
  padding: 0 24px;
  text-align: center;
}

.twp-manual-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
}

.twp-manual-desc {
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-muted);
}

.twp-manual-lock {
  max-width: 280px;
  padding: 8px 12px;
  border-radius: 10px;
  background: color-mix(in srgb, var(--warning) 14%, var(--surface));
  color: var(--warning);
  font-size: 12px;
  line-height: 1.6;
}

.twp-wheels-row.twp-daily {
  justify-content: center;
}

.twp-wheels-row.twp-daily .twp-wheel {
  flex: 0 0 96px;
}

.twp-labels-row {
  display: flex;
  padding: 0 8px;
  margin-bottom: 6px;
}

.twp-label {
  flex: 1;
  text-align: center;
  font-size: 11px;
  font-weight: 500;
  color: var(--text-muted);
  letter-spacing: 1px;
}

.twp-wheels-row {
  position: relative;
  display: flex;
  height: 188px;
  background: var(--surface-sunken);
  border-radius: 14px;
  overflow: hidden;
}

.twp-wheel {
  flex: 1;
  width: 78px;
  height: 100%;
  overflow: hidden;
  position: relative;
  z-index: 1;
  cursor: default;
  touch-action: manipulation;
}

.twp-wheel::-webkit-scrollbar {
  display: none;
}

.twp-step {
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  width: 28px;
  height: 28px;
  border: 0;
  background: transparent;
  color: #3b82f6;
  cursor: pointer;
  z-index: 6;
  opacity: 0.72;
  transition: all 0.16s ease;
  user-select: none;
  padding: 3px;
}

.twp-step:hover {
  color: #2563eb;
  opacity: 1;
  transform: translateX(-50%) scale(1.12);
}

.twp-step:active {
  transform: translateX(-50%) scale(0.94);
}

.twp-step:disabled {
  opacity: 0.18;
  cursor: not-allowed;
  transform: translateX(-50%);
}

.twp-step-up {
  top: 7px;
}

.twp-step-down {
  bottom: 7px;
}

.twp-step svg {
  display: block;
  width: 100%;
  height: 100%;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.twp-item {
  position: absolute;
  top: 50%;
  left: 0;
  right: 0;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  font-weight: 400;
  font-family: inherit;
  user-select: none;
  will-change: transform, opacity, color, font-size;
  transition:
    transform 0.22s cubic-bezier(0.22, 0.9, 0.28, 1),
    opacity 0.22s ease,
    color 0.22s ease,
    font-size 0.22s ease;
}

.twp-item.twp-sel {
  font-size: 20px;
  font-weight: 600;
}

.twp-lines {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 2;
}

.twp-line {
  position: absolute;
  left: 8px;
  right: 8px;
  height: 1px;
  background: var(--border);
}

.twp-line-top {
  top: 77px;
}

.twp-line-bot {
  top: 111px;
}

.twp-fade {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 0;
  background:
    linear-gradient(to bottom,
      var(--surface-sunken) 0%,
      color-mix(in srgb, var(--surface-sunken) 60%, transparent) 30%,
      transparent 38%,
      transparent 62%,
      color-mix(in srgb, var(--surface-sunken) 60%, transparent) 70%,
      var(--surface-sunken) 100%
    );
}

.twp-footer {
  display: flex;
  gap: 12px;
  padding: 8px 20px 20px;
  justify-content: center;
}

.twp-btn {
  flex: 1;
  height: 42px;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
  font-family: inherit;
  border: none;
  cursor: pointer;
  transition: all 0.2s;
}

.twp-btn-cancel {
  background: var(--surface-sunken);
  color: var(--text-muted);
}

.twp-btn-cancel:hover {
  background: var(--surface-hover);
  color: var(--text);
}

.twp-btn-confirm {
  background: linear-gradient(135deg, #4c74df, #02a6f0);
  color: #fff;
  box-shadow: 0 2px 8px rgba(76, 116, 223, 0.3);
}

.twp-btn-confirm:hover {
  box-shadow: 0 4px 16px rgba(76, 116, 223, 0.45);
  transform: translateY(-1px);
}

:global(:root[data-theme="dark"] .twp-overlay) {
  background: rgba(0, 0, 0, 0.62);
}

:global(:root[data-theme="dark"] .twp-modal) {
  box-shadow: 0 24px 72px rgba(0, 0, 0, 0.5);
}
</style>
