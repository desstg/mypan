<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { javImageURL } from "@/api/jav";
import { fetchJavWallActors, fetchJavWallActorProfile, type JavWallActorCard } from "@/api/strmJavWall";
import JavWallActorDetailDrawer from "@/components/admin/JavWallActorDetailDrawer.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import WallDetailDrawer from "@/components/admin/WallDetailDrawer.vue";
import WallPlayer from "@/components/admin/WallPlayer.vue";
import { useWallDetail } from "@/composables/useWallDetail";
import { popModal, pushModal } from "@/composables/modalStack";
import { lockPageScroll, unlockPageScroll } from "@/utils/scrollLock";
import "@/styles/jav.css";

/**
 * 演员墙：海报墙页头那颗人像图标点开的「演员列表页」。
 *
 * # 数据（用户明确要求：**不是上游数据**）
 *
 * 列的是**所有番号海报墙上的影片里真的出现过的**演员 —— 「所有」= 每一个番号
 * STRM 任务 + 每一个挂上来的番号本地目录，不限于当前选中的那面墙。
 * 来源是每部片自己的侧车 json（带 id / 头像 / 性别），没有侧车时回落 nfo 的
 * `<actor>`（只有名字）。
 *
 * 卡片上那个「N 部」是**墙上有几部片出现了这位演员**，不是上游报的参演总数 ——
 * 所以点进去看到的每一部，都能在墙上找到。
 *
 * # 默认不列男演员
 *
 * 用户 2026-10-21 要求：打开演员页默认只看女演员。男优**不是被前端筛掉**的，
 * 而是压根请求不到（后端 `include_male`）—— 勾上「显示男演员」才重新拉一遍。
 * 列表为空时还会说明「有 N 位男演员被隐藏」，免得看起来像功能坏了。
 *
 * # 为什么是整页浮层而不是 AppModal
 *
 * 用户要的是「一页演员列表」：上面搜索框，下面一片卡片，点卡片开详情抽屉。
 * 卡片大小还要求**与番号海报墙一致** —— 那面墙是面板里的一片 auto-fill 网格，
 * 卡片宽度由面板宽度决定。弹窗（AppModal 最宽 1000px）里铺同样列数的网格会窄一档，
 * 不满足「一样大」。
 *
 * 所以这里贴着后台内容区的边界铺满（left 让开侧边栏、top 让开顶栏，与
 * AdminSettingsDrawer 同一套定位令牌），网格的那几个数字与 StrmJavWall 的
 * `.jav-wall__grid--poster` 逐条对齐 —— 两面墙的卡片就是同一个尺寸。
 */
const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: [] }>();

const PAGE_SIZE = 60;

const items = ref<JavWallActorCard[]>([]);
const total = ref(0);
const totalPage = ref(0);
const sources = ref(0);
const movies = ref(0);
/** 这次被「不显示男演员」挡掉的位数（关键词命中的那批里数）。 */
const maleCount = ref(0);
/**
 * 是否列出男演员。**默认否**（用户 2026-10-21 的要求）——
 * 番号墙上女优占绝大多数，男优混在第一位列表里只会把要看的冲稀。
 */
const showMale = ref(false);
const loading = ref(false);
const loadingMore = ref(false);
/**
 * 后端回 `building` 时置上：索引正在**后台**建，这一页是空的但**墙上不一定没有演员**。
 *
 * 后端这么收口是因为这份索引要读遍墙上的侧车 / nfo（SMB 上实测两分钟出头）——
 * 同步做，群晖反代早就判超时回了 502（用户 2026-10-24 报的正是这个）。
 * 所以这里必须显示「正在整理」并轮询，绝不能把空表当成「一位演员都没有」。
 */
const building = ref(false);
const error = ref("");
const keywordInput = ref("");
const keyword = ref("");
/**
 * 「点名」的那位演员（从详情里点了别名时设上）。
 *
 * 别名常常来自 avbase / JAVDB（罗马音、旧艺名），墙上的索引里根本没这个写法 ——
 * 只拿这串字去过滤，用户点一下别名就得到「一个都没有」（用户 2026-10-22 的原话）。
 * 带上这个 key 之后，后端把那一位无条件留在结果里，于是「点别名」与「搜主名」
 * 看到的是同一张卡。用户自己改搜索框时它会被清掉（见模板上的 `@input`）。
 */
const anchorKey = ref("");

const scrollEl = ref<HTMLElement | null>(null);
const sentinelEl = ref<HTMLElement | null>(null);

const detailActor = ref<JavWallActorCard | null>(null);
const detailOpen = ref(false);

/**
 * 列表上那几张**没有头像**的卡：进页面之后在后台一位一位补。
 *
 * # 为什么列表接口不自己补
 *
 * 列表是一次聚合（135 位演员扫全部墙），补头像要打 avbase / JAVDB 两个外站，
 * 一位 1~2 秒 —— 塞进列表里就是「打开演员墙先白屏半分钟」。列表能给的只有
 * 「索引表里拼得出来」的那批头像（零网络，见后端 `javdbAvatarFromIndex`）。
 *
 * # 这里补的是什么
 *
 * 索引表里没有的演员（新人、欧美片、没进表的名字）只能靠资料层现查。
 * 每次查到的结果后端缓存 24h，所以**这里补完之后点进详情是秒开的**，
 * 不是白花一次请求。
 *
 * 并发 3：实测每位 1.6~2.6 秒，18 位缺头像时串行 31 秒、并发 3 约 11 秒。
 * 再多就变成拿外站当自家 CDN 刷了。
 */
const AVATAR_FILL_CONCURRENCY = 3;
const avatarFillTried = new Set<string>();
let avatarFillSeq = 0;

function scheduleAvatarFill() {
  const pending = items.value.filter((a) => a.key && !a.avatar_url && !avatarFillTried.has(a.key));
  if (!pending.length) return;
  void runAvatarFill(pending, avatarFillSeq);
}

async function runAvatarFill(pending: JavWallActorCard[], seq: number) {
  let cursor = 0;
  const worker = async () => {
    while (cursor < pending.length) {
      // 换了关键词 / 关掉重开：这一轮的结果已经不该落到任何一张卡上。
      if (seq !== avatarFillSeq) return;
      const card = pending[cursor++];
      avatarFillTried.add(card.key);
      try {
        const profile = await fetchJavWallActorProfile(card.key);
        if (seq !== avatarFillSeq) return;
        const url = (profile?.avatar_url ?? "").trim();
        if (!url) continue;
        // 就地改那一张卡：`v-for` 的 key 是 `key`，不会因此重建整个网格。
        const target = items.value.find((a) => a.key === card.key);
        if (target && !target.avatar_url) target.avatar_url = url;
      } catch {
        // 补头像失败不影响任何别的功能：卡上继续显示占位图。
      }
    }
  };
  await Promise.all(
    Array.from({ length: Math.min(AVATAR_FILL_CONCURRENCY, pending.length) }, worker),
  );
}

/**
 * 竞态序号：敲关键词时旧请求可能后回来。
 *
 * 与面板里 `loadItemsSeq` 同一条规矩 —— 只认最后一次发起的那一轮，
 * 否则「搜 A 再改成 B」会闪一下 A 的结果。
 */
let loadSeq = 0;

// —————————————————— 「正在整理」时的轮询 ——————————————————
//
// 后端把索引建在后台（两分钟量级，见 javwall_actor_index.go），所以第一次打开
// 拿到的是 `building: true` + 空表。这里轮询到它建好为止，**期间不算加载失败、
// 更不算「没有演员」**。
let buildingTimer: ReturnType<typeof setTimeout> | null = null;
/** 已经等了多久 —— 只用来把轮询间隔慢慢放长（建得越久，越不急着问）。 */
let buildingWaitedMs = 0;
const BUILDING_FIRST_POLL_MS = 2_000;
const BUILDING_MAX_POLL_MS = 8_000;
/**
 * 等多久就不再干等了。
 *
 * 后台建一次的预算是 10 分钟（见 javwall_actor_index.go），但让用户盯着
 * 「正在整理」十分钟没有意义：多半是那面墙（网盘 / SMB）真的不通。5 分钟后
 * 改成一句人能动手的话 —— 关掉重开一次，会比继续转圈有用。
 */
const BUILDING_GIVE_UP_MS = 5 * 60_000;

function clearBuildingPoll() {
  buildingWaitedMs = 0;
  if (buildingTimer) {
    clearTimeout(buildingTimer);
    buildingTimer = null;
  }
}

function scheduleBuildingPoll() {
  if (buildingTimer) return;
  if (buildingWaitedMs >= BUILDING_GIVE_UP_MS) {
    // 等太久了：不再转圈，给一句能动手的话。后台那一轮照旧在跑（它有自己的
    // 10 分钟预算），用户关掉重开时多半正好撞上它建完。
    building.value = false;
    error.value = "演员资料整理超时了（那面墙可能暂时连不上）。过一会儿关掉重开一次试试。";
    return;
  }
  const delay = Math.min(
    BUILDING_FIRST_POLL_MS * Math.pow(1.5, Math.floor(buildingWaitedMs / BUILDING_FIRST_POLL_MS)),
    BUILDING_MAX_POLL_MS,
  );
  buildingTimer = setTimeout(() => {
    buildingTimer = null;
    buildingWaitedMs += delay;
    if (!props.open || !building.value) return;
    void pollWhileBuilding();
  }, delay);
}

/** 只问一句「建好了没」：不碰 `loading`（否则整页每几秒闪一次「加载中」）。 */
async function pollWhileBuilding() {
  const seq = loadSeq;
  try {
    const res = await fetchJavWallActors({
      keyword: keyword.value,
      page: 1,
      page_size: PAGE_SIZE,
      include_male: showMale.value,
      anchor: anchorKey.value || undefined,
    });
    if (seq !== loadSeq || !props.open) return;
    if (res.building) {
      scheduleBuildingPoll();
      return;
    }
    // 建好了：走一次正常 reload，把网格 / 补头像 / 哨兵那一套完整铺开。
    void reload();
  } catch {
    // 网络抖一下不算失败：下一拍接着问（真失败时 reload 会给出错误）。
    if (seq === loadSeq && props.open) scheduleBuildingPoll();
  }
}

const hasMore = computed(() => items.value.length < total.value);

/** 影片详情抽屉（点演员详情里的一部片 → 那一面墙自己的详情抽屉）。 */
const wall = useWallDetail();

/**
 * 播放期间演员详情是不是被我们收起来了（关掉播放窗要放回来）。
 *
 * 这一页的路径是「演员墙 → 演员详情 → 点一部片 → 影片详情 → 播放」：播放时
 * `useWallDetail.play()` 只收起**影片详情**自己那层（见那边的 suspendedByPlayer），
 * 而演员详情同样是 elevated（10040），照样压在全屏播放窗（10000）上面 ——
 * 用户看到的就是「播放页被详情页挡住了」（用户 2026-10-24 报的）。
 *
 * ⚠️ 别去抬播放窗的 z-index：`.file-preview` 的 10000 是全局外壳刻意的档位
 * （低于 --z-dropdown，好让下拉菜单浮在预览之上），抬它会波及所有预览场景。
 *
 * 只切「收不收起」、**不动 `detailOpen`**：关掉 `open` 会把已取的影片与资料整份
 * 清掉（见抽屉里 `suspended` 那条），关掉播放窗后用户会看到一整页重新加载。
 */
const actorSuspendedByPlayer = ref(false);

watch(
  () => wall.playerOpen.value,
  (playing) => {
    if (playing) {
      if (!detailOpen.value) return;
      actorSuspendedByPlayer.value = true;
      return;
    }
    actorSuspendedByPlayer.value = false;
  },
);

// —————————————————— 浮层的开关：Esc / 锁滚动 ——————————————————
const myToken = Symbol("jav-wall-actors");
let keyHandler: ((e: KeyboardEvent) => void) | null = null;

function openLayer() {
  pushModal(myToken);
  lockPageScroll();
  keyHandler = (e: KeyboardEvent) => {
    if (e.key !== "Escape") return;
    // 一层一层关：播放窗 → 详情抽屉 → 演员详情 → 演员列表。
    // 这些子层有的是 Teleport 出去、自己没挂 Esc（播放窗 / 墙详情抽屉都不挂），
    // 所以这里统一收口，别让用户在播放中途被一下弹回海报墙。
    if (wall.playerOpen.value) {
      wall.closePlayer();
      return;
    }
    if (wall.open.value) {
      wall.close();
      return;
    }
    if (detailOpen.value) {
      closeDetail();
      return;
    }
    emit("close");
  };
  window.addEventListener("keydown", keyHandler);
}

function closeLayer() {
  popModal(myToken);
  unlockPageScroll();
  if (keyHandler) {
    window.removeEventListener("keydown", keyHandler);
    keyHandler = null;
  }
}

function openDetail(actor: JavWallActorCard) {
  detailActor.value = actor;
  detailOpen.value = true;
}

function closeDetail() {
  detailOpen.value = false;
}

/**
 * 详情里点了别名：关掉抽屉、把它填进搜索框，并**点名**这位演员。
 *
 * 为什么要点名而不是只把文字塞进搜索框：详情页里显示的别名有一大半来自
 * avbase / JAVDB（罗马音、旧艺名），墙上的关键词索引里没有这个写法 ——
 * 只按文字过滤必然是空列表（用户 2026-10-22 报的就是这个）。点名之后，
 * 别名搜索的结果与搜主名**是同一张卡**。
 *
 * 同时也把搜索框里的文字清成这个别名：用户看得见「我在搜谁」。
 */
function searchByActorName(name: string, actor: JavWallActorCard) {
  detailOpen.value = false;
  anchorKey.value = actor?.key ?? "";
  // 两道都自己写：防抖那条等 300ms 才把输入框的字搬进 `keyword`，而这里要的是
  // 「关掉抽屉的这一拍结果就是这个人」。先撤掉待执行的防抖，免得 300ms 后再多打一次。
  if (searchTimer) {
    clearTimeout(searchTimer);
    searchTimer = null;
  }
  keywordInput.value = name;
  keyword.value = name.trim();
  void reload();
}

/** 用户自己动手改搜索框：点名的意思就没了（他要搜的是新输入的那串字）。 */
function onKeywordInput() {
  if (!anchorKey.value) return;
  anchorKey.value = "";
}

/**
 * 在**影片详情**里点了某位演员（番号那面才有 `key`）→ 打开演员详情。
 *
 * ⚠️ **先把影片详情关掉**：两个抽屉都是 elevated（同一个 z-index），谁在上面
 * 只看 DOM 顺序 —— 而本组件里影片详情写在演员详情**之后**（因为「演员详情 → 点
 * 一部片 → 影片详情」那条路需要影片详情在上）。同一份 DOM 顺序满足不了两个方向，
 * 所以这里走「关一层、开一层」，两次点击都不打架，面包屑也清楚：
 * 演员墙 → 影片详情 → 演员详情，Esc 一路退回演员墙。
 */
function openActorFromMovie(actor: { key?: string; name: string; gender?: number; avatar_url?: string }) {
  if (!actor.key) return;
  wall.close();
  const known = items.value.find((a) => a.key === actor.key);
  detailActor.value =
    known ??
    ({
      key: actor.key,
      id: undefined,
      name: actor.name,
      gender: actor.gender ?? 0,
      avatar_url: actor.avatar_url,
      movies_count: 0,
    } as JavWallActorCard);
  detailOpen.value = true;
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      keywordInput.value = "";
      keyword.value = "";
      // 上次「点别名点名」的那一份不该带到这一次：重开是全新的搜索。
      anchorKey.value = "";
      items.value = [];
      total.value = 0;
      totalPage.value = 0;
      maleCount.value = 0;
      building.value = false;
      clearBuildingPoll();
      // 每次打开都回到默认口径（不列男优）—— 上次勾上的状态不该带到下一次。
      showMale.value = false;
      // 补头像的「已试过」记录也一并清掉：后端那份资料 24h 缓存，重开时再补一遍
      // 每位只要 0.02 秒（命中缓存），比「重开就再也补不上」划算得多。
      avatarFillTried.clear();
      error.value = "";
      detailOpen.value = false;
      // 上一轮「播放时收起演员详情」的标记不带到这一次（整页浮层是新的）。
      actorSuspendedByPlayer.value = false;
      wall.close();
      openLayer();
      void reload();
      return;
    }
    wall.close();
    closeDetail();
    // 浮层整块被卸载：观察器连同它盯着的哨兵一起作废（重开是新的 DOM）。
    disconnectSentinel();
    // 关掉就停止轮询：用户已经不在看了，没必要每隔几秒问一次后台。
    clearBuildingPoll();
    building.value = false;
    closeLayer();
  },
);

onBeforeUnmount(() => {
  // 组件没了，在途的补头像结果不该再往任何地方写。
  avatarFillSeq++;
  clearBuildingPoll();
  wall.close();
  closeDetail();
  closeLayer();
});

// —————————————————— 搜索（300ms 防抖） ——————————————————
let searchTimer: ReturnType<typeof setTimeout> | null = null;
watch(keywordInput, (value) => {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    const next = value.trim();
    // 点别名那条已经拿同一串字拉过了（见 searchByActorName），这里不必再打一次。
    if (next === keyword.value) return;
    keyword.value = next;
    void reload();
  }, 300);
});

// —————————————————— 列表 ——————————————————
async function reload() {
  const seq = ++loadSeq;
  // 换关键词 / 换「显示男演员」：上一轮还在补的头像与这一屏无关了，让它们作废。
  // 已补到的那几张卡留在 `avatarFillTried` 里，不重复打外站。
  avatarFillSeq++;
  loading.value = true;
  loadingMore.value = false;
  error.value = "";
  clearBuildingPoll();
  try {
    const res = await fetchJavWallActors({
      keyword: keyword.value,
      page: 1,
      page_size: PAGE_SIZE,
      include_male: showMale.value,
      anchor: anchorKey.value || undefined,
    });
    if (seq !== loadSeq) return;
    building.value = !!res.building;
    items.value = res.items ?? [];
    total.value = res.total ?? 0;
    totalPage.value = res.total_page ?? 0;
    sources.value = res.sources ?? 0;
    movies.value = res.movies ?? 0;
    maleCount.value = res.male_count ?? 0;
    if (scrollEl.value) scrollEl.value.scrollTop = 0;
  } catch (e) {
    if (seq !== loadSeq) return;
    building.value = false;
    items.value = [];
    total.value = 0;
    error.value = getApiErrorMessage(e, "加载演员列表失败");
  }
  if (seq !== loadSeq) return;
  // ⚠️ **必须先落 `loading`，再等下一拍去挂哨兵**。
  //
  // 哨兵写在模板的 `v-else` 那一支里（`v-if="loading"` / `v-else-if="error"` 都把它替换掉），
  // 所以 `loading` 还开着的时候 `sentinelEl` 是 null，`bindSentinel()` 会**静默早退**，
  // 之后再也没有人补挂 —— 观察者永远不观察哨兵，`loadMore()` 一次都不会被触发。
  //
  // 用户 2026-10-22 报的正是这个：「演员页好像只能显示 60 个」= 永远停在第一页。
  // 这是本项目的「静默变空」家族：不报错、不抛异常，只是少了一半东西。
  loading.value = false;
  await nextTick();
  if (seq !== loadSeq) return;
  if (building.value) {
    // 索引还在后台建：这一页是空的，但那是**还没有**，不是**没有**。
    // 只轮询，不挂哨兵（没东西可翻），也不补头像（一位都没有）。
    scheduleBuildingPoll();
    return;
  }
  bindSentinel();
  scheduleAvatarFill();
}

// 勾 / 取消「显示男演员」：从第一页重来（分页是按这一档算的，接着翻会错位）。
//
// ⚠️ 用 `:checked` + 自己的 change 处理，**不用 `v-model`**：复选框的 v-model 是
// 一条「change 时写回状态」的监听，而用户那一条 `@change` 会先跑 —— 先读到的
// 是**旧**值，等于勾上之后还按「不显示男优」去请求。手动先赋值再 reload，顺序明确。
function toggleMale(e: Event) {
  showMale.value = (e.target as HTMLInputElement).checked;
  void reload();
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return;
  // ⚠️ 用**总数页**算下一页，不能拿 `items.length / PAGE_SIZE` 推：最后一页
  // 不满时会算出浮点 / 重复的页码，结果是「无限拉同一个末页」。
  if (totalPage.value > 0 && items.value.length / PAGE_SIZE + 1 > totalPage.value) return;
  const seq = loadSeq;
  loadingMore.value = true;
  const page = Math.floor(items.value.length / PAGE_SIZE) + 1;
  try {
    const res = await fetchJavWallActors({
      keyword: keyword.value,
      page,
      page_size: PAGE_SIZE,
      include_male: showMale.value,
      // 续加载也要带上：后端每次都从头算一遍命中集合，少传这一个键，
      // 第 2 页的偏移就会与第 1 页错开一位（丢一位 / 重一位）。
      anchor: anchorKey.value || undefined,
    });
    if (seq !== loadSeq) return;
    items.value = [...items.value, ...(res.items ?? [])];
    total.value = res.total ?? total.value;
    totalPage.value = res.total_page ?? totalPage.value;
  } catch (e) {
    if (seq === loadSeq) error.value = getApiErrorMessage(e, "加载更多失败");
  } finally {
    if (seq === loadSeq) loadingMore.value = false;
  }
  if (seq !== loadSeq) return;
  // 与 `reload()` 同一个理由：挂完哨兵再补头像。
  //
  // 这里还要**先落 `loadingMore` 再 `bindSentinel`**：`observe()` 会立刻回一次
  // 当前是否可见，那一刻 `loadingMore` 还开着的话 `loadMore()` 直接早退 ——
  // 若追加完的这一页仍然不够一屏，观察器之后就再也不会回调，列表停在这里。
  await nextTick();
  bindSentinel();
  scheduleAvatarFill();
}

// —————————————————— 拉到底续加载 ——————————————————
// 用观察者而不是 scroll 事件：浮层里滚动条是容器自己的，监听 scroll 每次都要算
// scrollHeight（几百张图的长列表里那是一次布局查询）。哨兵常驻 DOM，重复
// observe 同一元素是幂等的，每次列表变长后补一次即可。
let observer: IntersectionObserver | null = null;
/**
 * 观察器当前绑的滚动容器。
 *
 * 浮层是 `v-if="open"`，关掉再打开时 `scrollEl` 是**另一个 DOM 元素** ——
 * 旧观察器的 `root` 已经脱离文档，`observe()` 到新哨兵上也不会再有任何回调。
 * 所以 root 换了就得重建观察器（否则「关一次再开就再也翻不了页」）。
 */
let observerRoot: HTMLElement | null = null;

function disconnectSentinel() {
  observer?.disconnect();
  observer = null;
  observerRoot = null;
}

function bindSentinel() {
  const root = scrollEl.value;
  const target = sentinelEl.value;
  if (!root || !target) return;
  if (typeof IntersectionObserver === "undefined") return;
  if (!observer || observerRoot !== root) {
    observer?.disconnect();
    observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) void loadMore();
      },
      { root, rootMargin: "200px" },
    );
    observerRoot = root;
  } else {
    observer.disconnect();
  }
  observer.observe(target);
}

onBeforeUnmount(() => {
  disconnectSentinel();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="jav-wall-actors" role="dialog" aria-label="演员">
      <header class="jav-wall-actors__head">
        <button type="button" class="jav-wall-actors__back" aria-label="返回" @click="emit('close')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M5 12h14m-6-6 6 6-6 6" />
          </svg>
        </button>
        <h2 class="jav-wall-actors__title">
          演员
          <span v-if="total > 0" class="jav-wall-actors__total">共 {{ total }} 位</span>
        </h2>
        <!-- 默认不列男优（用户要求）：女优占绝大多数，男优混进来只冲稀列表。
             它不是「筛掉」而是「不请求」——所以勾上要重新拉一遍（见 onToggleMale）。 -->
        <label class="jav-wall-actors__male" :title="maleCount > 0 ? `有 ${maleCount} 位男演员没显示` : undefined">
          <!-- ⚠️ 不用 v-model：它的写回发生在 change 之后，会和下面的 toggleMale 抢顺序
               （见脚本里那段）。这里 :checked + 自己赋值，一次搞定。 -->
          <input type="checkbox" :checked="showMale" @change="toggleMale" />
          <span>显示男演员</span>
          <span v-if="!showMale && maleCount > 0" class="jav-wall-actors__male-count">{{ maleCount }}</span>
        </label>
        <label class="jav-wall-actors__search">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" />
            <path d="m10.5 10.5 3 3" />
          </svg>
          <input
            v-model="keywordInput"
            type="search"
            placeholder="搜索演员名字"
            aria-label="搜索演员"
            @input="onKeywordInput"
          />
        </label>
      </header>

      <div ref="scrollEl" class="jav-wall-actors__body">
        <div v-if="loading" class="jav-empty">加载中…</div>
        <!-- 索引在后台建（第一次打开 / 墙刚变过）。第一次要读遍整面墙的侧车与 nfo，
             网盘 / SMB 上是一两分钟的事 —— 这里必须说「正在整理」并等，
             ⚠️ 绝不能掉进下面那个「一位演员都没有」的分支（那是本项目最老的坑）。 -->
        <div v-else-if="building" class="jav-empty">
          <div class="jav-empty__icon">⏳</div>
          <div>正在整理墙上的演员资料…</div>
          <div class="jav-wall-actors__hint">
            第一次打开要把每部片的侧车与 nfo 读一遍，网盘上大概一两分钟。<br />
            建好之后会自己出来，不用刷新页面。
          </div>
        </div>
        <div v-else-if="error" class="jav-empty">
          <div class="jav-empty__icon">⚠️</div>
          <div>{{ error }}</div>
        </div>
        <div v-else-if="!items.length" class="jav-empty">
          <div class="jav-empty__icon">👤</div>
          <!-- 点名了却一个人都没有：那位演员已经不在墙上了（影库刚变过）。
               这时按关键词说「没找到」会误导 —— 用户点的多半是别名，本来就搜不到。 -->
          <div v-if="anchorKey">这位演员已经不在墙上了（影库可能刚变过），关掉重开一次试试。</div>
          <div v-else-if="keyword && !maleCount">没有找到名字里带「{{ keyword }}」的演员。</div>
          <!-- 关键词只命中男优：默认不列，要说清「加个勾就能看到」而不是「没找到」。 -->
          <div v-else-if="keyword && !showMale">
            名字里带「{{ keyword }}」的只有 {{ maleCount }} 位男演员。<br />
            勾上「显示男演员」就能看到。
          </div>
          <!-- 空列表的几种原因要分开说：一面墙都没有 / 墙上有片但没演员 / 全是男优。 -->
          <div v-else-if="!sources">
            还没有番号海报墙。<br />
            先在海报墙上加一个「媒体类型 = 番号影片」的任务，或挂一个番号本地目录。
          </div>
          <div v-else-if="!movies">
            番号墙上一部片都没有，所以还没有演员。
          </div>
          <div v-else-if="!showMale && maleCount > 0">
            墙上的演员都是男演员（{{ maleCount }} 位），已按默认口径隐藏。<br />
            勾上「显示男演员」就能看到。
          </div>
          <div v-else>
            墙上的 {{ movies }} 部影片都没带演员信息。<br />
            对它们跑一次「在线刮削」，侧车与 nfo 里补上演员之后就会出现在这里。
          </div>
        </div>

        <template v-else>
          <div class="jav-wall-actors__grid">
            <article
              v-for="actor in items"
              :key="actor.key"
              class="jav-card jav-wall-actors__card"
              role="button"
              tabindex="0"
              :title="`${actor.name} · ${actor.movies_count} 部作品`"
              @click="openDetail(actor)"
              @keyup.enter.prevent="openDetail(actor)"
              @keydown.space.prevent="openDetail(actor)"
            >
              <div class="jav-card__cover jav-wall-actors__cover">
                <MediaImage
                  :src="javImageURL(actor.avatar_url)"
                  :alt="actor.name"
                  variant="person"
                  :lazy="false"
                />
                <!-- 性别：只标男优（与放映详情里的演员列表同一口径 —— 女演员占大多数，不标）。 -->
                <span v-if="actor.gender === 1" class="jav-wall-actors__gender">男</span>
                <span class="jav-wall-actors__count">{{ actor.movies_count }} 部</span>
              </div>
              <div class="jav-wall-actors__meta">
                <div class="jav-wall-actors__name" :title="actor.name">{{ actor.name }}</div>
                <div class="jav-wall-actors__sub">共 {{ actor.movies_count }} 部作品</div>
              </div>
            </article>
          </div>
          <!-- 哨兵常驻 DOM：它一旦被移除，挂在它上面的观察者就再也收不到回调。 -->
          <div ref="sentinelEl" class="jav-wall-actors__more">
            <span v-if="loadingMore">正在加载更多…</span>
            <span v-else-if="hasMore">往下滚，还有 {{ total - items.length }} 位</span>
            <span v-else>已全部加载（{{ total }} 位）</span>
          </div>
        </template>
      </div>

      <JavWallActorDetailDrawer
        :open="detailOpen"
        :suspended="actorSuspendedByPlayer"
        :actor="detailActor"
        :raised="!wall.open.value"
        searchable
        @close="closeDetail"
        @open-movie="(source, movie) => wall.openJav(source, movie)"
        @search="searchByActorName"
      />

      <!-- 墙上的影片详情 + 从它里面发起的播放窗：与在海报墙上点一张卡**同一条路**。 -->
      <WallDetailDrawer
        v-if="open"
        :open="wall.open.value"
        :loading="wall.loading.value"
        :detail="wall.detail.value"
        :error="wall.error.value"
        @close="wall.close"
        @play="wall.play"
        @open-actor="openActorFromMovie"
      />
      <WallPlayer
        v-if="wall.playerOpen.value"
        :title="wall.playerTitle.value"
        :file="wall.playerFile.value"
        :subtitles="wall.playerSubtitles.value"
        @close="wall.closePlayer"
      />
    </div>
  </Teleport>
</template>

<style scoped>
/* 贴着后台内容区铺满：left / top 让开侧边栏与顶栏，与 AdminSettingsDrawer 同一套令牌。
   这样网格的可用宽度与海报墙那块面板几乎一致 —— 卡片就是同一个尺寸。 */
.jav-wall-actors {
  position: fixed;
  top: var(--admin-chrome-h, 44px);
  right: 0;
  bottom: 0;
  left: var(--sidebar-width, 220px);
  z-index: 2500;
  display: flex;
  flex-direction: column;
  background: var(--bg);
  /* 入场轻微上浮：整页浮层直接闪出来会像是一次页面跳转。 */
  animation: jav-wall-actors-in 0.16s ease-out;
}

@keyframes jav-wall-actors-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.jav-wall-actors__head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-soft);
  background: var(--panel-head-bg);
}

.jav-wall-actors__back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
}
.jav-wall-actors__back:hover {
  color: var(--text);
  border-color: var(--brand);
}
.jav-wall-actors__back svg {
  width: 15px;
  height: 15px;
}

.jav-wall-actors__title {
  margin: 0;
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
  white-space: nowrap;
}
.jav-wall-actors__total {
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 500;
}

.jav-wall-actors__search {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin-left: auto;
  width: min(280px, 42vw);
  padding: 0 10px;
  height: 36px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
}.jav-wall-actors__search:focus-within {
  border-color: var(--brand);
}
.jav-wall-actors__search svg {
  flex: 0 0 auto;
  width: 15px;
  height: 15px;
}
.jav-wall-actors__search input {
  min-width: 0;
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text);
  font-size: 13px;
}

/* 「显示男演员」：搜索框左边的一个小勾。默认关着 —— 它是**附加**控件，
   所以字与框都比搜索框淡一档，不抢搜索框的注意力。 */
.jav-wall-actors__male {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
  height: 36px;
  padding: 0 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12.5px;
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
}
.jav-wall-actors__male:hover {
  border-color: var(--brand);
  color: var(--text);
}
.jav-wall-actors__male input {
  margin: 0;
  accent-color: var(--brand);
  cursor: pointer;
}
.jav-wall-actors__male-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 10.5px;
  line-height: 16px;
  text-align: center;
}

.jav-wall-actors__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px;
}

/* ⚠️ 与 StrmJavWall 的 `.jav-wall__grid--poster` **逐条对齐**（列宽 140 / 间距 14）：
   用户明确要求「演员卡片大小也和番号海报墙的卡片一样大」。
   改一面就得同时改另一面，否则两边会悄悄错开一档。 */
.jav-wall-actors__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 14px;
}

.jav-wall-actors__card {
  overflow: hidden;
}

/* 卡片下半截：与 StrmJavWall 的 .jav-wall__meta 同一套内边距与字号，
   两面墙的卡片看起来才是同一件东西。 */
.jav-wall-actors__meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px 10px;
}
.jav-wall-actors__name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jav-wall-actors__sub {
  font-size: 11px;
  color: var(--text-muted);
}

/* 头像按 2:3 的竖版画（与海报墙的 poster 视图一致）。 */
.jav-wall-actors__cover {
  aspect-ratio: 2 / 3;
}

.jav-wall-actors__gender {
  position: absolute;
  top: 6px;
  left: 6px;
  z-index: 2;
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: rgba(59, 130, 246, 0.92);
  color: #fff;
  font-size: 10.5px;
  font-weight: 600;
}

/* 作品数：右上角，与海报墙的角标同一套观感（半透明深底白字）。 */
.jav-wall-actors__count {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 2;
  padding: 1px 7px;
  border-radius: 5px;
  background: rgba(15, 23, 42, 0.6);
  color: #fff;
  font-size: 10.5px;
  white-space: nowrap;
}

.jav-wall-actors__more {
  padding: 18px 0 8px;
  text-align: center;
  color: var(--text-muted);
  font-size: 12px;
}

/* 「正在整理」下面那句解释：比正文淡一档，别抢标题。 */
.jav-wall-actors__hint {
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.7;
}

@media (max-width: 560px) {
  /* 手机：没有侧边栏，浮层要吃满整屏 */
  .jav-wall-actors {
    left: 0;
  }
  /* 搜索框在手机上一行放不下（返回键 + 标题 + 「显示男演员」勾 + 搜索框加起来
     必超 100vw），原来靠 `width: min(180px, 45vw)` 硬缩，还是溢出屏幕。
     用户 2026-10-23 要「搜索栏下一行显示」：让头部允许换行，搜索框自己占满
     一整行（`flex: 1 1 100%` 顶不出 100%，后面就跟不下别的东西了）。 */
  .jav-wall-actors__head {
    flex-wrap: wrap;
    row-gap: 10px;
  }
  .jav-wall-actors__search {
    flex: 1 1 100%;
    width: auto;
    margin-left: 0;
  }
}
</style>
