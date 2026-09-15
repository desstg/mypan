import { http } from "./client";

export interface MediaOrganizeTaskConfig {
  target_directory?: string;
  target_directory_id?: string;
  action_type?: string;
  target_root?: string;
  target_root_id?: string;
  media_type?: string;
  rename_marker?: string;
  use_tmdb?: boolean;
  /** 走番号匹配方案。与 use_tmdb 互斥，后端 NormalizeTaskConfig 会兜底。 */
  use_jav?: boolean;
  overwrite_existing?: boolean;
  recursive?: boolean;
  account_id?: string | number;
}

export interface MediaOrganizeTask {
  id: string;
  task_name: string;
  account_id: number;
  config: MediaOrganizeTaskConfig;
  status: string;
  last_run_at?: string;
  last_run_result?: MediaOrganizeRunResult;
  created_at?: string;
  updated_at?: string;
  is_running?: boolean;
}

export interface MediaOrganizeRunResult {
  total?: number;
  renamed?: number;
  moved?: number;
  skipped?: number;
  failed?: number;
  stopped?: boolean;
}

export interface MediaOrganizePlanAction {
  id: string;
  kind: string;
  source_id?: string;
  source_name?: string;
  source_parent_id?: string;
  target_parent_id?: string;
  target_name?: string;
  reason?: string;
  confidence?: number;
  metadata?: Record<string, unknown>;
  status?: string;
  error?: string;
}

export interface MediaOrganizePlan {
  task_id?: string;
  created_at?: string;
  /** "tmdb" | "jav"。番号计划零 tmdb_id，靠它避免「未匹配 TMDB」误报。 */
  scheme?: string;
  target_root_id?: string;
  target_parent_id?: string;
  actions?: MediaOrganizePlanAction[];
  skipped?: Array<Record<string, unknown>>;
  diagnostics?: Record<string, unknown>;
}

export interface MediaOrganizeProgress {
  stage?: string;
  scanned_dirs?: number;
  scanned_files?: number;
  groups?: number;
  actions?: number;
  skipped?: number;
  current_dir?: string;
  planned_works?: number;
  max_works?: number;
  quota_reached?: boolean;
  ai_total?: number;
  ai_completed?: number;
  ai_cached?: number;
  ai_failed?: number;
  ai_chunk?: number;
  ai_chunks?: number;
}

export interface MediaOrganizeLogEntry {
  time: string;
  message: string;
}

export interface MediaOrganizeSettings {
  proxy_enabled: boolean;
  proxy_url: string;
  proxy_username: string;
  proxy_password: string;
  tmdb_api_key: string;
  tmdb_language: string;
  tmdb_api_host: string;
  tmdb_image_host: string;
  api_request_interval_ms: number;
  tmdb_request_interval_ms: number;
  file_extensions: string;
  metadata_extensions: string;
  media_tag_order: string[] | string;
  align_media_tags: boolean;
  max_works_per_run: number;
  overwrite_existing: boolean;
}

export type MediaOrganizeTaskInput = {
  task_name: string;
  account_id: number;
  target_directory: string;
  target_directory_id: string;
  action_type: string;
  target_root?: string;
  target_root_id?: string;
  media_type: string;
  rename_marker?: string;
  use_tmdb: boolean;
  use_jav?: boolean;
  overwrite_existing?: boolean;
  recursive?: boolean;
};

// ── 番号匹配方案的规则 ──
//
// JSON key 与 115-auto 的 organize_rules.json 保持一致，方便日后直接导入导出那份文件。

export interface MediaOrganizeJavReplaceRule {
  from: string;
  to: string;
}

export interface MediaOrganizeJavClassifyRule {
  name: string;
  target_name: string;
  /**
   * 当前匹配方式：nocode | pattern | includes。
   *
   * 显式记录，而不是从「哪个字段有值」反推 —— 反推会导致切换方式时必须清空另一种的
   * 输入内容（用户点一下按钮，之前填的就没了），而且正则还没填内容时会被反推回关键词。
   * 老数据没有这个字段时后端按旧规则反推，前端同理。
   */
  mode?: "nocode" | "pattern" | "includes";
  /** 匹配「无番号」（mode === "nocode" 的等价写法，后端会同步） */
  nocode?: boolean;
  pattern?: string;
  includes?: string[];
  excludes?: string[];
}

export interface MediaOrganizeJavRules {
  junk_chars: string[];
  replace_rules: MediaOrganizeJavReplaceRule[];
  classify_rules: MediaOrganizeJavClassifyRule[];
}

export interface MediaOrganizeJavToggles {
  /** 清理小文件总开关。默认关闭 —— 删文件不可逆。 */
  delete_small: boolean;
  small_file_mb: number;
  /**
   * 只有这些扩展名（分号分隔）会被小文件清理删掉。留空 = 不限类型。
   */
  delete_types: string;
  /**
   * 这些扩展名永远不会被小文件清理删掉（分号分隔）。留空 = 不排除任何类型。
   *
   * 两者都留空时，只要大小满足阈值就会被删。
   */
  delete_exclude_types: string;
  clean_empty_dirs: boolean;
  max_dirs: number;
}

export interface MediaOrganizeJavRulesPayload {
  rules: MediaOrganizeJavRules;
  /** 后端下发的出厂默认值，供「恢复默认」用 —— 前端不重复维护一份默认表。 */
  defaults: MediaOrganizeJavRules;
  settings: MediaOrganizeJavToggles;
}

/** 试跑一条文件名的结果。 */
export interface MediaOrganizeJavExplain {
  name: string;
  has_code: boolean;
  code: string;
  renamed: string;
  dir_name: string;
  changed: boolean;
  /** 处理过程中实际命中的规则，按顺序 */
  steps: string[];
  classify_rule: string;
  classify_target: string;
  warnings: string[];
}

export function fetchMediaOrganizeTasks() {
  return http.get<MediaOrganizeTask[]>("/admin/media-organize/tasks");
}

export function createMediaOrganizeTask(input: MediaOrganizeTaskInput) {
  return http.post<MediaOrganizeTask>("/admin/media-organize/tasks", input);
}

export function updateMediaOrganizeTask(id: string, input: Partial<MediaOrganizeTaskInput>) {
  return http.put<MediaOrganizeTask>(`/admin/media-organize/tasks/${id}`, input);
}

export function deleteMediaOrganizeTask(id: string) {
  return http.del<{ id: string; stopping?: boolean }>(`/admin/media-organize/tasks/${id}`);
}

export interface MediaOrganizePlanResult {
  plan: MediaOrganizePlan;
  summary?: { actions?: number; skipped?: number };
}

export function planMediaOrganizeTask(id: string) {
  return http.post<MediaOrganizePlanResult>(`/admin/media-organize/tasks/${id}/plan`);
}

export function fetchMediaOrganizePlan(id: string) {
  return http.get<MediaOrganizePlan>(`/admin/media-organize/tasks/${id}/plan`);
}

export function applyMediaOrganizeTask(id: string) {
  return http.post<Record<string, unknown>>(`/admin/media-organize/tasks/${id}/apply`);
}

export function stopMediaOrganizeTask(id: string) {
  return http.post<{ stopping: boolean }>(`/admin/media-organize/tasks/${id}/stop`);
}

export function fetchMediaOrganizeLogs(id: string) {
  return http.get<{
    logs: MediaOrganizeLogEntry[];
    status: string;
    last_run_result?: MediaOrganizeRunResult;
  }>(`/admin/media-organize/tasks/${id}/logs`);
}

export function fetchMediaOrganizeProgress(id: string) {
  return http.get<MediaOrganizeProgress>(`/admin/media-organize/tasks/${id}/progress`);
}

export function updateMediaOrganizePlanAction(taskId: string, actionId: string, targetName: string) {
  return http.put<{ action?: MediaOrganizePlanAction; changed?: boolean }>(
    `/admin/media-organize/tasks/${taskId}/plan/actions/${actionId}`,
    { target_name: targetName },
  );
}

export function deleteMediaOrganizePlanAction(taskId: string, actionId: string) {
  return http.del<{ removed?: string }>(`/admin/media-organize/tasks/${taskId}/plan/actions/${actionId}`);
}

export function batchDeleteMediaOrganizePlanActions(taskId: string, actionIds: string[]) {
  return http.post<{ removed?: string[] }>(`/admin/media-organize/tasks/${taskId}/plan/actions/batch-delete`, {
    action_ids: actionIds,
  });
}

export function testMediaOrganizeTmdb(payload?: Partial<MediaOrganizeSettings>) {
  return http.post<{
    ok: boolean;
    api_ok?: boolean;
    image_ok?: boolean;
    image_status?: number;
    language?: string;
    proxy_used?: boolean;
  }>("/admin/media-organize/test-tmdb", payload ?? {});
}

export interface MediaOrganizeTmdbSearchHit {
  id?: number | string;
  title?: string;
  name?: string;
  original_title?: string;
  original_name?: string;
  release_date?: string;
  first_air_date?: string;
  poster_path?: string;
  media_type?: string;
  overview?: string;
}

export function searchMediaOrganizeTmdb(params: {
  query: string;
  year?: number;
  language?: string;
  media_type?: string;
}) {
  return http.get<MediaOrganizeTmdbSearchHit[]>("/admin/media-organize/search-tmdb", {
    query: params.query,
    year: params.year,
    language: params.language,
    media_type: params.media_type ?? "auto",
  });
}

export function setMediaOrganizeBinding(taskId: string, groupUid: string, tmdbId: string, mediaType: "movie" | "tv") {
  return http.post<{ group_uid: string; tmdb_id: string; media_type: string; plan?: MediaOrganizePlan }>(
    `/admin/media-organize/tasks/${taskId}/bindings`,
    { group_uid: groupUid, tmdb_id: tmdbId, media_type: mediaType },
  );
}

export function fetchMediaOrganizeSettings() {
  return http.get<MediaOrganizeSettings>("/admin/media-organize/settings");
}

export function saveMediaOrganizeSettings(settings: Partial<MediaOrganizeSettings>) {
  return http.put<MediaOrganizeSettings>("/admin/media-organize/settings", settings);
}

// ── 番号匹配方案 ──

export function fetchMediaOrganizeJavRules() {
  return http.get<MediaOrganizeJavRulesPayload>("/admin/media-organize/jav-rules");
}

export function saveMediaOrganizeJavRules(payload: {
  rules: MediaOrganizeJavRules;
  settings: MediaOrganizeJavToggles;
}) {
  return http.put<MediaOrganizeJavRulesPayload>("/admin/media-organize/jav-rules", payload);
}

/**
 * 用给定规则试跑一批文件名。
 *
 * 不传 rules 就用已保存的规则；传了就用草稿 —— 设置页在保存前就能看到效果，
 * 这是编辑规则时唯一的验证手段。
 */
export function previewMediaOrganizeJavRules(names: string[], rules?: MediaOrganizeJavRules) {
  return http.post<{ results: MediaOrganizeJavExplain[] }>("/admin/media-organize/jav-preview", {
    names,
    ...(rules ? { rules } : {}),
  });
}
