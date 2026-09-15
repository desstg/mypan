import { http } from "./client";
import type {
  BrowserFavoritesPayload,
  BrowserFavoritesState,
  FileNameAlignApplyPayload,
  FileNameAlignApplyResult,
  FileNameAlignPreviewPayload,
  FileNameAlignPreviewResult,
  FileCreateFolderPayload,
  FileDeletePayload,
  FileListResult,
  FileRenamePayload,
  FileTransferPayload,
} from "./types";

export interface TextPreviewBytes {
  bytes: Uint8Array;
  truncated: boolean;
}

export const TEXT_PREVIEW_MAX_BYTES = 2 * 1024 * 1024;

function downloadURL(
  accountId: number,
  fileId: string,
  fileName?: string,
  options: Record<string, string> = {},
) {
  const params = new URLSearchParams({
    account_id: String(accountId),
    file_id: fileId,
    ...options,
  });
  if (fileName) params.set("file_name", fileName);
  return `/api/files/download?${params.toString()}`;
}

function proxyPreviewURL(accountId: number, fileId: string, fileName?: string) {
  return downloadURL(accountId, fileId, fileName, { force_proxy: "1" });
}

async function fetchPreview(
  accountId: number,
  fileId: string,
  fileName: string,
  init: RequestInit,
  failureLabel: string,
) {
  const response = await fetch(proxyPreviewURL(accountId, fileId, fileName), {
    credentials: "include",
    ...init,
  });
  if (response.ok) return response;

  let message = `${failureLabel} (${response.status})`;
  try {
    const payload = await response.json() as { message?: string };
    if (payload.message) message = payload.message;
  } catch {
    // 下载网关也可能返回非 JSON 错误页，保留状态码信息。
  }
  throw new Error(message);
}

async function readPreviewBytes(response: Response, fileSize: number): Promise<TextPreviewBytes> {
  const reader = response.body?.getReader();
  if (!reader) {
    const bytes = new Uint8Array(await response.arrayBuffer());
    return {
      bytes: bytes.slice(0, TEXT_PREVIEW_MAX_BYTES),
      truncated: fileSize > TEXT_PREVIEW_MAX_BYTES || bytes.length > TEXT_PREVIEW_MAX_BYTES,
    };
  }

  const chunks: Uint8Array[] = [];
  let total = 0;
  while (total <= TEXT_PREVIEW_MAX_BYTES) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    total += value.length;
  }
  if (total > TEXT_PREVIEW_MAX_BYTES) await reader.cancel();

  const length = Math.min(total, TEXT_PREVIEW_MAX_BYTES);
  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    if (offset >= length) break;
    const part = chunk.subarray(0, length - offset);
    bytes.set(part, offset);
    offset += part.length;
  }

  const contentRangeTotal = Number(response.headers.get("Content-Range")?.split("/").pop());
  return {
    bytes,
    truncated:
      fileSize > TEXT_PREVIEW_MAX_BYTES ||
      total > TEXT_PREVIEW_MAX_BYTES ||
      (Number.isFinite(contentRangeTotal) && contentRangeTotal > TEXT_PREVIEW_MAX_BYTES),
  };
}

async function readHeadBytes(response: Response, limit: number) {
  const reader = response.body?.getReader();
  if (!reader) return new Uint8Array(await response.arrayBuffer()).slice(0, limit);
  const bytes = new Uint8Array(limit);
  let offset = 0;
  while (offset < limit) {
    const { done, value } = await reader.read();
    if (done) break;
    const part = value.subarray(0, limit - offset);
    bytes.set(part, offset);
    offset += part.length;
    if (part.length < value.length) break;
  }
  await reader.cancel().catch(() => undefined);
  return bytes.slice(0, offset);
}

async function readBinaryPreviewBytes(response: Response, maxBytes: number) {
  const contentLength = Number(response.headers.get("Content-Length"));
  if (Number.isFinite(contentLength) && contentLength > maxBytes) {
    await response.body?.cancel().catch(() => undefined);
    throw new Error("文件过大，建议下载后查看");
  }

  const reader = response.body?.getReader();
  if (!reader) {
    const bytes = new Uint8Array(await response.arrayBuffer());
    if (bytes.length > maxBytes) throw new Error("文件过大，建议下载后查看");
    return bytes;
  }

  const chunks: Uint8Array[] = [];
  let total = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.length;
    if (total > maxBytes) {
      await reader.cancel().catch(() => undefined);
      throw new Error("文件过大，建议下载后查看");
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.length;
  }
  return bytes;
}

export const filesApi = {
  list: (accountId: number, parentId: string, opts?: { forceRefresh?: boolean; path?: string }) =>
    http.get<FileListResult>("/files/list", {
      account_id: accountId,
      parent_id: parentId,
      ...(opts?.path ? { path: opts.path } : {}),
      ...(opts?.forceRefresh ? { force_refresh: "true" } : {}),
    }),

  deleteFiles: (payload: FileDeletePayload) =>
    http.del<{ file_ids: string[] }>("/files/delete", payload),

  moveFiles: (payload: FileTransferPayload) =>
    http.post<{ file_ids: string[]; target_parent_id: string }>("/files/move", payload),

  copyFiles: (payload: FileTransferPayload) =>
    http.post<{ file_ids: string[]; target_parent_id: string }>("/files/copy", payload),

  renameFile: (payload: FileRenamePayload) =>
    http.put<{ file_id: string; new_name: string }>("/files/rename", payload),

  getFavorites: (accountId: number) =>
    http.get<BrowserFavoritesState>("/files/favorites", {
      account_id: accountId,
    }),

  saveFavorites: (payload: BrowserFavoritesPayload) =>
    http.put<BrowserFavoritesState>("/files/favorites", payload),

  previewNameAlign: (payload: FileNameAlignPreviewPayload) =>
    http.post<FileNameAlignPreviewResult>("/files/name-align/preview", payload),

  applyNameAlign: (payload: FileNameAlignApplyPayload) =>
    http.post<FileNameAlignApplyResult>("/files/name-align/apply", payload),

  createFolder: (payload: FileCreateFolderPayload) =>
    http.post<{ folder_id: string; folder_name: string; parent_id: string }>(
      "/files/create-folder",
      payload,
    ),

  downloadURL,

  previewURL: (accountId: number, fileId: string, fileName?: string) =>
    downloadURL(accountId, fileId, fileName, { inline: "1" }),

  pdfPreviewURL: proxyPreviewURL,

  proxyPreviewURL,

  binaryPreviewBytes: async (
    accountId: number,
    fileId: string,
    fileName: string,
    maxBytes: number,
    signal?: AbortSignal,
  ) => {
    const response = await fetchPreview(
      accountId,
      fileId,
      fileName,
      { signal },
      "文件读取失败",
    );
    return readBinaryPreviewBytes(response, maxBytes);
  },

  textPreviewBytes: async (
    accountId: number,
    fileId: string,
    fileName: string,
    fileSize: number,
    signal?: AbortSignal,
  ): Promise<TextPreviewBytes> => {
    const response = await fetchPreview(
      accountId,
      fileId,
      fileName,
      { headers: { Range: `bytes=0-${TEXT_PREVIEW_MAX_BYTES}` }, signal },
      "文本读取失败",
    );
    return readPreviewBytes(response, fileSize);
  },

  previewHeadBytes: async (
    accountId: number,
    fileId: string,
    fileName: string,
    signal?: AbortSignal,
  ) => {
    const response = await fetchPreview(
      accountId,
      fileId,
      fileName,
      { headers: { Range: "bytes=0-32767" }, signal },
      "文件类型检测失败",
    );
    return readHeadBytes(response, 32 * 1024);
  },
};
