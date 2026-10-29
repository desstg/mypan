<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { javImageURL } from "@/api/jav";
import {
  deleteJavActorAvatar,
  fetchJavWallActorDetail,
  fetchJavWallActorProfile,
  fetchJavWallActorTMDB,
  javActorPhotoURL,
  uploadJavActorAvatar,
  type JavWallActorCard,
  type JavWallActorMovie,
  type JavWallActorProfile,
  type JavWallActorTMDB,
} from "@/api/strmJavWall";
import type { WallSourceRef } from "@/api/strmScrape";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import AppMenuPanel from "@/components/base/AppMenuPanel.vue";
import BusySpinner from "@/components/base/BusySpinner.vue";
import MediaImage from "@/components/base/MediaImage.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { toast } from "@/composables/useToast";
import type { DropdownMenuItem } from "@/types/menu";
import "@/styles/jav.css";

/**
 * 演员详情抽屉（演员墙里点一张卡打开）。
 *
 * # 数据来自**墙上的影片**
 *
 * `fetchJavWallActorDetail` 回的是「这位演员参演的、墙上真实存在的那几部」——
 * 后端从每部片的侧车 json / nfo 里读出来再聚合（见 strmscrape/javwall_actors.go），
 * 不是上游演员页的参演列表。所以这里每一张卡都能在墙上一部一部找到。
 *
 * # 主名与别名
 *
 * 列表带回来的 `name` 已经是**索引表里的中文统一名**（没这条时就是侧车里的原名），
 * `aliases` 是同一张表里的其它写法（日文原名、罗马音、旧艺名）。用户要求
 * （2026-10-21）主名显示统一名、别名一并列出来 —— 别名那行点一下就能复制/搜索，
 * 因为用户往往只记得侧车里那个日文写法。
 *
 * # TMDB 档案是**补充**
 *
 * 侧车 / nfo 只有 id、名字、性别、头像四样。TMDB 那边可能有简介、生日、出生地，
 * 就顺手显示一段（后端按名字核对过人才会给，见 lookJavActorTMDB）。
 * ⚠️ 它**不参与**「演过哪些片」—— 那一栏永远只有墙上的影片，这是用户明确要求的。
 *
 * # 影片卡片点开的是**那一面墙**的详情抽屉
 *
 * 每张卡带齐了 `strm_task_id` / `local_dir_id` + `rel_dir` + `stem`，点一下往上传
 * （`open-movie`），由演员墙那边调 `useWallDetail().openJav(...)` —— 与在海报墙上
 * 点一张卡**逐字相同**的那条路（同一个抽屉、同一个播放窗、同一套字幕）。
 *
 * ⚠️ 不在这里自己开抽屉：那个抽屉是 elevated（10040）一档，而本抽屉也是；两个
 * **兄弟**叠在一起时谁在上面由 DOM 顺序决定，不好控。交给外层统一挂一份，
 * 关掉影片详情之后回到这里，面包屑是干净的。
 */
const props = defineProps<{
  open: boolean;
  actor: JavWallActorCard | null;
  /**
   * 别名点一下能不能拿去搜演员墙。
   *
   * 从**演员墙**打开时能（那份列表就在下面，搜索框是它的）；从**海报墙的影片详情**
   * 打开时不能 —— 那里没有演员墙可搜，做成可点却点了没反应比不可点更糟。
   */
  searchable?: boolean;
  /**
   * 打开时压在**另一个 elevated 抽屉**之上（例如从影片详情里点演员进来）。
   *
   * 只用 `elevated` 时两个抽屉同档，谁在上面看 DOM 顺序 —— 电影详情写在后面就把它盖住。
   */
  raised?: boolean;
  /**
   * 暂时收起（**不卸载内容**），给「从参演影片里开播放窗」那一路用。
   *
   * 播放窗 `.file-preview` 的 z-index 是 10000，本抽屉是 elevated（10040）——
   * 直接播会被它整个盖住。关掉 `open` 也能收起来，但那样会**清空**已取的影片与
   * 资料、重开时整页重拉一遍并跳回顶部；所以这里单独给一个只影响显示的开关。
   */
  suspended?: boolean;
}>();

const emit = defineEmits<{
  close: [];
  /** 打开这一部**墙上的**影片（数据源 + 定位三件套）。 */
  openMovie: [source: WallSourceRef, movie: JavWallActorMovie];
  /**
   * 点别名 → 拿它去搜演员墙。
   *
   * 第二个参数把**这位演员本人**一并交出去：别名的来源常常是 avbase / JAVDB
   * （罗马音、旧艺名），墙上的关键词索引里根本没有这个写法，只拿文字去搜必然
   * 搜出空列表。外层据此「点名」锁定这一位，结果与搜主名看到的是同一张卡。
   */
  search: [keyword: string, actor: JavWallActorCard];
  /**
   * 用户换了这位演员的头像（上传或恢复默认）。
   *
   * `url` 为空串 = 恢复默认（回到侧车 / 资料层那张）。外层据此把墙上那张卡就地
   * 改掉，用户不必关掉重开 —— 但**真正的事实来源仍然是后端**（下一次刷新带回来的
   * 就是它），这个回调只是让当前这一屏立刻对上。
   */
  avatarChanged: [key: string, url: string];
}>();

const detail = ref<JavWallActorCard | null>(null);
const movies = ref<JavWallActorMovie[]>([]);
/** TMDB 补充档案：**单独一条请求**，晚到 / 失败都只影响那一段。 */
const tmdbInfo = ref<JavWallActorTMDB | null>(null);
/** 资料档案（生日 / 出身 / 三围 / 头像）：同样是一条独立的补充请求。 */
const profileInfo = ref<JavWallActorProfile | null>(null);
/**
 * 用户**自己上传**的那张头像（服务端地址）。空串 = 没传过，走自动那几档。
 *
 * 声明在这里（而不是下面那一段）是因为 `photoChain` 要用它，而 computed 的
 * 定义顺序决定求值顺序 —— 放后面读到的会是 `undefined`。
 */
const customAvatar = ref("");
const loading = ref(false);
const error = ref("");

/** 表头优先用刷新回来的那份，没有再回落到列表带过来的。 */
const header = computed(() => detail.value ?? props.actor);
const moviesCount = computed(() => header.value?.movies_count ?? movies.value.length);
const isMale = computed(() => (header.value?.gender ?? 0) === 1);
/** TMDB 补充档案：**单独一条请求**，晚到 / 失败都只影响那一段（后端不塞进详情）。 */
const tmdb = computed(() => tmdbInfo.value);
/** 资料档案（avbase / JAVDB / 索引表）：同样是补充，晚到只影响那一段。 */
const profile = computed(() => profileInfo.value);

/**
 * 别名：索引表 + avbase + JAVDB 三边并集（不含主名）。
 *
 * 列表带回的那份只有索引表里的写法 —— 很多演员（尤其是新人、欧美片）根本没进
 * 那张表，而 avbase / JAVDB 那边却有别名。两边并起来才够用。
 */
const aliases = computed(() => {
  const out: string[] = [];
  const seen = new Set<string>();
  const main = (header.value?.name ?? "").toLowerCase();
  for (const list of [header.value?.aliases ?? [], profile.value?.aliases ?? []]) {
    for (const a of list) {
      const name = a.trim();
      if (!name) continue;
      const key = name.toLowerCase();
      if (key === main || seen.has(key)) continue;
      seen.add(key);
      out.push(name);
    }
  }
  return out;
});

/** TMDB 那边另一个名字（统一名对不上时，看一眼就知道是不是同一个人）。 */
const tmdbOtherNames = computed(() => {
  const t = tmdb.value;
  if (!t) return [] as string[];
  const main = (header.value?.name ?? "").toLowerCase();
  const seen = new Set<string>([main, (t.name ?? "").toLowerCase()]);
  const out: string[] = [];
  for (const a of t.also_known_as ?? []) {
    const name = a.trim();
    if (!name) continue;
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(name);
  }
  return out;
});

/** 「生日（享年）」：有卒日才写享年，别给在世的人算一个数出来。 */
const lifeText = computed(() => {
  // 资料层（avbase）的生日最全，优先；TMDB 那份常常是空的。
  if (profile.value?.birthday) {
    const t = tmdb.value;
    if (t?.deathday) return `${profile.value.birthday} — ${t.deathday}`;
    return profile.value.birthday;
  }
  const t = tmdb.value;
  if (!t?.birthday) return "";
  if (!t.deathday) return t.birthday;
  return `${t.birthday} — ${t.deathday}`;
});

/** 出生地：资料层（avbase 的「出身地」）优先，其次 TMDB。 */
const placeText = computed(() => {
  return profile.value?.birthplace?.trim() || tmdb.value?.place_of_birth?.trim() || "";
});

/** 三围那一行：只拼有的那几项（`158cm · B101 / W55 / H84 · J罩杯`）。 */
const bodyText = computed(() => {
  const p = profile.value;
  if (!p) return "";
  const parts: string[] = [];
  if (p.height && p.height > 0) parts.push(`${p.height}cm`);
  const three = [] as string[];
  if (p.bust && p.bust > 0) three.push(`B${p.bust}`);
  if (p.waist && p.waist > 0) three.push(`W${p.waist}`);
  if (p.hip && p.hip > 0) three.push(`H${p.hip}`);
  if (three.length) parts.push(three.join(" / "));
  if (p.cup) parts.push(`${p.cup}罩杯`);
  return parts.join(" · ");
});

/** 资料层额外那几项（血型 / 爱好 / 站点标签）。 */
const extraFacts = computed(() => {
  const p = profile.value;
  if (!p) return [] as string[];
  const out: string[] = [];
  if (p.blood_type) out.push(`血型 ${p.blood_type}`);
  if (p.hobby) out.push(`爱好 ${p.hobby}`);
  if (p.note) out.push(p.note);
  return out;
});

/**
 * 维基导言（**介绍**）。只有维基有这一段 —— avbase / JAVDB / TMDB 对番号演员
 * 基本都不给介绍（用户 2026-10-22 问的「介绍的吗？」）。
 *
 * 有中文条目时后端给的就是中文，否则是日文原文：那时标一句「日文原文」，
 * 免得用户以为是没翻译好的中文。
 */
const intro = computed(() => profile.value?.intro?.trim() ?? "");
const introIsJa = computed(() => (profile.value?.intro_lang ?? "") === "ja");

/**
 * 人像地址：**墙上那张 > 资料层的高清 > 资料层的小图 > TMDB**。
 *
 * # 为什么墙上那张排第一（用户 2026-10-29 定的口径：「墙为准，详情放大同一张」）
 *
 * 原来第一档是 `avatar_hd_url`（维基条目的首图，960 宽的活动照），而墙上的卡片用的是
 * 侧车 / 索引表那张（100~300 的正面照）。真机审计 98 位演员里有 26 位因此**两张
 * 不同的照片**——用户的原话是「演员页的头像和详情页有时不一样」。
 *
 * 删掉「高清优先」之后：墙有图的人两面必然同一张；墙空的那些人仍然顺着高清 / 小图
 * 往下兜（那正是「演员页没头像、详情页又有」要治的另一半）。
 *
 * 最前面还是**用户自己上传的那张**：那是他亲手选的，任何自动来源都不该盖过它。
 */
const photoChain = computed(() => {
  const out: string[] = [];
  const push = (u: string) => {
    if (u && !out.includes(u)) out.push(u);
  };
  // 自传那张已经是 `/api/...`（javImageURL 会原样放行）。
  push(javImageURL(customAvatar.value));
  push(javImageURL(header.value?.avatar_url));
  push(javImageURL(profile.value?.avatar_hd_url));
  push(javImageURL(profile.value?.avatar_url));
  push(javActorPhotoURL(tmdb.value?.profile_path, "w185"));
  return out;
});

/**
 * 头像加载失败时**退到下一档**，而不是直接落占位图。
 *
 * 资料层给的地址是「探针能取到」的那一张（后端逐个真取过），但探针与浏览器
 * 之间还隔着一次代理 / 一次缓存失效；高清那张又只有维基一个源 —— 失败时退到
 * 小图、TMDB、侧车，都不行才让 `MediaImage` 落占位图。不要留一张破图。
 */
const photoTried = ref(0);
watch(photoChain, () => {
  photoTried.value = 0;
});
const effectivePhotoSrc = computed(() => {
  const chain = photoChain.value;
  return chain[Math.min(photoTried.value, Math.max(chain.length - 1, 0))] ?? "";
});
function onPhotoError() {
  if (photoTried.value < photoChain.value.length - 1) photoTried.value++;
}

// ————————————————— 自定义头像（用户自己上传） —————————————————
//
// 用户明确要求：演员名右边一个图片编辑图标，点一下能传自己的头像。
//
// ⚠️ **传的是当时那台设备上的文件，但显示不依赖它**：文件只被读一次，
// 后端解码 → 缩到 ≤480×720 → 重编码 JPEG 存进服务端的 `data/jav_actor_avatar/`，
// 之后一律用后端返回的 `/api/...` 地址。所以换手机、换电脑、清空相册都还在。
// 这里**绝不能**拿 `URL.createObjectURL()` 的 blob 地址当结果 —— 那只在本机本次
// 会话有效，换设备就是裂图，正好违背用户那条要求。
const avatarBusy = ref(false);

/** 上传入口弹出的小菜单（手机端给「相册」「文件浏览」两个入口，**不给拍照**）。 */
const avatarMenuOpen = ref(false);
/** 按钮那一簇的 DOM：点它自己不该把菜单收起来。 */
const avatarActions = ref<HTMLElement | null>(null);
/** 两个独立的 input：同一个 input 改 `accept` 再 `click()` 在移动端不一定重新选。 */
const albumInput = ref<HTMLInputElement | null>(null);
const fileInput = ref<HTMLInputElement | null>(null);

/**
 * 文件对话框里能选的格式 = 后端 `normalizeActorAvatar` 真的解得开的那些
 * （jpeg / png / gif / webp）。**HEIC 故意不在里面** —— 后端解不了，列进去
 * 只会让用户选中之后再吃一个「不是能识别的图片」。
 *
 * 同时写 MIME 和扩展名：Windows 的对话框按扩展名过滤，部分浏览器只认 MIME；
 * 两边都写才能「只显示支持的格式」。`.jpeg` 与 `image/jpeg` 是同一个格式的两种写法，
 * 都要列（用户从相机导出的文件常是 `.jpeg`）。
 */
const AVATAR_ACCEPT =
  "image/jpeg,image/png,image/gif,image/webp,.jpg,.jpeg,.png,.gif,.webp";
const AVATAR_MIMES = new Set(["image/jpeg", "image/png", "image/gif", "image/webp"]);
const AVATAR_EXTS = new Set(["jpg", "jpeg", "png", "gif", "webp"]);

/**
 * 是不是受支持的图片。
 *
 * 不能只看 `file.type`：空 `type` 很常见（某些文件管理器、从压缩包里拖出来的文件），
 * 那时按扩展名判。判据与上面的 `accept` 同源 —— 对话框能选的，这里一定放行。
 */
function isSupportedAvatarFile(file: File): boolean {
  const mime = (file.type || "").toLowerCase();
  if (AVATAR_MIMES.has(mime)) return true;
  const ext = (file.name.split(".").pop() || "").toLowerCase();
  return AVATAR_EXTS.has(ext);
}

const isMobileViewport = ref(false);
function syncViewport() {
  isMobileViewport.value = window.matchMedia("(max-width: 720px)").matches;
}

/** 点相机图标：桌面端直接开文件对话框，移动端先弹两个入口（再点一次收起）。 */
function openAvatarPicker() {
  if (!isMobileViewport.value) {
    avatarMenuOpen.value = false;
    fileInput.value?.click();
    return;
  }
  avatarMenuOpen.value = !avatarMenuOpen.value;
}
function pickFromAlbum() {
  avatarMenuOpen.value = false;
  albumInput.value?.click();
}
function pickFromFiles() {
  avatarMenuOpen.value = false;
  fileInput.value?.click();
}

function closeAvatarMenu() {
  avatarMenuOpen.value = false;
}
// 抽屉关掉时顺手收起菜单：不收回的话，下次打开会残留一个浮在名字下面的小面板。
watch(
  () => props.open,
  (open) => {
    if (!open) closeAvatarMenu();
  },
);
/**
 * 点别处收起菜单。
 *
 * 用 capture 阶段：`openAvatarPicker` 是按钮自己的 click 处理器，它在冒泡到
 * document 之前就把菜单**打开**了，只有 capture 才收得到「用户点的是别处」。
 * 点在按钮那一簇里则放行 —— 那里自己 `@click.stop` 管开关。
 */
function onDocClick(e: MouseEvent) {
  if (!avatarMenuOpen.value) return;
  const root = avatarActions.value;
  if (root && e.target instanceof Node && root.contains(e.target)) return;
  avatarMenuOpen.value = false;
}

/**
 * 手机端那两行入口。**没有「拍照」** —— 用户明确说「拍照就不要了」。
 * 桌面端不会弹这个菜单（直接开文件对话框），两条在桌面上本来就是同一个对话框。
 */
const avatarMenuItems: DropdownMenuItem[] = [
  { key: "album", type: "action", label: "从相册选择", icon: "image" },
  { key: "files", type: "action", label: "从文件选择", icon: "folder" },
];

function onAvatarMenuSelect(key: string) {
  if (key === "album") pickFromAlbum();
  else pickFromFiles();
}

onMounted(() => {
  syncViewport();
  window.addEventListener("resize", syncViewport);
  // 点别处收起菜单。capture 阶段才收得到（菜单自己 stop 了 click）。
  document.addEventListener("click", onDocClick, true);
});
onBeforeUnmount(() => {
  window.removeEventListener("resize", syncViewport);
  document.removeEventListener("click", onDocClick, true);
});

/**
 * 服务端地址判据：上传结果必须是后端给的（见上面那段注释）。
 *
 * 只认相对形式 `/api/...` —— 后端就是这么返回的；带 origin 的绝对形式会被
 * `javImageURL` 当成外站再包一层代理，反而不对。
 */
function isServerAvatarURL(url: string): boolean {
  return url.startsWith("/api/");
}

async function onAvatarFileSelected(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  // 清掉 value，否则同一个文件选第二次不触发 change（用户常见的「点了没反应」）。
  input.value = "";
  const actor = header.value;
  if (!file || !actor?.key) return;
  if (!isSupportedAvatarFile(file)) {
    toast.error("请选择 JPG / PNG / GIF / WebP 图片（HEIC 请先在相册里导出成 JPG）");
    return;
  }
  // 前端先拦一道，真正的闸在后端（解不开的图后端也会拒）。
  if (file.size > 8 * 1024 * 1024) {
    toast.error("图片不能超过 8 MB");
    return;
  }
  avatarBusy.value = true;
  try {
    const res = await uploadJavActorAvatar(actor.key, file);
    const url = (res?.avatar_url ?? "").trim();
    if (!isServerAvatarURL(url)) {
      // 拿到一个本地地址 / 空地址都算失败：那种地址换设备就没了。
      toast.error("上传结果异常，请重试");
      return;
    }
    customAvatar.value = url;
    // 表头那份也跟上：不然 `photoChain` 第二档还是上一张的地址（上传前后都是同一张卡）。
    if (detail.value) detail.value = { ...detail.value, avatar_url: url, custom_avatar: true };
    toast.success("头像已更新");
    emit("avatarChanged", actor.key, url);
  } catch (e) {
    toast.error(getApiErrorMessage(e, "上传头像失败"));
  } finally {
    avatarBusy.value = false;
  }
}

/** 恢复默认：删掉自传那张，回到侧车 / 资料层。 */
async function resetAvatar() {
  const actor = header.value;
  if (!actor?.key || avatarBusy.value) return;
  avatarBusy.value = true;
  try {
    await deleteJavActorAvatar(actor.key);
    customAvatar.value = "";
    // 表头那份先置空，落回资料层的高清 / 小图那两档（不会变成一片空白）；
    // 再补发一条详情，拿回**侧车 / 索引表给的那张自动图**当第一档。
    if (detail.value) {
      detail.value = { ...detail.value, avatar_url: "", custom_avatar: false };
    }
    void refreshHeaderCard(actor.key);
    toast.success("已恢复默认头像");
    emit("avatarChanged", actor.key, "");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "恢复默认头像失败"));
  } finally {
    avatarBusy.value = false;
  }
}

/** 打开时同步「当前是不是自传那张」——后端在卡片上给了标记，不用另发一条请求。 */
function syncCustomAvatar(card: JavWallActorCard | null | undefined) {
  const url = card?.custom_avatar ? (card.avatar_url ?? "").trim() : "";
  customAvatar.value = isServerAvatarURL(url) ? url : "";
}

/**
 * 只刷新表头那张卡（不重拉片单、不动别的段）。
 *
 * 「恢复默认」之后要用：删掉的只是自传那张，**自动那张**（侧车 / 索引表给的）
 * 只有后端知道 —— 原来那次详情带回来的已经是自传地址了，本地没有它的副本。
 * 补这一条就是把它取回来，避免头像空一档掉到资料层的高清图上。
 */
async function refreshHeaderCard(key: string) {
  const seq = loadSeq;
  try {
    const res = await fetchJavWallActorDetail(key);
    if (seq !== loadSeq || !res.actor) return;
    detail.value = res.actor;
    syncCustomAvatar(res.actor);
  } catch {
    // 取不到就保持置空状态：资料层那两档还在，不会是一片空白。
  }
}

/** 演员的片可能横跨好几面粉号墙 —— 多于一面时才在卡片上标来源。 */
const sourceCount = computed(() => {
  const seen = new Set<string>();
  for (const m of movies.value) seen.add(m.local_dir_id ? `L:${m.local_dir_id}` : `T:${m.strm_task_id ?? 0}`);
  return seen.size;
});

let loadSeq = 0;

watch(
  () => [props.open, props.actor?.key] as const,
  ([open, key]) => {
    if (!open || !key) {
      // 关掉也要推进序号：在途的两条请求回来时一律作废，
      // 否则它们会把上一位演员的简介 / 影片写进下一次打开的这一屏。
      loadSeq++;
      detail.value = null;
      movies.value = [];
      tmdbInfo.value = null;
      profileInfo.value = null;
      customAvatar.value = "";
      loading.value = false;
      error.value = "";
      return;
    }
    void load(String(key));
  },
  { immediate: true },
);

async function load(key: string) {
  const seq = ++loadSeq;
  detail.value = props.actor;
  movies.value = [];
  tmdbInfo.value = null;
  profileInfo.value = null;
  // 换人时先按列表带过来的那份对齐，详情回来后再对齐一次（列表那份可能已经旧了）。
  syncCustomAvatar(props.actor);
  error.value = "";
  loading.value = true;
  // 三条请求**并行发**：两条补充资料慢 / 不通，不该拖着这一页的影片。
  void loadProfile(key, seq);
  void loadTMDB(key, seq);
  try {
    const res = await fetchJavWallActorDetail(key);
    if (seq !== loadSeq) return;
    if (res.actor) detail.value = res.actor;
    syncCustomAvatar(res.actor ?? props.actor);
    movies.value = res.items ?? [];
  } catch (e) {
    if (seq !== loadSeq) return;
    error.value = getApiErrorMessage(e, "加载演员详情失败");
  } finally {
    if (seq === loadSeq) loading.value = false;
  }
}

/**
 * 资料档案（生日 / 出身 / 三围 / 头像 / 别名）。失败 / 没有人都**静默跳过**：
 * 详情页其余内容照常显示 —— 这段是补充，缺了不该让人以为出错了。
 *
 * 它与 TMDB 那条的关系：这一条命中率高得多（番号圈的源），TMDB 用来补简介。
 */
async function loadProfile(key: string, seq: number) {
  try {
    const info = await fetchJavWallActorProfile(key);
    if (seq !== loadSeq) return;
    profileInfo.value = info ?? null;
  } catch {
    if (seq !== loadSeq) return;
    profileInfo.value = null;
  }
}

/**
 * TMDB 补充档案（失败 / 没有人都**静默跳过**：详情页其余内容照常显示）。
 *
 * ⚠️ 不能用 `seq !== loadSeq` 判之外的东西拦住它 —— 换一位演员时旧结果必须丢掉，
 * 否则会把这人的简介贴到下一个人头上。
 */
async function loadTMDB(key: string, seq: number) {
  try {
    const info = await fetchJavWallActorTMDB(key);
    if (seq !== loadSeq) return;
    tmdbInfo.value = info ?? null;
  } catch {
    if (seq !== loadSeq) return;
    tmdbInfo.value = null;
  }
}

/** 卡片的图：海报优先（竖版网格），没有才回落缩略图。 */
function movieImage(movie: JavWallActorMovie): string {
  return movie.poster_url || movie.thumb_url || "";
}

/** 定位到那一面墙：本地目录源优先（与 sourceParams 同一条规矩）。 */
function sourceOf(movie: JavWallActorMovie): WallSourceRef {
  if (movie.local_dir_id) return { local_dir_id: movie.local_dir_id };
  return { strm_task_id: movie.strm_task_id ?? 0 };
}

function openMovie(movie: JavWallActorMovie) {
  emit("openMovie", sourceOf(movie), movie);
}

/**
 * 点别名 → 拿它去搜演员墙。
 *
 * 把**这位演员本人的卡片**一起交出去：别名的来源常常是 avbase / JAVDB
 * （罗马音、旧艺名），墙上的关键词索引里根本没有这个写法 —— 外层拿这串字
 * 去过滤必然空手。带上 key 之后，「点别名」与「搜主名」看到的是同一张卡。
 */
function searchAlias(name: string) {
  if (!props.searchable) return;
  const actor = header.value;
  if (!actor) return;
  emit("search", name, actor);
}
</script>

<template>
  <!-- elevated：演员墙本身是 2500 那一档的整页浮层，默认档（120）会被它整个盖住。 -->
  <AdminSettingsDrawer
    elevated
    :raised="raised"
    :open="open && !suspended"
    :title="header?.name || '演员'"
    hide-foot
    @close="emit('close')"
    @cancel="emit('close')"
  >
    <div v-if="!header" class="jav-empty">加载中…</div>

    <template v-else>
      <div class="jav-wall-actor-detail__head">
        <div class="jav-wall-actor-detail__avatar">
          <MediaImage
            :src="effectivePhotoSrc"
            :alt="header.name"
            variant="person"
            :lazy="false"
            @error="onPhotoError"
          />
        </div>
        <div class="jav-wall-actor-detail__info">
          <h1 class="jav-wall-actor-detail__name">
            <span class="jav-wall-actor-detail__name-text">{{ header.name || "（未命名）" }}</span>
            <span v-if="isMale" class="jav-wall-actor-detail__gender">男</span>
            <!-- 换头像：用户要求（2026-10-29）在演员名右边放一个图片编辑图标。 -->
            <span ref="avatarActions" class="jav-wall-actor-detail__avatar-actions" @click.stop>
              <button
                type="button"
                class="jav-wall-actor-detail__avatar-btn"
                :disabled="avatarBusy"
                :title="avatarBusy ? '上传中…' : '上传自定义头像'"
                aria-label="上传自定义头像"
                @click="openAvatarPicker"
              >
                <BusySpinner v-if="avatarBusy" :size="14" />
                <SvgIcon v-else name="camera" :size="15" />
              </button>
              <!-- 恢复默认：只在**确实有**自传头像时出现。 -->
              <button
                v-if="customAvatar"
                type="button"
                class="jav-wall-actor-detail__avatar-btn"
                :disabled="avatarBusy"
                title="恢复默认头像"
                aria-label="恢复默认头像"
                @click="resetAvatar"
              >
                <SvgIcon name="fa-undo-alt" :size="14" />
              </button>
              <!-- 手机端：相册 / 文件浏览两个入口（**不给拍照**，用户明确排除）。 -->
              <AppMenuPanel
                v-if="avatarMenuOpen"
                class="jav-wall-actor-detail__avatar-menu"
                :items="avatarMenuItems"
                @select="onAvatarMenuSelect"
              />
            </span>
          </h1>
          <!-- 两个**独立**的 input（见上面那段注释）：
               相册：accept 列后端真能解的四种格式，**不加 capture** —— 加了会直接唤起相机。
               文件：同一个 accept（不带 capture 时它打开的是文件 App / 文档管理器），
               显式过滤是为了让对话框里**只显示支持的图片**，而不是把 .txt / .pdf 全摆出来。 -->
          <input
            ref="albumInput"
            class="jav-wall-actor-detail__file-input"
            type="file"
            :accept="AVATAR_ACCEPT"
            @change="onAvatarFileSelected"
          />
          <input
            ref="fileInput"
            class="jav-wall-actor-detail__file-input"
            type="file"
            :accept="AVATAR_ACCEPT"
            @change="onAvatarFileSelected"
          />

          <!-- 别名：索引表里这位演员的其它写法（日文原名 / 罗马音 / 旧艺名）。
               点一下拿它去搜 —— 用户往往只记得侧车里那个日文写法。 -->
          <div v-if="aliases.length" class="jav-wall-actor-detail__aliases">
            <span class="jav-wall-actor-detail__alias-label">别名</span>
            <button
              v-for="alias in aliases"
              :key="alias"
              type="button"
              class="jav-wall-actor-detail__alias"
              :class="{ 'jav-wall-actor-detail__alias--static': !searchable }"
              :title="searchable ? `搜「${alias}」` : alias"
              @click="searchAlias(alias)"
            >{{ alias }}</button>
          </div>

          <div class="jav-wall-actor-detail__meta">
            <span>{{ moviesCount }} 部作品</span>
            <span v-if="header.id" class="jav-wall-actor-detail__id">{{ header.id }}</span>
          </div>

          <!-- 资料档案（avbase → JAVDB → 索引表）：生日 / 出身 / 三围 / 额外那几项。
               这是番号圈的源，命中率比 TMDB 高得多 —— 用户要的「生日、出生」就在这。 -->
          <div v-if="profile && (lifeText || placeText || bodyText || extraFacts.length)" class="jav-wall-actor-detail__profile">
            <span v-if="lifeText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-cake-candles" aria-hidden="true" />{{ lifeText }}
            </span>
            <span v-if="placeText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-location-dot" aria-hidden="true" />{{ placeText }}
            </span>
            <span v-if="bodyText" class="jav-wall-actor-detail__fact">
              <i class="fas fa-ruler-vertical" aria-hidden="true" />{{ bodyText }}
            </span>
            <span v-for="fact in extraFacts" :key="fact" class="jav-wall-actor-detail__fact">
              {{ fact }}
            </span>
          </div>

          <!-- TMDB 那边对上的人：名字 / 生日 / 出生地。只在真抓到档案时出现。
               生日与出生地已经在上面那一行由资料层显示过时，这里不再重复。 -->
          <div v-if="tmdb" class="jav-wall-actor-detail__tmdb-meta">
            <span v-if="tmdb.name && tmdb.name !== header.name" class="jav-wall-actor-detail__tmdb-name">
              TMDB：{{ tmdb.name }}
            </span>
            <span v-if="!profile?.birthday && lifeText">{{ lifeText }}</span>
            <span v-if="!placeText && tmdb.place_of_birth">{{ tmdb.place_of_birth }}</span>
            <span v-if="tmdbOtherNames.length" class="jav-wall-actor-detail__tmdb-alias">
              {{ tmdbOtherNames.slice(0, 4).join(" / ") }}
            </span>
          </div>
          <div class="jav-wall-actor-detail__hint">
            这些是番号墙上的影片，点开看详情、播放。
          </div>
        </div>
      </div>

      <!-- 介绍（维基导言）：资料层里唯一有「介绍」的源，而且只有 200~400 字，
           不用收起来。日文原文时标一句，免得当成没翻译好的中文。 -->
      <div v-if="intro" class="jav-wall-actor-detail__intro">
        <div class="jav-wall-actor-detail__intro-head">
          <span class="jav-wall-actor-detail__intro-label">介绍</span>
          <span v-if="introIsJa" class="jav-wall-actor-detail__intro-lang">日文原文</span>
          <a
            v-if="profile?.wiki_url"
            class="jav-wall-actor-detail__intro-link"
            :href="profile.wiki_url"
            target="_blank"
            rel="noopener noreferrer"
          >维基百科</a>
        </div>
        <p>{{ intro }}</p>
      </div>

      <!-- TMDB 简介：**补充资料**，演过哪些片不看它（那一栏只有墙上的影片）。
           已经有维基那段时不再展开另一段（同一个人写两遍，读者只会困惑）。 -->
      <details v-if="!intro && tmdb?.biography" class="jav-wall-actor-detail__bio">
        <summary>简介</summary>
        <p>{{ tmdb.biography }}</p>
      </details>

      <h2 class="jav-wall-actor-detail__section">参演影片</h2>

      <div v-if="loading" class="jav-empty">加载中…</div>
      <div v-else-if="error && !movies.length" class="jav-empty">
        <div class="jav-empty__icon">⚠️</div>
        <div>{{ error }}</div>
      </div>
      <div v-else-if="!movies.length" class="jav-empty">
        墙上的影片里还没有这位演员的片。
      </div>
      <!-- 卡片尺寸与海报墙（`--poster` 视图）逐条对齐：列宽 140 / 间距 14 / 封面 2:3。 -->
      <div v-else class="jav-wall-actor-detail__grid">
        <article
          v-for="movie in movies"
          :key="`${movie.local_dir_id || movie.strm_task_id}:${movie.rel_dir}:${movie.stem}`"
          class="jav-card jav-wall-actor-detail__card"
          role="button"
          tabindex="0"
          :title="`${movie.number || movie.title}\n${movie.title}`"
          @click="openMovie(movie)"
          @keyup.enter.prevent="openMovie(movie)"
          @keydown.space.prevent="openMovie(movie)"
        >
          <div class="jav-card__cover jav-wall-actor-detail__cover">
            <MediaImage :src="movieImage(movie)" :alt="movie.number || movie.title" :lazy="false" />
            <!-- 跨墙时才标来源：只有一面墙时这句是噪声。 -->
            <span v-if="sourceCount > 1 && movie.source_name" class="jav-wall-actor-detail__source">
              {{ movie.source_name }}
            </span>
          </div>
          <div class="jav-wall-actor-detail__meta-card">
            <div class="jav-wall-actor-detail__number" :title="movie.number">{{ movie.number }}</div>
            <div class="jav-wall-actor-detail__title" :title="movie.title">{{ movie.title }}</div>
            <div v-if="movie.release_date" class="jav-wall-actor-detail__sub">{{ movie.release_date }}</div>
          </div>
        </article>
      </div>
    </template>
  </AdminSettingsDrawer>
</template>

<style scoped>
.jav-wall-actor-detail__head {
  display: flex;
  align-items: center;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border-soft);
}

/* 头像：与墙面卡片同一个 2:3 竖版，给到 120px 宽 —— 详情里要看清脸。 */
.jav-wall-actor-detail__avatar {
  flex: 0 0 auto;
  width: 120px;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
}
.jav-wall-actor-detail__avatar img,
.jav-wall-actor-detail__avatar .media-ph {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.jav-wall-actor-detail__info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.jav-wall-actor-detail__name {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 700;
  color: var(--text);
  word-break: break-word;
}

/* 名字单独一格：换头像的两个按钮要贴着名字右边，但长名字换行时不能被按钮挤断。 */
.jav-wall-actor-detail__name-text {
  min-width: 0;
  word-break: break-word;
}

/* 换头像：一颗小圆按钮（相机）+ 一颗「恢复默认」（只在有自传头像时出现）。 */
.jav-wall-actor-detail__avatar-actions {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: 0 0 auto;
}

.jav-wall-actor-detail__avatar-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid var(--border-soft);
  border-radius: 50%;
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease;
}

.jav-wall-actor-detail__avatar-btn:hover:not(:disabled) {
  color: var(--accent, #3b82f6);
  border-color: var(--accent, #3b82f6);
}

.jav-wall-actor-detail__avatar-btn:disabled {
  opacity: 0.55;
  cursor: progress;
}

/* 手机端那两行入口：贴着按钮下方展开。 */
.jav-wall-actor-detail__avatar-menu {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 20;
}

/* 隐藏的文件输入：不能用 display:none（部分移动端浏览器 click() 会失效）。 */
.jav-wall-actor-detail__file-input {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
  border: 0;
}

.jav-wall-actor-detail__gender {
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: rgba(59, 130, 246, 0.92);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}

.jav-wall-actor-detail__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  color: var(--text-muted);
  font-size: 12.5px;
}

/* 别名那一行：一颗颗小胶囊，点了拿它去搜。 */
.jav-wall-actor-detail__aliases {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.jav-wall-actor-detail__alias-label {
  color: var(--text-muted);
  font-size: 11.5px;
}
.jav-wall-actor-detail__alias {
  padding: 1px 7px;
  border: 1px solid var(--border);
  border-radius: var(--radius-pill);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11.5px;
  cursor: pointer;
}
.jav-wall-actor-detail__alias:hover {
  border-color: var(--brand);
  color: var(--brand);
}
/* 不可搜时（从海报墙的影片详情打开）：保持胶囊外观，但不要「点了会有事」的暗示。 */
.jav-wall-actor-detail__alias--static {
  cursor: default;
}
.jav-wall-actor-detail__alias--static:hover {
  border-color: var(--border);
  color: var(--text-muted);
}

/* 资料档案那一行：生日 / 出身 / 三围。比 TMDB 那行**更实**（它是主资料），
   所以用正常文字色，图标压暗一档。 */
.jav-wall-actor-detail__profile {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 14px;
  color: var(--text);
  font-size: 12.5px;
}
.jav-wall-actor-detail__fact {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.jav-wall-actor-detail__fact i {
  color: var(--text-muted);
  font-size: 11.5px;
}

/* TMDB 那几项：比别名更淡一档 —— 它们是补充资料，不该和主信息抢。 */
.jav-wall-actor-detail__tmdb-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  color: var(--text-muted);
  font-size: 12px;
  opacity: 0.9;
}
.jav-wall-actor-detail__tmdb-name {
  font-weight: 600;
}
.jav-wall-actor-detail__tmdb-alias {
  opacity: 0.8;
}

/* 简介默认收起（有些人的 TMDB 简介有几千字），点一下展开。 */
.jav-wall-actor-detail__bio {
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12.5px;
  line-height: 1.7;
}
.jav-wall-actor-detail__bio summary {
  cursor: pointer;
  color: var(--text);
  font-weight: 600;
  font-size: 12.5px;
}
.jav-wall-actor-detail__bio p {
  margin: 8px 0 0;
  white-space: pre-wrap;
}

/* 介绍（维基导言）：200~400 字，直接展开，不用点。 */
.jav-wall-actor-detail__intro {
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 12.5px;
  line-height: 1.7;
}
.jav-wall-actor-detail__intro-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.jav-wall-actor-detail__intro-label {
  color: var(--text);
  font-weight: 600;
}
/* 「日文原文」只是提示，别做成按钮。 */
.jav-wall-actor-detail__intro-lang {
  font-size: 11px;
  opacity: 0.7;
}
.jav-wall-actor-detail__intro-link {
  margin-left: auto;
  color: var(--brand);
  font-size: 11.5px;
  text-decoration: none;
}
.jav-wall-actor-detail__intro-link:hover {
  text-decoration: underline;
}
.jav-wall-actor-detail__intro p {
  margin: 8px 0 0;
  white-space: pre-wrap;
}

.jav-wall-actor-detail__id {
  font-family: var(--font-mono, monospace);
  opacity: 0.75;
}

.jav-wall-actor-detail__hint {
  color: var(--text-muted);
  font-size: 12px;
}

.jav-wall-actor-detail__section {
  margin: 18px 0 12px;
  font-size: 14px;
  font-weight: 700;
  color: var(--text);
}

/* ⚠️ 与 StrmJavWall 的 `.jav-wall__grid--poster` / 演员墙的网格**逐条对齐**。 */
.jav-wall-actor-detail__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 14px;
}

.jav-wall-actor-detail__card {
  overflow: hidden;
}

.jav-wall-actor-detail__cover {
  aspect-ratio: 2 / 3;
}

.jav-wall-actor-detail__source {
  position: absolute;
  top: 6px;
  left: 6px;
  z-index: 2;
  max-width: calc(100% - 12px);
  padding: 1px 7px;
  border-radius: 5px;
  background: rgba(15, 23, 42, 0.62);
  color: #fff;
  font-size: 10.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jav-wall-actor-detail__meta-card {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px 10px;
}
.jav-wall-actor-detail__number {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jav-wall-actor-detail__title {
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jav-wall-actor-detail__sub {
  font-size: 11px;
  color: var(--text-muted);
}
</style>
