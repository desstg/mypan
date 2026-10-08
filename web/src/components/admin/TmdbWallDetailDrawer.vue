<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import type { PlayableFile, WallDetail } from "@/api/strmScrape";
import "@/styles/jav.css";

/**
 * TMDB 影片墙的详情抽屉。
 *
 * # 为什么不与番号那面共用一个组件
 *
 * 两面墙的数据源差得太远，共用会让两边到处写 `if`：
 *
 *   - **番号**：横版封面（`thumb.jpg` 700×394）、番号胶囊、片商/发行商/系列、
 *     5 分制评分、侧车里的演员（带头像）与 `extrafanart/` 剧照；
 *   - **TMDB**：竖版封面（2:3 海报）、没有番号、有导演/编剧/制片公司/国家/分级、
 *     10 分制评分、演员（本地头像，按 id 去重）、`extrafanart/` 剧照。
 *
 * 拆开之后两边各改各的，互不影响 —— 番号那面（`WallDetailDrawer.vue`）保持原样，
 * 它的观感一个像素都不动。
 *
 * # 版式
 *
 *   标题（大号）+ 原始标题（小字）
 *   [日期] [时长] [星级 + 评分 + 投票数] [分级]
 *   ┌ 竖版海报（正中播放键）┬ 信息卡：导演 / 编剧 / 制片公司 / 国家
 *   │                      ┴ 类别：类型 + 关键词
 *   演员（矩形图，与番号那面同一套样式）
 *   剧情简介
 *   剧照（网格 + 灯箱）
 *   源媒体信息
 *
 * # 刻意不做的
 *
 * - **背景大图**（用户明确要求：backdrop 下回来是给 Emby 当背景图的，抽屉里不显示）
 * - 磁链 / 评论 / 关联清单那几个 tab
 *
 * # 「有就显示」
 *
 * 老作品（本次改造之前刮的）磁盘上只有简版 nfo，那些块就是空的，整块不渲染。
 * 想补上就对该任务点一次「补齐剧照与演员」。
 */
const props = defineProps<{
  open: boolean;
  loading?: boolean;
  detail: WallDetail | null;
  error?: string;
}>();

const emit = defineEmits<{
  close: [];
  /** 请求播放某一集（`path` 是 `.strm` 正文里那行地址，`subtitles` 是同目录字幕）。 */
  play: [file: PlayableFile];
}>();

/** 选集面板：剧集（多条可播文件）点播放键时先弹它。 */
const pickerOpen = ref(false);
const playable = computed<PlayableFile[]>(() => props.detail?.playable ?? []);
const isSeries = computed(() => playable.value.length > 1);

watch(
  () => props.detail,
  () => {
    pickerOpen.value = false;
  },
);

/**
 * 标题。
 *
 * 标题取自**目录名**时带着 `{tmdb-299952}` 这样的标记（那是给整理与匹配用的，
 * 不是给人看的）。这里剥掉它 —— 只影响显示，磁盘上的目录名一个字节不动。
 */
const title = computed(() => stripTmdbTag(props.detail?.title) || "详情");

function stripTmdbTag(raw: string | undefined): string {
  return (raw ?? "").replace(/\s*\{tmdb-\d+\}\s*$/i, "").trim();
}

/** 原始标题（`original_title` / `original_name`）。与中文标题相同时不显示。 */
const originalTitle = computed(() => {
  const value = (props.detail?.origin_title ?? "").trim();
  if (!value || value === title.value) return "";
  return value;
});

/**
 * 选集面板里的短名。
 *
 * `.strm` 主干长这样：`早春晴朗 (2026) S01E21 [1080p H.264 AAC]` —— 直接显示会被
 * 截断，而且用户选的是「第几集」，中间那段发布信息是噪声。
 */
function episodeLabel(name: string): string {
  const stem = name.replace(/\.[^.]+$/, "");
  const m = /S(\d{1,2})E(\d{1,3})/i.exec(stem);
  if (m) return `S${m[1].padStart(2, "0")}E${m[2].padStart(2, "0")}`;
  return stem;
}

/** 时长：后端给的是分钟。0 表示没有，不显示。 */
const durationText = computed(() => {
  const mins = props.detail?.duration ?? 0;
  if (mins <= 0) return "";
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  return h > 0 ? `${h} 小时 ${m} 分` : `${m} 分钟`;
});

/**
 * 评分。
 *
 * ⚠️ TMDB 是 **10 分制**（后端给的 `score_max` 就是 10），与番号那面的 5 分制不同 ——
 * 星标要按 `score / score_max * 5` 折算，直接拿 score 当 5 分制会满屏 5 星。
 */
const hasRating = computed(() => Boolean(props.detail?.score && props.detail?.score_max));
const stars = computed(() => {
  const d = props.detail;
  if (!d?.score || !d.score_max) return 0;
  return Math.round((d.score / d.score_max) * 5);
});
const scoreText = computed(() => (props.detail?.score ?? 0).toFixed(1));

/** 竖版海报：TMDB 的海报是 2:3，封面比例与两栏宽度都跟着它变。 */
const isPortrait = computed(() => props.detail?.poster_ratio === "2:3");

// —— 播放（与番号那面同一套交互）——

/** 单集直接播；剧集先弹选集面板。 */
function onPlay() {
  if (!playable.value.length) return;
  if (isSeries.value) {
    pickerOpen.value = true;
    return;
  }
  emit("play", playable.value[0]);
}

function playAt(file: PlayableFile) {
  pickerOpen.value = false;
  emit("play", file);
}

// —— 剧照灯箱（与 JavMovieDrawer / 番号抽屉同一套类名与观感）——
const lightboxIndex = ref<number | null>(null);
const previews = computed(() => props.detail?.previews ?? []);

function openLightbox(index: number) {
  if (previews.value.length) lightboxIndex.value = index;
}

function stepLightbox(delta: number) {
  const n = previews.value.length;
  if (!n || lightboxIndex.value === null) return;
  lightboxIndex.value = (lightboxIndex.value + delta + n) % n;
}

function onKeydown(event: KeyboardEvent) {
  if (lightboxIndex.value === null) return;
  if (event.key === "Escape") lightboxIndex.value = null;
  else if (event.key === "ArrowLeft") stepLightbox(-1);
  else if (event.key === "ArrowRight") stepLightbox(1);
}

watch(lightboxIndex, (v) => {
  if (v === null) window.removeEventListener("keydown", onKeydown);
  else window.addEventListener("keydown", onKeydown);
});
</script>

<template>
  <AdminSettingsDrawer :open="open" :title="title" hide-foot elevated @close="emit('close')">
    <div v-if="loading" class="jd-empty">加载中…</div>
    <div v-else-if="error" class="jd-empty" style="color: var(--danger)">{{ error }}</div>

    <div v-else-if="detail">
      <h1 class="jd-title">{{ title }}</h1>
      <div v-if="originalTitle" class="tmdb-detail__original">{{ originalTitle }}</div>

      <!-- 信息条：日期 / 时长 / 评分 / 分级。 -->
      <div class="jd-meta">
        <div class="jd-meta__left">
          <span v-if="detail.release_date" class="jd-meta__item">{{ detail.release_date }}</span>
          <span v-if="durationText" class="jd-meta__item">{{ durationText }}</span>
        </div>
        <div class="jd-meta__right">
          <!-- 评分：10 分制，星标按比例折算（见 stars 的说明）。
               `jd-meta__right` 在 jav.css 里是 flex 两端对齐，这里只有这一项。 -->
          <span v-if="hasRating" class="jd-stars jd-stars--green" :title="`TMDB ${scoreText}/10`">
            <i
              v-for="i in 5"
              :key="i"
              class="fas fa-star jd-star"
              :class="{ 'jd-star--on': i <= stars }"
            />
            <span class="jd-score">{{ scoreText }}</span>
            <em v-if="detail.votes" class="jd-score__count">{{ detail.votes }}人评分</em>
          </span>
        </div>
      </div>

      <!-- 两栏：竖版海报 + 信息卡 / 类别卡。栏宽左窄右宽（见样式里的说明）。 -->
      <div class="jd-cols" :class="{ 'wall-detail__cols--portrait': isPortrait }">
        <div class="jd-col">
          <div class="jd-cover" :class="{ 'wall-detail__cover--portrait': isPortrait }">
            <MediaImage :src="detail.poster_url || ''" :alt="title" />
            <button
              v-if="playable.length"
              type="button"
              class="jd-cover__play"
              :title="isSeries ? `播放（共 ${playable.length} 集）` : '播放'"
              @click="onPlay"
            >
              <i class="fas fa-play" />
            </button>
          </div>
        </div>

        <div class="jd-col">
          <div class="jd-card">
            <div class="jd-card__title">信息</div>
            <div class="jd-info">
              <div class="jd-info__row">
                <span class="jd-info__k">导演</span>
                <span class="jd-info__v">{{ detail.director || "—" }}</span>
              </div>
              <!-- 制片公司：后端把 TMDB 的 production_companies 用 " / " 连成一串放进 maker。 -->
              <div class="jd-info__row">
                <span class="jd-info__k">制片</span>
                <span class="jd-info__v">{{ detail.maker || "—" }}</span>
              </div>
              <div v-if="detail.publisher" class="jd-info__row">
                <span class="jd-info__k">分级</span>
                <span class="jd-info__v">{{ detail.publisher }}</span>
              </div>
              <div v-if="detail.series" class="jd-info__row">
                <span class="jd-info__k">合集</span>
                <span class="jd-info__v">{{ detail.series }}</span>
              </div>
            </div>
          </div>

          <!-- 类别：类型 + 剧情关键词（后端合并进 tags）。 -->
          <div v-if="detail.tags?.length" class="jd-card">
            <div class="jd-card__title"><i class="fas fa-folder" /> 类别</div>
            <div class="jd-tags">
              <span v-for="t in detail.tags" :key="t" class="jd-tag">{{ t }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 演员：矩形图（与番号那面同一套样式）。头像是**本地文件**（media/actors/{id}.jpg），
           后端已经把 URL 拼成 /poster 的取图地址，不需要再经图片代理。 -->
      <div v-if="detail.actors?.length" class="jd-card" style="margin-top: 18px">
        <div class="jd-card__title"><i class="fas fa-user" /> 演员 ({{ detail.actors.length }})</div>
        <div class="jd-actors wall-detail__actors">
          <div v-for="a in detail.actors" :key="a.id || a.name" class="jd-actor wall-detail__actor">
            <div class="jd-actor__avatar wall-detail__actor-photo">
              <img v-if="a.avatar_url" :src="a.avatar_url" :alt="a.name" loading="lazy" />
              <i v-else class="fas fa-user" aria-hidden="true" />
            </div>
            <div class="wall-detail__actor-name" :title="a.name">{{ a.name }}</div>
          </div>
        </div>
      </div>

      <!-- 剧情简介 -->
      <div v-if="detail.summary" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title"><i class="fas fa-circle-info" /> 剧情简介</div>
        <div class="jd-summary">{{ detail.summary }}</div>
      </div>

      <!-- 剧照：网格 + 灯箱。 -->
      <div v-if="previews.length" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title">
          <i class="fas fa-images" /> 剧照
          <span style="margin-left: 6px; font-weight: 400; color: var(--text-muted)">
            {{ previews.length }} 张
          </span>
        </div>
        <div class="wall-detail__stills">
          <button
            v-for="(src, i) in previews"
            :key="src"
            type="button"
            class="wall-detail__still"
            @click="openLightbox(i)"
          >
            <img :src="src" :alt="`剧照 ${i + 1}`" loading="lazy" />
          </button>
        </div>
      </div>

      <!-- 源媒体信息：文件名 / 路径 / 大小 / 类型（路径按设置里的「宿主机路径映射」换算过）。 -->
      <div v-if="detail.source" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title"><i class="fas fa-hard-drive" /> 源媒体信息</div>
        <div class="jd-info">
          <div class="jd-info__row">
            <span class="jd-info__k">文件</span>
            <span class="jd-info__v">{{ detail.source.file_name }}</span>
          </div>
          <div class="jd-info__row">
            <span class="jd-info__k">路径</span>
            <span class="jd-info__v" style="word-break: break-all">{{ detail.source.path }}</span>
          </div>
          <div v-if="detail.source.size_text" class="jd-info__row">
            <span class="jd-info__k">大小</span>
            <span class="jd-info__v">{{ detail.source.size_text }}</span>
          </div>
          <div v-if="detail.source.ext" class="jd-info__row">
            <span class="jd-info__k">类型</span>
            <span class="jd-info__v">{{ detail.source.ext }}</span>
          </div>
        </div>
      </div>

      <div v-if="detail.notice" class="jd-empty" style="margin-top: 14px">{{ detail.notice }}</div>
    </div>

    <!-- 选集面板：剧集专用。单集直接播，不弹这个。 -->
    <Teleport to="body">
      <div v-if="pickerOpen" class="wall-picker" @click.self="pickerOpen = false">
        <div class="wall-picker__panel">
          <div class="wall-picker__head">
            <span>选择要播放的一集</span>
            <button type="button" class="wall-picker__close" title="关闭" @click="pickerOpen = false">✕</button>
          </div>
          <div class="wall-picker__list">
            <button
              v-for="file in playable"
              :key="file.name"
              type="button"
              class="wall-picker__item"
              :title="file.name"
              @click="playAt(file)"
            >
              <i class="fas fa-play" />
              <span>{{ episodeLabel(file.name) }}</span>
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 剧照灯箱 -->
    <Teleport to="body">
      <div
        v-if="lightboxIndex !== null"
        class="jd-lightbox"
        :class="{ 'jd-lightbox--single': previews.length <= 1 }"
        @click.self="lightboxIndex = null"
      >
        <button class="jd-lightbox__close" title="关闭" @click="lightboxIndex = null">✕</button>
        <button class="jd-lightbox__arrow jd-lightbox__arrow--prev" title="上一张" @click.stop="stepLightbox(-1)">‹</button>
        <img class="jd-lightbox__img" :src="previews[lightboxIndex]" alt="剧照" />
        <button class="jd-lightbox__arrow jd-lightbox__arrow--next" title="下一张" @click.stop="stepLightbox(1)">›</button>
      </div>
    </Teleport>
  </AdminSettingsDrawer>
</template>

<style scoped>
/* 原始标题：中文标题下面那行小字。比标题轻两档，读起来像注脚。 */
.tmdb-detail__original {
  margin: -8px 0 10px;
  color: var(--text-muted);
  font-size: 0.85rem;
  font-family: var(--font-sans);
}

/* 信息条右侧那组（评分 + 投票数）。`jd-meta__right` 在 jav.css 里是 flex 两端对齐，
   这里只补一点间距。 */
.jd-meta__right .jd-stars {
  gap: 2px;
}

/* ⚠️ 选集面板那批 `.wall-picker*` 的样式**必须在这个组件的 scoped 里也有一份**。
 *
 * 与番号那个抽屉是同一套类名、同一份样式，看起来像该共用 —— 但 `<style scoped>`
 * 会把选择器编译成「本组件元素 + .wall-picker」，**另一个组件的 scoped 样式管不到
 * 这个组件的元素**。第一版就是漏了这一份，于是剧集的选集面板点播放键**什么都不弹**
 * （元素渲染出来了，但 position/z-index 全丢，被抽屉盖在下面）。
 *
 * 放 `jav.css`（全局）也能解决，但那要动到番号那面在用的文件；两个组件各留一份
 * 更隔离 —— 代价是改一处要记得改两处，所以这段注释写在这里。 */
.wall-picker {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(15, 23, 42, 0.48);
}

.wall-picker__panel {
  width: 100%;
  max-width: 420px;
  max-height: 70vh;
  display: flex;
  flex-direction: column;
  border-radius: var(--radius-lg);
  background: var(--surface);
  box-shadow: var(--shadow-pop);
  overflow: hidden;
}

.wall-picker__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
  font-weight: 600;
}

.wall-picker__close {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 15px;
  cursor: pointer;
}

.wall-picker__close:hover {
  color: var(--text);
}

.wall-picker__list {
  padding: 8px;
  overflow-y: auto;
}

.wall-picker__item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.wall-picker__item:hover {
  background: var(--border-soft);
  color: var(--brand);
}

.wall-picker__item i {
  font-size: 0.72rem;
  color: var(--text-muted);
}

.wall-picker__item:hover i {
  color: var(--brand);
}
</style>
