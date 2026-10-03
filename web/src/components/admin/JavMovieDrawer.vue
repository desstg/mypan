<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import JavUserSharesModal from "@/components/admin/JavUserSharesModal.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavMagnets,
  fetchJavMovie,
  fetchJavPreviewURL,
  fetchJavRelatedLists,
  fetchJavReviews,
  javImageURL,
  pushJavMagnet,
  refreshJavMovie,
  refreshJavMovieMagnets,
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
  {
    key: TAB_MAGNETS,
    label: "磁力链接",
    // 还没拉到时给 `undefined`（不渲染数字）而不是 0 —— 「0」会让用户以为
    // 这片没有磁链，而其实后台正在抓（见 magnetsPending）。
    count: magnetsLoaded.value ? (detail.value?.magnets.length ?? 0) : undefined,
  },
  {
    key: TAB_COMMENTS,
    label: "评论区分享",
    // 同磁链那一档：还没拉到时不给数字（显示 0 会让人以为没人分享过）。
    count: sharesLoaded.value ? detail.value?.comment_shares.length ?? 0 : undefined,
  },
  { key: TAB_RELATED, label: "关联清单", count: relatedLoaded.value ? relatedLists.value.length : undefined },
  { key: TAB_REVIEWS, label: "评论", count: detail.value?.comments_count ?? 0 },
]);

/**
 * 简介是不是「还在补」。
 *
 * 判据 = 轮询还在跑（说明还有空缺）且这一部确实没有简介。轮询到顶或停了之后，
 * 就不再显示「加载中…」—— 那时已经问过一圈，没有就是没有，别一直吊着用户。
 */
const summaryPending = computed(() => !detail.value?.summary && pollingActive.value);

/** 轮询是否在跑（模板据此显示「加载中…」而不是「暂无」）。 */
const pollingActive = ref(false);

/**
 * 磁链那一档的状态（2026-09-28 起独立拉）。
 *
 * 详情首屏这次不再顺带抓磁链（那是两个境外站，很慢），所以这一档要自己：
 *   - `magnetsPending`：本地没有、后台正在抓 —— 界面显示「获取中…」而不是「没有磁链」。
 *     这一点很重要：显示 0 颗会让用户以为这片没有磁链。
 *   - `magnetsLoaded`：拉过一次没有，之后显示「暂无磁链」才是诚实的。
 */
const magnetsPending = ref(false);
const magnetsLoaded = ref(false);

/**
 * 「评论区分享」那一档：与评论共用一份数据（分享就是从评论里提链接出来的），
 * 所以**切到那一档时顺带把评论拉一遍**就行（loadReviews 会把 shares 填回来）。
 *
 * `sharesLoaded` 用来区分「还没拉过」与「拉过了确实没有」——
 * 前者不能显示「暂无分享」，那会让人以为这片没人贴过链接。
 */
const sharesLoaded = ref(false);

/**
 * 用户切到「磁力链接」那一档时调一次：那一档他正看着，没拉到就该显示「获取中…」
 * 而不是「还没有抓到」。幂等 —— 服务端有去重与冷却，重复要不会白打上游。
 */
function ensureMagnetsOnTab() {
  if (tab.value !== TAB_MAGNETS) return;
  if (magnetsLoaded.value || magnetsPending.value) return;
  void loadMagnets();
}

/** 要一次分享（走评论那条接口，它会把 shares 一起带回来）。 */
async function ensureShares() {
  if (!props.movieId) return;
  // 已经有评论在手里时，直接算一遍本地分享（不必重打接口）。
  if (reviews.value.length > 0 && detail.value) {
    try {
      const res = await fetchJavReviews(props.movieId, reviewPage.value);
      if (detail.value && detail.value.id === props.movieId && res.shares) {
        detail.value.comment_shares = res.shares;
      }
      sharesLoaded.value = true;
      return;
    } catch {
      /* 落到下面再试一次完整加载 */
    }
  }
  await loadReviews(1);
}

/** 本地没有磁链时去后台要（幂等，重复调用会被服务端的去重/冷却挡住）。 */
async function loadMagnets() {
  if (!props.movieId) return;
  try {
    const res = await fetchJavMagnets(props.movieId, false, true);
    magnetsLoaded.value = true;
    magnetsPending.value = Boolean(res.pending);
    // 拉到了就直接换掉这一块（局部更新，不动整页）
    if (detail.value && detail.value.id === props.movieId && res.items.length) {
      detail.value.magnets = res.items;
    }
  } catch {
    /* 静默：下一跳再试 */
  }
}

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

/**
 * 拉一次详情。
 *
 * 两种模式（2026-09-28 改）：
 *   - `local=true`（默认）：**只读本地**，毫秒级。首屏用它 —— 先显示库里已有的，
 *     缺的（简介 / 中文标题 / 磁链 / 评论）留着，由后台补完再长出来。
 *     服务端在 local 模式下会顺手把这部排进补缺队列，所以这里不用额外催。
 *   - `local=false`：老的同步语义（该抓就抓，几秒），留给「重新获取」。
 *
 * ⚠️ **换片时不再清空 `detail`**：以前是 `loading=true` + `detail=null`，
 * 于是整页白屏等上游 —— 那正是「打开很慢、还闪一下」的来源。现在保留上一部的
 * 数据先渲染（标题/封面那几块先显示），本地数据毫秒级就到了，观感是「立刻打开」。
 */
async function load(refresh = false, local = true) {
  if (!props.movieId) return;
  const first = !detail.value || detail.value.id !== props.movieId;
  if (first) {
    closePlayer();
    tab.value = TAB_MAGNETS;
    reviews.value = [];
    reviewTotal.value = 0;
    reviewPage.value = 1;
    // 换了一部片：关联清单得重新点开才拉（relatedLoaded 归零，表头角标也跟着消失）。
    relatedLists.value = [];
    relatedLoaded.value = false;
    relatedError.value = "";
    // 换片时**清掉上一部那份**（不然会显示别的片的简介/磁链），但**不清成长白屏**：
    // 本地那次是毫秒级，`loading` 只会闪这么一下。
    //
    // 与改动前的区别就在这里：以前是 `loading=true` + `detail=null` **等上游几秒**，
    // 现在等的是本地库（毫秒级），体感上是「点开就开」。
    detail.value = null;
    loading.value = true;
    // 磁链那一档的状态跟着换片重置（上一部的「获取中」不该带到这一部）。
    magnetsPending.value = false;
    magnetsLoaded.value = false;
    sharesLoaded.value = false;
  } else {
    refreshing.value = true;
  }
  error.value = "";
  try {
    detail.value = await fetchJavMovie(props.movieId, refresh, local);
  } catch (err) {
    error.value = getApiErrorMessage(err, "影片详情加载失败");
    // 刷新失败**不清空已有内容**：显示旧数据远好过一个空白页 +
    // 「重新获取」按钮（那条错误提示走 error 那条分支，见模板）。
    if (first) detail.value = null;
  } finally {
    loading.value = false;
    refreshing.value = false;
    schedulePushPoll();
    // 首屏本地数据到手之后：磁链单独去要一次（本地没有就排后台），
    // 还缺别的就开轮询。
    if (detail.value) {
      if (detail.value.magnets.length > 0) {
        // 本地就有（之前抓过）—— 直接算「已拉到」，tab 头立刻显示数字。
        magnetsLoaded.value = true;
      } else {
        void loadMagnets();
      }
      // 「评论区分享」同理，而且判据更硬：服务端那份分享是拿**全部本地评论**
      // 现算的（commentSharesLocal 读 ListByMovieAll），而 comments_count 数的
      // 也是同一批 —— 所以本地只要有一条评论，分享就已经是完整的。
      //
      // 不置这一位的话，那一档会一直显示「正在获取…」：它的三态只看 sharesLoaded，
      // 而那个标志以前只有 loadReviews / ensureShares 会置真（都是**点开那一档
      // 之后**才跑的）。于是本地明明已经有链接，用户点进去却只看到转圈，还得再点
      // 一次「评论」tab 才显示出来（loadReviews 顺手把它置真了）。
      // 本地没评论时保持 false 是对的：那说明确实还没抓过，得去要一次。
      if (detail.value.comments_count > 0) {
        sharesLoaded.value = true;
      }
    }
    // 首屏之后**至少还轮一跳**（见 pollMustTickOnce）：首屏那份可能是后台写入
    // 之前的快照，没有再拉一次的话导演/片商/评分/标签会一直停在旧值。
    if (first) {
      pollMustTickOnce.value = true;
      // 换了一部片：上一部「用户点过重新获取」不能把完成提示带到这一部
      //（这一部是自动刷的，全程静默）。
      userAskedRefresh = false;
    }
    syncDetailPoll();
  }
}

/**
 * 详情还在「缺东西」时，隔几秒拉一次本地详情 —— 后台补好了哪块，哪块自己长出来。
 *
 * 判据是「抽屉开着 + 还有空缺」，齐了就停（不做无限轮询）。
 * 直接给 `detail.value` 赋新值、**不碰 `loading`/`refreshing`**（照下面
 * schedulePushPoll 那套写法）—— 那两个标志翻了，界面会闪。
 */
const DETAIL_POLL_MS = 3000;
// 用 number（`window.setTimeout` 的返回类型）而不是 `ReturnType<typeof setTimeout>`：
// 这个项目里同时装了 @types/node，全局那个 setTimeout 返回的是 NodeJS.Timeout。
let detailPollTimer: number | null = null;

/**
 * 首屏之后**至少**还轮一次。
 *
 * 这是「第一跳总是要打」的那道闸门，解决一个实测到的竞态（2026-09-29）：
 * 首屏那次本地读可能拿到「后台写入**之前**」的快照（服务端刚被前一次操作触发过一轮
 * 写入），于是导演/片商/评分/标签那几格停在旧值 —— 用户看到「演员、关联影片都有，
 * 基本信息全是 —」，点一下「重新获取」才出来。
 *
 * 为什么不用「字段空着就继续轮」来兜：FC2 / 素人那类片**上游本来就没有**导演、片商、
 * 系列、标签，那些判据永远为真，每打开一次要白轮 20 跳（60 秒）才停，观感上就是
 * 「一直在转圈」。改成固定一跳之后，那类片只多一次本机读（毫秒级）。
 */
const pollMustTickOnce = ref(false);

function detailStillMissing(): boolean {
  const d = detail.value;
  if (!d) return false;
  // 还没拉过、且本地没有 → 还得等（后台在抓）
  if (!magnetsLoaded.value && d.magnets.length === 0) return true;
  // 这几项都补到了就停：简介、中文标题、评论。
  // （关联影片只在 raw_json 里有，走的是「一次抓全」那条，不单独轮询。）
  return !d.summary || !d.title_zh || d.comments_count === 0;
}

/**
 * 「重新获取」在跑时，轮询要等的是**服务端那个任务**，不是「本地还缺哪几个字段」——
 * 所以判据换成服务端回的 `refresh_state === "running"`。
 *
 * 为什么单列一位而不是并进 detailStillMissing：那一堆判据会随着补缺陆续满足而
 * 自动停（这正是它们的作用），而这一位**只有任务真跑完才该停**；混在一起会出现
 * 「字段先齐了就提前停轮询、任务结果永远看不到」。
 */
const refreshPending = ref(false);

/**
 * 这次任务是不是**用户点了按钮**发起的。
 *
 * 只有它决定「跑完要不要弹一句」：打开详情自动做的那次（autoRefresh）全程静默。
 * 不用 ref：它只被轮询回调读，不参与渲染。
 */
let userAskedRefresh = false;

/** 轮询上限（次）。缺的东西（比如这片本来就没有磁链）补不上时不能无限轮下去。 */
const DETAIL_POLL_MAX = 20;
/** 「重新获取」在跑时的轮询上限：它可能跑几分钟（服务端预算见 jav.refreshBudget）。 */
const REFRESH_POLL_MAX = 100;

function currentPollMax(): number {
  return refreshPending.value ? REFRESH_POLL_MAX : DETAIL_POLL_MAX;
}

function syncDetailPoll(round = 0) {
  if (detailPollTimer !== null) {
    clearTimeout(detailPollTimer);
    detailPollTimer = null;
  }
  // mustTickOnce 让「首屏之后至少再拉一跳」成立 —— 见 pollMustTickOnce 的说明。
  const wantPoll =
    detailStillMissing() || refreshPending.value || (pollMustTickOnce.value && round === 0);
  if (!props.open || !props.movieId || !wantPoll) {
    pollingActive.value = false;
    return;
  }
  if (round >= currentPollMax()) {
    // 到顶就停：已经问过一圈，缺的就是真缺（比如这片本来没有磁链），
    // 别再显示「加载中…」吊着用户。
    //
    // 但**再读一次本地**：最后一跳有可能正好落在后台写入之前，那样界面上会永远停在
    // 那份旧快照上（用户看到的就是「明明有数据、格子却是空的」）。这一次是本地读，
    // 毫秒级，代价可以忽略。
    pollingActive.value = false;
    pollMustTickOnce.value = false;
    void fetchJavMovie(props.movieId, false, true)
      .then((last) => {
        if (props.open && last.id === detail.value?.id) detail.value = last;
      })
      .catch(() => {
        /* 失败就算了：已经轮过一整轮，不打扰用户 */
      });
    return;
  }
  pollingActive.value = true;
  detailPollTimer = window.setTimeout(async () => {
    detailPollTimer = null;
    if (!props.open || !props.movieId) return;
    try {
      // 磁链是**独立那一档**（首屏不带它），所以这里一起拉一下 ——
      // 本地还没有的话服务端已经把它排进后台高优先级队列了。
      if (detail.value && detail.value.magnets.length === 0) {
        void loadMagnets();
      }
      const next = await fetchJavMovie(props.movieId, false, true);
      const wasPending = refreshPending.value;
      // 只在还是同一部时才写回（用户可能已经翻到别的片了）。
      if (next.id === detail.value?.id) detail.value = next;
      refreshPending.value = next.refresh_state === "running";
      // **完成边沿**：在跑 → 不跑了。这是本项目检测「后台任务收工」的通用套路
      // （见 StrmScrapePanel 的 wasRunning && !p.running）。
      if (wasPending && !refreshPending.value) onRefreshFinished(next);
    } catch {
      /* 轮询失败静默：下一跳再试，不打扰用户 */
    }
    // 这一跳已经打过了 —— 「首屏之后至少一跳」到此兑现。
    pollMustTickOnce.value = false;
    schedulePushPoll();
    syncDetailPoll(round + 1);
  }, DETAIL_POLL_MS);
}

/**
 * 「重新获取」跑完那一跳。
 *
 * 轮询期间**全程安静**（本项目规矩：轮询不弹提示），只在这里弹一次结果 ——
 * 而且**只在用户点过那颗按钮时**才弹：打开详情自动做的那一次（autoRefresh）
 * 也是同一个任务，但它不该给用户任何提示，该长出来的自己长出来就行了。
 *
 * 顺带把依赖的几块对齐：磁链与评论都被那条任务换过一遍，不能拿旧的那份继续用。
 */
function onRefreshFinished(next: JavMovieDetail) {
  if (userAskedRefresh) {
    userAskedRefresh = false;
    if (next.refresh_state === "failed") {
      toast.error(next.refresh_error || "重新获取失败");
    } else {
      toast.success("已重新获取");
    }
  }
  if (next.magnets.length) magnetsLoaded.value = true;
  if (next.comments_count > 0) sharesLoaded.value = true;
  // 正在看的那两档：内容换过了，重新拉一次（不在看就不管，切过去时会自己拉）。
  if (tab.value === TAB_REVIEWS && reviews.value.length > 0) void loadReviews(1);
  else if (tab.value === TAB_COMMENTS) void ensureShares();
  emit("changed");
}

function stopDetailPoll() {
  pollingActive.value = false;
  if (detailPollTimer !== null) {
    clearTimeout(detailPollTimer);
    detailPollTimer = null;
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
      // ⚠️ 必须走 `local=1`（第三个参数）：不带它就是**同步那条路**
      // （`Service.Detail`，可能跑 IngestMovie + 磁链 + 评论）。这是一条
      // **每 30 秒一跳**的轮询，拿同步路去打上游会一直打 —— 而它要的只是
      // 推送状态，那是本地表里的数据（服务端每次现算）。
      detail.value = await fetchJavMovie(props.movieId, false, true);
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
    // **这一跳顺带把「评论区分享」也补上**：分享就是从评论正文里提链接出来的，
    // 同一个接口已经算好了。详情首屏瘦身之后，那一档没别的地方拿分享。
    if (detail.value && detail.value.id === props.movieId && res.shares) {
      detail.value.comment_shares = res.shares;
      sharesLoaded.value = true;
    }
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
  // 磁链那一档：详情首屏这次不再顺带抓它（两个境外站，很慢），所以切过来时确认一下 ——
  // 没拉到就去要（服务端会排进后台高优先级队列），界面显示「获取中…」而不是「没有」。
  ensureMagnetsOnTab();
  // 评论区分享：分享是从评论正文里提出来的，所以拉评论就等于拉分享
  // （loadReviews 会把 shares 一起填回来）。
  if (key === TAB_COMMENTS && !sharesLoaded.value && !reviewsLoading.value) {
    void ensureShares();
  }
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
/**
 * 重新获取：**排一个后台任务，立刻返回**。
 *
 * 以前是同步等两件重活（POST /ingest + GET ?refresh=1），而后者内部又跑一遍
 * 整条补缺链 —— 实测一个请求 24~126 秒，中间任何一层（反代 / 浏览器侧代理 /
 * 网关）的读超时都会把它切掉，用户看到的是「请求失败 (502)」。
 *
 * 现在：POST 立刻回来 → 服务端的任务在后台跑 → 轮询看 `refresh_state`，
 * 跑完在 onRefreshFinished 里弹一次结果。**这样不管它跑多久都不会被切。**
 */
async function reingest() {
  if (!props.movieId || refreshPending.value) return;
  // 先乐观置位：POST 到下一次轮询之间按钮不该闪回「重新获取」。
  refreshPending.value = true;
  userAskedRefresh = true;
  try {
    const res = await refreshJavMovie(props.movieId);
    // 服务端说没入队 = 已经在跑了（去重），照样算在跑；以轮询看到的状态为准。
    refreshPending.value = res.queued || res.state === "running";
  } catch (err) {
    refreshPending.value = false;
    userAskedRefresh = false;
    toast.error(getApiErrorMessage(err, "重新获取失败"));
    return;
  }
  syncDetailPoll();
}

/**
 * 打开详情时**自动**做的那一次重新获取。
 *
 * 与用户点按钮调的是**同一个后端任务**（`POST /movies/{id}/refresh`：重取 JAVDB 详情
 * + 磁链 + 评论），差别只有一条：**全程静默**。
 *
 * 不弹「正在获取…」、不弹结果、按钮也不进「获取中…」—— 用户看到的就是
 * 「打开就是本地这些，过几秒该长出来的自己长出来了」。
 *
 * 为什么不弹：他不是来等这个的。多一个进度提示只会让他以为「要等它」，
 * 而这一趟跑多久都不影响他继续看（轮询每 3 秒一跳，本机读，很便宜）。
 *
 * 失败也不弹：打开详情顺手做的补全失败了，下一轮后台回填还在，没必要打扰他。
 */
async function autoRefresh() {
  if (!props.movieId) return;
  try {
    const res = await refreshJavMovie(props.movieId);
    // 没入队 = 已经在跑了（服务端去重），照样等着。
    refreshPending.value = res.queued || res.state === "running";
  } catch {
    /* 静默：这是顺手做的事，失败就等下一轮后台回填 */
    return;
  }
  syncDetailPoll();
}

/**
 * 重新抓一次磁链：**同样排后台任务**（那两个站很慢，理由与 reingest 一样）。
 *
 * 以前这里调的是 `fetchJavMovie(id, true)` —— 为了刷磁链把**整条补缺链**重跑一遍
 * （`?refresh=1` 内部会再跑一次 IngestMovie）。现在走磁链自己的端点：
 * 一次 JAVDB + 一次 JAVBUS，后台跑，前端看 `magnetsPending`（服务端把「磁链刷新在途」
 * 也并进了那个 pending 标记）。
 */
async function refreshMagnets() {
  if (!props.movieId || refreshing.value) return;
  refreshing.value = true;
  try {
    const res = await refreshJavMovieMagnets(props.movieId);
    magnetsPending.value = res.queued || res.state === "running";
    if (!magnetsPending.value) await loadMagnets();
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
async function pushMagnet(link: {
  uri: string;
  name: string;
  size_text: string;
  /** "comment" 表示这颗来自「评论区分享」档，服务端据此标来源。 */
  source?: string;
}) {
  if (!link.uri) return;
  pushing.value = true;
  try {
    const res = await pushJavMagnet(props.movieId ?? "", {
      uri: link.uri,
      name: link.name,
      size_text: link.size_text,
      source: link.source,
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
      // 首屏走**本地模式**：毫秒级返回，先把本地有的显示出来。
      void load(false, true);
      // 同时**自动做一次「重新获取」** —— 和用户点那颗按钮完全同一件事
      // （同一个后台任务：重取 JAVDB 详情 + 磁链 + 评论），只是不需要他点。
      //
      // **这是「用户打开了这部片」唯一的点火口**：服务端那条 `?local=1` 以前也会
      // 顺手排一次补缺，两边一起排会导致同一部片打两遍详情，已经从服务端去掉了。
      //
      // **静默**：不弹「正在获取…」、不弹结果、按钮也不变。用户看到的就是
      // 「打开就是这些，过几秒该长出来的自己长出来了」。
      void autoRefresh();
    } else {
      closePlayer();
      // 抽屉关了就停轮询：在后台每 30 秒打一次本地库没有意义。
      stopPushPoll();
      stopDetailPoll();
    }
  },
  { immediate: true },
);

onUnmounted(() => {
  stopPushPoll();
  stopDetailPoll();
});
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
      <AppButton
        variant="ghost"
        size="sm"
        style="margin-top: 12px"
        :disabled="refreshPending"
        @click="reingest"
      >
        {{ refreshPending ? "获取中…" : "重新获取" }}
      </AppButton>
    </div>

    <div v-else-if="detail">
      <!-- 标题取**中文优先、没有才回落**（用户要求）：别站补来的中文标题在 title_zh
           （JAVDB 那行大面积是日文），两栏各有各的语义，服务端不替我们选。 -->
      <h1 class="jd-title">
        {{ detail.title_zh || detail.title || detail.origin_title || "（无标题）" }}
      </h1>

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
          <!-- 禁用绑 **refreshPending** 而不是 refreshing：后者与「刷新磁链」共用，
               轮询把它点着会让这颗按钮莫名闪一下（那段注释就是这么写的）。 -->
          <button class="jd-btn" :disabled="refreshPending" @click="reingest">
            <i :class="refreshPending ? 'fas fa-spinner fa-spin' : 'fas fa-sync-alt'" />
            {{ refreshPending ? "获取中…" : "重新获取" }}
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
            <MediaImage v-else :src="javImageURL(detail.cover)" :alt="detail.number || detail.id" />

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
              <!-- 头像挂了也落到占位图（人形那个 variant）。 -->
              <MediaImage :src="javImageURL(a.avatar_url)" :alt="a.name" variant="person" />
            </span>
            <span>{{ a.name }}</span>
            <!-- 男优加一枚标记：列表按上游顺序排（女演员在前、男优在后），
                 没有标记的话满屏人名看不出谁是谁。女演员不加，保持列表干净。 -->
            <span v-if="a.gender === 1" class="jd-actor__gender">男</span>
          </button>
        </div>
        <div v-else class="jd-empty-hint">暂无演员。</div>
      </div>

      <!-- 剧情简介：在演员与预览图之间。
           表头用「剧情简介」与墙上编辑页（StrmJavMetaDrawer）的那一块同名，同一份东西两处别叫两个名字。
           没简介时**也显示这一块**并给一句空提示 —— 与上面的「演员」「标签」同一套观感。
           原来这里是 `v-if="detail.summary"`，空简介整块就没了：用户看到「别人有、自己没有」
           只会以为详情抓漏了，而不是知道「这部确实没有」。 -->
      <div class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title"><i class="fas fa-circle-info" /> 剧情简介</div>
        <!-- 三态：有就显示；还在补（后台刚跑完这部的补缺链之前）显示「加载中…」；
             补过之后确认没有，才说「暂无简介」。第二态是这次改动加的 —— 用户点开
             看到的是「正在长出来」而不是「这部没有简介」。 -->
        <div v-if="detail.summary" class="jd-summary">{{ detail.summary }}</div>
        <div v-else-if="summaryPending" class="jd-summary jd-summary--pending">加载中…</div>
        <div v-else class="jd-empty-hint">暂无简介。</div>
      </div>

      <div v-if="detail.preview_images.length" class="jd-card" style="margin-top: 14px">
        <div class="jd-card__title">
          <i class="fas fa-image" /> 预览图
          <span class="jd-count">{{ detail.preview_images.length }}</span>
        </div>
        <div class="jd-previews">
          <MediaImage
            v-for="(src, i) in detail.preview_images"
            :key="i"
            :src="javImageURL(src)"
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
              <MediaImage :src="javImageURL(r.thumb)" :alt="r.number" />
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

          <!-- 三态很重要：本地为空**不代表没有磁链** —— 后台可能正在抓。
               显示「0 颗 / 还没有抓到」会让用户以为这片没有资源，而其实只是还在路上。 -->
          <div v-if="magnetCount === 0 && magnetsPending" class="jd-empty-hint">
            <i class="fas fa-spinner fa-spin" /> 正在获取磁链…（本地还没有，已在后台抓取）
          </div>
          <div v-else-if="magnetCount === 0" class="jd-empty-hint">这部影片还没有抓到磁链。</div>

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
          <!-- 三态：拉到之前不能显示"暂无"——那会让人以为这片没人贴过链接，
               而其实只是还没去抓（评论抓取有 45 秒预算，是后台在跑）。 -->
          <div v-if="!sharesLoaded" class="jd-empty-hint">
            <i class="fas fa-spinner fa-spin" /> 正在获取评论区的分享…（需要先抓一遍评论）
          </div>
          <div v-else-if="!detail.comment_shares.length" class="jd-empty-hint">
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
                    @click="
                      pushMagnet({
                        uri: sh.uri,
                        name: sh.name,
                        size_text: sh.size_text,
                        source: 'comment',
                      })
                    "
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

