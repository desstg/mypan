<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import JavLibraryPanel from "@/components/admin/JavLibraryPanel.vue";
import JavMovieDrawer from "@/components/admin/JavMovieDrawer.vue";
import JavRankPanel from "@/components/admin/JavRankPanel.vue";
import JavSubscribePanel from "@/components/admin/JavSubscribePanel.vue";
import TGDiscoverWall from "@/components/admin/TGDiscoverWall.vue";
import TGSubscribedPanel from "@/components/admin/TGSubscribedPanel.vue";
import TGSubscribeSettingsDrawer from "@/components/admin/TGSubscribeSettingsDrawer.vue";
import TGTitleDetailModal from "@/components/admin/TGTitleDetailModal.vue";
import { getApiErrorMessage } from "@/api/client";
import { deleteJavSubscription, fetchJavConfig, fetchJavSubscriptions } from "@/api/jav";
import { createTGSubscription, fetchTGSubscriptions } from "@/api/tgSubscribe";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useAccountsStore } from "@/stores/accounts";
import { toast } from "@/composables/useToast";
import type { TGMediaType, TGSubscription, TGTMDBSearchResult } from "@/types/tg-subscribe";
import type { JavActor, JavSubscription } from "@/types/jav";
import { JAV_SEARCH_TYPES } from "@/types/jav";
import "@/styles/tg-subscribe.css";

// 顶栏形态照 8.png：左侧「图标 + tab」，右侧「搜索框 + 齿轮」。
// 主题切换不在这里重复做 —— AdminGlobalActions 里已经有了。

// 「已订阅」放在最左：它是这一页的「我的」，后面三个才是去外面找片。
const TAB_SUBSCRIBED = "subscribed";
const TAB_MOVIE = "movie";
const TAB_TV = "tv";
const TAB_JAV = "jav";
// 番号是个**分组**：它自己不再是内容 Tab，真正的两档是展开出来的子项。
const TAB_JAV_RANK = "jav-rank";
const TAB_JAV_LIB = "jav-lib";
const TAB_JAV_SUB = "jav-sub";
const JAV_CHILD_TABS: readonly string[] = [TAB_JAV_RANK, TAB_JAV_LIB, TAB_JAV_SUB];

/**
 * 番号功能是否可见。
 *
 * 读的是「番号相关设置」里的开关，默认为真 —— 拿不到配置时也按可见处理：
 * 后端不可用不该让整个菜单消失，那只会在排查时多一层干扰。
 */
const javEnabled = ref(true);
async function loadJavEnabled() {
  try {
    javEnabled.value = (await fetchJavConfig()).enabled;
  } catch {
    javEnabled.value = true;
  }
}

// 默认落在「电影」：这一页的主要用途是逛片（发现墙），
// 「已订阅」是回头查订阅状态才去的。先进发现墙更符合打开的意图。
const { activeTab, setActiveTab } = useSectionTabRoute(TAB_MOVIE, [
  TAB_SUBSCRIBED,
  TAB_MOVIE,
  TAB_TV,
  TAB_JAV,
  ...JAV_CHILD_TABS,
]);

const accountsStore = useAccountsStore();

/**
 * 当前 tab 对应的 TMDB 媒体类型。
 *
 * 只看**发现墙用不用得上**：已订阅读的是本地订阅表，番号是占位空档 ——
 * 两者都不打 TMDB，所以统一回落成 movie 只是给不渲染的组件一个合法值。
 */
const mediaType = computed<TGMediaType>(() => (activeTab.value === TAB_TV ? "tv" : "movie"));

/** 「番号」分组的展开状态。默认收起，点一下才把它那两档放出来。 */
const javOpen = ref(false);

/**
 * 展开之前停在哪一档。
 *
 * 收起时如果人正待在子项里，得把他放回这里 —— 否则当前 Tab 的按钮已经消失，
 * 页面却还在渲染它，用户看到的是一屏对不上任何按钮的内容。
 */
const tabBeforeJav = ref<string>(TAB_SUBSCRIBED);

const TABS = computed(() => [
  { key: TAB_SUBSCRIBED, label: "已订阅" },
  { key: TAB_MOVIE, label: "电影" },
  { key: TAB_TV, label: "剧集" },
  // 关掉开关时连分组按钮一起不渲染 —— 用户要的是「看不到番号」，
  // 留一个能展开的空壳不算看不到。
  ...(javEnabled.value
    ? [
        { key: TAB_JAV, label: "番号", expandable: true, expanded: javOpen.value },
        ...(javOpen.value
          ? [
              { key: TAB_JAV_RANK, label: "榜单" },
              { key: TAB_JAV_LIB, label: "影库" },
              { key: TAB_JAV_SUB, label: "订阅" },
            ]
          : []),
      ]
    : []),
]);

/**
 * Tab 栏的点击入口。
 *
 * 只有「番号」这一档特殊：它是分组开关，点它只切换展开状态、不切页。
 * 其余（含展开出来的子项）照常走 setActiveTab。
 */
function onTabChange(key: string) {
  if (key === TAB_JAV) {
    toggleJavMenu();
    return;
  }
  void setActiveTab(key);
}

function toggleJavMenu() {
  if (!javOpen.value) {
    tabBeforeJav.value = activeTab.value;
    javOpen.value = true;
    return;
  }
  javOpen.value = false;
  if (JAV_CHILD_TABS.includes(activeTab.value)) {
    void setActiveTab(tabBeforeJav.value);
  }
}

// 深链进来（?tab=jav-rank）或者从浏览器后退回到某一档子项时，分组必须是展开的，
// 否则 Tab 按钮不在、内容却在渲染。
watch(
  [activeTab, javEnabled],
  ([tab, enabled]) => {
    if (!enabled) {
      // 开关关掉时，正停在番号子项上的用户会被留在一屏「对不上任何按钮」的内容里。
      // 先把他送回来处，再收起分组。
      javOpen.value = false;
      if (JAV_CHILD_TABS.includes(tab)) void setActiveTab(tabBeforeJav.value || TAB_SUBSCRIBED);
      return;
    }
    if (JAV_CHILD_TABS.includes(tab)) javOpen.value = true;
  },
  { immediate: true },
);

/** 发现墙只在电影/剧集两个 tab 出现。 */
const discoverVisible = computed(() => activeTab.value === TAB_MOVIE || activeTab.value === TAB_TV);

/**
 * 番号搜索框只在番号分组里出现。
 *
 * 与电影/剧集那个 TMDB 搜索框共用同一个位置，但两者不会同时出现 ——
 * 同一处并排两个搜索框，用户每次都得先想「我该用哪个」。
 */
const javVisible = computed(() => JAV_CHILD_TABS.includes(activeTab.value));

/** 番号搜索的查询状态。由页面持有、传给影库页 —— 不塞 URL，
 *  否则每敲一个字都会往历史里 push 一条。 */
const javType = ref("all");
const javKeywordInput = ref("");
const javKeyword = ref("");
const javSearchType = ref("all");
/**
 * 当前正在看的清单（非空时影库那一档展示它的成员）。
 *
 * 必须有真实的清单 id：成员只能靠抓官网清单页拿，而那是按 id 的路由。
 * 任何一次新的搜索/筛选都会把它清掉 —— 那是两种互斥的浏览态。
 */
const javList = ref<{ id: string; name: string } | null>(null);
let javSearchTimer: ReturnType<typeof setTimeout> | null = null;

function onJavSearchInput() {
  if (javSearchTimer) clearTimeout(javSearchTimer);
  // 与 TMDB 那个搜索框同款防抖：每敲一个字打一次上游既慢又容易被限流。
  javSearchTimer = setTimeout(() => submitJavSearch(), 400);
}

function submitJavSearch() {
  const kw = javKeywordInput.value.trim();
  // 搜索与标签筛选是互斥的两种浏览态：带着标签去搜上游只会得到空结果。
  if (kw) javTag.value = "";
  // 搜索与**清单**也是互斥的：一旦重新搜，就不该再挂着上一个清单。
  if (kw) javList.value = null;
  javKeyword.value = kw;
  javSearchType.value = javType.value;
  // 提交后切到影库 —— 搜索结果就落在那里。
  if (kw && activeTab.value !== TAB_JAV_LIB) void setActiveTab(TAB_JAV_LIB);
}

function clearJavSearch() {
  javKeywordInput.value = "";
  javKeyword.value = "";
}

/** 已订阅的番号集合，卡片用它把「订阅」换成「已订阅」。 */
const javSubscribedKeys = computed(() => {
  const set = new Set<string>();
  for (const sub of javSubscriptions.value) {
    set.add(`${sub.target_type}:${sub.target_id.toLowerCase()}`);
  }
  return set;
});

/** 影库页的标签筛选。点详情里的标签时设上，切到影库那一档展示。 */
const javTag = ref("");

const javSubscriptions = ref<JavSubscription[]>([]);
const javSubPanelRef = ref<InstanceType<typeof JavSubscribePanel> | null>(null);

async function loadJavSubscriptions() {
  try {
    const res = await fetchJavSubscriptions({ page_size: 500 });
    javSubscriptions.value = res.items ?? [];
  } catch {
    // 番号订阅拉不到不该影响电影/剧集那两个 tab 的可用性。
    javSubscriptions.value = [];
  }
}

// —— 番号详情抽屉 ——
const javDrawerOpen = ref(false);
const javDrawerMovieId = ref<string | null>(null);

/**
 * 抽屉里那一部对应的订阅记录，没订阅就是 null。
 *
 * 详情接口不返回订阅状态，而全量订阅列表本来就躺在这一层（同一份还要喂给
 * 一屏卡片，见 javSubscribedKeys），所以直接现算，不额外发请求。
 */
const javDrawerSubscription = computed(() => {
  const id = javDrawerMovieId.value?.toLowerCase();
  if (!id) return null;
  return (
    javSubscriptions.value.find(
      (s) => s.target_type === "movie" && s.target_id.toLowerCase() === id,
    ) ?? null
  );
});

/** 抽屉右上角那颗心是不是填满的。 */
const javDrawerSubscribed = computed(() => javDrawerSubscription.value !== null);
/** 推送要拿它当上下文：推去哪个网盘、哪个目录是订阅的属性，不是影片的。 */
const javDrawerSubscriptionId = computed(() => javDrawerSubscription.value?.id);

function openJavMovie(movie: { id: string }) {
  javDrawerMovieId.value = movie.id;
  javDrawerOpen.value = true;
}

/**
 * 从详情里点「关联影片」跳到另一部。
 *
 * 只换 movieId，抽屉不关 —— 用户是在顺着一条线往下看，关掉再开是多余的一跳。
 * 抽屉内部会因为 movieId 变了而重新加载（它有对应的 watch）。
 */
function openJavMovieById(id: string) {
  if (!id) return;
  javDrawerMovieId.value = id;
}

/**
 * 卡片上的订阅按钮。
 *
 * 已经订阅过的直接取消，否则把目标交给订阅页弹窗 ——
 * 与电影/剧集那两个 tab 的「+ 订阅」是同一套交互。
 */
async function toggleJavSubscribe(movie: { id: string; number?: string; title?: string }) {
  const key = `movie:${movie.id.toLowerCase()}`;
  if (javSubscribedKeys.value.has(key)) {
    const existing = javSubscriptions.value.find(
      (s) => s.target_type === "movie" && s.target_id.toLowerCase() === movie.id.toLowerCase(),
    );
    if (!existing) return;
    try {
      await deleteJavSubscription(existing.id);
      toast.success(`已取消订阅《${movie.number || movie.title}》`);
      await loadJavSubscriptions();
    } catch (error) {
      toast.error(getApiErrorMessage(error, "取消订阅失败"));
    }
    return;
  }
  // 建订阅需要更多条件（网盘、画质…），交给订阅页的弹窗，不在这里静默建一条默认的。
  await setActiveTab(TAB_JAV_SUB);
  javSubPanelRef.value?.onPendingTarget({
    id: movie.id,
    name: movie.number || movie.title || movie.id,
    type: "movie",
  });
}

/**
 * 详情抽屉右上角那颗心。
 *
 * 与卡片上那颗是**同一套判断**，所以直接复用 toggleJavSubscribe ——
 * 区别只是抽屉只递过来 id 和番号，拿不到整张卡片。
 */
function toggleJavDrawerSubscribe(movie: { id: string; name: string }) {
  void toggleJavSubscribe({ id: movie.id, number: movie.name });
}

/**
 * 演员订阅按钮。榜单页、搜索页两处共用。
 *
 * 参数收窄成 { id, name } 而不是完整 JavActor：搜索页反查出来的只有这两样
 * （搜索结果里没有头像），没必要为了类型好看去补一个空头像字段。
 */
async function toggleJavActorSubscribe(actor: { id: string; name: string }) {
  const key = `actor:${actor.id.toLowerCase()}`;
  const existing = javSubscriptions.value.find(
    (s) => s.target_type === "actor" && s.target_id.toLowerCase() === actor.id.toLowerCase(),
  );
  if (key && javSubscribedKeys.value.has(key) && existing) {
    try {
      await deleteJavSubscription(existing.id);
      toast.success(`已取消订阅「${actor.name}」`);
      await loadJavSubscriptions();
    } catch (error) {
      toast.error(getApiErrorMessage(error, "取消订阅失败"));
    }
    return;
  }
  await setActiveTab(TAB_JAV_SUB);
  javSubPanelRef.value?.onPendingTarget({ id: actor.id, name: actor.name, type: "actor" });
}

/**
 * 「订阅清单」那颗按钮。与 toggleJavActorSubscribe 逐条同构 ——
 * 已订阅就取消，否则切到订阅页并把目标交给它，由它打开创建弹窗（清单名已填好）。
 *
 * 清单的 target_id 用的是 JAVDB 的清单 id（从关联清单点进来时带过来的），
 * 源码存 `data-target-id="{{ list_id }}"` 也是这个。
 */
async function toggleJavListSubscribe(list: { id: string; name: string }) {
  const key = `list:${list.id.toLowerCase()}`;
  const existing = javSubscriptions.value.find(
    (s) => s.target_type === "list" && s.target_id.toLowerCase() === list.id.toLowerCase(),
  );
  if (javSubscribedKeys.value.has(key) && existing) {
    try {
      await deleteJavSubscription(existing.id);
      toast.success(`已取消订阅「${list.name}」`);
      await loadJavSubscriptions();
    } catch (error) {
      toast.error(getApiErrorMessage(error, "取消订阅失败"));
    }
    return;
  }
  await setActiveTab(TAB_JAV_SUB);
  javSubPanelRef.value?.onPendingTarget({ id: list.id, name: list.name, type: "list" });
}

/**
 * 点详情里的演员 → 按这个名字搜。
 *
 * 搜索状态在页面这一层（搜索框就在 Tab 栏上），所以由这里改写它，
 * 再切到影库页 —— 那里就是搜索结果的落点。
 */
function searchJavActor(name: string) {
  javTag.value = "";
  javType.value = "actor";
  javKeywordInput.value = name;
  // 先收起抽屉再切页：抽屉是从右侧盖出来的，不收起来的话
  // 切过去的结果仍然被它挡着，看起来像「点了没反应」。
  javDrawerOpen.value = false;
  submitJavSearch();
}

/**
 * 从详情里点「关联清单」→ 按清单名搜片，落在影库那一档。
 *
 * 与 searchJavActor 是同一套：搜索类型选「清单」、关键词填清单名，
 * 上游的 /v2/search?type=lists 就是「按清单名找片子」。
 *
 * 源码那边点清单是跳 /list/<id>（它有个清单详情页，影片靠抓官网 HTML 拿）；
 * LitePan 没有那条链，走搜索这条路结果一样，而且复用现成的影库页 ——
 * 卡片、筛选、翻页、订阅按钮全都是现成的。
 */
function searchJavList(list: { id: string; name: string }) {
  if (!list.id) return;
  // 清单与搜索是互斥的两态：进清单态就把搜索条件清干净，
  // 否则影库页会顶着上一次的关键词、却显示清单的内容。
  clearJavSearch();
  javTag.value = "";
  javList.value = list;
  // 先收起抽屉再切页：抽屉是从右侧盖出来的，不收起来的话
  // 切过去的结果仍然被它挡着，看起来像「点了没反应」。
  javDrawerOpen.value = false;
  if (activeTab.value !== TAB_JAV_LIB) void setActiveTab(TAB_JAV_LIB);
}

/** 点详情里的标签 → 按标签筛影库。对应源码跳 /library?tag=。 */
function filterJavTag(tag: string) {
  clearJavSearch();
  // 同上：进标签筛选就退出清单态。
  javList.value = null;
  javTag.value = tag;
  // 同上：不收起抽屉的话，筛选结果会被抽屉挡住。
  javDrawerOpen.value = false;
  if (activeTab.value !== TAB_JAV_LIB) void setActiveTab(TAB_JAV_LIB);
}

function openJavActor(actor: JavActor) {
  // 演员榜点进去 = 按演员搜索，与源码跳 /search?type=actor 同一意图。
  javType.value = "actor";
  javKeywordInput.value = actor.name;
  submitJavSearch();
}

const subscriptions = ref<TGSubscription[]>([]);
const configOpen = ref(false);

const detailOpen = ref(false);
const detailItem = ref<TGTMDBSearchResult | null>(null);

// 搜索：400ms 防抖后再交给海报墙，避免每敲一个字打一次 TMDB。
const keywordInput = ref("");
const keyword = ref("");
let searchTimer: ReturnType<typeof setTimeout> | null = null;

/** 已订阅的 TMDB 标识集合，海报墙用它把「+ 订阅」换成 ✓。 */
const subscribedKeys = computed(() => {
  const set = new Set<string>();
  for (const sub of subscriptions.value) {
    set.add(`${sub.media_type}:${sub.tmdb_id}`);
  }
  return set;
});

async function loadSubscriptions() {
  try {
    subscriptions.value = await fetchTGSubscriptions();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载订阅列表失败"));
  }
}

function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    keyword.value = keywordInput.value;
  }, 400);
}

function clearSearch() {
  keywordInput.value = "";
  keyword.value = "";
}

function openDetail(item: TGTMDBSearchResult) {
  detailItem.value = item;
  detailOpen.value = true;
}

/** 海报墙右上角的「+」：直接按默认设置订阅，不用进弹窗。 */
async function quickSubscribe(item: TGTMDBSearchResult) {
  try {
    await createTGSubscription({
      tmdb_id: String(item.id),
      media_type: mediaType.value,
      title: item.title || item.name || "",
      original_title: item.original_title || item.original_name || "",
      year: Number((item.release_date || item.first_air_date || "").slice(0, 4)) || 0,
      poster_path: item.poster_path || "",
      overview: item.overview || "",
      quality_profile_id: 0,
      target_account_id: 0,
      target_parent_id: "",
      target_display_path: "",
      push_provider: "auto",
      collect_window_min: 5,
      // 与详情页保持一致：洗版默认关闭，否则「+」建出来的订阅永远不会自动完成。
      upgrade_enabled: false,
    });
    toast.success(`已订阅《${item.title || item.name}》，可在卡片详情里调目标与画质`);
    await loadSubscriptions();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "订阅失败"));
  }
}

async function onChanged() {
  await loadSubscriptions();
  await loadJavSubscriptions();
}

// 切换 tab 时清掉搜索词 —— 上一个是关键词的结果留在另一个 tab 里很突兀。
watch(activeTab, () => {
  clearSearch();
  // 番号的搜索词只在番号分组内保留：切到电影再把「SSIS-001」带回来，
  // 影库会因为拿不到上游而回落成本地搜索，看起来像是坏了。
  if (!JAV_CHILD_TABS.includes(activeTab.value)) clearJavSearch();
});

/**
 * 关闭设置抽屉后重读番号开关。
 *
 * 用户刚在「番号相关设置」里关掉菜单，这一页的 Tab 栏要立刻跟着变 ——
 * 不然他关完回来发现菜单还在，会以为没生效。
 */
async function onJavSettingsClosed() {
  configOpen.value = false;
  await loadJavEnabled();
  if (javEnabled.value) await loadJavSubscriptions();
}

onMounted(async () => {
  await Promise.all([
    loadSubscriptions(),
    accountsStore.loadAccounts(),
    loadJavEnabled(),
    loadJavSubscriptions(),
  ]);
});
</script>

<template>
  <div class="tg-wall">
    <SectionTabBar
      :model-value="activeTab"
      :tabs="TABS"
      icon="fire"
      @update:model-value="onTabChange"
    >
      <template #actions>
        <div style="display: flex; align-items: center; gap: 10px">
          <div v-if="discoverVisible" style="width: 240px">
            <AppInput
              v-model="keywordInput"
              placeholder="搜索影片，回车或稍候自动搜索"
              @input="onSearchInput"
              @keyup.enter="keyword = keywordInput"
            />
          </div>
          <AppButton v-if="discoverVisible && keyword" type="button" variant="ghost" size="sm" @click="clearSearch">
            清除
          </AppButton>

          <!-- 番号搜索框：类型下拉 + 输入框，与源码 base.html 的类型选择一致。
               只在番号分组里出现，与上面那个 TMDB 搜索框共用同一个位置。 -->
          <template v-if="javVisible">
            <div style="width: 120px">
              <AppSelect v-model="javType" :options="JAV_SEARCH_TYPES.map((t) => ({ value: t.value, label: t.label }))" />
            </div>
            <div style="width: 220px">
              <AppInput
                v-model="javKeywordInput"
                placeholder="搜索番号 / 演员 / 片商…"
                @input="onJavSearchInput"
                @keyup.enter="submitJavSearch"
              />
            </div>
            <AppButton v-if="javKeyword" type="button" variant="ghost" size="sm" @click="clearJavSearch">
              清除
            </AppButton>
          </template>
          <button
            type="button"
            class="tg-icon-btn"
            title="TG 订阅设置"
            aria-label="TG 订阅配置"
            @click="configOpen = true"
          >
            <i class="fas fa-cog" aria-hidden="true" />
          </button>
        </div>
      </template>
    </SectionTabBar>

    <!-- 这一页原先还顶着一条「抓取状态 + 追更计数」的横条，四个 tab 都显示。
         去掉了：已订阅页的每张卡片已经逐条标了状态与集数进度，横条纯属重复；
         而电影/剧集页读的是 TMDB 的发现墙，抓取跑没跑跟那两页没有半点关系。
         抓取状态要看的话在「TG 频道」那个 tab 里，那才是它讲的主角。 -->

    <TGSubscribedPanel v-if="activeTab === TAB_SUBSCRIBED" @changed="onChanged" />

    <!-- 番号三档。影库那一档同时承担搜索结果 —— 搜索框提交后就切到这里。 -->
    <JavRankPanel
      v-else-if="activeTab === TAB_JAV_RANK"
      :subscribed-keys="javSubscribedKeys"
      @open="openJavMovie"
      @subscribe="toggleJavSubscribe"
      @subscribe-actor="toggleJavActorSubscribe"
      @open-actor="openJavActor"
    />

    <JavLibraryPanel
      v-else-if="activeTab === TAB_JAV_LIB"
      :keyword="javKeyword"
      :search-type="javSearchType"
      :tag="javTag"
      :subscribed-keys="javSubscribedKeys"
      :list-target="javList"
      @open="openJavMovie"
      @subscribe="toggleJavSubscribe"
      @subscribe-actor="toggleJavActorSubscribe"
      @subscribe-list="toggleJavListSubscribe"
      @clear-search="clearJavSearch"
      @clear-tag="javTag = ''"
      @clear-list="javList = null"
    />

    <JavSubscribePanel
      v-else-if="activeTab === TAB_JAV_SUB"
      ref="javSubPanelRef"
      :movie-detail-open="javDrawerOpen"
      @open="openJavMovie"
      @changed="onChanged"
    />

    <!-- 兜底：?tab=jav 这种老链接。点「番号」本身已经不会切到这一档了。 -->
    <AdminEmptyState
      v-else-if="activeTab === TAB_JAV"
      icon="🚧"
      title="番号"
      description="点「番号」可以展开它下面的榜单、影库与订阅。"
    />

    <TGDiscoverWall
      v-else
      :media-type="mediaType"
      :keyword="keyword"
      :subscribed-keys="subscribedKeys"
      @open="openDetail"
      @subscribe="quickSubscribe"
    />

    <TGTitleDetailModal
      :open="detailOpen"
      :item="detailItem"
      :media-type="mediaType"
      @close="detailOpen = false"
      @changed="onChanged"
    />

    <TGSubscribeSettingsDrawer
      :open="configOpen"
      @close="onJavSettingsClosed"
      @changed="onChanged"
    />

    <JavMovieDrawer
      :open="javDrawerOpen"
      :movie-id="javDrawerMovieId"
      :subscription-id="javDrawerSubscriptionId"
      :subscribed="javDrawerSubscribed"
      @close="javDrawerOpen = false"
      @changed="onChanged"
      @search-actor="searchJavActor"
      @search-list="searchJavList"
      @filter-tag="filterJavTag"
      @open-movie="openJavMovieById"
      @toggle-subscribe="toggleJavDrawerSubscribe"
    />
  </div>
</template>
