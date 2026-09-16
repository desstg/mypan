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
  | "ignored";

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

export interface TGConfig {
  enabled: boolean;
  token_set: boolean;
  api_host: string;
  proxy_enabled: boolean;
  proxy_url: string;
  proxy_username: string;
  proxy_password_set: boolean;
  auto_push: boolean;
  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_quality_profile_id: number;
  collect_window_min: number;
  max_push_per_hour: number;
  status: string;
  status_message: string;
  bot_name: string;
}

export interface TGConfigInput {
  enabled: boolean;
  token: string;
  api_host: string;
  proxy_enabled: boolean;
  proxy_url: string;
  proxy_username: string;
  proxy_password: string;
  auto_push: boolean;
  default_account_id: number;
  default_parent_id: string;
  default_display_path: string;
  default_quality_profile_id: number;
  collect_window_min: number;
  max_push_per_hour: number;
}

export interface TGStats {
  status: {
    enabled: boolean;
    auto_push: boolean;
    token_set: boolean;
    bot_name: string;
    connected: boolean;
    status: string;
    status_message: string;
    offset: number;
    channel_count: number;
    subscription_count: number;
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
