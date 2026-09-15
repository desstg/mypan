import type { ApiResp } from "./types";

export interface QrStartResult {
  token: string;
  qr_image_base64: string;
  qr_url: string;
  expires_in: number;
  title?: string;
  hint?: string;
}

export type QrStatus = "waiting" | "success" | "failed" | "expired";

export interface QrPollResult {
  status: QrStatus;
  cookie?: string;
  access_token?: string;
  refresh_token?: string;
  fields?: Record<string, string>;
  message?: string;
}

async function qrFetch<T>(path: string, body: unknown): Promise<ApiResp<T>> {
  const resp = await fetch(`/api${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return (await resp.json()) as ApiResp<T>;
}

export async function qrStart(driverType: string, config?: string) {
  return qrFetch<QrStartResult>("/qr/start", {
    driver_type: driverType,
    config: config || "",
  });
}

export async function qrPoll(driverType: string, token: string) {
  return qrFetch<QrPollResult>("/qr/poll", { driver_type: driverType, token });
}
