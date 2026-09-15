import type { ApiResp } from "./types";

export class ApiError extends Error {
  readonly errorType: string;
  readonly status: number;
  constructor(message: string, errorType: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.errorType = errorType;
    this.status = status;
  }
}

export function getApiErrorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

type Query = Record<string, string | number | boolean | undefined>;

interface RequestOptions {
  query?: Query;
  body?: unknown;
  form?: URLSearchParams | FormData;
  skipAuthRedirect?: boolean;
  signal?: AbortSignal;
  timeoutMs?: number;
}

function buildURL(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

let authRedirecting = false;

function maybeRedirectLogin(errorType: string, status: number, skip?: boolean) {
  if (skip || authRedirecting) return;
  if (status !== 401 || errorType !== "ADMIN_AUTH_REQUIRED") return;
  const path = window.location.pathname;
  if (path === "/login" || path.startsWith("/login")) return;
  authRedirecting = true;
  const redirect = encodeURIComponent(window.location.pathname + window.location.search);
  window.location.assign(`/login?redirect=${redirect}`);
}

function shouldSkipAuthRedirect(path: string): boolean {
  return path.startsWith("/auth/") || path.startsWith("/public/");
}

const defaultRequestTimeoutMs = 90_000;

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  let cancelled = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, opts.timeoutMs ?? defaultRequestTimeoutMs);
  const onAbort = () => {
    cancelled = true;
    controller.abort();
  };
  if (opts.signal?.aborted) {
    onAbort();
  } else {
    opts.signal?.addEventListener("abort", onAbort, { once: true });
  }
  const init: RequestInit = {
    method,
    credentials: "include",
    headers: {},
    signal: controller.signal,
  };
  if (opts.form) {
    init.body = opts.form;
  } else if (opts.body !== undefined) {
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
    init.body = JSON.stringify(opts.body);
  }
  if (method !== "GET" && method !== "HEAD") {
    (init.headers as Record<string, string>)["Origin"] = window.location.origin;
  }

  let resp: Response;
  try {
    resp = await fetch(buildURL(`/api${path}`, opts.query), init);
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      if (cancelled || opts.signal?.aborted) {
        throw new ApiError("请求已取消", "aborted", 0);
      }
      if (timedOut) {
        throw new ApiError("请求超时，请稍后重试", "timeout", 0);
      }
      throw new ApiError("请求超时，请稍后重试", "timeout", 0);
    }
    throw new ApiError("网络请求失败，请检查后端是否在运行", "network_error", 0);
  } finally {
    clearTimeout(timer);
    opts.signal?.removeEventListener("abort", onAbort);
  }

  let payload: ApiResp<T> | null = null;
  try {
    payload = (await resp.json()) as ApiResp<T>;
  } catch {
    /* 非 JSON 响应由调用方另行处理 */
  }

  if (!payload) {
    if (!resp.ok) throw new ApiError(`请求失败 (${resp.status})`, "http_error", resp.status);
    return undefined as T;
  }

  if (!payload.success) {
    const errorType = payload.error_type || "unknown";
    maybeRedirectLogin(errorType, resp.status, opts.skipAuthRedirect || shouldSkipAuthRedirect(path));
    throw new ApiError(payload.message || "请求失败", errorType, resp.status);
  }
  return payload.data as T;
}

export const http = {
  get: <T>(path: string, query?: Query) => request<T>("GET", path, { query }),
  post: <T>(path: string, body?: unknown, query?: Query, signal?: AbortSignal) =>
    request<T>("POST", path, { body, query, signal }),
  postWithTimeout: <T>(path: string, body: unknown, timeoutMs: number, signal?: AbortSignal) =>
    request<T>("POST", path, { body, signal, timeoutMs }),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, { body }),
  del: <T>(path: string, body?: unknown, query?: Query) =>
    request<T>("DELETE", path, { body, query }),
  form: <T>(path: string, form: URLSearchParams | FormData) =>
    request<T>("POST", path, { form, skipAuthRedirect: path.startsWith("/auth/") }),
};
