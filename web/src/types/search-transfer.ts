/** 「搜索转存」页的类型。字段与后端 internal/searchtransfer + internal/pansou 一一对应。 */

/** 盘搜的一条结果（pansou.Hit 的线格式）。 */
export interface SearchHit {
  url: string;
  password?: string;
  /** 结果标题，来自搜索服务的 note 字段。 */
  note: string;
  datetime?: string;
  /** 上游来源：`tg:<频道>` 或 `plugin:<插件>`。 */
  source?: string;
  /** 上游给的缩略图，多数条目没有。 */
  images?: string[];
}

export interface SearchResult {
  keyword: string;
  total: number;
  /** 按网盘类型分组，key 是盘搜的类型名（magnet / 115 / quark / …）。 */
  merged_by_type: Record<string, SearchHit[]>;
  /** 这次真正打过去的地址，用来回显「打的是内置还是自建」。 */
  base_url: string;
}

export interface SearchSite {
  base_url: string;
  /** false = 用的是内置/默认地址，不是用户填的。 */
  configured: boolean;
  ready: boolean;
  reason?: string;
  plugins?: string[];
  channels?: string[];
}

/** 一个 scheme 在所选账号上的可用性。 */
export interface SchemeCapability {
  /** native = 网盘原生离线；builtin = 内置下载器；空 = 都不支持。 */
  provider: "" | "native" | "builtin";
  ok: boolean;
  reason?: string;
}

export interface SaveCapabilities {
  account_id: number;
  /**
   * 这个账号对应的**盘搜网盘类型**（115 / 123 / quark…），没有对应值时是空串。
   *
   * 搜索页拿它限制搜索范围：选了 123 账号就只要 123 的结果，否则搜出来的
   * 全是自己存不进去的链接，一屏按钮都是灰的。映射表在后端（驱动类型的定义在那边）。
   */
  pansou_cloud_type: string;
  schemes: Record<string, SchemeCapability>;
  /** 各家分享链在这个账号上能不能转存（115 要网页 Cookie、123 要网页账号密码、夸克要 Cookie、光鸭用登录令牌）。 */
  share_115: SchemeCapability;
  share_123: SchemeCapability;
  share_quark: SchemeCapability;
  share_guangya: SchemeCapability;
  /** 驱动**类型上**支不支持该种分享（与 ok 的区别：后者还要看账号当下的配置）。 */
  share_115_driver: boolean;
  share_123_driver: boolean;
  share_quark_driver: boolean;
  share_guangya_driver: boolean;
}

export interface SaveShareResult {
  target_path: string;
  count: number;
  file_ids?: string[];
}

/** 转存记录的一条。搜索页与转存页写进同一张表。 */
export interface TransferRecord {
  id: number;
  /** 资源类型：magnet / ed2k / http / ftp / thunder / share_115 / share_123 / share_quark。 */
  kind: string;
  link: string;
  password: string;
  /** 分享标题 / 资源名（盘搜的 note）。离线下载那条路只有它。 */
  share_name: string;
  file_id: string;
  /** 网盘落盘后的文件名，离线任务跑完才有。 */
  file_name: string;
  /** pending / success / failed / rapid_success / rapid_failed */
  status: string;
  note: string;
  /** search = 搜索页；manual = 转存页手工粘贴。 */
  source: string;
  account_id: number;
  account_name: string;
  target_display_path: string;
  created_at: number;
  updated_at: number;
}

export interface TransferRecordList {
  items: TransferRecord[];
  total: number;
}

/** 转存 tab 批量提交的结果清单。 */
export interface SaveLinkItem {
  url: string;
  kind: string;
  ok: boolean;
  message: string;
  /** 实际走的通道：native（网盘离线）/ builtin（内置下载器）/ share（分享转存）。 */
  provider?: string;
}

export interface SaveLinksResult {
  accepted: number;
  failed: number;
  items: SaveLinkItem[];
}
