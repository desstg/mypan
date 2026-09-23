import { http } from "./client";
import type {
  JavCheckResult,
  JavConfig,
  JavConfigInput,
  JavConnectionResult,
  JavListResult,
  JavMovieCard,
  JavMovieDetail,
  JavPushRecord,
  JavPushRecordFilter,
  JavPushResult,
  JavRankingKind,
  JavRankingResult,
  JavFollowedUser,
  JavRelatedList,
  JavReview,
  JavUserSharesResult,
  JavRun,
  JavSearchResult,
  JavServer,
  JavSubscription,
  JavSubscriptionInput,
  JavSubMovie,
  JavSyncStatus,
  JavTestConnectionResult,
  JavBlacklistEntry,
  JavCandidate,
  JavCompletedMovie,
} from "@/types/jav";

const BASE = "/jav";

/** 把可选参数拼成查询串，空值一律不拼 —— 拼一个 `&type=` 出去会让后端多做一次判断。 */
function q(params: Record<string, string | number | undefined | null>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  const text = search.toString();
  return text ? `?${text}` : "";
}

// ————————————————————— 配置 —————————————————————

export function fetchJavConfig() {
  return http.get<JavConfig>(`${BASE}/config`);
}

export function saveJavConfig(input: JavConfigInput) {
  return http.put<JavConfig>(`${BASE}/config`, input);
}

export function loginJav(username: string, password: string) {
  return http.post<{ ok: boolean; masked: string }>(`${BASE}/config/login`, {
    username,
    password,
  });
}

export function testJavConnection() {
  return http.post<JavTestConnectionResult>(`${BASE}/config/test-connection`, {});
}

export function testJavNodes() {
  return http.post<{ results: JavConnectionResult[] }>(`${BASE}/config/nodes/test`, {});
}

// ————————————————————— 榜单 / 搜索 / 影库 —————————————————————

export function fetchJavRanking(
  kind: JavRankingKind,
  options: { type?: string; page?: number } = {},
) {
  const path =
    kind === "actor"
      ? "actors"
      : kind === "top250"
        ? "top"
        : "hot";
  const params: Record<string, string | number | undefined> = { page: options.page };
  if (kind === "actor" || kind === "top250") params.type = options.type;
  if (kind === "daily" || kind === "weekly" || kind === "monthly") {
    params.period = kind;
  }
  return http.get<JavRankingResult>(
    `${BASE}/rankings/${path}${q(params)}`,
  );
}

export function searchJav(params: {
  keyword: string;
  type: string;
  filter?: string;
  year?: string;
  sort?: string;
  dir?: string;
  page?: number;
}) {
  return http.get<JavSearchResult>(
    `${BASE}/search${q({
      q: params.keyword,
      type: params.type,
      filter: params.filter,
      year: params.year,
      sort: params.sort,
      dir: params.dir,
      page: params.page,
    })}`,
  );
}

export function fetchJavLocalMovies(params: {
  keyword?: string;
  type?: string;
  year?: string;
  tag?: string;
  sort?: string;
  dir?: string;
  page?: number;
  page_size?: number;
}) {
  return http.get<JavListResult>(`${BASE}/movies${q(params)}`);
}

export function fetchJavMovie(id: string, refresh = false) {
  return http.get<JavMovieDetail>(
    `${BASE}/movies/${encodeURIComponent(id)}${q({ refresh: refresh ? 1 : undefined })}`,
  );
}

export function ingestJavMovie(id: string) {
  return http.post<JavMovieCard>(`${BASE}/movies/${encodeURIComponent(id)}/ingest`, {});
}

/**
 * 现取一个**新鲜**的预览片播放地址。
 *
 * 不能拿详情里那份 `preview_video_url` 直接播：它是上游签发的**限时**地址
 * （带 sign / t 签名），实测十几个小时之后上游就回 `ExpiredSignature`，
 * 而库里那份可能是几天前抓的 —— 拿它去播就是一帧不出的黑屏。
 */
export async function fetchJavPreviewURL(id: string) {
  const res = await http.get<{ url: string }>(`${BASE}/movies/${encodeURIComponent(id)}/preview-url`);
  return res?.url ?? "";
}

export function fetchJavMagnets(id: string, refresh = false) {
  return http.get<{ items: JavMovieDetail["magnets"] }>(
    `${BASE}/movies/${encodeURIComponent(id)}/magnets${q({ refresh: refresh ? 1 : undefined })}`,
  );
}

export function fetchJavReviews(id: string, page = 1) {
  return http.get<{ items: JavReview[]; total: number }>(
    `${BASE}/movies/${encodeURIComponent(id)}/reviews${q({ page })}`,
  );
}


export function fetchJavActorMovies(actorId: string) {
  return http.get<{ items: JavMovieCard[] }>(
    `${BASE}/actors/${encodeURIComponent(actorId)}/movies`,
  );
}

// ————————————————————— 订阅 —————————————————————

export function fetchJavSubscriptions(params: {
  status?: string;
  target_type?: string;
  keyword?: string;
  page?: number;
  page_size?: number;
} = {}) {
  return http.get<{ items: JavSubscription[]; total: number }>(
    `${BASE}/subscriptions${q(params)}`,
  );
}

/** 「已完成」那一档：推送成功过的影片。 */
/**
 * 「已完成」那一档：推送成功过的影片。
 *
 * sort 取 code / created / resource / done（番号 / 创建时间 / 资源时间 / 完成时间），
 * dir 传 "asc" 为正序，其余（含不传）为倒序。服务端按白名单认，认不出来就用默认排序。
 */
export function fetchJavCompletedMovies(
  params: { page?: number; page_size?: number; sort?: string; dir?: "asc" | "desc" } = {},
) {
  return http.get<{ items: JavCompletedMovie[]; total: number }>(
    `${BASE}/subscriptions/completed-movies${q(params)}`,
  );
}

export function createJavSubscription(input: JavSubscriptionInput) {
  return http.post<JavSubscription>(`${BASE}/subscriptions`, input);
}

export function updateJavSubscription(id: number, input: JavSubscriptionInput) {
  return http.put<JavSubscription>(`${BASE}/subscriptions/${id}`, input);
}

export function deleteJavSubscription(id: number) {
  return http.del<{ ok: boolean }>(`${BASE}/subscriptions/${id}`);
}

export function setJavSubscriptionStatus(id: number, status: string) {
  return http.post<{ ok: boolean }>(`${BASE}/subscriptions/${id}/status`, { status });
}

export function checkJavSubscription(id: number) {
  return http.post<JavCheckResult>(`${BASE}/subscriptions/${id}/check`, {});
}

export function fetchJavCandidates(
  id: number,
  params: {
    run_id?: number;
    movie_id?: string;
    matched?: number;
    push_ok?: number;
    untried?: number;
    limit?: number;
    offset?: number;
  } = {},
) {
  return http.get<{ items: JavCandidate[]; total: number }>(
    `${BASE}/subscriptions/${id}/candidates${q(params)}`,
  );
}

export function fetchJavRuns(id: number, limit = 20) {
  return http.get<{ items: JavRun[] }>(
    `${BASE}/subscriptions/${id}/runs${q({ limit })}`,
  );
}

/**
 * 手动推送：推详情页上的**某一颗磁链**，不依赖订阅。
 *
 * 目标是「番号相关设置」里的默认推送目标；记录落在推送记录表的 subscription_id=0
 * （那张表本来就把 0 定义成「手动推送」），所以下载记录页看得到，网盘下完之后
 * 状态也会照常回写 —— 如果这部片在某条订阅里，那条订阅里它的状态会变成已完成。
 */
/**
 * 手动推一颗链接到网盘。
 *
 * 字段叫 `uri` 而不是 `magnet`：详情页的磁链 tab 与「评论区分享」档都用它，
 * 后者还会出现 ed2k 链接（服务端按链接自己的 scheme 判通道能力）。
 */
export function pushJavMagnet(
  movieId: string,
  input: { uri: string; name?: string; size_text?: string },
) {
  return http.post<JavPushResult>(
    `${BASE}/movies/${encodeURIComponent(movieId)}/push-magnet`,
    input,
  );
}

/**
 * 取含这部影片的清单。
 *
 * 独立于详情：那一档是点开表头才加载的，打开详情不该顺带打一次上游。
 */
export function fetchJavRelatedLists(id: string) {
  return http.get<{ items: JavRelatedList[] }>(
    `${BASE}/movies/${encodeURIComponent(id)}/related-lists`,
  );
}

/**
 * 取某个清单里的影片（一页 40 部）。
 *
 * 走的是**官网清单页的 HTML 抓取** —— 上游 API 没有「按清单 id 取影片」的能力，
 * 按清单名去搜又是模糊匹配影片标题（搜「驾驶双马尾」会返回标题里含「双马尾」的
 * 无关片子）。所以这一档比别的接口慢：一次实打实的网页请求。
 */
export function fetchJavListMovies(id: string, page: number) {
  return http.get<{ items: JavMovieCard[]; total: number }>(
    `${BASE}/lists/${encodeURIComponent(id)}/movies?page=${page}`,
  );
}

/**
 * 取某位分享者分享过的影片（按影片聚合）。
 *
 * 是**本地聚合** —— 上游没有「按用户查他发过的评论」的接口（实测四个候选路径全是
 * 404）。所以它只覆盖评论已入库的影片，弹窗里会写明这一点。
 */
export function fetchJavUserShares(userId: number, page = 1) {
  return http.get<JavUserSharesResult>(`${BASE}/users/${userId}/shares${q({ page })}`);
}

/** 取关注过的分享者（订阅页「用户」那一档）。 */
export function fetchJavFollowedUsers() {
  return http.get<{ items: JavFollowedUser[] }>(`${BASE}/users/followed`);
}

/** 关注一个分享者。幂等。 */
export function followJavUser(userId: number, username: string) {
  return http.post<{ ok: boolean }>(`${BASE}/users/${userId}/follow`, { username });
}

/** 取关。幂等。 */
export function unfollowJavUser(userId: number) {
  return http.post<{ ok: boolean }>(`${BASE}/users/${userId}/unfollow`, {});
}

export function fetchJavSubscriptionMovies(id: number) {
  return http.get<{ items: JavSubMovie[] }>(`${BASE}/subscriptions/${id}/movies`);
}

export function setJavMovieSkip(id: number, movieId: string, skip: boolean) {
  const action = skip ? "skip" : "unskip";
  return http.post<{ ok: boolean }>(
    `${BASE}/subscriptions/${id}/movies/${encodeURIComponent(movieId)}/${action}`,
    {},
  );
}

export function autoPushJavSubscription(id: number, force = false) {
  return http.post<JavPushResult>(`${BASE}/subscriptions/${id}/auto-push`, { force });
}

export function subscribeJavMovie(id: number, movieId: string) {
  return http.post<JavPushResult>(
    `${BASE}/subscriptions/${id}/movies/${encodeURIComponent(movieId)}/subscribe`,
    {},
  );
}

export function pushJavCandidate(id: number, candidateId: number) {
  return http.post<JavPushResult>(
    `${BASE}/subscriptions/${id}/candidates/${candidateId}/push`,
    {},
  );
}

// ————————————————————— 黑名单 —————————————————————

export function fetchJavBlacklist(targetType = "") {
  return http.get<{ items: JavBlacklistEntry[] }>(
    `${BASE}/blacklist${q({ target_type: targetType })}`,
  );
}

export function addJavBlacklist(input: {
  target_type: string;
  target_id?: string;
  target_url?: string;
  target_name: string;
  reason?: string;
  /** 从哪条订阅点进来的。后端靠它算「加入这一刻符合条件的影片」快照。 */
  subscription_id?: number;
}) {
  return http.post<{ id: number }>(`${BASE}/blacklist`, input);
}

export function deleteJavBlacklist(id: number) {
  return http.del<{ ok: boolean }>(`${BASE}/blacklist/${id}`);
}

/**
 * 一条黑名单**加入时**记下来的影片快照。
 *
 * 读的是条目里存的那份历史记录，不是现算 —— 拉黑之后这些片全部变成不合格，
 * 现算只会是空集。
 */
export function fetchJavBlacklistMovies(id: number) {
  return http.get<{ items: JavMovieCard[] }>(`${BASE}/blacklist/${id}/movies`);
}

// ————————————————————— 推送记录 —————————————————————

export function fetchJavPushRecords(filter: JavPushRecordFilter = {}) {
  return http.get<{ items: JavPushRecord[]; total: number }>(
    `${BASE}/push-records${q({ ...filter })}`,
  );
}

export function fetchJavPushRecordDownloaders() {
  return http.get<{ items: string[] }>(`${BASE}/push-records/downloaders`);
}

export function repushJavRecord(id: number) {
  return http.post<JavPushResult>(`${BASE}/push-records/${id}/repush`, {});
}

export function setJavRecordStatus(id: number, status: string) {
  return http.post<{ ok: boolean }>(`${BASE}/push-records/${id}/status`, { status });
}

export function deleteJavRecord(id: number) {
  return http.del<{ ok: boolean }>(`${BASE}/push-records/${id}`);
}

export function deleteJavRecords(ids: number[]) {
  return http.post<{ deleted: number }>(`${BASE}/push-records/batch-delete`, { ids });
}

// ————————————————————— 媒体服务器与库同步 —————————————————————

export function fetchJavServers() {
  return http.get<{ items: JavServer[] }>(`${BASE}/servers`);
}

export function addJavServer(input: {
  name: string;
  url: string;
  api_key: string;
  type: string;
}) {
  return http.post<{ id: number }>(`${BASE}/servers`, input);
}

export function updateJavServer(id: number, input: Record<string, unknown>) {
  return http.put<{ ok: boolean }>(`${BASE}/servers/${id}`, input);
}

export function deleteJavServer(id: number) {
  return http.del<{ ok: boolean }>(`${BASE}/servers/${id}`);
}

export function testJavServer(id: number) {
  return http.post<JavConnectionResult>(`${BASE}/servers/${id}/test`, {});
}

export function syncJavServer(id: number) {
  return http.post<{
    server_id: number;
    name: string;
    ok: boolean;
    items: number;
    codes: number;
    message: string;
  }>(`${BASE}/servers/${id}/sync`, {});
}

export function syncJavAll() {
  return http.post<{ ok: boolean; message: string }>(`${BASE}/sync`, {});
}

export function fetchJavSyncStatus() {
  return http.get<JavSyncStatus>(`${BASE}/sync/status`);
}

export function fetchJavSyncSchedule() {
  return http.get<{
    enabled: boolean;
    cron: string;
    next_run_at: string;
    last_run_at: string;
    status: string;
    message: string;
  }>(`${BASE}/sync/schedule`);
}

export function saveJavSyncSchedule(enabled: boolean, cron: string) {
  return http.put<{ ok: boolean }>(`${BASE}/sync/schedule`, { enabled, cron });
}

export function fetchJavLibraryStats() {
  return http.get<import("@/types/jav").JavLibraryStats>(`${BASE}/library/stats`);
}

export function lookupJavLibrary(code: string) {
  return http.get<{
    items: Array<{
      server_id: number;
      server_name: string;
      item_id: string;
      code: string;
      title: string;
      path: string;
      resolution: number;
      has_quality: boolean;
      size_bytes: number;
      synced_at: string;
    }>;
  }>(`${BASE}/library/lookup${q({ code })}`);
}

export type { JavPushRecord, JavPushResult };

/**
 * 把上游封面地址转成本地图片代理地址。
 *
 * 必须走代理，不能让浏览器直接 src 上游 —— 两个原因（见 internal/jav/image.go）：
 *   1. JAVDB 的封面是 XOR 混淆的，直连显示出来是花屏；
 *   2. CDN 给的 Content-Type 是 binary/octet-stream，浏览器不当图片渲染。
 *
 * 空串原样返回，让 <img v-if> 能正常跳过。
 */
export function javImageURL(raw: string | undefined | null): string {
  const url = (raw ?? "").trim();
  if (!url) return "";
  // 已经是本地代理地址或 data: 的不重复包一层。
  if (url.startsWith("/api/") || url.startsWith("data:")) return url;
  return `/api/jav/image?url=${encodeURIComponent(url)}`;
}
