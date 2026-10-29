import { http } from "@/api/client";
// 可播文件是**两面墙共用**的形状（同一个后端实现、同一套字幕结构），
// 所以类型定义留在 strmScrape.ts 那一份，这里只 import —— 两边各写一份迟早会漂。
import { sourceParams, type WallDetail, type WallSourceRef } from "@/api/strmScrape";

// 番号影片的海报墙（辅助工具 → 海报墙，选中「媒体类型 = 番号影片」的任务时用）。
//
// 与 TMDB 那套（strmScrape.ts）共用同一个页面，但数据源完全不同：这边是**扫本地
// 媒体库目录**（作品载体主干 + 四个产物的有无），不碰 TMDB、也不落库。
//
// **载体有两种**：本应用生成的 `.strm`，以及「本地目录」源里用户别的工具产出的
// 真实视频（`.mp4` / `.mkv` …）。判据在后端 `strm.IsMediaFileName`，前端不必区分 ——
// 卡片、详情、播放、删除都不关心那到底是哪一种（例外只有播放地址：见 PlayableFile.path）。

/** 一级目录（用户口语里的「类型」）。name 为空串表示根目录下散落的那些。 */
export interface JavWallCategory {
  name: string;
  count: number;
}

/** 墙上一张卡 = 一个载体主干（`.strm` 或真实视频）的那一套本地文件。 */
export interface JavWallItem {
  id: string;
  rel_dir: string;
  category: string;
  /** 磁盘上的原样主干（含大小写与尾空格）——回传时必须原样，服务端靠它派生文件名。 */
  stem: string;
  title: string;
  number: string;
  /** 平铺布局（该目录有多个作品载体），决定四个产物的文件名。 */
  flat: boolean;
  has_thumb: boolean;
  has_poster: boolean;
  has_nfo: boolean;
  has_fanart: boolean;
  has_sidecar: boolean;
  thumb_url?: string;
  poster_url?: string;
  /** 图片 mtime，已拼进 url 做缓存击穿；这里只作展示与比对。 */
  thumb_rev?: string;
  poster_rev?: string;
  release_date?: string;
  added_at?: string;
  /**
   * 这一部是 **LTX 修复版**（nfo 里有 `<genre>LTX破解</genre>`）。
   *
   * 卡片在番号旁挂一颗小胶囊。只从 nfo 读 —— 侧车里的 `resource.ltx` 是同一份
   * 事实的另一个落点，nfo 与侧车由同一次推送写出，正常是同步的。
   */
  ltx?: boolean;
}

export interface JavWallStats {
  total: number;
  has_thumb: number;
  has_poster: number;
  has_nfo: number;
  has_sidecar: number;
}

export interface JavWallListResult {
  items: JavWallItem[];
  total: number;
  offset: number;
  limit: number;
  has_more: boolean;
  categories: JavWallCategory[];
  /** 被藏起来的一级目录（默认是「未匹配」）——界面要说一句，否则用户以为片子丢了。 */
  hidden_dirs: string[];
  stats: JavWallStats;
  /** 任务扫描方式是「全量」时为真：每次扫描都会用侧车重建 nfo/海报，手工编辑会被覆盖。 */
  full_sync_wipe: boolean;
}

export interface JavWallListQuery {
  category?: string;
  keyword?: string;
  sort?: string;
  offset?: number;
  limit?: number;
}

/** 编辑器里可改的字段（与后端 emby.MovieMeta 对应）。 */
export interface JavMovieMeta {
  number: string;
  number_letter: string;
  title: string;
  origin_title: string;
  summary: string;
  /**
   * 演员名列表（编辑器里是一个字符串列表控件，只改名字）。
   *
   * 后端 `emby.MovieMeta.Actors` 现在是 `[]ActorEntry`（带 gender），但编辑器
   * **只读名字** —— 这份类型是「表单形状」，不是后端的原始形状。
   */
  actors: string[];
  director: string;
  /** **只含真标签**（4K / 番号字母 / 演员 / 破解 / 系列:xxx 这些合成项已剥掉）。 */
  tags: string[];
  /**
   * `<set>`（Emby/Kodi 的「所属合集」）的成员。
   *
   * 扫描生成时由后端按「一位女演员一个」算好（男优不写、上限 5 个、没有女演员就空）。
   * 读回来的是 nfo 里原样那份 —— 保存时后端**原样写回、不重算**，所以这里改了什么
   * 就存什么。表单里用同一个字符串列表控件编辑。
   */
  sets: string[];
  series: string;
  maker: string;
  publisher: string;
  label: string;
  release_date: string;
  duration: number;
  score: number;
  score_max: number;
  votes: number;
  rating_name: string;
  javdb_url: string;
  cover_url: string;
  trailer_url: string;
  added_at: string;
  four_k: boolean;
  uncensored: boolean;
  has_subtitle: boolean;
  /**
   * 这一部是 **LTX 修复版**（nfo 的 `<genre>LTX破解</genre>`）。
   *
   * 编辑器不在表单上暴露它，但**必须让它穿过保存**：这份对象会被 `Object.assign`
   * 灌进表单、再用 `{...form}` 原样提交回去，后端重建 nfo 时按它决定写不写那条 genre。
   * 类型里漏了它，TS 虽然不报错（运行时照样带着），但任何人想在这里加逻辑时
   * 都会以为「没有这个字段」。
   */
  ltx: boolean;
  lock_data: boolean;
  custom_rating: string;
  mpaa: string;
  country_code: string;
}

/** 原生像素的裁剪窗口（左上原点）。 */
export interface JavCropRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface JavWallPosterState {
  has_thumb: boolean;
  has_poster: boolean;
  thumb_width: number;
  thumb_height: number;
  rect: JavCropRect;
  /** matched = 从现有 poster 反推出来的；default = 生成器的默认窗口；none = 没图 */
  rect_source: "matched" | "default" | "none" | string;
}

/** 编辑页「添加水印」那一块需要的全部数据（后端按侧车算好预置）。 */
export interface JavWallWatermarkState {
  /** 按影片属性自动预置的勾选（编辑页打开时用它初始化，用户可改）。 */
  preset: string[];
  /** 大小 / 边距，百分数（18 = 水印宽占海报宽的 18%）。 */
  scale: number;
  margin: number;
  /**
   * 影片属性：true = 有码。
   *
   * **只用来显示那行灰字，不参与任何勾选**（2026-10-04 起）：无码片原来
   * 会自动挂「无码流出」水印，用户要求去掉，于是属性与「贴不贴」拆开了。
   */
  censored: boolean;
  /** 认得的水印 id，前端据此渲染选项。 */
  available: string[];
}

export interface JavWallItemDetail {
  item: JavWallItem;
  meta: JavMovieMeta;
  names: { nfo: string; poster: string; thumb: string; fanart: string; extra_dir?: string };
  poster: JavWallPosterState;
  notice?: string;
  watermark: JavWallWatermarkState;
}

function listParams(ref: number | WallSourceRef, q: JavWallListQuery): Record<string, string | number> {
  const out: Record<string, string | number> = { ...sourceParams(ref) };
  // category 明确允许空串（= 全部），所以判 undefined 而不是判 falsy。
  if (q.category !== undefined && q.category !== "") out.category = q.category;
  if (q.keyword) out.keyword = q.keyword;
  if (q.sort) out.sort = q.sort;
  if (q.offset) out.offset = q.offset;
  if (q.limit) out.limit = q.limit;
  return out;
}

export function fetchJavWallItems(ref: number | WallSourceRef, q: JavWallListQuery = {}) {
  return http.get<JavWallListResult>("/admin/strm-scrape/jav-wall/items", listParams(ref, q));
}

/**
 * 「刷新元数据」：作废服务端快照、重读磁盘、按同一套条件重列。
 *
 * **它不是重新生成**（那是卡片上的「重刮」）——只重读本地文件，不联网、不重建
 * nfo/图片，作用是把刚保存的东西显示出来（图片 URL 上的 rev 也跟着换）。
 */
export function refreshJavWall(ref: number | WallSourceRef, q: JavWallListQuery = {}) {
  return http.post<JavWallListResult>("/admin/strm-scrape/jav-wall/refresh", {}, listParams(ref, q));
}

export function fetchJavWallItem(ref: number | WallSourceRef, relDir: string, stem: string) {
  return http.get<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
  });
}

export function saveJavWallMeta(ref: number | WallSourceRef, relDir: string, stem: string, meta: JavMovieMeta) {
  return http.put<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/meta", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
    meta,
  });
}

/**
 * 保存海报裁剪。`watermarks` 是页面上勾的水印 id（空 = 不贴）。
 *
 * **这条路不看总开关** —— 用户当场勾了、也当场看得见，那就是他的意思；
 * 总开关管的是重刮 / 扫描那条自动路。
 */
export function saveJavWallPoster(
  ref: number | WallSourceRef,
  relDir: string,
  stem: string,
  rect: JavCropRect,
  watermarks: string[] = [],
) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/poster", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
    rect,
    watermarks,
  });
}

/** 「重刮」：用**本地那份 json** 重建这一部（nfo 重写、封面/海报重下、剧照补缺）。 */
export function rebuildJavWallItem(ref: number | WallSourceRef, relDir: string, stem: string) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/rebuild", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
  });
}

export function refreshJavWallItem(ref: number | WallSourceRef, relDir: string, stem: string) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/refresh", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
  });
}

/** 勾选界面的一行：一个一级目录 + 它的部数 + 当前是否被隐藏。 */
export interface JavWallHiddenDirOption {
  name: string;
  /** 该目录下的卡片数。**隐藏档也有数字** —— 要让人知道藏掉了多少。 */
  count: number;
  hidden: boolean;
}

export interface JavWallHiddenDirList {
  /** 候选清单（磁盘上的一级目录 ∪ 分类规则的目标目录 ∪ 已存名单）。 */
  dirs: JavWallHiddenDirOption[];
  /** 当前生效的名单（勾上的那批）。 */
  hidden: string[];
  /** 「还没勾过」时默认隐藏的那一个名字（分类规则里那条兜底规则的目标目录）。 */
  fallback_name: string;
}

/**
 * 列「整档隐藏的一级目录」候选。
 *
 * `taskId` 传 0（或省略）就不扫盘，只按分类规则的目标目录列 —— 设置页那一行走这条；
 * 墙页头那个按钮传任务 id，能拿到磁盘上真实存在的目录与各自的部数。
 */
export function fetchJavWallHiddenDirs(ref: number | WallSourceRef = 0) {
  const params: Record<string, string | number> = sourceParams(ref);
  return http.get<JavWallHiddenDirList>("/admin/strm-scrape/jav-wall/hidden-dirs", params);
}

/**
 * 保存隐藏名单（**勾上 = 隐藏**）。
 *
 * 名单是**全局**一份（不按任务存），`taskId` 只决定保存后回吐的候选清单按哪个任务扫。
 */
export function saveJavWallHiddenDirs(ref: number | WallSourceRef, dirs: string[]) {
  return http.put<JavWallHiddenDirList>("/admin/strm-scrape/jav-wall/hidden-dirs", {
    ...sourceParams(ref),
    dirs,
  });
}

export const JAV_WALL_SORTS = [
  { value: "added_desc", label: "添加时间（新→旧）" },
  { value: "added_asc", label: "添加时间（旧→新）" },
  { value: "release_desc", label: "发行日期（新→旧）" },
  { value: "number_asc", label: "番号升序" },
  { value: "number_desc", label: "番号降序" },
];

/** 番号影片墙的详情（详情抽屉用）。 */
export function fetchJavWallItemDetail(input: WallSourceRef & {
  rel_dir: string;
  stem: string;
}) {
  return http.get<WallDetail>("/admin/strm-scrape/jav-wall/item/detail", {
    ...sourceParams(input),
    rel_dir: input.rel_dir,
    stem: input.stem,
  });
}

/** 在线刮削的结果（打上游补齐缺的元数据）。 */
export interface JavWallOnlineScrapeResult {
  total: number;
  scraped: number;
  /** 已经齐了 / 取不到番号（**不含**上游没有的那一批）。 */
  skipped: number;
  /**
   * 「上游根本没有这一部」的部数：JAVDB 与整条补缺链都没给。
   *
   * 与 `skipped` / `scraped` 是**两个维度**：这一档说的是上游的事实，
   * 那些片就算本地那份 json/nfo 被顺手修过（也算 scraped），它仍然是上游没有。
   * 界面用它解释「为什么这批怎么刮都刮不出东西」—— 那是源的问题，不是漏刮。
   */
  not_found: number;
  failed: number;
  /** 补出来的图片 / 字幕文件数（封面、海报、剧照、字幕）。 */
  images: number;
  /**
   * 「提交的那几部里真的有几步在磁盘上找到了」。
   *
   * 整根刮削时等于 `total`。⚠️ **不要用本地的卡片列表自己算**：手上只有当前分类
   * 这一页，跨分类勾选时会把别的 tab 上那几部误判成「已经不在墙上了」。
   */
  matched: number;
  /**
   * 整分类那一轮里「这一档一共几部」（`0` / 缺省 = 没走分类这条路）。
   *
   * 收尾文案**报数要用它做分母**：整分类 + 取消掉几部时，`total` 按老口径回显的是
   * 「排除集有几部」，拿它当分母会说成「提交 3 部、匹配 85 部」。
   * ⚠️ 它与 `matched` 不是一回事（`matched` = 提交的那些坐标在磁盘上找到几部）。
   */
  category_matched?: number;
  /** 这一轮按哪个分类抓的（空串是**根目录**那一档的合法值；缺省 = 没走分类路）。 */
  category?: string;
}

/** 在线刮削的进度（前端按钮上的「刮削中…」）。 */
export interface JavWallOnlineScrapeProgress {
  strm_task_id: number;
  /** 这一轮跑的是哪个数据源（见 StrmScrapeProgress.source_key）。 */
  source_key?: string;
  running: boolean;
  done: number;
  total: number;
  message: string;
  /** 跑完之后的汇总（running 变 false 时才有）。 */
  result?: JavWallOnlineScrapeResult;
  /**
   * 这一轮是**点名刮削**（用户勾了几部）还是整根。
   *
   * 注意：判断「跑完的这轮是不是我提交的那批」**不能**只看它 —— 同一数据源上
   * 跑着的可能是另一个页面起的整根任务。前端靠的是**自己**在起任务成功后记下的
   * 那份清单（见墙里那个 pendingBatchItems），那才是「我提交了没有」的硬信号。
   */
  scoped?: boolean;
  /**
   * **只在起任务接口的响应里**为真：已有一条（别人的）任务在跑，这一轮没提交。
   *
   * 那种情况下响应里是**别人那轮**的进度。不区分的话，会拿别人整根任务跑完的结果
   * 去消费自己的选择集 —— 选择被清空了，却一部都没刮。
   */
  not_started?: boolean;
}

/** 「点名刮这一部」：相对数据源根的目录（空 = 根目录）+ 主干。 */
export interface JavWallScrapeItem {
  rel_dir: string;
  stem: string;
}

/**
 * 在线刮削：打上游把目录下**缺的**元数据补齐，写回 json 与 nfo。
 *
 * **后台任务**：接口立刻返回进度，跑完的汇总从进度接口的 `result` 里读
 * （同步跑十几分钟会被反代 502）。
 *
 * `items` **不给**（undefined）= 整根，老行为不变；给了非空数组 = 只刮这几部；
 * 给了空数组服务端会回 400（绝不静默退化成整根 —— 那是十几分钟的白等）。
 *
 * # `category`（整分类，2026-10-19）
 *
 * 给了就是**整个分类**（`""` 是**根目录**那一档，合法值 —— 所以判据必须是
 * `!== undefined` 而不是真值判断，与 listParams 里那条同源）。
 * 与 `items` 同时给时，`items` 的角色**从「要刮的」变成「要排除的」**：
 * 「这一档里除了这几部以外都要」。这是「取消某几部 / 取消整个分类」在服务端唯一
 * 表达得出来的形态 —— 前端手上只有当前那一页，算不出「剩下哪几部」。
 * 带 `category` 时 `items: []` 是**合法**的（= 这一档全要，不排除任何东西）。
 *
 * 与「刷新元数据」的区别：那一条只重读本地磁盘（不联网），这一条会联网。
 * 番号按 **nfo 的 `<num>` → 侧车 json 的 number → 拆主干**（去掉 `-U`/`-C`/`-4K`
 * 那些质量后缀）三条取，**不拿文件名当番号**。
 */
export function onlineScrapeJavWall(
  ref: number | WallSourceRef,
  items?: JavWallScrapeItem[],
  category?: string,
) {
  const body: Record<string, unknown> = { ...sourceParams(ref) };
  // ⚠️ 键**只在真有 items 时才出现**：不带 category 时，一个空数组会被服务端判成 400。
  // 带了 category 时空数组是合法的（见上面那段），所以那一种照发。
  if (items?.length) body.items = items;
  else if (items && category !== undefined) body.items = items;
  // ⚠️ `category` 判 `!== undefined`（不是真值）：空串是「根目录」那一档的**合法值**。
  if (category !== undefined) body.category = category;
  // http.post 的第二参是 body、第三参才是 query。
  return http.post<JavWallOnlineScrapeProgress>("/admin/strm-scrape/jav-wall/online-scrape", body);
}

export function fetchJavWallOnlineScrapeProgress(ref: number | WallSourceRef) {
  return http.get<JavWallOnlineScrapeProgress>("/admin/strm-scrape/jav-wall/online-scrape/progress", sourceParams(ref));
}

// ————————————————————— 删除一部 —————————————————————

/**
 * 「会删掉什么」——删除前的预览。**不写任何东西**，纯算。
 *
 * 为什么要有这一步：删什么由服务端算（哪些本地文件属于这一部、目录会不会一起删、
 * 网盘那边删的是整个目录还是几个文件），前端只负责摆给用户看 + 二次确认。
 * 前端自己拼文案的话，两边的判据迟早会漂 —— 那一漂就是「提示说删 3 个文件、
 * 实际删了 12 个」这种事故。
 */
export interface JavWallDeletePreview {
  /** 本地将被删掉的文件（相对任务根），已排序。 */
  local_files: string[];
  /** 这一部所在的本地目录（相对任务根）；根目录那档为空串。 */
  local_dir: string;
  /** 删完文件之后本地目录会不会一起删掉（目录里没有别的东西了）。 */
  remove_local_dir: boolean;
  /** 平铺布局（该目录还有别的作品载体）——文案要说清「只删这一部」。 */
  flat: boolean;
  /** 网盘上这一步要动的路径；cloud_skipped 非空时它是空串。 */
  cloud_path?: string;
  /** 网盘上删的是**整个目录**（独占布局）；为假时删的是 cloud_files 那几个文件。 */
  cloud_remove_dir: boolean;
  /** 平铺布局下网盘上将被删掉的文件名（独占时为 undefined）。 */
  cloud_files?: string[];
  /** 非空时写明网盘那一步为什么没做（照原样显示给用户）。 */
  cloud_skipped?: string;
}

export interface JavWallDeleteResult {
  /** 真正删掉的本地文件数。 */
  removed_files: number;
  /** 本地目录也删了。 */
  removed_dir: boolean;
  /** 网盘那一份也删了。 */
  cloud_deleted: boolean;
  /** 网盘上删的是整个目录（假 = 只删了几个文件）。 */
  cloud_remove_dir: boolean;
  /** 非空时说明网盘那一步为什么没做（本地已经删干净了）。 */
  cloud_skipped?: string;
}

export function previewJavWallDelete(ref: number | WallSourceRef, relDir: string, stem: string) {
  return http.post<JavWallDeletePreview>("/admin/strm-scrape/jav-wall/item/delete-preview", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
  });
}

/**
 * 真删。**本地文件不可恢复，网盘那一份进回收站。**
 *
 * 调用方必须先走 `previewJavWallDelete` 把清单摆给用户确认 —— 服务端不重复弹窗。
 */
export function deleteJavWallItem(ref: number | WallSourceRef, relDir: string, stem: string) {
  return http.post<JavWallDeleteResult>("/admin/strm-scrape/jav-wall/item/delete", {
    ...sourceParams(ref),
    rel_dir: relDir,
    stem,
  });
}

// ————————————————————— 演员墙 —————————————————————
//
// 数据是**所有番号墙上的影片自己的**演员（侧车 json 优先、nfo 兜底），不是上游的
// 演员库。「N 部」= 墙上有几部片出现了这位演员。

/** 演员墙上的一个卡片。 */
export interface JavWallActorCard {
  /**
   * 详情要原样带回来的标识：有上游 id 就是 id，否则 `name:<小写规范名>`。
   *
   * ⚠️ 与 `name` 不是一回事：`name` 会被索引表换成**中文统一名**，`key` 仍然是
   * 聚合时用的那个键（上游 id，或索引表的规范名）。
   */
  key: string;
  /** 上游演员 id，空串 = 只在 nfo 里见过这个名字。 */
  id?: string;
  /** 显示名：索引表里有这位演员时是**中文统一名**，否则是侧车 / nfo 里的原名。 */
  name: string;
  /**
   * 别名（不含 name 本身）。同样来自索引表；表里没有这位演员时为空。
   *
   * 用户要求（2026-10-21）：详情页把别名一并列出来。
   */
  aliases?: string[];
  /** 1 = 男。0 = 侧车没说。 */
  gender: number;
  /** 上游原始地址（可能经 XOR 混淆），**必须**走 javImageURL 包一层。
   *
   * 例外见 `custom_avatar`：那一档已经是本机同源地址，不能再包。 */
  avatar_url?: string;
  /**
   * 为真表示 `avatar_url` 是**用户自己上传的**那张（服务端 `/api/admin/...` 地址）。
   *
   * 两件事跟着变：取图不再走 `/api/jav/image` 代理（那是给外站用的白名单）；
   * 墙上「补一张小图」那条链也不许覆盖它 —— 用户亲手选的就是最终答案。
   */
  custom_avatar?: boolean;
  /** 墙上有几部影片出现了这位演员。 */
  movies_count: number;
}

/** TMDB 的个人档案（`/person/{id}` 里那几项）。 */
export interface JavWallActorTMDB {
  id: number;
  name: string;
  /** TMDB 图片路径（`/abc.jpg`），**不是完整地址** —— 走 `javActorPhotoURL()` 代理。 */
  profile_path?: string;
  birthday?: string;
  deathday?: string;
  place_of_birth?: string;
  /** Acting / Directing / …（TMDB 原文，不翻译）。 */
  known_for_department?: string;
  biography?: string;
  also_known_as?: string[];
}

/** 演员参演的一部**墙上的**影片（点开走那一面墙的详情抽屉）。 */
export interface JavWallActorMovie {
  /** 数据源：任务 id 或本地目录 id，二者恰有一个非空。 */
  strm_task_id?: number;
  local_dir_id?: string;
  /** 这面墙的名字（任务名 / 本地目录显示名）—— 片子可能来自好几面墙。 */
  source_name: string;
  /** 海报墙上那张卡的 id（只在那一面墙里唯一）。 */
  id: string;
  rel_dir: string;
  stem: string;
  number: string;
  title: string;
  category?: string;
  release_date?: string;
  added_at?: string;
  poster_url?: string;
  thumb_url?: string;
}

export interface JavWallActorListResult {
  items: JavWallActorCard[];
  total: number;
  page: number;
  page_size: number;
  total_page: number;
  /** 聚合了几面粉号墙 / 一共几部墙上的片（空列表时用来解释「为什么是空的」）。 */
  sources: number;
  movies: number;
  /**
   * 这次被**「不显示男演员」**挡掉的位数（只数关键词命中的那批）。
   *
   * 默认口径不列男优，所以空列表可能是「有演员、但都是男优」——
   * 没有这个数，那种情况会显示成「一条演员都没有」，看着像坏了。
   */
  male_count?: number;
  /**
   * 索引还在**后台**建（第一次打开、或者墙刚变过）。
   *
   * ⚠️ 这时 `items` 是空的，但**不代表墙上没有演员** —— 必须显示「正在整理」并
   * 轮询，否则用户看到的是「一位演员都没有」，会以为数据丢了（本项目的老坑）。
   *
   * 后端这么设计是因为这份索引要读整面墙的侧车 / nfo：SMB 上实测两分钟出头，
   * 同步做必然被群晖反代判超时（502）。
   */
  building?: boolean;
}

export interface JavWallActorDetailResult {
  actor: JavWallActorCard;
  items: JavWallActorMovie[];
  total: number;
}

/**
 * 演员墙的一页。
 *
 * ⚠️ **不带数据源**：要的是所有番号墙上的演员，不是当前选中那一面
 * （见后端 listJavWallActors 的说明）。
 *
 * `include_male` 默认**不传**（= 不列男优，用户 2026-10-21 定的默认口径）。
 *
 * `anchor` 是「点名」：这位演员一定出现在结果里，不受关键词与男优口径影响。
 * 从详情页点别名去搜时用它，否则别名（avbase / JAVDB 的罗马音、旧艺名）在墙上
 * 的索引里查不到，用户会看到「一个都没有」。
 */
export function fetchJavWallActors(
  params: { keyword?: string; page?: number; page_size?: number; include_male?: boolean; anchor?: string } = {},
) {
  const query: Record<string, string | number> = {};
  if (params.keyword) query.keyword = params.keyword;
  if (params.page) query.page = params.page;
  if (params.page_size) query.page_size = params.page_size;
  // 只在**要男演员**时才带这个键：后端判 `==1`，缺省就是不要。
  if (params.include_male) query.include_male = 1;
  if (params.anchor) query.anchor = params.anchor;
  return http.get<JavWallActorListResult>("/admin/strm-scrape/jav-wall/actors", query);
}

/**
 * 一位演员 + 参演的墙上影片。
 *
 * `key` 走 query 而不是路径段：名字里可能有空格与斜杠，编码进路径迟早出事。
 */
export function fetchJavWallActorDetail(key: string) {
  return http.get<JavWallActorDetailResult>("/admin/strm-scrape/jav-wall/actor", { key });
}

/**
 * 一位演员的 TMDB 补充档案（简介 / 生日 / 出生地 / 别名）。
 *
 * **与详情分开请求**：详情要「立刻看到片」，而 TMDB 可能很慢 / 不通（后端那侧
 * 也给它单独设了预算）。这条失败 / 回 null 时详情页照常显示，只是少一段补充资料。
 */
export function fetchJavWallActorTMDB(key: string) {
  return http.get<JavWallActorTMDB | null>("/admin/strm-scrape/jav-wall/actor/tmdb", { key });
}

/**
 * 一位演员的**资料档案**（生日 / 出身 / 三围 / 头像 / 别名）。
 *
 * 后端把三个源拼成一份（avbase → JAVDB 搜索 → 索引表的 href），**不打开** JAVDB
 * 的演员主页（那个要登录）。与 TMDB 那条一样是独立的补充请求：它要打外站，
 * 慢和不通都是常态，回来就补上面板，没回来也就少一段资料。
 */
export interface JavWallActorProfile {
  /** 上游给的名字（通常是日文原名）；显示名仍然是卡片上的中文统一名。 */
  name?: string;
  /** 三个源并集后的别名（不含主名）。 */
  aliases?: string[];
  /** 1 = 男（侧车没给时由 avbase 的「男優」标签补）。 */
  gender?: number;
  /** 上游原始地址，**必须**走 javImageURL 包一层（JAVDB CDN 直连不通）。 */
  avatar_url?: string;
  /**
   * 维基条目首图（960 宽），比 `avatar_url` 那张（JAVDB 300×300 / dmm 90×122）
   * 清楚一个量级。**只给详情抽屉用** —— 墙上一屏几十张卡，拉大图是白花流量，
   * 后端因此把它单独放在这个字段里，没有并进 `avatar_url`。
   */
  avatar_hd_url?: string;
  birthday?: string;
  birthplace?: string;
  /** 身高 / 三围（cm），0 = 不知道。 */
  height?: number;
  bust?: number;
  waist?: number;
  hip?: number;
  /** 罩杯（`J` / `E`），空 = 不知道。 */
  cup?: string;
  blood_type?: string;
  hobby?: string;
  /** avbase 的站点标签（`男優` / `舞ワイフ` 这类），原文照给。 */
  note?: string;
  /** JAVDB 演员短 id（`z0GW`）。 */
  javdb_id?: string;
  /**
   * 维基导言那一段（**介绍**）。avbase / JAVDB 都没有这个字段，只有维基有。
   *
   * 中文条目存在时是中文，否则是日文原文（看 `intro_lang` 决定要不要标「日文原文」）。
   */
  intro?: string;
  /** `intro` 的语言：`zh` / `ja`。 */
  intro_lang?: string;
  /** 维基条目页地址（「去维基看全文」）。 */
  wiki_url?: string;
  /** 拼出这份档案的源：`actormap` / `avbase` / `javdb` / `wikipedia`。 */
  sources?: string[];
}

/** 演员资料档案。查不到时后端回 `null`（不是错误）。 */
export function fetchJavWallActorProfile(key: string) {
  return http.get<JavWallActorProfile | null>("/admin/strm-scrape/jav-wall/actor/profile", { key });
}

/**
 * TMDB 人像的代理地址（`actor.tmdb.profile_path` → 可直接当 src 用）。
 *
 * 与番号封面同一个理由：image.tmdb.org 在国内浏览器经常打不开，必须由后端转发。
 * 空路径原样返回空串，让 `<img v-if>` 正常跳过。
 */
export function javActorPhotoURL(path: string | undefined | null, size = "w185"): string {
  const p = (path ?? "").trim();
  if (!p) return "";
  return `/api/admin/strm-scrape/jav-wall/actor/photo?path=${encodeURIComponent(p)}&size=${encodeURIComponent(size)}`;
}

/**
 * 用户**自己上传**的演员头像（存/删的返回值）。
 *
 * `avatar_url` 一定是**服务端**地址（`/api/admin/...`），删除时是空串（表示回到
 * 侧车 / 资料层那张）。⚠️ 前端拿到之后要核对这一点 —— 绝不能把本机文件路径或
 * `URL.createObjectURL` 的 blob 地址当结果，那样换台设备就裂图了。
 */
export interface JavWallActorAvatar {
  key: string;
  avatar_url: string;
  updated_at?: string;
}

/**
 * 上传一张自定义头像，盖过侧车 / 资料层那只。
 *
 * 传上去的是**当前这台设备上**的图片文件，但后端会解码 → 缩到 ≤480×720 →
 * 重新编码成 JPEG 落进 `data/jav_actor_avatar/`。之后头像由服务端地址提供，
 * 与这份原文件、这台设备彻底无关（换手机、清空相册都还在）。
 */
export function uploadJavActorAvatar(key: string, file: File) {
  const form = new FormData();
  form.append("file", file);
  return http.form<JavWallActorAvatar>(
    `/admin/strm-scrape/jav-wall/actor/avatar?key=${encodeURIComponent(key)}`,
    form,
  );
}

/**
 * 删掉自定义头像，回到侧车 / 资料层那张（幂等：本来就没有也不报错）。
 */
export function deleteJavActorAvatar(key: string) {
  return http.del<JavWallActorAvatar>("/admin/strm-scrape/jav-wall/actor/avatar", undefined, { key });
}

