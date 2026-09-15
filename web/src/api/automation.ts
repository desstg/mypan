import { http } from "./client";

export type AutomationTriggerType = "daily" | "interval" | "webhook" | "offline_download";
export type AutomationStatus = "running" | "paused";
export type AutomationCondition = "always" | "prev_success" | "prev_failed";
export type AutomationActionType = "cache_clear" | "organize" | "strm" | "strm_scrape" | "delay" | "emby_refresh" | "emby_complete_media_info";

export type EmbyRefreshMode = "global" | "library";

export interface AutomationAction {
  id: string;
  type: AutomationActionType;
  name: string;
  condition: AutomationCondition;
  params: Record<string, unknown>;
}

export interface AutomationRuleInput {
  name: string;
  trigger_type: AutomationTriggerType;
  trigger_config: Record<string, unknown>;
  actions: AutomationAction[];
  status: AutomationStatus;
}

export interface AutomationRule extends AutomationRuleInput {
  id: number;
  next_run_at?: string;
  last_run_at?: string;
  last_run_status?: string;
  last_run_message?: string;
  is_running?: boolean;
  running_step?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
}

export interface AutomationRun {
  id: number;
  rule_id: number;
  trigger_source: string;
  status: string;
  message: string;
  result: Record<string, unknown>;
  started_at?: string;
  finished_at?: string;
  created_at?: string;
}

export interface AutomationValidationIssue {
  level: string;
  message: string;
  action_index?: number;
  action_type?: string;
}

export interface AutomationValidationResult {
  ok: boolean;
  issues: AutomationValidationIssue[];
}

export interface AutomationOptionItem {
  id: string | number;
  name: string;
  account_id?: number;
  path?: string;
  schedule_mode?: string;
  branch_check_enabled?: boolean;
}

export interface AutomationOptions {
  strm_tasks: AutomationOptionItem[];
  organize_tasks: AutomationOptionItem[];
  emby_configs: Array<{
    id: string;
    name: string;
    emby_url: string;
  }>;
}

export interface AutomationTriggerConfig {
  time: string;
  start_time: string;
  interval_hours: number;
  event: string;
  source: string;
  path_prefix: string;
  require_path: boolean;
  account_id: number;
  account_name: string;
  parent_id: string;
  path: string;
}

export function normalizeAutomationTriggerConfig(
  config: Record<string, unknown> = {},
): AutomationTriggerConfig {
  return {
    time: String(config.time ?? ""),
    start_time: String(config.start_time ?? ""),
    interval_hours: Number(config.interval_hours || 72),
    event: String(config.event ?? ""),
    source: String(config.source ?? ""),
    path_prefix: String(config.path_prefix ?? ""),
    require_path: config.require_path === true,
    account_id: Number(config.account_id || 0),
    account_name: String(config.account_name ?? ""),
    parent_id: String(config.parent_id ?? ""),
    path: String(config.path ?? ""),
  };
}

export function serializeAutomationTriggerConfig(
  config: AutomationTriggerConfig,
): Record<string, unknown> {
  return {
    time: config.time || "",
    start_time: config.start_time || "",
    interval_hours: Number(config.interval_hours || 72),
    event: String(config.event || "").trim(),
    source: String(config.source || "").trim(),
    path_prefix: String(config.path_prefix || "").trim(),
    require_path: config.require_path === true,
    account_id: Number(config.account_id || 0),
    account_name: String(config.account_name || "").trim(),
    parent_id: String(config.parent_id || "").trim(),
    path: String(config.path || "").trim(),
  };
}

export function fetchAutomationRules() {
  return http.get<AutomationRule[]>("/admin/automation/rules");
}

export function createAutomationRule(body: AutomationRuleInput) {
  return http.post<AutomationRule>("/admin/automation/rules", body);
}

export function updateAutomationRule(id: number, body: AutomationRuleInput) {
  return http.put<AutomationRule>(`/admin/automation/rules/${id}`, body);
}

export function deleteAutomationRule(id: number) {
  return http.del<{ id: number }>(`/admin/automation/rules/${id}`);
}

export function toggleAutomationRule(id: number) {
  return http.post<AutomationRule>(`/admin/automation/rules/${id}/toggle`, {});
}

export function runAutomationRule(id: number) {
  return http.post<{ submitted: boolean; rule_id: number; trigger_source: string }>(`/admin/automation/rules/${id}/run`, {});
}

export function validateAutomationRule(actions: AutomationAction[]) {
  return http.post<AutomationValidationResult>("/admin/automation/validate", { actions });
}

export function fetchAutomationRuns(ruleId?: number, limit = 20) {
  const query: Record<string, string | number> = { limit };
  if (ruleId) query.rule_id = ruleId;
  return http.get<AutomationRun[]>("/admin/automation/runs", query);
}

export function clearAutomationRuns() {
  return http.post<{ deleted: number }>("/admin/automation/runs/clear", {});
}

export function fetchAutomationOptions() {
  return http.get<AutomationOptions>("/admin/automation/options");
}
