/** TG 影片订阅相关的类型，与后端 internal/tgsubscribe 的对外 DTO 一一对应。 */

export type TGMediaType = "movie" | "tv";
export type TGSubscriptionStatus = "active" | "paused" | "completed";
export type TGChannelStatus = "unknown" | "ok" | "error";
export type TGPushProvider = "auto" | "native" | "builtin";

/** 命中记录状态。unmatched/ambiguous/filtered/duplicate 是「没推」的四类原因。 */
export type TGRecordStatus =
  | "unmatched"
  | "ambiguous"
  | "filtered"
  | "duplicate"
  | "pending"
  | "pushed"
  | "upgraded"
  | "superseded"
  | "failed"
  | "ignored"
  /** 识别到了但投不出去：没有投递器、网盘不收这个协议，或账号没配转存凭据。不进重试。 */
  | "unsupported"
  /** 推送失败且**重试也不会好**（提取码错、分享失效、目录对不上）。同样不进重试。 */
  | "unretryable";

export interface TGChannel {
  id: number;
  chat_id: string;
  username: string;
  title: string;
  remark: string;
  level: number;
  enabled: boolean;
  status: TGChannelStatus;
  last_error: string;
  last_message_id: number;
  last_post_at?: string;
  matched_count: number;
  created_at?: string;
  /** 只在「新增频道」的响应里带值：首次回填落库了多少条历史帖。 */
  backfill_posts?: number;
  /**
   * 这个频道累计产出的匹配记录数。
   *
   * 与 matched_count 的区别很重要：matched_count 其实是「已见帖子数」。
   * 帖子数一直涨而产出一直是 0，就说明这个频道抓不到内容。
   */
  record_count: number;
}

/**
 * 内置推荐频道。
 *
 * 清单是实测筛出来的（见后端 internal/tgsubscribe/recommended_channels.go）：
 * 判据是「抽得出来 **而且投得出去**」——很多活跃频道发的全是夸克/百度分享链，
 * LitePan 抽不出来或投不出去，收录进来只会堆满「暂不支持投递」。
 *
 * 首次启动时它们已经**自动入库**成了普通频道，所以这里通常返回空（全被标成已添加）。
 * 还有内容只剩一种情况：当时没加成功（多半是还没配代理），用户后来配好了手动补加。
 * 用户自己删掉的那些不会重新出现 —— 播种是一次性的。
 */
export interface TGRecommendedChannel {
  username: string;
  /** 建议的备注名，入库时直接当备注用，之后想改就改。 */
  remark: string;
  /** 这个频道发什么、需要什么前提。 */
  summary: string;
  /** 当前用户的频道列表里是不是已经有了 —— 有了就不再单独列出来。 */
  added: boolean;
}

export interface TGTMDBSearchResult {
  id: number;
  media_type?: TGMediaType;
  title?: string;
  name?: string;
  original_title?: string;
  original_name?: string;
  overview?: string;
  poster_path?: string;
  release_date?: string;
  first_air_date?: string;
  vote_average?: number;
}

export interface TGSeasonInfo {
  season_number: number;
  episode_count: number;
  air_date?: string;
  name?: string;
}

export interface TGSubscription {
  id: number;
  tmdb_id: string;
  media_type: TGMediaType;
  title: string;
  original_title: string;
  year: number;
  poster_path: string;
  overview: string;
  aliases: string[] | null;
  alias_count: number;
  status: TGSubscriptionStatus;
  target_account_id: number;
  target_parent_id: string;
  target_display_path: string;
  quality_profile_id: number;
  push_provider: TGPushProvider;
  collect_window_min: number;
  upgrade_enabled: boolean;
  best_quality_score: number;
  matched_count: number;
  pushed_count: number;
  last_match_at?: string;
  last_push_at?: string;
  last_error: string;
  collected_episodes: number;
  aired_episodes: number;
  total_episodes: number;
  seasons?: TGSeasonInfo[];
  created_at?: string;
}

export interface TGSubscriptionEpisode {
  season: number;
  episode: number;
  record_id: number;
  offline_task_id: string;
  account_id: number;
  file_id: string;
  target_path: string;
  delivered_at?: string;
}

/**
 * 「已订阅」页的类型筛选。
 *
 * `jav`（番号）目前只是占位：后端 domain.TGMediaType 只有 movie / tv，
 * 订阅表里不会出现这个值 —— 选了它就是一个空列表加一句说明，
 * 而不是假装有数据。
 */
export type TGSubscribeKindFilter = "all" | "movie" | "tv" | "jav";

export const TG_KIND_FILTERS: { value: TGSubscribeKindFilter; label: string }[] = [
  { value: "all", label: "全部类型" },
  { value: "movie", label: "影片" },
  { value: "tv", label: "剧集" },
  { value: "jav", label: "番号" },
];

/**
 * 「已订阅」页的状态筛选。
 *
 * 取值刻意与订阅状态一一对应（`all` 之外都是 TGSubscriptionStatus 的字面量），
 * 这样状态下拉既当筛选器用、又直接是批量修改的目标值。
 */
export type TGSubscribeStatusFilter = "all" | TGSubscriptionStatus;

export const TG_STATUS_FILTERS: { value: TGSubscribeStatusFilter; label: string }[] = [
  { value: "all", label: "全部状态" },
  { value: "active", label: "订阅中" },
  { value: "paused", label: "暂停订阅" },
  { value: "completed", label: "完成订阅" },
];

/** 订阅状态的中文名，列表与详情弹窗共用同一份口径。 */
export function tgStatusLabel(status: TGSubscriptionStatus): string {
  switch (status) {
    case "active":
      return "订阅中";
    case "paused":
      return "暂停订阅";
    case "completed":
      return "完成订阅";
    default:
      return status;
  }
}


export interface TGQualityConfig {
  prefer_resolution: string[];
  prefer_codec: string[];
  prefer_source: string[];
  exclude_keywords: string[];
  min_resolution: string;
  max_size_gb: number;
  weights: Record<string, number>;
}

export interface TGQualityProfile {
  id: number;
  name: string;
  is_default: boolean;
  config: TGQualityConfig;
}

export interface TGMatchRecord {
  id: number;
  channel_id: number;
  chat_title: string;
  message_id: number;
  message_date?: string;
  raw_name: string;
  name_source: string;
  /** 资源类型：magnet / ed2k / share_115 / share_quark / http。 */
  resource_kind: string;
  /** 资源类型的中文名，直接显示用。 */
  kind_label: string;
  /** 资源的原始链接。字段名是历史遗留 —— ed2k 与分享链也存在这里，
   *  显示时请用 kind_label 而不是硬编码「磁力链接」。 */
  magnet: string;
  magnet_hash: string;
  size_bytes: number;
  parsed_title: string;
  parsed_year: number;
  /** -1 表示未识别 —— 0 是合法季号（特别篇），不能用 0 表达缺失。 */
  season: number;
  episode: number;
  episode_end: number;
  is_batch: boolean;
  resolution: string;
  video_codec: string;
  source_tag: string;
  subscription_id: number;
  subscription_title?: string;
  match_score: number;
  quality_score: number;
  status: TGRecordStatus;
  status_label: string;
  reason: string;
  offline_task_id: string;
  account_id: number;
  provider_kind: string;
  retry_count: number;
  next_retry_at?: string;
  created_at?: string;
}

/** TG 订阅的抓取配置。代理不在这里 —— 它是「系统设置 → 其他设置」里的全局项。 */
export interface TGConfig {
  enabled: boolean;
  auto_push: boolean;
  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_quality_profile_id: number;
  collect_window_min: number;
  max_push_per_hour: number;
  /** 基准抓取间隔（秒）。实际间隔还会按频道优先级与频道数放大。 */
  poll_interval_sec: number;
  /** 新频道首次订阅时往回翻的页数，每页 20 条。0 表示只追新。 */
  backfill_pages: number;
  /** 实际生效的每频道间隔 —— 频道数 × 请求间隔 会抬高基准，这个值让用户看得见真实节奏。 */
  effective_interval_sec: number;
  channel_count: number;
  status: string;
  status_message: string;
  last_poll_at: string;
}

export interface TGConfigInput {
  enabled: boolean;
  auto_push: boolean;
  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_quality_profile_id: number;
  collect_window_min: number;
  max_push_per_hour: number;
  poll_interval_sec: number;
  backfill_pages: number;
}

/** 体检报告里的一行：某种资源类型在最近一页帖子里命中了多少。 */
export interface TGProbeResourceCount {
  kind: string;
  /** 类型的中文名，直接显示。 */
  label: string;
  count: number;
  /**
   * 当前版本**在类型层面**能不能把它投递出去。
   * 「抓到了但投不了」要能和「压根抓不到」分开显示。
   *
   * 注意它是按频道算的，没有账号上下文：115 分享属于「类型支持、但要目标账号配了
   * 网页 Cookie 才真的能转存」，那种前提由 warnings 用文字交代，不在这里下结论。
   */
  deliverable: boolean;
}

/** 频道校验结果。网页预览模式不再有 bot 相关字段。 */
export interface TGChannelProbe {
  chat_id: string;
  username: string;
  title: string;
  type: string;
  post_count: number;
  latest_message_id: number;
  latest_post_at?: string;
  url_button_count: number;
  bare_button_count: number;
  /** 最近一页帖子里各类资源的命中情况（固定顺序，含命中 0 的类型）。 */
  resource_counts?: TGProbeResourceCount[];
  /** 正文链接里指向第三方站点的域名（最多 3 个）——非空且没抽到资源时，链接都在中转站上。 */
  external_hosts?: string[];
  /** 指向 @xxx_bot 的按钮/深链数，点进去私聊机器人才拿得到下载地址。 */
  bot_deep_links: number;
  /** 抽到了资源、但发布名不像影视资源因而不会落库的帖子数。 */
  discarded_posts: number;
  warnings?: string[];
}

/** 把体检的资源计数拼成一行可读文案，如「磁力 0 · ed2k 1 · 115 分享 15」。 */
export function formatProbeCounts(counts?: TGProbeResourceCount[]): string {
  if (!counts?.length) return "";
  return counts.map((c) => `${c.label} ${c.count}`).join(" · ");
}

/** 体检里「识别到了但投不出去」的那部分计数。 */
export function undeliverableProbeCounts(counts?: TGProbeResourceCount[]): TGProbeResourceCount[] {
  return (counts ?? []).filter((c) => c.count > 0 && !c.deliverable);
}

/** 一次历史搜索里某个频道的命中情况。 */
export interface TGChannelSearchHit {
  channel_id: number;
  name: string;
  matched: number;
}

/**
 * 历史搜索的结果。
 *
 * 「搜的是历史帖」这件事决定了它的产物：只落库、不推送，命中在匹配历史里
 * 以「待确认」出现，由用户确认后手动推。
 */
export interface TGHistorySearchResult {
  subscription_id: number;
  title: string;
  /** 实际用来搜的关键词（主标题 + 原名 + 别名，最多 3 个）。 */
  keywords: string[];
  channel_count: number;
  /** 计划发出的请求数（频道数 × 关键词数）—— 让用户对耗时与限流风险有预期。 */
  request_count: number;
  /** 实际失败的请求数（限流、超时等）。 */
  failed_requests: number;
  posts_scanned: number;
  /** 这次新落库的记录数。 */
  hit_records: number;
  channels?: TGChannelSearchHit[];
  /** 没有公开用户名、无法搜索的频道。 */
  skipped_channels?: string[];
  message: string;
}

export interface TGStats {
  status: {
    enabled: boolean;
    auto_push: boolean;
    connected: boolean;
    status: string;
    status_message: string;
    channel_count: number;
    subscription_count: number;
    poll_interval_sec: number;
    backfill_pages: number;
    last_poll_at: string;
  };
  records: Record<string, number>;
}

/** 试跑结果：把整条决策链摊开给用户看。 */
export interface TGQualityPreview {
  parsed: {
    raw: string;
    title_candidates: string[] | null;
    year: number | null;
    season: number | null;
    episode: number | null;
    episode_end: number | null;
    is_batch: boolean;
    resolution: string;
    video_codec: string;
    source: string;
    audio_codec: string;
    release_group: string;
    looks_like: boolean;
  };
  quality: { passed: boolean; score: number; reason: string };
  quality_profile_id: number;
  candidates: {
    subscription_id: number;
    title: string;
    year: number;
    media_type: TGMediaType;
    match_score: number;
    accepted: boolean;
    reason: string;
    title_hit: string;
    title_source: string;
  }[];
  unmatched_hint: string;
  thresholds: { accept: number; ambiguous: number };
}

export interface TGDiscoverPayload {
  page: number;
  total_pages: number;
  total_results: number;
  results: TGTMDBSearchResult[];
}
