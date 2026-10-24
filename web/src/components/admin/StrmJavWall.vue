<script setup lang="ts">
import { computed, nextTick, onActivated, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import type { WallSourceRef } from "@/api/strmScrape";
import {
  fetchJavWallItems,
  fetchJavWallOnlineScrapeProgress,
  onlineScrapeJavWall,
  rebuildJavWallItem,
  refreshJavWall,
  type JavWallItem,
  type JavWallListResult,
  type JavWallOnlineScrapeProgress,
  type JavWallScrapeItem,
} from "@/api/strmJavWall";
import AppButton from "@/components/base/AppButton.vue";
import AppContextMenu from "@/components/base/AppContextMenu.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import type { DropdownMenuItem } from "@/types/menu";
import StrmJavMetaDrawer from "@/components/admin/StrmJavMetaDrawer.vue";
import StrmJavWallDeleteModal from "@/components/admin/StrmJavWallDeleteModal.vue";
import JavWallActorDetailDrawer from "@/components/admin/JavWallActorDetailDrawer.vue";
import type { JavWallActorCard, JavWallActorMovie } from "@/api/strmJavWall";
import type { WallActor } from "@/api/strmScrape";
import { confirm } from "@/composables/useConfirm";
import { findScrollBox, useFillViewport } from "@/composables/useFillViewport";
import { toast } from "@/composables/useToast";
import "@/styles/jav.css";

// 番号影片的海报墙（辅助工具 → 海报墙，任务媒体类型 = 番号影片时由 StrmScrapePanel 挂上来）。
//
// 与 TMDB 那面墙的区别都来自用户的要求：
//   * 「类型」不是电影/剧集，而是**磁盘上实际存在的一级目录**（有码/无码/欧美/国产…）；
//     哪些目录整档不显示由用户自己勾（页头「全部目录」按钮 / STRM 设置里那一行），
//     「未匹配」默认已勾 —— 它取的是分类规则里那条兜底规则的目标目录，改名会自动跟上。
//   * 多分片目录（`91CM-109-cd1/cd2/cd3`）**每个分片一张卡**，三张都显示 cd1 的图。
//   * 缩略图 / 海报图 两种视图切换（纯前端，两个 URL 都在数据里）。
//   * 鼠标指过去出「编辑」「重刮」。

// 搜索与排序由**页头**统一控制（用户要求：番号墙自己的那两个框不要了）。
// 子组件只按这三个 prop 请求 —— 页头那套按钮对两种任务长得一模一样，功能各是各的。
import { useWallDetail } from "@/composables/useWallDetail";
import WallDetailDrawer from "@/components/admin/WallDetailDrawer.vue";
import WallPlayer from "@/components/admin/WallPlayer.vue";

const props = defineProps<{
  /** 数据源：任务 id 或 `{ local_dir_id }`（见 api 的 WallSourceRef）。 */
  source: number | WallSourceRef;
  keyword?: string;
  sort?: string;
}>();

/** 隐藏名单变了（或拿到新的一份）时把名单抛给外面：页头那颗按钮要显示「隐藏 N 个目录」。 */
const emit = defineEmits<{
  "hidden-dirs": [string[]];
}>();

const loading = ref(false);
const refreshing = ref(false);
/**
 * 这一次加载已经等了一会儿了（超过 4 秒）。
 *
 * 扫盘是**按目录读**的，而数据源可能是网盘 / SMB 上的目录（「本地目录」那个入口）——
 * 那里每读一层目录都要走一次网络，几百部作品的库冷扫十几秒是正常的。只显示一句
 * 「加载中…」的话，用户会以为卡死了。等过一会儿再补一句解释，比一直干等强。
 *
 * 只在**首屏**（`loading`）时出现，续加载（`loadingMore`）不弹 —— 那一屏一屏地滚，
 * 用户已经知道在加载什么了。
 */
const slowHint = ref(false);
let slowHintTimer: number | undefined;

function armSlowHint() {
  window.clearTimeout(slowHintTimer);
  slowHint.value = false;
  slowHintTimer = window.setTimeout(() => {
    slowHint.value = true;
  }, 4000);
}

function clearSlowHint() {
  window.clearTimeout(slowHintTimer);
  slowHint.value = false;
}
const result = ref<JavWallListResult | null>(null);
const category = ref<string>("");
const view = ref<"thumb" | "poster">("poster");
const busyStem = ref<string>("");

// ————————————————————— 分页 —————————————————————
//
// 后端 `listJavWall` 本来就有 offset / limit / has_more，这里原来只调一次、写死
// limit=200、不带 offset —— 于是**超过 200 张的部分永远看不到，界面上还没有任何提示**
// （属于「静默变空」那一族）。现在改成拉到底续加载。
//
// 每页 200 = 后端上限（`maxItemListLimit`）。首次就把上限取满，是为了让手机竖屏
// 也一次备够十屏，滚动时几乎碰不到「正在加载」。
const PAGE_LIMIT = 200;
const loadingMore = ref(false);
/** 还有没有下一页 —— 来自后端，不靠「这一页拿满了没」去猜。 */
const hasMore = ref(false);
/** 过滤后的总条数（后端报的），底部状态行要显示「还有 N 条」。 */
const total = ref(0);

const drawerOpen = ref(false);
const editing = ref<JavWallItem | null>(null);

const items = computed(() => result.value?.items ?? []);
const categories = computed(() => result.value?.categories ?? []);

/** 底部状态行该不该占位置。首屏还在转圈时不显示，免得底部先冒一条文案又跳走。 */
const showLoadMore = computed(() => !loading.value && items.value.length > 0);

/** 把隐藏名单抛给外面（页头那颗「全部目录」按钮要显示「隐藏 N 个目录」）。 */
function publishHiddenDirs() {
  emit("hidden-dirs", result.value?.hidden_dirs ?? []);
}

/**
 * 取一页。`append` 为真时接在现有列表后面（拉到底续加载）。
 *
 * ⚠️ 追加时**不能**把 `loading` 置真：模板里 `v-if="loading"` 会把整片网格换成
 * 「加载中…」，用户每滚一屏就看到列表闪一下没了。所以另开 `loadingMore`。
 */
async function load(options: { append?: boolean } = {}) {
  if (!props.source) {
    // ⚠️ 守卫里也要把 loading 收掉。`resetAndLoad` 是同步置 `loading=true` 再调这里的，
    // 没有 source 时就早退了 —— 不收的话那一份「加载中」会永远挂在屏上
    // （换数据源的那一瞬可达）。它原来就漏了，这里顺手补上（纯修 bug，与批量选择无关）。
    loading.value = false;
    return;
  }
  const append = Boolean(options.append);
  if (append) {
    if (loading.value || loadingMore.value || !hasMore.value) return;
    loadingMore.value = true;
  } else {
    loading.value = true;
    armSlowHint();
  }
  try {
    const next = await fetchJavWallItems(props.source, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "added_desc",
      limit: PAGE_LIMIT,
      offset: append ? (result.value?.items.length ?? 0) : 0,
    });
    if (append && result.value) {
      // 按 id 去重再拼：快照 TTL 是 60 秒，跨过一次重扫顺序可能挪位，
      // 重复的 key 会让 Vue 报「Duplicate keys」并且渲染错位。
      const seen = new Set(result.value.items.map((it) => it.id));
      result.value.items = [...result.value.items, ...next.items.filter((it) => !seen.has(it.id))];
      result.value.has_more = next.has_more;
      result.value.total = next.total;
      // 追加这条**可以**靠 length watcher（长度一定变了）—— 但统一走这一手更省心。
      void nextTick(afterListReplaced);
    } else {
      result.value = next;
      // ⚠️ 换了一批时**不能**指望 length watcher：新旧长度相同时它不触发，
      // 而 resetAndLoad 已经把取图窗口清空了 —— 那面墙就只剩骨架（用户 2026-10-17 报的）。
      void nextTick(afterListReplaced);
    }
    hasMore.value = Boolean(result.value?.has_more);
    total.value = result.value?.total ?? 0;
    publishHiddenDirs();
  } catch (error) {
    // 追加失败保持已有内容 —— 滚到一半一次请求失败，不该把看过的一屏清空。
    if (!append) {
      toast.error(getApiErrorMessage(error, "读取番号海报墙失败"));
      result.value = null;
      hasMore.value = false;
      total.value = 0;
      publishHiddenDirs();
    }
  } finally {
    loading.value = false;
    loadingMore.value = false;
    clearSlowHint();
  }
}

// —— 批量选择（只刮选中的那几部）——
//
// 用户提的：整根扫一遍太慢，只想补几部新片。开着的时候点卡片 = 勾选（不是开详情），
// 卡片正中出勾选圈；页头那颗按钮显示已选部数，再点「在线刮削」就只刮这几部。
//
// 选中集合**自己存 `rel_dir` + `stem`**，不存 `item.id`：
//   * 切分类 tab 要**保留已选、跨分类累加**（用户拍板）—— 而 `id` 是
//     `pathToItemID(joinRel(relDir, stem))` 哈希来的，从 id 反推不回 rel_dir；
//   * 换源之后旧的勾选没有意义（路径按源算），所以要能被整体清掉。

const batchMode = ref(false);
/** key = pickKey(item) → 「点名刮这一部」的那两个字段。 */
const picked = ref(new Map<string, JavWallScrapeItem>());

/**
 * 这一部在选择集里的键。
 *
 * ⚠️ **不要用 `item.id`**：那是 `pathToItemID(joinRel(relDir, stem))`，分源不唯一，
 * 而且切 tab 之后从它反推不回 `rel_dir`（提交时服务端要的正是 rel_dir + stem）。
 */
function pickKey(item: JavWallItem): string {
  return `${item.rel_dir}\u0000${item.stem}`;
}

const pickedCount = computed(() => picked.value.size);

// ⚠️ 只给**画勾选圈**用（卡片是不是亮着）。而且只认当前这一张卡所在的 tab：
// 整分类态下「本分类全部选中」，但**别的**分类的卡不该跟着亮（用户会以为下面那些
// 也会被刮 —— 提交时服务端只认 `category` 那一档，别的分类确实不动）。
//
// ⚠️ **两种语义要一起翻面**，判据就是 `wholeCategoryOn`（它与 `picked` 是同一条规矩的
// 两半：整分类态开着时 `picked` 装的是排除集）：
//   * 点名态：`picked` 里有 = 选中；
//   * 整分类态：`picked` 里**有** = 被**排除**了 —— 这一档默认全选，点一下是取消。
// 写成「有就亮」的话，用户点掉一张卡它会**继续亮着**（而提交时它确实不被刮）。
function isPicked(item: JavWallItem): boolean {
  if (wholeCategoryOn.value && item.category === wholeCategory.value) {
    return !picked.value.has(pickKey(item));
  }
  return picked.value.has(pickKey(item));
}

/**
 * 「提交那一刻」冻结下来的部数 —— 按钮上那个 `刮削选中（N）`。
 *
 * 为什么冻结：提交成功时会**立刻**清掉选择集（用户看得到的收尾），而轮询要跑好几分钟
 * —— 文案里那个 N 得是「你提交了几部」，不是「现在还剩几部」（后者恒为 0）。
 */
const submittedCount = ref(0);

/**
 * 这一轮提交的是哪几部（**没提交就是 null**）。
 *
 * 为什么要这个「我到底起没起任务」的硬信号：服务端同一数据源不能并发，已在跑时
 * 它返回的是**别人那轮**的进度。若不区分，别人整根任务跑完会把用户当前的选择集
 * 当成本次提交的结果吃掉（选择被清空了，却一部都没刮）。
 *
 * 普通 `let`（不是 ref）—— 它不参与渲染，照 `adoptInFlight` 的写法。
 */
let pendingBatchItems: JavWallScrapeItem[] | null = null;

/** 收回「这一轮是我起的」这个标记（三条收尾分支都要走一次）。 */
function clearPendingBatch(): JavWallScrapeItem[] | null {
  const items = pendingBatchItems;
  pendingBatchItems = null;
  submittedCount.value = 0;
  return items;
}

/** 退出批量选择：顺手把选择集清掉（用户点「取消」时不该留着一堆看不见的勾）。 */
function exitBatchMode() {
  batchMode.value = false;
  picked.value = new Map();
  submittedCount.value = 0;
  // 「整档都要」也是选择态的一部分，一起撤（它的排除集就是上面那份 picked，
  // 已经清了；留着标记的话再开批量选择会莫名其妙地「整个分类都要」）。
  wholeCategory.value = null;
}

function toggleBatchMode() {
  // 刮削期间不许退出：收尾要靠这份选择集来清场，中途退了会留下一堆
  // 「看不见但仍在选中」的条目。页头那颗按钮同时也禁用了，这里再兜一道。
  if (onlineScraping.value) return;
  if (batchMode.value) exitBatchMode();
  else batchMode.value = true;
}

/**
 * 点一张卡：加进 / 移出 `picked`。
 *
 * ⚠️ 整分类态开着时这一份的语义**翻面**成「这一档里排除掉的」（见上面那一大段），
 * 所以同一个动作在那时读作「取消这一部」——`isPicked` 在那一档里是取反的，
 * 视觉与语义对得上（点掉一张卡，勾就没了，提交时它也确实不被刮）。
 */
function togglePick(item: JavWallItem) {
  const next = new Map(picked.value);
  const key = pickKey(item);
  if (next.has(key)) next.delete(key);
  else next.set(key, { rel_dir: item.rel_dir, stem: item.stem });
  picked.value = next;
}

/**
 * 点卡片：批量选择开着时是**勾选**，否则开详情。
 *
 * 所有点击路径（鼠标 / Enter / Space）都收敛到这一个函数，判据只有一份。
 */
function onCardClick(item: JavWallItem) {
  if (batchMode.value) togglePick(item);
  else wall.openJav(props.source, item);
}

// —— 整个分类（分类 tab 上右键）——
//
// 用户 2026-10-19 要求：「点了批量选择之后，一个动作把整个分类选上」（例如「欧美 88 部」）。
// 入口是**分类 tab 上的右键菜单**（不占工具栏位置），两项：全选本分类 / 取消本分类。
//
// ⚠️⚠️ **界面上根本不存在分类的完整成员列表** —— 墙是服务端分页的（200/页），
// 528 部的分类要滚三屏才全进内存。所以这里**不假装选上了什么**：
//
//   * `wholeCategory` 只是「这一档整个都要」这个意思表示，**没有**一堆选中的卡片；
//   * 提交时发的是 `category=<分类名>`，服务端拿**快照**（那份是全的）去收集目标；
//   * 这一档里**取消掉的那几部**记在 `picked` 里 —— 它这时是**排除集**，不是选择集。
//     服务端把两者求差（见 onlineScrapeJavWall 的说明）。这也是「取消几部」唯一
//     表达得出来的形态：台上算不出「剩下哪几部」，排除集却是精确的。
//
// 于是 `picked` 有**两种语义**，由「整分类态开着没有」决定（与服务端 `items`
// 随 `category` 一起翻转的那条规矩**同源**）：
//
//   * `wholeCategory === null` → 「点名的这几部」（老口径，跨 tab 累加）；
//   * 整分类态开着          → 「这一档里排除掉的这几部」。
//
// ⚠️ 整分类态**只属于当前那一档**（下面那条 `category` watcher 会把切走之后的状态清掉）。
// 刻意的：服务端一次只认一个 `category`，所以「整档欧美 + 另外点名几部有码」这种组合
// 本来就要分两次跑；与其让按钮上的数对不上实际要刮的东西，不如让它一次只表达一件事。
//
// ⚠️ 「全部」那颗按钮与 `""`（根目录）那一档**不给菜单**（见 onCatContextMenu）：前者是
// 「不过滤」，后者在前端与它撞成同一个值（都写 `category = ""`），点下去表达不出
// 「只刮根目录那一档」的意思 —— 与其给一个会刮错的入口，不如不给。

/** 整个要刮的分类名（`null` = 没走整分类）。 */
const wholeCategory = ref<string | null>(null);

/** 当前 tab 的整分类计数（取自 tab 上那个数，就是后端报的整分类部数）。 */
const activeCatCount = computed(
  () => categories.value.find((c) => c.name === category.value)?.count ?? 0,
);

/** 这一档整个都要（整分类态开着，且看的就是这一档）。 */
const wholeCategoryOn = computed(
  () => wholeCategory.value !== null && wholeCategory.value === category.value,
);

/**
 * 「全选本分类」当前该不该是勾上的：整分类态且一部都没排除。
 *
 * ⚠️ 只跟**这一档**有关，所以整分类态看别的 tab 时它是假的（那个 tab 还没整档选）。
 */
const wholeCategoryAllPicked = computed(() => wholeCategoryOn.value && picked.value.size === 0);

/**
 * 现在到底有多少部会被刮（按钮与文案上那个数）。
 *
 * ⚠️ 只影响**显示**：真正刮哪些由服务端按 `category` + 排除集算（见 onlineScrape）。
 * 整分类态下 `picked.size` 是**排除数**，所以是减法 —— 与提交上去的那两个维度同源。
 */
const pickedCountDisplay = computed(() => {
  if (!wholeCategoryOn.value) return picked.value.size;
  return Math.max(0, activeCatCount.value - picked.value.size);
});

/**
 * 「全选本分类」：置一个标记 + **清掉选择集**（那几部在整档里本来就会被刮到）。
 *
 * ⚠️ 不清的话它们会当场翻个意思：整分类态下 `picked` 是**排除集**，原来点名的那几部
 * 会变成「排除掉」—— 用户点一下「全选本分类」，那几部的勾反而没了。
 *
 * ⚠️ 清掉的是**整个**选择集，包括别的 tab 上点名的那些（它们在整分类态下发上去也只会
 * 被当成别的目录的排除项、即无效）。所以要**如实说一句**，别让用户的勾无声消失。
 */
function pickWholeCategory() {
  if (!batchMode.value || category.value === "") return;
  const dropped = picked.value.size;
  picked.value = new Map();
  wholeCategory.value = category.value;
  if (dropped > 0) {
    toast.info(`改为整个分类：已放弃之前点名的 ${dropped} 部`);
  }
}

/**
 * 「取消本分类」：取消「整档都要」这个意思表示，并**丢掉那份排除集**。
 *
 * ⚠️ 排除集**必须**一起清（它是这个模式的产物）：整分类态一撤，`picked` 立刻恢复成
 * 「点名要刮的」那一档 —— 留着排除集的话，刚被「整档里排除掉」的那几部会**反过来**
 * 变成「就是要刮这几部」。
 *
 * 撤完之后台上那一档一部都没选（按钮回到「在线刮削」的禁用态），与「我刚取消了整档」
 * 这件事一致；想只取消几部就别撤销整档，直接点那几张卡（它们在整档里是排除项）。
 */
function unpickWholeCategory() {
  wholeCategory.value = null;
  picked.value = new Map();
}

/**
 * 分类 tab 上的右键菜单。
 *
 * ⚠️ 批量选择没开时**根本不打开**（判据在 `onCatContextMenu` 里，那是唯一会把它
 * 置真的地方）：那个模式下点卡片是勾选，菜单里的两个动作才有意义。
 */
const catMenu = ref<{ open: boolean; x: number; y: number; name: string }>({
  open: false,
  x: 0,
  y: 0,
  name: "",
});

const catMenuItems = computed<DropdownMenuItem[]>(() => {
  const all = wholeCategoryAllPicked.value;
  return [
    { key: "pick-all", label: "全选本分类", disabled: all, type: "action" },
    // 没在整分类态时禁用（没有「整档都要」可取消）。
    { key: "unpick-all", label: "取消本分类", disabled: !wholeCategoryOn.value, type: "action" },
  ];
});

/**
 * 开右键菜单。
 *
 * ⚠️ 只有真的会弹时才 `preventDefault()`：批量选择没开（或点在「根目录」那一档）时
 * 把浏览器的原生右键菜单**留给用户**，而不是吞掉它什么都不出 —— 那看起来像卡了。
 *
 * ⚠️ 触摸屏上的「长按」也会走到这里（浏览器发的是 contextmenu）。
 */
function onCatContextMenu(name: string, ev: MouseEvent) {
  if (!batchMode.value || name === "") return;
  ev.preventDefault();
  catMenu.value = { open: true, x: ev.clientX, y: ev.clientY, name };
}

function closeCatMenu() {
  catMenu.value = { ...catMenu.value, open: false };
}

/**
 * 菜单选中：**先切到那一档再执行**，两个动作都以「这一档」为目标。
 *
 * ⚠️ 切 tab 会触发重列（`watch([source, category, sort])` → `resetAndLoad()`）。
 * ⚠️ 但**不能**被「切档就放弃整分类」那条 watcher 清掉 —— 它是靠「值等于新档」放行的
 * （见那条 watcher 的说明），所以这里的顺序必须是**先切 tab、再置整分类**。
 */
function onCatMenuSelect(key: string) {
  const name = catMenu.value.name;
  closeCatMenu();
  category.value = name;
  if (key === "pick-all") pickWholeCategory();
  else if (key === "unpick-all") unpickWholeCategory();
}

// —— 本页全选 ——
//
// 放在墙自己的工具条里（分类 tab 那一行右侧），因为它管的是「眼下这一页」——
// 跨分类累加要靠用户自己切 tab 再点一次，语义清楚。

const pageAllPicked = computed(
  () => items.value.length > 0 && items.value.every((it) => isPicked(it)),
);

/**
 * 整分类态下「本页全部被排除」了没。
 *
 * ⚠️ 整分类态里 `pageAllPicked` **恒为真**（整档的卡片都画成选中），那颗按钮若还照旧
 * 写「取消本页全选」、点了却什么都不做，看起来就是个坏按钮。所以那一档里它换一套
 * 语义：**排除本页 / 恢复本页**（同一份 `picked`，在这里是排除集）。
 */
const pageAllExcluded = computed(
  () => items.value.length > 0 && items.value.every((it) => picked.value.has(pickKey(it))),
);

function togglePageAll() {
  const next = new Map(picked.value);
  if (wholeCategoryOn.value) {
    // 整分类态：按钮管的是「这一页在排除集里要不要整页加/整页减」。
    if (pageAllExcluded.value) {
      for (const it of items.value) next.delete(pickKey(it));
    } else {
      for (const it of items.value) next.set(pickKey(it), { rel_dir: it.rel_dir, stem: it.stem });
    }
    picked.value = next;
    return;
  }
  if (pageAllPicked.value) {
    for (const it of items.value) next.delete(pickKey(it));
  } else {
    for (const it of items.value) next.set(pickKey(it), { rel_dir: it.rel_dir, stem: it.stem });
  }
  picked.value = next;
}

// —— 在线刮削（打上游补齐缺的元数据）——

/**
 * 是否正在在线刮削。
 *
 * 这个状态**只影响这一颗按钮**（禁用 + 文案变「刮削中…」）—— 用户明确要求
 * 「用户可以正常操作」：墙照常翻页、点卡片照常开详情，后端那条路也不占
 * 刮削的互斥锁（见 JavWallOnlineScrape 的说明）。
 */
const onlineScraping = ref(false);
let onlinePollTimer: number | undefined;

/**
 * 在线刮削：打上游把**缺的**元数据补齐，写回 json 与 nfo。
 *
 * 三种口径（用户拍板）：
 *   * **批量选择没开** → 整根 + 确认弹窗（老行为逐字不变）；
 *   * **开着** → 只刮那几部（勾选本身就是明确表达，不再弹确认）；
 *   * **整分类态**（分类 tab 上右键「全选本分类」）→ 刮**整个分类**：发 `category`，
 *     台上那几部勾这时是**排除集**（服务端求差，见上面那段说明）。
 *
 * ⚠️ 整分类那条路的部数**不由前端算**：台上只有当前那几页，`cat.count`（后端给的整分类
 * 计数）才是整档的数；收尾文案的分母也来自服务端（`category_matched`）。
 */
async function onlineScrape() {
  if (!props.source || onlineScraping.value) return;
  const batch = batchMode.value;
  // 整分类那条路**只对当前那一档**生效（见上面那段说明）。
  const cat = batch && wholeCategoryOn.value ? category.value : null;
  // ⚠️ 两套语义的取舍就在这里：`cat === null` 时 items 是「要刮的」，
  // 有 cat 时它是「要排除的」—— 与后端 items 随 category 翻面的那条规矩同源。
  const pickedItems = batch ? [...picked.value.values()] : [];
  // 「开着但一部没勾」时按钮是禁用的（整分类态下它显示的是「整档减掉几部」，
  // 那几部全被排除光时也确实没得刮）。这里再兜一道守卫。
  if (batch && pickedItems.length === 0 && cat === null) return;
  if (!batch) {
    try {
      await confirm({
        title: "在线刮削",
        message:
          "将按番号打上游（JAVDB），把本任务下**缺的**元数据补齐，并写入侧车 JSON 与 NFO。只补空：已有的值（包括你在编辑器里改过的）一律不动。数量多时请耐心等待 —— 期间墙可以正常使用。",
        icon: "info",
        confirmText: "开始刮削",
        danger: false,
      });
    } catch {
      return;
    }
  }
  // 先读一次**当前进度**，把「已经在跑」这种情况分开处理。
  //
  // 两个理由：
  //   1. **认领**：后端同一数据源不能并发（已在跑就直接返回当前进度）。上次跑完
  //      没来得及收尾、或者换了页面回来而服务端还在跑，这时点按钮该做的是
  //      「接上那条轮询」，不是再发一次注定被拒的起任务请求。
  //   2. **对表**：把这一次读到的「跑之前」的进度状态（含收没收到 result）交给
  //      轮询，让它知道手上这份是不是**上一轮**留下的残影（收尾状态没带 result
  //      时，下一轮的 `(0,0)` 才不会当下当成新任务的结论 —— 那是这个键第一次
  //      被点就弹「在线刮削失败」的来源）。
  let baseline: JavWallOnlineScrapeProgress | null = null;
  let alreadyRunning: JavWallOnlineScrapeProgress | null = null;
  try {
    const now = await fetchJavWallOnlineScrapeProgress(props.source);
    if (now.running) alreadyRunning = now;
    else baseline = now;
  } catch {
    // 进度读不到（网络抖一下）：当作没有基线，往下走。
  }
  if (alreadyRunning) {
    // 已经在跑：只接管轮询，不再弹一次确认、也不发那个会被拒的起任务请求。
    //
    // ⚠️ **绝不设 `pendingBatchItems`**：跑着的那条可能是**别人**（另一个页面）起的
    // 整根任务，不是这一轮的选择。设了的话，它跑完会把用户的选择集当成本次提交
    // 的结果消费掉 —— 选择没了，却一部都没刮。
    onlineScraping.value = true;
    startOnlinePoll(alreadyRunning);
    return;
  }
  onlineScraping.value = true;
  try {
    // **起任务**（接口立刻返回，活在后头跑）。结果从进度里读 —— 见 startOnlinePoll。
    // http.post 的第二参是 body、第三参才是 query（见 onlineScrapeJavWall）。
    //
    // ⚠️ 整分类态下 `pickedItems` 是**排除集**（上面那段说明）—— 服务端拿到 `category`
    // 之后就这么理解它。**一部都没排除时也要把这个空数组发上去**：那是「这一档全要」
    // 的合法表达（见 onlineScrapeJavWall 里那条 `items && category !== undefined`）。
    const res = await onlineScrapeJavWall(
      props.source,
      batch ? pickedItems : undefined,
      cat ?? undefined,
    );
    if (res?.not_started) {
      // 服务端那边已经有一条在跑（读进度与这次 POST 之间被人插了一脚）——
      // 这一轮的选择**根本没提交**，如实说清楚，并且**不认领**那条任务的收尾。
      toast.info("已有一个刮削任务在跑（可能是另一个页面起的），本次选择没有提交");
      onlineScraping.value = true;
      startOnlinePoll(undefined, baseline);
      return;
    }
    // 真的起了自己的任务：记下「这一轮提交的是什么」，收尾时据此清场。
    if (batch) {
      pendingBatchItems = pickedItems;
      // 按钮上那个数用**提交上去的意思**，不是台上的勾数：整分类态下它是整档的部数
      // （`pickedCountDisplay` 已经按那一档算好了）。
      submittedCount.value = pickedCountDisplay.value;
      // 立刻清掉选择集与选择态：选择属于「提交前」，
      // 而收尾那几条分支靠 `pendingBatchItems` 这份独立副本，不依赖这里的勾。
      batchMode.value = false;
      picked.value = new Map();
      // ⚠️ 整分类态**也要清**（与上面 `picked` 同一个道理，而且更隐蔽）：
      // 它不进 `pendingBatchItems`，留着的话收尾清场只把 `batchMode` 关掉、它原样还在 ——
      // 用户重新开批量选择时会**莫名其妙地整档都在要**（而台上一个勾都没有）。
      wholeCategory.value = null;
    }
    // 不在这里等：轮询那条路会在 running 变 false 时弹结果并收尾。
    startOnlinePoll(undefined, baseline);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "在线刮削失败"));
    onlineScraping.value = false;
    // 起任务失败：这一轮根本没提交，别让收尾把选择集当成本次的结果吃掉。
    clearPendingBatch();
    stopOnlinePoll();
  }
}

/**
 * 轮询进度：按钮上显示「刮削中… (n/m)」，跑完弹结果。
 *
 * # 为什么轮询而不是 await 那次请求
 *
 * 在线刮削要打上游一百多次、还要下上千张图，整个任务十几分钟。**同步等它**
 * 会被中间那层反代 502（群晖上实测跑到 20 部就断，把图片间隔调大只会更早）。
 * 所以后端改成后台任务，这里轮询到结束为止 —— 期间用户可以照常用墙。
 *
 * # 收尾判据就是 `running === false`，但要先排掉「上一轮的残影」
 *
 * 服务端把「跑完」写成**一次**带锁的赋值（Running=false + Message + Result 一起落），
 * 所以轮询读到的 `running=false` 就是结论。
 *
 * 唯一的例外是 `baseline` —— **起任务之前**读到的那份进度：那是上一轮跑完（或
 * 上一轮起任务失败）之后一直留在服务端的状态。起任务那个 POST 与第一次轮询
 * 是两条并发的请求，轮询完全可能先落地、读到这份残影：
 *   * 残影是**空状态**（从来没跑过）→ 旧代码会当成「没在跑也没结果」判失败；
 *   * 残影带着**上一轮的 result** → 不排掉的话会当场弹上一轮的战绩并收尾，
 *     而这一轮其实正在跑（按钮早早复位、后面再没人轮询）。
 * 两种都不是结论，所以：**读到与 baseline 一模一样的那一次，跳过，只跳一次。**
 *
 * 为什么「只跳一次」是安全的：POST 在返回之前就已经把服务端的进度换成新任务的
 * 「准备中…」了，所以跳过之后的下一次读数要么是新任务的进度（running=true），
 * 要么是它跑完的结论（真跑完了就正好该报）。
 *
 * ⚠️ 后端那个取进度的函数原来会把 `Running` **强行置回 true**，于是
 * `running === false` 永远不成立：任务早就跑完了，按钮却一直「刮削中…」，
 * 刷新页面才好（用户 2026-10-12 报的正是这个）。已修，见 JavOnlineScrapeProgressOf。
 */
function startOnlinePoll(
  initial?: JavWallOnlineScrapeProgress | null,
  baseline?: JavWallOnlineScrapeProgress | null,
) {
  stopOnlinePoll();
  // 接管一个**已经在跑**的任务时，先把当前进度摆上，按钮不会闪一下「在线刮削」。
  if (initial) onlineProgress.value = initial;
  // 只认一次残影（见上面）；对上一次之后就一律按真实读数判。
  let shadow: JavWallOnlineScrapeProgress | null = baseline ?? null;
  const tick = async () => {
    if (!props.source) return;
    try {
      const p = await fetchJavWallOnlineScrapeProgress(props.source);
      if (shadow && sameProgress(p, shadow)) {
        shadow = null;
        return;
      }
      shadow = null;
      onlineProgress.value = p.running ? p : null;
      if (p.running) return;
      // 跑完了（或失败）：弹结果并收尾。
      stopOnlinePoll();
      onlineScraping.value = false;
      // **先**把「这一轮是不是我提交的那批」取出来并原地清空 —— 三条收尾分支
      // （有 result / 只有 message / 两样都没有）都要消费它，谁先走都得清，
      // 否则下一次整根任务收尾时会拿着一份过期的标记去清用户新勾的选择。
      const submitted = clearPendingBatch();
      if (p.result) {
        const r = p.result;
        const parts = [`补到 ${r.scraped} 部`];
        if (r.images) parts.push(`补图 ${r.images} 张`);
        if (r.skipped) parts.push(`跳过 ${r.skipped} 部`);
        if (r.failed) parts.push(`失败 ${r.failed} 部`);
        // 「上游没有这一部」单独说（后端单独计数，见 JavWallOnlineScrapeResult.not_found）。
        // 不说的话，用户只能看到「跳过 43 部」—— 那读起来像漏刮，实际是那批片
        // JAVDB 与整条补缺链都不收录（换源或认命，改本地文件改不出来）。
        if (r.not_found) parts.push(`其中 ${r.not_found} 部上游根本没有`);
        // ⚠️ **报数的分母**：整分类那一轮后端会给 `category_matched`（整档一共几部）。
        // 不能拿 `r.total` —— 整分类态下它按老口径回显的是**排除集**的长度
        // （见 backend 那边的 `out.Total = len(items)`），于是「整档 88 部、排除了 3 部」
        // 会被说成「提交 3 部、其中 85 部已不在墙上」，等于把用户吓一跳。
        const denom = r.category_matched && r.category_matched > 0 ? r.category_matched : r.total;
        // 报名（0 部）那两档单独说，别报「补到 0 部」—— 那读起来像「一切正常」。
        // ⚠️ 判据从**服务端**来（见 JavWallOnlineScrapeResult.matched）：
        // 前端手上只有当前分类这一页，跨分类勾选时它自己数必然误判。
        if (r.matched === 0) {
          toast.error(
            denom > 0
              ? `在线刮削完成：提交的 ${denom} 部一部都没找到（可能已重命名或删除），没有补到任何东西`
              : "在线刮削结束：这个数据源目录里没有扫到任何番号影片",
          );
        } else {
          const missed = Math.max(0, denom - r.matched);
          if (missed > 0) parts.push(`${missed} 部已不在墙上（可能已重命名或删除）`);
          toast.success(`在线刮削完成：${parts.join("，")}`);
        }
      } else if (p.message) {
        // 没有 result 但有 message = 跑挂了（后端把错误写在 message 里）。
        toast.error(p.message);
      } else {
        // **两样都没有** = 服务端手上压根没有这一轮的记录（最可能是中途重启了，
        // 内存里那份进度跟着没了）。这时如实说一句，别默认成「失败」——
        // 这条路上正常跑完一定会带 result。
        toast.error("在线刮削已中断：服务端没有留下这次的记录（可能刚重启过）");
      }
      // 点名的这一轮跑完了：选择态与选择集一并归零（提交那一刻已经清过一次，
      // 这里是**兜底** —— 万一将来有别的路子在刮削期间把批量选择打开了，
      // 收尾时也得把它关掉，不能留一堆「看不见但仍被选中」的条目）。
      //
      // ⚠️ **放在 `await load()` 之前**：刷新会换掉墙上的 `rel_dir` / `stem`，
      // 之后再去清就留下一堆配不上任何卡片的幽灵条目。
      if (submitted) {
        batchMode.value = false;
        picked.value = new Map();
        // 整分类态与 `picked` 是一对（前者是意思表示、后者是排除集），一起清。
        wholeCategory.value = null;
      }
      await load();
    } catch {
      // 进度拿不到（网络抖一下）不该收尾：下一轮继续问。
    }
  };
  void tick();
  onlinePollTimer = window.setInterval(tick, 3000);
}

/** 两份进度是不是同一份（认「残影」用：它没有 result 可对，只能比这几个字段）。 */
function sameProgress(a: JavWallOnlineScrapeProgress, b: JavWallOnlineScrapeProgress): boolean {
  return (
    a.running === b.running && a.done === b.done && a.total === b.total && a.message === b.message
  );
}

function stopOnlinePoll() {
  if (onlinePollTimer !== undefined) window.clearInterval(onlinePollTimer);
  onlinePollTimer = undefined;
  onlineProgress.value = null;
}

const onlineProgress = ref<{ done: number; total: number } | null>(null);
/**
 * 「在线刮削」那颗按钮的文案。
 *
 * 批量选择开着 → 「刮削选中（N）」。**N 取 `pickedCountDisplay`**（不是 `picked.size`）：
 * 整分类态下台上那几部勾是**排除集**，真正要刮的是「整档减掉排队的那些」，
 * 直接报 `picked.size` 会说成「刮削选中（3）」而实际要跑 85 部。
 *
 * 整分类态还带上**档名**（`刮削选中（欧美 88）`）—— 那颗按钮长在页头，离墙上那句
 * 「整个分类欧美」有一段距离，光一个数字看不出这一轮刮的是哪一档。
 *
 * 刮削中那个数取**提交那一刻冻结**的值（`submittedCount`）：提交成功后选择集就清了，
 * 实时数会立刻掉成 0，而那几部正在后头刮着 —— 按钮上写着「刮削选中（0）」是自相矛盾的。
 */
const onlineScrapeLabel = computed(() => {
  if (!onlineScraping.value) {
    const n = pickedCountDisplay.value;
    if (n <= 0) return "在线刮削";
    if (wholeCategoryOn.value) return `刮削选中（${category.value} ${n}）`;
    return `刮削选中（${n}）`;
  }
  const p = onlineProgress.value;
  if (p && p.total > 0) return `刮削中… (${p.done}/${p.total})`;
  if (submittedCount.value > 0) return `刮削中… (0/${submittedCount.value})`;
  return "刮削中…";
});

/**
 * 「在线刮削」在**批量选择开着但一部都没得刮**时禁用。
 *
 * 三档（见 pickedCountDisplay）：
 *   * 点名态（`batch && picked.size === 0`）→ 禁用，用户拍板的口径；
 *   * 整分类态 → 按 `pickedCountDisplay` 判（整档 88 部时**不**禁用；把 88 部全排除光了才禁用）；
 *   * 没开批量选择 → 不禁用（整根那条路）。
 */
const onlineScrapeDisabled = computed(() => batchMode.value && pickedCountDisplay.value === 0);

/**
 * 页头「批量选择」那颗按钮的文案（开了显示已选部数）。
 *
 * ⚠️ 整分类态下如实说「整个分类」—— 报一个排除数（「已选 3 部」）会让用户点下去
 * 之前以为只刮 3 部。
 */
const batchModeLabel = computed(() => {
  if (!batchMode.value) return "批量选择";
  if (wholeCategoryOn.value) {
    const n = pickedCountDisplay.value;
    return `批量选择（整个分类 ${n} 部${picked.value.size > 0 ? `，已排除 ${picked.value.size}` : ""}）`;
  }
  return pickedCount.value > 0 ? `批量选择（已选 ${pickedCount.value} 部）` : "批量选择（点卡片勾选）";
});

/** 认领请求在飞时不重复发 —— onMounted 与「窗口重新可见」可能挨着触发。 */
let adoptInFlight = false;

/**
 * 挂载 / 换数据源时**认领**正在跑的那条任务。
 *
 * # 修的是什么（用户 2026-10-17 报的）
 *
 * 「刮削时把窗口关了，再打开按钮就显示『在线刮削』，不是『刮削中…(8/128)』」。
 *
 * 根因：`onlineScraping` 是**这个组件的本地 ref**，而在线刮削是**服务端的后台任务**
 * （见 StartJavWallOnlineScrape）。组件一被销毁，那个 ref 跟着没了 —— 任务还在跑，
 * 界面上却回到了初始态。而 `onlineScrape()` 只在**用户再点一次按钮**时才去读进度、
 * 才可能接管轮询（它开头那段 alreadyRunning 就是干这个的），所以中间这段时间按钮
 * 一直是「在线刮削」。
 *
 * 组件什么时候会被销毁：
 *   * 切到别的页面再切回来 —— `AuxToolsManagement` 被 `AdminView` 的 `<KeepAlive>`
 *     缓存，**不会重新挂载**（onMounted 不重跑），但会触发 `onActivated`；
 *   * 换数据源 —— `isJavTask` 翻面时这个组件被 v-if 摘掉再挂上；
 *   * keep-alive 缓存被淘汰（AdminView 那个 `:key` 变了）—— 这时才真的重挂。
 *
 * 所以这一手挂在**每一个「组件刚活过来」的时刻**：onMounted / onActivated /
 * 数据源变化 / 窗口重新可见。
 *
 * # 为什么用「接管轮询」而不是只读一次进度
 *
 * 只读一次的话拿到的是**那一刻**的 n/m，之后不再更新 —— 而这里恰恰是用户盯着看
 * 的那个数字（他要的是「只要刮削没完成就一直显示刮削中」）。接管轮询（`startOnlinePoll`
 * 那条路）会一路跟到结束、弹结果、并 `await load()` 把这一轮刮出来的东西刷上墙。
 *
 * ⚠️ **只在真的在跑时接管**（`p.running`）。没在跑时若也调 `startOnlinePoll`，
 * 它读到 `running=false` 就会把上一轮留下的 `result` 弹成一次 toast —— 而用户只是
 * 新开了一次页面，没做任何事。
 */
async function adoptRunningScrape() {
  if (!props.source || onlineScraping.value || adoptInFlight) return;
  adoptInFlight = true;
  try {
    const p = await fetchJavWallOnlineScrapeProgress(props.source);
    // 读的这几百毫秒里用户可能已经点了按钮（那条路自己会接），别抢。
    if (!p.running || onlineScraping.value) return;
    onlineScraping.value = true;
    startOnlinePoll(p);
  } catch {
    // 读不到（网络抖一下 / 后端刚好在重启）：当作没在跑。按钮点下去时还会再读一次。
  } finally {
    adoptInFlight = false;
  }
}

/**
 * 窗口重新可见时补一次检查。
 *
 * 手机上切到别的 app 再回来、电脑上切走标签页再切回来，Vue 不会给任何钩子 ——
 * 而这段时间里服务端那条任务可能已经跑完（或者反过来，是在**别处**点起来的）。
 * 一次轻量请求就能把按钮对回真实状态。
 */
function onOnlineScrapeVisibility() {
  if (document.visibilityState === "visible") void adoptRunningScrape();
}

onMounted(() => {
  void adoptRunningScrape();
  document.addEventListener("visibilitychange", onOnlineScrapeVisibility);
});
// keep-alive 的页面切回来走这一条（那时 onMounted 不会重跑，但任务可能已经跑完或
// 是在这一段里被点起来的）。`onActivated` 在**没有 keep-alive** 时不会触发，
// 所以它与 onMounted 不是重复调用 —— 两个都留着。
onActivated(() => {
  void adoptRunningScrape();
});
onUnmounted(() => {
  stopOnlinePoll();
  document.removeEventListener("visibilitychange", onOnlineScrapeVisibility);
});

/** 「刷新元数据」= 重读本地磁盘并重列（不是重新生成；重新生成是卡片上的「重刮」）。 */
async function refreshMeta() {
  if (!props.source) return;
  refreshing.value = true;
  try {
    result.value = await refreshJavWall(props.source, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "added_desc",
      limit: PAGE_LIMIT,
    });
    hasMore.value = Boolean(result.value?.has_more);
    total.value = result.value?.total ?? 0;
    publishHiddenDirs();
    // 与 load() 同一条路：整份换掉了，取图窗口必须自己重算一次
    // （别指望 length watcher —— 长度可能一模一样，见 afterListReplaced 的说明）。
    void nextTick(afterListReplaced);
    toast.success("已重新读取本地文件");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "刷新失败"));
  } finally {
    refreshing.value = false;
  }
}

function openEditor(item: JavWallItem) {
  editing.value = item;
  drawerOpen.value = true;
}

// —— 删除 ——
//
// 点垃圾桶**不直接删**，先开确认弹窗（`StrmJavWallDeleteModal`）——那一份会去
// 服务端取「会删掉什么」的清单摆给用户看，再让他点第二次。这是全项目唯一会
// 删用户东西的动作，多这一道是刻意的。
//
// 删完不整表重列（快照可能还没过期），**只把这张卡从当前列表里摘掉**：
// 用户马上看到它消失，滚动位置也不跳。后端那边已经作废了快照，下一次真正
// 刷新时列表会与磁盘一致。
const deleting = ref<JavWallItem | null>(null);

function askDelete(item: JavWallItem) {
  deleting.value = item;
}

function onDeleted() {
  const removed = deleting.value;
  deleting.value = null;
  const id = removed?.id;
  // 删掉的那一部不该留在选择集里 —— 留着的话「刮削选中（N）」会比墙上能勾到的多一部，
  // 提交后服务端报「N 部里有一部没找到」，看着像刮削出了问题，其实只是这一部没了。
  if (removed && picked.value.size) {
    const key = pickKey(removed);
    if (picked.value.has(key)) {
      const next = new Map(picked.value);
      next.delete(key);
      picked.value = next;
    }
  }
  if (!id || !result.value) return;
  const idx = result.value.items.findIndex((it) => it.id === id);
  if (idx < 0) return;
  result.value.items.splice(idx, 1);
  result.value.total = Math.max(0, result.value.total - 1);
  total.value = result.value.total;
}

/** 保存之后只换这一张卡（不整表重列，免得滚动位置乱跳）。 */
function replaceItem(updated: JavWallItem) {
  if (!result.value) return;
  const idx = result.value.items.findIndex((it) => it.id === updated.id);
  if (idx >= 0) result.value.items[idx] = updated;
}

async function rebuild(item: JavWallItem) {
  if (!props.source) return;
  busyStem.value = item.stem;
  try {
    const detail = await rebuildJavWallItem(props.source, item.rel_dir, item.stem);
    replaceItem(detail.item);
    toast.success("已重刮：" + item.number);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "重刮失败"));
  } finally {
    busyStem.value = "";
  }
}

function subtitle(item: JavWallItem): string {
  const bits: string[] = [item.category || "根目录"];
  if (!item.has_nfo) bits.push("缺 nfo");
  if (!item.has_sidecar) bits.push("无侧车");
  return bits.join(" · ");
}

// ————————————————————— 取图窗口 —————————————————————
//
// 一页最多 200 张，一屏只看得见 30~40 张。**只给看得见的那些发图片请求**：
// `MediaImage` 的原生 `loading="lazy"` 在这里不管用 —— 它的触发距离按视口高度的
// 倍数算，长列表里首屏渲染就会把下面好几屏的图一起排队（实测 50 张全发、2 MB 多、
// 最后一张要几秒）。所以关掉原生懒加载，改由这里按滚动位置发牌。
//
// # 是**滑动窗口**，不是「前 N 张」（2026-10-04 修）
//
// 原来这里算的是一个「一屏 × 3」的**数字**，`imageSrc` 写成 `index >= budget` 就返回空
// —— 那只能表达「前 N 张」，表达不了「当前这一段」。于是滚动时算出来的永远是同一个
// 数，**第 N+1 张往后永远没有图**：群晖上一页 94 张、N 算出来 72，滚到底最后 22 张
// 清一色是占位骨架。用户看到的就是「有些显示、有些不显示」，切 tab / 切视图时数字
// 一变又会好一阵 —— 像极了缓存问题。
//
// 现在按**滚动位置**算出一段区间 `[start, end)`：当前视口那一屏，前后各多给一屏。
// 往前留一屏是为了**往回滚**时图已经在手里。
//
// # 已经发过图的下标**不再撤回**
//
// 窗口滑动时区间会往前移，真按它去撤图，往回滚就会看到图闪一下重新加载。
// 所以另记一个「已经发过图的下标区间」`[loadedMin, loadedMax)`：窗口的并集。
// `imageSrc` 只对**从没进过窗口**的下标返回空串。
//
// ⚠️ 用**两个 ref** 而不是一个 `Set`：Set 的增删不会触发 Vue 重渲染，
// 第一版写成 Set 时整面墙一张图都不出（`imageSrc` 加了新下标但组件没重渲染）。
/** 已经进过窗口的下标区间（并集）。空集用 min=+∞ / max=0 表示。 */
const loadedMin = ref(Number.POSITIVE_INFINITY);
const loadedMax = ref(0);

/** 视口高度：优先用滚动容器自己的（它才是真正被裁切的那个盒子）。 */
function viewportHeight(): number {
  const box = scrollTarget.value;
  return box instanceof HTMLElement ? box.clientHeight : window.innerHeight;
}

/** 量出网格的列数与行高（含间距）。量不到时回落估算（宁可多给，别少给）。 */
function measureGrid(): { cols: number; rowH: number } {
  const cards = gridEl.value?.querySelectorAll<HTMLElement>(".jav-card");
  if (cards && cards.length > 0) {
    const firstTop = cards[0].offsetTop;
    let cols = 0;
    for (const card of cards) {
      if (card.offsetTop !== firstTop) break;
      cols++;
    }
    if (cols > 0) {
      const cardH = cards[0].offsetHeight;
      const secondRow = cards[cols];
      const gap = secondRow ? secondRow.offsetTop - firstTop - cardH : (view.value === "thumb" ? 16 : 14);
      return { cols, rowH: Math.max(1, cardH + Math.max(0, gap)) };
    }
  }
  // 回落：列数按海报视图的 minmax(140px)+gap14 反推，行高按 300 估。
  const cols = Math.max(1, Math.floor((window.innerWidth - 48 + 14) / (140 + 14)));
  return { cols, rowH: 300 };
}

/**
 * 按当前滚动位置重算窗口，并把新区间并入 `loaded`。
 *
 * 用 `getBoundingClientRect` 而不是 `offsetTop`：后者相对 offsetParent，而滚动量在
 * 滚动盒子上，两个坐标系拼起来容易差一截（差一屏就是「滚到底还有一排没图」）。
 */
function refreshImageBudget() {
  const total = items.value.length;
  if (total === 0) {
    return;
  }
  const grid = gridEl.value;
  if (!grid) return;

  const { cols, rowH } = measureGrid();
  const box = scrollTarget.value;
  const viewTop = box instanceof HTMLElement ? box.getBoundingClientRect().top : 0;
  const gridTop = grid.getBoundingClientRect().top;

  // 视口顶落在网格里的第几行（往上滚过头时为负 → 夹到 0）
  const firstRow = Math.max(0, Math.floor((viewTop - gridTop) / rowH));
  const rowsOnScreen = Math.max(1, Math.ceil(viewportHeight() / rowH));
  // 前后各留一屏
  const startRow = Math.max(0, firstRow - rowsOnScreen);
  const endRow = firstRow + rowsOnScreen * 2;

  const start = Math.max(0, Math.min(total, startRow * cols));
  const end = Math.max(start, Math.min(total, endRow * cols));
  if (start < loadedMin.value) loadedMin.value = start;
  if (end > loadedMax.value) loadedMax.value = end;
}

let onScrollTimer: number | undefined;
function scheduleBudgetRefresh() {
  window.clearTimeout(onScrollTimer);
  onScrollTimer = window.setTimeout(refreshImageBudget, 120);
}

/**
 * 这个位置该显示哪张图 —— 轮不到就返回空串（组件会渲染占位/骨架）。
 *
 * 判据是**列表下标**而不是元素的真实可视状态：网格是等高的，下标与屏幕位置
 * 一一对应，算一次比给每张卡挂 IntersectionObserver 便宜得多，也不会因为
 * 卡片进出视口而反复卸载/重载图片（那种抖动比多下几张图更难受）。
 */
function imageSrc(item: JavWallItem, index: number): string {
  if (index < loadedMin.value || index >= loadedMax.value) return "";
  return view.value === "poster" ? (item.poster_url ?? "") : (item.thumb_url ?? "");
}

/**
 * 这张卡是**还没轮到取图**，还是**根本没有图**。
 *
 * 模板里「骨架」只该占前一种：后一种（磁盘上没侧车图 —— 比如直接从网盘拷进来、
 * 还没刮削过的作品）用骨架盖住占位图，看上去就是「一直加载不出来」。用户
 * 2026-10-17 报的「切到不满一屏的分类还是显示不了海报墙」，一半就是它：
 * 那 6 部 `<番号>.strm` 的目录里一份图都没有，`poster_url` 是空串，
 * 而 `v-if="!imageSrc(...)"` 对此同样成立，于是骨架永久盖在「暂无封面」上面。
 *
 * 判据必须看**这一部有没有 URL**，不能只看窗口；否则两种状态永远分不开
 * （属于「静默变空」那一族：看起来只是没加载出来）。
 */
function imagePending(item: JavWallItem, index: number): boolean {
  const url = view.value === "poster" ? (item.poster_url ?? "") : (item.thumb_url ?? "");
  if (!url) return false;
  return index < loadedMin.value || index >= loadedMax.value;
}

// ————————————————————— 滚动容器 —————————————————————
//
// 图片窗口要跟着**真正在滚的那个盒子**走。这个组件在后台里是挂在 `.admin__body`
// 下面的（`overflow-y: auto`），而它自己不知道这件事 —— 所以往上找第一个
// 「overflow-y 是 auto/scroll 且真的能滚」的祖先，找不到才回落 `window`。
//
// ⚠️ 别改回 `window.addEventListener("scroll", …)`：那样一次都不会触发，
// 而失败是**静默**的（图少了一半，没有任何报错）。
//
// 挂在 `document` 上用**捕获**阶段监听：scroll 事件不冒泡，但**捕获阶段能收到
// 所有后代的滚动**，所以不用去猜是哪个祖先在滚，也不会漏掉布局变化后换容器的情形。
const gridEl = ref<HTMLElement | null>(null);
/** 当前认定的滚动盒子（只用于读视口高度；找不到时是 null = 用 window）。 */
const scrollTarget = ref<HTMLElement | null>(null);

/** 重新认定滚动盒子（布局或内容变了之后调）。 */
function bindScrollTarget() {
  scrollTarget.value = findScrollBox(gridEl.value);
}

/** 捕获阶段的滚动处理：任何后代的滚动都会走到这里。 */
function onAnyScroll() {
  if (!scrollTarget.value) bindScrollTarget();
  scheduleBudgetRefresh();
}

// ————————————————————— 拉到底续加载 —————————————————————
//
// 两条路一起用：
//   1. `IntersectionObserver` 盯底部哨兵 —— 「滚到底」那条路（哨兵常驻 DOM，见模板）；
//   2. `useFillViewport` —— 「内容不足一屏、根本没法滚」那条路。光有 1 解决不了它：
//      没有滚动空间时观察器只在挂载那一刻回调过一次，用户怎么拖都不动。
const sentinel = ref<HTMLElement | null>(null);
let loadMoreObserver: IntersectionObserver | null = null;

function disconnectLoadMoreObserver() {
  loadMoreObserver?.disconnect();
  loadMoreObserver = null;
}

function setupLoadMoreObserver() {
  disconnectLoadMoreObserver();
  if (typeof IntersectionObserver === "undefined") return;
  if (!hasMore.value || !sentinel.value) return;
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      void load({ append: true });
    },
    // 提前 320px 开始拉，滚到底时下一页通常已经在了 —— 与榜单 / 影库同一档间距。
    { rootMargin: "320px 0px" },
  );
  loadMoreObserver.observe(sentinel.value);
}

const { fill: fillViewportIfShort, reset: resetAutoFill } = useFillViewport({
  sentinel,
  hasMore,
  busy: computed(() => loading.value || loadingMore.value),
  loadMore: () => void load({ append: true }),
});

// ————————————————————— 列表变了 —————————————————————
//
// ⚠️ 「重置取图窗口」的判据是**查询条件变了**，不是**数组变了**。
//
// 加续加载之前，`items` 变了就等于「换了一批」（首屏、翻档、搜索），无条件重置没问题。
// 加了追加之后这个假设不成立：**追加一页也会让 items 变**，无条件重置会把已经显示出来的
// 图撤回去，往回滚就闪 —— 那正是用户说的「有些海报显示不出来」。
//
// 所以拆成两类 watch：
//   * 条件变了 → 重置窗口 + 重列（换批才重置）
//   * 只是变长了 → 不重置，只重新算一次窗口（新下标会被 refreshImageBudget 补进来）

/** 换了一批（首屏 / 换档 / 搜索 / 换排序）：窗口归零，然后重列。 */
function resetAndLoad() {
  loadedMin.value = Number.POSITIVE_INFINITY;
  loadedMax.value = 0;
  resetAutoFill();
  void load();
}

// 数据源变了只重置分类（换源总是从「全部」看起）。
watch(() => props.source, () => {
  category.value = "";
  // 换源 = 换了一条进度（服务端按数据源键各记一份）。原来那个源的轮询必须停掉，
  // 否则它会在新源上继续问旧源的结果；停掉之后立刻认领**新源**在跑的那条。
  //
  // ⚠️ 必须先 `onlineScraping = false`：`adoptRunningScrape` 见到 true 就直接返回
  // （那条守卫是给「用户已经点了按钮」用的），不清掉它，换源之后按钮会一直停在
  // 上一个源的「刮削中…」。
  stopOnlinePoll();
  onlineScraping.value = false;
  // 选择集是**按源算的路径**（rel_dir + stem），换源之后一个都对不上 —— 全清。
  // `pendingBatchItems` 也一起清：它装的是上一个源的路径，换源后那条任务若跑完，
  // 拿它去清场是清了个寂寞（而且会让用户看着「选择态莫名退出」）。
  batchMode.value = false;
  picked.value = new Map();
  pendingBatchItems = null;
  submittedCount.value = 0;
  // 分类名是**按这个源盘上的目录**算出来的，换源之后同样作废。
  wholeCategory.value = null;
  catMenu.value = { ...catMenu.value, open: false };
  void adoptRunningScrape();
});
// 重列：换任务 / 换分类 / 换排序都走这一条。
//
// ⚠️ **`props.source` 必须留在源里，不能只靠「category 会被重置」那一跳。**
// 原来这里只有 `[category, sort]`，换任务时靠上面那条把 category 置空来顺带触发 ——
// 可**上一个任务停在「全部」时 category 本来就是空串**，赋值等于没变、watcher 不触发，
// 于是墙上留着上一个任务的卡片，而 `props.source` 已经换成了新任务：
//
//   - 从前只是「看着不对」（点开详情报「这一部不在墙上」）；
//   - 删除做出来之后它会**删错东西** —— 点新任务页面上的旧卡片，请求带的是
//     新任务 id + 旧任务的 rel_dir/stem，删掉的是新任务里**同名**的那个目录。
//     2026-10-14 实测踩到：任务 112 的 SZL-034 卡片打到任务 116 上（好在那边没有
//     同名目录，报「目录不存在」）。
//
// 重列期间 `loading` 为真、模板会把整片网格换成「加载中…」，所以旧卡片不会
// 停在屏幕上被点到。
watch([() => props.source, category, () => props.sort], resetAndLoad);

/**
 * 切 tab：只有「切到**别的**档」才放弃整分类态。
 *
 * ⚠️ 判据必须是 `wholeCategory !== next`，不能是「`wholeCategory` 非空就清」——
 * 菜单那条路是**先切 tab 再置整分类**（见 onCatMenuSelect），赋值本身会触发这个
 * watcher（watcher 在 flush 时看到的是已经置好的新值）。写成「非空就清」的话，
 * 右键「全选本分类」会当场把自己清掉，用户看到的就是「点了没反应」。
 *
 * ⚠️ 换档要连**排除集**一起丢：`picked` 在整分类态下是**排除集**，只对原来那一档
 * 有意义。留着它跨档的话，切换到新档时那些勾会当场翻面成「点名要刮的这几部」。
 */
watch(category, (next) => {
  if (wholeCategory.value !== null && wholeCategory.value !== next) {
    wholeCategory.value = null;
    picked.value = new Map();
  }
  // 切档时把右键菜单收掉（它浮在旧位置，动作里的「本分类」也已经是旧档）。
  catMenu.value = { ...catMenu.value, open: false };
});
// 关键词来自页头那个输入框：那边是 v-model 直连，这里跟着 prop 变就重列（防抖）。
let keywordTimer: number | undefined;
watch(() => props.keyword, () => {
  window.clearTimeout(keywordTimer);
  keywordTimer = window.setTimeout(resetAndLoad, 300);
});

watch(() => items.value.length, () => {
  void nextTick(afterListReplaced);
});

/**
 * 列表换完之后的那一拍：重认滚动盒子、重算取图窗口、判一次「够不够一屏」。
 *
 * # 为什么要有这么一个显式的函数（2026-10-17 修的）
 *
 * 这三件事原来只挂在 `watch(() => items.value.length)` 上。watch 的语义是
 * **值变了才回调** —— 而「换了一批」未必改长度：本地目录那种大库，每个一级目录
 * 都是一整页（200 条），切分类时长度 `200 → 200` **一模一样**。于是
 * `resetAndLoad()` 把取图窗口清成空集之后，没有任何人再把它填回去：
 *
 *   * `imageSrc()` 对每个下标都返回空串 → 整面墙清一色骨架；
 *   * 而 `onAnyScroll`（滚动时才会跑）里有 `refreshImageBudget()` ——
 *     所以「随便滚一下就全出来了」，正是用户报的现象。
 *
 * 现在这条路的两个入口（`load()` 与 `refreshMeta()`）在**赋值之后**直接调它，
 * 不等 watch。长度 watcher 留着兜底（别处改 items 的情形）。
 */
function afterListReplaced() {
  bindScrollTarget();
  refreshImageBudget();
  // 网格渲染完再判一次「够不够一屏」—— 这时量到的哨兵位置才是最终的。
  fillViewportIfShort();
}

// 列表换完（首屏 / 追加）也要重挂观察器：hasMore 可能从 false 翻成 true。
watch([sentinel, hasMore], () => {
  void nextTick(setupLoadMoreObserver);
});

// 视图切换（海报 ↔ 缩略图）会换掉卡片高度与列数，窗口要跟着重量一次。
watch(view, () => {
  void nextTick(() => {
    refreshImageBudget();
    fillViewportIfShort();
  });
});

onMounted(() => {
  void nextTick(() => {
    bindScrollTarget();
    refreshImageBudget();
  });
  // 捕获阶段：scroll 不冒泡，但 document 的捕获阶段能收到**所有后代**的滚动。
  document.addEventListener("scroll", onAnyScroll, { passive: true, capture: true });
  window.addEventListener("resize", scheduleBudgetRefresh);
  void load();
});
onUnmounted(() => {
  window.clearTimeout(keywordTimer);
  window.clearTimeout(onScrollTimer);
  disconnectLoadMoreObserver();
  document.removeEventListener("scroll", onAnyScroll, { capture: true } as EventListenerOptions);
  window.removeEventListener("resize", scheduleBudgetRefresh);
  window.removeEventListener("keydown", onActorLayerKey);
});

// 卡片点击 → 详情抽屉 → 从抽屉里播放。**卡片上不再有播放键**（用户要求）。
const wall = useWallDetail();

/**
 * 影片详情里点了某位演员 → 打开演员详情（用户 2026-10-21 的要求）。
 *
 * # 为什么这里要说一句「这一面墙没有演员页」
 *
 * 从**演员墙**（`JavActorWall`）点进来的演员详情是叠在那一页上的，关掉就回到演员
 * 列表；而这里是从海报墙的影片详情点进来的，底下没有演员墙 —— 关掉就是回海报墙。
 * 所以在这一面传 `:searchable="false"`（别名不可点去搜），`:raised="true"`
 * （压过影片详情，两个都是 elevated 同档）。
 *
 * `JavWallActorCard` 只填 key / name / gender / avatar：详情抽屉拿到 key 就自己
 * 去后端取完整的那份（片单 + 资料档案），这里给的那几项只是**首屏占位**，
 * 免得抽屉打开的头几百毫秒是一条「加载中…」。
 */
const actorDetailOpen = ref(false);
const actorDetailCard = ref<JavWallActorCard | null>(null);

function openActorFromMovie(actor: WallActor) {
  if (!actor.key) return;
  actorDetailCard.value = {
    key: actor.key,
    id: actor.id,
    name: actor.name,
    gender: actor.gender ?? 0,
    // 影片详情里那张已经是索引表拼过的地址（侧车没头像时），直接沿用 ——
    // 详情抽屉的探针会再换一张更可靠的，换不到就显示这张。
    avatar_url: actor.avatar_url,
    movies_count: 0,
  };
  actorDetailOpen.value = true;
  // 是**从影片详情点进来**的：影片详情留在底下，演员详情按 `raised` 压在上面
  // （这是这一面的原始版式）。上一次「被影片详情收起」的标记必须清掉，
  // 否则重开一个已取过数据的演员详情时会是一片空白 —— 看起来像点了没反应。
  actorSuspendedByMovie.value = false;
}

/**
 * 播放期间演员详情是不是被我们收起来了（关掉播放窗要放回来）。
 *
 * 同一条路在**这一面**也走得通：影片详情 → 点演员 → 演员详情 → 点其中的一部片
 * → 影片详情 → 播放。播放时 `useWallDetail.play()` 只收起**影片详情**自己那层
 * （见那边的 suspendedByPlayer），演员详情仍是 elevated（10040），照样压在全屏
 * 播放窗（10000）上面 —— 看起来就是「播放页被详情页挡住了」。
 *
 * ⚠️ 别去抬播放窗的 z-index：`.file-preview` 的 10000 是全局外壳刻意的档位
 * （低于 --z-dropdown，好让下拉菜单浮在预览之上），抬它会波及所有预览场景。
 *
 * 只切「收不收起」、**不动 `actorDetailOpen`**：关掉 `open` 会把已取的片单与资料
 * 整份清掉（见抽屉里 `suspended` 那条），关掉播放窗后用户会看到一整页重新加载。
 */
const actorSuspendedByPlayer = ref(false);

/**
 * 演员详情被**从它里面点开的那部影片详情**收起（关掉影片详情再放回来）。
 *
 * 和上面那个一个道理，只是压住它的是**影片详情**而不是播放窗：本组件里影片详情的
 * 抽屉写在演员详情**之前**、档位也低一档（elevated 10040 < raised 10041），
 * 所以从演员详情点一部参演影片时，影片详情会被整个盖住 —— 用户看到的就是
 * 「点了参演影片，什么都不发生」（用户 2026-10-24 报的这个）。
 *
 * 做法仍是「收起被压住的那一层」，而不是去抬影片详情的档位：影片详情在
 * `JavActorWall` 里还要反过来压住演员详情，两边都抬就变成谁后写谁赢。
 */
const actorSuspendedByMovie = ref(false);

/** 从演员详情点一部参演影片：收起演员详情（保留已取的数据），开影片详情。 */
function openMovieFromActor(source: WallSourceRef, movie: JavWallActorMovie) {
  if (actorDetailOpen.value) actorSuspendedByMovie.value = true;
  wall.openJav(source, movie);
}

/**
 * 关掉影片详情：顺手把「被它收起的演员详情」放回来。
 *
 * ⚠️ 不能写成 `watch(() => wall.open)`：播放时 `useWallDetail.play()` 也会把
 * `open` 置 false（那是它收起自己那层的机制），那一刻若把标记清了，演员详情会
 * 弹到**播放窗上面** —— 又变回用户报的那个「播放页被详情页挡住」。所以只在
 * **用户真的关掉影片详情**这条路上清，也就是这里（返回键 / 点遮罩）和 Esc。
 */
function closeMovie() {
  wall.close();
  actorSuspendedByMovie.value = false;
}

/**
 * 演员详情这一刻该不该收起来。
 *
 * 两种收起的原因各自独立：播放窗开着（`actorSuspendedByPlayer`），或者从它里面
 * 点开的那部影片详情正开着（`actorSuspendedByMovie`）。任一成立就不显示 ——
 * 否则播放时「影片详情被收起」这一拍会顺手把演员详情弹到播放窗上面。
 */
const actorSuspended = computed(
  () => actorSuspendedByPlayer.value || actorSuspendedByMovie.value,
);

watch(
  () => wall.playerOpen.value,
  (playing) => {
    if (playing) {
      if (!actorDetailOpen.value) return;
      actorSuspendedByPlayer.value = true;
      return;
    }
    actorSuspendedByPlayer.value = false;
  },
);

// Esc：影片详情抽屉自己不挂键盘处理（`useWallDetail` 也不挂），而演员详情是
// 从它里面开出来的一层。这一面没有别的浮层栈可挂，所以在这里收口：
// 播放窗 → 影片详情 → 演员详情 → 海报墙，一层一层关。
//
// 判据用**收起的标记**而不是「演员详情开没开」：从演员详情点一部参演影片之后，
// 演员详情只是被收起来（`actorDetailOpen` 仍是 true），它若排在影片详情前面判，
// 第一下 Esc 会关掉一个看不见的东西，用户以为按键没反应。
function onActorLayerKey(e: KeyboardEvent) {
  if (e.key !== "Escape") return;
  if (wall.playerOpen.value) {
    wall.closePlayer();
    return;
  }
  if (wall.open.value && actorSuspendedByMovie.value) {
    closeMovie();
    return;
  }
  if (actorDetailOpen.value) {
    actorDetailOpen.value = false;
    return;
  }
  if (wall.open.value) {
    wall.close();
  }
}
onMounted(() => window.addEventListener("keydown", onActorLayerKey));

defineExpose({
  refreshMeta,
  load,
  onlineScrape,
  onlineScraping,
  onlineScrapeLabel,
  // 批量选择：页头那颗按钮要按 `isJavSource` 长在番号墙这一侧
  // （见 AuxToolsManagement 里那个 v-else-if 模板）。
  //
  // ⚠️ 包 `computed(() => ...)` 而不是把 ref 直接抛出去：模板里那颗按钮是
  // `:disabled="pickedCount === 0"`，Vue 对**模板里用到的** ref 会自动解包，
  // 但经 `defineExpose` 传给父组件之后**不会** —— 抛裸 ref 的话父组件拿到的是
  // 一个对象，判据恒为真（按钮永远禁用）。`onlineScrapeLabel` 那种 computed
  // 本来就是值，所以上面那几个不用包。
  batchMode: computed(() => batchMode.value),
  batchModeLabel,
  pickedCount: computed(() => picked.value.size),
  toggleBatchMode,
  onlineScrapeDisabled,
  // 整分类那档（2026-10-19）：按钮上的部数现在要按「整档 / 整档减几部」算，
  // 页头那颗「批量选择（已选 N 部）」若还读 `pickedCount`（排除集长度）会说反。
  // `pickedCountDisplay` 是 computed，本来就是值；`wholeCategory` 是 ref，要包一层。
  pickedCountDisplay,
  wholeCategory: computed(() => wholeCategory.value),
  // 菜单的两个动作也抛出去：将来别的入口（工具栏 / 快捷键）想复用，不必再进来找。
  pickWholeCategory,
  unpickWholeCategory,
});
</script>

<template>
  <div class="jav-wall">
    <!-- 全量扫描的常驻警告：那种模式每轮都会用侧车重建 nfo/海报、并重下封面，手改的会被覆盖 -->
    <p v-if="result?.full_sync_wipe" class="jav-wall__banner">
      ⚠ 本任务是「全量」扫描模式：每次扫描都会用侧车 JSON 重建 nfo 与海报，并重新下载封面（thumb / fanart），
      手工编辑会被覆盖。想保留编辑请把扫描方式改成「更新」或「补缺」；想还原自动生成的版本，就跑一次全量扫描。
    </p>

    <div class="jav-wall__bar">
      <div class="jav-wall__tabs">
        <button
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === '' }"
          @click="category = ''"
        >
          全部
        </button>
        <!-- 分类 tab。批量选择开着时**右键 / 长按**出「全选本分类 / 取消本分类」——
             入口放在 tab 上而不是工具栏里，是因为它管的就是「这一档」，语义贴着走。 -->
        <button
          v-for="cat in categories"
          :key="cat.name"
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === cat.name }"
          :title="batchMode && cat.name ? '右键：全选 / 取消整个分类' : undefined"
          @click="category = cat.name"
          @contextmenu="onCatContextMenu(cat.name, $event)"
        >
          {{ cat.name || "根目录" }}<span class="jav-tab__count">{{ cat.count }}</span>
        </button>
      </div>

      <!-- 搜索与排序都挪到页头那两个按钮上了（与 tmdb 那套外观一致、功能各是各的），
           这里只留视图切换。批量选择开着时补一颗「全选本页」——
           一排一排地点太慢（一页 200 张），而它管的就是「眼下这一页」：
           跨分类累加要靠用户自己切 tab 再点一次，语义清楚。 -->
      <div class="jav-wall__tools">
        <!-- 整分类态一句话说清「在刮什么」：这个模式下**台上没有满屏的勾**
             （分类的全部成员前台根本拿不到），不给句话用户会以为没选上。 -->
        <span v-if="batchMode && wholeCategoryOn" class="jav-wall__whole-hint">
          整个分类{{ category }}{{ picked.size > 0 ? `（已排除 ${picked.size} 部）` : "" }}
        </span>
        <button
          v-if="batchMode"
          type="button"
          class="jav-wall__pickall"
          :disabled="!items.length"
          @click="togglePageAll"
        >
          <template v-if="wholeCategoryOn">
            {{ pageAllExcluded ? `恢复本页（${items.length}）` : `排除本页（${items.length}）` }}
          </template>
          <template v-else>
            {{ pageAllPicked ? "取消本页全选" : `全选本页（${items.length}）` }}
          </template>
        </button>
        <div class="jav-wall__view" role="group" aria-label="视图切换">
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'thumb' }"
            title="缩略图（封面原图，横版）"
            @click="view = 'thumb'"
          >
            缩略图
          </button>
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'poster' }"
            title="海报图（2:3 竖版）"
            @click="view = 'poster'"
          >
            海报图
          </button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="jav-wall__state">
      <span>加载中…</span>
      <!-- 冷扫要走网络（数据源可能是网盘 / SMB 上的目录），十几秒是正常的 ——
           等过 4 秒补一句解释，免得用户以为卡死了。 -->
      <span v-if="slowHint" class="jav-wall__state-hint">
        正在读取目录，第一次会比较慢（本地目录在网盘 / 共享盘上时，每读一层都要走一次网络）。
        读完就会缓存，之后的翻页与切换都很快。
      </span>
    </div>
    <div v-else-if="!items.length" class="jav-wall__state">
      这个任务的输出目录里还没有番号影片。先在「STRM 任务」里同步一次，或确认任务的媒体类型与输出目录。
    </div>

    <div v-else ref="gridEl" class="jav-wall__grid" :class="`jav-wall__grid--${view}`">
      <article
        v-for="(item, index) in items"
        :key="item.id"
        class="jav-card jav-card--wall"
        :class="{ 'jav-card--picking': batchMode, 'jav-card--picked': batchMode && isPicked(item) }"
        role="button"
        tabindex="0"
        :title="batchMode ? `《${item.number || item.title}》· 点击选择` : `《${item.number || item.title}》· 点开详情`"
        :aria-pressed="batchMode ? isPicked(item) : undefined"
        @click="onCardClick(item)"
        @keydown.enter.prevent="onCardClick(item)"
        @keydown.space.prevent="onCardClick(item)"
      >
        <div
          class="jav-card__cover"
          :class="{ 'jav-card__cover--poster': view === 'poster', 'jav-card__cover--picked': batchMode && isPicked(item) }"
        >
          <!-- 两个视图各自的 URL；都没有或加载失败 → 统一占位图。
               这里给 lazy=false 并自己按视口决定「发不发」——见 imgWindow。 -->
          <MediaImage
            :src="imageSrc(item, index)"
            :alt="item.number"
            :lazy="false"
          />
          <!-- 还没轮到取图时占住位置，避免取到图之前卡片高度塌下去。
               ⚠️ 判据是「该不该有图但还没轮到」，**不是**「imageSrc 是空串」——
               没有侧车图的那一部，`imageSrc` 永远是空串，那样骨架会永盖在
               「暂无封面」上，看起来就是「一直加载不出来」（用户 2026-10-17 报的）。 -->
          <div v-if="imagePending(item, index)" class="jav-card__skeleton" />

          <!-- 批量选择开着时，卡片**正中**那个圈（视觉照 TMDB 订阅那套勾选圈）。
               未选 = 圈里一个加号；已选 = 打勾。

               ⚠️ 这个圈是**纯装饰**：`pointer-events: none`（见样式）。
               让它自己收点击的话，圈上停一次、article 上再翻一次 —— 等于永远选不上。 -->
          <span
            v-if="batchMode"
            class="jav-card__pick"
            :class="{ 'jav-card__pick--on': isPicked(item) }"
            aria-hidden="true"
          >
            <svg viewBox="0 0 24 24" width="26" height="26" focusable="false">
              <template v-if="isPicked(item)">
                <path
                  d="M5 12.5l4.2 4.2L19 7"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.4"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </template>
              <template v-else>
                <path
                  d="M12 6.5v11M6.5 12h11"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.2"
                  stroke-linecap="round"
                />
              </template>
            </svg>
          </span>

          <div v-if="!batchMode" class="jav-card__hover">
            <AppButton type="button" size="sm" variant="secondary" @click.stop="openEditor(item)">编辑</AppButton>
            <AppButton
              type="button"
              size="sm"
              variant="primary"
              :disabled="busyStem === item.stem"
              title="用本地 json 重建这一部的 nfo，并重新下载封面与海报（剧照只补缺的）"
              @click.stop="rebuild(item)"
            >
              {{ busyStem === item.stem ? "重刮中…" : "重刮" }}
            </AppButton>
          </div>
          <!-- 删除键在**右上角**（用户要求），与底部那两颗分开：
               它是破坏性动作，混在「编辑 / 重刮」里会点错。
               指过去才出现（触屏上常显，否则永远点不到），点开的是确认弹窗、
               不是直接删 —— 弹窗里会列出具体会删掉什么。 -->
          <button
            v-if="!batchMode"
            type="button"
            class="jav-card__del"
            title="删除这一部（本地文件与网盘那一份）"
            aria-label="删除"
            @click.stop="askDelete(item)"
          >
            <i class="fas fa-trash-can" />
          </button>
          <div v-if="busyStem === item.stem" class="jav-wall__busy" />
        </div>
        <div class="jav-wall__meta">
          <div class="jav-wall__number" :title="item.number">{{ item.number }}</div>
          <div class="jav-wall__title" :title="item.title">{{ item.title }}</div>
          <div class="jav-wall__sub">{{ subtitle(item) }}</div>
        </div>
      </article>
    </div>

    <!-- 详情抽屉 + 从它里面发起的播放窗。数据都在 click 那一刻才取（见 useWallDetail）。 -->
    <WallDetailDrawer
      :open="wall.open.value"
      :loading="wall.loading.value"
      :detail="wall.detail.value"
      :error="wall.error.value"
      @close="closeMovie"
      @play="wall.play"
      @open-actor="openActorFromMovie"
    />
    <!-- 演员详情：从上面那个影片详情里点演员进来（用户 2026-10-21 的要求）。
         `raised` 让它压过影片详情 —— 两个都是 elevated 同档，只靠 DOM 顺序不稳。
         `:searchable="false"`：这一面底下没有演员墙，别名点了没法去搜。

         `suspended`：从它里面点一部参演影片（或播放）时暂时收起它 —— 它比影片详情
         高一档，不收起来就会把影片详情 / 播放窗整个盖住（用户 2026-10-24 报的）。 -->
    <JavWallActorDetailDrawer
      :open="actorDetailOpen"
      :suspended="actorSuspended"
      :actor="actorDetailCard"
      raised
      @close="actorDetailOpen = false"
      @open-movie="openMovieFromActor"
    />
    <WallPlayer
      v-if="wall.playerOpen.value"
      :title="wall.playerTitle.value"
      :file="wall.playerFile.value"
      :subtitles="wall.playerSubtitles.value"
      @close="wall.closePlayer"
    />

    <!-- 拉到底续加载。哨兵**常驻 DOM**（不在上面的 v-if 分支里）—— 它一旦被移除，
         挂在它上面的 IntersectionObserver 就再也收不到回调了。
         没有文案时（首屏还在转圈 / 空列表）用 --idle 把高度收掉，免得底部留一条空白；
         元素本身仍在 DOM 里、观察器仍挂着。 -->
    <div
      ref="sentinel"
      class="jav-loadmore"
      :class="{ 'jav-loadmore--idle': !showLoadMore }"
    >
      <span v-if="loadingMore">正在加载更多…</span>
      <span v-else-if="hasMore">往下滚，还有 {{ total - items.length }} 条</span>
      <span v-else-if="items.length">已全部加载（{{ total }} 条）</span>
    </div>

    <StrmJavMetaDrawer
      :open="drawerOpen"
      :source="props.source"
      :item="editing"
      :full-sync-wipe="result?.full_sync_wipe ?? false"
      @close="drawerOpen = false"
      @changed="replaceItem"
    />

    <!-- 删除确认弹窗：会去服务端取「会删掉什么」的清单再让用户确认。 -->
    <StrmJavWallDeleteModal
      :open="deleting !== null"
      :source="props.source"
      :item="deleting"
      @close="deleting = null"
      @deleted="onDeleted"
    />

    <!-- 分类 tab 的右键菜单（批量选择没开时 `open` 恒假 → 什么都不弹）。 -->
    <AppContextMenu
      :open="catMenu.open"
      :x="catMenu.x"
      :y="catMenu.y"
      :items="catMenuItems"
      @select="onCatMenuSelect"
      @close="closeCatMenu"
    />
  </div>
</template>

<style scoped>
.jav-wall { display: flex; flex-direction: column; gap: 12px; }
.jav-wall__banner { margin: 0; padding: 10px 12px; border-radius: var(--radius-sm); background: rgba(180, 83, 9, 0.08); color: #b45309; font-size: 12px; line-height: 1.6; }
.jav-wall__bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.jav-wall__tabs { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.jav-tab__count { margin-left: 6px; font-size: 11px; opacity: 0.7; }
.jav-wall__tools { display: flex; align-items: center; gap: 8px; }
.jav-wall__view { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-pill); overflow: hidden; }
.jav-wall__view-btn { padding: 5px 12px; font-size: 12px; background: transparent; border: 0; color: var(--text-muted); cursor: pointer; }
.jav-wall__view-btn--active { background: var(--brand); color: #fff; }
.jav-wall__state { padding: 40px; text-align: center; color: var(--text-muted); }
/* 慢扫时那句解释：居中、限宽（一句话铺满整屏很难读），与「加载中…」之间留一点空。 */
.jav-wall__state-hint {
  display: block;
  max-width: 520px;
  margin: 10px auto 0;
  font-size: 12px;
  line-height: 1.6;
  opacity: 0.85;
}
/* 两套网格，按视图切：
   - 缩略图（横版）：5 列固定，保持原来的大小 —— 横图铺满宽度后本来就矮，再缩小就看不清了；
   - 海报图（竖版）：**与「STRM 刮削」那面墙同尺寸**（auto-fill 140px + 14px 间距）。
     两边都是 2:3 的海报，尺寸一致才像同一套界面；原来固定 5 列在宽屏上一张有 260px 宽，
     比刮削墙大了一大截。 */
.jav-wall__grid--thumb { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 16px; }
@media (max-width: 1280px) { .jav-wall__grid--thumb { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 900px) { .jav-wall__grid--thumb { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 640px) { .jav-wall__grid--thumb { grid-template-columns: repeat(2, minmax(0, 1fr)); } }

.jav-wall__grid--poster { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 14px; }

/* 海报图视图用 2:3，缩略图视图保持 .jav-card__cover 原本的 3:2 */
.jav-card__cover--poster { aspect-ratio: 2 / 3; }
.jav-wall__meta { padding: 8px 2px 0; display: flex; flex-direction: column; gap: 2px; }
.jav-wall__number { font-weight: 600; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__title { font-size: 12px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__sub { font-size: 11px; color: var(--text-muted); }

/* 悬停显形（照 CoverExtractToolCard 的 .cand-rm 那一套）：触屏上常显，否则永远点不到 */
.jav-card__hover { position: absolute; inset: auto 6px 6px 6px; display: flex; gap: 6px; justify-content: center; opacity: 0; transition: opacity 0.12s; z-index: 2; }
.jav-card:hover .jav-card__hover,
.jav-card:focus-within .jav-card__hover { opacity: 1; }
@media (hover: none) { .jav-card__hover { opacity: 1; } }

/* 右上角的删除键。**与底部那两颗同一套显隐规矩**（悬停 / 聚焦 / 触屏常显），
   但位置分开：它是破坏性动作，混在「编辑 / 重刮」里迟早会点错。
   背景压一层半透明黑，因为封面深浅不定 —— 纯图标在亮封面上会看不见。 */
.jav-card__del {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 3;
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 50%;
  background: rgba(15, 23, 42, 0.55);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s, background 0.12s;
}
.jav-card:hover .jav-card__del,
.jav-card:focus-within .jav-card__del { opacity: 1; }
@media (hover: none) { .jav-card__del { opacity: 1; } }
.jav-card__del:hover { background: var(--danger); }
.jav-card__del:focus-visible { opacity: 1; outline: 2px solid var(--danger); outline-offset: 1px; }
.jav-wall__busy { position: absolute; inset: 0; background: rgba(15, 23, 42, 0.25); }
/* 还没轮到取图的卡片：占住封面那块位置，免得图到之前高度塌下去、
   滚动位置跟着跳（那比多下几张图更难受）。 */
.jav-card__skeleton { position: absolute; inset: 0; background: var(--surface-sunken); }

/* ————————————————— 批量选择 —————————————————

   圈的位置是**卡片正中**（用户要求），视觉照 TMDB 订阅那套勾选圈
   （web/src/styles/tg-subscribe.css 的 .tg-card__check）。

   ⚠️ **不照抄那几条显隐**：那边挂在 `.tg-card:hover` 上（指过去才出现），
   而这里批量选择开着就该**常驻** —— 用户要从一排圈里点，圈得先看得见。
   ⚠️ **`pointer-events: none` 是必须的**：点击统一走 article（onCardClick），
   让圈自己收一次点击再冒泡到 article 就是勾了又取消 —— 表现成「永远选不上」。 */
.jav-card__pick {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 4;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  pointer-events: none;
  color: #fff;
  background: color-mix(in srgb, var(--surface) 45%, transparent);
  border: 2px solid rgba(255, 255, 255, 0.85);
  box-shadow: 0 1px 6px rgba(15, 23, 42, 0.45);
  transition: background 0.12s, border-color 0.12s, transform 0.12s;
}
/* 选中：亮色实心 + 打勾。用 brand 而不是纯白，扫一眼就能把「选中的那几部」分出来。 */
.jav-card__pick--on {
  background: var(--brand);
  border-color: var(--brand);
}
.jav-card--picked .jav-card__cover--picked { outline: 2px solid var(--brand); outline-offset: -2px; }
/* 键盘用户：卡片本身是 role=button + tabindex，聚焦时给一圈可见的焦点环。 */
.jav-card--picking:focus-visible { outline: 2px solid var(--brand); outline-offset: 2px; }

/* 「全选本页」：与右边那组视图切换同高（都是 12px 字），摆在它左边。 */
.jav-wall__pickall {
  padding: 5px 12px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.jav-wall__pickall:hover:not(:disabled) { border-color: var(--brand); color: var(--brand); }
.jav-wall__pickall:disabled { opacity: 0.5; cursor: default; }
/* 整分类态的提示：这个模式下台上**没有满屏的勾**（分类的全部成员前端拿不到），
   所以得有一句话替它说「现在要刮的是整个这一档」。与「全选本页」同高。 */
.jav-wall__whole-hint { font-size: 12px; color: var(--brand); }
</style>
