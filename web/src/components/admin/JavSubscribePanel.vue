<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppPagination from "@/components/base/AppPagination.vue";
import JavMovieCard from "@/components/admin/JavMovieCard.vue";
import JavUserSharesModal from "@/components/admin/JavUserSharesModal.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  addJavBlacklist,
  autoPushJavSubscription,
  checkJavSubscription,
  createJavSubscription,
  deleteJavBlacklist,
  deleteJavSubscription,
  fetchJavBlacklist,
  fetchJavBlacklistMovies,
  fetchJavCandidates,
  fetchJavCompletedMovies,
  fetchJavFollowedUsers,
  fetchJavSubscriptionMovies,
  fetchJavSubscriptions,
  javImageURL,
  pushJavCandidate,
  setJavMovieSkip,
  setJavSubscriptionStatus,
  subscribeJavMovie,
  updateJavSubscription,
} from "@/api/jav";
import { useAccountsStore } from "@/stores/accounts";
import { toast } from "@/composables/useToast";
import type { DropdownMenuItem } from "@/types/menu";
import type {
  JavBlacklistEntry,
  JavCompletedMovie,
  JavFollowedUser,
  JavCandidate,
  // 与上面那个组件同名，按项目惯例用别名（JavLibraryPanel / JavRankPanel 同）。
  JavMovieCard as JavCard,
  JavSubscription,
  JavSubMovie,
} from "@/types/jav";
import {
  JAV_DOWNLOAD_MODES,
  JAV_QUALITY_OPTIONS,
  javModeToFields,
  javStatusLabel,
  javTargetLabel,
} from "@/types/jav";
import "@/styles/jav.css";

/**
 * 订阅页。照源码 want.html：
 *   六个视图（全部 / 影片 / 演员 / 清单 / 在线 / 黑名单）+ 订阅卡 + 条件弹窗 +
 *   影片子弹窗（演员/清单订阅点进去看逐部状态）。
 */
const props = defineProps<{
  /** 外部（影库/榜单的订阅按钮）请求打开新建弹窗的目标。 */
  pendingTarget?: { id: string; name: string; type: string } | null;
  /**
   * 影片详情抽屉是不是开着。那抽屉可以从下面这张影片子弹窗的列表里点开，
   * 开着的时候这张弹窗不能再接「点背景关闭」—— 见 closeMoviesModal。
   */
  movieDetailOpen?: boolean;
}>();

const emit = defineEmits<{
  open: [movie: { id: string }];
  consumePending: [];
  changed: [];
}>();

const accountsStore = useAccountsStore();

/** 订阅级推送目标。留空表示用「番号相关设置」里的默认目标。 */
const pickerOpen = ref(false);

const targetText = computed(() => {
  if (!form.target_account_id) return "";
  const account = accountsStore.accounts.find((a) => a.id === form.target_account_id);
  return `${account?.name ?? `账号 ${form.target_account_id}`} · ${form.target_display_path || "/"}`;
});

function onTargetPicked(payload: { accountId: number; parentId: string; path: string }) {
  form.target_account_id = payload.accountId;
  form.target_parent_id = payload.parentId;
  form.target_display_path = payload.path || "/";
  pickerOpen.value = false;
}

function clearTarget() {
  form.target_account_id = 0;
  form.target_parent_id = "";
  form.target_display_path = "";
}

const VIEW_COMPLETED = "completed";
const VIEW_MOVIE = "movie";
const VIEW_ACTOR = "actor";
const VIEW_LIST = "list";
const VIEW_USER = "user";
const VIEW_BLACKLIST = "blacklist";

const TABS = [
  { key: VIEW_COMPLETED, label: "已完成" },
  { key: VIEW_MOVIE, label: "影片" },
  { key: VIEW_ACTOR, label: "演员" },
  { key: VIEW_LIST, label: "清单" },
  { key: VIEW_USER, label: "用户" },
  { key: VIEW_BLACKLIST, label: "黑名单" },
];

const view = ref(VIEW_COMPLETED);
const loading = ref(false);
const subs = ref<JavSubscription[]>([]);
const blacklist = ref<JavBlacklistEntry[]>([]);

// —— 条件弹窗 ——
const formOpen = ref(false);
const saving = ref(false);
const editingId = ref<number | null>(null);
/**
 * 表单草稿类型：字段全必填。
 *
 * 不直接用 JavSubscriptionInput —— 它的可选字段是为 PATCH 语义准备的
 * （没传就是不改），拿来做表单绑定会让每个 v-model 都变成 `T | undefined`。
 */
type JavForm = {
  target_type: string;
  target_id: string;
  target_url: string;
  target_name: string;
  download_mode: string;
  pre_download: boolean;
  include_comment_links: boolean;
  qualities: string[];
  min_size_mb: number | null;
  max_size_mb: number | null;
  max_file_count: number | null;
  release_date_from: string;
  release_date_to: string;
  expiry_days: number | null;
  categories: string[];
  exclude_categories: string[];
  enabled: boolean;
  target_account_id: number;
  target_parent_id: string;
  target_display_path: string;
  push_provider: string;
  subfolder_mode: string;
};

const form = reactive<JavForm>(blankForm());

function blankForm(): JavForm {
  return {
    target_type: "movie",
    target_id: "",
    target_url: "",
    target_name: "",
    download_mode: "strict",
    pre_download: false,
    // 默认关：这条链路放宽了质量判定（评论链接缺角标/文件数/体积，那些项跳过不判），
    // 代价是真的，所以得用户明确勾。
    include_comment_links: false,
    // 默认勾「高清」：它是这套库里最普遍的一档，不勾等于不限、什么分辨率都收，
    // 反而会把 480p 那类也算进来。要更严就自己加勾「超清」，要放开就取消勾选。
    qualities: ["hd"],
    min_size_mb: null,
    max_size_mb: null,
    max_file_count: null,
    release_date_from: "",
    release_date_to: "",
    expiry_days: null,
    categories: [],
    exclude_categories: [],
    enabled: true,
    target_account_id: 0,
    target_parent_id: "",
    target_display_path: "",
    // 默认「不建子目录」：新建订阅时不再自动按番号套一层目录。
    // （已存在的订阅存的是它当时选的值，编辑时表单显示的是**存下来的值**，不是这里。）
    subfolder_mode: "none",
    push_provider: "auto",
  };
}

/**
 * 「演员订阅」「清单订阅」的默认起映日：**当年 1 月 1 日**。
 *
 * 这两类订阅是「把这个人 / 这份清单名下的片子整个捞一遍」，不给下限就会把十几年前的
 * 老片一起拉进来，候选一大片、推送也没了边。影片订阅针对单个番号，没有这个问题，留空。
 *
 * 用本地年份：用户看的是自己的日历，跨年那几个小时里跟着界面上的日期一致即可。
 */
function defaultReleaseFrom(targetType: string): string {
  if (targetType !== "actor" && targetType !== "list") return "";
  return `${new Date().getFullYear()}-01-01`;
}

/**
 * 上一次**自动**填进 release_date_from 的值。
 *
 * 靠它区分「这串日期是默认值」与「这是用户自己挑的」：只有前者允许被类型切换改写，
 * 手填过的日期永远不动。
 */
let autoReleaseFrom = "";

function applyReleaseFromDefault(targetType: string) {
  if (form.release_date_from !== "" && form.release_date_from !== autoReleaseFrom) return;
  autoReleaseFrom = defaultReleaseFrom(targetType);
  form.release_date_from = autoReleaseFrom;
}

// 在弹窗里改「订阅类型」时跟着换默认值 —— 从「创建订阅」按钮进来再选演员，也该有默认。
// 只在创建态生效：编辑态显示的是订阅里存下来的值，自动填会让人一保存就把日期悄悄改掉。
//
// watch 默认在渲染前批量触发，openCreate/openEdit 里那串同步赋值会合并成一次回调，
// 届时 editingId 已经定好、拿到的也是最终类型。
watch(
  () => form.target_type,
  (type) => {
    if (editingId.value !== null) return;
    applyReleaseFromDefault(type);
  },
);

// —— 「用户」档：关注的分享者 ——

const followedUsers = ref<JavFollowedUser[]>([]);
const followedLoading = ref(false);
/** 抓取失败的说明。有它才能把「抓不到」和「一个都没关注」分开。 */
const followedError = ref("");

/** 分享者弹窗（点用户卡打开）。它与详情页里点分享者名字是同一个组件。 */
const sharesOpen = ref(false);
const sharesUserId = ref(0);
const sharesUsername = ref("");
const sharesFollowed = ref(false);

async function loadFollowed() {
  followedLoading.value = true;
  followedError.value = "";
  try {
    const res = await fetchJavFollowedUsers();
    followedUsers.value = res.items ?? [];
  } catch (err) {
    const message = getApiErrorMessage(err, "关注列表加载失败");
    followedError.value = message;
    if (followedUsers.value.length) {
      // 已经有列表：原样留着，只拿 toast 说一句。刷新失败就把整屏换成错误页，
      // 等于每次网络抖一下都清空一次用户刚看的东西。
      toast.error(message);
    } else {
      followedUsers.value = [];
    }
  } finally {
    followedLoading.value = false;
  }
}

function openUserShares(u: JavFollowedUser) {
  sharesUserId.value = u.user_id;
  sharesUsername.value = u.username;
  sharesFollowed.value = true;
  sharesOpen.value = true;
}

/**
 * 从分享者弹窗里点开一部影片。
 *
 * **必须先关掉弹窗**：它走 AppModal 那一档（10100），而影片详情抽屉是 elevated
 * （10040）—— 不关的话抽屉会开在弹窗背后，看起来像「点了没反应」。
 */
function openMovieFromUserShares(id: string) {
  sharesOpen.value = false;
  emit("open", { id });
}

/** 弹窗里关注状态变了：列表跟着变（取关的应当当场消失）。 */
function onUserFollowChanged(userId: number, followed: boolean) {
  if (!followed) {
    followedUsers.value = followedUsers.value.filter((u) => u.user_id !== userId);
    return;
  }
  void loadFollowed();
}

/** 头像里那颗字：取用户名前两个字符（与清单卡同一套）。 */
function userAvatarText(name: string): string {
  return Array.from((name || "").trim()).slice(0, 2).join("");
}

// —— 影片子弹窗 ——
const moviesOpen = ref(false);
const moviesSub = ref<JavSubscription | null>(null);
const subMovies = ref<JavSubMovie[]>([]);
const moviesLoading = ref(false);
/** 这次加载慢到需要解释一句（服务端回落到上游抓取时才会）。 */
const moviesSlow = ref(false);
/**
 * 没过条件（日期窗、清晰度、类别、黑名单挡掉的）的影片数。
 *
 * 这些**不列**：卡片上「检：N」数的就是过条件的那些，把不过条件的一起列出来，
 * 两者对不上，用户只会觉得条件没生效。留个数字在标题栏里，是为了别让
 * 「少了几部」变成一个说不清的现象。
 */
const ineligibleCount = computed(() => subMovies.value.filter((m) => !m.eligible).length);

/**
 * 弹窗里的**状态分页**（21.png 那四颗胶囊）：按影片自己的推进状态分开看。
 * 「全部」= 三种之和；跳过的影片只在「跳过」页里，不在「订阅中」页出现。
 */
const MOVIE_STATUS_TABS = [
  { key: "active", label: "订阅中" },
  { key: "completed", label: "已完成" },
  { key: "skipped", label: "跳过" },
  { key: "all", label: "全部" },
] as const;

const movieStatusTab = ref<string>("active");

/**
 * 各状态的影片数。照 21.png：只有「订阅中」和「全部」带计数。
 *
 * **只数「过条件」的** —— 也就是下面列表里真正会画出来的那些。
 *
 * 以前它数的是全部（含被日期窗 / 清晰度 / 类别挡掉的），于是标签写着
 * 「订阅中（305）」、下面只列出 62 部；用户再拿卡片上那个「检」（= 过条件的
 * 影片数）一对，三个数字互相对不上（2026-09-22 报上来的）。
 * 没过条件的那些由标题栏那句「另有 N 部没过条件」交代，不必挤进标签页 ——
 * 标题栏说的是「除此之外还有多少」，标签页说的是「这一档里有多少」，
 * 各司其职才对得上。
 */
const statusCounts = computed(() => {
  const shown = subMovies.value.filter((m) => m.eligible);
  const c: Record<string, number> = { active: 0, completed: 0, skipped: 0, all: shown.length };
  for (const m of shown) {
    if (m.sub_status === "completed") c.completed++;
    else if (m.sub_status === "skipped") c.skipped++;
    else c.active++;
  }
  return c;
});

/** 先按状态分页，再按「只看符合条件的」过滤 —— 两颗开关是正交的。 */
const statusFiltered = computed(() =>
  movieStatusTab.value === "all"
    ? subMovies.value
    : subMovies.value.filter((m) => m.sub_status === movieStatusTab.value),
);

const visibleSubMovies = computed(() => statusFiltered.value.filter((m) => m.eligible));

/** 当前分页的中文名 —— 空状态那句「没有『已完成』的影片」要用。 */
const movieStatusTabLabel = computed(
  () => MOVIE_STATUS_TABS.find((t) => t.key === movieStatusTab.value)?.label ?? "",
);

/** 当前分页里没过条件的部数：空状态提示「点显示全部能看到那 N 部」时要用。 */
const ineligibleInTab = computed(() => statusFiltered.value.filter((m) => !m.eligible).length);

// —— 批量选择 ——
const batchMode = ref(false);
const batchSelected = ref<Set<string>>(new Set());
/** 批量任务的进度文案（串行执行时显示「已推送 N/M」）。 */
const batchRunning = ref(false);
const batchProgress = ref("");

/**
 * 操作菜单的两项，与 22.png 一致。
 *
 * `type: "action"` **不能省** —— AppMenuPanel 的 onItemClick 是
 * `if (item.type !== "action" || item.disabled) return`，不写这一项的话点了
 * 什么都不会发生（既不报错也不派发），排查起来像是「按钮坏了」。
 */
const opsItems = computed<DropdownMenuItem[]>(() => [
  {
    key: "subscribe",
    type: "action",
    label: batchRunning.value ? "执行中…" : "执行订阅",
    disabled: batchRunning.value,
  },
  { key: "batch", type: "action", label: batchMode.value ? "退出批量选择" : "开启批量选择" },
]);

function onOpsSelect(key: string) {
  if (key === "subscribe") void runFilteredSubscribe();
  else if (key === "batch") toggleBatchMode();
}

function toggleBatchMode() {
  batchMode.value = !batchMode.value;
  batchSelected.value = new Set();
}

function toggleSelect(movie: JavSubMovie) {
  const next = new Set(batchSelected.value);
  if (next.has(movie.id)) next.delete(movie.id);
  else next.add(movie.id);
  batchSelected.value = next;
}

/**
 * 批量模式下点卡片是**勾选**，不是打开详情。
 *
 * 用捕获阶段把它拦下来：卡片自己的 @open 挂在它的根节点上，捕获先于冒泡，
 * 这里 stopPropagation 之后卡片就收不到这次点击了 —— 不用给 JavMovieCard
 * 加一个「禁用点击」的 prop（那是为了一个临时模式去污染通用组件）。
 */
function onCardClickCapture(event: MouseEvent, movie: JavSubMovie) {
  if (!batchMode.value) return;
  // 跳过键压在卡片上，它有自己的动作。捕获阶段先于它自己的 handler 执行，
  // 所以光靠 @click.stop 拦不住这里 —— 得在这儿认出来并放行。
  if ((event.target as HTMLElement | null)?.closest(".jav-sub-movie__skip")) return;
  event.stopPropagation();
  event.preventDefault();
  toggleSelect(movie);
}

/**
 * 逐部执行订阅（串行）。
 *
 * 串行 + 每部之间停顿 800ms：网盘对同一账号的并发提交有风控，几十部一起打过去
 * 正是触发它的姿势。这与定时推送里那个 paceSleep 是同一个理由。
 */
async function pushMoviesSerial(movies: JavSubMovie[]): Promise<{ ok: number; fail: number }> {
  const sub = moviesSub.value;
  if (!sub || movies.length === 0) return { ok: 0, fail: 0 };
  batchRunning.value = true;
  let ok = 0;
  let fail = 0;
  try {
    for (let i = 0; i < movies.length; i++) {
      batchProgress.value = `正在执行 ${i + 1}/${movies.length}`;
      try {
        const res = await subscribeJavMovie(sub.id, movies[i].id);
        if (res.ok) ok++;
        else fail++;
      } catch {
        fail++;
      }
      if (i < movies.length - 1) await new Promise((r) => setTimeout(r, 800));
    }
  } finally {
    batchRunning.value = false;
    batchProgress.value = "";
  }
  return { ok, fail };
}

/**
 * 「操作 → 执行订阅」：**勾了谁就推谁**；一部没勾就推当前这一页还没推的（订阅中的）。
 *
 * 两种用法都要照顾到：批量选择是「挑几部推」，不选直接点则是「这一页的都推」。
 * 已完成/跳过的不推 —— 前者已经在盘里了，后者是用户明确不要的。
 */
async function runFilteredSubscribe() {
  const picked = visibleSubMovies.value.filter(
    (m) => batchSelected.value.has(m.id) && m.sub_status === "active",
  );
  const targets =
    picked.length > 0 ? picked : visibleSubMovies.value.filter((m) => m.sub_status === "active");
  if (targets.length === 0) {
    toast.info(
      batchSelected.value.size > 0
        ? "勾选的里面没有「订阅中」的影片"
        : "当前这一页没有「订阅中」的影片",
    );
    return;
  }
  const { ok, fail } = await pushMoviesSerial(targets);
  toast[fail ? "error" : "success"](`已执行 ${ok} 部${fail ? `，失败 ${fail} 部` : ""}`);
  await reloadMovies();
  emit("changed");
}

/** 批量勾选之后：跳过 / 取消跳过。 */
async function batchSkip(skip: boolean) {
  const sub = moviesSub.value;
  if (!sub) return;
  const ids = [...batchSelected.value];
  if (ids.length === 0) {
    toast.info("先勾几部影片");
    return;
  }
  batchRunning.value = true;
  let done = 0;
  try {
    for (let i = 0; i < ids.length; i++) {
      batchProgress.value = `正在${skip ? "跳过" : "取消跳过"} ${i + 1}/${ids.length}`;
      try {
        await setJavMovieSkip(sub.id, ids[i], skip);
        done++;
      } catch {
        /* 单部失败不中断整批 */
      }
    }
  } finally {
    batchRunning.value = false;
    batchProgress.value = "";
  }
  toast.success(`已${skip ? "跳过" : "取消跳过"} ${done} 部`);
  batchSelected.value = new Set();
  await reloadMovies();
  emit("changed");
}

/** 批量勾选之后：逐部执行订阅。 */
async function batchSubscribe() {
  const targets = visibleSubMovies.value.filter(
    (m) => batchSelected.value.has(m.id) && m.sub_status === "active",
  );
  if (targets.length === 0) {
    toast.info("勾选的里面没有「订阅中」的影片");
    return;
  }
  const { ok, fail } = await pushMoviesSerial(targets);
  toast[fail ? "error" : "success"](`已执行 ${ok} 部${fail ? `，失败 ${fail} 部` : ""}`);
  batchSelected.value = new Set();
  await reloadMovies();
  emit("changed");
}

// —— 检查结果弹窗 ——
const checkOpen = ref(false);
const checkTitle = ref("");
const candidates = ref<JavCandidate[]>([]);
/** 候选弹窗当前对应的订阅 —— 从候选推单颗、以及「执行订阅」都要它。 */
const checkSub = ref<JavSubscription | null>(null);
const checkSubId = computed(() => checkSub.value?.id ?? null);
const pushing = ref(false);

/**
 * 当前这一栏要显示的订阅。
 *
 * 推完的影片订阅（`isMovieDone`）从「影片」栏里收走 —— 它那部片已经在「已完成」栏
 * 里了，再留一张「订阅中」的卡片在那儿是同一个东西说两遍话。isMovieDone 只对
 * target_type === "movie" 成立，所以其余几栏不受影响。
 */
/**
 * 当前这一档要列哪些订阅。
 *
 * 「在线」档已经不在了（改成了「用户」，装的是关注的分享者）——
 * 但 `target_type === "online"` 的订阅还得有地方去，否则它们就没有入口了。
 * 它的解析路径本来就和影片订阅完全一样（见 check.go 的 resolveTarget：
 * `JavTargetMovie, JavTargetOnline` 走同一个分支），所以并进「影片」这一档。
 */
const filtered = computed(() =>
  subs.value.filter(
    (s) =>
      !isMovieDone(s) &&
      (view.value === VIEW_MOVIE
        ? s.target_type === VIEW_MOVIE || s.target_type === "online"
        : s.target_type === view.value),
  ),
);

/** 「已完成」那一档显示的是**推送成功过的影片**，不是订阅。 */
const completed = ref<JavCompletedMovie[]>([]);
const completedTotal = ref(0);
const completedPage = ref(1);
const completedLoading = ref(false);
const COMPLETED_PAGE_SIZE = 60;

/**
 * 「已完成」那一档的排序胶囊。键名与服务端白名单一一对应（store 的 completedOrderBy），
 * 服务端认不出来就回落到完成时间倒序。
 */
const COMPLETED_SORTS = [
  { key: "code", label: "番号" },
  { key: "created", label: "创建时间" },
  { key: "resource", label: "资源时间" },
  { key: "done", label: "完成时间" },
] as const;

const completedSort = ref<string>("done");
const completedDesc = ref(true);

/** 点排序胶囊：换一颗就按它的倒序开头，再点同一颗则翻转正序/倒序。 */
function pickCompletedSort(key: string) {
  if (completedSort.value === key) {
    completedDesc.value = !completedDesc.value;
  } else {
    completedSort.value = key;
    completedDesc.value = true;
  }
  void loadCompleted(1);
}

async function loadCompleted(page = 1) {
  completedLoading.value = true;
  try {
    const res = await fetchJavCompletedMovies({
      page,
      page_size: COMPLETED_PAGE_SIZE,
      sort: completedSort.value,
      dir: completedDesc.value ? "desc" : "asc",
    });
    completed.value = res.items ?? [];
    completedTotal.value = res.total ?? 0;
    completedPage.value = page;
  } catch (err) {
    toast.error(getApiErrorMessage(err, "已完成影片加载失败"));
    completed.value = [];
    completedTotal.value = 0;
  } finally {
    completedLoading.value = false;
  }
}

const completedPages = computed(() =>
  Math.max(1, Math.ceil(completedTotal.value / COMPLETED_PAGE_SIZE)),
);

/** 切到「已完成」时要拉数据（其余档读的是已经加载好的订阅列表）。 */
watch(view, (v) => {
  if (v === VIEW_COMPLETED && completed.value.length === 0) void loadCompleted(1);
  // 用户档每次进来都刷一遍：关注/取关可能刚在详情页的弹窗里发生过。
  if (v === VIEW_USER) void loadFollowed();
});

async function load() {
  loading.value = true;
  try {
    // 一次性拿全部再本地分视图：订阅量级是几十条，
    // 每切一次视图就打一次接口反而更慢，而且切换时会出现空列表闪一下。
    const [subRes, blackRes] = await Promise.all([
      fetchJavSubscriptions({ page_size: 500 }),
      fetchJavBlacklist(),
    ]);
    subs.value = subRes.items ?? [];
    blacklist.value = blackRes.items ?? [];
  } catch (err) {
    toast.error(getApiErrorMessage(err, "订阅列表加载失败"));
    subs.value = [];
    blacklist.value = [];
  } finally {
    loading.value = false;
  }
}

/**
 * 点「创建订阅」按钮（没带具体目标）时，类型跟着当前这一栏走：
 * 演员栏里开出来就是「演员订阅」，清单栏里就是「清单订阅」。其余栏维持影片订阅。
 */
function defaultCreateType(): string {
  return view.value === VIEW_ACTOR || view.value === VIEW_LIST ? view.value : VIEW_MOVIE;
}

function openCreate(target?: { id: string; name: string; type: string }) {
  Object.assign(form, blankForm());
  autoReleaseFrom = "";
  editingId.value = null;
  form.target_type = target?.type ?? defaultCreateType();
  if (target) {
    form.target_id = target.id;
    form.target_name = target.name;
  }
  // 从演员卡/清单卡直接点「订阅」进来时，类型已经定好了，watch 也认得出「这是默认值」
  applyReleaseFromDefault(form.target_type);
  formOpen.value = true;
}

function openEdit(sub: JavSubscription) {
  Object.assign(form, blankForm());
  autoReleaseFrom = "";
  editingId.value = sub.id;
  form.target_type = sub.target_type;
  form.target_id = sub.target_id;
  form.target_url = sub.target_url;
  form.target_name = sub.target_name;
  form.download_mode = sub.download_mode;
  form.pre_download = sub.pre_download;
  form.include_comment_links = sub.include_comment_links;
  form.qualities = [...sub.qualities];
  form.min_size_mb = sub.min_size_mb;
  form.max_size_mb = sub.max_size_mb;
  form.max_file_count = sub.max_file_count;
  form.release_date_from = sub.release_date_from;
  form.release_date_to = sub.release_date_to;
  form.expiry_days = sub.expiry_days;
  form.categories = [...sub.categories];
  form.exclude_categories = [...sub.exclude_categories];
  form.enabled = sub.enabled;
  form.target_account_id = sub.target_account_id;
  form.target_parent_id = sub.target_parent_id;
  form.target_display_path = sub.target_display_path;
  form.push_provider = sub.push_provider;
  form.subfolder_mode = sub.subfolder_mode;
  formOpen.value = true;
}

/** 界面上的三档 ↔ 正交两列的换算。 */
const modeValue = computed({
  get: () => (form.download_mode === "upgrade" ? "upgrade" : form.pre_download ? "predownload" : "strict"),
  set: (v: string) => {
    const fields = javModeToFields(v);
    form.download_mode = fields.download_mode;
    form.pre_download = fields.pre_download;
  },
});

function toggleQuality(value: string) {
  const idx = form.qualities.indexOf(value);
  if (idx >= 0) form.qualities.splice(idx, 1);
  else form.qualities.push(value);
}

async function save() {
  if (!form.target_name.trim()) {
    toast.error("请填写名称");
    return;
  }
  saving.value = true;
  try {
    if (editingId.value) {
      await updateJavSubscription(editingId.value, form);
      toast.success("订阅已更新");
    } else {
      await createJavSubscription(form);
      toast.success("订阅已创建");
    }
    formOpen.value = false;
    await load();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function remove(sub: JavSubscription) {
  if (!window.confirm(`确认删除订阅「${sub.target_name}」？`)) return;
  try {
    await deleteJavSubscription(sub.id);
    toast.success("已删除");
    await load();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "删除失败"));
  }
}

async function toggleStatus(sub: JavSubscription) {
  const next = sub.status === "paused" ? "active" : "paused";
  try {
    await setJavSubscriptionStatus(sub.id, next);
    await load();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "状态修改失败"));
  }
}

async function execute(sub: JavSubscription) {
  try {
    const res = await autoPushJavSubscription(sub.id, false);
    toast[res.ok ? "success" : "error"](res.message || "已执行");
    await load();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "执行失败"));
  }
}

/**
 * 推送一颗指定的候选。
 *
 * 这是**预下载的手动确认入口** —— 预下载订阅的 auto-push 会被「需要手动确认」挡住，
 * 用户看到候选上的「待确认」标记后点这里推出去。源码的 renderCandidates 里
 * 也是这个按钮（`data-push-candidate`）。
 */
async function pushOneCandidate(c: JavCandidate) {
  if (!checkSubId.value) return;
  pushing.value = true;
  try {
    const res = await pushJavCandidate(checkSubId.value, c.id);
    toast[res.ok ? "success" : "error"](res.message || (res.ok ? "已提交" : "推送失败"));
    if (res.ok) {
      checkOpen.value = false;
      await load();
      emit("changed");
    }
  } catch (err) {
    toast.error(getApiErrorMessage(err, "推送失败"));
  } finally {
    pushing.value = false;
  }
}

/** 预下载订阅的「确认推送」：带 force 绕过那道闸。 */
async function confirmPreDownload(sub: JavSubscription) {
  if (!window.confirm(`确认推送「${sub.target_name}」？

预下载订阅需要你确认后才会推给网盘。`)) return;
  try {
    const res = await autoPushJavSubscription(sub.id, true);
    toast[res.ok ? "success" : "error"](res.message || "已确认");
    await load();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "推送失败"));
  }
}

async function loadCandidates(sub: JavSubscription) {
  try {
    const res = await fetchJavCandidates(sub.id, { limit: 200 });
    checkTitle.value = sub.target_name;
    checkSub.value = sub;
    candidates.value = res.items ?? [];
    checkOpen.value = true;
  } catch (err) {
    toast.error(getApiErrorMessage(err, "候选加载失败"));
  }
}

/**
 * 点背景关影片子弹窗。
 *
 * 详情抽屉开着时不关。详情抽屉是 inset 的（顶留 `--admin-chrome-h`、左留
 * `--sidebar-width`），顶栏与侧栏那两条边带仍归这张弹窗 —— 不加这道闸，
 * 用户点在那两条边带上会把弹窗关在抽屉背后：他眼前还是详情，等关掉详情
 * 才发现底下的影片列表没了。抽屉自己的背景遮罩照旧一点即关，不受影响。
 *
 * 右上角那颗 ✕ 不走这里：那是「关掉这张弹窗」的明确动作，照关。
 * （抽屉开着时它本来也被抽屉压着点不到。）
 */
function closeMoviesModal() {
  if (props.movieDetailOpen) return;
  moviesOpen.value = false;
}

async function openMovies(sub: JavSubscription) {
  moviesSub.value = sub;
  moviesOpen.value = true;
  // 每次打开都回到默认：停在「订阅中」页、不在批量选择里。
  // 上一次那些是临时看一眼，不该变成下次打开弹窗时的默认。
  movieStatusTab.value = "active";
  batchMode.value = false;
  batchSelected.value = new Set();
  await reloadMovies();
}

/**
 * 把卡片上那个「检」对齐成弹窗现算出来的数。
 *
 * 服务端在 `SubscriptionMovies` 里已经写回了库，但那份要等订阅列表重新拉一次
 * 才反映到卡片上 —— 用户刚看完弹窗、关掉，卡片还写着旧数，看着就像没生效。
 * 这里就地改一份，卡片当场跟上。
 *
 * 「过条件的影片数」= 列表里 eligible 的那些（与标签页「全部」同源）。
 */
function syncMatchedCount(subID: number) {
  const eligible = subMovies.value.filter((m) => m.eligible).length;
  const row = subs.value.find((s) => s.id === subID);
  if (row && row.matched_count !== eligible) row.matched_count = eligible;
}

/** 重新拉一次这个订阅的影片（批量操作之后用它刷新状态，不重置任何筛选）。 */
async function reloadMovies() {
  const sub = moviesSub.value;
  if (!sub) return;
  moviesLoading.value = true;
  // 本地有片时这是个毫秒级查询，正常不会有人看见第二句。本地一部都没有
  // （刚建好、调度器还没轮到）时服务端才回落到上游抓一次，那一次要几十秒 ——
  // 与其让「加载中…」干挂着，不如说清楚在等什么。
  moviesSlow.value = false;
  const slowTimer = setTimeout(() => {
    moviesSlow.value = true;
  }, 2000);
  try {
    const res = await fetchJavSubscriptionMovies(sub.id);
    subMovies.value = res.items ?? [];
    syncMatchedCount(sub.id);
  } catch (err) {
    toast.error(getApiErrorMessage(err, "影片列表加载失败"));
    subMovies.value = [];
  } finally {
    clearTimeout(slowTimer);
    moviesSlow.value = false;
    moviesLoading.value = false;
  }
}


async function toggleSkip(movie: JavSubMovie) {
  if (!moviesSub.value) return;
  const skip = movie.sub_status !== "skipped";
  try {
    await setJavMovieSkip(moviesSub.value.id, movie.id, skip);
    await reloadMovies();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "操作失败"));
  }
}

async function addBlacklist(sub: JavSubscription) {
  try {
    await addJavBlacklist({
      // online 归一成 movie：后端 AddBlacklist 只认 movie/actor/list，
      // 直接把 "online" 发过去会被校验拒掉。
      target_type: sub.target_type === "online" ? "movie" : sub.target_type,
      target_id: sub.target_id,
      target_url: sub.target_url,
      target_name: sub.target_name,
      reason: "从订阅页加入",
      // 后端要靠它算「加入这一刻符合条件的影片」快照 —— 那份快照只能在落库之前算，
      // 落库之后这些片当场变成不合格，再算就是空集。
      subscription_id: sub.id,
    });
    toast.success("已加入黑名单");
    await load();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "加入黑名单失败"));
  }
}

async function removeBlacklist(entry: JavBlacklistEntry) {
  try {
    await deleteJavBlacklist(entry.id);
    await load();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "删除失败"));
  }
}

/**
 * 黑名单匹配键：`类型:小写id`。
 *
 * 必须小写归一 —— 后端的 target_key 是 CanonicalTargetKey 产出的
 * `lower(type)+":"+lower(value)`，而 entry.target_id 存的是原文，直接比会漏。
 * 只能按 id 比：JavBlacklistEntry 里没有 target_url 字段。
 * online 归一到 movie：加黑名单时后端也只认 movie/actor/list。
 */
function blacklistKey(targetType: string, targetId: string): string {
  const t = (targetType || "").trim().toLowerCase();
  return `${t === "online" ? "movie" : t}:${(targetId || "").trim().toLowerCase()}`;
}

/** 黑名单索引。放 computed 而不是在函数里 find：模板里每张卡要问三四次。 */
const blacklistIndex = computed(() => {
  const m = new Map<string, JavBlacklistEntry>();
  for (const e of blacklist.value) m.set(blacklistKey(e.target_type, e.target_id), e);
  return m;
});

function blacklistEntryOf(sub: JavSubscription): JavBlacklistEntry | undefined {
  return blacklistIndex.value.get(blacklistKey(sub.target_type, sub.target_id));
}

function isBlacklisted(sub: JavSubscription): boolean {
  return blacklistEntryOf(sub) !== undefined;
}

// —— 黑名单条目的影片快照（只读弹窗） ——

const blacklistMoviesOpen = ref(false);
const blacklistMoviesLoading = ref(false);
const blacklistMovies = ref<JavCard[]>([]);
/** 打开的条目名，弹窗标题用。 */
const blacklistMoviesTitle = ref("");

async function openBlacklistMovies(entry: JavBlacklistEntry) {
  blacklistMoviesTitle.value = entry.target_name;
  blacklistMovies.value = [];
  blacklistMoviesOpen.value = true;
  blacklistMoviesLoading.value = true;
  try {
    const res = await fetchJavBlacklistMovies(entry.id);
    blacklistMovies.value = res.items ?? [];
  } catch (err) {
    toast.error(getApiErrorMessage(err, "黑名单影片加载失败"));
    blacklistMoviesOpen.value = false;
  } finally {
    blacklistMoviesLoading.value = false;
  }
}

/** 外部请求新建订阅（影库/榜单卡片上的订阅按钮）。 */
function onPendingTarget(target: { id: string; name: string; type: string }) {
  openCreate(target);
  emit("consumePending");
}

/** 正在检查的订阅 id。图标按钮转圈用。 */
const checking = ref<Set<number>>(new Set());

/**
 * 影片订阅的「展示层已完成」。
 *
 * 影片订阅的 status **永不重算** —— 照搬源码 `refresh_status`：`target_type == "movie"`
 * 直接 return，注释写「单部即整体」。于是它那部片推完了，status 还是 active、角标一直
 * 报「订阅中」，卡片也一直挂在「影片」栏里，跟「已完成」栏里已经出现的那部片对不上。
 *
 * 这里另外算一个：strict 模式下已经推成功过的那一部就算完事。
 * **排除 upgrade**：洗版订阅的意图就是「有更好的版本再推一次」，推过一次不算完。
 */
function isMovieDone(sub: JavSubscription): boolean {
  return (
    sub.target_type === "movie" &&
    sub.mode !== "upgrade" &&
    (sub.pushed_count ?? 0) > 0
  );
}

/** 状态胶囊的文案：检查中优先显示「匹配中」，其次看影片订阅是不是已经推完。 */
function statusText(sub: JavSubscription): string {
  if (checking.value.has(sub.id)) return "匹配中";
  if (isMovieDone(sub)) return "已完成";
  return javStatusLabel(sub.status);
}

/** 状态胶囊的配色类。 */
function statusClass(sub: JavSubscription): string {
  return `sub-card__status--${isMovieDone(sub) ? "completed" : sub.status}`;
}

/**
 * 演员 / 清单订阅走另一套卡片形态：圆头像 + 居中名字。
 *
 * 它们都**没有横版封面** —— 演员的 `cover` 给的是头像（照影片那套 3:2 的框摆
 * 会把两侧裁掉），清单压根没有图。而且番号/日期对这两种目标也没有意义。
 */
function usesAvatarCard(sub: JavSubscription): boolean {
  return sub.target_type === "actor" || sub.target_type === "list";
}

/**
 * 圆头像里那颗字。
 *
 * 演员有头像图就用图（下面模板里判 `sub.cover`）；清单没有图，取清单名的
 * **前两个字**当头像 —— 与演员卡缺图时退成首字同一个思路，只是多取一个字：
 * 中文清单名两字才认得出（「5分」「精选」），一个字太含糊。
 *
 * 按 rune 取而不是 `slice(0, 2)`：名字里可能有 emoji 之类的代理对，
 * 按 UTF-16 切会切出半个字符。
 */
function listAvatarText(name: string): string {
  return Array.from((name || "").trim()).slice(0, 2).join("");
}

/** 最后检查时间的展示：没检查过就说「还没检查」。 */
function lastCheckedText(sub: JavSubscription): string {
  if (!sub.last_checked_at) return "还没检查";
  return sub.last_checked_at.slice(0, 16).replace("T", " ");
}

/**
 * 卡片本体点击。
 *
 * 影片订阅点进去看影片详情；演员/清单点开影片子弹窗（那才是它们的「内容」）。
 * 图标按钮都在带 @click.stop 的容器里，不会误触发这里。
 */
function onCardClick(sub: JavSubscription) {
  // 被拉黑的订阅点不动。要拦住的必须是这个 return：封面是 div，没有真的 disabled，
  // 模板上的 aria-disabled 只是宣告。想看被挡了哪些片，去「黑名单」那一档点开卡片。
  if (isBlacklisted(sub)) return;
  if (sub.target_type === "actor" || sub.target_type === "list") {
    void openMovies(sub);
    return;
  }
  if (sub.target_id) emit("open", { id: sub.target_id });
}

/**
 * 卡片上的检查按钮：转圈、不弹窗、跑完刷新。
 *
 * 与整屏 loading 分开：检查一条订阅要跑几秒，期间把整页变成「加载中」
 * 会让用户以为整个页面都卡住了。只让这一张卡的按钮转圈。
 */
async function silentCheck(sub: JavSubscription) {
  if (checking.value.has(sub.id)) return;
  checking.value = new Set(checking.value).add(sub.id);
  try {
    await checkJavSubscription(sub.id);
    toast.success("检查完成");
    await load();
    emit("changed");
  } catch (err) {
    toast.error(getApiErrorMessage(err, "检查失败"));
  } finally {
    const next = new Set(checking.value);
    next.delete(sub.id);
    checking.value = next;
  }
}

defineExpose({ openCreate, onPendingTarget, reload: load });

onMounted(() => {
  void load();
  void loadCompleted();
  void accountsStore.loadAccounts();
});
</script>

<template>
  <div>
    <SectionTabBar :model-value="view" :tabs="TABS" @update:model-value="view = $event" />

    <!-- space-between：左侧是「创建订阅 / 刷新」，右侧留给「已完成」那一档的四颗
         排序胶囊。用 space-between 而不是给胶囊加 margin-left:auto —— 后者在
         「这一档左侧一个按钮都没有」时靠不住。 -->
    <div style="display: flex; gap: 8px; margin-bottom: 14px; align-items: center; justify-content: space-between">
      <!-- 「已完成」那一档不放「创建订阅」：那一栏看的是已经拿到手的片，
           在这儿放一个新建入口既没用又容易被误点。换成排序胶囊。 -->
      <AppButton v-if="view !== VIEW_COMPLETED" size="sm" @click="openCreate()">创建订阅</AppButton>
      <AppButton v-if="view !== VIEW_COMPLETED && view !== VIEW_BLACKLIST" variant="ghost" size="sm" @click="load">
        刷新
      </AppButton>
      <!-- 排序胶囊与影库那一栏同一套 class（.jav-sort-chips / .jav-sort-chip），
           不另造一套样式；箭头只在当前生效的那颗上显示。 -->
      <div v-if="view === VIEW_COMPLETED" class="jav-sort-chips">
        <button
          v-for="s in COMPLETED_SORTS"
          :key="s.key"
          type="button"
          class="jav-sort-chip"
          :class="{ 'jav-sort-chip--active': completedSort === s.key }"
          :title="
            completedSort === s.key
              ? completedDesc
                ? '已是倒序，点一下改成正序'
                : '已是正序，点一下改成倒序'
              : `按${s.label}排序`
          "
          @click="pickCompletedSort(s.key)"
        >
          {{ s.label }}
          <span v-if="completedSort === s.key">{{ completedDesc ? "↓" : "↑" }}</span>
        </button>
      </div>
    </div>

    <!-- 已完成：推送成功过的影片。影片订阅 / 演员订阅 / 清单订阅推成功的都汇在这里。 -->
    <template v-if="view === VIEW_COMPLETED">
      <div v-if="completedLoading" class="jav-empty">加载中…</div>
      <div v-else-if="!completed.length" class="jav-empty">
        <div class="jav-empty__icon">✅</div>
        <div>还没有推送成功的影片。</div>
      </div>
      <template v-else>
        <div class="jav-grid">
          <JavMovieCard
            v-for="m in completed"
            :key="m.id"
            :movie="m"
            :show-subscribe="false"
            @open="emit('open', m)"
          />
        </div>
        <div v-if="completedPages > 1" class="jav-pagination">
          <AppPagination
            :page="completedPage"
            :total-pages="completedPages"
            @update:page="loadCompleted"
          />
        </div>
      </template>
    </template>

    <!-- 黑名单 -->
    <template v-else-if="view === VIEW_BLACKLIST">
      <div v-if="!blacklist.length" class="jav-empty">黑名单是空的。</div>
      <div v-else class="sub-card-grid">
        <div v-for="entry in blacklist" :key="entry.id" class="jav-card sub-card">
          <!-- 点开看这条黑名单挡住了哪些片。
               有条目里存的**快照**就读快照；没有（迁移前的老条目、或加入时上游不通）
               后端会按目标现列 —— 该演员/该清单名下的片。两种都不为空才点得动。 -->
          <div
            class="sub-card__cover jav-card__cover sub-card__cover--clickable"
            @click="openBlacklistMovies(entry)"
          >
            <div class="jav-card__placeholder">黑名单</div>
            <span class="sub-card__status sub-card__status--completed">
              {{ javTargetLabel(entry.target_type) }}
            </span>
            <!-- 快照为空（老条目）时不显示「N 部」—— 后端这时按目标现列，
                 片数要等点开才知道，先给个 0 会误导。 -->
            <span v-if="entry.movies_count > 0" class="sub-card__badge sub-card__badge--count">
              <i class="fas fa-film" /> {{ entry.movies_count }} 部
            </span>
            <!-- @click.stop 是必须的：封面现在可点（打开影片快照），不拦的话
                 点「移出黑名单」会冒泡上去，顺手弹一个正在被删的条目的窗口。 -->
            <div class="sub-card__acts sub-card__acts--tr" @click.stop>
              <button
                type="button"
                class="sub-card__icon sub-card__icon--danger"
                title="移出黑名单"
                @click="removeBlacklist(entry)"
              >
                <i class="fas fa-trash" />
              </button>
            </div>
          </div>
          <div class="jav-card__body">
            <div class="jav-card__meta">
              <span class="jav-card__num">{{ javTargetLabel(entry.target_type) }}</span>
            </div>
            <div class="jav-card__title" :title="entry.target_name">{{ entry.target_name }}</div>
            <div class="sub-card__foot">
              <span class="sub-card__foot-left">
                <i class="fas fa-clock" />
                {{ (entry.created_at || "").slice(0, 16).replace("T", " ") }}
              </span>
              <span class="sub-card__foot-right">黑名单</span>
            </div>
          </div>
        </div>
      </div>
    </template>

    <!-- 「用户」档：关注的分享者。与订阅不是一回事（那些是 jav_follows 里的行），
         所以不走 filtered，单独一块。卡片形态与清单/演员订阅卡一致
         （.sub-card--avatar + 圆头像），只是头像里放用户名。 -->
    <template v-else-if="view === VIEW_USER">
      <!-- 「加载中」和错误页只在**还没有列表**时占屏。已经有内容时后台刷新：
           这一档每次切进来都会重拉（关注/取关可能在详情页的弹窗里刚发生过），
           用整屏的「加载中…」把它抹掉，看起来就像每次打开都要等半天。 -->
      <div v-if="followedLoading && !followedUsers.length" class="jav-empty">加载中…</div>
      <div v-else-if="followedError && !followedUsers.length" class="jav-empty">
        <div class="jav-empty__icon">⚠️</div>
        <div>{{ followedError }}</div>
        <AppButton variant="ghost" size="sm" style="margin-top: 12px" @click="loadFollowed">
          重试
        </AppButton>
      </div>
      <div v-else-if="!followedUsers.length" class="jav-empty">
        <div class="jav-empty__icon">👤</div>
        <div>还没有关注任何分享者。</div>
        <div style="margin-top: 6px; font-size: 12px">
          在影片详情的「评论区分享」里点分享者的名字，就能关注 TA。
        </div>
      </div>
      <div v-else class="sub-card-grid">
        <div v-for="u in followedUsers" :key="u.user_id" class="jav-card sub-card sub-card--avatar">
          <div class="sub-card__cover jav-card__cover" @click="openUserShares(u)">
            <!-- 用户没有头像图，圆里放名字的前两个字。底色与清单的紫区分开，
                 一眼分得出「这是人」还是「这是片单」。 -->
            <div class="jav-actor-avatar">
              <span class="jav-actor-avatar__text jav-actor-avatar__text--user">
                {{ userAvatarText(u.username) }}
              </span>
            </div>
          </div>
          <div class="jav-card__body">
            <div class="jav-actor-name" :title="u.username">{{ u.username || "匿名" }}</div>
            <div class="sub-card__foot">
              <span class="sub-card__foot-left">
                <i class="fas fa-share" />
                分享 {{ u.share_count }} 条
              </span>
              <span class="sub-card__foot-right">
                {{ (u.created_at || "").slice(0, 10) }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <JavUserSharesModal
        :open="sharesOpen"
        :user-id="sharesUserId"
        :username="sharesUsername"
        :followed="sharesFollowed"
        @close="sharesOpen = false"
        @open-movie="openMovieFromUserShares"
        @follow-changed="onUserFollowChanged"
      />
    </template>

    <!-- 影片（含在线订阅） / 演员 / 清单 -->
    <template v-else>
      <div v-if="loading" class="jav-empty">加载中…</div>
      <div v-else-if="!filtered.length" class="jav-empty">
        <div class="jav-empty__icon">🚧</div>
        <div>这一档还没有订阅。</div>
      </div>
      <div v-else class="sub-card-grid">
        <div
          v-for="sub in filtered"
          :key="sub.id"
          class="jav-card sub-card"
          :class="{
            'sub-card--avatar': usesAvatarCard(sub),
            'sub-card--blacklisted': isBlacklisted(sub),
          }"
        >
          <div
            class="sub-card__cover jav-card__cover"
            :aria-disabled="isBlacklisted(sub) ? 'true' : undefined"
            @click="onCardClick(sub)"
          >
            <!-- 演员/清单订阅没有横版封面，头像居中摆成圆的 —— 与榜单的演员卡同一形态。 -->
            <div v-if="usesAvatarCard(sub)" class="jav-actor-avatar">
              <img
                v-if="sub.cover"
                :src="javImageURL(sub.cover)"
                loading="lazy"
                :alt="sub.target_name"
              />
              <!-- 没有图时退成文字：演员给 👤，清单给名字的前两个字。
                   清单多取一个字，是因为中文清单名一个字太含糊。 -->
              <span v-else-if="sub.target_type === 'list'" class="jav-actor-avatar__text">
                {{ listAvatarText(sub.target_name) }}
              </span>
              <span v-else>👤</span>
            </div>
            <template v-else>
              <img
                v-if="sub.cover"
                :src="javImageURL(sub.cover)"
                loading="lazy"
                :alt="sub.target_name"
              />
              <div v-else class="jav-card__placeholder">
                {{ javTargetLabel(sub.target_type) }}
              </div>
            </template>

            <!-- 左上：订阅状态 -->
            <span
              class="sub-card__status"
              :class="checking.has(sub.id) ? 'sub-card__status--checking' : statusClass(sub)"
            >
              {{ statusText(sub) }}
            </span>

            <!-- 右上：黑名单角标。**常驻**（不像动作组那样悬停才出）—— 它是状态不是动作。
                 撞位由 CSS 解：拉黑的卡通把悬停动作组挤到下一行。 -->
            <span v-if="isBlacklisted(sub)" class="sub-card__badge">
              <i class="fas fa-ban" /> 黑名单
            </span>

            <!-- 右上：检查 / 编辑 / 开始·暂停订阅 / 删除（悬停才出）。
                 **被拉黑时整组禁用** —— 这张卡已经不参与推送了，管理它也没有意义；
                 要恢复就把黑名单那一档里的条目移出（取消黑名单后这些按钮自己就活了）。 -->
            <div class="sub-card__acts sub-card__acts--tr" @click.stop>
              <button
                type="button"
                class="sub-card__icon"
                :disabled="checking.has(sub.id) || isBlacklisted(sub)"
                title="检查"
                @click="silentCheck(sub)"
              >
                <i :class="checking.has(sub.id) ? 'fas fa-spinner fa-spin' : 'fas fa-sync-alt'" />
              </button>
              <button
                type="button"
                class="sub-card__icon"
                :disabled="isBlacklisted(sub)"
                title="编辑"
                @click="openEdit(sub)"
              >
                <i class="fas fa-pen" />
              </button>
              <button
                type="button"
                class="sub-card__icon"
                :disabled="isBlacklisted(sub)"
                :title="sub.status === 'paused' ? '开始订阅' : '暂停订阅'"
                @click="toggleStatus(sub)"
              >
                <i :class="sub.status === 'paused' ? 'fas fa-play' : 'fas fa-pause'" />
              </button>
              <button
                type="button"
                class="sub-card__icon sub-card__icon--danger"
                :disabled="isBlacklisted(sub)"
                title="删除"
                @click="remove(sub)"
              >
                <i class="fas fa-trash" />
              </button>
            </div>

            <!-- 右下：候选 / 黑名单。与右上那一组一样，被拉黑时全部禁用。
                 要取消黑名单，去「黑名单」那一档点「移出黑名单」——
                 取消之后这张卡自己就恢复正常了。 -->
            <div class="sub-card__acts sub-card__acts--br" @click.stop>
              <button
                type="button"
                class="sub-card__icon"
                :disabled="isBlacklisted(sub)"
                title="候选"
                @click="loadCandidates(sub)"
              >
                <i class="fas fa-list" />
              </button>
              <button
                type="button"
                class="sub-card__icon sub-card__icon--danger"
                :disabled="isBlacklisted(sub)"
                title="加入黑名单"
                @click="addBlacklist(sub)"
              >
                <i class="fas fa-ban" />
              </button>
            </div>
          </div>

          <div class="jav-card__body">
            <!-- 演员/清单订阅头一行就是名字（榜单演员卡也是这个形态），
                 不带番号/日期 —— 那两个对它们没意义。 -->
            <div
              v-if="usesAvatarCard(sub)"
              class="jav-actor-name"
              :title="sub.target_name"
            >
              {{ sub.target_name }}
            </div>
            <template v-else>
              <div class="jav-card__meta">
                <span class="jav-card__num">{{ sub.number || javTargetLabel(sub.target_type) }}</span>
                <span v-if="sub.release_date" class="jav-card__date">{{ sub.release_date }}</span>
              </div>
              <div class="jav-card__title" :title="sub.target_name">{{ sub.target_name }}</div>
            </template>

            <!-- 左=最后一次检查时间（与影片卡同款），右=两个数：
                 检 = 这一轮匹配到多少条磁链，推 = 已**成功到网盘**多少部
                 （离线任务完成才算，见后端 onOfflineDownloadCompleted）。 -->
            <div class="sub-card__foot">
              <span class="sub-card__foot-left" title="最后检查时间">
                <i class="fas fa-clock" />
                {{ lastCheckedText(sub) }}
              </span>
              <span
                class="sub-card__foot-right"
                :class="{ 'sub-card__foot-error': sub.last_error }"
                :title="sub.last_error || '检＝这一轮匹配到的磁链条数；推＝已成功到网盘的影片数'"
              >
                <span>检：{{ sub.matched_count || 0 }}</span>
                <span>推：{{ sub.pushed_count || 0 }}</span>
                <!-- 「下：N」（已提交、还在网盘下载的部数）**去掉了** ——
                     它和左边的「最后检查时间」挤在一行，把「检/推」挤出可视区。
                     代价是从提交到下载完成那段窗口里卡片只剩「推：0」，
                     看着像没推成功；要治的话得把「下」并进「推」显示
                     （如「推：3 (+2)」），而不是再加一个词。 -->
              </span>
            </div>
          </div>
        </div>
      </div>
    </template>

    <!-- 条件弹窗 -->
    <Teleport to="body">
      <div v-if="formOpen" class="jav-modal jav-modal--above-drawer" @click.self="formOpen = false">
        <div class="jav-modal__panel">
          <div class="jav-modal__head">
            {{ editingId ? "编辑订阅" : "创建订阅" }}
            <button
              type="button"
              class="jav-modal__close"
              style="margin-left: auto"
              aria-label="关闭"
              @click="formOpen = false"
            >
              ×
            </button>
          </div>
          <div class="jav-modal__body">
            <label class="jav-field">
              <span>订阅类型</span>
              <!-- 没有「在线订阅」这一项：库里一条都没有，它的解析路径又与影片订阅完全相同
                   （check.go 的 resolveTarget 把 movie / online 走同一分支），没必要单列。 -->
              <AppSelect
                v-model="form.target_type"
                :options="[
                  { value: 'movie', label: '影片订阅' },
                  { value: 'actor', label: '演员订阅' },
                  { value: 'list', label: '清单订阅' },
                ]"
              />
            </label>

            <label class="jav-field">
              <span>名称</span>
              <AppInput v-model="form.target_name" placeholder="番号、演员或清单名称" />
            </label>

            <div class="jav-field">
              <span>下载模式</span>
              <AppSelect
                v-model="modeValue"
                :options="JAV_DOWNLOAD_MODES.map((m) => ({ value: m.value, label: m.label }))"
              />
            </div>
            <div class="jav-hint">
              严格模式要求所有条件满足；洗版模式只在库里有更优资源时重推；预下载先挑一颗相对最优的等你确认。
            </div>

            <div class="jav-field">
              <span>质量</span>
              <div style="display: flex; gap: 8px; flex-wrap: wrap">
                <button
                  v-for="q in JAV_QUALITY_OPTIONS"
                  :key="q.value"
                  type="button"
                  class="jav-sort-chip"
                  :class="{ 'jav-sort-chip--active': form.qualities.includes(q.value) }"
                  @click="toggleQuality(q.value)"
                >
                  {{ q.label }}
                </button>
              </div>
            </div>
            <div class="jav-hint">不勾就是不限。勾多项时要求**同时**满足（与逻辑）。</div>

            <div class="jav-field">
              <span>文件大小（MB）</span>
              <div style="display: flex; gap: 8px; align-items: center">
                <AppInput v-model.number="form.min_size_mb" type="number" placeholder="最小" />
                <span>—</span>
                <AppInput v-model.number="form.max_size_mb" type="number" placeholder="最大" />
              </div>
            </div>

            <div class="jav-field">
              <span>上映日期</span>
              <div style="display: flex; gap: 8px; align-items: center">
                <AppInput v-model="form.release_date_from" type="date" />
                <span>—</span>
                <AppInput v-model="form.release_date_to" type="date" />
              </div>
            </div>

            <!-- 放在上面那几个条件之后：这一栏说的是「那些条件对评论链接怎么用」，
                 排在它们前面会让人在还不知道放宽什么的情况下先勾。 -->
            <div class="jav-field">
              <span>评论区链接</span>
              <label style="display: inline-flex; gap: 6px; align-items: center; font-size: 12.5px">
                <input v-model="form.include_comment_links" type="checkbox" />
                把影片评论区里分享的链接也纳入候选
              </label>
            </div>
            <div class="jav-hint">
              只对**本地已经抓过评论**的影片生效（在详情页打开过「评论区分享」，或后台铺评论扫到过它）。
              打开这个开关**不会**去上游多抓任何东西。
            </div>
            <div class="jav-hint">
              评论里的链接通常没有分辨率角标、文件数和体积，这些**缺的项跳过不判**（不会因此被拒），
              其余条件照常 —— 所以它会让这条订阅收进来的资源变多。
            </div>

            <div class="jav-field">
              <span>推送目标</span>
              <div style="display: flex; gap: 8px; align-items: center">
                <AccountFolderField
                  :display="targetText"
                  title="选择这条订阅推送到哪个网盘目录"
                  placeholder="留空 = 用默认推送目标"
                  @browse="pickerOpen = true"
                />
                <AppButton v-if="form.target_account_id" variant="ghost" size="sm" @click="clearTarget">
                  清除
                </AppButton>
              </div>
            </div>
            <div class="jav-hint">
              不选就用「番号相关设置 → 数据源」里的默认目标。推送时会在该目录下按番号建子目录。
            </div>

            <div class="jav-field">
              <span>子目录</span>
              <AppSelect
                v-model="form.subfolder_mode"
                :options="[
                  { value: 'code', label: '按番号（SSIS-001/）' },
                  { value: 'title', label: '按片名' },
                  { value: 'none', label: '不建子目录' },
                ]"
              />
            </div>
          </div>
          <div class="jav-modal__foot">
            <AppButton variant="ghost" @click="formOpen = false">取消</AppButton>
            <AppButton :disabled="saving" @click="save">{{ saving ? "保存中…" : "保存" }}</AppButton>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 检查结果 / 候选弹窗 -->
    <Teleport to="body">
      <div v-if="checkOpen" class="jav-modal" @click.self="checkOpen = false">
        <div class="jav-modal__panel" style="max-width: 860px">
          <div class="jav-modal__head">
            {{ checkTitle }} · 候选资源
            <div style="display: flex; gap: 6px; margin-left: auto; margin-right: 8px">
              <AppButton
                v-if="checkSub"
                variant="ghost"
                size="sm"
                @click="execute(checkSub)"
              >
                执行订阅
              </AppButton>
              <AppButton
                v-if="checkSub && checkSub.mode === 'predownload'"
                size="sm"
                @click="confirmPreDownload(checkSub)"
              >
                确认推送
              </AppButton>
            </div>
            <button
              type="button"
              class="jav-modal__close"
              style="margin-left: auto"
              aria-label="关闭"
              @click="checkOpen = false"
            >
              ×
            </button>
          </div>
          <div class="jav-modal__body">
            <div v-if="candidates.length === 0" class="jav-empty">没有候选。</div>
            <div
              v-for="c in candidates"
              :key="c.id"
              class="jav-candidate"
              :class="{ 'jav-candidate--rejected': !c.push_ok }"
            >
              <div class="jav-candidate__main">
                <div class="jav-candidate__name">{{ c.magnet_name }}</div>
                <div class="jav-candidate__meta">
                  <span v-if="c.movie_number">{{ c.movie_number }}</span>
                  <span>{{ c.size_text || "体积未知" }}</span>
                  <!-- 清晰度角标与磁链卡片同款：4K / UHD / HD 只挂一颗。 -->
                  <span v-if="c.resolution_badge" class="jav-badge jav-badge--res">
                    {{ c.resolution_badge }}
                  </span>
                  <span v-if="c.uncensored" class="jav-badge jav-badge--break">破解</span>
                  <span v-if="c.subtitle" class="jav-badge jav-badge--on">字幕</span>
                  <span v-if="c.push_ok" class="jav-badge jav-badge--on">合格</span>
                  <span v-if="c.predownload" class="jav-badge">待确认</span>
                  <span v-if="c.attempted" class="jav-badge">已试过</span>
                  <!-- 来源角标：这个弹窗回答的正是「为什么挑了这颗」，
                       而「它是评论里来的、条件被放宽过」是这个答案的一半。 -->
                  <span
                    v-if="c.from_comment"
                    class="jav-badge"
                    title="来自影片评论区的用户分享，不是 JAVBUS 的磁力"
                  >
                    评论区
                  </span>
                  <!-- 推送键**贴着这一排的右端**，而不是另起一行挂在卡片下面 ——
                       它和体积、角标读的是同一件事（这颗资源怎么样），
                       并排看才是一句话；另起一行会把每张候选卡都撑高一截。
                       源码也只给「合格」与「待确认」两类候选这个按钮：
                       不合格的推了也是白推，给一个必然失败的按钮不如不给。 -->
                  <AppButton
                    v-if="c.push_ok || c.predownload"
                    class="jav-candidate__push"
                    size="sm"
                    :disabled="pushing"
                    @click="pushOneCandidate(c)"
                  >
                    推送网盘
                  </AppButton>
                </div>
                <!-- 拒收原因给人话版本：直接展示 below_min_size 用户看不懂，
                     而「为什么这颗没被推」恰恰是他最需要看懂的。 -->
                <div v-if="!c.push_ok && c.rejection_reasons_text.length" class="jav-candidate__reasons">
                  {{ c.rejection_reasons_text.join("；") }}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <FolderPickerModal
      :open="pickerOpen"
      title="选择推送目录"
      confirm-text="用这个目录"
      selectable-account
      :accounts="accountsStore.accounts"
      :account-id="form.target_account_id || null"
      allow-create-folder
      @close="pickerOpen = false"
      @resolve="onTargetPicked"
    />

    <!-- 影片子弹窗（演员/清单订阅） -->
    <Teleport to="body">
      <div v-if="moviesOpen" class="jav-modal" @click.self="closeMoviesModal">
        <div class="jav-modal__panel jav-modal__panel--steady" style="max-width: 1000px">
          <div class="jav-modal__head">
            {{ moviesSub?.target_name }} · 影片
            <!-- 不过条件的影片（日期窗、清晰度、类别挡掉的）**不列**：卡片上「检：N」
                 数的就是过条件的那些，列出来两者对不上，用户只会觉得条件没生效。
                 但也不能让它悄无声息地少几部 —— 于是留一句数量说明。 -->
            <span
              v-if="!moviesLoading && ineligibleCount > 0"
              class="jav-modal__head-hint"
              :title="`这些影片没过本订阅的条件（日期窗、清晰度、类别、黑名单…），不会被推送，所以不在这里列出`"
            >
              另有 {{ ineligibleCount }} 部没过条件
            </span>
            <!-- 关闭键与「新建整理任务」那类弹窗同一形态（AppModal 的 .modal__close）：
                 无边框、透明的 ×，悬停才变成正文色。 -->
            <button
              type="button"
              class="jav-modal__close"
              style="margin-left: auto"
              aria-label="关闭"
              @click="moviesOpen = false"
            >
              ×
            </button>
          </div>
          <!-- 工具栏放在**内容区里面**的第一行：它是页面内容，不是弹窗的边框。 -->
          <div class="jav-modal__body">
          <!-- 状态分页 + 操作菜单（照 21.png / 22.png）。
               分页是「这个订阅下影片的推进状态」：跳过的只在「跳过」页出现，
               不在「订阅中」页占地方。 -->
          <div v-if="!moviesLoading && subMovies.length > 0" class="jav-modal__tabs">
            <div class="jav-sort-chips">
              <button
                v-for="t in MOVIE_STATUS_TABS"
                :key="t.key"
                type="button"
                class="jav-sort-chip"
                :class="{ 'jav-sort-chip--active': movieStatusTab === t.key }"
                @click="movieStatusTab = t.key"
              >
                {{ t.label }}（{{ statusCounts[t.key] }}）
              </button>
            </div>
            <AppDropdown :items="opsItems" align="right" @select="onOpsSelect">
              <template #trigger="{ toggle }">
                <button type="button" class="jav-sort-chip" :disabled="batchRunning" @click="toggle">
                  ⚙ 操作 <span>{{ batchRunning ? "" : "▾" }}</span>
                </button>
              </template>
            </AppDropdown>
          </div>

          <!-- 批量选择条：勾了之后能一次跳过 / 取消跳过 / 逐部执行订阅。 -->
          <div v-if="(batchMode || batchRunning) && !moviesLoading" class="jav-batch-bar">
            <template v-if="batchRunning">
              <span class="jav-batch-bar__text">{{ batchProgress }}</span>
            </template>
            <template v-else>
              <span class="jav-batch-bar__text">已选 {{ batchSelected.size }} 部</span>
              <AppButton size="sm" variant="ghost" :disabled="batchSelected.size === 0" @click="batchSkip(true)">
                跳过
              </AppButton>
              <AppButton size="sm" variant="ghost" :disabled="batchSelected.size === 0" @click="batchSkip(false)">
                取消跳过
              </AppButton>
              <AppButton size="sm" :disabled="batchSelected.size === 0" @click="batchSubscribe">
                执行订阅
              </AppButton>
              <AppButton size="sm" variant="ghost" @click="toggleBatchMode">退出</AppButton>
            </template>
          </div>

            <div v-if="moviesLoading" class="jav-empty">
              {{ moviesSlow ? "本地还没有这个订阅的影片，正在去抓一次，可能要几十秒…" : "加载中…" }}
            </div>
            <div v-else-if="subMovies.length === 0" class="jav-empty">这个订阅下还没有影片。</div>
            <div v-else-if="statusFiltered.length === 0" class="jav-empty">
              这个订阅下没有「{{ movieStatusTabLabel }}」的影片。
            </div>
            <div v-else-if="visibleSubMovies.length === 0" class="jav-empty">
              这一页里的 {{ ineligibleInTab }} 部影片都没过本订阅的条件（日期窗、清晰度、
              类别…），不会被推送，所以不列出。
            </div>
            <div v-else class="jav-grid">
              <div v-for="movie in visibleSubMovies" :key="movie.id">
                <!-- 批量模式下点卡片是勾选，不是打开详情（见 onCardClickCapture）。
                     捕获阶段拦下来，省得给通用卡片加一个「禁用点击」的 prop。 -->
                <div
                  class="jav-sub-movie"
                  :class="{ 'jav-sub-movie--picked': batchSelected.has(movie.id) }"
                  @click.capture="onCardClickCapture($event, movie)"
                >
                  <JavMovieCard :movie="movie" :show-subscribe="false" @open="emit('open', movie)" />

                  <!-- 批量模式：左上角让给勾选圈。状态这时由上面的分页胶囊说明，
                       不必两个角标挤在同一个角上。 -->
                  <span v-if="batchMode" class="jav-sub-movie__tick">
                    {{ batchSelected.has(movie.id) ? "✓" : "" }}
                  </span>
                  <!-- 状态：封面左上角。 -->
                  <span
                    v-else
                    class="jav-sub-movie__state"
                    :class="`jav-sub-movie__state--${movie.sub_status}`"
                  >
                    {{ javStatusLabel(movie.sub_status) }}
                  </span>

                  <!-- 跳过 / 取消跳过：封面右上角。
                       逐部的「执行订阅」**去掉了** —— 推片走「操作 → 执行订阅」：
                       勾了谁就推谁，没勾就推当前这一页还没推的（批量选择见操作菜单）。 -->
                  <button
                    type="button"
                    class="jav-sub-movie__skip"
                    :class="{ 'jav-sub-movie__skip--on': movie.sub_status === 'skipped' }"
                    :disabled="batchRunning"
                    :title="
                      movie.sub_status === 'skipped'
                        ? '取消跳过，让它回到可推送的队列'
                        : '跳过这一部，之后不再推它'
                    "
                    @click.stop="toggleSkip(movie)"
                  >
                    {{ movie.sub_status === "skipped" ? "取消跳过" : "跳过" }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 黑名单的影片快照（**只读**）。
         与订阅那张影片弹窗同一形态，但没有「⚙ 操作」、没有批量选择、没有状态分页 ——
         这些片在匹配上已经全部不合格，列出来只为让用户知道「这一拉挡住了哪些」。
         卡片也不接 open：详情抽屉是 elevated（10040），从这个弹窗里点开会被它盖住。 -->
    <Teleport to="body">
      <div v-if="blacklistMoviesOpen" class="jav-modal" @click.self="blacklistMoviesOpen = false">
        <div class="jav-modal__panel jav-modal__panel--steady" style="max-width: 1000px">
          <div class="jav-modal__head">
            {{ blacklistMoviesTitle }} · 被挡住的 {{ blacklistMovies.length }} 部影片
            <button
              type="button"
              class="jav-modal__close"
              style="margin-left: auto"
              aria-label="关闭"
              @click="blacklistMoviesOpen = false"
            >
              ×
            </button>
          </div>
          <div class="jav-modal__body">
            <div v-if="blacklistMoviesLoading" class="jav-empty">加载中…</div>
            <div v-else-if="!blacklistMovies.length" class="jav-empty">
              这条黑名单名下没有影片（本地一条都没抓过，或是从链接加的、没有目标 id）。
            </div>
            <div v-else class="jav-grid">
              <JavMovieCard
                v-for="movie in blacklistMovies"
                :key="movie.id"
                :movie="movie"
                :show-subscribe="false"
              />
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.jav-modal {
  position: fixed;
  inset: 0;
  z-index: 2500;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 23, 42, 0.48);
  padding: 24px;
}

/* 「建订阅」那张弹窗要比**影片详情抽屉**高。
 *
 * 抽屉里那颗「订阅」心走的是 toggleJavSubscribe → 切到订阅页 + 打开这张弹窗，
 * 而详情抽屉是 elevated 的（AdminSettingsDrawer，10040）。弹窗若还是 2500，
 * 就整个藏在抽屉背后 —— 用户看到的是「点了没反应」。
 *
 * 值卡在 `--z-dropdown - 5`（10045）这一档，两头都得顾：
 *   上：要高过抽屉的 10040；
 *   下：要低于 --z-dropdown（10050）—— 这张表单里有下拉选择（模式、画质…），
 *       它们浮在 --z-dropdown 那一层，弹窗高过它的话，菜单会藏在弹窗背后。
 *
 * **只抬这一张**，别去动 .jav-modal：「影片子弹窗」必须留在详情抽屉**下面**，
 * 因为详情正是从那张弹窗里点开的（见 AdminSettingsDrawer 的 elevated 注释）。
 *
 * 写在 scoped 块里而不是 jav.css：要和上面的 .jav-modal 同特异性、靠源码顺序取胜；
 * 挪到全局样式表里反而会被这里带 scoped 属性的规则压住。 */
.jav-modal--above-drawer {
  z-index: calc(var(--z-dropdown) - 5);
}

.jav-modal__panel {
  width: 100%;
  max-width: 620px;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
  overflow: hidden;
}

.jav-modal__head {
  display: flex;
  align-items: center;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
  font-size: 14px;
  font-weight: 600;
}

.jav-modal__body {
  padding: 16px 18px;
  overflow-y: auto;
}

.jav-modal__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid var(--border);
}

.jav-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin-bottom: 12px;
  font-size: 12.5px;
  color: var(--text-regular);
}

.jav-hint {
  margin: -6px 0 12px;
  font-size: 11.5px;
  color: var(--text-muted);
  line-height: 1.55;
}
</style>
