<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import JavUserSharesModal from "@/components/admin/JavUserSharesModal.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavMovie,
  fetchJavPreviewURL,
  fetchJavRelatedLists,
  fetchJavReviews,
  ingestJavMovie,
  javImageURL,
  pushJavMagnet,
} from "@/api/jav";
import { toast } from "@/composables/useToast";
import type {
  JavActor,
  JavCommentShare,
  JavMovieDetail,
  JavRelatedList,
  JavReview,
} from "@/types/jav";
import "@/styles/jav.css";

/**
 * 影片详情抽屉。内容按源码 detail.html，但换成 LitePan 的右侧滑出抽屉。
 *
 * 四个 tab：磁力链接 / 评论区分享 / 关联清单 / 评论。
 * **不做 AVDB 磁链** —— 那一档依赖源码自己的外部磁链库集成，LitePan 没有。
 *
 * 「评论区分享」与「关联清单」在源码里各自有独立的数据源，这里的数据模型
 * 还没有对应的采集（ed2k 分享要抓评论区、关联清单要走 /v1/lists/related），
 * 所以两档先以「暂无数据」呈现，而不是伪装成有内容 —— 那会让人以为抓取坏了。
 */
const props = defineProps<{
  open: boolean;
  movieId: string | null;
  /**
   * 这一部对应的订阅记录 id，没订阅就是 undefined。推送拿它当上下文
   * （往哪个网盘、哪个目录推是订阅的属性，不是影片的）。
   */
  subscriptionId?: number;
  /**
   * 这一部订阅了没有。**必须由页面喂进来**：详情接口不返回订阅状态，
   * 而全量订阅列表在页面那一层（它还要给一屏卡片用，见 javSubscribedKeys）。
   */
  subscribed?: boolean;
}>();

const emit = defineEmits<{
  close: [];
  changed: [];
  /** 点演员 → 按演员搜。与源码跳 /search?type=actor&q=<名字> 同一意图。 */
  searchActor: [name: string];
  /** 点标签 → 按标签筛影库。对应源码跳 /library?tag=<标签>。 */
  filterTag: [tag: string];
  /**
   * 点关联清单 → 按清单名搜片。
   *
   * 源码是跳 /list/<id>（它那边有个清单详情页，影片靠抓官网 HTML 拿）；
   * LitePan 没有那条链，改走搜索 —— 上游的 /v2/search?type=lists 正是
   * 「按清单名找片子」，与「点演员 → 按演员搜」是同一个套路。
   *
   * 把 id 一起带上去：搜索结果页那颗「订阅清单」要用它当订阅的 target_id
   * （源码存的也是 JAVDB 的清单 id）。只按名字搜出来的清单没有 id，
   * 那种情况由页面那边回落成用名字当键。
   */
  searchList: [list: { id: string; name: string }];
  /** 点关联影片 → 打开那一部的详情。 */
  openMovie: [id: string];
  /**
   * 点右上角那颗心。已订阅=取消、未订阅=建订阅，两件事差别太大，
   * 判断和后续动作都在页面那一层做（它才有订阅列表和创建弹窗）。
   */
  toggleSubscribe: [movie: { id: string; name: string }];
}>();

const TAB_MAGNETS = "magnets";
const TAB_COMMENTS = "comments";
const TAB_RELATED = "related";
const TAB_REVIEWS = "reviews";

const tab = ref(TAB_MAGNETS);
const loading = ref(false);
const refreshing = ref(false);
const pushing = ref(false);
const detail = ref<JavMovieDetail | null>(null);
const error = ref("");

/** 分享者弹窗（点评论区里的用户名打开）。 */
const sharerOpen = ref(false);
const sharerUserId = ref(0);
const sharerName = ref("");

const reviews = ref<JavReview[]>([]);
const reviewTotal = ref(0);
const reviewPage = ref(1);
const reviewsLoading = ref(false);

/**
 * 关联清单：**点开那一档才加载**。
 *
 * 它不在详情响应里（那样打开详情就得多打一次上游，而多数人点开详情只看磁链）。
 * 代价是表头上那个数字要等点过一次才知道 —— 所以没加载过时**不渲染**那个角标，
 * 而不是先写个 0（0 会被读成「这部片确实没有关联清单」）。
 */
const relatedLists = ref<JavRelatedList[]>([]);
const relatedLoaded = ref(false);
const relatedLoading = ref(false);
/**
 * 抓取失败的说明。
 *
 * 有它才能把「抓不到」和「确实没有」分开：两者都表现为一个空列表，
 * 只显示「暂无关联清单」会让上游的一次抖动看起来像「这部片就是没有片单」。
 */
const relatedError = ref("");

/**
 * 四个 Tab。
 *
 * 磁链 / 分享 / 评论的数量来自**详情响应**（服务端组装时一并算好），所以抽屉一
 * 打开就是准的；关联清单那一档是点开才拉的，没拉过就不给数字。
 */
const TABS = computed(() => [
  { key: TAB_MAGNETS, label: "磁力链接", count: detail.value?.magnets.length ?? 0 },
  { key: TAB_COMMENTS, label: "评论区分享", count: detail.value?.comment_shares.length ?? 0 },
  { key: TAB_RELATED, label: "关联清单", count: relatedLoaded.value ? relatedLists.value.length : undefined },
  { key: TAB_REVIEWS, label: "评论", count: detail.value?.comments_count ?? 0 },
]);

/** 星级：四舍五入到 0-5，与源码 stars 宏一致。 */
const starCount = computed(() => {
  const n = Math.round(detail.value?.score || 0);
  return Math.max(0, Math.min(5, n));
});

/**
 * 播放。
 */
/**
 * 播放封面上的那颗按钮放的是**预览片**（JAVDB 给的预告），不是整片。
 *
 * 上游给的是 m3u8。Chrome 不原生支持，所以要 hls.js —— 直接塞进 <video src>
 * 会得到一片黑，控制台只留一句很含糊的格式错误。
 */
/** 正在现取播放地址。取一次要走上游，慢的时候要一秒多。 */
const playerLoading = ref(false);

async function onPlay() {
  if (!props.movieId || playerLoading.value) return;
  playerLoading.value = true;
  try {
    // 不用 detail 里那份 preview_video_url：它是上游签发的**限时**地址
    // （带 sign/t，十几个小时就过期），播放前现取一次拿新签名。
    const url = await fetchJavPreviewURL(props.movieId);
    if (!url) {
      toast.info("这部影片没有预览片");
      return;
    }
    playerURL.value = url;
    showPlayer.value = true;
    await mountPlayer();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "预览片地址获取失败"));
  } finally {
    playerLoading.value = false;
  }
}

/** 挂 <video>：HLS 走 hls.js，真能原生播的（Safari）才直连。 */
async function mountPlayer() {
  await new Promise((r) => setTimeout(r, 0));
  const el = videoRef.value;
  const url = playerURL.value;
  if (!el || !url) return;

  // 判据必须是 "probably"，不能是「canPlayType 有返回值」。
  //
  // 这里踩过一个坑：**Chrome 对 `application/vnd.apple.mpegurl` 也回 "maybe"**
  // （它对 video/mp4 一样回 "maybe"，所以这个值压根不能当能力用）。早先写的是
  // 「非空即能播」，于是 Chrome 也走了直连那条路、hls.js 永远挂不上 ——
  // 浏览器把 m3u8 当普通文件拉，控制台一句 MEDIA_ERR_SRC_NOT_SUPPORTED（code 4），
  // 画面上就是一帧不出的黑屏。
  const isHls = url.toLowerCase().includes(".m3u8");
  const nativeHls = el.canPlayType("application/vnd.apple.mpegurl") === "probably";

  if (isHls && !nativeHls) {
    const { default: Hls } = await import("hls.js");
    if (Hls.isSupported()) {
      const instance = new Hls();
      instance.loadSource(url);
      instance.attachMedia(el);
      // 挂上播放器只是「准备好了」，不调 play() 画面就停在第一帧，用户还得再去按一次
      // 原生控制条上的播放键 —— 明明刚才已经按过封面那颗了。
      //
      // 调两次是故意的：**立刻**这次赶的是用户手势的时效（点封面键给的授权只有几秒，
      // 而取地址 + 拉清单可能就耗掉它）；清单解析完那次赶的是「真的有数据可播了」。
      // 两次都被自动播放策略拦下时会 reject，吞掉即可 —— 控制条上的播放键照常能用。
      void el.play().catch(() => {});
      instance.on(Hls.Events.MANIFEST_PARSED, () => {
        void el.play().catch(() => {});
      });
      hls = instance;
      return;
    }
  }
  // 原生 HLS（Safari）/ 非 m3u8 / 浏览器没有 MSE：交给 <video> 自己拉。
  //
  // 注意顺序：走 hls.js 时**不能**先塞 el.src —— 那会让元素先去拉一次它根本
  // 播不了的 m3u8，白白留下一个 code 4 的错误（hls.js 挂上后也不一定擦得掉）。
  el.src = url;
  void el.play().catch(() => {});
}

/** 关掉播放：把 <video> 的 src 也清掉，否则它会继续在后台跑。 */
function closePlayer() {
  showPlayer.value = false;
  playerURL.value = "";
  if (hls) {
    hls.destroy();
    hls = null;
  }
  const el = videoRef.value;
  if (el) {
    el.pause();
    el.removeAttribute("src");
    el.load();
  }
}

/** 灯箱当前显示第几张。null = 没打开。存索引而不是 URL —— 左右翻页要它。 */
const lightboxIndex = ref<number | null>(null);
const lightboxImages = computed(() => detail.value?.preview_images ?? []);

function openLightbox(i: number) {
  if (!lightboxImages.value.length) return;
  lightboxIndex.value = i;
}

function closeLightbox() {
  lightboxIndex.value = null;
}

/** 左右翻页。到头了绕回另一端 —— 与源码的取模循环一致。 */
function stepLightbox(delta: number) {
  const n = lightboxImages.value.length;
  if (!n || lightboxIndex.value === null) return;
  lightboxIndex.value = (lightboxIndex.value + delta + n) % n;
}

function onLightboxKey(e: KeyboardEvent) {
  if (lightboxIndex.value === null) return;
  if (e.key === "Escape") closeLightbox();
  else if (e.key === "ArrowLeft") stepLightbox(-1);
  else if (e.key === "ArrowRight") stepLightbox(1);
}

/** 关联影片那一排的翻页。与灯的左右键同一套手感：一次滚一屏。 */
const relTrack = ref<HTMLElement | null>(null);

function scrollRel(dir: number) {
  const el = relTrack.value;
  if (!el) return;
  el.scrollBy({ left: dir * el.clientWidth * 0.9, behavior: "smooth" });
}

// 抽屉一直挂着（只是隐藏），所以监听器挂一次即可；没打开时它自己会 no-op。
window.addEventListener("keydown", onLightboxKey);

// —— 封面上的预览片播放 ——
const showPlayer = ref(false);
const playerURL = ref("");
const videoRef = ref<HTMLVideoElement | null>(null);
let hls: { destroy: () => void } | null = null;

const magnetCount = computed(() => detail.value?.magnets.length ?? 0);

async function load(refresh = false) {
  if (!props.movieId) return;
  const first = !detail.value || detail.value.id !== props.movieId;
  if (first) {
    loading.value = true;
    closePlayer();
    detail.value = null;
    tab.value = TAB_MAGNETS;
    reviews.value = [];
    reviewTotal.value = 0;
    reviewPage.value = 1;
    // 换了一部片：关联清单得重新点开才拉（relatedLoaded 归零，表头角标也跟着消失）。
    relatedLists.value = [];
    relatedLoaded.value = false;
    relatedError.value = "";
  } else {
    refreshing.value = true;
  }
  error.value = "";
  try {
    detail.value = await fetchJavMovie(props.movieId, refresh);
  } catch (err) {
    error.value = getApiErrorMessage(err, "影片详情加载失败");
    detail.value = null;
  } finally {
    loading.value = false;
    refreshing.value = false;
    schedulePushPoll();
  }
}

/**
 * 有磁链还在网盘下载时，每 30 秒回查一次状态。
 *
 * 「推送中 → 已推送」这一步是**服务端异步**完成的：离线任务轮询（60 秒一跳，
 * 见 offlinedownload 的 nativeRefreshLoop）发现下完了才发事件、才把推送记录
 * 翻成 pushed。抽屉自己不回查的话，那颗「推送中」会一直挂着 —— 用户以为卡住了，
 * 其实早就下完了。（这个模块已经栽过一次同类的坑：轮询没起来时，「已推送」永远
 * 不出现，卡片上一直是 0。）
 *
 * 只在抽屉开着、且真有在途磁链时才排下一跳：没有在途就一次都不发。
 * 直接改写 detail 而不是走 load()：load() 会翻 refreshing，那个标志是给
 * 「刷新磁链」按钮用的，轮询把它点着，按钮会莫名其妙地闪一下「刷新中…」。
 */
const PUSH_POLL_MS = 30_000;
let pushPollTimer: ReturnType<typeof setTimeout> | null = null;

function stopPushPoll() {
  if (pushPollTimer !== null) {
    clearTimeout(pushPollTimer);
    pushPollTimer = null;
  }
}

function schedulePushPoll() {
  stopPushPoll();
  if (!props.open || !props.movieId) return;
  if (!detail.value?.magnets.some((m) => m.pushing)) return;
  pushPollTimer = setTimeout(async () => {
    pushPollTimer = null;
    if (!props.open || !props.movieId) return;
    try {
      detail.value = await fetchJavMovie(props.movieId, false);
    } catch {
      // 轮询失败不弹提示：网盘那边本来就可能慢，下一跳还会再试。
    }
    schedulePushPoll();
  }, PUSH_POLL_MS);
}

async function loadReviews(page = 1) {
  if (!props.movieId) return;
  reviewsLoading.value = true;
  try {
    const res = await fetchJavReviews(props.movieId, page);
    reviews.value = res.items ?? [];
    reviewTotal.value = res.total ?? 0;
    reviewPage.value = page;
  } catch (err) {
    toast.error(getApiErrorMessage(err, "评论加载失败"));
    reviews.value = [];
    reviewTotal.value = 0;
  } finally {
    reviewsLoading.value = false;
  }
}

function onTabChange(key: string) {
  tab.value = key;
  // 评论按需加载：多数人点开详情只看磁链，为了一次点击先把评论拉回来不值。
  if (key === TAB_REVIEWS && reviews.value.length === 0 && !reviewsLoading.value) {
    void loadReviews(1);
  }
  // 关联清单同理，而且它是唯一一个**必须**点开才拉上游的（详情里没有它）。
  if (key === TAB_RELATED && !relatedLoaded.value && !relatedLoading.value) {
    void loadRelatedLists();
  }
}

/** 拉一次关联清单。成功后把 relatedLoaded 立起来，表头的数字才会出现。 */
async function loadRelatedLists() {
  if (!props.movieId) return;
  relatedLoading.value = true;
  relatedError.value = "";
  try {
    const res = await fetchJavRelatedLists(props.movieId);
    relatedLists.value = res.items ?? [];
    relatedLoaded.value = true;
  } catch (err) {
    // 这是用户主动点开的一档，失败了要说清楚 —— 静默成空列表会让人
    // 以为「这部片确实没有关联清单」。面板里也留一句，别只弹个 toast 就没了。
    relatedError.value = getApiErrorMessage(err, "关联清单加载失败");
    toast.error(relatedError.value);
  } finally {
    relatedLoading.value = false;
  }
}

/** 重新抓一次上游元数据。 */
async function reingest() {
  if (!props.movieId) return;
  refreshing.value = true;
  try {
    await ingestJavMovie(props.movieId);
    await load(true);
    toast.success("已重新获取");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "重新获取失败"));
  } finally {
    refreshing.value = false;
  }
}

/** 重新抓一次磁链。 */
async function refreshMagnets() {
  if (!props.movieId) return;
  refreshing.value = true;
  try {
    detail.value = await fetchJavMovie(props.movieId, true);
    toast.success("磁链已刷新");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "磁链刷新失败"));
  } finally {
    refreshing.value = false;
    schedulePushPoll();
  }
}

async function copyMagnet(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success("磁链已复制");
  } catch {
    toast.error("复制失败，请手动选择文本");
  }
}

/**
 * 推送一颗磁链。
 *
 * 有订阅上下文时走「按候选推送」，否则走订阅的「执行订阅」——
 * 手动推送必须挂在一条订阅上，因为推送目标是订阅的属性（哪个网盘、哪个目录）。
 */
/**
 * 点右上角那颗心。
 *
 * 只上报「点的是哪一部」——是否已订阅、取消还是新建，都由页面判定：
 * 订阅列表在它手里，创建订阅的弹窗也在它手里。
 * 名字优先用番号，弹窗和「已取消订阅《…》」的提示都用这一份。
 */
function toggleSubscribe() {
  const id = props.movieId;
  if (!id) return;
  emit("toggleSubscribe", {
    id,
    name: detail.value?.number || detail.value?.title || id,
  });
}

/**
 * 推**这一条**链接。不依赖订阅 —— 目标是「番号相关设置」里的默认推送目标，
 * 记录落在推送记录表的 subscription_id=0（手动推送）。
 *
 * 这里以前是「先建订阅，再让订阅自己挑一颗」：不但要求先有订阅，点了之后推的
 * 也压根不是用户点的那颗（函数末尾还留着一句 `void magnet`）。现在推的就是它。
 *
 * 磁链 tab 与「评论区分享」档共用这一个入口：后者还会出现 ed2k，服务端按链接
 * 自己的 scheme 判通道能力，前端不用分情况。
 *
 * 不改订阅状态：那由服务端在网盘下完之后统一算（有订阅的话会变成已完成）。
 */
async function pushMagnet(link: { uri: string; name: string; size_text: string }) {
  if (!link.uri) return;
  pushing.value = true;
  try {
    const res = await pushJavMagnet(props.movieId ?? "", {
      uri: link.uri,
      name: link.name,
      size_text: link.size_text,
    });
    toast[res.ok ? "success" : "error"](res.message || (res.ok ? "已提交" : "推送失败"));
    if (res.ok) {
      // load(false) 而不是 load(true)：要刷的是**推送状态**，而它是本地库里的东西
      // （服务端每次都现算，见 jav.Magnets）。refresh=true 会顺带往上游重抓一次
      // 详情（Detail → IngestMovie），点一次推送就白跑一趟 JAVDB。
      await load(false);
      emit("changed");
    }
  } catch (err) {
    toast.error(getApiErrorMessage(err, "推送失败"));
  } finally {
    pushing.value = false;
  }
}

/**
 * 点演员：按这个名字去搜。
 *
 * 服务端不做这件事 —— 搜索状态由页面持有（搜索框在 Tab 栏上），
 * 抽屉只负责把「点了谁」告诉它，免得两边各存一份关键词。
 */
/**
 * 点分享者名字：打开「他分享过的影片」弹窗。
 *
 * 列表从**本地已入库的评论**里聚合（上游没有按用户查的接口），
 * 弹窗里会写明这个覆盖范围。
 */
function openSharer(sh: JavCommentShare) {
  if (!sh.sharer_id) return;
  sharerUserId.value = sh.sharer_id;
  sharerName.value = sh.sharer;
  sharerOpen.value = true;
}

/** 从弹窗里点开某一部 → 换掉抽屉里的影片，并关掉弹窗。 */
function openMovieFromShares(id: string) {
  sharerOpen.value = false;
  emit("openMovie", id);
}

async function searchByActor(actor: JavActor) {
  if (!actor.name) return;
  emit("searchActor", actor.name);
}

/** 点标签：按这个标签筛影库。 */
function filterByTag(tag: string) {
  if (!tag) return;
  emit("filterTag", tag);
}

/**
 * 点关联清单：按这个清单名去搜。
 *
 * 只上报「点了哪条清单」—— 搜索状态（关键词、类型）由页面持有，
 * 与点演员那条路一样，免得两边各存一份。
 */
function searchByList(list: JavRelatedList) {
  if (!list.name) return;
  emit("searchList", { id: list.id, name: list.name });
}

watch(
  () => [props.open, props.movieId],
  ([open]) => {
    if (open && props.movieId) {
      void load(false);
    } else {
      closePlayer();
      // 抽屉关了就停轮询：在后台每 30 秒打一次本地库没有意义。
      stopPushPoll();
    }
  },
  { immediate: true },
);

onUnmounted(stopPushPoll);
</script>

<template>
  <!-- elevated：订阅页的「影片」子弹窗（演员/清单订阅点进去看逐部状态）里也能点卡片
       进详情，那个弹窗是 2500，不抬起来详情会正好藏在它背后。 -->
  <AdminSettingsDrawer
    elevated
    :open="open"
    title="影片详情"
    hide-foot
    @close="emit('close')"
    @cancel="emit('close')"
  >
    <div v-if="loading" class="jav-empty">加载中…</div>

    <div v-else-if="error && !detail" class="jav-empty">
      <div class="jav-empty__icon">⚠️</div>
      <div>{{ error }}</div>
      <AppButton variant="ghost" size="sm" style="margin-top: 12px" @click="reingest">重新获取</AppButton>
    </div>

    <div v-else-if="detail">
      <h1 class="jd-title">{{ detail.title || detail.origin_title || "（无标题）" }}</h1>

      <!-- 信息条：左=番号/角标/日期/时长/入库，右=重新获取/订阅 -->
      <div class="jd-meta">
        <div class="jd-meta__left">
          <span class="jd-num">{{ detail.number || detail.id }}</span>
          <span v-if="detail.can_play" class="jd-badge jd-badge--online">在线</span>
          <span v-if="detail.has_cn_sub" class="jd-badge jd-badge--sub">中字</span>
          <span v-if="detail.release_date" class="jd-meta__item">{{ detail.release_date }}</span>
          <span v-if="detail.duration" class="jd-meta__item">{{ detail.duration }}分钟</span>
          <span class="jd-lib" :class="{ 'jd-lib--missing': !detail.in_library }">
            <i :class="detail.in_library ? 'fas fa-check' : 'fas fa-times'" />
            {{ detail.in_library ? "已入库" : "未入库" }}
          </span>
        </div>
        <div class="jd-meta__right">
          <button class="jd-btn" :disabled="refreshing" @click="reingest">
            <i class="fas fa-sync-alt" /> {{ refreshing ? "获取中…" : "重新获取" }}
          </button>
          <button
            class="jd-btn"
            :class="{ 'jd-btn--on': subscribed }"
            :title="subscribed ? '取消订阅' : '订阅'"
            @click="toggleSubscribe"
          >
            <i class="fas fa-heart" /> {{ subscribed ? "已订阅" : "订阅" }}
          </button>
        </div>
      </div>

      <!-- 两栏：封面 / 信息卡 + 类别卡 -->
      <div class="jd-cols">
        <div class="jd-col">
          <div class="jd-cover">
            <video v-if="showPlayer" ref="videoRef" controls playsinline />
            <img
              v-else-if="detail.cover"
              :src="javImageURL(detail.cover)"
              :alt="detail.number || detail.id"
            />
            <div v-else class="jav-card__placeholder">无封面</div>

            <button
              v-if="!showPlayer"
              class="jd-cover__play"
              :disabled="playerLoading"
              :title="playerLoading ? '正在获取预览片…' : '播放预览片'"
              @click="onPlay"
            >
              <!-- 取地址要走上游，慢的时候一秒多；转个圈比让按钮看起来没反应强。 -->
              <i :class="playerLoading ? 'fas fa-spinner fa-spin' : 'fas fa-play'" />
            </button>
            <button v-else class="jd-cover__close" title="关闭" @click="closePlayer">
              <i class="fas fa-times" />
            </button>
          </div>
        </div>

        <div class="jd-col">
          <div class="jd-card">
            <div class="jd-card__title">信息</div>
            <div class="jd-info">
              <div class="jd-info__row">
                <span class="jd-info__k">导演</span>
                <span class="jd-info__v">{{ detail.director_name || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">片商</span>
                <span class="jd-info__v">{{ detail.maker_name || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">发行商</span>
                <span class="jd-info__v">{{ detail.publisher_name || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">系列</span>
                <span class="jd-info__v">{{ detail.series_name || "—" }}</span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">众评</span>
                <span class="jd-info__v">
                  <span class="jd-stars jd-stars--green">
                    <i v-for="i in 5" :key="i" class="fas fa-star jd-star" :class="{ 'jd-star--on': i <= starCount }" />
                  </span>
                  <b class="jd-score">{{ (detail.score || 0).toFixed(2) }}</b>
                  <em class="jd-score__count">{{ detail.reviews_count || 0 }}人评分</em>
                </span>
              </div>
              <div class="jd-info__row">
                <span class="jd-info__k">评分</span>
                <span class="jd-info__v">
                  <span class="jd-stars jd-stars--gray">
                    <i v-for="i in 5" :key="i" class="fas fa-star jd-star" :class="{ 'jd-star--on': i <= starCount }" />
                  </span>
                  <b class="jd-score">{{ (detail.score || 0).toFixed(2) }}</b>
                </span>
              </div>
            </div>
          </div>

          <div class="jd-card">
            <div class="jd-card__title"><i class="fas fa-folder" /> 类别</div>
            <div v-if="detail.tags.length" class="jd-tags">
              <button
                v-for="t in detail.tags"
                :key="t"
                type="button"
                class="jd-tag"
                :title="`查看影库中含「${t}」标签的影片`"
                @click="filterByTag(t)"
              >
                {{ t }}
              </button>
            </div>
            <div v-else class="jd-empty-hint">暂无标签。</div>
          </div>
        </div>
      </div>

      <!-- 演员：通栏，在卡片下方 -->
      <div class="jd-card" style="margin-top: 18px">
        <div class="jd-card__title">
          <i class="fas fa-user" /> 演员
          <span v-if="detail.actors.length">({{ detail.actors.length }})</span>
        </div>
        <div v-if="detail.actors.length" class="jd-actors">
          <button
            v-for="a in detail.actors"
            :key="a.id"
            type="button"
            class="jd-actor"
            :title="`搜索「${a.name}」的影片`"
            @click="searchByActor(a)"
          >
            <span class="jd-actor__avatar">
              <img
                v-if="a.avatar_url"
                :src="javImageURL(a.avatar_url)"
                loading="lazy"
                :alt="a.name"
              />
              <template v-else>{{ a.name.slice(0, 1) }}</template>
            </span>
            <span>{{ a.name }}</span>
          </button>
        </div>
        <div v-else class="jd-empty-hint">暂无演员。</div>
      </div>

      <div v-if="detail.summary" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title"><i class="fas fa-circle-info" /> 简介</div>
        <div class="jd-summary">{{ detail.summary }}</div>
      </div>

      <div v-if="detail.preview_images.length" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title">
          <i class="fas fa-image" /> 预览图
          <span class="jd-count">{{ detail.preview_images.length }}</span>
        </div>
        <div class="jd-previews">
          <img
            v-for="(src, i) in detail.preview_images"
            :key="i"
            :src="javImageURL(src)"
            loading="lazy"
            alt="预览图"
            @click="openLightbox(i)"
          />
        </div>
      </div>

      <!-- 关联影片：一排竖向小卡，点开就是那一部的详情。 -->
      <div v-if="detail.relative_movies.length" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title">
          <i class="fas fa-film" /> 关联影片
          <span class="jd-count">{{ detail.relative_movies.length }}</span>
        </div>
        <div class="jd-rel-wrap">
          <button class="jd-rel__nav jd-rel__nav--prev" title="上一组" @click="scrollRel(-1)">‹</button>
          <div ref="relTrack" class="jd-rel">
            <div
              v-for="r in detail.relative_movies"
              :key="r.id"
              class="jd-rel__card"
              :title="r.number"
              @click="emit('openMovie', r.id)"
            >
              <img v-if="r.thumb" :src="javImageURL(r.thumb)" loading="lazy" :alt="r.number" />
              <div v-else class="jav-card__placeholder">无封面</div>
              <span class="jd-rel__flag" :class="{ 'jd-rel__flag--missing': !r.in_library }">
                {{ r.in_library ? "已入库" : "未入库" }}
              </span>
              <span class="jd-rel__num">{{ r.number }}</span>
            </div>
          </div>
          <button class="jd-rel__nav jd-rel__nav--next" title="下一组" @click="scrollRel(1)">›</button>
        </div>
      </div>

      <!-- 磁链 / 评论等分档 -->
      <div class="jd-card" style="margin-top: 14px">
        <SectionTabBar :model-value="tab" :tabs="TABS" @update:model-value="onTabChange" />

        <div v-show="tab === TAB_MAGNETS">
          <div style="display: flex; align-items: center; margin-bottom: 10px">
            <span class="jd-empty-hint">共 {{ magnetCount }} 颗</span>
            <button class="jd-btn" style="margin-left: auto" :disabled="refreshing" @click="refreshMagnets">
              <i class="fas fa-sync-alt" /> {{ refreshing ? "刷新中…" : "刷新磁链" }}
            </button>
          </div>

          <div v-if="magnetCount === 0" class="jd-empty-hint">这部影片还没有抓到磁链。</div>

          <div v-for="mg in detail.magnets" :key="mg.btih || mg.magnet" class="jav-magnet">
            <div class="jav-magnet__top">
              <span class="jav-magnet__size">{{ mg.size_text || "—" }}</span>
              <span class="jav-magnet__date">{{ mg.date }}</span>
            </div>
            <div class="jav-magnet__name">{{ mg.name }}</div>
            <div class="jav-magnet__foot">
              <!-- 清晰度只挂一颗：4K > UHD > HD。优先级和判定都在服务端
                   （quality.Tags.ResolutionBadge，名字认不出时用体积兜底），
                   这里只负责画 —— 免得同一套规则在两边各写一遍。 -->
              <span v-if="mg.resolution_badge" class="jav-badge jav-badge--res">
                {{ mg.resolution_badge }}
              </span>
              <span v-if="mg.uncensored" class="jav-badge jav-badge--break">破解</span>
              <span v-if="mg.subtitle" class="jav-badge jav-badge--on">字幕</span>
              <span v-if="mg.edited" class="jav-badge">精剪</span>
              <span v-if="mg.source_label" class="jav-badge">{{ mg.source_label }}</span>
              <span v-if="mg.codec_label" class="jav-badge">{{ mg.codec_label }}</span>
              <span v-if="mg.tracker_count" class="jav-badge">{{ mg.tracker_count }} 个 tracker</span>
              <!-- 两颗角标都是**这一颗**的状态，且由服务端算好（逐颗对资源指纹，
                   见 jav.Magnets）。「已推送」只在那一颗真的下完时才出现；
                   中间那段还在下的窗口挂「推送中」—— 少了它，刚点完推送的人
                   看到角标没变，只会以为没推成功。 -->
              <span v-if="mg.pushed" class="jav-badge jav-badge--on">已推送</span>
              <span v-else-if="mg.pushing" class="jav-badge">推送中</span>

              <div class="jav-magnet__actions">
                <button class="jd-btn" @click="copyMagnet(mg.magnet)"><i class="fas fa-copy" /> 复制</button>
                <button
                  class="jd-btn"
                  :disabled="pushing"
                  @click="pushMagnet({ uri: mg.magnet, name: mg.name, size_text: mg.size_text })"
                >
                  <i class="fas fa-paper-plane" /> 推送
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- 评论区分享：用户在自己评论里贴出来的链接。卡片与磁链 tab **逐格同构**
             （同一个 .jav-magnet 骨架、同一套角标），只多一行「分享者 + 原评论」——
             两个 tab 摆的是同一种东西，样式不该长得不一样。 -->
        <div v-show="tab === TAB_COMMENTS">
          <div v-if="!detail.comment_shares.length" class="jd-empty-hint">
            评论区暂无用户分享的磁链 / ED2K 链接。
          </div>
          <template v-else>
            <p class="jd-empty-hint" style="margin-bottom: 10px">
              以下为评论区用户分享的磁链 / ED2K 链接，附分享者与原评论。
              <br />
              这些链接是否参与**自动推送**，由订阅上的「评论区链接」开关决定。
            </p>
            <div
              v-for="sh in detail.comment_shares"
              :key="sh.uri"
              class="jav-magnet"
            >
              <div class="jav-magnet__top">
                <span class="jav-magnet__size">{{ sh.size_text || "—" }}</span>
                <span class="jav-magnet__kind">{{ sh.kind === "ed2k" ? "ED2K" : "磁链" }}</span>
                <span class="jav-magnet__date">{{ sh.date }}</span>
              </div>
              <div class="jav-magnet__name">{{ sh.name }}</div>
              <div class="jav-share">
                <i class="fas fa-user" />
                <!-- 点名字看这个人分享过的影片。用 <button> 而不是带 @click 的
                     span：键盘 Tab 能走到、回车能触发。匿名（没有 sharer_id）
                     没有身份可查，就不给点。 -->
                <button
                  v-if="sh.sharer && sh.sharer_id"
                  type="button"
                  class="jav-share__who jav-share__who--link"
                  :title="`看「${sh.sharer}」分享过的影片`"
                  @click="openSharer(sh)"
                >
                  {{ sh.sharer }}
                </button>
                <span v-else-if="sh.sharer" class="jav-share__who">{{ sh.sharer }}</span>
                <span v-else class="jav-share__who jav-share__who--anon">匿名</span>
                <span v-if="sh.comment" class="jav-share__comment" :title="sh.comment">
                  {{ sh.comment }}
                </span>
              </div>
              <div class="jav-magnet__foot">
                <span v-if="sh.resolution_badge" class="jav-badge jav-badge--res">
                  {{ sh.resolution_badge }}
                </span>
                <span v-if="sh.uncensored" class="jav-badge jav-badge--break">破解</span>
                <span v-if="sh.subtitle" class="jav-badge jav-badge--on">字幕</span>

                <div class="jav-magnet__actions">
                  <button class="jd-btn" @click="copyMagnet(sh.uri)">
                    <i class="fas fa-copy" /> 复制
                  </button>
                  <button
                    class="jd-btn"
                    :disabled="pushing"
                    @click="pushMagnet({ uri: sh.uri, name: sh.name, size_text: sh.size_text })"
                  >
                    <i class="fas fa-paper-plane" /> 推送
                  </button>
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- 关联清单：含这部影片的片单。数据源是上游的 /v1/lists/related，
             点开这一档才拉（见 onTabChange）。 -->
        <div v-show="tab === TAB_RELATED">
          <div v-if="relatedLoading" class="jd-empty-hint">加载中…</div>
          <div v-else-if="relatedError" class="jd-empty-hint">
            <div>{{ relatedError }}</div>
            <AppButton variant="ghost" size="sm" style="margin-top: 12px" @click="loadRelatedLists">
              重试
            </AppButton>
          </div>
          <div v-else-if="!relatedLists.length" class="jd-empty-hint">暂无关联清单。</div>
          <div v-else class="jav-related">
            <!-- 照 23.png 的版式：清单名在上、部数+箭头在下的居中方卡，网格排布。
                 仍然用 <button> 而不是带 @click 的 <div>：键盘 Tab 能走到、
                 回车能触发，这些一个 div 全没有。样式在 jav.css 里重置。 -->
            <button
              v-for="l in relatedLists"
              :key="l.id"
              type="button"
              class="jav-related__card"
              :title="`查看清单「${l.name}」里的影片`"
              @click="searchByList(l)"
            >
              <span class="jav-related__name">{{ l.name }}</span>
              <span class="jav-related__count">
                {{ l.movies_count || 0 }} 部影片
                <i class="fas fa-arrow-right" />
              </span>
            </button>
          </div>
        </div>

        <div v-show="tab === TAB_REVIEWS">
          <div v-if="reviewsLoading" class="jd-empty-hint">加载中…</div>
          <div v-else-if="reviews.length === 0" class="jd-empty-hint">还没有评论。</div>
          <template v-else>
            <div v-for="r in reviews" :key="r.id" class="jav-magnet">
              <div class="jav-magnet__top">
                <strong style="font-size: 12.5px">{{ r.username || "匿名" }}</strong>
                <span v-if="r.score" class="jav-chip jav-chip--brand">★ {{ r.score }}</span>
                <span v-if="r.status_title" class="jav-badge">{{ r.status_title }}</span>
                <span class="jav-magnet__date">{{ r.likes_count }} 赞</span>
              </div>
              <div style="font-size: 12.5px; line-height: 1.6; color: var(--text-regular)">{{ r.content }}</div>
            </div>
            <div v-if="reviewTotal > reviews.length" style="text-align: center; margin-top: 10px">
              <button class="jd-btn" @click="loadReviews(reviewPage + 1)">加载更多</button>
            </div>
          </template>
        </div>
      </div>
    </div>

    <!-- 分享者分享过的影片。独立组件：订阅页「用户」那一档点用户卡也是它。 -->
    <JavUserSharesModal
      :open="sharerOpen"
      :user-id="sharerUserId"
      :username="sharerName"
      @close="sharerOpen = false"
      @open-movie="openMovieFromShares"
    />

    <!-- 预览图全屏查看器：毛玻璃背景，右上关闭、左右翻页。 -->
    <Teleport to="body">
      <div
        v-if="lightboxIndex !== null"
        class="jd-lightbox"
        :class="{ 'jd-lightbox--single': lightboxImages.length <= 1 }"
        @click.self="closeLightbox"
      >
        <button class="jd-lightbox__close" title="关闭" @click="closeLightbox">✕</button>
        <button class="jd-lightbox__arrow jd-lightbox__arrow--prev" title="上一张" @click.stop="stepLightbox(-1)">‹</button>
        <img
          class="jd-lightbox__img"
          :src="javImageURL(lightboxImages[lightboxIndex])"
          alt="预览图"
        />
        <button class="jd-lightbox__arrow jd-lightbox__arrow--next" title="下一张" @click.stop="stepLightbox(1)">›</button>
      </div>
    </Teleport>

  </AdminSettingsDrawer>
</template>

