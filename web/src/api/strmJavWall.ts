import { http } from "@/api/client";
// 可播文件是**两面墙共用**的形状（同一个后端实现、同一套字幕结构），
// 所以类型定义留在 strmScrape.ts 那一份，这里只 import —— 两边各写一份迟早会漂。
import type { WallDetail } from "@/api/strmScrape";

// 番号影片的海报墙（辅助工具 → 海报墙，选中「媒体类型 = 番号影片」的任务时用）。
//
// 与 TMDB 那套（strmScrape.ts）共用同一个页面，但数据源完全不同：这边是**扫本地
// 媒体库目录**（`.strm` 主干 + 四个产物的有无），不碰 TMDB、也不落库。

/** 一级目录（用户口语里的「类型」）。name 为空串表示根目录下散落的那些。 */
export interface JavWallCategory {
  name: string;
  count: number;
}

/** 墙上一张卡 = 一个 `.strm` 主干的那一套本地文件。 */
export interface JavWallItem {
  id: string;
  rel_dir: string;
  category: string;
  /** 磁盘上的原样主干（含大小写与尾空格）——回传时必须原样，服务端靠它派生文件名。 */
  stem: string;
  title: string;
  number: string;
  /** 平铺布局（该目录有多个 `.strm`），决定四个产物的文件名。 */
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

function listParams(taskId: number, q: JavWallListQuery): Record<string, string | number> {
  const out: Record<string, string | number> = { strm_task_id: taskId };
  // category 明确允许空串（= 全部），所以判 undefined 而不是判 falsy。
  if (q.category !== undefined && q.category !== "") out.category = q.category;
  if (q.keyword) out.keyword = q.keyword;
  if (q.sort) out.sort = q.sort;
  if (q.offset) out.offset = q.offset;
  if (q.limit) out.limit = q.limit;
  return out;
}

export function fetchJavWallItems(taskId: number, q: JavWallListQuery = {}) {
  return http.get<JavWallListResult>("/admin/strm-scrape/jav-wall/items", listParams(taskId, q));
}

/**
 * 「刷新元数据」：作废服务端快照、重读磁盘、按同一套条件重列。
 *
 * **它不是重新生成**（那是卡片上的「重刮」）——只重读本地文件，不联网、不重建
 * nfo/图片，作用是把刚保存的东西显示出来（图片 URL 上的 rev 也跟着换）。
 */
export function refreshJavWall(taskId: number, q: JavWallListQuery = {}) {
  return http.post<JavWallListResult>("/admin/strm-scrape/jav-wall/refresh", {}, listParams(taskId, q));
}

export function fetchJavWallItem(taskId: number, relDir: string, stem: string) {
  return http.get<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item", {
    strm_task_id: taskId,
    rel_dir: relDir,
    stem,
  });
}

export function saveJavWallMeta(taskId: number, relDir: string, stem: string, meta: JavMovieMeta) {
  return http.put<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/meta", {
    strm_task_id: taskId,
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
  taskId: number,
  relDir: string,
  stem: string,
  rect: JavCropRect,
  watermarks: string[] = [],
) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/poster", {
    strm_task_id: taskId,
    rel_dir: relDir,
    stem,
    rect,
    watermarks,
  });
}

/** 「重刮」：用**本地那份 json** 重建这一部（nfo 重写、封面/海报重下、剧照补缺）。 */
export function rebuildJavWallItem(taskId: number, relDir: string, stem: string) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/rebuild", {
    strm_task_id: taskId,
    rel_dir: relDir,
    stem,
  });
}

export function refreshJavWallItem(taskId: number, relDir: string, stem: string) {
  return http.post<JavWallItemDetail>("/admin/strm-scrape/jav-wall/item/refresh", {
    strm_task_id: taskId,
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
export function fetchJavWallHiddenDirs(taskId = 0) {
  const params: Record<string, number> = {};
  if (taskId > 0) params.strm_task_id = taskId;
  return http.get<JavWallHiddenDirList>("/admin/strm-scrape/jav-wall/hidden-dirs", params);
}

/**
 * 保存隐藏名单（**勾上 = 隐藏**）。
 *
 * 名单是**全局**一份（不按任务存），`taskId` 只决定保存后回吐的候选清单按哪个任务扫。
 */
export function saveJavWallHiddenDirs(taskId: number, dirs: string[]) {
  return http.put<JavWallHiddenDirList>("/admin/strm-scrape/jav-wall/hidden-dirs", {
    strm_task_id: taskId > 0 ? taskId : 0,
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
export function fetchJavWallItemDetail(input: {
  strm_task_id: number;
  rel_dir: string;
  stem: string;
}) {
  return http.get<WallDetail>("/admin/strm-scrape/jav-wall/item/detail", {
    strm_task_id: String(input.strm_task_id),
    rel_dir: input.rel_dir,
    stem: input.stem,
  });
}

/** 在线刮削的结果（打上游补齐缺的元数据）。 */
export interface JavWallOnlineScrapeResult {
  total: number;
  scraped: number;
  skipped: number;
  failed: number;
  /** 补出来的图片 / 字幕文件数（封面、海报、剧照、字幕）。 */
  images: number;
}

/** 在线刮削的进度（前端按钮上的「刮削中…」）。 */
export interface JavWallOnlineScrapeProgress {
  strm_task_id: number;
  running: boolean;
  done: number;
  total: number;
  message: string;
  /** 跑完之后的汇总（running 变 false 时才有）。 */
  result?: JavWallOnlineScrapeResult;
}

/**
 * 在线刮削：打上游把目录下**缺的**元数据补齐，写回 json 与 nfo。
 *
 * **后台任务**：接口立刻返回进度，跑完的汇总从进度接口的 `result` 里读
 * （同步跑十几分钟会被反代 502）。
 *
 * 与「刷新元数据」的区别：那一条只重读本地磁盘（不联网），这一条会联网。
 * 番号按 **nfo 的 `<num>` → 侧车 json 的 number → 拆主干**（去掉 `-U`/`-C`/`-4K`
 * 那些质量后缀）三条取，**不拿文件名当番号**。
 */
export function onlineScrapeJavWall(input: { strm_task_id: number; rel_dir?: string; stem?: string }) {
  return http.post<JavWallOnlineScrapeProgress>("/admin/strm-scrape/jav-wall/online-scrape", input);
}

export function fetchJavWallOnlineScrapeProgress(strmTaskId: number) {
  return http.get<JavWallOnlineScrapeProgress>("/admin/strm-scrape/jav-wall/online-scrape/progress", {
    strm_task_id: strmTaskId,
  });
}
