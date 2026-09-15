<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { quarkTVApi, type QuarkTVAccount } from "@/api/cloudTools";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import AppPlainModal from "@/components/base/AppPlainModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const props = defineProps<{
  open: boolean;
  accounts: QuarkTVAccount[];
}>();
const emit = defineEmits<{ close: []; bound: [] }>();

type Phase = "loading" | "waiting" | "success" | "failed" | "expired" | "error";

const phase = ref<Phase>("loading");
const qrImage = ref("");
const message = ref("");
const token = ref("");
const expiresIn = ref(0);
const accountId = ref<number | null>(null);
const binding = ref(false);

let pollTimer: ReturnType<typeof setTimeout> | null = null;
let countdownTimer: ReturnType<typeof setInterval> | null = null;
let errorStreak = 0;

const canStart = computed(() => accountId.value !== null && !binding.value);
const accountOptions = computed(() => props.accounts.map((a) => ({ value: a.id, label: a.name })));
const displayMessage = computed(() => message.value.replace(/^DRIVER_ERROR:\s*/i, "").trim());
const showDeviceLimitNotice = computed(() => displayMessage.value.includes("设备数超限"));
const failedTitle = computed(() => {
  const base = phase.value === "expired" ? "二维码已过期" : phase.value === "failed" ? "绑定失败" : "获取二维码失败";
  if (!displayMessage.value || displayMessage.value === base) return base;
  return `${base}：${displayMessage.value}`;
});
const primaryButtonText = computed(() => {
  if (binding.value) return "绑定中…";
  if (phase.value === "failed" || phase.value === "expired" || phase.value === "error") {
    return "重新获取二维码";
  }
  return "获取二维码";
});

const expireText = computed(() => {
  if (phase.value === "expired") return "二维码已过期";
  if (expiresIn.value <= 0) return "";
  return `二维码剩余有效时间：${expiresIn.value} 秒`;
});

function clearTimers() {
  if (pollTimer) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
}

function startCountdown(seconds: number) {
  if (countdownTimer) clearInterval(countdownTimer);
  expiresIn.value = Math.max(0, seconds || 0);
  if (expiresIn.value <= 0) return;
  countdownTimer = setInterval(() => {
    expiresIn.value = Math.max(0, expiresIn.value - 1);
    if (expiresIn.value <= 0 && phase.value === "waiting") {
      phase.value = "expired";
      message.value = "二维码已过期，请重新获取";
      clearTimers();
    }
  }, 1000);
}

function schedulePoll(delay = 2000) {
  if (pollTimer) clearTimeout(pollTimer);
  pollTimer = setTimeout(() => void poll(), delay);
}

function reset() {
  clearTimers();
  phase.value = "loading";
  qrImage.value = "";
  message.value = "";
  token.value = "";
  expiresIn.value = 0;
  binding.value = false;
  errorStreak = 0;
  accountId.value = props.accounts[0]?.id ?? null;
}

async function start() {
  if (accountId.value === null) {
    toast.error("请先选择夸克账号");
    return;
  }
  clearTimers();
  phase.value = "loading";
  qrImage.value = "";
  message.value = "";
  token.value = "";
  expiresIn.value = 0;
  errorStreak = 0;
  try {
    const res = await quarkTVApi.bindStart(accountId.value);
    if (!res.token || !res.qr_image) throw new Error("获取二维码失败");
    token.value = res.token;
    qrImage.value = res.qr_image.startsWith("data:")
      ? res.qr_image
      : `data:image/jpeg;base64,${res.qr_image}`;
    phase.value = "waiting";
    startCountdown(res.expires_in || 300);
    schedulePoll(2000);
  } catch (e) {
    phase.value = "error";
    message.value = getApiErrorMessage(e, "获取二维码失败");
  }
}

async function poll() {
  if (!token.value) return;
  try {
    const res = await quarkTVApi.bindPoll(token.value);
    errorStreak = 0;
    if (res.status === "success") {
      binding.value = true;
      phase.value = "success";
      clearTimers();
      toast.success("绑定成功，STRM 播放请求将改走夸克 TV 直链");
      emit("bound");
      return;
    }
    if (res.status === "failed" || res.status === "expired") {
      phase.value = res.status === "expired" ? "expired" : "failed";
      message.value = res.message || (res.status === "expired" ? "二维码已过期" : "绑定失败");
      clearTimers();
      return;
    }
    schedulePoll(2000);
  } catch {
    errorStreak += 1;
    if (errorStreak >= 5) {
      phase.value = "error";
      message.value = "网络异常，请重试";
      clearTimers();
      return;
    }
    schedulePoll(3000);
  }
}

function handleClose() {
  clearTimers();
  emit("close");
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      reset();
      void start();
    } else {
      clearTimers();
    }
  },
);

onUnmounted(clearTimers);
</script>

<template>
  <AppPlainModal :open="open" title="选择绑定的账号" size="sm" @close="handleClose">
    <AppSelect
      v-model="accountId"
      :options="accountOptions"
      :disabled="binding"
      @update:modelValue="phase === 'waiting' ? start() : undefined"
    />

    <div class="qtv-body" :class="{ 'qtv-body--failed': phase === 'failed' || phase === 'expired' || phase === 'error' }">
      <div v-if="phase === 'loading'" class="qtv-state qtv-state--loading">
        <BusySpinner :size="28" color="var(--brand)" />
        <span>正在生成二维码...</span>
      </div>

      <div v-else-if="phase === 'waiting' || phase === 'success'" class="qtv-state">
        <img v-if="qrImage" class="qtv-image" :src="qrImage" alt="夸克 TV 扫码二维码" />
        <div class="qtv-hint">请使用夸克 App 扫码，并在手机端确认登录。TV 端与所选夸克账号需为同一账号。</div>
        <div v-if="phase === 'waiting' && expireText" class="qtv-countdown">{{ expireText }}</div>
        <div v-else class="qtv-success">绑定成功</div>
      </div>

      <div v-else class="qtv-state qtv-state--failed">
        <div class="qtv-failed-panel">
          <div class="qtv-failed-panel__header">
            <div class="qtv-failed-panel__icon-wrap">
              <i class="fas fa-circle-exclamation"></i>
            </div>
            <div class="qtv-failed-panel__meta">
              <div class="qtv-result-title">{{ failedTitle }}</div>
            </div>
          </div>
          <div v-if="showDeviceLimitNotice" class="qtv-notice-card">
            <div class="qtv-notice-card__title">处理建议</div>
            <div class="qtv-notice-card__text">
              单个夸克账号最多绑定 2 个终端。
            </div>
            <div class="qtv-notice-card__text">
              请在夸克 App 中进入「头像 > 登录设备」，退出或解绑不用的 TV 端。
            </div>
            <div class="qtv-notice-card__text qtv-notice-card__text--warn">
              解绑操作每 7 天仅可执行一次。
            </div>
          </div>
        </div>
      </div>
    </div>

      <div class="qtv-action">
        <AppButton variant="primary" :disabled="!canStart" @click="start">
          {{ primaryButtonText }}
        </AppButton>
      </div>
  </AppPlainModal>
</template>

<style scoped>
.qtv-body {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 300px;
  margin-top: 14px;
}
.qtv-action {
  display: flex;
  justify-content: center;
  padding-top: 14px;
}
.qtv-body--failed {
  align-items: flex-start;
  min-height: auto;
  margin-top: 10px;
  padding-top: 4px;
}
.qtv-state {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  text-align: center;
  color: var(--text);
  font-size: 13px;
}
.qtv-state--loading {
  color: var(--text-muted);
}
.qtv-state--failed {
  align-items: stretch;
  justify-content: flex-start;
  color: var(--text);
}
.qtv-image {
  width: 220px;
  height: 220px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: #fff;
  padding: 8px;
  box-sizing: border-box;
}
.qtv-hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.5;
}
.qtv-countdown {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
}
.qtv-success {
  color: #16a34a;
  font-size: 13px;
  font-weight: 600;
}
.qtv-result-title {
  color: #1f2937;
  font-size: 17px;
  font-weight: 700;
  line-height: 1.35;
}
.qtv-failed-panel {
  width: min(100%, 560px);
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 0 auto;
}
.qtv-failed-panel__header {
  display: flex;
  align-items: center;
  gap: 12px;
  text-align: left;
}
.qtv-failed-panel__icon-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  border-radius: 13px;
  background: linear-gradient(180deg, #ef4444, #dc2626);
  box-shadow: 0 12px 24px rgba(220, 38, 38, 0.18);
  color: #fff;
}
.qtv-failed-panel__icon-wrap i {
  font-size: 18px;
}
.qtv-failed-panel__meta {
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.qtv-notice-card {
  width: 100%;
  padding: 16px 18px;
  border: 1px solid rgba(245, 158, 11, 0.22);
  border-radius: 14px;
  background: linear-gradient(180deg, rgba(255, 251, 235, 0.96), rgba(255, 247, 237, 0.92));
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.05);
  text-align: left;
}
.qtv-notice-card__title {
  margin-bottom: 9px;
  color: #b45309;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.04em;
}
.qtv-notice-card__text {
  color: #7c5a10;
  font-size: 13px;
  line-height: 1.78;
}
.qtv-notice-card__text + .qtv-notice-card__text {
  margin-top: 8px;
}
.qtv-notice-card__text--warn {
  color: #92400e;
  font-weight: 600;
}
@media (max-width: 640px) {
  .qtv-failed-panel__header {
    gap: 10px;
  }
  .qtv-failed-panel__icon-wrap {
    width: 38px;
    height: 38px;
    border-radius: 12px;
  }
  .qtv-result-title {
    font-size: 16px;
  }
}
</style>
