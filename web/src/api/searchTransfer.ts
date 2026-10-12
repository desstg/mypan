import { http } from "./client";
import type {
  SaveCapabilities,
  SaveLinksResult,
  SaveShareResult,
  SearchResult,
  SearchSite,
  TransferRecordList,
} from "@/types/search-transfer";

const BASE = "/search-transfer";

export interface SearchTransferSearchPayload {
  keyword: string;
  /** 按次传的网盘类型，不落库。留空则用后端默认（一整套宽类型）。 */
  cloud_types?: string[];
  plugins?: string[];
  channels?: string[];
  /** 留空则用设置里那一份（与 TG 订阅的网盘搜索同一个键）。 */
  base_url?: string;
  token?: string;
}

/** 一条待转存的链接（转存 tab 的一行）。 */
export interface SaveLinkItemPayload {
  url: string;
  password?: string;
  /** 搜索页带过来的标题，只用于转存记录留档。 */
  share_name?: string;
}

export const searchTransferApi = {
  /** 探活盘搜站点。失败也是 200，看 ready/reason。 */
  site(baseUrl?: string) {
    return http.get<SearchSite>(`${BASE}/site`, baseUrl ? { base_url: baseUrl } : undefined);
  },

  search(payload: SearchTransferSearchPayload) {
    return http.post<SearchResult>(`${BASE}/search`, payload);
  },

  /** 所选账号能收哪些 scheme（界面据此置灰）。 */
  saveCapabilities(accountId: number) {
    return http.get<SaveCapabilities>(`${BASE}/save-capabilities`, { account_id: accountId });
  },

  /** 115 分享链的单条转存（搜索页结果行上的按钮）。 */
  saveShare(payload: {
    account_id: number;
    share_url: string;
    password?: string;
    share_name?: string;
    target_parent_id: string;
    target_display_path: string;
  }) {
    return http.post<SaveShareResult>(`${BASE}/save-share`, payload);
  },

  /** 「转存」tab 的批量提交：服务端按行判类型分派并逐条落记录。 */
  saveLinks(payload: {
    account_id: number;
    items: SaveLinkItemPayload[];
    target_parent_id: string;
    target_display_path: string;
    provider_kind?: string;
    /** 这一批从哪一页发起（search / manual），进转存记录。 */
    source?: "search" | "manual";
  }) {
    return http.post<SaveLinksResult>(`${BASE}/save-links`, payload);
  },

  /** 转存记录列表（搜索页与转存页共用）。 */
  records(params: { page?: number; page_size?: number; kind?: string; status?: string } = {}) {
    return http.get<TransferRecordList>(`${BASE}/records`, params);
  },

  /** 删记录。**不动网盘里的文件**。 */
  deleteRecords(ids: number[]) {
    return http.post<{ deleted: number }>(`${BASE}/records/delete`, { ids });
  },
};
