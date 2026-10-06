<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import AppInput from "@/components/base/AppInput.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import AppPagination from "@/components/base/AppPagination.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchTGDiscoverCached,
  fetchTGGenres,
  searchTGTMDB,
  tgPosterURL,
  type TGDiscoverQuery,
} from "@/api/tgSubscribe";
import { toast } from "@/composables/useToast";
import { useGridPageSize } from "@/composables/useGridColumns";
import type { TGMediaType, TGTMDBSearchResult } from "@/types/tg-subscribe";

const props = defineProps<{
  mediaType: TGMediaType;
  /** 搜索词。非空时展示搜索结果，为空时展示 TMDB 发现列表。 */
  keyword: string;
  /** 已订阅的 TMDB 标识（`type:id`），用于把「+ 订阅」换成 ✓。 */
  subscribedKeys: Set<string>;
}>();

const emit = defineEmits<{ open: [TGTMDBSearchResult]; subscribe: [TGTMDBSearchResult] }>();

const loading = ref(false);
const results = ref<TGTMDBSearchResult[]>([]);
const page = ref(1);
const totalPages = ref(1);

const country = ref("");
const genre = ref("");
const year = ref("");
const posterSize = ref<"w300" | "original">("w300");

const genres = ref<{ value: string; label: string }[]>([]);

/**
 * 每页几行。列数由 CSS 的 auto-fill 决定（见 tg-subscribe.css），这里只定行数。
 *
 * 桌面宽度下是 7 列 × 5 行 = 35 部 —— 也就是「每页 40 部左右」。
 * 换列数时**不用改这里**：每页条数按实测列数现算，永远是整数行。
 */
const PAGE_ROWS = 5;

const COUNTRY_OPTIONS = [
  { value: "", label: "全部国家" },
  { value: "CN", label: "中国大陆" },
  { value: "HK", label: "中国香港" },
  { value: "TW", label: "中国台湾" },
  { value: "US", label: "美国" },
  { value: "JP", label: "日本" },
  { value: "KR", label: "韩国" },
  { value: "GB", label: "英国" },
  { value: "FR", label: "法国" },
];

const genreOptions = computed(() => [{ value: "", label: "全部类型" }, ...genres.value]);

/** 搜索模式下筛选条件无意义（TMDB 搜索接口不看这些），所以整条筛选栏隐藏。 */
const searching = computed(() => props.keyword.trim().length > 0);

const gridRef = ref<HTMLElement | null>(null);
const { pageSize, measure } = useGridPageSize(gridRef, PAGE_ROWS);

/**
 * 本页实际渲染的条目。
 *
 * 发现列表要在客户端切片：TMDB 每页固定 20 条，凑不出「整数行」的 35 部，
 * 所以从上游拉相邻的两页拼成 40 条，再切出前 35 条。多出来的 5 条丢掉
 * （下一张页面里没有它们 —— 但翻页本来就是看个大概，不值得为 5 条去补位）。
 *
 * **搜索模式不切片**：TMDB 的搜索接口没有分页，结果固定是 10 条，
 * 切片只会让某些关键词少显示几部。
 */
const visibleResults = computed(() => {
  if (searching.value) return results.value;
  return results.value.slice(0, pageSize.value);
});

function titleOf(item: TGTMDBSearchResult) {
  return item.title || item.name || item.original_title || item.original_name || "（无标题）";
}

function yearOf(item: TGTMDBSearchResult) {
  const raw = item.release_date || item.first_air_date || "";
  return raw ? raw.slice(0, 4) : "";
}

function keyOf(item: TGTMDBSearchResult) {
  return `${props.mediaType}:${item.id}`;
}

async function loadGenres() {
  try {
    const payload = await fetchTGGenres(props.mediaType);
    genres.value = (payload.genres ?? []).map((g) => ({ value: String(g.id), label: g.name }));
  } catch {
    // 类型下拉拉不到不影响主流程（多半是没配 TMDB Key，主列表会给出更明确的报错）。
  }
}

/**
 * 本地页码 → 上游页码。
 *
 * 本地一页 = 上游两页（见 visibleResults）：本地第 1 页取上游 1、2，
 * 第 2 页取上游 3、4。上游每页固定 20 条，所以 2 × 20 = 40 条足够切出 35 部。
 */
function upstreamPages(local: number): number[] {
  const base = (local - 1) * 2 + 1;
  return [base, base + 1];
}

/**
 * 本地筛选条件 → 上游查询参数（不含 page）。
 *
 * 抽出来是为了让它同时当**缓存键**：`load()` 与「本页要不要重取」两处
 * 必须用完全一致的条件，各写一份迟早会漂。
 */
function filterQuery(): TGDiscoverQuery {
  const base: TGDiscoverQuery = { type: props.mediaType };
  if (country.value) base.country = country.value;
  if (genre.value) base.genres = genre.value;
  if (year.value) base.year = year.value;
  return base;
}

/** 同一页同一筛选条件下已经拉过的上游页数：拉够了就不必再打一次（哪怕是缓存的）。 */
const loadedPages = ref(0);

async function load(reset = false) {
  if (reset) page.value = 1;
  loading.value = true;
  try {
    if (searching.value) {
      results.value = await searchTGTMDB({ q: props.keyword.trim(), type: props.mediaType });
      totalPages.value = 1;
      loadedPages.value = 0;
      return;
    }
    const base = filterQuery();

    const pages = upstreamPages(page.value);
    const payloads = await Promise.all(
      pages.map((p) => fetchTGDiscoverCached({ ...base, page: p }).catch(() => null)),
    );
    // 上游最后一页之后会 500（TMDB 的 page 上限是 500），所以这里按「哪几页回来了」
    // 拼，而不是断言两页都在。第一页拿不到才算真失败。
    if (!payloads[0]) throw new Error("empty payload");
    results.value = payloads.flatMap((p) => p?.results ?? []);
    // 上游 total_pages 是 20 条一页的，本地一页顶它两页。
    totalPages.value = Math.max(1, Math.ceil((payloads[0].total_pages || 1) / 2));
    loadedPages.value = payloads.filter(Boolean).length;
    await nextTick();
    measure();
  } catch (error) {
    results.value = [];
    toast.error(getApiErrorMessage(error, "加载影片列表失败"));
  } finally {
    loading.value = false;
  }
}

/**
 * 列数变了（侧栏收起 / 展开、窗口缩放）时只做一件事：重新切片。
 *
 * **不重新请求** —— 数据还是那两页数据，变的只是「一页显示几张」。
 * 列数一变每页条数就从 35 变 40，不重新切片就会按旧条数渲染，
 * 40 张卡片排进 7 列 → 最后一排空两格。
 *
 * 拉回来的上游页数不够新条数时（窄屏切宽屏，35 → 40）才补一次请求；
 * 补完也不会回头把已经拉过的两页再打一遍。
 */
async function refit() {
  if (searching.value || !results.value.length) return;
  const wanted = upstreamPages(page.value);
  if (loadedPages.value >= wanted.length) return;
  loading.value = true;
  try {
    const base = filterQuery();
    const payloads = await Promise.all(
      wanted.map((p) => fetchTGDiscoverCached({ ...base, page: p }).catch(() => null)),
    );
    if (!payloads[0]) return;
    results.value = payloads.flatMap((p) => p?.results ?? []);
    loadedPages.value = payloads.filter(Boolean).length;
  } catch {
    // 补数据失败就维持现状：屏幕上还留着上一次的结果，比清空好。
  } finally {
    loading.value = false;
  }
}

watch(pageSize, () => {
  void refit();
});

// 分页器在列表**下方**，翻页后不把视口带回顶部的话，用户看到的是新一页的末尾，
// 会以为页码没生效。越界钳制交给 AppPagination，这里只管跳。
async function goPage(next: number) {
  page.value = next;
  await load();
  gridRef.value?.scrollIntoView({ behavior: "smooth", block: "start" });
}

watch(
  () => props.mediaType,
  () => {
    genre.value = "";
    void loadGenres();
    void load(true);
  },
);
// 搜索词由父级防抖后传入，这里不用再防抖。
watch(
  () => props.keyword,
  () => void load(true),
);
watch([country, genre, year], () => void load(true));
onMounted(async () => {
  await Promise.all([loadGenres(), load(true)]);
});

defineExpose({ load });
</script>

<template>
  <div class="tg-wall">
    <!-- 筛选裸放在页面上，不套 SettingsCard：这是一排「选完就看着海报墙」的即时
         筛选器，不是需要保存的设置，给它一张白底卡片 + 左侧色条会把一个纯粹的操作行
         抬成和下面海报墙同等重量的一个区块。 -->
    <div v-if="!searching" class="tg-filters">
      <div class="tg-filters__field">
        <label class="tg-filters__label">国家 / 地区</label>
        <AppSelect v-model="country" :options="COUNTRY_OPTIONS" />
      </div>
      <div class="tg-filters__field">
        <label class="tg-filters__label">类型</label>
        <AppSelect v-model="genre" :options="genreOptions" />
      </div>
      <div class="tg-filters__field">
        <label class="tg-filters__label">年份</label>
        <AppInput v-model="year" placeholder="例如 2024" />
      </div>
      <div class="tg-filters__field">
        <label class="tg-filters__label">海报尺寸</label>
        <SettingsSegment
          v-model="posterSize"
          label="海报尺寸"
          :options="[
            { value: 'w300', label: '标清' },
            { value: 'original', label: '原图' },
          ]"
        />
      </div>
    </div>

    <SettingsCard v-else :title="`搜索「${keyword.trim()}」`" accent="var(--brand)">
      <template #head-aside>
        <span>共 {{ results.length }} 条结果。点卡片看详情，右上角按钮直接订阅。</span>
      </template>
    </SettingsCard>

    <div v-if="visibleResults.length" ref="gridRef" class="tg-grid">
      <button
        v-for="item in visibleResults"
        :key="item.id"
        type="button"
        class="tg-card"
        @click="emit('open', item)"
      >
        <div class="tg-card__poster">
          <MediaImage
            class="tg-card__image"
            :src="tgPosterURL(item.poster_path, posterSize)"
            :alt="titleOf(item)"
          />

          <span class="tg-card__type">{{ mediaType === "movie" ? "电影" : "剧集" }}</span>

          <!-- 已订阅显示常驻 ✓；未订阅 hover 才浮出「+」。 -->
          <span v-if="subscribedKeys.has(keyOf(item))" class="tg-card__subscribed" title="已订阅">✓</span>
          <button
            v-else
            type="button"
            class="tg-card__action"
            title="订阅"
            @click.stop="emit('subscribe', item)"
          >
            +
          </button>
        </div>
        <div class="tg-card__meta">
          <span class="tg-card__title" :title="titleOf(item)">{{ titleOf(item) }}</span>
          <span class="tg-card__sub">
            <span v-if="yearOf(item)">{{ yearOf(item) }}</span>
            <span v-if="item.vote_average" class="tg-card__score">★ {{ item.vote_average.toFixed(1) }}</span>
          </span>
        </div>
      </button>
    </div>

    <AdminEmptyState
      v-else-if="!loading"
      icon="🍿"
      :title="searching ? '没有搜到匹配的影片' : '没有拿到影片列表'"
      :description="
        searching
          ? '换个关键词试试。'
          : '「任务管理 → 目录整理 → 整理设置 → TMDB 设置」里填好 API Key 并且已在「系统设置 → 其他设置 → 网络代理」开启代理'
      "
    />

    <div v-else class="tg-wall__footer">加载中…</div>

    <!-- 搜索模式下 totalPages 恒为 1，分页器自己就不渲染，不用外面再判一次。 -->
    <AppPagination :page="page" :total-pages="totalPages" @update:page="goPage" />
  </div>
</template>

