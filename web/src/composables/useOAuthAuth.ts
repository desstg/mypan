import { ref } from "vue";
import { confirmOAuthReceived, getOAuthStatus, startOAuth } from "@/api/oauth";
import { toast } from "@/composables/useToast";

let pollTimer: ReturnType<typeof setTimeout> | null = null;
let popup: Window | null = null;

function clearPoll() {
  if (pollTimer) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function closePopup() {
  if (popup && !popup.closed) {
    try {
      popup.close();
    } catch {}
  }
  popup = null;
}

function renderPopup(title: string, message: string, isError = false) {
  if (!popup || popup.closed) return;
  try {
    popup.document.open();
    popup.document.write(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"><title>${title}</title>
<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f6f8fb;font-family:"PingFang SC",sans-serif}
.panel{width:min(420px,calc(100vw - 32px));padding:28px 24px;border-radius:16px;background:#fff;box-shadow:0 16px 48px rgba(15,23,42,.12);text-align:center}
.title{font-size:20px;font-weight:700;margin-bottom:12px;color:${isError ? "#b91c1c" : "#111827"}}
.msg{font-size:14px;line-height:1.7;color:#4b5563;white-space:pre-wrap}</style></head>
<body><div class="panel"><div class="title">${title}</div><div class="msg">${message}</div></div></body></html>`);
    popup.document.close();
  } catch {
    /* 已跳转到跨域页 */
  }
}

function openOAuthProgressPage() {
  popup = window.open("", "_blank");
  if (!popup) {
    throw new Error("浏览器拦截了授权页面，请允许弹出窗口后重试");
  }
  renderPopup("正在连接授权服务", "请稍候，页面将自动跳转到 OAuth 授权页。");
}

function navigateOAuthWindow(url: string) {
  if (popup && !popup.closed) {
    popup.location.href = url;
    return;
  }
  popup = window.open(url, "_blank");
  if (!popup) {
    throw new Error(
      `浏览器拦截了授权页面，请允许弹出窗口后重试，或手动打开：\n${url}`,
    );
  }
}

function oauthFieldValue(value: unknown): string {
  if (typeof value !== "string") return "";
  // OAuth Token 本身允许包含 +、/、= 等字符，只清理 JSON 外层可能夹带的空白/BOM。
  return value.replace(/^\uFEFF/, "").trim();
}

export function normalizeOAuthFields(
  tokens: Record<string, unknown> | null | undefined,
  fieldNames: string[],
): Record<string, string> {
  if (!tokens || typeof tokens !== "object") return {};
  const accepted = new Set(fieldNames);
  const out: Record<string, string> = {};
  for (const name of accepted) {
    const value = oauthFieldValue(tokens[name]);
    if (value) out[name] = value;
  }
  return out;
}

async function pollStatus(
  sessionId: string,
  cancelled: () => boolean,
): Promise<Record<string, unknown>> {
  const maxAttempts = 30;
  let attempts = 0;

  return new Promise((resolve, reject) => {
    const schedule = (delay: number) => {
      pollTimer = setTimeout(() => void tick().catch(reject), delay);
    };

    const tick = async () => {
      if (cancelled()) {
        reject(new Error("OAuth认证已取消"));
        return;
      }
      if (attempts >= maxAttempts) {
        reject(new Error("OAuth认证超时，请检查是否已完成授权"));
        return;
      }
      attempts++;

      try {
        const result = await getOAuthStatus(sessionId);
        if (!result.success || !result.data?.status) {
          throw new Error(result.message || "查询 OAuth 状态失败");
        }
        const { status, token_data: tokens, error } = result.data;
        if (status === "success") {
          clearPoll();
          try {
            await confirmOAuthReceived(sessionId);
          } catch {
            /* non-fatal */
          }
          resolve(tokens ?? {});
          return;
        }
        if (status === "error") {
          clearPoll();
          reject(new Error(error || result.data.message || "OAuth 认证失败"));
          return;
        }
        const interval = attempts <= 3 ? 2000 : attempts <= 10 ? 3000 : 4000;
        schedule(interval);
      } catch {
        if (cancelled()) {
          reject(new Error("OAuth认证已取消"));
          return;
        }
        schedule(Math.min(attempts * 2000, 10000));
      }
    };

    void tick().catch(reject);
  });
}

export function useOAuthAuth() {
  const loading = ref(false);
  const cancelled = ref(false);

  function cancel() {
    cancelled.value = true;
    loading.value = false;
    clearPoll();
    closePopup();
  }

  async function run(driverType: string, fieldNames: string[]): Promise<Record<string, string>> {
    loading.value = true;
    cancelled.value = false;
    clearPoll();
    closePopup();

    try {
      openOAuthProgressPage();
      const start = await startOAuth(driverType);
      if (!start.success) {
        throw new Error(
          start.message ||
            "启动 OAuth 认证失败，请检查「系统设置」中的 OAuth 代理服务地址是否正确",
        );
      }
      const sessionId = start.data?.session_id;
      const url = start.data?.oauth_url?.trim();
      if (!sessionId) {
        throw new Error("OAuth 服务未返回 session_id");
      }
      if (!url) {
        throw new Error(
          "OAuth 服务未返回授权地址，请确认代理服务可用且驱动类型受支持",
        );
      }
      navigateOAuthWindow(url);
      const tokens = await pollStatus(sessionId, () => cancelled.value);
      if (driverType === "115_open") {
        await new Promise((r) => setTimeout(r, 2000));
      }
      const filled = normalizeOAuthFields(tokens, fieldNames);
      const count = Object.keys(filled).length;
      if (count === 0) {
        throw new Error("OAuth 服务未返回当前驱动可用的认证字段");
      }
      renderPopup("OAuth 认证成功", `已自动填充 ${count} 个字段，可关闭此页返回。`);
      toast.success(`OAuth 认证成功，已自动填充 ${count} 个字段`);
      return filled;
    } catch (e) {
      const msg = e instanceof Error ? e.message : "OAuth 认证失败";
      if (!cancelled.value) {
        if (popup && !popup.closed) {
          renderPopup("OAuth 认证失败", `${msg}\n\n请检查后台「系统设置 → OAuth 代理服务地址」。`, true);
        }
        toast.error(msg);
      }
      throw e;
    } finally {
      loading.value = false;
    }
  }

  return { loading, run, cancel };
}
