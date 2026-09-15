<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { DriverInfo } from "@/api/types";
import DriverIcon from "@/components/driver/DriverIcon.vue";
import AppButton from "@/components/base/AppButton.vue";

const ITEM_H = 196;

type SlideItem = { driver: DriverInfo; key: string; realIndex: number };

const props = defineProps<{ drivers: DriverInfo[]; modelValue: string }>();
const emit = defineEmits<{ "update:modelValue": [string]; next: [] }>();

const VIEW_KEY = "litepan_add_account_driver_view_mode";
const saved = localStorage.getItem(VIEW_KEY);
const viewMode = ref<"carousel" | "grid">(
  saved === "carousel" || saved === "grid" ? saved : "carousel",
);
const searchQuery = ref("");
const trackIndex = ref(0);
const trackTransition = ref(true);
const trackResetting = ref(false);

const filtered = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return props.drivers;
  return props.drivers.filter(
    (d) =>
      d.display_name.toLowerCase().includes(q) ||
      d.name.toLowerCase().includes(q) ||
      (d.description || "").toLowerCase().includes(q),
  );
});

const slides = computed((): SlideItem[] => {
  const list = filtered.value;
  if (list.length <= 1) {
    return list.map((driver, realIndex) => ({ driver, key: driver.name, realIndex }));
  }
  const first = list[0];
  const last = list[list.length - 1];
  return [
    { driver: last, key: `clone-head-${last.name}`, realIndex: list.length - 1 },
    ...list.map((driver, realIndex) => ({ driver, key: driver.name, realIndex })),
    { driver: first, key: `clone-tail-${first.name}`, realIndex: 0 },
  ];
});

const positionCur = computed(() => {
  const slide = slides.value[trackIndex.value];
  return slide ? slide.realIndex + 1 : 0;
});

function syncTrackFromModel() {
  const list = filtered.value;
  if (!list.length) {
    trackIndex.value = 0;
    emit("update:modelValue", "");
    return;
  }
  let idx = list.findIndex((d) => d.name === props.modelValue);
  if (idx < 0) idx = 0;
  trackIndex.value = list.length <= 1 ? idx : idx + 1;
  if (list[idx] && list[idx].name !== props.modelValue) {
    emit("update:modelValue", list[idx].name);
  }
}

watch(() => props.drivers, syncTrackFromModel, { immediate: true });
watch(filtered, () => {
  syncTrackFromModel();
});

function selectDriver(name: string, realIndex?: number) {
  const list = filtered.value;
  if (realIndex !== undefined) {
    trackIndex.value = list.length <= 1 ? realIndex : realIndex + 1;
  } else {
    const idx = list.findIndex((d) => d.name === name);
    if (idx >= 0) trackIndex.value = list.length <= 1 ? idx : idx + 1;
  }
  emit("update:modelValue", name);
}

function move(delta: number) {
  const n = filtered.value.length;
  if (n <= 1 || trackResetting.value) return;
  trackTransition.value = true;
  trackIndex.value += delta;
  const slide = slides.value[trackIndex.value];
  if (slide) emit("update:modelValue", slide.driver.name);
}

function resetTrackIndex(next: number) {
  trackResetting.value = true;
  trackTransition.value = false;
  trackIndex.value = next;
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      trackTransition.value = true;
      trackResetting.value = false;
    });
  });
}

function onTrackTransitionEnd(e: TransitionEvent) {
  if (trackResetting.value) return;
  if (e.target !== e.currentTarget || e.propertyName !== "transform") return;
  const n = filtered.value.length;
  if (n <= 1) return;
  if (trackIndex.value === 0) {
    resetTrackIndex(n);
  } else if (trackIndex.value === n + 1) {
    resetTrackIndex(1);
  }
}

function toggleView() {
  viewMode.value = viewMode.value === "carousel" ? "grid" : "carousel";
  localStorage.setItem(VIEW_KEY, viewMode.value);
}

function clearSearch() {
  searchQuery.value = "";
}

const canNext = computed(() => Boolean(props.modelValue));

function driverDescription(driver: DriverInfo) {
  return driver.description || "云存储服务，支持文件浏览与管理";
}

function driverCardTags(driver: DriverInfo) {
  return (driver.card_tags || []).map((tag) => String(tag).trim()).filter(Boolean).slice(0, 4);
}

function driverAuthLabel(driver: DriverInfo) {
  if (driver.auth_label) return driver.auth_label;
  if (driver.supports_oauth) return "OAuth";
  if (driver.supports_qr_login) return "扫码";
  if (driver.auth_type === "cookie") return "Cookie";
  if (driver.auth_type === "token") return "Token";
  if (driver.auth_type === "none") return "本地";
  return "认证";
}
</script>

<template>
  <div class="picker">
    <div class="picker__viewport">
      <div v-show="viewMode === 'carousel'" class="carousel">
        <div
          class="carousel__track"
          :class="{ 'carousel__track--instant': !trackTransition }"
          :style="{ transform: `translateY(-${trackIndex * ITEM_H}px)` }"
          @transitionend="onTrackTransitionEnd"
        >
          <div
            v-for="slide in slides"
            :key="slide.key"
            class="carousel__item"
            @click="selectDriver(slide.driver.name, slide.realIndex)"
          >
            <div
              class="carousel__card"
              :class="{ selected: slide.driver.name === modelValue }"
              :style="{ '--driver-color': slide.driver.card_color }"
            >
              <div class="carousel__content">
                <DriverIcon
                  :logo="slide.driver.card_logo"
                  :color="slide.driver.card_color"
                  :name="slide.driver.display_name"
                  :size="68"
                />
                <div class="carousel__info">
                  <div class="carousel__title-row">
                    <h3>{{ slide.driver.display_name }}</h3>
                    <span
                      v-if="slide.driver.internal_experimental"
                      class="carousel__exp"
                      title="内部实验性驱动，仅供内部测试，可能随时失效"
                    >
                      内部实验性
                    </span>
                    <span class="carousel__auth">{{ driverAuthLabel(slide.driver) }}</span>
                  </div>
                  <p>{{ driverDescription(slide.driver) }}</p>
                  <div v-if="driverCardTags(slide.driver).length" class="carousel__tags">
                    <span
                      v-for="tag in driverCardTags(slide.driver)"
                      :key="tag"
                      class="carousel__tag"
                    >
                      {{ tag }}
                    </span>
                  </div>
                </div>
              </div>
              <div v-if="filtered.length > 1" class="carousel__controls">
                <button type="button" class="nav-arrow" @click.stop="move(-1)">▲</button>
                <div class="position-indicator">
                  <span class="position-text">{{ positionCur }}</span>
                  <div class="position-divider" />
                  <span class="total-text">{{ filtered.length }}</span>
                </div>
                <button type="button" class="nav-arrow" @click.stop="move(1)">▼</button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-show="viewMode === 'grid'" class="grid">
        <button
          v-for="(driver, index) in filtered"
          :key="driver.name"
          type="button"
          class="mini-card"
          :class="{ selected: driver.name === modelValue }"
          @click="selectDriver(driver.name, index)"
        >
          <DriverIcon
            :logo="driver.card_logo"
            :color="driver.card_color"
            :name="driver.display_name"
            :size="30"
          />
          <span class="mini-card__name">{{ driver.display_name }}</span>
          <span v-if="driver.internal_experimental" class="mini-card__exp">实验</span>
        </button>
        <div v-if="!filtered.length" class="grid__empty">未找到匹配的驱动</div>
      </div>
    </div>

    <div class="actions">
      <div class="actions__search">
        <button
          type="button"
          class="view-toggle"
          :title="viewMode === 'carousel' ? '切换到卡片视图' : '切换到翻动视图'"
          @click="toggleView"
        >
          <svg v-if="viewMode === 'carousel'" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
            <rect x="1" y="1" width="6" height="6" rx="1" />
            <rect x="9" y="1" width="6" height="6" rx="1" />
            <rect x="1" y="9" width="6" height="6" rx="1" />
            <rect x="9" y="9" width="6" height="6" rx="1" />
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
            <path d="M2 5h12v1.5H2V5zm0 5h12V11.5H2V11z" />
            <path d="M5 2.5 2 5.5l3 3V2.5zm6 0v6l3-3-3-3z" />
          </svg>
        </button>
        <div class="search-box">
          <span class="search-box__icon">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="7" />
              <path d="M20 20l-4-4" />
            </svg>
          </span>
          <input
            v-model="searchQuery"
            type="text"
            class="search-box__input"
            placeholder="搜索驱动..."
          />
          <button v-if="searchQuery" type="button" class="search-box__clear" @click="clearSearch">×</button>
        </div>
        <span v-if="searchQuery" class="search-hint">找到 {{ filtered.length }} 个匹配的驱动</span>
      </div>
      <AppButton variant="primary" :disabled="!canNext" @click="emit('next')">
        下一步 →
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.picker__viewport {
  height: 196px;
  flex-shrink: 0;
}

.carousel {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  border-radius: 12px;
  isolation: isolate;
}
.carousel__track {
  display: flex;
  flex-direction: column;
  transition: transform 0.42s cubic-bezier(0.22, 1, 0.36, 1);
  will-change: transform;
  transform: translateZ(0);
  backface-visibility: hidden;
}
.carousel__track--instant {
  transition: none;
}
.carousel__item {
  height: 196px;
  flex-shrink: 0;
  contain: paint;
}
.carousel__card {
  position: relative;
  width: 100%;
  height: 100%;
  background:
    radial-gradient(ellipse at 16% 28%, rgba(255, 255, 255, 0.94), transparent 24%),
    radial-gradient(ellipse at 76% 18%, color-mix(in srgb, var(--driver-color, var(--brand)) 9%, transparent), transparent 30%),
    radial-gradient(ellipse at 42% 92%, rgba(255, 255, 255, 0.7), transparent 24%),
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--driver-color, var(--brand)) 4%, var(--surface)) 0%,
      color-mix(in srgb, var(--brand) 8%, var(--surface-sunken)) 58%,
      color-mix(in srgb, var(--driver-color, var(--brand)) 7%, var(--surface)) 100%
    );
  border: 2px solid var(--border);
  border-radius: 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  padding: 20px 38px;
  box-sizing: border-box;
  transition: border-color 0.3s ease, background 0.3s ease, box-shadow 0.3s ease;
  overflow: hidden;
  transform: translateZ(0);
  backface-visibility: hidden;
  contain: paint;
}
.carousel__card:hover {
  border-color: var(--brand);
  box-shadow: var(--shadow-card);
}
.carousel__card.selected {
  border-color: var(--brand);
}
.carousel__content {
  position: relative;
  z-index: 1;
  flex: 1;
  display: flex;
  align-items: center;
  gap: 24px;
  min-width: 0;
  transform: translateZ(0);
  backface-visibility: hidden;
}
.carousel__title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  margin-bottom: 7px;
}
.carousel__info h3 {
  margin: 0;
  font-size: 24px;
  font-weight: 700;
  color: var(--text);
  line-height: 1.15;
}
.carousel__auth {
  flex-shrink: 0;
  padding: 3px 8px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--driver-color, var(--brand)) 14%, rgba(255, 255, 255, 0.8));
  color: color-mix(in srgb, var(--driver-color, var(--brand)) 82%, var(--text));
  font-size: 11.5px;
  font-weight: 700;
  line-height: 1.2;
}

.carousel__exp {
  flex-shrink: 0;
  padding: 3px 8px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, #eab308 16%, rgba(255, 255, 255, 0.8));
  color: #a16207;
  font-size: 11.5px;
  font-weight: 700;
  line-height: 1.2;
}
.carousel__info p {
  margin: 0;
  color: var(--text-muted);
  font-size: 13.5px;
  line-height: 1.45;
  max-width: 460px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.carousel__tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 10px;
}
.carousel__tag {
  height: 22px;
  display: inline-flex;
  align-items: center;
  padding: 0 8px;
  border-radius: var(--radius-pill);
  background: rgba(255, 255, 255, 0.78);
  border: 1px solid color-mix(in srgb, var(--driver-color, var(--brand)) 16%, var(--border));
  color: color-mix(in srgb, var(--driver-color, var(--brand)) 62%, var(--text-muted));
  font-size: 11px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(30, 55, 95, 0.05);
}
.carousel__controls {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding-left: 30px;
  opacity: 0.7;
}
.carousel__card:hover .carousel__controls {
  opacity: 1;
}
.nav-arrow {
  width: 34px;
  height: 34px;
  border: none;
  border-radius: 50%;
  background: var(--surface);
  color: var(--brand);
  cursor: pointer;
  font-size: 13px;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: var(--shadow-soft);
  transition: all 0.3s ease;
}
.nav-arrow:hover {
  background: var(--brand);
  color: var(--text-on-brand);
  transform: scale(1.1);
}

.position-indicator {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--brand);
  min-width: 20px;
}
.position-text {
  font-size: 15px;
  font-weight: 700;
  color: var(--brand);
  line-height: 1;
}
.position-divider {
  width: 12px;
  height: 1px;
  background: color-mix(in srgb, var(--brand) 40%, transparent);
  margin: 2px 0;
}
.total-text {
  font-size: 12px;
  font-weight: 500;
  color: color-mix(in srgb, var(--brand) 70%, var(--text-muted));
  line-height: 1;
}

.grid {
  height: 100%;
  overflow-y: auto;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  grid-auto-rows: 60px;
  align-content: start;
  gap: 8px;
  padding-right: 2px;
}
.mini-card {
  height: 60px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface-sunken);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 9px;
  cursor: pointer;
  text-align: left;
  transition: all 0.25s ease;
  overflow: hidden;
}
.mini-card:hover {
  border-color: var(--border);
  background: var(--surface);
  box-shadow: var(--shadow-card);
}
.mini-card.selected {
  border-color: var(--brand);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--brand) 12%, transparent);
}
.mini-card__name {
  min-width: 0;
  color: var(--text);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.25;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.mini-card__exp {
  flex: none;
  padding: 1px 6px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, #eab308 16%, rgba(255, 255, 255, 0.8));
  color: #a16207;
  font-size: 10px;
  font-weight: 700;
}
.grid__empty {
  grid-column: 1 / -1;
  height: 100%;
  min-height: 0;
  border: 2px dashed var(--border);
  border-radius: 12px;
  color: var(--text-muted);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
}

.actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: 8px;
  padding-bottom: 0;
  flex-shrink: 0;
}
.actions__search {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
}
.view-toggle {
  width: 40px;
  height: 40px;
  border: 2px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  color: var(--brand);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.25s ease;
}
.view-toggle:hover {
  border-color: var(--brand);
  background: color-mix(in srgb, var(--brand) 8%, var(--surface));
}
.search-box {
  position: relative;
  width: 240px;
  flex-shrink: 0;
}
.search-box__icon {
  position: absolute;
  left: 14px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-muted);
  display: flex;
  pointer-events: none;
}
.search-box__input {
  width: 100%;
  height: 40px;
  padding: 10px 36px 10px 42px;
  border: 2px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
  color: var(--text);
  background: var(--surface);
  box-sizing: border-box;
}
.search-box__input::placeholder {
  color: var(--text-muted);
}
.search-box__input:focus {
  outline: none;
  border-color: var(--brand);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--brand) 15%, transparent);
}
.search-box__clear {
  position: absolute;
  right: 10px;
  top: 50%;
  transform: translateY(-50%);
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: none;
  background: var(--danger);
  color: var(--text-on-brand);
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
}
.search-hint {
  font-size: 13px;
  color: var(--text-muted);
  white-space: nowrap;
}

@media (max-width: 640px) {
  .picker {
    gap: 8px;
  }

  .carousel__card {
    padding: 18px 48px 18px 22px;
  }

  .carousel__content {
    gap: 18px;
  }

  .carousel__content :deep(.driver-icon) {
    width: 58px !important;
    height: 58px !important;
    flex: 0 0 58px;
  }

  .carousel__title-row {
    margin-bottom: 0;
  }

  .carousel__info h3 {
    font-size: 22px;
    line-height: 1.16;
    word-break: break-word;
  }

  .carousel__auth,
  .carousel__info p,
  .carousel__tags {
    display: none;
  }

  .carousel__controls {
    position: absolute;
    right: 12px;
    top: 50%;
    transform: translateY(-50%);
    gap: 8px;
    padding-left: 0;
  }

  .nav-arrow {
    width: 30px;
    height: 30px;
  }

  .grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
    grid-auto-rows: 56px;
    gap: 6px;
  }

  .mini-card {
    height: 56px;
    flex-direction: column;
    justify-content: center;
    gap: 4px;
    padding: 5px 4px;
    text-align: center;
  }

  .mini-card :deep(.driver-icon) {
    width: 24px !important;
    height: 24px !important;
    flex: 0 0 24px;
  }

  .mini-card__name {
    width: 100%;
    font-size: 10px;
    line-height: 1.15;
    -webkit-line-clamp: 1;
  }

  .actions {
    gap: 8px;
  }

  .actions__search {
    gap: 8px;
    min-width: 0;
  }

  .search-box {
    width: auto;
    min-width: 0;
    flex: 1;
  }

  .search-hint {
    display: none;
  }
}

:global(:root[data-theme="dark"] .carousel__card) {
  background:
    radial-gradient(ellipse at 18% 30%, rgba(55, 79, 118, 0.28), transparent 28%),
    radial-gradient(ellipse at 74% 16%, color-mix(in srgb, var(--driver-color, var(--brand)) 16%, transparent), transparent 34%),
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--driver-color, var(--brand)) 7%, var(--surface-sunken)) 0%,
      color-mix(in srgb, var(--brand) 8%, var(--surface)) 58%,
      color-mix(in srgb, var(--driver-color, var(--brand)) 9%, var(--surface-sunken)) 100%
    );
  border-color: color-mix(in srgb, var(--brand) 76%, var(--border));
}

:global(:root[data-theme="dark"] .carousel__info h3) {
  color: var(--text);
}

:global(:root[data-theme="dark"] .carousel__info p) {
  color: color-mix(in srgb, var(--text-muted) 82%, #ffffff);
}

:global(:root[data-theme="dark"] .carousel__auth) {
  background: color-mix(in srgb, var(--driver-color, var(--brand)) 22%, rgba(255, 255, 255, 0.1));
  color: color-mix(in srgb, var(--driver-color, var(--brand)) 52%, #dbeafe);
}

:global(:root[data-theme="dark"] .carousel__tag) {
  background: color-mix(in srgb, var(--surface-muted) 86%, var(--driver-color, var(--brand)));
  border-color: color-mix(in srgb, var(--driver-color, var(--brand)) 28%, var(--border));
  color: color-mix(in srgb, var(--driver-color, var(--brand)) 42%, #d7e3f7);
  box-shadow: none;
}

:global(:root[data-theme="dark"] .nav-arrow) {
  background: color-mix(in srgb, var(--surface-muted) 92%, transparent);
  color: color-mix(in srgb, var(--brand) 78%, #ffffff);
  box-shadow: 0 8px 18px rgba(0, 0, 0, 0.22);
}

:global(:root[data-theme="dark"] .mini-card) {
  background: var(--surface-sunken);
  border-color: var(--border);
}

:global(:root[data-theme="dark"] .mini-card:hover) {
  background: var(--surface-hover);
}

:global(:root[data-theme="dark"] .mini-card.selected) {
  background: color-mix(in srgb, var(--brand) 14%, var(--surface));
}

:root[data-skin="brutal"] .carousel,
:root[data-skin="brutal"] .carousel__card,
:root[data-skin="brutal"] .nav-arrow,
:root[data-skin="brutal"] .mini-card,
:root[data-skin="brutal"] .grid__empty,
:root[data-skin="brutal"] .view-toggle,
:root[data-skin="brutal"] .search-box__input,
:root[data-skin="brutal"] .search-box__clear {
  border-radius: 0;
}

:root[data-skin="brutal"] .carousel__card,
:root[data-skin="brutal"] .mini-card,
:root[data-skin="brutal"] .grid__empty,
:root[data-skin="brutal"] .view-toggle,
:root[data-skin="brutal"] .search-box__input {
  border-color: var(--brutal-ink);
}

:root[data-skin="brutal"] .carousel__card:hover,
:root[data-skin="brutal"] .mini-card:hover,
:root[data-skin="brutal"] .view-toggle:hover {
  background: var(--brutal-yellow);
  border-color: var(--brutal-ink);
  box-shadow: 4px 4px 0 var(--brutal-ink);
  transform: none;
}

:root[data-skin="brutal"] .carousel__card.selected,
:root[data-skin="brutal"] .mini-card.selected {
  background: color-mix(in srgb, var(--brand) 12%, var(--surface));
  border-color: var(--brand);
  box-shadow: 4px 4px 0 color-mix(in srgb, var(--brand) 48%, var(--brutal-ink));
}

:root[data-skin="brutal"] .nav-arrow:hover,
:root[data-skin="brutal"] .search-box__clear:hover {
  background: var(--brutal-yellow);
  color: var(--brutal-ink);
  transform: none;
}

:root[data-skin="brutal"] .search-box__input:focus {
  border-color: var(--brand);
  box-shadow: 4px 4px 0 color-mix(in srgb, var(--brand) 55%, var(--brutal-ink));
}

:root[data-skin="brutal"] .view-toggle rect {
  rx: 0;
}
</style>
