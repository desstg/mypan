import { http } from "./client";

export interface CoverFrame { id: string; time_ms: number }
export interface CoverFile { id: string; account_id: number; file_id: string; parent_id: string; target_parent_id: string; target_path: string; name: string; size: number; status: string; error?: string; duration_ms?: number; frames: CoverFrame[] }
export interface CoverRuntime { enabled: boolean; ready: boolean; error?: string; manual_path: string; auto_download_available: boolean }
export interface CoverStyle { shape: "slant" | "straight"; height: number; panel_color: string; opacity: number; text_color: string; packaged: boolean }

const base = "/admin/tools/cover-extract";
export const coverExtractApi = {
  files: () => http.get<{ files: CoverFile[] }>(`${base}/files`),
  add: (payload: { account_id: number; file_id: string; parent_id: string; directory_chain: Array<{ id: string; name: string }> }) => http.post<CoverFile>(`${base}/files`, payload),
  setTarget: (id: string, payload: { parent_id: string; path: string }) => http.put<CoverFile>(`${base}/files/${encodeURIComponent(id)}/target`, payload),
  remove: (id: string) => http.del<{ ok: boolean }>(`${base}/files/${encodeURIComponent(id)}`),
  removeFrame: (id: string, frameID: string) => http.del<{ ok: boolean }>(`${base}/files/${encodeURIComponent(id)}/frames/${encodeURIComponent(frameID)}`),
  clear: () => http.del<{ ok: boolean }>(`${base}/files`),
  runtime: () => http.get<CoverRuntime>(`${base}/runtime`),
  setEnabled: (enabled: boolean) => http.put<CoverRuntime>(`${base}/enabled`, { enabled }),
  download: () => http.post<CoverRuntime>(`${base}/runtime/download`),
  getStyle: () => http.get<CoverStyle>(`${base}/style`),
  saveStyle: (payload: CoverStyle) => http.put<CoverStyle>(`${base}/style`, payload),
  // 远端媒体在低带宽或代理模式下可能需要较长时间，避免被通用 90 秒请求超时提前取消。
  // approx：逐帧候选提取标记（非精确，走关键帧极速路径）。
  extract: (payload: { session_file_id: string; mode: "head" | "random" | "timestamp" | "probe"; timestamp_ms?: number; approx?: boolean }) => http.postWithTimeout<CoverFile>(`${base}/extract`, payload, 6 * 60_000),
  save: (payload: { session_file_id: string; frame_id: string; overwrite: boolean }) => http.post<{ ok: boolean; conflict?: boolean; filename: string }>(`${base}/save`, payload),
  saveComposed: (payload: { session_file_id: string; frame_id: string; overwrite: boolean }, poster: Blob) => {
    const form = new FormData();
    form.set("session_file_id", payload.session_file_id);
    form.set("frame_id", payload.frame_id);
    form.set("overwrite", String(payload.overwrite));
    form.set("poster", poster, "poster.jpg");
    return http.form<{ ok: boolean; conflict?: boolean; filename: string }>(`${base}/save-composed`, form);
  },
  imageURL: (id: string) => `/api${base}/images/${encodeURIComponent(id)}`,
};
