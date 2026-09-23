<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppPagination from "@/components/base/AppPagination.vue";
import JavMovieCard from "@/components/admin/JavMovieCard.vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchJavRanking, javImageURL } from "@/api/jav";
import type { JavActor, JavMovieCard as JavCard, JavRankingKind } from "@/types/jav";
import "@/styles/jav.css";

/**
 * 榜单页。照源码 top250.html：
 *   五档 tab（Top250 / 日榜 / 周榜 / 月榜 / 演员榜）+ 卡片网格 + 分页。
 *
 * 每页条数与源码一致：Top250 = 40、日/周/月榜 = 20、演员榜一次取 500 不分页。
 * 这几个数不是随手定的 —— 热播榜上游一次就给整榜，本地切片翻页即可；
 * Top250 是真的分页接口，页大小跟着走。
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

const kind = ref<JavRankingKind>("top250");
const page = ref(1);
const loading = ref(false);
const movies = ref<JavCard[]>([]);
const actors = ref<JavActor[]>([]);
const error = ref("");

/** 每页条数：与源码一致。演员榜不分页，所以恒为 1。 */
const PER_PAGE: Record<JavRankingKind, number> = {
  top250: 40,
  daily: 20,
  weekly: 20,
  monthly: 20,
  actor: 500,
};

/** 条数胶囊显示的数字。Top250 就是 250（源站固定榜单），其余按实际拿到多少。 */
const count = computed(() => {
  if (kind.value === "top250") return 250;
  if (kind.value === "actor") return actors.value.length;
  // 热播榜一次给整榜，但本地只拿到切片 —— 用总条数才准确，拿不到就退回当前页条数。
  return totalMovies.value || movies.value.length;
});

/** 热播榜的总条数。分页要靠它算总页数。 */
const totalMovies = ref(0);

const totalPages = computed(() => {
  if (kind.value === "actor") return 1;
  const total = kind.value === "top250" ? 250 : totalMovies.value;
  if (!total) return 1;
  return Math.max(1, Math.ceil(total / PER_PAGE[kind.value]));
});

/** 卡片上的订阅状态键。演员榜用的是演员类型。 */
function movieKey(movie: JavCard) {
  return `movie:${movie.id.toLowerCase()}`;
}

function actorKey(actor: JavActor) {
  return `actor:${actor.id.toLowerCase()}`;
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const res = await fetchJavRanking(kind.value, { page: page.value });
    movies.value = res.movies ?? [];
    actors.value = res.actors ?? [];
    // 热播榜后端一次给整榜再本地切片，所以只能自己估总数：
    // 拿满一页就说明后面还有。估错顶多多一个空页，比显示「共 1 页」然后
    // 用户找不到下一页要好。
    if (kind.value !== "top250" && kind.value !== "actor") {
      const filled = movies.value.length >= PER_PAGE[kind.value];
      totalMovies.value = filled
        ? page.value * PER_PAGE[kind.value] + 1
        : (page.value - 1) * PER_PAGE[kind.value] + movies.value.length;
    }
  } catch (err) {
    error.value = getApiErrorMessage(err, "榜单加载失败");
    movies.value = [];
    actors.value = [];
  } finally {
    loading.value = false;
  }
}

function selectTab(next: JavRankingKind) {
  if (kind.value === next) return;
  kind.value = next;
  page.value = 1;
  totalMovies.value = 0;
  void load();
}

function goPage(next: number) {
  if (next === page.value) return;
  page.value = next;
  void load();
}

watch(page, () => {
  window.scrollTo({ top: 0, behavior: "smooth" });
});

onMounted(load);
</script>

<template>
  <div>
    <div class="jav-rank-head">
      排行榜
      <span class="jav-rank-count">{{ count }}</span>
    </div>

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
          :rank="kind === 'top250' ? (page - 1) * PER_PAGE.top250 + index + 1 : 0"
          :subscribed="subscribedKeys.has(movieKey(movie))"
          @open="emit('open', movie)"
          @subscribe="emit('subscribe', movie)"
        />
      </div>
      <div v-else class="jav-empty">
        <div class="jav-empty__icon">⭐</div>
        <div>暂无数据。</div>
      </div>

      <div v-if="!loading && totalPages > 1" class="jav-pagination">
        <AppPagination :page="page" :total-pages="totalPages" @update:page="goPage" />
      </div>
    </template>
  </div>
</template>
