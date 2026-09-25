<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AppSelect from "@/components/base/AppSelect.vue";
import JavMovieCard from "@/components/admin/JavMovieCard.vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchJavRanking, javImageURL } from "@/api/jav";
import {
  JAV_RANK_TYPE_TABS,
  javTopTypeOptions,
  javTopTypeParams,
  type JavActor,
  type JavMovieCard as JavCard,
  type JavRankingKind,
} from "@/types/jav";
import "@/styles/jav.css";

/**
 * 榜单页。照源码 top250.html：
 *   五档 tab（Top250 / 日榜 / 周榜 / 月榜 / 演员榜）+ 卡片网格。
 *
 * 每页条数：Top250 = 40、日/周/月榜 = 20、演员榜一次取 500 不分页。
 * 这几个数不是随手定的 —— 日/周/月榜上游一次就给整榜（实测固定 60 条），
 * 本地切片翻页即可；Top250 是真的分页接口，页大小跟着走。
 *
 * 翻页用**拉到底自动加载**（内网那套也是这个交互）：日/周/月榜整榜只有 60 条、
 * Top250 是无限榜，都比点分页器顺手。
 */
const props = defineProps<{
  /** 已订阅的目标集合，键是 `${target_type}:${target_id}`。 */
  subscribedKeys: Set<string>;
}>();

const emit = defineEmits<{
  open: [movie: JavCard];
  subscribe: [movie: JavCard];
  subscribeActor: [actor: JavActor];
  openActor: [actor: JavActor];
}>();

const TABS: Array<{ key: JavRankingKind; label: string; icon: string }> = [
  { key: "top250", label: "Top250", icon: "🏆" },
  { key: "daily", label: "日榜", icon: "☀️" },
  { key: "weekly", label: "周榜", icon: "📅" },
  { key: "monthly", label: "月榜", icon: "🗓️" },
  { key: "actor", label: "演员榜", icon: "👤" },
];

// 默认「日榜」：榜单页最常看的是当天新片。Top250 变动慢，
// 排在日榜前面会让人每次进来都先看到一份没变化的列表。
const kind = ref<JavRankingKind>("daily");
const page = ref(1);
const loading = ref(false);
/** loadingMore 是「拉下一页中」，与首次/切档的 loading 分开 —— 前者只在底部转圈。 */
const loadingMore = ref(false);
const movies = ref<JavCard[]>([]);
const actors = ref<JavActor[]>([]);
const error = ref("");

/**
 * 内容分类。三档榜单共用这一个 ref，切 tab 时按各自的可用档位校正。
 *
 * 语义随榜单变：日/周/月与演员榜是 0/1/2/3；Top250 走 topType 那个下拉，
 * 用的是上游的 all / video_type / year，两套编码别混。
 */
const rankType = ref("0");
/** Top250 的下拉值：空 = 全部，'0'..'3' = 分类，四位年份 = 年份。 */
const topType = ref("");

/** 日/周/月榜与演员榜的分类胶囊。演员榜不给 FC2（上游静默回落成有码）。 */
const typeTabs = computed(() =>
  kind.value === "actor"
    ? JAV_RANK_TYPE_TABS.filter((t) => t.value !== "3")
    : JAV_RANK_TYPE_TABS,
);

const topTypeOptions = computed(() => javTopTypeOptions());

/** 服务端报的总条数。整榜 60 / Top250 的 250 / 演员数。 */
const total = ref(0);

/** 条数胶囊显示的数字。Top250 就是 250（源站固定榜单），其余按服务端报的来。 */
const count = computed(() => {
  if (kind.value === "top250") return total.value || 250;
  if (kind.value === "actor") return actors.value.length || total.value;
  return total.value || movies.value.length;
});

/**
 * 还有没有下一页。
 *
 * 靠**服务端报的 total** 算，不再用「这一页拿满了没」去猜 —— 那个猜法在
 * 整榜 60 条、每页 20 时会得出「还有第 4 页」，拉下去是一个空页。
 */
const hasMore = computed(() => {
  if (kind.value === "actor") return false;
  if (!total.value) return false;
  return movies.value.length < total.value;
});

/** 底部那条状态文案该不该显示。演员榜不分页、出错、首屏还没回来时都不显示。 */
const showLoadMore = computed(
  () => !error.value && kind.value !== "actor" && (loadingMore.value || movies.value.length > 0),
);

/** 卡片上的订阅状态键。演员榜用的是演员类型。 */
function movieKey(movie: JavCard) {
  return `movie:${movie.id.toLowerCase()}`;
}

function actorKey(actor: JavActor) {
  return `actor:${actor.id.toLowerCase()}`;
}

/** 当前榜单对应的请求参数。 */
function requestOptions(extra?: { page?: number }) {
  if (kind.value === "top250") {
    const { type, typeValue } = javTopTypeParams(topType.value);
    return { type, typeValue, page: extra?.page ?? page.value };
  }
  return { type: rankType.value, page: extra?.page ?? page.value };
}

/** load 拉一页。append 为 true 时接在现有列表后面（拉到底加载）。 */
async function load(append = false) {
  if (append) {
    if (loading.value || loadingMore.value || !hasMore.value) return;
    loadingMore.value = true;
  } else {
    loading.value = true;
    error.value = "";
  }
  const target = append ? page.value + 1 : page.value;
  try {
    const res = await fetchJavRanking(kind.value, requestOptions({ page: target }));
    const next = res.movies ?? [];
    if (append) {
      // 按 id 去重再拼：榜单在两次请求之间可能变动，重复的 key 会让 Vue 报
      // 「Duplicate keys」并且渲染错位。
      const seen = new Set(movies.value.map((m) => m.id));
      movies.value = [...movies.value, ...next.filter((m) => !seen.has(m.id))];
    } else {
      movies.value = next;
      actors.value = res.actors ?? [];
    }
    total.value = res.total ?? 0;
    page.value = target;
  } catch (err) {
    // 追加失败保持已有内容：滚到一半一次请求失败，不该把看过的一屏清空。
    // 首屏失败才清空（那份数据本来就是空的）。
    error.value = getApiErrorMessage(err, "榜单加载失败");
    if (!append) {
      movies.value = [];
      actors.value = [];
      total.value = 0;
    }
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
}

/** reset 回到第一页并重新拉。切 tab / 切分类都走它。 */
function reset() {
  page.value = 1;
  total.value = 0;
  movies.value = [];
  actors.value = [];
  error.value = "";
  void load();
}

function selectTab(next: JavRankingKind) {
  if (kind.value === next) return;
  kind.value = next;
  // 演员榜没有 FC2 这一档，从日榜带着 3 切过去会显示有码的名单挂在 FC2 下 ——
  // 上游 type=3 就是静默回落成 0 的。切过去时先归到有码。
  if (next === "actor" && rankType.value === "3") rankType.value = "0";
  reset();
}

function pickType(next: string) {
  if (rankType.value === next) return;
  rankType.value = next;
  reset();
}

watch(topType, () => reset());

// ————————————————————— 拉到底自动加载 —————————————————————
//
// 内网那套也是这个交互（IntersectionObserver + 底部哨兵），比点分页器顺手：
// 日/周/月榜整榜只有 60 条，Top250 本身就是个无限榜。
//
// 哨兵**常驻 DOM**（不是 v-if）—— 它一旦被移除，挂在它上面的观察器就再也
// 收不到回调了。这一条与影库那一档（JavLibraryPanel）是同一个写法。
const sentinel = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;

onMounted(() => {
  void load();
  if (typeof IntersectionObserver === "undefined" || !sentinel.value) return;
  observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) void load(true);
    },
    // 提前 300px 就开始拉，滚到底时下一页通常已经在了 —— 与影库同一档间距。
    { rootMargin: "300px" },
  );
  observer.observe(sentinel.value);
});

onBeforeUnmount(() => {
  observer?.disconnect();
  observer = null;
});
</script>

<template>
  <div>
    <div class="jav-rank-head">
      排行榜
      <span class="jav-rank-count">{{ count }}</span>
    </div>

    <!-- 榜单档位与内容分类**同一排**，中间一条短竖线隔开 —— 两者是同一层的
         两个维度（看哪份榜 × 看哪一类），拆成两行会让人以为是主从关系。 -->
    <div class="jav-tabs">
      <button
        v-for="tab in TABS"
        :key="tab.key"
        type="button"
        class="jav-tab"
        :class="{ 'jav-tab--active': kind === tab.key }"
        @click="selectTab(tab.key)"
      >
        <span aria-hidden="true">{{ tab.icon }}</span>{{ tab.label }}
      </button>

      <template v-if="kind !== 'top250'">
        <span class="jav-tabs__sep" aria-hidden="true"></span>
        <button
          v-for="t in typeTabs"
          :key="t.value"
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': rankType === t.value }"
          @click="pickType(t.value)"
        >
          {{ t.label }}
        </button>
      </template>
    </div>

    <!-- Top250 那一档的分类不是四颗胶囊而是一个「分类/年份」下拉，塞进同一排会把
         那排撑得很长，所以它仍然单独一行。 -->
    <div v-if="kind === 'top250'" class="jav-rank-controls">
      <span class="jav-rank-controls__label">分类</span>
      <AppSelect v-model="topType" :options="topTypeOptions" style="width: 150px" />
    </div>

    <div v-if="error" class="jav-empty">
      <div class="jav-empty__icon">⚠️</div>
      <div>{{ error }}</div>
    </div>

    <template v-else-if="kind === 'actor'">
      <div v-if="loading" class="jav-empty">加载中…</div>
      <div v-else-if="actors.length" class="jav-actor-grid">
        <div
          v-for="(actor, index) in actors"
          :key="actor.id"
          class="jav-actor-card"
          role="button"
          tabindex="0"
          @click="emit('openActor', actor)"
          @keyup.enter="emit('openActor', actor)"
        >
          <span
            class="jav-rank-num"
            :class="{
              'jav-rank-num--r1': index === 0,
              'jav-rank-num--r2': index === 1,
              'jav-rank-num--r3': index === 2,
              'jav-rank-num--rn': index > 2,
            }"
            >{{ index + 1 }}</span
          >
          <button
            type="button"
            class="jav-card__sub"
            :class="{ 'jav-card__sub--on': subscribedKeys.has(actorKey(actor)) }"
            @click.stop="emit('subscribeActor', actor)"
          >
            {{ subscribedKeys.has(actorKey(actor)) ? "已订阅" : "订阅" }}
          </button>
          <div class="jav-actor-avatar">
            <!-- 头像同样要过代理：上游的图是 XOR 混淆的，直链会是花屏。
                 全模块只有这一处漏了包 javImageURL。 -->
            <img
              v-if="actor.avatar_url"
              :src="javImageURL(actor.avatar_url)"
              :alt="actor.name"
              loading="lazy"
            />
            <span v-else>👤</span>
          </div>
          <div class="jav-actor-name">{{ actor.name }}</div>
        </div>
      </div>
      <div v-else class="jav-empty">
        <div class="jav-empty__icon">⭐</div>
        <div>暂无数据。</div>
      </div>
    </template>

    <template v-else>
      <div v-if="loading" class="jav-empty">加载中…</div>
      <div v-else-if="movies.length" class="jav-grid">
        <JavMovieCard
          v-for="(movie, index) in movies"
          :key="movie.id"
          :movie="movie"
          :rank="kind === 'top250' ? index + 1 : 0"
          :subscribed="subscribedKeys.has(movieKey(movie))"
          @open="emit('open', movie)"
          @subscribe="emit('subscribe', movie)"
        />
      </div>
      <div v-else class="jav-empty">
        <div class="jav-empty__icon">⭐</div>
        <div>暂无数据。</div>
      </div>
    </template>

    <!-- 滚动到底自动续上。
         哨兵**常驻 DOM**（不放在上面的 v-if/v-else 分支里）—— 挂在它上面的
         IntersectionObserver 是 onMounted 时挂一次的，一旦它被移除就再也收不到
         回调；而切 tab 会让分支重渲染。所以它留在这层。
         没有文案时（演员榜不分页、出错、首屏还没回来）用 --idle 把高度收掉，
         免得底部留一条空白。 -->
    <div
      ref="sentinel"
      class="jav-loadmore"
      :class="{ 'jav-loadmore--idle': !showLoadMore }"
    >
      <span v-if="loadingMore">加载中…</span>
      <span v-else-if="hasMore">往下滚，还有 {{ total - movies.length }} 条</span>
      <span v-else-if="movies.length">已全部加载（{{ total }} 条）</span>
    </div>
  </div>
</template>
