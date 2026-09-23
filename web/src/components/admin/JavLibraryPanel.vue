<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import AppSelect from "@/components/base/AppSelect.vue";
import JavMovieCard from "@/components/admin/JavMovieCard.vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchJavListMovies, fetchJavLocalMovies, searchJav } from "@/api/jav";
import type { JavMovieCard as JavCard, JavSearchResult } from "@/types/jav";
import { JAV_SEARCH_TYPES, JAV_TYPE_FILTERS } from "@/types/jav";
import "@/styles/jav.css";

/**
 * 影库页 —— 一份组件承担两种形态，与源码的 library.html / search.html 对应：
 *
 *   无查询 = 本地影库：筛选 / 年份 / 排序 / 标签。
 *   有查询 = 在线搜索结果：打的是 JAVDB 全站，顶部给一条「搜的什么、找到多少」。
 *
 * 两页合成一份是刻意的：源码里那两个模板除了头部提示条以外几乎逐行相同，
 * 分成两个组件意味着以后每次改卡片或改筛选都要改两处。
 */
const props = defineProps<{
  /** 搜索关键字。空串表示展示本地影库。 */
  keyword: string;
  /** 搜索类型（全部 / 番号 / 演员 …）。 */
  searchType: string;
  /** 标签筛选。非空时只列带这个标签的影片（源码 /library?tag= 的语义）。 */
  tag: string;
  subscribedKeys: Set<string>;
  /**
   * 非空时这一页展示的是**某个清单里的影片**（第三态：本地影库 / 搜索 / 清单）。
   *
   * 必须有真实的清单 **id**：清单成员只能靠抓官网清单页拿，而那个页面是按 id 的
   * 路由（`/lists/{id}`）。没有 id 就无从抓起 —— 所以这里不再是「可选的身份」，
   * 而是「切不切到清单态」的开关。
   */
  listTarget?: { id: string; name: string } | null;
}>();

const emit = defineEmits<{
  open: [movie: JavCard];
  subscribe: [movie: JavCard];
  /** 点那颗心（演员搜索页）：把反查出来的演员交给页面去建订阅。 */
  subscribeActor: [actor: { id: string; name: string }];
  /** 点「订阅清单」：把清单交给页面去建订阅。 */
  subscribeList: [list: { id: string; name: string }];
  clearSearch: [];
  clearTag: [];
  /** 点清单提示条上的「返回影库」：退出清单态。 */
  clearList: [];
}>();

const loading = ref(false);
const error = ref("");
const items = ref<JavCard[]>([]);
const total = ref(0);

// —— 本地影库的筛选状态 ——
const page = ref(1);
const pageSize = 60;
const typeFilter = ref("");
const yearFilter = ref("");
const sortKey = ref("release_date");
const sortDir = ref<"asc" | "desc">("desc");

// —— 搜索状态 ——
const searching = computed(() => props.keyword.trim() !== "");
const searchSource = ref<JavSearchResult["source"]>("upstream");
const searchNotice = ref("");
/** 按演员搜时，服务端反查出来的演员（搜索结果本身不含演员信息）。 */
const searchActorId = ref("");
const searchActorName = ref("");
/** 搜索结果的分页与本地影库分开 —— 两者的页大小与语义都不同。 */
const searchPage = ref(1);
/** 清单态的页码也单独一份：每页 40 部（官网清单页的固定页大小）。 */
const listPage = ref(1);
// 搜索结果的页大小由服务端定（internal/jav 的 searchPerPage = 24），
// 前端只说「要第几页」。

const loadingMore = ref(false);

/**
 * 还有没有下一页。
 *
 * 用「已加载条数 < 总数」判断，而不是「当前页 < 总页数」—— 两者本该等价，
 * 但前者在最后一页恰好装满时也不会多请求一次空页。
 */
const hasMore = computed(() => items.value.length < total.value);

/** 年份下拉。从今年往前数 26 年，与源码的 range(2026, 1999, -1) 同宽。 */
const yearOptions = computed(() => {
  const now = new Date().getFullYear();
  const out = [{ value: "", label: "年份 全部" }];
  for (let y = now; y > now - 27; y--) {
    out.push({ value: String(y), label: String(y) });
  }
  return out;
});

const typeOptions = JAV_TYPE_FILTERS.map((t) => ({ value: t.value, label: t.label }));

/** 提示条里那个括号里的类型名。「全部」是默认值，标出来只是噪音。 */
const searchTypeLabel = computed(() => {
  const found = JAV_SEARCH_TYPES.find((t) => t.value === props.searchType);
  return found && found.value !== "all" ? found.label : "";
});

/** 排序 chips。方向切换的语义照源码：新选中的默认倒序，再点一下翻转。 */
const SORTS = [
  { key: "release_date", label: "上映日期", hasDir: true },
  { key: "score", label: "评分", hasDir: true },
  { key: "relevance", label: "相关度", hasDir: false },
];

/**
 * append = 追加下一页（滚动到底自动加载），而不是替换整个列表。
 *
 * 追加时**不能**把 `loading` 置真：模板里 `v-else-if="loading"` 会把整片网格
 * 换成「加载中…」，用户每滚一屏就看到列表闪一下没了。
 */
async function loadLocal(append = false) {
  if (append) loadingMore.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const res = await fetchJavLocalMovies({
      type: typeFilter.value,
      year: yearFilter.value,
      // 标签筛选只在本地影库这一态生效：搜索态打的是上游，
      // 上游不吃我们的标签体系，带过去只会得到一个空结果。
      tag: props.tag,
      sort: sortKey.value,
      dir: sortDir.value,
      page: page.value,
      page_size: pageSize,
    });
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
  } catch (err) {
    // 追加失败就保持已有内容 —— 滚到一半网络抖动不该把看过的一屏清空。
    if (!append) {
      error.value = getApiErrorMessage(err, "影库加载失败");
      items.value = [];
      total.value = 0;
    }
  } finally {
    if (append) loadingMore.value = false;
    else loading.value = false;
  }
}

async function loadSearch(append = false) {
  if (append) loadingMore.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const res = await searchJav({
      keyword: props.keyword,
      type: props.searchType,
      filter: typeFilter.value,
      year: yearFilter.value,
      sort: sortKey.value,
      dir: sortDir.value,
      page: searchPage.value,
    });
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
    searchSource.value = res.source;
    searchNotice.value = res.notice ?? "";
    // 只在「按演员搜」时才可能有值，其余搜索类型服务端返回空串。
    searchActorId.value = res.actor_id ?? "";
    searchActorName.value = res.actor_name ?? "";
  } catch (err) {
    if (!append) {
      error.value = getApiErrorMessage(err, "搜索失败");
      items.value = [];
      total.value = 0;
    }
  } finally {
    if (append) loadingMore.value = false;
    else loading.value = false;
  }
}

/**
 * 清单态：从官网清单页抓一页。
 *
 * 与另两态的区别是它**不受筛选/排序影响** —— 那些参数是本地库与上游搜索吃的东西，
 * 清单页给什么就是什么（顺序也由官网决定）。所以清单态下筛选条是藏起来的。
 */
async function loadList(append = false) {
  const id = props.listTarget?.id;
  if (!id) return;
  if (append) loadingMore.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const res = await fetchJavListMovies(id, listPage.value);
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
  } catch (err) {
    // 追加失败保持已有内容：滚到一半一次网页请求失败，不该把看过的一屏清空。
    if (!append) {
      error.value = getApiErrorMessage(err, "清单加载失败");
      items.value = [];
      total.value = 0;
    }
  } finally {
    if (append) loadingMore.value = false;
    else loading.value = false;
  }
}

function reload() {
  if (listMode.value) return loadList();
  if (searching.value) return loadSearch();
  return loadLocal();
}

/**
 * 滚到底时加载下一页。
 *
 * 上游搜索一次能翻回几百条（后端已聚合），但一次全画出来会卡；
 * 这里按页追加，用户滚到哪儿看到哪儿。
 */
async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return;
  if (listMode.value) {
    listPage.value += 1;
    await loadList(true);
  } else if (searching.value) {
    searchPage.value += 1;
    await loadSearch(true);
  } else {
    page.value += 1;
    await loadLocal(true);
  }
}

// 哨兵进视口就加载下一页。rootMargin 留 300px，让下一屏在用户滚到之前就备好，
// 滚起来不会一顿一顿的。
const sentinel = ref<HTMLElement | null>(null);
let observer: IntersectionObserver | null = null;

onMounted(() => {
  if (typeof IntersectionObserver === "undefined" || !sentinel.value) return;
  observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) void loadMore();
    },
    { rootMargin: "300px" },
  );
  observer.observe(sentinel.value);
});

onUnmounted(() => {
  observer?.disconnect();
  observer = null;
});

function pickSort(key: string, hasDir: boolean) {
  if (sortKey.value === key && hasDir) {
    // 再点已选中的那一项：翻转方向。相关度没有方向，保持原样。
    sortDir.value = sortDir.value === "desc" ? "asc" : "desc";
  } else {
    sortKey.value = key;
    sortDir.value = "desc";
  }
  page.value = 1;
  void reload();
}

function onFilterChange() {
  page.value = 1;
  searchPage.value = 1;
  void reload();
}

/**
 * 关键字 / 类型 / 标签 / 清单变了就回到第一页重查。
 *
 * 清单那一项用 `?.id` 做键：页面传的是同一个对象的引用时不会白跑一趟，
 * 而换了清单（id 变了）必须重新抓。
 */
watch(
  () => [props.keyword, props.searchType, props.tag, props.listTarget?.id],
  () => {
    searchPage.value = 1;
    page.value = 1;
    listPage.value = 1;
    void reload();
  },
);

function movieKey(movie: JavCard) {
  return `movie:${movie.id.toLowerCase()}`;
}

/** 演员订阅卡的键与已订阅状态，与榜单页那颗演员订阅按钮同一套。 */
const actorSubscribed = computed(
  () =>
    !!searchActorId.value &&
    props.subscribedKeys.has(`actor:${searchActorId.value.toLowerCase()}`),
);

/** 清单态：这一页展示的是某个清单的成员。 */
const listMode = computed(() => !!props.listTarget?.id);
const listSubscribed = computed(() => {
  const id = props.listTarget?.id;
  return !!id && props.subscribedKeys.has(`list:${id.toLowerCase()}`);
});

onMounted(reload);

defineExpose({ reload });
</script>

<template>
  <div>
    <!-- 搜索结果提示条。用户点名要的：搜的什么关键字、符合的有多少条。
         对应源码 search.html 的 search-head。 -->
    <div v-if="searching" class="jav-search-notice">
      <span>🔍 搜索</span>
      <span class="jav-search-notice__query">「{{ keyword }}」</span>
      <span v-if="searchTypeLabel">（{{ searchTypeLabel }}）</span>
      <span>· 找到</span>
      <span class="jav-search-notice__query">{{ total }}</span>
      <span>条符合的内容</span>
      <!-- 「已显示」随滚动增长，让用户知道还有多少没看。
           总数是服务端聚合出来的真值（上游每页只给 50 条，聚合修好之前
           这个数会一直卡在 50 —— 那是后端翻页提前终止的老 bug）。 -->
      <span v-if="items.length < total" class="jav-search-notice__shown">
        · 已显示 {{ items.length }}
      </span>
      <!-- 上游不可用回落到本地时要说清楚，否则用户会以为「站上就这么多」。 -->
      <span v-if="searchNotice" class="jav-search-notice__fallback">· {{ searchNotice }}</span>
      <button type="button" class="jav-sort-chip" style="margin-left: auto" @click="emit('clearSearch')">
        清除
      </button>
    </div>

    <!-- 清单提示条：这一页是某份清单的成员。清单页的顺序由官网决定，
         我们的筛选与排序都掺和不进去，所以那两条在这里是藏起来的。 -->
    <div v-if="listTarget" class="jav-search-notice">
      <span>📋 清单</span>
      <span class="jav-search-notice__query">「{{ listTarget.name }}」</span>
      <span>· 共</span>
      <span class="jav-search-notice__query">{{ total }}</span>
      <span>部影片</span>
      <span v-if="items.length < total" class="jav-search-notice__shown">
        · 已显示 {{ items.length }}
      </span>
      <!-- 「订阅清单」。与演员搜索页那颗心同一套 class 与交互：
           已订阅=取消，未订阅=切到订阅页并打开创建弹窗（清单名已填好）。
           放在这一条上而不是筛选条里 —— 筛选条在清单态是藏起来的。 -->
      <button
        type="button"
        class="jd-btn jav-filters__actor-sub"
        style="margin-left: auto"
        :class="{ 'jd-btn--on': listSubscribed }"
        :title="listSubscribed ? '取消订阅' : `订阅清单「${listTarget.name}」`"
        @click="emit('subscribeList', { id: listTarget.id, name: listTarget.name })"
      >
        <i class="fas fa-heart" /> {{ listSubscribed ? "已订阅" : "订阅清单" }}
      </button>
      <button type="button" class="jav-sort-chip" @click="emit('clearList')">返回影库</button>
    </div>

    <!-- 标签筛选条。只在本地影库这一态出现，与源码 library.html 的 tag-filter-bar 一致。 -->
    <div v-if="tag && !searching" class="jav-tag-bar">
      <span class="jav-tag-bar__label">🏷️ 标签：<b>{{ tag }}</b></span>
      <button type="button" class="jav-tag-bar__clear" title="清除标签筛选" @click="emit('clearTag')">
        ✕ 清除
      </button>
    </div>

    <!-- 筛选与排序条。搜索结果也走本地二次过滤（番号的精确匹配、类别、年份、
         排序都在本地做），所以这一条在搜索时同样可用 —— 与源码 search.html 一致。
         **清单态除外**：那些参数是本地库与上游搜索吃的东西，清单页给什么就是什么。 -->
    <div v-if="!listTarget" class="jav-filters">
      <AppSelect
        v-model="typeFilter"
        :options="typeOptions"
        style="width: 130px"
        @update:model-value="onFilterChange"
      />
      <AppSelect
        v-model="yearFilter"
        :options="yearOptions"
        style="width: 130px"
        @update:model-value="onFilterChange"
      />
      <div class="jav-sort-chips">
        <button
          v-for="s in SORTS"
          :key="s.key"
          type="button"
          class="jav-sort-chip"
          :class="{ 'jav-sort-chip--active': sortKey === s.key }"
          @click="pickSort(s.key, s.hasDir)"
        >
          {{ s.label }}
          <span v-if="s.hasDir && sortKey === s.key">{{ sortDir === "asc" ? "↑" : "↓" }}</span>
        </button>
      </div>

      <!-- 演员搜索页那颗心。只在「按演员搜」且服务端反查到了演员 id 时出现 ——
           搜索结果里没有演员信息，那个 id 是从详情里反查出来的（见后端
           resolveSearchActor）。样式与详情页那颗心同一套：.jd-btn + --on。 -->
      <button
        v-if="searchActorId"
        type="button"
        class="jd-btn jav-filters__actor-sub"
        :class="{ 'jd-btn--on': actorSubscribed }"
        :title="actorSubscribed ? '取消订阅' : `订阅演员「${searchActorName || keyword}」`"
        @click="emit('subscribeActor', { id: searchActorId, name: searchActorName || keyword })"
      >
        <i class="fas fa-heart" /> {{ actorSubscribed ? "已订阅" : "订阅" }}
      </button>

    </div>

    <div v-if="error" class="jav-empty">
      <div class="jav-empty__icon">⚠️</div>
      <div>{{ error }}</div>
    </div>

    <div v-else-if="loading" class="jav-empty">加载中…</div>

    <div v-else-if="items.length" class="jav-grid">
      <JavMovieCard
        v-for="movie in items"
        :key="movie.id"
        :movie="movie"
        :subscribed="subscribedKeys.has(movieKey(movie))"
        @open="emit('open', movie)"
        @subscribe="emit('subscribe', movie)"
      />
    </div>

    <div v-else class="jav-empty">
      <div class="jav-empty__icon">{{ searching ? "🔍" : "🗂️" }}</div>
      <div v-if="searching">没有找到「{{ keyword }}」相关内容。</div>
      <div v-else>影库为空，去榜单或搜索里找几部入库吧。</div>
    </div>

    <!-- 滚动到底自动续上。哨兵常驻 DOM（不是 v-if）—— 它一旦被移除，
         挂在它上面的 IntersectionObserver 就再也收不到回调了。 -->
    <div ref="sentinel" class="jav-loadmore">
      <span v-if="loadingMore">加载中…</span>
      <span v-else-if="hasMore">往下滚，还有 {{ total - items.length }} 条</span>
      <span v-else-if="items.length">已全部加载（{{ total }} 条）</span>
    </div>
  </div>
</template>
