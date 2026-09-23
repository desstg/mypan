<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppInput from "@/components/base/AppInput.vue";
import AppPagination from "@/components/base/AppPagination.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchTGDiscover,
  fetchTGGenres,
  searchTGTMDB,
  tgPosterURL,
  type TGDiscoverQuery,
} from "@/api/tgSubscribe";
import { toast } from "@/composables/useToast";
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

async function load(reset = false) {
  if (reset) page.value = 1;
  loading.value = true;
  try {
    if (searching.value) {
      results.value = await searchTGTMDB({ q: props.keyword.trim(), type: props.mediaType });
      totalPages.value = 1;
      return;
    }
    const query: TGDiscoverQuery = { type: props.mediaType, page: page.value };
    if (country.value) query.country = country.value;
    if (genre.value) query.genres = genre.value;
    if (year.value) query.year = year.value;
    const payload = await fetchTGDiscover(query);
    results.value = payload.results ?? [];
    totalPages.value = payload.total_pages || 1;
  } catch (error) {
    results.value = [];
    toast.error(getApiErrorMessage(error, "加载影片列表失败"));
  } finally {
    loading.value = false;
  }
}

const gridRef = ref<HTMLElement | null>(null);

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

    <div v-if="results.length" ref="gridRef" class="tg-grid">
      <button
        v-for="item in results"
        :key="item.id"
        type="button"
        class="tg-card"
        @click="emit('open', item)"
      >
        <div class="tg-card__poster">
          <img
            v-if="item.poster_path"
            class="tg-card__image"
            :src="tgPosterURL(item.poster_path, posterSize)"
            :alt="titleOf(item)"
            loading="lazy"
          />
          <div v-else class="tg-card__image tg-card__image--empty">🎬</div>

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
      :description="searching ? '换个关键词试试。' : '确认已在「媒体整理 → TMDB 设置」里填好 API Key。'"
    />

    <div v-else class="tg-wall__footer">加载中…</div>

    <!-- 搜索模式下 totalPages 恒为 1，分页器自己就不渲染，不用外面再判一次。 -->
    <AppPagination :page="page" :total-pages="totalPages" @update:page="goPage" />
  </div>
</template>
