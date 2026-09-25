/**
 * 番号（JAV）模块的前端类型。
 *
 * 字段名与 Go 侧 DTO 一一对应（snake_case），不做驼峰转换 ——
 * 转换层是另一处会漂移的地方，而接口返回的就是这个形状。
 */

/** 影片卡片。列表、榜单、搜索结果共用。 */
export interface JavMovieCard {
  id: string;
  number: string;
  title: string;
  origin_title: string;
  cover_url: string;
  thumb_url: string;
  javbus_cover: string;
  /** 服务端算好的封面回落值（cover_url → javbus_cover → thumb_url）。 */
  cover: string;
  duration: number;
  release_date: string;
  score: number;
  tags: string[];
  magnets_count: number;
  type: string;
  in_library: boolean;
}

/** 影片详情。比卡片多出简介、演员、磁链等。 */
export interface JavMovieDetail extends JavMovieCard {
  summary: string;
  review: string;
  director_name: string;
  maker_name: string;
  publisher_name: string;
  series_name: string;
  series_id: string;
  preview_video_url: string;
  preview_images: string[];
  actors: JavActor[];
  magnets: JavMagnet[];
  /** 评论区里用户贴出来的链接（磁链 / ed2k），带分享者。 */
  comment_shares: JavCommentShare[];
  /** 含这部影片的清单。 */
  related_lists: JavRelatedList[];
  /**
   * **本地存着的**评论条数 —— 「评论」那一档表头上的数字用它。
   *
   * 不用 reviews_count：那是上游报的总数，可能几百条，而这一档实际翻得出来的
   * 只有本地这批，表头写个翻不到的数就是骗人。
   */
  comments_count: number;
  has_cn_sub: boolean;
  /** 上游有没有在线播放源（详情页那个「在线」角标）。 */
  can_play: boolean;
  relative_movies: JavRelativeMovie[];
  reviews_count: number;
}

/**
 * 「评论区分享」里的一条：用户评论正文里贴出来的链接。
 *
 * 由服务端从评论正文现算（不落库），所以没有 id —— 用 uri 当 key。
 */
export interface JavCommentShare {
  /** 链接原文（磁链或 ed2k），推送时原样提交，一个字符都不改写。 */
  uri: string;
  kind: "magnet" | "ed2k";
  name: string;
  size_text: string;
  size_bytes: number;
  /** 是不是认出了体积。「没认出」和「体积是 0」是两回事。 */
  has_size: boolean;
  /** 评论日期，YYYY-MM-DD。 */
  date: string;
  /** 分享者用户名，空串时显示「匿名」。 */
  sharer: string;
  sharer_id: number;
  /** 去掉链接之后的评论正文。 */
  comment: string;
  resolution: string;
  resolution_badge: string;
  uncensored: boolean;
  subtitle: boolean;
}

/**
 * 「某位分享者分享过的影片」里的一部。
 *
 * 是「他的评论里贴过链接的那些评论所属的影片」，**本地聚合** ——
 * 上游没有「按用户查他发过的评论」的接口，所以覆盖范围 = 评论已入库的影片。
 */
export interface JavUserShare extends JavMovieCard {
  /** 他在这一部下贴出来的链接，原样给 —— 弹窗里每一条都要能直接复制/推送。 */
  links: JavCommentShare[];
}

/** 分享者弹窗的一页。 */
export interface JavUserSharesResult {
  username: string;
  items: JavUserShare[];
  /** 他分享过的影片**总部数**（不只这一页）。 */
  total: number;
  has_more: boolean;
}

/** 「用户」那一档里关注的分享者。 */
export interface JavFollowedUser {
  user_id: number;
  username: string;
  /** 他贴过链接的评论条数（与点进去看到的影片数同源）。 */
  share_count: number;
  created_at: string;
}

/** 「关联清单」里的一条。 */
export interface JavRelatedList {
  id: string;
  name: string;
  movies_count: number;
}

export interface JavActor {
  id: string;
  name: string;
  avatar_url: string;
}

/** 磁链。角标由服务端算好，前端只负责画。 */
export interface JavMagnet {
  btih: string;
  name: string;
  magnet: string;
  size_text: string;
  size_bytes: number;
  date: string;
  /** 档位名「高清 / 超清」，没有信号时为空串。分档，与订阅排序共用一套判定。 */
  resolution: string;
  /** 磁链卡片上那颗清晰度胶囊的文案：4K / UHD / HD，没有信号时为空串。 */
  resolution_badge: string;
  uncensored: boolean;
  subtitle: boolean;
  edited: boolean;
  source_label: string;
  codec_label: string;
  tracker_count: number;
  /** **这一颗**已经推送到网盘并下载完成。逐颗算，不是「这部片推成功过」。 */
  pushed: boolean;
  /** **这一颗**已提交、还在网盘下载。提交到下载完成之间可能有好几分钟。 */
  pushing: boolean;
}

export interface JavReview {
  id: number;
  username: string;
  score: number;
  content: string;
  status_title: string;
  watched_count: number;
  likes_count: number;
  liked: boolean;
  created_at: string;
}

/** 一次搜索的结论。 */
export interface JavSearchResult {
  items: JavMovieCard[];
  total: number;
  page: number;
  /** upstream = 打的是 JAVDB 全站；local = 上游不可用、回落到了本地库。 */
  source: "upstream" | "local";
  /** 回落时给用户的一句解释，正常时为空。 */
  notice: string;
  /**
   * 按演员搜时反查出来的演员 id / 名字，供「订阅该演员」按钮建订阅。
   *
   * 反查不出来时是空串 —— 那时不显示按钮，好过订错人。
   */
  actor_id: string;
  actor_name: string;
}

export interface JavListResult {
  items: JavMovieCard[];
  total: number;
  page: number;
  page_size: number;
  total_page: number;
}

/** 榜单种类。 */
export type JavRankingKind = "top250" | "daily" | "weekly" | "monthly" | "actor";

export interface JavRankingResult {
  movies: JavMovieCard[];
  actors: JavActor[];
  /**
   * 这一档的总条数。
   *
   * 日/周/月榜是**整榜条数**（官网一次给全，实测固定 60），服务端按 20 一页切片 ——
   * 分页器靠它算总页数。以前没有这个字段，前端只能拿「这页拿满了没」去猜，
   * 60 条 20 一页时会算出 4 页、多出一个空页。
   */
  total?: number;
}

/** 订阅的目标类型。 */
export type JavTargetType = "movie" | "online" | "actor" | "list";

/** 界面上的三档下载模式。 */
export type JavMode = "strict" | "upgrade" | "predownload";

export interface JavSubscription {
  id: number;
  target_type: JavTargetType;
  target_id: string;
  target_url: string;
  target_name: string;
  status: "active" | "paused" | "completed";
  /** 卡片封面：影片订阅给影片封面，演员订阅给头像。 */
  cover: string;
  thumb: string;
  number: string;
  release_date: string;
  /** 由 download_mode + pre_download 合成的展示值。 */
  mode: JavMode;
  download_mode: "strict" | "upgrade";
  pre_download: boolean;
  /** 把影片评论区里分享的链接也纳入自动推送的候选。默认关。 */
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
  subfolder_mode: "none" | "code" | "title";
  last_checked_at: string;
  last_push_at: string;
  completed_at: string;
  last_error: string;
  /** 这一轮检查匹配到多少条。 */
  matched_count: number;
  /** **成功到网盘**的影片数（去重）。只数离线任务已完成的，刚提交还在下的不算。 */
  pushed_count: number;
  /** 已提交、**还在网盘下载**的影片数。跟 pushed_count 一起看才说明白「推了、还在下」。 */
  pending_count: number;
  created_at: string;
}

export interface JavSubscriptionInput {
  id?: number;
  target_type: string;
  target_id: string;
  target_url?: string;
  target_name: string;
  download_mode: string;
  pre_download: boolean;
  /** 把影片评论区里分享的链接也纳入自动推送的候选。默认关。 */
  include_comment_links: boolean;
  qualities: string[];
  min_size_mb?: number | null;
  max_size_mb?: number | null;
  max_file_count?: number | null;
  release_date_from?: string;
  release_date_to?: string;
  expiry_days?: number | null;
  categories?: string[];
  exclude_categories?: string[];
  enabled: boolean;
  target_account_id?: number;
  target_parent_id?: string;
  target_display_path?: string;
  push_provider?: string;
  subfolder_mode?: string;
}

/** 候选资源。拒收原因同时给原始码与人话版本。 */
export interface JavCandidate {
  id: number;
  movie_id: string;
  movie_number: string;
  movie_title: string;
  magnet_name: string;
  magnet_uri: string;
  size_text: string;
  size_bytes: number;
  release_date: string;
  quality_tags: string[];
  /** [清晰度, 破解, 体积]，按字典序比较。 */
  resource_score: number[];
  matched: boolean;
  push_ok: boolean;
  predownload: boolean;
  attempted: boolean;
  rejection_reasons: string[];
  rejection_reasons_text: string[];
  /** 档位名「高清 / 超清」。 */
  resolution: string;
  /** 角标文案：4K / UHD / HD。与磁链卡片同一套判定。 */
  resolution_badge: string;
  uncensored: boolean;
  subtitle: boolean;
  source_label: string;
  codec_label: string;
  /** 这颗资源来自**影片评论区的用户分享**，而不是 JAVBUS 磁链表。 */
  from_comment: boolean;
}

export interface JavCheckResult {
  run_id: number;
  matched_count: number;
  rejected_count: number;
  movies: number;
  candidates: JavCandidate[];
  message: string;
}

export interface JavRun {
  id: number;
  subscription_id: number;
  trigger_type: string;
  status: string;
  matched_count: number;
  rejected_count: number;
  error: string;
  started_at: string;
  finished_at: string;
}

/** 「已完成」那一档的一条：一部推送成功过的影片。 */
export interface JavCompletedMovie extends JavMovieCard {
  subscription_id: number;
  subscription_name: string;
  target_type: string;
  pushed_at: string;
  sub_status: string;
}

/** 「关联影片」里的一条。 */
export interface JavRelativeMovie {
  id: string;
  number: string;
  /** 竖版小图。上游给的就是竖图，用 3:2 的框会裁掉大半。 */
  thumb: string;
  in_library: boolean;
}

/** 演员/清单订阅弹窗里的一部影片。 */
export interface JavSubMovie extends JavMovieCard {
  sub_status: "active" | "completed" | "skipped";
  /** 过不过本订阅自己的条件（日期窗/清晰度/类别/黑名单…）。判据与检查同一个调用。 */
  eligible: boolean;
  /** 不合条件的原因，人话版本。同一部片可能同时踩几条。 */
  reject_text: string[];
}

export interface JavBlacklistEntry {
  id: number;
  target_type: string;
  target_id: string;
  target_name: string;
  reason: string;
  created_at: string;
  /**
   * 加入这条黑名单时记下来的影片数（快照）。0 = 当时没取到
   * （迁移之前的老条目，或拉黑时上游不通）。
   */
  movies_count: number;
}

export interface JavPushResult {
  ok: boolean;
  message: string;
  record_id: number;
  task_id: string;
  movie_id: string;
  magnet: string;
  size_text: string;
}

/** 下载记录页的一行。 */
export interface JavPushRecord {
  id: number;
  magnet: string;
  name: string;
  size_text: string;
  movie_id: string;
  code: string;
  title: string;
  cover: string;
  status: "pending" | "pushed" | "failed";
  downloader: string;
  target_path: string;
  error: string;
  subscription_id: number;
  pushed_at: string;
  created_at: string;
  hd: boolean;
  uncensored: boolean;
  /** 这条资源来自**影片评论区的用户分享**。存下来的历史事实，不是现算的。 */
  from_comment: boolean;
}

export interface JavPushRecordFilter {
  status?: string;
  downloader?: string;
  keyword?: string;
  from?: string;
  to?: string;
  page?: number;
  limit?: number;
}

/** 媒体服务器。api_key 是只写的，这里只有 has_api_key。 */
export interface JavServer {
  id: number;
  name: string;
  url: string;
  type: "emby" | "jellyfin";
  enabled: boolean;
  has_api_key: boolean;
  last_sync_at: string;
  last_status: string;
  last_error: string;
  item_count: number;
  code_count: number;
}

export interface JavLibraryStats {
  servers: Array<{
    server_id: number;
    name: string;
    type: string;
    url: string;
    item_count: number;
    code_count: number;
    distinct_codes: number;
    last_sync_at: string;
    last_status: string;
    last_error: string;
  }>;
  total_distinct_codes: number;
  total_items: number;
  movies_with_code: number;
  movies_without_code: number;
  /**
   * 角标真的会标成「已入库」的影片数。
   *
   * 与 total_distinct_codes 不是一回事：那个数的是媒体库里的番号（可能有番号
   * 本地压根没有影片记录），这个数的是本地的影片。
   */
  in_library_movies: number;
}

export interface JavConnectionResult {
  ok: boolean;
  message: string;
  latency_ms: number;
  detail?: string;
}

export interface JavTestConnectionResult {
  javdb: JavConnectionResult;
  javbus: JavConnectionResult;
  logged_in: boolean;
}

export interface JavAPINode {
  name: string;
  base: string;
}

/** 番号相关设置。密码与 token 从不回传，只有 has_* 标记。 */
export interface JavConfig {
  enabled: boolean;

  username: string;
  has_password: boolean;
  has_token: boolean;
  login_status: string;
  login_message: string;
  last_login_at: string;

  api_base: string;
  api_nodes: JavAPINode[];
  javbus_base: string;
  use_proxy: boolean;
  /** 用户自己配了代理并且开着（仅供开关回显）。 */
  proxy_ready: boolean;
  /** 实际会走的那条路：具体地址 / 系统代理（…） / 直连。 */
  effective_proxy: string;
  min_interval_ms: number;
  request_gap_ms: number;
  timeout_sec: number;
  retry: number;

  sub_check_enabled: boolean;
  sub_daily_times: string[];
  sub_check_interval_min: number;
  sub_sync_enabled: boolean;
  sub_sync_times: string[];
  sub_push_batch: number;
  sub_concurrency: number;
  sub_retry_enabled: boolean;
  sub_interval_min_sec: number;
  sub_interval_max_sec: number;
  sub_timeout_sec: number;
  /** 推送成功后要不要在资源所在目录写 `<番号>.json`（元数据侧车）。 */
  sidecar_enabled: boolean;

  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_push_provider: string;

  library_sync_enabled: boolean;
  library_cron: string;
  /** 由服务端用 cron 算出来的下次触发时间，算不出时为空串。 */
  library_next_run_at: string;
  library_last_run_at: string;
  library_sync_status: string;
  library_sync_message: string;
}

export interface JavConfigInput {
  enabled?: boolean;
  username?: string;
  /** 留空 = 不修改。凭据从不回传，所以「清空」这个意图表达不出来。 */
  password?: string;
  token?: string;
  api_base?: string;
  javbus_base?: string;
  use_proxy?: boolean;
  min_interval_ms?: number;
  request_gap_ms?: number;
  timeout_sec?: number;
  retry?: number;
  sub_check_enabled?: boolean;
  sub_daily_times?: string[];
  sub_check_interval_min?: number;
  sub_sync_enabled?: boolean;
  sub_sync_times?: string[];
  sub_push_batch?: number;
  sub_concurrency?: number;
  sub_retry_enabled?: boolean;
  sub_interval_min_sec?: number;
  sub_interval_max_sec?: number;
  sub_timeout_sec?: number;
  sidecar_enabled?: boolean;
  default_account_id?: number;
  default_parent_id?: string;
  default_display_path?: string;
  default_push_provider?: string;
  library_sync_enabled?: boolean;
  library_cron?: string;
}

export interface JavSyncStatus {
  running: boolean;
  trigger: string;
  started_at: string;
  finished_at: string;
  message: string;
  servers: Array<{
    server_id: number;
    name: string;
    ok: boolean;
    items: number;
    codes: number;
    message: string;
  }>;
}

/** 搜索框的类型下拉，与源码 base.html 的类型选择一致。 */
export const JAV_SEARCH_TYPES = [
  { value: "all", label: "全部" },
  { value: "number", label: "番号" },
  { value: "actor", label: "演员" },
  { value: "series", label: "系列" },
  { value: "maker", label: "片商" },
  { value: "director", label: "导演" },
  { value: "lists", label: "清单" },
] as const;

/** 影库页的筛选档位。 */
export const JAV_TYPE_FILTERS = [
  { value: "", label: "筛选 全部" },
  { value: "0", label: "有码" },
  { value: "1", label: "无码" },
  { value: "2", label: "欧美" },
  { value: "3", label: "FC2" },
] as const;

/**
 * 榜单的内容分类胶囊。
 *
 * 日/周/月榜与演员榜都是这四档，但**演员榜不给 FC2**：上游 `type=3` 是静默
 * 回落成 `type=0` 的（实测返回有码那份名单，连 md5 都一样），给了会显示
 * 「有码的名单挂在 FC2 标签下」。
 *
 * 没有「全部」这一档 —— 官网的 `t=` 是必填的，缺省就是有码。
 */
export const JAV_RANK_TYPE_TABS = [
  { value: "0", label: "有码" },
  { value: "1", label: "无码" },
  { value: "2", label: "欧美" },
  { value: "3", label: "FC2" },
] as const;

/**
 * Top250 的分类/年份下拉。
 *
 * 上游把「分类」和「年份」做成同一个 `type` 参数的两种取值，所以界面是一个下拉：
 * 空 → `type=all`；0..3 → `type=video_type&type_value=<值>`；
 * 四位年份 → `type=year&type_value=<年份>`。翻译见 `javTopTypeParams`。
 */
export function javTopTypeOptions(now = new Date().getFullYear()) {
  const years: { value: string; label: string }[] = [];
  // 2008 是源站 Top250 的起点，与内网那套一致。
  for (let y = now; y >= 2008; y--) years.push({ value: String(y), label: String(y) });
  return [
    { value: "", label: "全部" },
    { value: "0", label: "有码" },
    { value: "1", label: "无码" },
    { value: "2", label: "欧美" },
    { value: "3", label: "FC2" },
    ...years,
  ];
}

/** 把 Top250 下拉选中的值翻成上游的 (type, type_value) 那一对。 */
export function javTopTypeParams(value: string): { type: string; typeValue: string } {
  const v = String(value ?? "").trim();
  if (v === "") return { type: "all", typeValue: "" };
  if (["0", "1", "2", "3"].includes(v)) return { type: "video_type", typeValue: v };
  return { type: "year", typeValue: v };
}

/** 订阅弹窗里可勾选的质量。 */
export const JAV_QUALITY_OPTIONS = [
  { value: "hd", label: "高清" },
  { value: "uhd", label: "超清" },
  { value: "subtitle", label: "字幕" },
  { value: "uncensored", label: "破解" },
] as const;

export const JAV_DOWNLOAD_MODES = [
  { value: "strict", label: "严格模式" },
  { value: "upgrade", label: "洗版模式" },
  { value: "predownload", label: "预下载" },
] as const;

export const JAV_SUBFOLDER_MODES = [
  { value: "code", label: "按番号（SSIS-001/）" },
  { value: "title", label: "按片名" },
  { value: "none", label: "不建子目录" },
] as const;

/** 把 download_mode + pre_download 合成三档，与后端 SubscriptionMode 同一套规则。 */
export function javModeOf(downloadMode: string, preDownload: boolean): JavMode {
  if (downloadMode === "upgrade") return "upgrade";
  if (preDownload) return "predownload";
  return "strict";
}

/** 把三档拆回正交的两个字段。 */
export function javModeToFields(mode: string): {
  download_mode: string;
  pre_download: boolean;
} {
  if (mode === "upgrade") return { download_mode: "upgrade", pre_download: false };
  if (mode === "predownload") return { download_mode: "strict", pre_download: true };
  return { download_mode: "strict", pre_download: false };
}

export function javTargetLabel(targetType: string): string {
  switch (targetType) {
    case "movie":
      return "影片订阅";
    case "online":
      return "在线订阅";
    case "actor":
      return "演员订阅";
    case "list":
      return "清单订阅";
    default:
      return targetType;
  }
}

export function javStatusLabel(status: string): string {
  switch (status) {
    case "active":
      return "订阅中";
    case "paused":
      return "已暂停";
    case "completed":
      return "已完成";
    case "skipped":
      return "已跳过";
    default:
      return status;
  }
}
