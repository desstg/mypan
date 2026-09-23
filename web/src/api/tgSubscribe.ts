import { http } from "./client";
import type {
  TGChannel,
  TGChannelProbe,
  TGConfig,
  TGConfigInput,
  TGDiscoverPayload,
  TGHistorySearchResult,
  TGWebSearchResult,
  TGQualityConfig,
  TGRecommendedChannel,
  TGQualityPreview,
  TGQualityProfile,
  TGMatchRecord,
  TGMediaType,
  TGStats,
  TGSubscription,
  TGSubscriptionEpisode,
  TGSubscriptionStatus,
  TGTMDBSearchResult,
} from "@/types/tg-subscribe";

const BASE = "/tg-subscribe";

// ——————————————————— 配置 ———————————————————

export function fetchTGConfig() {
  return http.get<TGConfig>(`${BASE}/config`);
}

export function saveTGConfig(input: TGConfigInput) {
  return http.put<TGConfig>(`${BASE}/config`, input);
}

/** 探活：抓一次 t.me 的公开预览，验证网络、代理与页面结构。 */
export function testTGConnection() {
  return http.post<{ detail: string }>(`${BASE}/config/test`);
}

// ——————————————————— 热门推荐 ———————————————————

export interface TGDiscoverQuery {
  type: TGMediaType;
  page?: number;
  country?: string;
  genres?: string;
  year?: string;
  sort?: string;
}

export function fetchTGDiscover(query: TGDiscoverQuery) {
  const params: Record<string, string> = { type: query.type };
  if (query.page) params.page = String(query.page);
  if (query.country) params.country = query.country;
  if (query.genres) params.genres = query.genres;
  if (query.year) params.year = query.year;
  if (query.sort) params.sort = query.sort;
  return http.get<TGDiscoverPayload>(`${BASE}/tmdb/discover`, params);
}

export function searchTGTMDB(query: { q: string; year?: string; type?: string }) {
  const params: Record<string, string> = { q: query.q };
  if (query.year) params.year = query.year;
  if (query.type) params.type = query.type;
  return http.get<TGTMDBSearchResult[]>(`${BASE}/tmdb/search`, params);
}

export function fetchTGGenres(type: TGMediaType) {
  return http.get<{ genres: { id: number; name: string }[] }>(`${BASE}/tmdb/genres`, { type });
}

export function fetchTGDetail(id: string | number, type: TGMediaType) {
  return http.get<TGTMDBSearchResult>(`${BASE}/tmdb/detail`, { id: String(id), type });
}

/** 海报一律走后端代理 —— image.tmdb.org 在国内浏览器经常打不开。 */
export function tgPosterURL(posterPath: string | undefined, size = "w300") {
  if (!posterPath) return "";
  return `/api${BASE}/poster?path=${encodeURIComponent(posterPath)}&size=${encodeURIComponent(size)}`;
}

// ——————————————————— 频道 ———————————————————

export interface TGChannelInput {
  chat: string;
  remark: string;
  level: number;
  enabled: boolean;
}

export function fetchTGChannels() {
  return http.get<TGChannel[]>(`${BASE}/channels`);
}

/**
 * 内置推荐频道（附「已添加」标记）。
 *
 * 首次启动时它们已经自动入库成普通频道，所以这里通常返回空。
 * 还有内容只剩一种情况：当时没加成功（多半是还没配代理），用户后来配好了手动补加。
 */
export function fetchRecommendedTGChannels() {
  return http.get<TGRecommendedChannel[]>(`${BASE}/channels/recommended`);
}

export function createTGChannel(input: TGChannelInput) {
  return http.post<TGChannel>(`${BASE}/channels`, input);
}

export function updateTGChannel(id: number, input: TGChannelInput) {
  return http.put<TGChannel>(`${BASE}/channels/${id}`, input);
}

export function deleteTGChannel(id: number) {
  return http.del<null>(`${BASE}/channels/${id}`);
}

export function testTGChannel(id: number) {
  return http.post<TGChannelProbe>(`${BASE}/channels/${id}/test`);
}

// ——————————————————— 订阅 ———————————————————

export interface TGSubscriptionInput {
  tmdb_id: string;
  media_type: TGMediaType;
  title: string;
  original_title: string;
  year: number;
  poster_path: string;
  overview: string;
  quality_profile_id: number;
  target_account_id: number;
  target_parent_id: string;
  target_display_path: string;
  push_provider: string;
  collect_window_min: number;
  upgrade_enabled: boolean;
}

export function fetchTGSubscriptions(status?: TGSubscriptionStatus | "") {
  return http.get<TGSubscription[]>(`${BASE}/subscriptions`, status ? { status } : undefined);
}

/**
 * 批量改状态（「已订阅」页的批量操作）。
 *
 * 没有单条接口那样的失败明细：后端一条 UPDATE 要么全成要么全不成，
 * 返回的 updated 是要显示给用户的条数。
 */
export function batchSetTGSubscriptionStatus(ids: number[], status: TGSubscriptionStatus) {
  return http.post<{ updated: number }>(`${BASE}/subscriptions/status`, { ids, status });
}

export function fetchTGSubscription(id: number) {
  return http.get<TGSubscription>(`${BASE}/subscriptions/${id}`);
}

/** 海报墙用它标注「已订阅」。未订阅时后端返回 null。 */
export function fetchTGSubscriptionByTMDB(tmdbId: string | number, type: TGMediaType) {
  return http.get<TGSubscription | null>(`${BASE}/subscriptions/by-tmdb`, {
    tmdb_id: String(tmdbId),
    type,
  });
}

export function createTGSubscription(input: TGSubscriptionInput) {
  return http.post<TGSubscription>(`${BASE}/subscriptions`, input);
}

export function updateTGSubscription(id: number, input: TGSubscriptionInput) {
  return http.put<TGSubscription>(`${BASE}/subscriptions/${id}`, input);
}

export function deleteTGSubscription(id: number) {
  return http.del<null>(`${BASE}/subscriptions/${id}`);
}

export function setTGSubscriptionStatus(id: number, status: TGSubscriptionStatus) {
  return http.post<null>(`${BASE}/subscriptions/${id}/status`, { status });
}

export function resetTGSubscription(id: number) {
  return http.post<{ removed: number }>(`${BASE}/subscriptions/${id}/reset`);
}

export function fetchTGSubscriptionEpisodes(id: number) {
  return http.get<TGSubscriptionEpisode[]>(`${BASE}/subscriptions/${id}/episodes`);
}

// ——————————————————— 画质方案 ———————————————————

export interface TGQualityProfileInput {
  name: string;
  config: TGQualityConfig;
  is_default: boolean;
}

export function fetchTGQualityProfiles() {
  return http.get<TGQualityProfile[]>(`${BASE}/quality-profiles`);
}

export function createTGQualityProfile(input: TGQualityProfileInput) {
  return http.post<{ id: number }>(`${BASE}/quality-profiles`, input);
}

export function updateTGQualityProfile(id: number, input: TGQualityProfileInput) {
  return http.put<null>(`${BASE}/quality-profiles/${id}`, input);
}

export function deleteTGQualityProfile(id: number) {
  return http.del<null>(`${BASE}/quality-profiles/${id}`);
}

/** 匹配算法的调试器：粘一段发布名，看解析、画质判定与候选得分。 */
export function previewTGQuality(rawName: string, qualityProfileId = 0) {
  return http.post<TGQualityPreview>(`${BASE}/quality-preview`, {
    raw_name: rawName,
    quality_profile_id: qualityProfileId,
  });
}

// ——————————————————— 匹配历史 ———————————————————

export interface TGRecordQuery {
  status?: string;
  subscription_id?: number;
  channel_id?: number;
  keyword?: string;
  limit?: number;
  offset?: number;
}

export function fetchTGRecords(query: TGRecordQuery = {}) {
  const params: Record<string, string> = {};
  if (query.status) params.status = query.status;
  if (query.subscription_id) params.subscription_id = String(query.subscription_id);
  if (query.channel_id) params.channel_id = String(query.channel_id);
  if (query.keyword) params.keyword = query.keyword;
  if (query.limit) params.limit = String(query.limit);
  if (query.offset) params.offset = String(query.offset);
  return http.get<{ items: TGMatchRecord[]; total: number }>(`${BASE}/records`, params);
}

export function fetchTGRecord(id: number) {
  return http.get<TGMatchRecord>(`${BASE}/records/${id}`);
}

/** 手动推送：待确认 / 未匹配 / 推送失败的兜底入口。 */
export function pushTGRecord(id: number, subscriptionId = 0) {
  return http.post<TGMatchRecord>(`${BASE}/records/${id}/push`, { subscription_id: subscriptionId });
}

export function ignoreTGRecord(id: number) {
  return http.post<null>(`${BASE}/records/${id}/ignore`);
}

export function clearTGRecords(before?: string) {
  return http.del<{ removed: number }>(`${BASE}/records`, undefined, before ? { before } : undefined);
}

// ——————————————————— 状态 ———————————————————

export function fetchTGStats() {
  return http.get<TGStats>(`${BASE}/stats`);
}

/** 探测某个网盘账号推送磁力时会走原生离线还是内置下载器。 */
export function fetchTGProviderSummary(accountId: number) {
  return http.get<{ provider: string }>(`${BASE}/provider-summary`, {
    account_id: String(accountId),
  });
}

/**
 * 搜索这条订阅在频道里的历史帖。
 *
 * 补的是增量抓取的盲区：订阅之前就发过的帖子永远抓不到（首次订阅只回填 1 页）。
 * 只落库、不推送，命中会以「待确认」出现在匹配历史里，由用户确认后手动推。
 */
export function searchTGHistory(subscriptionId: number) {
  return http.post<TGHistorySearchResult>(`${BASE}/subscriptions/${subscriptionId}/search`, {});
}

/**
 * 拿订阅片名去外部网盘搜索引擎搜（默认只搜磁力）。
 *
 * 「搜历史帖」的补充：那条只在**你已经订阅的频道**里翻，频道少的时候覆盖不到；
 * 这条不依赖频道数。同样只落库、不推送，命中以「待确认」进匹配历史。
 *
 * 需要在设置里先打开「网盘搜索」，否则后端直接报错。
 */
export function searchTGWeb(subscriptionId: number) {
  return http.post<TGWebSearchResult>(`${BASE}/subscriptions/${subscriptionId}/search-web`, {});
}
