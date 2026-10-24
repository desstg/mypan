<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { javImageURL } from "@/api/jav";
import {
  fetchJavWallActorDetail,
  fetchJavWallActorProfile,
  fetchJavWallActorTMDB,
  javActorPhotoURL,
  type JavWallActorCard,
  type JavWallActorMovie,
  type JavWallActorProfile,
  type JavWallActorTMDB,
} from "@/api/strmJavWall";
import type { WallSourceRef } from "@/api/strmScrape";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import "@/styles/jav.css";

/**
 * 演员详情抽屉（演员墙里点一张卡打开）。
 *
 * # 数据来自**墙上的影片**
 *
 * `fetchJavWallActorDetail` 回的是「这位演员参演的、墙上真实存在的那几部」——
 * 后端从每部片的侧车 json / nfo 里读出来再聚合（见 strmscrape/javwall_actors.go），
 * 不是上游演员页的参演列表。所以这里每一张卡都能在墙上一部一部找到。
 *
 * # 主名与别名
 *
 * 列表带回来的 `name` 已经是**索引表里的中文统一名**（没这条时就是侧车里的原名），
 * `aliases` 是同一张表里的其它写法（日文原名、罗马音、旧艺名）。用户要求
 * （2026-10-21）主名显示统一名、别名一并列出来 —— 别名那行点一下就能复制/搜索，
 * 因为用户往往只记得侧车里那个日文写法。
 *
 * # TMDB 档案是**补充**
 *
 * 侧车 / nfo 只有 id、名字、性别、头像四样。TMDB 那边可能有简介、生日、出生地，
 * 就顺手显示一段（后端按名字核对过人才会给，见 lookJavActorTMDB）。
 * ⚠️ 它**不参与**「演过哪些片」—— 那一栏永远只有墙上的影片，这是用户明确要求的。
 *
 * # 影片卡片点开的是**那一面墙**的详情抽屉
 *
 * 每张卡带齐了 `strm_task_id` / `local_dir_id` + `rel_dir` + `stem`，点一下往上传
 * （`open-movie`），由演员墙那边调 `useWallDetail().openJav(...)` —— 与在海报墙上
 * 点一张卡**逐字相同**的那条路（同一个抽屉、同一个播放窗、同一套字幕）。
 *
 * ⚠️ 不在这里自己开抽屉：那个抽屉是 elevated（10040）一档，而本抽屉也是；两个
 * **兄弟**叠在一起时谁在上面由 DOM 顺序决定，不好控。交给外层统一挂一份，
 * 关掉影片详情之后回到这里，面包屑是干净的。
 */
const props = defineProps<{
  open: boolean;
  actor: JavWallActorCard | null;
  /**
   * 别名点一下能不能拿去搜演员墙。
   *
   * 从**演员墙**打开时能（那份列表就在下面，搜索框是它的）；从**海报墙的影片详情**
   * 打开时不能 —— 那里没有演员墙可搜，做成可点却点了没反应比不可点更糟。
   */
  searchable?: boolean;
  /**
   * 打开时压在**另一个 elevated 抽屉**之上（例如从影片详情里点演员进来）。
   *
   * 只用 `elevated` 时两个抽屉同档，谁在上面看 DOM 顺序 —— 电影详情写在后面就把它盖住。
   */
  raised?: boolean;
  /**
   * 暂时收起（**不卸载内容**），给「从参演影片里开播放窗」那一路用。
   *
   * 播放窗 `.file-preview` 的 z-index 是 10000，本抽屉是 elevated（10040）——
   * 直接播会被它整个盖住。关掉 `open` 也能收起来，但那样会**清空**已取的影片与
   * 资料、重开时整页重拉一遍并跳回顶部；所以这里单独给一个只影响显示的开关。
   */
  suspended?: boolean;
}>();

const emit = defineEmits<{
  close: [];
  /** 打开这一部**墙上的**影片（数据源 + 定位三件套）。 */
  openMovie: [source: WallSourceRef, movie: JavWallActorMovie];
  /**
   * 点别名 → 拿它去搜演员墙。
   *
   * 第二个参数把**这位演员本人**一并交出去：别名的来源常常是 avbase / JAVDB
   * （罗马音、旧艺名），墙上的关键词索引里根本没有这个写法，只拿文字去搜必然
   * 搜出空列表。外层据此「点名」锁定这一位，结果与搜主名看到的是同一张卡。
   */
  search: [keyword: string, actor: JavWallActorCard];
}>();

const detail = ref<JavWallActorCard | null>(null);
const movies = ref<JavWallActorMovie[]>([]);
/** TMDB 补充档案：**单独一条请求**，晚到 / 失败都只影响那一段。 */
const tmdbInfo = ref<JavWallActorTMDB | null>(null);
/** 资料档案（生日 / 出身 / 三围 / 头像）：同样是一条独立的补充请求。 */
const profileInfo = ref<JavWallActorProfile | null>(null);
const loading = ref(false);
const error = ref("");

/** 表头优先用刷新回来的那份，没有再回落到列表带过来的。 */
const header = computed(() => detail.value ?? props.actor);
const moviesCount = computed(() => header.value?.movies_count ?? movies.value.length);
const isMale = computed(() => (header.value?.gender ?? 0) === 1);
/** TMDB 补充档案：**单独一条请求**，晚到 / 失败都只影响那一段（后端不塞进详情）。 */
const tmdb = computed(() => tmdbInfo.value);
/** 资料档案（avbase / JAVDB / 索引表）：同样是补充，晚到只影响那一段。 */
const profile = computed(() => profileInfo.value);

/**
 * 别名：索引表 + avbase + JAVDB 三边并集（不含主名）。
 *
 * 列表带回的那份只有索引表里的写法 —— 很多演员（尤其是新人、欧美片）根本没进
 * 那张表，而 avbase / JAVDB 那边却有别名。两边并起来才够用。
 */
const aliases = computed(() => {
  const out: string[] = [];
  const seen = new Set<string>();
  const main = (header.value?.name ?? "").toLowerCase();
  for (const list of [header.value?.aliases ?? [], profile.value?.aliases ?? []]) {
    for (const a of list) {
      const name = a.trim();
      if (!name) continue;
      const key = name.toLowerCase();
      if (key === main || seen.has(key)) continue;
      seen.add(key);
      out.push(name);
    }
  }
  return out;
});

/** TMDB 那边另一个名字（统一名对不上时，看一眼就知道是不是同一个人）。 */
const tmdbOtherNames = computed(() => {
  const t = tmdb.value;
  if (!t) return [] as string[];
  const main = (header.value?.name ?? "").toLowerCase();
  const seen = new Set<string>([main, (t.name ?? "").toLowerCase()]);
  const out: string[] = [];
  for (const a of t.also_known_as ?? []) {
    const name = a.trim();
    if (!name) continue;
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(name);
  }
  return out;
});

/** 「生日（享年）」：有卒日才写享年，别给在世的人算一个数出来。 */
const lifeText = computed(() => {
  // 资料层（avbase）的生日最全，优先；TMDB 那份常常是空的。
  if (profile.value?.birthday) {
    const t = tmdb.value;
    if (t?.deathday) return `${profile.value.birthday} — ${t.deathday}`;
    return profile.value.birthday;
  }
  const t = tmdb.value;
  if (!t?.birthday) return "";
  if (!t.deathday) return t.birthday;
  return `${t.birthday} — ${t.deathday}`;
});

/** 出生地：资料层（avbase 的「出身地」）优先，其次 TMDB。 */
const placeText = computed(() => {
  return profile.value?.birthplace?.trim() || tmdb.value?.place_of_birth?.trim() || "";
});

/** 三围那一行：只拼有的那几项（`158cm · B101 / W55 / H84 · J罩杯`）。 */
const bodyText = computed(() => {
  const p = profile.value;
  if (!p) return "";
  const parts: string[] = [];
  if (p.height && p.height > 0) parts.push(`${p.height}cm`);
  const three = [] as string[];
  if (p.bust && p.bust > 0) three.push(`B${p.bust}`);
  if (p.waist && p.waist > 0) three.push(`W${p.waist}`);
  if (p.hip && p.hip > 0) three.push(`H${p.hip}`);
  if (three.length) parts.push(three.join(" / "));
  if (p.cup) parts.push(`${p.cup}罩杯`);
  return parts.join(" · ");
});

/** 资料层额外那几项（血型 / 爱好 / 站点标签）。 */
const extraFacts = computed(() => {
  const p = profile.value;
  if (!p) return [] as string[];
  const out: string[] = [];
  if (p.blood_type) out.push(`血型 ${p.blood_type}`);
  if (p.hobby) out.push(`爱好 ${p.hobby}`);
  if (p.note) out.push(p.note);
  return out;
});

/**
 * 维基导言（**介绍**）。只有维基有这一段 —— avbase / JAVDB / TMDB 对番号演员
 * 基本都不给介绍（用户 2026-10-22 问的「介绍的吗？」）。
 *
 * 有中文条目时后端给的就是中文，否则是日文原文：那时标一句「日文原文」，
 * 免得用户以为是没翻译好的中文。
 */
const intro = computed(() => profile.value?.intro?.trim() ?? "");
const introIsJa = computed(() => (profile.value?.intro_lang ?? "") === "ja");

/**
 * 人像地址：资料层的高清那张 > 资料层的小图 > TMDB > 侧车（列表里的头像）。
 *
 * 「高清」那张来自维基条目首图（960 宽），而 JAVDB 只有 300×300、dmm 那张
 * 90×122 —— 在 120px 的框里，小图在高分屏上肉眼就是糊的。它**只在这里用**：
 * 墙上的卡片仍然用 `avatar_url`（一屏几十张，拉大图是白花流量）。
 */
const photoChain = computed(() => {
  const out: string[] = [];
  const push = (u: string) => {
    if (u && !out.includes(u)) out.push(u);
  };
  push(javImageURL(profile.value?.avatar_hd_url));
  push(javImageURL(profile.value?.avatar_url));
  push(javActorPhotoURL(tmdb.value?.profile_path, "w185"));
  push(javImageURL(header.value?.avatar_url));
  return out;
});

/**
 * 头像加载失败时**退到下一档**，而不是直接落占位图。
 *
 * 资料层给的地址是「探针能取到」的那一张（后端逐个真取过），但探针与浏览器
 * 之间还隔着一次代理 / 一次缓存失效；高清那张又只有维基一个源 —— 失败时退到
 * 小图、TMDB、侧车，都不行才让 `MediaImage` 落占位图。不要留一张破图。
 */
const photoTried = ref(0);
watch(photoChain, () => {
  photoTried.value = 0;
});
const effectivePhotoSrc = computed(() => {
  const chain = photoChain.value;
  return chain[Math.min(photoTried.value, Math.max(chain.length - 1, 0))] ?? "";
});
function onPhotoError() {
  if (photoTried.value < photoChain.value.length - 1) photoTried.value++;
}

/** 演员的片可能横跨好几面粉号墙 —— 多于一面时才在卡片上标来源。 */
const sourceCount = computed(() => {
  const seen = new Set<string>();
  for (const m of movies.value) seen.add(m.local_dir_id ? `L:${m.local_dir_id}` : `T:${m.strm_task_id ?? 0}`);
  return seen.size;
});

let loadSeq = 0;

watch(
  () => [props.open, props.actor?.key] as const,
  ([open, key]) => {
    if (!open || !key) {
      // 关掉也要推进序号：在途的两条请求回来时一律作废，
      // 否则它们会把上一位演员的简介 / 影片写进下一次打开的这一屏。
      loadSeq++;
      detail.value = null;
      movies.value = [];
      tmdbInfo.value = null;
      profileInfo.value = null;
      loading.value = false;
      error.value = "";
      return;
    }
    void load(String(key));
  },
  { immediate: true },
);

async function load(key: string) {
  const seq = ++loadSeq;
  detail.value = props.actor;
  movies.value = [];
  tmdbInfo.value = null;
  profileInfo.value = null;
  error.value = "";
  loading.value = true;
  // 三条请求**并行发**：两条补充资料慢 / 不通，不该拖着这一页的影片。
  void loadProfile(key, seq);
  void loadTMDB(key, seq);
  try {
    const res = await fetchJavWallActorDetail(key);
    if (seq !== loadSeq) return;
    if (res.actor) detail.value = res.actor;
    movies.value = res.items ?? [];
  } catch (e) {
    if (seq !== loadSeq) return;
    error.value = getApiErrorMessage(e, "加载演员详情失败");
  } finally {
    if (seq === loadSeq) loading.value = false;
  }
}

/**
 * 资料档案（生日 / 出身 / 三围 / 头像 / 别名）。失败 / 没有人都**静默跳过**：
 * 详情页其余内容照常显示 —— 这段是补充，缺了不该让人以为出错了。
 *
 * 它与 TMDB 那条的关系：这一条命中率高得多（番号圈的源），TMDB 用来补简介。
 */
async function loadProfile(key: string, seq: number) {
  try {
    const info = await fetchJavWallActorProfile(key);
    if (seq !== loadSeq) return;
    profileInfo.value = info ?? null;
  } catch {
    if (seq !== loadSeq) return;
    profileInfo.value = null;
  }
}

/**
 * TMDB 补充档案（失败 / 没有人都**静默跳过**：详情页其余内容照常显示）。
 *
 * ⚠️ 不能用 `seq !== loadSeq` 判之外的东西拦住它 —— 换一位演员时旧结果必须丢掉，
 * 否则会把这人的简介贴到下一个人头上。
 */
async function loadTMDB(key: string, seq: number) {
  try {
    const info = await fetchJavWallActorTMDB(key);
    if (seq !== loadSeq) return;
    tmdbInfo.value = info ?? null;
  } catch {
    if (seq !== loadSeq) return;
    tmdbInfo.value = null;
  }
}

/** 卡片的图：海报优先（竖版网格），没有才回落缩略图。 */
function movieImage(movie: JavWallActorMovie): string {
  return movie.poster_url || movie.thumb_url || "";
}

/** 定位到那一面墙：本地目录源优先（与 sourceParams 同一条规矩）。 */
function sourceOf(movie: JavWallActorMovie): WallSourceRef {
  if (movie.local_dir_id) return { local_dir_id: movie.local_dir_id };
  return { strm_task_id: movie.strm_task_id ?? 0 };
}

function openMovie(movie: JavWallActorMovie) {
  emit("openMovie", sourceOf(movie), movie);
}

/**
 * 点别名 → 拿它去搜演员墙。
 *
 * 把**这位演员本人的卡片**一起交出去：别名的来源常常是 avbase / JAVDB
 * （罗马音、旧艺名），墙上的关键词索引里根本没有这个写法 —— 外层拿这串字
 * 去过滤必然空手。带上 key 之后，「点别名」与「搜主名」看到的是同一张卡。
 */
function searchAlias(name: string) {
  if (!props.searchable) return;
  const actor = header.value;
  if (!actor) return;
  emit("search", name, actor);
}
</script>

<template>
  <!-- elevated：演员墙本身是 2500 那一档的整页浮层，默认档（120）会被它整个盖住。 -->
  <AdminSettingsDrawer
    elevated
    :raised="raised"
    :open="open && !suspended"
    :title="header?.name || '演员'"
    hide-foot
    @close="emit('close')"
    @cancel="emit('close')"
  >
    <div v-if="!header" class="jav-empty">加载中…</div>

    <template v-else>
      <div class="jav-wall-actor-detail__head">
        <div class="jav-wall-actor-detail__avatar">
          <MediaImage
            :src="effectivePhotoSrc"
            :alt="header.name"
            variant="person"
            :lazy="false"
            @error="onPhotoError"
          />
        </div>
        <div class="jav-wall-actor-detail__info">
          <h1 class="jav-wall-actor-detail__name">
            {{ header.name || "（未命名）" }}
            <span v-if="isMale" class="jav-wall-actor-detail__gender">男</span>
          </h1>

          <!-- 别名：索引表里这位演员的其它写法（日文原名 / 罗马音 / 旧艺名）。
               点一下拿它去搜 —— 用户往往只记得侧车里那个日文写法。 -->
          <div v-if="aliases.length" class="jav-wall-actor-detail__aliases">
            <span class="jav-wall-actor-detail__alias-label">别名</span>
            <button
              v-for="alias in aliases"
              :key="alias"
              type="button"
              class="jav-wall-actor-detail__alias"
              :class="{ 'jav-wall-actor-detail__alias--static': !searchable }"
              :title="searchable ? `搜「${alias}」` : alias"
              @click="searchAlias(alias)"
            >{{ alias }}</button>
          </div>

          <div class="jav-wall-actor-detail__meta">
            <span>{{ moviesCount }} 部作品</span>
            <span v-if="header.id" class="jav-wall-actor-detail__id">{{ header.id }}</span>
          </div>

          <!-- 资料档案（avbase → JAVDB → 索引表）：生日 / 出身 / 三围 / 额外那几项。
               这是番号圈的源，命中率比 TMDB 高得多 —— 用户要的「生日、出生」就在这。 -->
          <div v-if="profile && (lifeText || placeText || bodyText || extraFacts.length)" class="jav-wall-actor-detail__profile">
            <span v-if="lifeText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-cake-candles" aria-hidden="true" />{{ lifeText }}
            </span>
            <span v-if="placeText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-location-dot" aria-hidden="true" />{{ placeText }}
            </span>
            <span v-if="bodyText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-ruler-vertical" aria-hidden="true" />{{ bodyText }}
            </span>
            <span v-for="fact in extraFacts" :key="fact" class="jav-wall-actor-detail__fact">
              {{ fact }}
            </span>
          </div>

          <!-- TMDB 那边对上的人：名字 / 生日 / 出生地。只在真抓到档案时出现。
               生日与出生地已经在上面那一行由资料层显示过时，这里不再重复。 -->
          <div v-if="tmdb" class="jav-wall-actor-detail__tmdb-meta">
            <span v-if="tmdb.name && tmdb.name !== header.name" class="jav-wall-actor-detail__tmdb-name">
              TMDB：{{ tmdb.name }}
            </span>
            <span v-if="!profile?.birthday && lifeText">{{ lifeText }}</span>
            <span v-if="!placeText && tmdb.place_of_birth">{{ tmdb.place_of_birth }}</span>
            <span v-if="tmdbOtherNames.length" class="jav-wall-actor-detail__tmdb-alias">
              {{ tmdbOtherNames.slice(0, 4).join(" / ") }}
            </span>
          </div>
          <div class="jav-wall-actor-detail__hint">
            这些是番号墙上的影片，点开看详情、播放。
          </div>
        </div>
      </div>

      <!-- 介绍（维基导言）：资料层里唯一有「介绍」的源，而且只有 200~400 字，
           不用收起来。日文原文时标一句，免得当成没翻译好的中文。 -->
      <div v-if="intro" class="jav-wall-actor-detail__intro">
        <div class="jav-wall-actor-detail__intro-head">
          <span class="jav-wall-actor-detail__intro-label">介绍</span>
          <span v-if="introIsJa" class="jav-wall-actor-detail__intro-lang">日文原文</span>
          <a
            v-if="profile?.wiki_url"
            class="jav-wall-actor-detail__intro-link"
            :href="profile.wiki_url"
            target="_blank"
            rel="noopener noreferrer"
          >维基百科</a>
        </div>
        <p>{{ intro }}</p>
      </div>

      <!-- TMDB 简介：**补充资料**，演过哪些片不看它（那一栏只有墙上的影片）。
           已经有维基那段时不再展开另一段（同一个人写两遍，读者只会困惑）。 -->
      <details v-if="!intro && tmdb?.biography" class="jav-wall-actor-detail__bio">
        <summary>简介</summary>
        <p>{{ tmdb.biography }}</p>
      </details>

      <h2 class="jav-wall-actor-detail__section">参演影片</h2>

      <div v-if="loading" class="jav-empty">加载中…</div>
      <div v-else-if="error && !movies.length" class="jav-empty">
        <div class="jav-empty__icon">⚠️</div>
        <div>{{ error }}</div>
      </div>
      <div v-else-if="!movies.length" class="jav-empty">
        墙上的影片里还没有这位演员的片。
      </div>
      <!-- 卡片尺寸与海报墙（`--poster` 视图）逐条对齐：列宽 140 / 间距 14 / 封面 2:3。 -->
      <div v-else class="jav-wall-actor-detail__grid">
        <article
          v-for="movie in movies"
          :key="`${movie.local_dir_id || movie.strm_task_id}:${movie.rel_dir}:${movie.stem}`"
          class="jav-card jav-wall-actor-detail__card"
          role="button"
          tabindex="0"
          :title="`${movie.number || movie.title}\n${movie.title}`"
          @click="openMovie(movie)"
          @keyup.enter.prevent="openMovie(movie)"
          @keydown.space.prevent="openMovie(movie)"
        >
          <div class="jav-card__cover jav-wall-actor-detail__cover">
            <MediaImage :src="movieImage(movie)" :alt="movie.number || movie.title" :lazy="false" />
            <!-- 跨墙时才标来源：只有一面墙时这句是噪声。 -->
            <span v-if="sourceCount > 1 && movie.source_name" class="jav-wall-actor-detail__source">
              {{ movie.source_name }}
            </span>
          </div>
          <div class="jav-wall-actor-detail__meta-card">
            <div class="jav-wall-actor-detail__number" :title="movie.number">{{ movie.number }}</div>
            <div class="jav-wall-actor-detail__title" :title="movie.title">{{ movie.title }}</div>
            <div v-if="movie.release_date" class="jav-wall-actor-detail__sub">{{ movie.release_date }}</div>
          </div>
        </article>
      </div>
    </template>
  </AdminSettingsDrawer>
</template>

<style scoped>
.jav-wall-actor-detail__head {
  display: flex;
  align-items: center;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border-soft);
}

/* 头像：与墙面卡片同一个 2:3 竖版，给到 120px 宽 —— 详情里要看清脸。 */
.jav-wall-actor-detail__avatar {
  flex: 0 0 auto;
  width: 120px;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
}
.jav-wall-actor-detail__avatar img,
.jav-wall-actor-detail__avatar .media-ph {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.jav-wall-actor-detail__info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.jav-wall-actor-detail__name {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 700;
  color: var(--text);
  word-break: break-word;
}

.jav-wall-actor-detail__gender {
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: rgba(59, 130, 246, 0.92);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}

.jav-wall-actor-detail__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  color: var(--text-muted);
  font-size: 12.5px;
}

/* 别名那一行：一颗颗小胶囊，点了拿它去搜。 */
.jav-wall-actor-detail__aliases {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.jav-wall-actor-detail__alias-label {
  color: var(--text-muted);
  font-size: 11.5px;
}
.jav-wall-actor-detail__alias {
  padding: 1px 7px;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11.5px;
  cursor: pointer;
}
.jav-wall-actor-detail__alias:hover {
  border-color: var(--brand);
  color: var(--brand);
}
/* 不可搜时（从海报墙的影片详情打开）：保持胶囊外观，但不要「点了会有事」的暗示。 */
.jav-wall-actor-detail__alias--static {
  cursor: default;
}
.jav-wall-actor-detail__alias--static:hover {
  border-color: var(--border);
  color: var(--text-muted);
}

/* 资料档案那一行：生日 / 出身 / 三围。比 TMDB 那行**更实**（它是主资料），
   所以用正常文字色，图标压暗一档。 */
.jav-wall-actor-detail__profile {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 14px;
  color: var(--text);
  font-size: 12.5px;
}
.jav-wall-actor-detail__fact {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.jav-wall-actor-detail__fact i {
  color: var(--text-muted);
  font-size: 11.5px;
}

/* TMDB 那几项：比别名更淡一档 —— 它们是补充资料，不该和主信息抢。 */
.jav-wall-actor-detail__tmdb-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  color: var(--text-muted);
  font-size: 12px;
  opacity: 0.9;
}
.jav-wall-actor-detail__tmdb-name {
  font-weight: 600;
}
.jav-wall-actor-detail__tmdb-alias {
  opacity: 0.8;
}

/* 简介默认收起（有些人的 TMDB 简介有几千字），点一下展开。 */
.jav-wall-actor-detail__bio {
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12.5px;
  line-height: 1.7;
}
.jav-wall-actor-detail__bio summary {
  cursor: pointer;
  color: var(--text);
  font-weight: 600;
  font-size: 12.5px;
}
.jav-wall-actor-detail__bio p {
  margin: 8px 0 0;
  white-space: pre-wrap;
}

/* 介绍（维基导言）：200~400 字，直接展开，不用点。 */
.jav-wall-actor-detail__intro {
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12.5px;
  line-height: 1.7;
}
.jav-wall-actor-detail__intro-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.jav-wall-actor-detail__intro-label {
  color: var(--text);
  font-weight: 600;
}
/* 「日文原文」只是提示，别做成按钮。 */
.jav-wall-actor-detail__intro-lang {
  font-size: 11px;
  opacity: 0.7;
}
.jav-wall-actor-detail__intro-link {
  margin-left: auto;
  color: var(--brand);
  font-size: 11.5px;
  text-decoration: none;
}
.jav-wall-actor-detail__intro-link:hover {
  text-decoration: underline;
}
.jav-wall-actor-detail__intro p {
  margin: 8px 0 0;
  white-space: pre-wrap;
}

.jav-wall-actor-detail__id {
  font-family: var(--font-mono, monospace);
  opacity: 0.75;
}

.jav-wall-actor-detail__hint {
  color: var(--text-muted);
  font-size: 12px;
}

.jav-wall-actor-detail__section {
  margin: 18px 0 12px;
  font-size: 14px;
  font-weight: 700;
  color: var(--text);
}

/* ⚠️ 与 StrmJavWall 的 `.jav-wall__grid--poster` / 演员墙的网格**逐条对齐**。 */
.jav-wall-actor-detail__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 14px;
}

.jav-wall-actor-detail__card {
  overflow: hidden;
}

.jav-wall-actor-detail__cover {
  aspect-ratio: 2 / 3;
}

.jav-wall-actor-detail__source {
  position: absolute;
  top: 6px;
  left: 6px;
  z-index: 2;
  max-width: calc(100% - 12px);
  padding: 1px 7px;
  border-radius: 5px;
  background: rgba(15, 23, 42, 0.62);
  color: #fff;
  font-size: 10.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jav-wall-actor-detail__meta-card {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px 10px;
}
.jav-wall-actor-detail__number {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jav-wall-actor-detail__title {
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jav-wall-actor-detail__sub {
  font-size: 11px;
  color: var(--text-muted);
}
</style>
