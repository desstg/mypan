import type { ApiResp } from "./types";

export interface OAuthStartResult {
  session_id: string;
  oauth_url: string;
}

export interface OAuthStatusResult {
  status: "pending" | "success" | "error";
  token_data?: Record<string, string>;
  error?: string;
  message?: string;
}

async function oauthFetch<T>(method: string, path: string, body?: unknown): Promise<ApiResp<T>> {
  const init: RequestInit = { method, headers: {} };
  if (body !== undefined) {
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(`/api${path}`, init);
  return (await resp.json()) as ApiResp<T>;
}

export async function startOAuth(driverType: string) {
  return oauthFetch<OAuthStartResult>("POST", "/oauth/start", {
    driver_type: driverType,
    server_use: true,
  });
}

export async function getOAuthStatus(sessionId: string) {
  return oauthFetch<OAuthStatusResult>("GET", `/oauth/status/${sessionId}`);
}

export async function confirmOAuthReceived(sessionId: string) {
  return oauthFetch<unknown>("POST", `/oauth/confirm-received/${sessionId}`);
}
