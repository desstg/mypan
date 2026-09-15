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
