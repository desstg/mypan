package quark

import (
	"context"
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := strings.TrimSpace(creds.Cookie); c != "" {
		d.cookie = c
	}
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) currentCookie() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cookie
}

// consumeCookieChanged 读取并清空"上次请求是否吸收了新 Cookie"标记。
func (d *Driver) consumeCookieChanged() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	changed := d.lastCookieChanged
	d.lastCookieChanged = false
	return changed
}

// RefreshAuth 健康检查；无 refresh_token，仅验 Cookie。
func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	if err := d.Ping(ctx); err != nil {
		if ae, ok := domain.AsAppError(err); ok {
			switch ae.Code {
			case domain.CodeAuthExpired, domain.CodePermissionDenied:
				// 夸克无 refresh_token 无法自愈：凭据失效（AuthExpired）或封号/权限不足（PermissionDenied）
				// 都直接标记不可用并暂停关联任务，避免每分钟探测空打上游。
				return driver.RefreshFatal, err
			}
		}
		return driver.RefreshRetryable, err
	}
	return driver.RefreshSuccess, nil
}

// absorbSetCookie 吸收夸克下发的 __puus/__pus 增量 Cookie，合并后回写 account_auth_states。
func (d *Driver) absorbSetCookie(ctx context.Context, header http.Header) {
	raw := header.Values("Set-Cookie")
	if len(raw) == 0 {
		return
	}
	parsed := (&http.Response{Header: http.Header{"Set-Cookie": raw}}).Cookies()
	incoming := map[string]string{}
	for _, c := range parsed {
		if (c.Name == "__puus" || c.Name == "__pus") && c.Value != "" {
			incoming[c.Name] = c.Value
		}
	}
	if len(incoming) == 0 {
		return
	}

	d.mu.Lock()
	keys, vals := parseCookie(d.cookie)
	changed := false
	for name, val := range incoming {
		if vals[name] != val {
			if _, ok := vals[name]; !ok {
				keys = append(keys, name)
			}
			vals[name] = val
			changed = true
		}
	}
	if !changed {
		d.mu.Unlock()
		return
	}
	newCookie := buildCookie(keys, vals)
	d.cookie = newCookie
	d.lastCookieChanged = true
	persist := d.persist
	d.mu.Unlock()

	if persist != nil {
		// 回写失败不影响本次请求：新 Cookie 已在内存生效，下次启动再从 add.Cookie 兜底。
		_ = persist(ctx, domain.AuthCredentials{Cookie: newCookie})
	}
}

func parseCookie(s string) ([]string, map[string]string) {
	keys := []string{}
	vals := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.Index(part, "=")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(part[:i])
		if k == "" {
			continue
		}
		if _, ok := vals[k]; !ok {
			keys = append(keys, k)
		}
		vals[k] = strings.TrimSpace(part[i+1:])
	}
	return keys, vals
}

func buildCookie(keys []string, vals map[string]string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+vals[k])
	}
	return strings.Join(parts, "; ")
}
