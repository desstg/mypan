<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import "media-chrome";
import "media-chrome/dist/lang/zh-CN.js";
import { setLanguage } from "media-chrome/dist/utils/i18n.js";
import "@fortawesome/fontawesome-free/css/all.min.css";
import { filesApi } from "@/api/files";
import type { FileItem } from "@/api/types";
import { useBodyScrollLock } from "@/composables/useBodyScrollLock";
import { fileKind } from "@/utils/fileIcon";
import { fileExtension, formatSize } from "@/utils/format";
import PreviewHeader from "./PreviewHeader.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";

const props = defineProps<{
  accountId: number;
  files: FileItem[];
  initialFileId: string;
}>();

const emit = defineEmits<{
  close: [];
  download: [file: FileItem];
}>();

setLanguage("zh-CN");

const audioRef = ref<HTMLAudioElement | null>(null);
const trackListRef = ref<HTMLElement | null>(null);
const mediaLoading = ref(true);
const mediaError = ref(false);
const mediaPlaying = ref(false);
const notice = ref("");
let noticeTimer: number | undefined;

const tracks = computed(() =>
  props.files
    .filter((file) => !file.is_dir && fileKind(file) === "audio")
    .sort((left, right) =>
      left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: "base" }),
    ),
);

const initialIndex = tracks.value.findIndex((file) => file.id === props.initialFileId);
const currentIndex = ref(initialIndex >= 0 ? initialIndex : 0);
const currentFile = computed(() => tracks.value[currentIndex.value] ?? null);
const mediaURL = computed(() => {
  const file = currentFile.value;
  return file ? filesApi.previewURL(props.accountId, file.id, file.name) : "";
});

function fileStem(name: string) {
  return name.replace(/\.[^.]+$/, "");
}

function trackTitle(name: string) {
  const stem = fileStem(name).trim();
  return stem || name.trim() || "未命名音频";
}

function audioDetails(file: FileItem) {
  const extension = fileExtension(file.name).toUpperCase() || "音频";
  const size = file.size > 0 ? formatSize(file.size) : "未知大小";
  return `${extension} · ${size}`;
}
function showNotice(message: string) {
  notice.value = message;
  window.clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => {
    notice.value = "";
  }, 1500);
}

async function playCurrent() {
  await nextTick();
  const audio = audioRef.value;
  if (!audio) return;
  audio.load();
  await audio.play().catch(() => undefined);
}

async function selectTrack(index: number) {
  if (index < 0 || index >= tracks.value.length || index === currentIndex.value) return;
  currentIndex.value = index;
  mediaLoading.value = true;
  mediaError.value = false;
  mediaPlaying.value = false;
  showNotice(`正在播放 ${fileStem(tracks.value[index].name)}`);
  await playCurrent();
  scrollCurrentIntoView();
}

function playAdjacent(direction: -1 | 1) {
  const target = currentIndex.value + direction;
  if (target < 0 || target >= tracks.value.length) {
    showNotice(direction > 0 ? "已经是最后一首" : "已经是第一首");
    return;
  }
  void selectTrack(target);
}

function handleEnded() {
  if (currentIndex.value < tracks.value.length - 1) void selectTrack(currentIndex.value + 1);
}

function scrollCurrentIntoView() {
  void nextTick(() => {
    trackListRef.value
      ?.querySelector<HTMLElement>(".audio-track.is-active")
      ?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  });
}

function adjustTime(seconds: number) {
  const audio = audioRef.value;
  if (!audio) return;
  const duration = Number.isFinite(audio.duration) ? audio.duration : Infinity;
  audio.currentTime = Math.max(0, Math.min(duration, audio.currentTime + seconds));
  showNotice(seconds > 0 ? `快进 ${seconds} 秒` : `后退 ${Math.abs(seconds)} 秒`);
}

function adjustVolume(delta: number) {
  const audio = audioRef.value;
  if (!audio) return;
  audio.volume = Math.max(0, Math.min(1, audio.volume + delta));
  if (audio.volume > 0) audio.muted = false;
  showNotice(`音量 ${Math.round(audio.volume * 100)}%`);
}

function togglePlayback() {
  const audio = audioRef.value;
  if (!audio) return;
  if (audio.paused) void audio.play().catch(() => undefined);
  else audio.pause();
}

function toggleMute() {
  const audio = audioRef.value;
  if (!audio) return;
  audio.muted = !audio.muted;
  showNotice(audio.muted ? "已静音" : "已取消静音");
}

function isEditableTarget(target: EventTarget | null) {
  const element = target instanceof HTMLElement ? target : null;
  return !!element?.closest("input, textarea, select, [contenteditable='true']");
}

function handleKeydown(event: KeyboardEvent) {
  if (isEditableTarget(event.target)) return;
  const key = event.key.toLowerCase();
  if (["arrowleft", "arrowright", "arrowup", "arrowdown", " "].includes(key)) event.preventDefault();
  if (key === "escape") emit("close");
  else if (key === "arrowleft") adjustTime(-10);
  else if (key === "arrowright") adjustTime(10);
  else if (key === "arrowup") adjustVolume(0.05);
  else if (key === "arrowdown") adjustVolume(-0.05);
  else if (key === " ") togglePlayback();
  else if (key === "p") playAdjacent(-1);
  else if (key === "n") playAdjacent(1);
  else if (key === "m") toggleMute();
}

function handleReady() {
  mediaLoading.value = false;
  mediaError.value = false;
}

function handleError() {
  mediaLoading.value = false;
  mediaError.value = true;
  mediaPlaying.value = false;
}

function downloadCurrent() {
  if (currentFile.value) emit("download", currentFile.value);
}

useBodyScrollLock();

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
  scrollCurrentIntoView();
  void playCurrent();
});

onUnmounted(() => {
  window.clearTimeout(noticeTimer);
  window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <main class="file-preview audio-preview" role="dialog" aria-modal="true" aria-label="音频预览">
      <PreviewHeader
        :file-name="currentFile?.name"
        :status="`正在播放第${currentIndex + 1}首/共${tracks.length}首`"
        download-label="下载当前音频"
        @close="emit('close')"
        @download="downloadCurrent"
      />

      <section class="audio-preview__stage">
        <media-controller audio noautohide class="audio-preview__controller">
          <audio
            v-if="currentFile"
            ref="audioRef"
            slot="media"
            :key="currentFile.id"
            :src="mediaURL"
            autoplay
            referrerpolicy="no-referrer"
            preload="metadata"
            @loadedmetadata="handleReady"
            @canplay="handleReady"
            @playing="mediaPlaying = true; handleReady()"
            @pause="mediaPlaying = false"
            @waiting="mediaLoading = true"
            @error="handleError"
            @ended="handleEnded"
          />

          <div class="audio-preview__layout" slot="centered-chrome">
            <Transition name="audio-notice">
              <div v-if="notice" class="audio-preview__notice" role="status">{{ notice }}</div>
            </Transition>

            <section class="audio-preview__now">
              <div class="audio-preview__art" :class="{ 'is-playing': mediaPlaying }" aria-hidden="true">
                <div class="audio-preview__disc">
                  <span><i class="fa-solid fa-music" /></span>
                </div>
              </div>

              <div v-if="currentFile" class="audio-preview__meta">
                <span>{{ audioDetails(currentFile) }}</span>
                <h1 :title="trackTitle(currentFile.name)">{{ trackTitle(currentFile.name) }}</h1>
                <p>{{ mediaError ? "当前浏览器无法解码此音频格式" : "来自当前文件夹" }}</p>
              </div>

              <div v-if="mediaLoading && !mediaError" class="audio-preview__loading" aria-label="正在加载音频">
                <BusySpinner :size="16" color="#1687ff" />
                正在加载音频…
              </div>

              <div v-if="mediaError" class="audio-preview__error" role="alert">
                <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
                <div>
                  <strong>浏览器无法直接播放这个音频</strong>
                  <span>可能是音频编码不受支持，可下载后使用本地播放器打开。</span>
                </div>
                <button type="button" @click="downloadCurrent">下载音频</button>
              </div>

              <div class="audio-preview__transport">
                <button type="button" aria-label="上一首" :disabled="currentIndex <= 0" @click="playAdjacent(-1)">
                  <i class="fa-solid fa-backward-step" aria-hidden="true" />
                </button>
                <media-control-bar class="audio-preview__controls">
                  <media-play-button aria-label="播放或暂停" />
                  <media-time-display show-duration />
                  <media-time-range />
                  <media-mute-button aria-label="静音" />
                  <media-volume-range />
                  <media-playback-rate-button rates="0.5 0.75 1 1.25 1.5 2" />
                </media-control-bar>
                <button type="button" aria-label="下一首" :disabled="currentIndex >= tracks.length - 1" @click="playAdjacent(1)">
                  <i class="fa-solid fa-forward-step" aria-hidden="true" />
                </button>
              </div>

              <div class="audio-preview__shortcuts" aria-hidden="true">
                <span>← / → 快退快进</span><i>·</i><span>↑ / ↓ 音量</span><i>·</i>
                <span>空格 播放暂停</span><i>·</i><span>P 上一首丨N 下一首</span><i>·</i><span>M 静音切换</span>
              </div>
            </section>

            <aside class="audio-preview__playlist">
              <header>
                <div>
                  <strong>播放列表</strong>
                  <span>当前文件夹</span>
                </div>
                <b>{{ tracks.length }} 首</b>
              </header>
              <div ref="trackListRef" class="audio-preview__tracks">
                <button
                  v-for="(track, index) in tracks"
                  :key="track.id"
                  type="button"
                  class="audio-track"
                  :class="{ 'is-active': index === currentIndex }"
                  :aria-current="index === currentIndex ? 'true' : undefined"
                  @click="selectTrack(index)"
                >
                  <span class="audio-track__index">
                    <i v-if="index === currentIndex" class="fa-solid fa-volume-high" aria-hidden="true" />
                    <template v-else>{{ String(index + 1).padStart(2, "0") }}</template>
                  </span>
                  <span class="audio-track__copy">
                    <strong :title="trackTitle(track.name)">{{ trackTitle(track.name) }}</strong>
                    <small>{{ audioDetails(track) }}</small>
                  </span>
                  <i class="fa-solid fa-play audio-track__play" aria-hidden="true" />
                </button>
              </div>
            </aside>
          </div>
        </media-controller>
      </section>
    </main>
  </Teleport>
</template>

<style scoped>
.audio-preview {
  min-height: 560px;
  background:
    radial-gradient(circle at 28% 35%, rgb(26 94 166 / 28%), transparent 38%),
    radial-gradient(circle at 68% 70%, rgb(51 33 123 / 20%), transparent 38%),
    #020711;
}
.audio-preview__stage,
.audio-preview__controller { display: block; width: 100%; height: 100%; }
.audio-preview__controller {
  --media-primary-color: #1687ff;
  --media-secondary-color: #f4f8ff;
  --media-control-background: transparent;
  --media-control-hover-background: rgb(255 255 255 / 10%);
  background: transparent;
}

.audio-preview__layout {
  position: absolute;
  inset: 70px 0 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(310px, 390px);
  gap: clamp(28px, 5vw, 78px);
  align-items: center;
  padding: clamp(28px, 5vw, 76px);
  pointer-events: auto;
}

.audio-preview__notice {
  position: absolute;
  left: 50%;
  top: 24px;
  z-index: 5;
  transform: translateX(-50%);
  padding: 9px 14px;
  border: 1px solid rgb(255 255 255 / 18%);
  border-radius: 8px;
  background: rgb(3 11 25 / 88%);
  box-shadow: 0 12px 38px rgb(0 0 0 / 32%);
  font-size: 13px;
  white-space: nowrap;
  backdrop-filter: blur(14px);
}
.audio-notice-enter-active, .audio-notice-leave-active { transition: opacity 150ms ease, transform 150ms ease; }
.audio-notice-enter-from, .audio-notice-leave-to { opacity: 0; transform: translate(-50%, 6px); }

.audio-preview__now {
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
}

.audio-preview__art {
  width: clamp(210px, 28vw, 390px);
  aspect-ratio: 1;
  display: grid;
  place-items: center;
  margin-bottom: clamp(24px, 4vh, 42px);
  border: 1px solid rgb(127 180 242 / 16%);
  border-radius: 26px;
  background:
    linear-gradient(145deg, rgb(36 106 185 / 35%), rgb(7 20 42 / 40%)),
    rgb(7 18 36 / 68%);
  box-shadow: 0 30px 90px rgb(0 0 0 / 42%), inset 0 1px 0 rgb(255 255 255 / 8%);
  backdrop-filter: blur(18px);
}

.audio-preview__disc {
  width: 74%;
  aspect-ratio: 1;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background:
    repeating-radial-gradient(circle, transparent 0 8px, rgb(255 255 255 / 3%) 9px 10px),
    conic-gradient(from 20deg, #101b2e, #0a101d, #1c3150, #080d17, #142641, #101b2e);
  box-shadow: 0 18px 45px rgb(0 0 0 / 48%), inset 0 0 0 1px rgb(255 255 255 / 8%);
  animation: audio-disc-spin 9s linear infinite paused;
}
.audio-preview__art.is-playing .audio-preview__disc { animation-play-state: running; }
.audio-preview__disc span {
  width: 29%;
  aspect-ratio: 1;
  display: grid;
  place-items: center;
  border-radius: 50%;
  color: #fff;
  background: linear-gradient(135deg, #237de0, #3b9dff);
  box-shadow: 0 8px 22px rgb(20 116 222 / 42%);
  font-size: clamp(22px, 3vw, 38px);
}
@keyframes audio-disc-spin { to { transform: rotate(360deg); } }

.audio-preview__meta { width: min(760px, 100%); text-align: center; }
.audio-preview__meta > span { color: #6f91b9; font-size: 11px; font-weight: 650; letter-spacing: 0.1em; }
.audio-preview__meta h1 {
  margin: 8px 0 4px;
  overflow: hidden;
  font-size: clamp(22px, 2.6vw, 34px);
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.audio-preview__meta p { margin: 0; color: #7f91aa; font-size: 12px; }

.audio-preview__loading {
  min-height: 40px;
  display: flex;
  align-items: center;
  gap: 9px;
  margin-top: 14px;
  color: #9fb0c6;
  font-size: 12px;
}

.audio-preview__error {
  width: min(660px, 100%);
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 13px;
  margin-top: 16px;
  padding: 13px 15px;
  border: 1px solid rgb(255 169 78 / 24%);
  border-radius: 10px;
  background: rgb(44 25 10 / 32%);
}
.audio-preview__error > i { color: #ffb45e; font-size: 20px; }
.audio-preview__error div { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.audio-preview__error strong { font-size: 13px; }
.audio-preview__error span { color: #aa9c8e; font-size: 11px; }
.audio-preview__error button {
  padding: 7px 11px;
  border: 1px solid rgb(255 180 93 / 38%);
  border-radius: 7px;
  background: rgb(129 72 18 / 34%);
  font-size: 11px;
}

.audio-preview__transport {
  width: min(860px, 100%);
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr) 46px;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
}
.audio-preview__transport > button {
  width: 46px;
  height: 46px;
  border: 1px solid rgb(127 167 221 / 18%);
  border-radius: 50%;
  background: rgb(9 25 48 / 68%);
  font-size: 16px;
}
.audio-preview__transport > button:hover:not(:disabled) { color: #5cadff; border-color: rgb(55 147 245 / 48%); }
.audio-preview__transport > button:disabled { opacity: 0.28; }
.audio-preview__controls {
  display: grid;
  grid-template-columns: 48px 108px minmax(150px, 1fr) 44px 92px 62px;
  align-items: center;
  gap: 6px;
  min-width: 0;
  min-height: 54px;
  padding: 0 8px;
  border: 1px solid rgb(129 174 232 / 14%);
  border-radius: 14px;
  background: rgb(5 17 34 / 64%);
  box-shadow: 0 14px 40px rgb(0 0 0 / 22%);
  backdrop-filter: blur(16px);
}
.audio-preview__controls > * { min-width: 0; }
.audio-preview__controls media-time-range {
  width: 100%;
  --media-range-track-background: rgb(201 215 236 / 36%);
  --media-time-range-buffered-color: rgb(255 255 255 / 20%);
  --media-range-bar-color: #1687ff;
}
.audio-preview__controls media-volume-range { --media-range-track-height: 4px; }

.audio-preview__shortcuts {
  min-height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 11px;
  margin-top: 9px;
  color: #65758d;
  font-size: 10px;
}
.audio-preview__shortcuts i { opacity: 0.55; }

.audio-preview__playlist {
  height: min(680px, calc(100dvh - 150px));
  min-height: 360px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid rgb(127 171 228 / 16%);
  border-radius: 18px;
  background: rgb(5 16 32 / 58%);
  box-shadow: 0 24px 70px rgb(0 0 0 / 30%), inset 0 1px 0 rgb(255 255 255 / 5%);
  backdrop-filter: blur(20px);
}
.audio-preview__playlist > header {
  min-height: 76px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  border-bottom: 1px solid rgb(131 170 220 / 13%);
}
.audio-preview__playlist header div { display: flex; flex-direction: column; gap: 3px; }
.audio-preview__playlist header strong { font-size: 15px; }
.audio-preview__playlist header span { color: #74869f; font-size: 10px; }
.audio-preview__playlist header b {
  padding: 5px 9px;
  border-radius: 999px;
  color: #8ac7ff;
  background: rgb(28 124 224 / 16%);
  font-size: 10px;
}
.audio-preview__tracks { flex: 1; overflow-y: auto; padding: 8px; scrollbar-width: thin; scrollbar-color: rgb(86 122 169 / 38%) transparent; }
.audio-track {
  width: 100%;
  min-height: 62px;
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) 28px;
  align-items: center;
  gap: 9px;
  padding: 7px 10px;
  text-align: left;
  border: 1px solid transparent;
  border-radius: 10px;
  background: transparent;
}
.audio-track:hover { background: rgb(255 255 255 / 5%); }
.audio-track.is-active { border-color: rgb(43 144 247 / 24%); background: rgb(22 105 194 / 17%); }
.audio-track__index { color: #63758e; text-align: center; font-size: 11px; }
.audio-track.is-active .audio-track__index { color: #47a8ff; }
.audio-track__copy { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.audio-track__copy strong {
  display: block;
  overflow: hidden;
  color: #edf5ff;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.audio-track.is-active .audio-track__copy strong { color: #fff; }
.audio-track__copy small { color: #6f8199; font-size: 9px; }
.audio-track__play { color: #4aa8ff; font-size: 10px; opacity: 0; }
.audio-track:hover .audio-track__play,
.audio-track.is-active .audio-track__play { opacity: 1; }

@media (max-width: 1100px) {
  .audio-preview__layout { grid-template-columns: minmax(0, 1fr) 310px; gap: 24px; padding: 32px; }
  .audio-preview__controls { grid-template-columns: 44px 96px minmax(120px, 1fr) 42px 70px 56px; }
  .audio-preview__shortcuts { display: none; }
}

@media (max-width: 760px) {
  .audio-preview__layout {
    inset: 58px 0 0;
    grid-template-columns: 1fr;
    grid-template-rows: auto minmax(180px, 1fr);
    gap: 18px;
    align-items: start;
    padding: 20px 14px 14px;
    overflow-y: auto;
  }
  .audio-preview__art { width: min(48vw, 210px); margin-bottom: 14px; border-radius: 18px; }
  .audio-preview__meta h1 { font-size: 18px; }
  .audio-preview__transport { grid-template-columns: 38px minmax(0, 1fr) 38px; gap: 5px; }
  .audio-preview__transport > button { width: 38px; height: 38px; }
  .audio-preview__controls { grid-template-columns: 42px 1fr 42px; min-height: 48px; }
  .audio-preview__controls media-time-display,
  .audio-preview__controls media-volume-range,
  .audio-preview__controls media-playback-rate-button { display: none; }
  .audio-preview__playlist { height: auto; min-height: 260px; }
  .audio-preview__playlist > header { min-height: 58px; }
}
</style>
