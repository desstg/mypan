<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import AppSelect from "@/components/base/AppSelect.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import JavMovieCard from "@/components/admin/JavMovieCard.vue";
import { useFillViewport } from "@/composables/useFillViewport";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavEntityMovies,
  fetchJavLocalMovies,
  javImageURL,
  searchJav,
  searchJavEntities,
} from "@/api/jav";
import type {
  JavEntity,
  JavEntityKind,
  JavMovieCard as JavCard,
  JavSearchResult,
} from "@/types/jav";
import { JAV_SEARCH_TYPES, JAV_TYPE_FILTERS } from "@/types/jav";
import "@/styles/jav.css";

/**
 * 影库页 —— 一份组件承担**四**种形态，与源码的 library.html / search.html 对应：
 *
 *   无查询           = 本地影库：筛选 / 年份 / 排序 / 标签。
 *   有查询 + 搜影片   = 在线搜索结果：打的是 JAVDB 全站，顶部给一条「搜的什么、找到多少」。
 *   有查询 + 搜实体   = 演员 / 系列 / 片商 / 导演 / 清单的**候选列表**，点一个进去看作品。
 *   点进某个实体     = 那个实体的作品（抓官网 HTML，一页 40 部）。
 *
 * 「搜实体」这一档是 2026-10-26 补的。改造前搜索框那六个档位全都打向「搜影片」，
 * 而上游对电影搜索不看实体类型（见 internal/jav/entities.go 顶部那段）——
 * 于是选「导演」和选「全部」出来的是一模一样的一批片。
 *
 * 四态合成一份是刻意的：它们除了头部提示条以外几乎逐行相同，
 * 分成几个组件意味着以后每次改卡片或改筛选都要改好几处。
 */
const props = defineProps<{
  /** 搜索关键字。空串表示展示本地影库。 */
  keyword: string;
  /** 搜索类型（全部 / 番号 / 演员 / 清单 …）。 */
  searchType: string;
  /** 标签筛选。非空时只列带这个标签的影片（源码 /library?tag= 的语义）。 */
  tag: string;
  subscribedKeys: Set<string>;
  /**
   * 非空时这一页展示的是**某个清单里的影片**。
   *
   * 必须有真实的清单 **id**：清单成员只能靠抓官网清单页拿，而那个页面是按 id 的
   * 路由（`/lists/{id}`）。没有 id 就无从抓起 —— 所以这里不再是「可选的身份」，
   * 而是「切不切到清单态」的开关。
   *
   * 它与下面 entityTarget 走的是**同一套**展示（`activeEntity` 把两者归一），
   * 区别只是来源：这一条来自详情抽屉的「关联清单」，那条来自搜索结果里点了一张卡。
   */
  listTarget?: { id: string; name: string } | null;
}>();

const emit = defineEmits<{
  open: [movie: JavCard];
  subscribe: [movie: JavCard];
  /** 点那颗心（演员搜索页）：把演员交给页面去建订阅。 */
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
/** 实体的作品页页码。每页 40 部（官网作品页的固定页大小）。 */
const entityPage = ref(1);
// 搜索结果的页大小由服务端定（internal/jav 的 searchPerPage = 24），
// 前端只说「要第几页」。

const loadingMore = ref(false);

/**
 * 请求代次。**每发起一次加载 +1，回来时对不上号的结果直接丢弃。**
 *
 * 这一条是 2026-10-26 实测出来的：搜索接口要往上游翻 10 页聚合，
 * 一次 1～6 秒，而输入框 400ms 就去抖一次 —— 于是「SS」「SSIS」「SSIS-0」
 * 好几个请求同时在飞，**谁先回来谁就先画**。
 *
 * 真机日志（按真人速度逐字输入 SSIS-001）：
 *   SSIS-001 的请求 1.0 秒回来 → 画出 1 张卡
 *   更早那次「SS」的请求 5.2 秒才回来 → 用它的空结果把那张卡盖掉
 * 用户看到的就是「搜出来结果，显示一下，又不显示了」。
 *
 * 加了这个序号以后，只有**最后一次**发起的那次结果能落到界面上。
 */
let reqSeq = 0;

/**
 * 当前那次搜索的取消器。发起新的一次就把上一次**真的掐掉**。
 *
 * 光靠上面的序号也能不画错数据，但请求还在那儿跑：一次搜索要往上翻 10 页、
 * 1～6 秒，逐字输入时能有五六个同时在打上游 —— 既拖慢最新那一次（上游限流
 * 是共享的），也白白多打几十次外站。掐掉之后最新那次能立刻拿到通道。
 */
let searchAbort: AbortController | null = null;

/**
 * 番号这一档的补充提示（其余类型恒为空串）。
 *
 * 上游对 type=number 是「归一化后**精确**比对」（见 internal/jav/catalog.go），
 * 所以「SSIS」「SSIS-0」这种没输完的必然 0 条 —— 界面上不能只甩一句
 * 「没有找到」，那看着像站上没有这部片。
 */
const numberHint = ref("");
/** 一个字都还没带数字：不用去打上游了，结果必然是空。 */
const NUMBER_HINT_NEED_DIGIT = "番号里得有数字，完整形如 SSIS-001";
/** 带数字但结尾那串太短：多半还没输完。 */
const NUMBER_HINT_SHORT = "这个番号可能还没输完（完整形如 SSIS-001）";

/** 结尾连续数字的位数：`SSIS-001` → 3。判「是不是还没输完」用。 */
function trailingDigitCount(v: string) {
  const m = /\d+$/.exec(v.trim());
  return m ? m[0].length : 0;
}

// —— 按实体搜（系列 / 片商 / 导演 / 清单）——
//
// 这四档与「搜影片」是两条上游路：那一条 `type=movie` 返回影片，这一条
// `type=<实体>` 返回**实体候选**，点一个进去才看作品。改造前它们全都打向
// 「搜影片」，而 movie_type 是影片的内容分类、上游对不认识的值静默按 all 处理 ——
// 于是选「导演」和选「全部」出来的是同一批片。
//
// **演员不在这里**：按演员名搜影片本来就是上游支持的能力（电影的搜索会匹配演员），
// 而且那一档能筛选、排序、翻页，比「先选人再看作品」好用。把它挪到实体路
// 等于每搜一次都要多点一下，属于倒退。
const ENTITY_KINDS = ["series", "maker", "director", "lists"] as const;

/** 官网作品页的页大小：40 部。它决定了「还有没有下一页」怎么判。 */
const ENTITY_PAGE_SIZE = 40;

function isEntityKind(v: string): v is JavEntityKind {
  return (ENTITY_KINDS as readonly string[]).includes(v);
}

/** 当前搜索类型对应的实体档；不是实体档时为 null。 */
const entityKind = computed<JavEntityKind | null>(() =>
  isEntityKind(props.searchType) ? props.searchType : null,
);

/** 实体档的中文名，提示条里用。 */
const ENTITY_KIND_LABELS: Record<JavEntityKind, string> = {
  actor: "演员",
  series: "系列",
  maker: "片商",
  director: "导演",
  lists: "清单",
};

/**
 * 当前正在看的实体（点开了某个候选才非空）。
 *
 * 与 listTarget 是**同一件事的两个来源**：listTarget 来自详情抽屉的「关联清单」，
 * 这一个来自搜索结果里点中的卡片。归一成一个对象，下面只需要一套加载逻辑。
 */
const entityTarget = ref<{ kind: JavEntityKind; id: string; name: string } | null>(null);

const activeEntity = computed(() => {
  if (entityTarget.value) return entityTarget.value;
  const lt = props.listTarget;
  if (lt?.id) return { kind: "lists" as JavEntityKind, id: lt.id, name: lt.name };
  return null;
});

/**
 * 是不是「在搜实体候选」这一态。
 *
 * 必须连 `searching` 一起判：搜索类型下拉停在「清单」、但搜索框是空的时候，
 * 这一页该照常显示本地影库 —— 只看 entityKind 的话会拿一个空关键字去打上游，
 * 然后显示「没有找到叫「」的清单」，而用户什么都没搜。
 */
const entitySearchMode = computed(
  () => searching.value && !!entityKind.value && !activeEntity.value,
);
/** 是不是「在看某个实体的作品」这一态。 */
const entityMoviesMode = computed(() => !!activeEntity.value);

/** 实体候选（最多 20 个，上游的上限，没有分页）。 */
const entities = ref<JavEntity[]>([]);
/** 上一次抓的实体作品页有没有装满 —— 没装满就是最后一页。 */
const entityHasMore = ref(false);
/** 实体作品页有没有总数（只有清单有，其余四档恒为 0）。 */
const entityTotal = ref(0);

/**
 * 还有没有下一页。
 *
 * 用「已加载条数 < 总数」判断，而不是「当前页 < 总页数」—— 两者本该等价，
 * 但前者在最后一页恰好装满时也不会多请求一次空页。
 *
 * 实体的作品页是另一套：演员/系列/片商/导演那四档页面**不写总数**，
 * 只能靠「这一页有没有装满 40 部」判断，所以单独记一个标志。
 */
const hasMore = computed(() => {
  if (entityMoviesMode.value) return entityHasMore.value;
  if (entitySearchMode.value) return false;
  return items.value.length < total.value;
});

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
 * 停掉还在飞的那次搜索。切到本地影库 / 实体候选时也要叫一声，
 * 否则那个请求还在上游翻页（1～6 秒），白占限流额度。
 */
function abortSearch() {
  searchAbort?.abort();
  searchAbort = null;
}

/**
 * append = 追加下一页（滚动到底自动加载），而不是替换整个列表。
 *
 * 追加时**不能**把 `loading` 置真：模板里 `v-else-if="loading"` 会把整片网格
 * 换成「加载中…」，用户每滚一屏就看到列表闪一下没了。
 */
async function loadLocal(append = false) {
  if (!append) abortSearch();
  // append 不算「新的一次查询」：它是接着当前这一批往下续，
  // 把代次也翻掉的话上一页的续页会因为对不上号而被丢掉。
  const seq = append ? reqSeq : ++reqSeq;
  if (append) loadingMore.value = true;
  else {
    loading.value = true;
    // 换态时先把上一态的东西清干净。
    //
    // 不清的话就会看到「搜的提示条 + 影库的总数（17554 条）· 已显示 60」——
    // 那是两个态的数字混在一行里，用户会以为搜索已经出结果了。实测就是这么显示的。
    items.value = [];
    total.value = 0;
    searchNotice.value = "";
    searchActorId.value = "";
    searchActorName.value = "";
    numberHint.value = "";
  }
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
    if (seq !== reqSeq) return;
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
  } catch (err) {
    if (seq !== reqSeq) return;
    // 追加失败就保持已有内容 —— 滚到一半网络抖动不该把看过的一屏清空。
    if (!append) {
      error.value = getApiErrorMessage(err, "影库加载失败");
      items.value = [];
      total.value = 0;
    }
  } finally {
    if (seq === reqSeq) {
      if (append) loadingMore.value = false;
      else loading.value = false;
    }
  }
}

/**
 * 番号这一档「还没输完」的提示。
 *
 * 只在结果为空、且输入看着像半截番号时才给 —— 否则搜「三上悠亜」这种
 * 正常查不到的名字也会被附上一句「番号可能没输完」，那是误导。
 */
function numberHintFor(keyword: string): string {
  const kw = keyword.trim();
  if (!kw) return "";
  if (!/\d/.test(kw)) return NUMBER_HINT_NEED_DIGIT;
  if (trailingDigitCount(kw) < 3) return NUMBER_HINT_SHORT;
  return "";
}

/**
 * 这一趟**注定**为空、不必打上游的判据。
 *
 * 只收「一个字都不含数字」这一种：归一化后的番号一定带数字
 * （`SSIS001` 之于 `SSIS-001`），所以没数字的输入与任何番号都不可能相等 ——
 * 那是**确定**的空结果，不去打上游也一模一样。
 *
 * 反过来「数字不够长」**不敢**短路：番号里两位尾数的确实存在，
 * 拿它当「还没输完」直接判空会把真结果吞掉。那种只给提示、照常去查。
 */
function numberSkipHint(keyword: string): string {
  const kw = keyword.trim();
  if (!kw) return "";
  return /\d/.test(kw) ? "" : NUMBER_HINT_NEED_DIGIT;
}

async function loadSearch(append = false) {
  const seq = append ? reqSeq : ++reqSeq;
  // 换关键字了：把还在飞的上一次搜索掐掉（见 searchAbort 的说明）。
  // 追加下一页时不掐 —— 那是同一次查询的续页。
  if (!append) {
    searchAbort?.abort();
    searchAbort = new AbortController();
  }
  if (append) loadingMore.value = true;
  else {
    loading.value = true;
    // 同 loadLocal：换态时清掉上一态残留，别让两个态的数字混在一行。
    items.value = [];
    total.value = 0;
    searchNotice.value = "";
    numberHint.value = "";
  }
  error.value = "";
  const keyword = props.keyword;
  const type = props.searchType;
  // 番号没带数字就不打上游了：后端对 type=number 是归一化后**精确**比对，
  // 不带数字的输入与任何番号都不可能相等，必空 —— 而打这一趟要往上翻 10 页
  // 聚合、实测 1～6 秒。不去筛的话，逐字敲的过程中会有好几个必空的请求同时在飞
  // （这也正是「显示一下又不显示」的另一半原因：慢的回来把快的盖了）。
  // 这里就地给出结论，与后端一模一样，还省掉一串外站请求。
  const localEmptyHint = type === "number" ? numberSkipHint(keyword) : "";
  if (localEmptyHint) {
    items.value = [];
    total.value = 0;
    searchNotice.value = "";
    searchActorId.value = "";
    searchActorName.value = "";
    numberHint.value = localEmptyHint;
    loading.value = false;
    loadingMore.value = false;
    return;
  }
  try {
    const res = await searchJav(
      {
        keyword,
        type,
        filter: typeFilter.value,
        year: yearFilter.value,
        sort: sortKey.value,
        dir: sortDir.value,
        page: searchPage.value,
      },
      append ? undefined : searchAbort?.signal,
    );
    // 迟到的那一次（用户的后续输入已经发起了更新的请求）直接丢掉。
    if (seq !== reqSeq) return;
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
    searchSource.value = res.source;
    searchNotice.value = res.notice ?? "";
    // 只在「按演员搜」时才可能有值，其余搜索类型服务端返回空串。
    searchActorId.value = res.actor_id ?? "";
    searchActorName.value = res.actor_name ?? "";
    numberHint.value =
      type === "number" && items.value.length === 0 ? numberHintFor(keyword) : "";
  } catch (err) {
    if (seq !== reqSeq) return;
    if (!append) {
      error.value = getApiErrorMessage(err, "搜索失败");
      items.value = [];
      total.value = 0;
      numberHint.value = "";
    }
  } finally {
    if (seq === reqSeq) {
      if (append) loadingMore.value = false;
      else loading.value = false;
    }
  }
}

/**
 * 实体候选态：把演员 / 系列 / 片商 / 导演 / 清单的名字搜成一屏候选。
 *
 * 没有分页也没有「加载更多」：上游对这个接口的 limit 与 page 都不生效
 * （固定 20 条，翻第 2 页还是那 20 条，实测 2026-10-26）。所以这一态是
 * 「一屏候选、点一个进去」。
 */
async function loadEntities() {
  const kind = entityKind.value;
  if (!kind) return;
  abortSearch();
  const seq = ++reqSeq;
  loading.value = true;
  error.value = "";
  entities.value = [];
  try {
    const res = await searchJavEntities(props.keyword, kind);
    if (seq !== reqSeq) return;
    entities.value = res.items ?? [];
  } catch (err) {
    if (seq !== reqSeq) return;
    // 这一档**没有本地回落**：库里没有实体索引，回落出来的候选会缺一大半，
    // 用户分不清是站上没有还是我们没存 —— 所以如实报错。
    error.value = getApiErrorMessage(err, "搜索失败");
    entities.value = [];
  } finally {
    if (seq === reqSeq) loading.value = false;
  }
}

/**
 * 实体的作品态：从官网作品页抓一页（每页 40 部）。
 *
 * 与「搜影片」的区别是它**不受筛选/排序影响** —— 那些参数是本地库与上游搜索
 * 吃的东西，官网页面给什么就是什么（顺序也由官网决定）。所以这一态下筛选条
 * 是藏起来的。
 */
async function loadEntityMovies(append = false) {
  const target = activeEntity.value;
  if (!target) return;
  if (!append) abortSearch();
  const seq = append ? reqSeq : ++reqSeq;
  if (append) loadingMore.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const res = await fetchJavEntityMovies(target.kind, target.id, entityPage.value);
    if (seq !== reqSeq) return;
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    entityTotal.value = res.total ?? 0;
    // 「还有没有下一页」：有总数就按总数判；没总数（演员/系列/片商/导演）
    // 只能按「这一页装满没装满」判 —— 官网那四档页面不写总数。
    const reported = res.total ?? 0;
    entityHasMore.value = reported > 0
      ? items.value.length < reported
      : list.length >= ENTITY_PAGE_SIZE;
    total.value = reported > 0 ? reported : items.value.length;
  } catch (err) {
    if (seq !== reqSeq) return;
    // 追加失败保持已有内容：滚到一半一次网页请求失败，不该把看过的一屏清空。
    if (!append) {
      error.value = getApiErrorMessage(err, "加载失败");
      items.value = [];
      total.value = 0;
      entityHasMore.value = false;
    }
  } finally {
    if (seq === reqSeq) {
      if (append) loadingMore.value = false;
      else loading.value = false;
    }
  }
}

function reload() {
  if (entityMoviesMode.value) return loadEntityMovies();
  if (entitySearchMode.value) return loadEntities();
  if (searching.value) return loadSearch();
  return loadLocal();
}

/**
 * 滚到底时加载下一页。
 *
 * 上游搜索一次能翻回几百条（后端已聚合），但一次全画出来会卡；
 * 这里按页追加，用户滚到哪儿看到哪儿。
 *
 * 注意实体候选态**不续页**：那一档上游没有下一页。
 */
async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return;
  if (entityMoviesMode.value) {
    entityPage.value += 1;
    await loadEntityMovies(true);
  } else if (entitySearchMode.value) {
    return;
  } else if (searching.value) {
    searchPage.value += 1;
    await loadSearch(true);
  } else {
    page.value += 1;
    await loadLocal(true);
  }
}

/** 点中一个实体候选 → 进它的作品态。 */
function openEntity(entity: JavEntity) {
  entityTarget.value = { kind: entity.kind, id: entity.id, name: entity.name };
  entityPage.value = 1;
  entityHasMore.value = false;
  items.value = [];
  total.value = 0;
  entityTotal.value = 0;
  resetAutoFill();
  void loadEntityMovies();
}

/** 从实体作品态退回候选列表（还在搜实体这一档时）。 */
function backToEntities() {
  entityTarget.value = null;
  entityPage.value = 1;
  items.value = [];
  total.value = 0;
  entityHasMore.value = false;
  void reload();
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

// 「内容不足一屏」那条路。观察器只管「滚到底」，而**没有滚动空间时它一次都不会再触发**
// （内容不满一屏，页面根本滚不动）—— 手机竖屏看只有几条的分类时就是这个现象。
// 补一条：哨兵还在视口内就继续拉，直到填满一屏。
const { reset: resetAutoFill } = useFillViewport({
  sentinel,
  hasMore,
  busy: computed(() => loading.value || loadingMore.value),
  loadMore: () => void loadMore(),
});

// 换关键字 / 类型 / 标签 / 清单都会回第一页重查 —— 自动填充的额度也一起归零。
watch(
  () => [props.keyword, props.searchType, props.tag, props.listTarget?.id],
  () => resetAutoFill(),
);

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
 *
 * 这里还要**退出「实体作品」态**：那个态是「点了某个候选之后」的下一层，
 * 而上层条件（关键字 / 类型）一变，那一层就不成立了 —— 不退的话会顶着一个
 * 新的关键字、却还显示上一个演员的影片。
 */
watch(
  () => [props.keyword, props.searchType, props.tag, props.listTarget?.id],
  () => {
    searchPage.value = 1;
    page.value = 1;
    entityPage.value = 1;
    entityTarget.value = null;
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

/**
 * 当前实体的订阅状态。只有清单有订阅入口（订阅表只认 movie / actor / list，
 * 系列 / 片商 / 导演没有对应的订阅类型），所以这里只算清单那一套键。
 *
 * 注意订阅表里 target_type 是 `list`（单数），而界面口径是 `lists` ——
 * 用错的话那颗心永远是空心，点一下还会再建一条重复的订阅。
 */
const activeEntitySubscribed = computed(() => {
  const t = activeEntity.value;
  if (!t || t.kind !== "lists") return false;
  return props.subscribedKeys.has(`list:${t.id.toLowerCase()}`);
});

onMounted(reload);

defineExpose({ reload });
</script>

<template>
  <div>
    <!-- 搜索结果提示条。用户点名要的：搜的什么关键字、符合的有多少条。
         对应源码 search.html 的 search-head。
         `!activeEntity` 这一条不能省：进了某个清单/系列的作品态之后，搜索框里的
         关键字往往还留着（这一态就是从搜索结果点进来的），少了它就会同时挂出
         「找到了 N 条」和下面那条「共 N 部影片」—— 两条数字各自是不同来源的
         口径（一条是搜索命中的影片总数，一条是清单的成员总数），用户看到的是
         两个互相矛盾的数。 -->
    <div v-if="searching && !entitySearchMode && !activeEntity" class="jav-search-notice">
      <span>🔍 搜索</span>
      <span class="jav-search-notice__query">「{{ keyword }}」</span>
      <span v-if="searchTypeLabel">（{{ searchTypeLabel }}）</span>
      <!-- 还在飞的时候不要报「找到 0 条」：那看起来像已经查完了。
           一次搜索要往上翻几页聚合，真机上 1～6 秒，这段时间只说「搜索中」。 -->
      <template v-if="loading && !items.length">
        <span>· 搜索中…</span>
      </template>
      <template v-else>
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
      </template>
      <button type="button" class="jav-sort-chip" style="margin-left: auto" @click="emit('clearSearch')">
        清除
      </button>
    </div>

    <!-- 实体候选提示条：这一屏是「符合这个名字的演员/系列/片商/导演/清单」，
         点一个进去才看作品。上游这个接口**没有分页**（limit / page 都不生效），
         所以这里只说「找到 N 个」，不谈「已显示」。 -->
    <div v-if="entitySearchMode" class="jav-search-notice">
      <span>🔍 {{ ENTITY_KIND_LABELS[entityKind!] }}</span>
      <span class="jav-search-notice__query">「{{ keyword }}」</span>
      <!-- 与上面的影片搜索同一条理由：还没回来的时候不要说「找到 0 个」，
           那看着像已经查完了，而用户正盯着一个「0」等结果。 -->
      <template v-if="loading && !entities.length">
        <span>· 搜索中…</span>
      </template>
      <template v-else>
        <span>· 找到</span>
        <span class="jav-search-notice__query">{{ entities.length }}</span>
        <span>个匹配项，点一个看它的作品</span>
        <span v-if="entities.length >= 20" class="jav-search-notice__shown">
          · 上游一次最多给 20 个，换个更具体的名字能缩小范围
        </span>
      </template>
      <button type="button" class="jav-sort-chip" style="margin-left: auto" @click="emit('clearSearch')">
        清除
      </button>
    </div>

    <!-- 实体作品条：这一页是某个实体的作品。顺序由官网决定，我们的筛选与排序
         都掺和不进去，所以那两条在这里是藏起来的。
         清单（listTarget）走的是同一个对象，见 activeEntity。 -->
    <div v-if="activeEntity" class="jav-search-notice">
      <span>{{ activeEntity.kind === "lists" ? "📋 清单" : "🎬 " + ENTITY_KIND_LABELS[activeEntity.kind] }}</span>
      <span class="jav-search-notice__query">「{{ activeEntity.name }}」</span>
      <!-- 只有清单页写总数（「共 N 部影片」）；另外四档的页面不写，
           那时显示已加载条数，别用一个 0 去冒充总数。 -->
      <template v-if="entityTotal > 0">
        <span>· 共</span>
        <span class="jav-search-notice__query">{{ entityTotal }}</span>
        <span>部影片</span>
        <span v-if="items.length < entityTotal" class="jav-search-notice__shown">
          · 已显示 {{ items.length }}
        </span>
      </template>
      <template v-else-if="items.length">
        <span>· 已显示</span>
        <span class="jav-search-notice__query">{{ items.length }}</span>
        <span>部（这一页不写总数，滚到底为止）</span>
      </template>
      <!-- 「订阅清单」：与演员搜索页那颗心同一套 class 与交互，已订阅=取消，
           未订阅=切到订阅页并打开创建弹窗（清单名已填好）。
           放在这一条上而不是筛选条里 —— 筛选条在这一态是藏起来的。
           清单与 actorTarget 是同一个对象（activeEntity），所以这里只判 kind，
           不再区分「从抽屉点进来的清单」与「从搜索结果点进来的清单」。 -->
      <button
        v-if="activeEntity.kind === 'lists'"
        type="button"
        class="jd-btn jav-filters__actor-sub"
        style="margin-left: auto"
        :class="{ 'jd-btn--on': activeEntitySubscribed }"
        :title="activeEntitySubscribed ? '取消订阅' : `订阅清单「${activeEntity.name}」`"
        @click="emit('subscribeList', { id: activeEntity.id, name: activeEntity.name })"
      >
        <i class="fas fa-heart" /> {{ activeEntitySubscribed ? "已订阅" : "订阅清单" }}
      </button>
      <!-- 「返回」的去处分两种：从搜索结果点进来的回候选列表，
           从详情抽屉的关联清单点进来的回影库。 -->
      <button
        v-if="entityTarget"
        type="button"
        class="jav-sort-chip"
        @click="backToEntities"
      >
        返回{{ ENTITY_KIND_LABELS[entityTarget.kind] }}列表
      </button>
      <button v-else type="button" class="jav-sort-chip" @click="emit('clearList')">
        返回影库
      </button>
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
         **实体那一族除外**（作品页与候选列表）：那些参数是本地库与上游搜索吃的东西，
         官网作品页给什么就是什么；候选列表是「点一个进去」的选择，不适用片子筛选。 -->
    <div v-if="!activeEntity && !entitySearchMode" class="jav-filters">
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

    <!-- 实体候选：一屏卡片，点一个进去看它的作品。
         头像只有演员有；系列 / 片商 / 导演 / 清单都没有图，圆里放名字的前两个字
         （清单用紫色，与详情页「磁链是蓝的、清单是紫的」同一套区分）。
         候选**不显示订阅按钮**：系列 / 片商 / 导演目前没有对应的订阅类型，
         而清单要订阅得先确认是哪一个（同名清单不少），点进去再订阅更稳。 -->
    <div v-else-if="entitySearchMode && entities.length" class="jav-actor-grid">
      <div
        v-for="entity in entities"
        :key="entity.id"
        class="jav-actor-card"
        role="button"
        tabindex="0"
        @click="openEntity(entity)"
        @keyup.enter="openEntity(entity)"
      >
        <div class="jav-actor-avatar">
          <MediaImage
            v-if="entity.avatar_url"
            :src="javImageURL(entity.avatar_url)"
            :alt="entity.name"
            variant="person"
          />
          <span v-else class="jav-actor-avatar__text">{{ entity.name.slice(0, 2) }}</span>
        </div>
        <div class="jav-actor-name">{{ entity.name }}</div>
        <div v-if="entity.count" class="jav-entity-count">{{ entity.count }} 部作品</div>
      </div>
    </div>

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
      <div class="jav-empty__icon">{{ searching || entitySearchMode ? "🔍" : "🗂️" }}</div>
      <div v-if="entitySearchMode">
        没有找到叫「{{ keyword }}」的{{ ENTITY_KIND_LABELS[entityKind!] }}。
      </div>
      <!-- 实体作品态要排在 searching 之前：那一态往往也还带着关键字
           （比如「搜索框里留着 xx、点了某个清单」），按 searching 先判会
           冒出「没有找到「xx」相关内容」—— 而用户看的是那个清单，不是搜索结果。 -->
      <div v-else-if="activeEntity">这里一部影片都没有（也可能官网这一页抓不下来，稍后重试）。</div>
      <div v-else-if="searching">
        <div>没有找到「{{ keyword }}」相关内容。</div>
        <!-- 番号走的是**精确匹配**，半截输入必然 0 条。不给这句的话
             用户会以为是站上没有这部片，实际上只是还没输完。 -->
        <div v-if="numberHint" class="jav-search-notice__shown" style="margin-top: 8px">
          {{ numberHint }}
        </div>
      </div>
      <div v-else>影库为空，去榜单或搜索里找几部入库吧。</div>
    </div>

    <!-- 滚动到底自动续上。哨兵**常驻 DOM**（不用 v-if 摘掉）——
         观察器是 onMounted 时挂一次的，元素一旦被移除，它就再也收不到回调
         （这个坑在演员墙那儿踩过：哨兵在 v-else 分支里，于是永远只出 60 位）。
         实体候选态不续页，所以那时只是不显示文字，元素留着。 -->
    <div ref="sentinel" class="jav-loadmore">
      <template v-if="!entitySearchMode">
        <span v-if="loadingMore">加载中…</span>
        <!-- 演员/系列/片商/导演的作品页不写总数，那时 entityTotal 是 0，
             「还有 N 条」算出来也永远是 0 —— 换成不带数字的说法。 -->
        <span v-else-if="hasMore">
          {{ entityMoviesMode && !entityTotal ? "往下滚，继续加载" : `往下滚，还有 ${total - items.length} 条` }}
        </span>
        <span v-else-if="items.length">
          {{ entityMoviesMode && !entityTotal ? `已全部加载（${items.length} 条）` : `已全部加载（${total} 条）` }}
        </span>
      </template>
    </div>
  </div>
</template>
