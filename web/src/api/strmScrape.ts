import { http } from "./client";
import type { FileItem } from "./types";

export type StrmScrapeWriteMode = "missing_only" | "overwrite";
export type StrmScrapeItemStatus = "ok" | "miss" | "doubt";
export type StrmScrapeTVState = "ended" | "updating";
export type StrmScrapeItemListSort =
  | "title_asc"
  | "year_desc"
  | "year_asc"
  | "added_desc"
  | "added_asc";

export interface StrmScrapeItem {
  id: string;
  rel_dir: string;
  strm_name?: string;
  title: string;
  year?: number;
  media_type: string;
  status: StrmScrapeItemStatus;
  has_nfo: boolean;
  has_poster: boolean;
  has_pending?: boolean;
  manual_done?: boolean;
  tmdb_id?: string;
  poster_url?: string;
  folder_name?: string;
  file_count: number;
  ep_local?: number;
  ep_tmdb?: number;
  ep_scraped?: number;
  tv_state?: StrmScrapeTVState | string;
  added_at?: string;
}

export interface StrmScrapeProgress {
  running: boolean;
  strm_task_id: number;
  total: number;
  done: number;
  skipped: number;
  failed: number;
  message: string;
  error?: string;
  started_at?: string;
  current_item_id: string;
  item_revision: number;
  updated_item?: StrmScrapeItem;
}

export interface StrmScrapeRematchResult {
  item: StrmScrapeItem;
  started: boolean;
  progress: StrmScrapeProgress;
}

export interface StrmScrapeSettings {
  write_mode: StrmScrapeWriteMode;
  tmdb_api_key: string;
  tmdb_language: string;
  tmdb_api_host: string;
  tmdb_image_host: string;
  tmdb_request_interval_ms: number;
}

export interface StrmScrapeItemListQuery {
  offset?: number;
  limit?: number;
  keyword?: string;
  status?: StrmScrapeItemStatus | "";
  media_type?: "movie" | "tv" | "";
  tv_state?: StrmScrapeTVState | "";
  sort?: StrmScrapeItemListSort;
}

export interface StrmScrapeItemListStats {
  total: number;
  ok: number;
  miss: number;
  doubt: number;
}

export interface StrmScrapeItemListResult {
  items: StrmScrapeItem[];
  total: number;
  offset: number;
  limit: number;
  has_more: boolean;
  stats: StrmScrapeItemListStats;
}

export interface StrmScrapeScope {
  strm_task_id: number;
  excluded_dirs: string[];
}

export function fetchStrmScrapeScope(strmTaskId: number) {
  return http.get<StrmScrapeScope>("/admin/strm-scrape/scope", { strm_task_id: strmTaskId });
}

export function saveStrmScrapeScope(strmTaskId: number, excludedDirs: string[]) {
  return http.put<StrmScrapeScope>("/admin/strm-scrape/scope", {
    strm_task_id: strmTaskId,
    excluded_dirs: excludedDirs,
  });
}

export async function fetchStrmScrapeScopeDirectories(strmTaskId: number, parent = "") {
  const items = await http.get<Array<{ id: string; name: string; mod_time?: string }>>(
    "/admin/strm-scrape/scope/directories",
    { strm_task_id: strmTaskId, parent },
  );
  return items.map<FileItem>((item) => ({
    id: item.id,
    name: item.name,
    size: 0,
    is_dir: true,
    mod_time: item.mod_time,
  }));
}

export function fetchStrmScrapeSettings() {
  return http.get<StrmScrapeSettings>("/admin/strm-scrape/settings");
}

export function saveStrmScrapeSettings(settings: Partial<StrmScrapeSettings>) {
  return http.put<StrmScrapeSettings>("/admin/strm-scrape/settings", settings);
}

export function runStrmScrape(strmTaskId: number, writeMode?: StrmScrapeWriteMode) {
  return http.post<StrmScrapeProgress>("/admin/strm-scrape/run", {
    strm_task_id: strmTaskId,
    write_mode: writeMode,
  });
}

export function stopStrmScrape() {
  return http.post<StrmScrapeProgress>("/admin/strm-scrape/stop");
}

export function fetchStrmScrapeProgress() {
  return http.get<StrmScrapeProgress>("/admin/strm-scrape/progress");
}

export function fetchStrmScrapeItems(strmTaskId: number, query: StrmScrapeItemListQuery = {}) {
  return http.get<StrmScrapeItemListResult>("/admin/strm-scrape/items", {
    strm_task_id: strmTaskId,
    ...query,
  });
}

export function refreshStrmScrapeIndex(strmTaskId: number, query: StrmScrapeItemListQuery = {}) {
  return http.post<StrmScrapeItemListResult>("/admin/strm-scrape/refresh-index", {
    strm_task_id: strmTaskId,
    ...query,
  });
}

export function rematchStrmScrapeItem(input: {
  strm_task_id: number;
  item_id: string;
  tmdb_id: string;
  media_type: string;
  title?: string;
  year?: number;
}) {
  return http.post<StrmScrapeRematchResult>("/admin/strm-scrape/rematch", input);
}

export function markStrmScrapeNormal(input: {
  strm_task_id: number;
  item_id: string;
  media_type?: string;
  clear_match?: boolean;
}) {
  return http.post<StrmScrapeItem>("/admin/strm-scrape/mark-normal", input);
}

export function rescrapeStrmScrapeItem(input: { strm_task_id: number; item_id: string }) {
  return http.post<StrmScrapeRematchResult>("/admin/strm-scrape/rescrape", input);
}

/**
 * 一个可播放文件：`.strm` 的文件名 + 它正文里那一行播放地址。
 *
 * 墙的卡片 payload 里拿不到账号与 file_id，所以播放地址只能由服务端读 `.strm` 得到
 * （见后端 internal/strmscrape/playable.go）。详情抽屉一次拿到该作品的全部集。
 */
export interface PlayableFile {
  /** 磁盘上的 `.strm` 文件名（含后缀），列表的 key 与显示名都用它。 */
  name: string;
  /** `.strm` 正文里那一行，**原样**（后端不重新拼，那是唯一的真相源）。 */
  path: string;
  /** 同目录的字幕。字幕在本地磁盘上，不需要网盘 file_id。 */
  subtitles?: PlayableSubtitle[];
}

/** 作品目录里的一份字幕。 */
export interface PlayableSubtitle {
  name: string;
  /** 语言名，由文件名后缀推出来（「简体中文」等）。 */
  label: string;
  format: "srt" | "vtt" | "sup";
  /** 取内容的站内地址。 */
  url: string;
}

/**
 * 一张卡的详情（详情抽屉用）。
 *
 * 两个抽屉共用这一个形状：番号那面（`WallDetailDrawer`）填番号/片商/发行商/系列，
 * TMDB 那面（`TmdbWallDetailDrawer`）填导演/制片/分级/合集/评分——**同一批字段装不同的
 * 内容**，所以两边的映射写在各自的后端方法里（`JavWallDetail` / `TMDBWallDetail`），
 * 前端按「有就显示」渲染。
 *
 * 字段大多可能为空：番号那面只有抓过详情的片才有演员/剧照；TMDB 那面只有**本次改造
 * 之后刮过**（或点过「补齐剧照与演员」）的才有。
 */
export interface WallDetail {
  number: string;
  title: string;
  origin_title?: string;
  release_date?: string;
  duration?: number;
  score?: number;
  /** 评分满分。番号是 5、TMDB 是 10 —— 星标必须按它折算，别写死。 */
  score_max?: number;
  votes?: number;
  summary?: string;
  director?: string;
  /** 番号：片商；TMDB：制片公司（多个用 ` / ` 连）。 */
  maker?: string;
  /** 番号：发行商；TMDB：分级（mpaa）。空则前端显示 `—`。 */
  publisher?: string;
  /** 番号：系列；TMDB：所属合集。 */
  series?: string;
  tags?: string[];
  actors?: WallActor[];
  poster_url?: string;
  /**
   * 海报的宽高比：`3:2` 横版 / `2:3` 竖版 / `1:1`。
   *
   * **由后端读图片头算**，别在前端 onload 之后再改布局 —— 那样会先按错误比例
   * 铺一次再跳一下。番号的 `thumb.jpg` 是横版，TMDB 的 `poster.jpg` 是竖版。
   * 空串 = 读不出图，前端按横版（默认）处理。
   */
  poster_ratio?: string;
  /**
   * 背景图（`fanart.jpg`）的取图地址。
   *
   * ⚠️ **两个抽屉都不渲染它**（用户明确要求不做背景大图）。填上是为了 Emby 之外
   * 将来想用时不必再改后端。TMDB 刮削会真的把这张图下下来（给 Emby 当背景）。
   */
  fanart_url?: string;
  /** 剧照（本地 `extrafanart/` 里的那些），走 /poster 取图。 */
  previews?: string[];
  /** 上游预告片地址（m3u8）。空 = 没有。 */
  trailer_url?: string;
  /** 这个作品能播的文件（多集就是多条），与卡片播放同一套。 */
  playable?: PlayableFile[];
  javdb_url?: string;
  notice?: string;
  /** 源媒体信息（文件名 / 路径 / 大小 / 类型），参照 Emby 的详情页。 */
  source?: WallSource;
}

/**
 * 「源媒体信息」那一块。
 *
 * `path` 是**容器内**路径经设置里的「宿主机路径映射」换算后的结果 ——
 * 群晖上看到的就是映射后那条；没配映射时是容器内路径（至少真实）。
 */
export interface WallSource {
  file_name: string;
  path: string;
  /** 网盘上那个**源文件**的大小（不是 .strm 自己的）。取不到为 0。 */
  size?: number;
  size_text?: string;
  ext?: string;
}

/**
 * 详情里的一位演员。
 *
 * 两面墙的 `avatar_url` 来源不同，前端**取图方式也不同**：
 *   - 番号：上游 JAVDB 地址（经 XOR 混淆），必须走 `/api/jav/image` 代理 ——
 *     所以 `WallDetailDrawer` 用 `javImageURL()` 包一层；
 *   - TMDB：本地文件（`media/actors/{id}.jpg`），后端已经把 URL 拼成 `/poster`
 *     的取图地址，直接 `src` 即可（`TmdbWallDetailDrawer` 就是这么用的）。
 */
export interface WallActor {
  id?: string;
  name: string;
  /** 上游性别码：1 = 男。TMDB 那面不填（它没有这个字段）。 */
  gender?: number;
  avatar_url?: string;
}

/** TMDB 影片墙的详情。 */
export function fetchStrmScrapeItemDetail(strmTaskId: number, itemId: string) {
  return http.get<WallDetail>("/admin/strm-scrape/items/detail", {
    strm_task_id: String(strmTaskId),
    item_id: itemId,
  });
}

/**
 * 给已经刮过的作品补上后来才有的数据（背景图 / 剧照 / 演员 / 评分 / 时长 / 完整 nfo）。
 *
 * 只补缺：**不重写已有 nfo 的正文**，也不覆盖已有的图。已经齐了的作品会被跳过。
 * 与「开始刮削」同一条后台队列，进度看 `fetchStrmScrapeProgress`。
 */
export function backfillStrmScrapeImages(strmTaskId: number) {
  return http.post<StrmScrapeProgress>("/admin/strm-scrape/backfill", {
    strm_task_id: strmTaskId,
  });
}
