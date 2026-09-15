<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { RouterLink } from "vue-router";
import { publicApi } from "@/api/public";
import { logout } from "@/api/auth";
import { useAuthStore } from "@/stores/auth";
import { toast } from "@/composables/useToast";
import SvgIcon from "@/components/icons/SvgIcon.vue";

type BrightStarShape = "dot" | "cross" | "penta" | "six";

interface BackgroundStar {
  key: string;
  left: number;
  top: number;
  size: number;
  minOpacity: number;
  maxOpacity: number;
  duration: number;
  delay: number;
  twinkleScale: number;
  blur: number;
  color: string;
}

interface BrightStar {
  key: string;
  left: number;
  top: number;
  size: number;
  core: number;
  shapeSize: number;
  duration: number;
  delay: number;
  color: string;
  rotation: number;
  shape: BrightStarShape;
  minOpacity: number;
  midOpacity: number;
  maxOpacity: number;
  pulseScale: number;
}

const backgroundStarColors = [
  "rgba(255, 255, 255, 0.98)",
  "rgba(214, 230, 255, 0.96)",
  "rgba(255, 241, 224, 0.94)",
] as const;

const brightStarColors = [
  "rgba(255, 255, 255, 0.95)",
  "rgba(214, 229, 255, 0.92)",
  "rgba(255, 239, 219, 0.9)",
] as const;

const brightStarShapes = ["dot", "dot", "dot", "cross", "cross", "penta", "six"] as const;

function randomBetween(min: number, max: number) {
  return Math.random() * (max - min) + min;
}

function pickRandom<T>(items: readonly T[]): T {
  return items[Math.floor(Math.random() * items.length)]!;
}

function createBackgroundStars(count = 54): BackgroundStar[] {
  return Array.from({ length: count }, (_, index) => {
    const size = randomBetween(0.7, 2.3);
    const maxOpacity = randomBetween(0.22, 0.68);
    return {
      key: `background-${index}`,
      left: randomBetween(1.5, 98.5),
      top: randomBetween(7, 86),
      size,
      minOpacity: Number((maxOpacity * randomBetween(0.35, 0.62)).toFixed(3)),
      maxOpacity: Number(maxOpacity.toFixed(3)),
      duration: Number(randomBetween(4.8, 9.8).toFixed(2)),
      delay: Number(randomBetween(-9.5, 0).toFixed(2)),
      twinkleScale: Number(randomBetween(1.02, 1.18).toFixed(3)),
      blur: size > 1.6 ? Number(randomBetween(0.1, 0.45).toFixed(2)) : 0,
      color: pickRandom(backgroundStarColors),
    };
  });
}

function createBrightStar(index: number, left: number, top: number): BrightStar {
  const shape = pickRandom(brightStarShapes);
  const isCross = shape === "cross";
  const isSix = shape === "six";
  const isPenta = shape === "penta";
  const maxOpacity = randomBetween(0.34, 0.62);
  const minOpacity = maxOpacity * randomBetween(0.18, 0.34);

  return {
    key: `bright-${index}`,
    left: Number(left.toFixed(2)),
    top: Number(top.toFixed(2)),
    size: Number(randomBetween(5.2, 8.4).toFixed(2)),
    core: Number((isPenta ? randomBetween(3.1, 4.3) : randomBetween(1.3, 2.4)).toFixed(2)),
    shapeSize: Number((
      isCross
        ? randomBetween(4.1, 5.2)
        : isSix
          ? randomBetween(4.8, 6.1)
          : isPenta
            ? randomBetween(4.3, 5.6)
            : randomBetween(1.3, 2.4)
    ).toFixed(2)),
    duration: Number(randomBetween(5.2, 10.6).toFixed(2)),
    delay: Number(randomBetween(-10, 0).toFixed(2)),
    color: pickRandom(brightStarColors),
    rotation: Number((shape === "dot" ? 0 : randomBetween(-24, 24)).toFixed(2)),
    shape,
    minOpacity: Number(minOpacity.toFixed(3)),
    midOpacity: Number((maxOpacity * randomBetween(0.45, 0.72)).toFixed(3)),
    maxOpacity: Number(maxOpacity.toFixed(3)),
    pulseScale: Number(randomBetween(1.04, 1.18).toFixed(3)),
  };
}

function createBrightStars(count = 12): BrightStar[] {
  const stars: BrightStar[] = [];
  const bandLoads = [0, 0, 0, 0, 0];

  for (let index = 0; index < count; index += 1) {
    let left = randomBetween(4, 96);
    let top = randomBetween(8, 84);
    let placed = false;

    for (let attempt = 0; attempt < 60; attempt += 1) {
      left = randomBetween(4, 96);
      top = randomBetween(8, 84);
      const band = Math.min(bandLoads.length - 1, Math.floor((top - 8) / 16));
      const sameRowCount = stars.filter((star) => Math.abs(star.top - top) < 8).length;
      const isCrowded = stars.some(
        (star) => Math.abs(star.left - left) < 9 && Math.abs(star.top - top) < 12,
      );

      if (bandLoads[band] >= 3 || sameRowCount >= 2 || isCrowded) {
        continue;
      }

      bandLoads[band] += 1;
      placed = true;
      break;
    }

    if (!placed) {
      left = randomBetween(6, 94);
      top = randomBetween(10, 82);
    }

    stars.push(createBrightStar(index, left, top));
  }

  return stars;
}

const backgroundStars = ref<BackgroundStar[]>(createBackgroundStars());
const brightStars = ref<BrightStar[]>(createBrightStars());

const headerEffectsEnabled = ref(true);
const auth = useAuthStore();
const loggedIn = computed(() => auth.sessionAdmin);
const loggingOut = ref(false);

async function handleLogout() {
  if (loggingOut.value) return;
  loggingOut.value = true;
  try {
    await logout();
  } catch {
    /* 即使接口失败也清本地状态 */
  }
  auth.clear();
  toast.success("已退出登录");
  loggingOut.value = false;
}

onMounted(async () => {
  if (!auth.loaded) {
    await auth.load();
  }
  try {
    const cfg = await publicApi.systemConfig();
    headerEffectsEnabled.value = cfg.header_effects_enabled ?? true;
  } catch {
    headerEffectsEnabled.value = true;
  }
});
</script>

<template>
  <header class="header">
    <div v-if="headerEffectsEnabled" class="header__sky" aria-hidden="true">
      <div class="theme-light-only sunlight-container">
        <div class="sun"></div>
        <div class="cloud-layer">
          <div class="cloud-band cloud-band--1"></div>
          <div class="cloud-band cloud-band--2"></div>
          <div class="cloud-wisp cloud-wisp--1"></div>
          <div class="cloud-wisp cloud-wisp--2"></div>
        </div>
        <div class="sun-wash"></div>
        <div class="light-fall">
          <div class="light-fall__ray light-fall__ray--1"></div>
          <div class="light-fall__ray light-fall__ray--2"></div>
          <div class="light-fall__ray light-fall__ray--3"></div>
        </div>
        <div class="sky-plane">
          <img src="/static/img/header-plane.png" alt="" />
        </div>
        <div class="beam-glow"></div>
        <div class="beam"></div>
        <div class="beam beam--2"></div>
        <div class="beam beam--3"></div>
        <div class="haze"></div>
        <div class="dandelion-cluster">
          <div class="orb-seed orb-seed--1">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M17.6 14.8 L16.1 31.2" stroke="rgba(255,255,255,0.72)" stroke-width="1.1" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.85" stroke-linecap="round">
                <path d="M17.4 14.8 L6 8.4" /><path d="M17.4 14.8 L10.4 3.9" /><path d="M17.4 14.8 L16.5 2.2" /><path d="M17.4 14.8 L23.1 3.4" /><path d="M17.4 14.8 L28.7 7.9" />
              </g>
            </svg>
          </div>
          <div class="orb-seed orb-seed--2">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M18.5 15.3 Q17.1 22.3 14.6 31.1" stroke="rgba(255,255,255,0.72)" stroke-width="1.06" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.82" stroke-linecap="round">
                <path d="M18.2 15 L8.6 9.8" /><path d="M18.2 15 L12.6 5.1" /><path d="M18.2 15 L19.1 3.5" /><path d="M18.2 15 L25.1 5.8" /><path d="M18.2 15 L28.8 9.9" />
              </g>
            </svg>
          </div>
          <div class="orb-seed orb-seed--3">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M17 15.4 Q18.7 22.8 21 30.9" stroke="rgba(255,255,255,0.72)" stroke-width="1.06" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.82" stroke-linecap="round">
                <path d="M17.2 15 L9.5 10.8" /><path d="M17.2 15 L13.6 5.7" /><path d="M17.2 15 L21.6 5.2" /><path d="M17.2 15 L27.2 8.4" /><path d="M17.2 15 L29.4 14.1" />
              </g>
            </svg>
          </div>
          <div class="near-seed near-seed--1">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M17.9 14.9 Q17 22.8 15 31" stroke="rgba(255,255,255,0.76)" stroke-width="1.1" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.94)" stroke-width="0.84" stroke-linecap="round">
                <path d="M17.8 14.9 L7.4 9.1" /><path d="M17.8 14.9 L11.6 4.8" /><path d="M17.8 14.9 L17.3 2.8" /><path d="M17.8 14.9 L24.1 4.4" /><path d="M17.8 14.9 L29.8 8.9" />
              </g>
            </svg>
          </div>
          <div class="near-seed near-seed--2">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M16.9 15.1 Q19.2 22.4 21.7 30.7" stroke="rgba(255,255,255,0.74)" stroke-width="1.08" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.94)" stroke-width="0.84" stroke-linecap="round">
                <path d="M17.1 15 L8.3 10.7" /><path d="M17.1 15 L13.6 5.7" /><path d="M17.1 15 L20.8 5" /><path d="M17.1 15 L26.3 7.7" /><path d="M17.1 15 L29.6 13.2" />
              </g>
            </svg>
          </div>
          <div class="seed seed--1">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M17.6 14.8 L16.1 31.2" stroke="rgba(255,255,255,0.72)" stroke-width="1.1" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.84" stroke-linecap="round">
                <path d="M17.4 14.8 L6 8.4" /><path d="M17.4 14.8 L10.4 3.9" /><path d="M17.4 14.8 L16.5 2.2" /><path d="M17.4 14.8 L23.1 3.4" /><path d="M17.4 14.8 L28.7 7.9" />
              </g>
            </svg>
          </div>
          <div class="seed seed--2">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M18.4 15.2 Q17 22.3 14.4 31.2" stroke="rgba(255,255,255,0.72)" stroke-width="1.06" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.82" stroke-linecap="round">
                <path d="M18.1 14.9 L8.7 9.7" /><path d="M18.1 14.9 L12.3 5" /><path d="M18.1 14.9 L19 3.4" /><path d="M18.1 14.9 L24.9 5.3" /><path d="M18.1 14.9 L28.9 9.7" />
              </g>
            </svg>
          </div>
          <div class="seed seed--3">
            <svg viewBox="0 0 34 34" fill="none">
              <path d="M17 15.1 Q18.9 22.6 21.3 31" stroke="rgba(255,255,255,0.72)" stroke-width="1.08" stroke-linecap="round" />
              <g stroke="rgba(255,255,255,0.92)" stroke-width="0.82" stroke-linecap="round">
                <path d="M17.2 15 L9.3 10.8" /><path d="M17.2 15 L13.7 5.7" /><path d="M17.2 15 L21.2 5.1" /><path d="M17.2 15 L27.1 8.1" /><path d="M17.2 15 L30.2 13.7" />
              </g>
            </svg>
          </div>
        </div>
      </div>

      <div class="theme-dark-only stars-container">
        <div class="star-layer">
          <div
            v-for="star in backgroundStars"
            :key="star.key"
            class="star-item"
            :style="{
              left: `${star.left}%`,
              top: `${star.top}%`,
              width: `${star.size}px`,
              height: `${star.size}px`,
              '--star-color': star.color,
              '--star-opacity-min': `${star.minOpacity}`,
              '--star-opacity-max': `${star.maxOpacity}`,
              '--twinkle-scale': `${star.twinkleScale}`,
              '--star-blur': `${star.blur}px`,
              animationDuration: `${star.duration}s`,
              animationDelay: `${star.delay}s`,
            }"
          ></div>
          <div
            v-for="star in brightStars"
            :key="star.key"
            :class="['bright-star', `star-${star.shape}`]"
            :style="{
              left: `${star.left}%`,
              top: `${star.top}%`,
              width: `${star.size}px`,
              height: `${star.size}px`,
              '--core': `${star.core}px`,
              '--shape-size': `${star.shapeSize}px`,
              '--color': star.color,
              '--star-rotation': `${star.rotation}deg`,
              '--star-opacity-min': `${star.minOpacity}`,
              '--star-opacity-mid': `${star.midOpacity}`,
              '--star-opacity-max': `${star.maxOpacity}`,
              '--pulse-scale': `${star.pulseScale}`,
              animationDuration: `${star.duration}s`,
              animationDelay: `${star.delay}s`,
            }"
          ></div>
        </div>
      </div>

      <div class="theme-dark-only meteors-container">
        <div class="meteor meteor--1"></div>
        <div class="meteor meteor--2"></div>
        <div class="meteor meteor--3"></div>
      </div>
    </div>
    <div class="header__inner container">
      <RouterLink to="/" class="header__brand">
        <img src="/static/img/logo.png" alt="MyPan" class="header__logo" />
      </RouterLink>

      <nav class="header__nav">
        <RouterLink v-if="!loggedIn" to="/login" class="header-auth" title="登录后台">
          <span class="header-auth__icon" aria-hidden="true">
            <SvgIcon name="sign-in" :size="15" />
          </span>
          <span class="header-auth__sep" aria-hidden="true" />
          <span class="header-auth__text">登录后台</span>
        </RouterLink>

        <div v-else class="header-auth">
          <button
            type="button"
            class="header-auth__icon-btn"
            title="退出登录"
            aria-label="退出登录"
            :disabled="loggingOut"
            @click="handleLogout"
          >
            <SvgIcon name="sign-out" :size="15" />
          </button>
          <span class="header-auth__sep" aria-hidden="true" />
          <RouterLink to="/admin" class="header-auth__text">管理后台</RouterLink>
        </div>
      </nav>
    </div>
  </header>
</template>

<style scoped>
.header {
  position: sticky;
  top: 0;
  z-index: 100;
  height: var(--header-height);
  min-height: var(--header-height);
  box-sizing: border-box;
  overflow: hidden;
  background: var(--brand-gradient-h);
  color: var(--text-on-brand);
  border-bottom: 1px solid rgba(255, 255, 255, 0.14);
  box-shadow: none;
}

.header__sky {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  overflow: hidden;
}

.theme-light-only { display: none; }
.theme-dark-only { display: none; }

:root[data-theme="light"] .theme-light-only { display: block; }
:root[data-theme="dark"] .theme-dark-only { display: block; }
:root[data-theme="dark"] .header {
  background:
    linear-gradient(180deg, rgba(0, 0, 0, 0.08), rgba(0, 0, 0, 0.12)),
    var(--brand-gradient-h);
  border-bottom-color: rgba(255, 255, 255, 0.05);
}

.sunlight-container {
  position: absolute;
  inset: 0;
  overflow: hidden;
  isolation: isolate;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.06), transparent 34%),
    radial-gradient(circle at 84% 8%, rgba(255, 240, 190, 0.2), transparent 28%);
}
.sunlight-container::before,
.sunlight-container::after {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.sunlight-container::before {
  background:
    radial-gradient(ellipse at 78% 4%, rgba(255, 247, 210, 0.22), rgba(255, 241, 199, 0.12) 26%, rgba(255, 255, 255, 0) 58%),
    linear-gradient(180deg, rgba(255, 248, 225, 0.08) 0%, rgba(255, 255, 255, 0) 40%);
  mix-blend-mode: screen;
  opacity: 0.92;
}
.sunlight-container::after {
  inset: 40% 0 0;
  background: linear-gradient(180deg, rgba(90, 116, 170, 0) 0%, rgba(82, 105, 150, 0.08) 60%, rgba(71, 92, 132, 0.12) 100%);
  opacity: 0.88;
}
.sun {
  position: absolute;
  top: -92px;
  right: 82px;
  width: 170px;
  height: 170px;
  border-radius: 50%;
  background:
    radial-gradient(circle at 50% 56%, rgba(255, 251, 236, 0.7) 0 16%, rgba(255, 246, 212, 0.24) 34%, rgba(255, 235, 180, 0.06) 54%, rgba(255, 231, 166, 0) 72%);
  filter: blur(2px);
  opacity: 0.46;
  animation: sun-breathe 10s ease-in-out infinite alternate;
}
.sun::before,
.sun::after {
  content: "";
  position: absolute;
  inset: -10px;
  border-radius: 50%;
}
.sun::before {
  background:
    conic-gradient(from 0deg, rgba(255, 245, 198, 0) 0deg, rgba(255, 245, 198, 0.12) 78deg, rgba(255, 248, 222, 0.22) 148deg, rgba(255, 243, 190, 0.07) 230deg, rgba(255, 245, 198, 0) 360deg);
  mix-blend-mode: screen;
  animation: sun-rotate 26s linear infinite;
}
.sun::after {
  inset: -28px;
  background: radial-gradient(circle, rgba(255, 247, 213, 0.16), transparent 68%);
  opacity: 0.36;
  animation: halo-pulse 14s ease-in-out infinite alternate;
}
.beam {
  --beam-width: 460px;
  --beam-angle: -12deg;
  position: absolute;
  top: 8px;
  right: 152px;
  width: var(--beam-width);
  height: 14px;
  border-radius: 999px;
  background: linear-gradient(90deg, rgba(255, 255, 255, 0) 0%, rgba(255, 255, 255, 0.18) 18%, rgba(255, 255, 246, 0.62) 48%, rgba(255, 237, 186, 0.32) 78%, rgba(255, 255, 255, 0) 100%);
  filter: blur(9px);
  opacity: 0.42;
  mix-blend-mode: screen;
  transform-origin: right center;
  animation: beam-sway 9s ease-in-out infinite alternate;
}
.beam::before,
.beam::after {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
}
.beam::before {
  background: linear-gradient(90deg, rgba(255, 255, 255, 0) 0%, rgba(255, 251, 229, 0.05) 24%, rgba(255, 247, 212, 0.34) 48%, rgba(255, 252, 236, 0.1) 72%, rgba(255, 255, 255, 0) 100%);
  filter: blur(14px);
  opacity: 0.36;
  animation: beam-bloom 8.4s ease-in-out infinite alternate;
}
.beam::after {
  inset: -2px -10%;
  background: linear-gradient(90deg, rgba(255, 255, 255, 0) 0%, rgba(255, 250, 216, 0) 24%, rgba(255, 252, 237, 0.58) 50%, rgba(255, 244, 201, 0) 76%, rgba(255, 255, 255, 0) 100%);
  filter: blur(7px);
  opacity: 0;
  transform: translateX(18%) scaleX(0.62);
  animation: beam-shimmer 11s ease-in-out infinite;
}
.beam--2 {
  --beam-width: 390px;
  --beam-angle: -18deg;
  top: 18px;
  right: 164px;
  height: 10px;
  opacity: 0.28;
  animation-delay: -3s;
}
.beam--3 {
  --beam-width: 310px;
  --beam-angle: -6deg;
  top: 14px;
  right: 138px;
  height: 8px;
  opacity: 0.18;
  animation-delay: -6s;
}
.beam-glow {
  position: absolute;
  top: 4px;
  right: 62px;
  width: 520px;
  height: 80px;
  border-radius: 999px;
  background: radial-gradient(ellipse at 78% 50%, rgba(255, 247, 213, 0.22), rgba(255, 244, 205, 0.1) 34%, rgba(255, 255, 255, 0) 72%);
  filter: blur(22px);
  opacity: 0.3;
  mix-blend-mode: screen;
  transform-origin: right center;
  animation: beam-glow-drift 14s ease-in-out infinite alternate;
  pointer-events: none;
}
.sun-wash {
  position: absolute;
  top: -40px;
  right: 52px;
  width: 680px;
  height: 190px;
  border-radius: 50%;
  background:
    radial-gradient(ellipse at 78% 12%, rgba(255, 248, 219, 0.32), rgba(255, 244, 210, 0.17) 28%, rgba(255, 255, 255, 0) 68%);
  filter: blur(20px);
  opacity: 0.46;
  mix-blend-mode: screen;
  transform-origin: 82% 8%;
  animation: sun-wash-drift 16s ease-in-out infinite alternate;
  pointer-events: none;
}
.cloud-layer {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 1;
  overflow: hidden;
}
.cloud-band {
  position: absolute;
  height: 44px;
  border-radius: 999px;
  background:
    linear-gradient(90deg, rgba(255, 255, 255, 0) 0%, rgba(255, 255, 255, 0.16) 18%, rgba(255, 255, 255, 0.24) 46%, rgba(255, 255, 255, 0.1) 72%, rgba(255, 255, 255, 0) 100%);
  filter: blur(14px);
  mix-blend-mode: screen;
  opacity: 0;
  animation: cloud-band-drift var(--cloud-duration, 34s) ease-in-out infinite;
  animation-delay: var(--cloud-delay, 0s);
}
.cloud-band::before,
.cloud-band::after {
  content: "";
  position: absolute;
  inset: -8px 2%;
  border-radius: inherit;
  background:
    radial-gradient(ellipse at 20% 54%, rgba(255, 255, 255, 0.18), rgba(255, 255, 255, 0) 56%),
    radial-gradient(ellipse at 48% 48%, rgba(255, 255, 255, 0.16), rgba(255, 255, 255, 0) 58%),
    radial-gradient(ellipse at 76% 44%, rgba(255, 255, 255, 0.14), rgba(255, 255, 255, 0) 54%);
  filter: blur(12px);
}
.cloud-band::before {
  transform: translate(-4%, 8%) scale(1.08, 0.84);
  opacity: 0.82;
}
.cloud-band::after {
  transform: translate(6%, -10%) scale(0.92, 0.72);
  opacity: 0.54;
}
.cloud-band--1 {
  top: 10px;
  left: 7%;
  width: 220px;
  --cloud-duration: 36s;
  --cloud-delay: -6s;
}
.cloud-band--2 {
  top: 38px;
  left: 24%;
  width: 180px;
  height: 28px;
  --cloud-duration: 32s;
  --cloud-delay: -17s;
}
.cloud-wisp {
  position: absolute;
  width: 140px;
  height: 22px;
  border-radius: 999px;
  background: linear-gradient(90deg, rgba(255, 255, 255, 0), rgba(255, 255, 255, 0.12) 40%, rgba(255, 255, 255, 0) 100%);
  filter: blur(8px);
  mix-blend-mode: screen;
  opacity: 0;
  animation: cloud-wisp-drift var(--wisp-duration, 24s) ease-in-out infinite;
  animation-delay: var(--wisp-delay, 0s);
}
.cloud-wisp--1 {
  top: 18px;
  left: 42%;
  width: 150px;
  --wisp-duration: 26s;
  --wisp-delay: -8s;
}
.cloud-wisp--2 {
  top: 52px;
  left: 15%;
  width: 108px;
  --wisp-duration: 22s;
  --wisp-delay: -13s;
}
.light-fall {
  position: absolute;
  top: -54px;
  right: -46px;
  width: 660px;
  height: 160px;
  pointer-events: none;
  z-index: 1;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(90deg, transparent 0%, rgba(0, 0, 0, 0.9) 16%, #000 58%, rgba(0, 0, 0, 0.92) 88%, transparent 100%);
  mask-image: linear-gradient(90deg, transparent 0%, rgba(0, 0, 0, 0.9) 16%, #000 58%, rgba(0, 0, 0, 0.92) 88%, transparent 100%);
}
.light-fall__ray {
  position: absolute;
  top: -8%;
  right: 0;
  width: var(--ray-width, 220px);
  height: 132%;
  border-radius: 999px;
  background: linear-gradient(180deg, rgba(255, 251, 230, 0.36) 0%, rgba(255, 248, 219, 0.16) 24%, rgba(255, 247, 223, 0.08) 48%, rgba(255, 255, 255, 0) 100%);
  filter: blur(18px);
  mix-blend-mode: screen;
  opacity: 0;
  transform-origin: top center;
  transform: translate3d(0, 0, 0) rotate(var(--ray-angle, -18deg)) scaleY(0.92);
  animation: light-fall-drift var(--ray-duration, 18s) ease-in-out infinite;
  animation-delay: var(--ray-delay, 0s);
}
.light-fall__ray::after {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: linear-gradient(180deg, rgba(255, 251, 234, 0.24) 0%, rgba(255, 248, 219, 0.1) 32%, rgba(255, 255, 255, 0) 100%);
  filter: blur(10px);
  opacity: 0.54;
  animation: light-fall-pulse calc(var(--ray-duration, 18s) * 0.7) ease-in-out infinite;
}
.light-fall__ray--1 { --ray-width: 190px; --ray-angle: -15deg; --ray-duration: 17s; --ray-delay: -2s; right: 34px; }
.light-fall__ray--2 { --ray-width: 146px; --ray-angle: -21deg; --ray-duration: 20s; --ray-delay: -8s; right: 150px; }
.light-fall__ray--3 { --ray-width: 110px; --ray-angle: -10deg; --ray-duration: 15s; --ray-delay: -5s; right: 244px; }
.sky-plane {
  position: absolute;
  top: 64px;
  left: 0;
  width: 44px;
  height: auto;
  opacity: 0;
  z-index: 4;
  filter: saturate(0.9) brightness(1.04) drop-shadow(0 0 4px rgba(255, 255, 255, 0.16));
  transform: rotate(-4deg);
  transform-origin: center center;
  animation: plane-glide 34s linear infinite -6s;
}
.sky-plane img {
  display: block;
  width: 100%;
  height: auto;
}
.haze {
  position: absolute;
  inset: auto -6% -18% -6%;
  height: 52%;
  background:
    radial-gradient(ellipse at 18% 100%, rgba(255, 255, 255, 0.18), transparent 44%),
    radial-gradient(ellipse at 54% 100%, rgba(255, 255, 255, 0.14), transparent 52%),
    radial-gradient(ellipse at 84% 100%, rgba(255, 255, 255, 0.12), transparent 42%);
  filter: blur(18px);
  opacity: 0.56;
}
.dandelion-cluster {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 3;
}
.seed svg,
.orb-seed svg,
.near-seed svg {
  width: 100%;
  height: 100%;
  overflow: visible;
}
.orb-seed {
  position: absolute;
  width: 18px;
  height: 18px;
  opacity: 0;
  transform-origin: 50% 78%;
  filter: drop-shadow(0 0 6px rgba(255, 245, 215, 0.22));
  animation: orb-seed-peel var(--duration, 9s) ease-in-out infinite;
  animation-delay: var(--delay, 0s);
}
.orb-seed--1 { right: 158px; top: 24px; --duration: 8.2s; --delay: -1.8s; }
.orb-seed--2 { right: 132px; top: 10px; --duration: 9.4s; --delay: -4.2s; transform: scale(0.84); }
.orb-seed--3 { right: 112px; top: 36px; --duration: 7.6s; --delay: -3.1s; transform: scale(0.76); }
.near-seed,
.seed {
  position: absolute;
  opacity: 0;
  filter: drop-shadow(0 0 8px rgba(255, 246, 210, 0.18));
}
.near-seed {
  width: 20px;
  height: 20px;
  transform-origin: 50% 72%;
  animation: near-seed-drift var(--duration, 15s) ease-in-out infinite;
  animation-delay: var(--delay, 0s);
}
.near-seed--1 { left: 56%; top: 8px; --duration: 14s; --delay: -3s; transform: scale(0.92); }
.near-seed--2 { left: 63%; top: 12px; --duration: 16s; --delay: -9s; transform: scale(0.8); }
.seed {
  width: 16px;
  height: 16px;
  transform-origin: 50% 70%;
  animation: seed-drift var(--duration, 18s) linear infinite;
  animation-delay: var(--delay, 0s);
}
.seed--1 { left: 73%; top: 6px; --duration: 17s; --delay: -2s; }
.seed--2 { left: 66%; top: 16px; --duration: 19s; --delay: -8s; transform: scale(0.82); }
.seed--3 { left: 59%; top: 28px; --duration: 21s; --delay: -11s; transform: scale(0.9); }

@keyframes sun-breathe {
  0% { transform: scale(0.97); opacity: 0.36; }
  100% { transform: scale(1.03); opacity: 0.5; }
}
@keyframes halo-pulse {
  0% { transform: scale(0.96); opacity: 0.16; }
  100% { transform: scale(1.06); opacity: 0.34; }
}
@keyframes sun-rotate {
  0% { transform: rotate(0deg) scale(0.98); opacity: 0.16; }
  50% { opacity: 0.26; }
  100% { transform: rotate(360deg) scale(1.03); opacity: 0.18; }
}
@keyframes beam-sway {
  0% { transform: translate(22px, -5px) rotate(var(--beam-angle)) scaleX(0.96); opacity: 0.16; filter: blur(11px); }
  34% { opacity: 0.3; filter: blur(9px); }
  66% { opacity: 0.38; }
  100% { transform: translate(-26px, 7px) rotate(var(--beam-angle)) scaleX(1.04); opacity: 0.24; filter: blur(7px); }
}
@keyframes beam-bloom {
  0% { opacity: 0.14; transform: scaleX(0.9) translateX(14px); }
  50% { opacity: 0.28; }
  100% { opacity: 0.1; transform: scaleX(1.08) translateX(-22px); }
}
@keyframes beam-shimmer {
  0%, 100% { opacity: 0; transform: translateX(20%) scaleX(0.58); }
  18% { opacity: 0.06; }
  48% { opacity: 0.34; transform: translateX(-2%) scaleX(0.92); }
  74% { opacity: 0.12; }
  100% { opacity: 0; transform: translateX(-26%) scaleX(1.08); }
}
@keyframes beam-glow-drift {
  0% { opacity: 0.14; transform: translate(18px, -4px) rotate(-10deg) scale(0.94); }
  50% { opacity: 0.32; }
  100% { opacity: 0.14; transform: translate(-34px, 8px) rotate(-14deg) scale(1.06); }
}
@keyframes sun-wash-drift {
  0% { opacity: 0.26; transform: translate(18px, -8px) rotate(-10deg) scale(0.94); }
  45% { opacity: 0.46; }
  100% { opacity: 0.22; transform: translate(-28px, 14px) rotate(-14deg) scale(1.06); }
}
@keyframes light-fall-drift {
  0% {
    opacity: 0.12;
    transform: translate(34px, -18px) rotate(var(--ray-angle, -18deg)) scaleY(0.9);
    filter: blur(22px);
  }
  24% {
    opacity: 0.38;
  }
  58% {
    opacity: 0.56;
    transform: translate(-10px, 8px) rotate(calc(var(--ray-angle, -18deg) - 1.6deg)) scaleY(1.02);
    filter: blur(18px);
  }
  100% {
    opacity: 0.18;
    transform: translate(-54px, 24px) rotate(calc(var(--ray-angle, -18deg) - 3deg)) scaleY(1.08);
    filter: blur(26px);
  }
}
@keyframes light-fall-pulse {
  0%, 100% { opacity: 0.28; transform: scaleY(0.94); }
  50% { opacity: 0.72; transform: scaleY(1.04); }
}
@keyframes cloud-band-drift {
  0% { opacity: 0.08; transform: translate3d(42px, -8px, 0) scale(0.96, 0.9) skewX(-10deg); }
  24% { opacity: 0.16; }
  56% { opacity: 0.26; transform: translate3d(-10px, 4px, 0) scale(1.04, 1) skewX(-14deg); }
  100% { opacity: 0.08; transform: translate3d(-72px, 10px, 0) scale(1.08, 1.06) skewX(-18deg); }
}
@keyframes cloud-wisp-drift {
  0% { opacity: 0.04; transform: translate3d(28px, -6px, 0) scaleX(0.92) skewX(-16deg); }
  36% { opacity: 0.14; }
  68% { opacity: 0.18; transform: translate3d(-18px, 6px, 0) scaleX(1.06) skewX(-22deg); }
  100% { opacity: 0.04; transform: translate3d(-58px, 12px, 0) scaleX(1.14) skewX(-28deg); }
}
@keyframes plane-glide {
  0% { opacity: 0; transform: translate(-120px, 30px) rotate(-4deg); }
  12% { opacity: 0.16; }
  70% { opacity: 0.12; transform: translate(420px, -8px) rotate(-4deg); }
  100% { opacity: 0; transform: translate(620px, -42px) rotate(-4deg); }
}
@keyframes orb-seed-peel {
  0% {
    opacity: 0.16;
    transform: translate(0, 0) scale(var(--scale, 1)) rotate(-10deg);
  }
  18% {
    opacity: 0.84;
  }
  58% {
    opacity: 0.62;
    transform: translate(-22px, -10px) scale(var(--scale, 1)) rotate(6deg);
  }
  100% {
    opacity: 0;
    transform: translate(-60px, 14px) scale(var(--scale, 1)) rotate(18deg);
  }
}
@keyframes near-seed-drift {
  0% { opacity: 0; transform: translate(12px, -8px) scale(var(--scale, 1)) rotate(-12deg); }
  20% { opacity: 0.72; }
  58% { opacity: 0.58; transform: translate(-30px, 16px) scale(var(--scale, 1)) rotate(10deg); }
  100% { opacity: 0; transform: translate(-96px, 46px) scale(var(--scale, 1)) rotate(18deg); }
}
@keyframes seed-drift {
  0% {
    opacity: 0;
    transform: translate(34px, -10px) scale(var(--scale, 1)) rotate(-10deg);
  }
  8% {
    opacity: 0.74;
  }
  34% {
    opacity: 0.58;
    transform: translate(-40px, 16px) scale(var(--scale, 1)) rotate(8deg);
  }
  68% {
    opacity: 0.34;
    transform: translate(-118px, 44px) scale(var(--scale, 1)) rotate(18deg);
  }
  100% {
    opacity: 0;
    transform: translate(-184px, 64px) scale(var(--scale, 1)) rotate(24deg);
  }
}

.stars-container {
  position: absolute;
  inset: 0;
  overflow: hidden;
}
.star-layer {
  position: absolute;
  inset: 0;
  pointer-events: none;
}
.star-item {
  position: absolute;
  left: 0;
  top: 0;
  border-radius: 50%;
  background: var(--star-color, rgba(255, 255, 255, 0.9));
  filter: blur(calc(var(--star-blur, 0) * 0.55));
  transform: translate(-50%, -50%);
  opacity: var(--star-opacity-min, 0.18);
  animation: star-random-breathe 6.4s ease-in-out infinite;
}
.bright-star {
  position: absolute;
  left: 0;
  top: 0;
  width: var(--size, 10px);
  height: var(--size, 10px);
  opacity: var(--star-opacity-min, 0.12);
  transform: translate(-50%, -50%);
  animation: bright-star-pulse var(--duration, 5.2s) ease-in-out infinite;
  animation-delay: var(--delay, 0s);
}
.bright-star::before {
  content: "";
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%) rotate(var(--star-rotation, 0deg));
  pointer-events: none;
}
.star-dot::before {
  width: var(--core, 1.8px);
  height: var(--core, 1.8px);
  border-radius: 50%;
  background: var(--color, rgba(255, 255, 255, 0.94));
}
.star-cross::before {
  width: var(--shape-size, 6px);
  height: var(--shape-size, 6px);
  background:
    linear-gradient(90deg, transparent 0%, transparent 44%, var(--color, rgba(255, 255, 255, 0.94)) 44%, var(--color, rgba(255, 255, 255, 0.94)) 56%, transparent 56%, transparent 100%),
    linear-gradient(180deg, transparent 0%, transparent 44%, var(--color, rgba(255, 255, 255, 0.94)) 44%, var(--color, rgba(255, 255, 255, 0.94)) 56%, transparent 56%, transparent 100%);
}
.star-six::before {
  width: var(--shape-size, 6px);
  height: var(--shape-size, 6px);
  background: var(--color, rgba(255, 255, 255, 0.94));
  clip-path: polygon(
    50% 0%,
    61% 28%,
    88% 18%,
    72% 45%,
    94% 68%,
    63% 69%,
    50% 100%,
    37% 69%,
    6% 68%,
    28% 45%,
    12% 18%,
    39% 28%
  );
}
.star-penta::before {
  width: var(--shape-size, 4.8px);
  height: var(--shape-size, 4.8px);
  background: var(--color, rgba(255, 255, 255, 0.94));
  clip-path: polygon(50% 0%, 62% 34%, 98% 36%, 70% 57%, 79% 92%, 50% 72%, 21% 92%, 30% 57%, 2% 36%, 38% 34%);
}
@keyframes star-random-breathe {
  0%, 100% {
    opacity: var(--star-opacity-min, 0.18);
    transform: translate(-50%, -50%) scale(0.92);
  }
  48% {
    opacity: var(--star-opacity-max, 0.48);
    transform: translate(-50%, -50%) scale(var(--twinkle-scale, 1.12));
  }
}
@keyframes bright-star-pulse {
  0%, 100% {
    opacity: var(--star-opacity-min, 0.12);
    transform: translate(-50%, -50%) scale(0.92);
  }
  26% {
    opacity: var(--star-opacity-mid, 0.24);
  }
  58% {
    opacity: var(--star-opacity-max, 0.48);
    transform: translate(-50%, -50%) scale(var(--pulse-scale, 1.1));
  }
}

.meteors-container {
  position: absolute;
  inset: 0;
  overflow: hidden;
}
.meteor {
  position: absolute;
  width: 120px;
  height: 2px;
  background: linear-gradient(to right, rgba(255,255,255,1) 0%, rgba(255,255,255,0) 100%);
  transform-origin: left center;
  opacity: 0;
}
.meteor::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translate(-50%, -50%);
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 0 10px 3px #fff, 0 0 20px 6px #8bbdff;
}

.meteor--1 { top: 20%; left: 80%; animation: meteor-fall 8s linear infinite 1s; }
.meteor--2 { top: -10%; left: 60%; animation: meteor-fall 12s linear infinite 5s; }
.meteor--3 { top: 40%; left: 90%; animation: meteor-fall 15s linear infinite 9s; transform: scale(0.7); }

@keyframes meteor-fall {
  0% { opacity: 0; transform: rotate(-35deg) translateX(100px); }
  5% { opacity: 1; }
  15% { opacity: 0; transform: rotate(-35deg) translateX(-400px); }
  100% { opacity: 0; transform: rotate(-35deg) translateX(-400px); }
}

.header__inner {
  position: relative;
  z-index: 1;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.header__brand {
  display: flex;
  align-items: center;
}
.header__logo {
  height: var(--header-logo-height);
  width: auto;
  margin-left: 10px;
  display: block;
}
.header__nav {
  display: flex;
  align-items: center;
  gap: 10px;
}

.header-auth {
  display: inline-flex;
  align-items: center;
  gap: 0;
  min-height: 32px;
  padding: 0 11px 0 9px;
  border-radius: var(--radius-sm);
  background: rgba(255, 255, 255, 0.9);
  color: #64748b;
  font-size: 13px;
  font-weight: 500;
  line-height: 1;
  text-decoration: none;
  border: 1px solid rgba(255, 255, 255, 0.55);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.1);
  backdrop-filter: blur(8px);
}
.header-auth__icon,
.header-auth__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  color: #64748b;
  line-height: 0;
}
.header-auth__icon-btn {
  margin: 0;
  padding: 0;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  cursor: pointer;
}
.header-auth__icon-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.header-auth__sep {
  width: 1px;
  height: 12px;
  margin: 0 7px;
  background: #cbd5e1;
  flex-shrink: 0;
}
.header-auth__text {
  color: #64748b;
  text-decoration: none;
  white-space: nowrap;
  letter-spacing: 0.01em;
}
</style>
