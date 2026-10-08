<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import { javImageURL } from "@/api/jav";
import type { PlayableFile, WallDetail } from "@/api/strmScrape";
import "@/styles/jav.css";

/**
 * 番号影片墙的详情抽屉。
 *
 * # 它现在只服务**番号**那一面
 *
 * 原本两面墙共用一个抽屉。TMDB 那面抓全数据之后版式要另起一套（它的封面是竖版、
 * 字段也不同），所以拆出了 `TmdbWallDetailDrawer.vue` —— **这一份保持原样**，
 * 番号那面的观感一个像素都不动。
 *
 * # 版式
 *
 * **照影库详情页（JavMovieDrawer）那套**，复用同一批 `jd-*` 类名与 jav.css 里的
 * 样式 —— 详情页长得一样，不新造一套观感。
 *
 *   标题（大号，占满整行）
 *   [番号] [日期] [时长]
 *   ┌ 横版封面（正中播放键）┬ 信息卡：导演/片商/发行商/系列/众评/评分
 *   │                      ┴ 类别：标签胶囊
 *   演员（矩形图，不裁圆）
 *   剧照（网格 + 灯箱）
 *   源媒体信息：文件名 / 路径 / 大小 / 类型
 *
 * # 刻意不做的
 *
 * - **背景大图**（用户要求，两面都不做）
 * - **磁链 / 评论区 / 关联清单 / 评论 四个 tab**（那是影库详情页的东西，这里不要）
 * - **重新获取 / 已订阅 / 已入库角标**（同上）
 *
 * # 「有就显示」是本组件的核心规矩
 *
 * 番号那面的数据只有**抓过详情**的片才有（侧车 / nfo / extrafanart），
 * 所以每块都 `v-if="有内容"`，空的时候整块不渲染。
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
 * 标题取自**目录名**时可能带着 `{tmdb-299952}` 这样的标记（那是给整理与匹配用的，
 * 不是给人看的）。这里剥掉它 —— 只影响显示，磁盘上的目录名一个字节不动。
 * 番号那面不会有这种后缀，剥了也没副作用。
 */
const title = computed(() => stripTmdbTag(props.detail?.title) || props.detail?.number || "详情");

function stripTmdbTag(raw: string | undefined): string {
  return (raw ?? "").replace(/\s*\{tmdb-\d+\}\s*$/i, "").trim();
}

/**
 * 选集面板里的短名。
 *
 * `.strm` 主干长这样：`早春晴朗 (2026) S01E21 [1080p H.264 AAC]` —— 直接显示会被
 * 截断，而且用户选的是「第几集」，中间那段发布信息是噪声。
 * 能认出 `SxxExx` 就只显示它（外加前后那点集名），认不出才回落成整个主干。
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

/** 评分：score_max 为 0 表示没有评分（别显示 0/5）。 */
const hasRating = computed(() => Boolean(props.detail?.score && props.detail?.score_max));

/** 星标：满分 5 颗（整颗近似，与影库详情页同一口径）。 */
const stars = computed(() => {
  const d = props.detail;
  if (!d?.score || !d.score_max) return 0;
  return Math.round((d.score / d.score_max) * 5);
});

const scoreText = computed(() => (props.detail?.score ?? 0).toFixed(2));

/**
 * 封面是不是竖版。
 *
 * 后端读图片头给 `poster_ratio`。番号那面**绝大多数**是 `thumb.jpg`（700×394，3:2 横，
 * 恒为 false）；只有「有 poster.jpg 但没有 thumb.jpg」的那批才会拿到 2:3 —— 那种情况下
 * 封面比例与两栏宽度**仍然一起**变成竖版那一套（**这两条是同一条决定的两半**，
 * 别只改一个：左栏一宽，2:3 的封面就高得离谱，把右边挤成一条）。
 */
const isPortrait = computed(() => props.detail?.poster_ratio === "2:3");

/** 演员：有头像的走图片代理（会解 XOR），没头像的显示占位图标。 */
function actorAvatar(actor: { avatar_url?: string }) {
  return javImageURL(actor.avatar_url);
}

// —— 播放 ——

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

// —— 剧照灯箱（与 JavMovieDrawer 那个同一套类名与观感）——
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
      <!-- 标题：用 `title` computed（已剥掉 `{tmdb-xxx}` 标记），别直接用 detail.title ——
           标题取自目录名时会带着那个标记，直接显示会露出 `{tmdb-299952}`。 -->
      <h1 class="jd-title">{{ title }}</h1>

      <!-- 信息条：番号 / 日期 / 时长。角标与右侧那两个按钮都**不要**（用户要求）。 -->
      <div class="jd-meta">
        <div class="jd-meta__left">
          <span class="jd-num">{{ detail.number }}</span>
          <span v-if="detail.release_date" class="jd-meta__item">{{ detail.release_date }}</span>
          <span v-if="durationText" class="jd-meta__item">{{ durationText }}</span>
        </div>
      </div>

      <!-- 两栏：横版封面 + 信息卡 / 类别卡。
           竖版（左窄右宽 + 封面 2:3）这一面**保留着**：番号的图绝大多数是 `thumb.jpg`
           （3:2 横版，isPortrait 为 false），但**只有 poster.jpg 没有 thumb.jpg** 的那批
           （275×394，2:3）确实会走到这里 —— 保持原行为，不动。 -->
      <div class="jd-cols" :class="{ 'wall-detail__cols--portrait': isPortrait }">
        <div class="jd-col">
          <div class="jd-cover" :class="{ 'wall-detail__cover--portrait': isPortrait }">
            <MediaImage :src="detail.poster_url || ''" :alt="title" />
            <!-- 播放键：封面正中。单集直接播，剧集弹选集面板。 -->
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
              <div class="jd-info__row">
                <span class="jd-info__k">片商</span>
                <span class="jd-info__v">{{ detail.maker || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">发行商</span>
                <span class="jd-info__v">{{ detail.publisher || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">系列</span>
                <span class="jd-info__v">{{ detail.series || "—" }}</span>
              </div>
              <!-- 众评 / 评分：没有评分数据时这两行不显示（别显示 0.00） -->
              <div v-if="hasRating" class="jd-info__row">
                <span class="jd-info__k">众评</span>
                <span class="jd-info__v">
                  <span class="jd-stars jd-stars--green">
                    <i
                      v-for="i in 5"
                      :key="i"
                      class="fas fa-star jd-star"
                      :class="{ 'jd-star--on': i <= stars }"
                    />
                  </span>
                  <span class="jd-score">{{ scoreText }}</span>
                  <em v-if="detail.votes" class="jd-score__count">{{ detail.votes }}人评分</em>
                </span>
              </div>
              <div v-if="hasRating" class="jd-info__row">
                <span class="jd-info__k">评分</span>
                <span class="jd-info__v">
                  <span class="jd-stars jd-stars--gray">
                    <i
                      v-for="i in 5"
                      :key="i"
                      class="fas fa-star jd-star"
                      :class="{ 'jd-star--on': i <= stars }"
                    />
                  </span>
                  <span class="jd-score">{{ scoreText }}</span>
                </span>
              </div>
            </div>
          </div>

          <div v-if="detail.tags?.length" class="jd-card">
            <div class="jd-card__title"><i class="fas fa-folder" /> 类别</div>
            <div class="jd-tags">
              <!-- 纯展示，**不是按钮**：点标签去筛影库是另一件事（要切档 + 带筛选条件），
                   这一版没做。做成按钮却点了没反应，比做成标签更糟 —— 用户会以为坏了。 -->
              <span v-for="t in detail.tags" :key="t" class="jd-tag">{{ t }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 演员：**矩形图，不裁圆**（用户要求）。没有头像的画占位人形。 -->
      <div v-if="detail.actors?.length" class="jd-card" style="margin-top: 18px">
        <div class="jd-card__title"><i class="fas fa-user" /> 演员 ({{ detail.actors.length }})</div>
        <div class="jd-actors wall-detail__actors">
          <div v-for="a in detail.actors" :key="a.id || a.name" class="jd-actor wall-detail__actor">
            <div class="jd-actor__avatar wall-detail__actor-photo">
              <img v-if="a.avatar_url" :src="actorAvatar(a)" :alt="a.name" loading="lazy" />
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

      <!-- 剧照：网格铺开（照 Emby 的剧照区），点开灯箱 -->
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

      <!-- 源媒体信息：文件名 / 路径 / 大小 / 类型。
           路径是**容器内**的，按设置里的「宿主机路径映射」换算过 —— 群晖上看到的
           是映射后那条；没配映射时给容器内路径（至少真实，不会指向不存在的地方）。 -->
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

    <!-- 剧照灯箱：与 JavMovieDrawer 同一套类名与观感 -->
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
/* 番号影片墙的详情抽屉样式。
 *
 * 这一面**只有横版封面**，所以没有竖版那套（左窄右宽、封面 2:3）——
 * 那些在 TmdbWallDetailDrawer.vue 里。
 *
 * 演员矩形图、剧照网格、灯箱那几块的样式在 jav.css 的 `jd-*` 里（与影库详情页共用），
 * 这里只放**这一面自己**的东西：标签不做按钮的覆盖、以及选集面板。
 */

/* 标签改成纯展示（不是按钮）后，把 jav.css 里那条 `cursor: pointer` 盖掉 ——
   留着它，鼠标移上去还是「可点」的手型，等于骗人。
   hover 的高亮同理，一并去掉。 */
.jd-tags .jd-tag {
  cursor: default;
}

.jd-tags .jd-tag:hover {
  border-color: var(--border);
  color: var(--text-regular);
}

/* 选集面板：居中弹窗。z-index 取 --z-modal —— 详情抽屉是 elevated（10040），
   这个必须高过它，否则会被抽屉整个盖住（与 JavMovieDrawer 的灯箱同一个理由）。 */
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
