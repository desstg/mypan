import { http } from "./client";

export type SettingType = "string" | "int" | "bool" | "select";

export interface SettingOption {
  value: string;
  label: string;
}

export interface SettingItem {
  key: string;
  type: SettingType;
  category: string;
  label: string;
  description?: string;
  value: string;
  default: string;
  is_default: boolean;
  unit?: string;
  min?: number;
  max?: number;
  options?: SettingOption[];
  sensitive?: boolean;
}

export interface SettingCategory {
  id: string;
  label: string;
}

export interface SettingsPayload {
  categories: SettingCategory[];
  items: SettingItem[];
}

export function fetchSettings() {
  return http.get<SettingsPayload>("/admin/settings");
}

// 仅提交改动过的键值（字符串形式），后端按类型校验并返回最新快照。
export function saveSettings(values: Record<string, string>) {
  return http.put<SettingsPayload>("/admin/settings", values);
}

/** 一个探测目标的结论。status 为 0 表示压根没收到响应（连接失败/超时）。 */
export interface ProxyProbeTarget {
  ok: boolean;
  status: number;
  elapsed_ms: number;
}

/** 代理连通性探测的完整结论。 */
export interface ProxyProbeResult {
  ok: boolean;
  /** 这次测的是走代理还是直连。 */
  enabled: boolean;
  api: ProxyProbeTarget;
  image: ProxyProbeTarget;
  /** 实际生效的代理地址（账号密码已脱敏）。 */
  proxy_url: string;
  /** 一句给人看的话：成功说「可用」，失败指明卡在哪一环。 */
  notice: string;
}

/**
 * 测代理能不能用。**不写入任何设置** —— 传进来的只是「覆盖值」，
 * 用来测表单里还没保存的草稿：**测通了再保存**。
 *
 * 字段全部可选，不传就用库里存的值。密码留空表示「不修改」，
 * 后端会回落到库里存的那份（前端拿不到明文）。
 */
export function testProxySettings(payload?: {
  enabled?: boolean;
  proxy_url?: string;
  proxy_username?: string;
  proxy_password?: string;
}) {
  return http.post<ProxyProbeResult>("/admin/settings/test-proxy", payload ?? {});
}
