package pan115open

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

var refreshFatalCodes = map[int64]struct{}{
	40140115: {},
	40140116: {},
	40140119: {},
	40140120: {},
}

var refreshRetryableCodes = map[int64]struct{}{
	40140117: {},
	40140121: {},
}

func classifyRefreshError(err error) driver.RefreshOutcome {
	code := errorCode(err)
	if _, ok := refreshFatalCodes[code]; ok {
		return driver.RefreshFatal
	}
	if _, ok := refreshRetryableCodes[code]; ok {
		return driver.RefreshRetryable
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "refresh_token") && strings.Contains(msg, "empty") ||
		strings.Contains(msg, "不能都为空") {
		return driver.RefreshFatal
	}
	return driver.ClassifyOAuthRefreshError(err)
}

func errorCode(err error) int64 {
	if ae, ok := domain.AsAppError(err); ok {
		msg := ae.Message
		if i := strings.Index(msg, "("); i >= 0 {
			if j := strings.Index(msg[i+1:], ")"); j >= 0 {
				codeStr := msg[i+1 : i+1+j]
				if c, err := strconv.ParseInt(strings.TrimSpace(codeStr), 10, 64); err == nil {
					return c
				}
			}
		}
	}
	msg := err.Error()
	const prefix = "code: "
	if idx := strings.Index(msg, prefix); idx >= 0 {
		rest := msg[idx+len(prefix):]
		end := strings.Index(rest, ",")
		if end < 0 {
			end = len(rest)
		}
		if c, err := strconv.ParseInt(strings.TrimSpace(rest[:end]), 10, 64); err == nil {
			return c
		}
	}
	if i := strings.Index(msg, "115 刷新失败("); i >= 0 {
		rest := msg[i+len("115 刷新失败("):]
		if j := strings.Index(rest, ")"); j >= 0 {
			if c, err := strconv.ParseInt(strings.TrimSpace(rest[:j]), 10, 64); err == nil {
				return c
			}
		}
	}
	return 0
}

func (d *Driver) RefreshAuth(ctx context.Context, _ driver.RefreshCaller) (driver.RefreshOutcome, error) {
	if err := d.beforeCall(ctx); err != nil {
		return driver.RefreshRetryable, err
	}
	_, err := d.doRefresh(ctx)
	if err != nil {
		outcome := classifyRefreshError(err)
		if outcome == driver.RefreshFatal {
			return outcome, err
		}
		return outcome, err
	}
	return driver.RefreshSuccess, nil
}

type refreshData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (d *Driver) doRefresh(ctx context.Context) (string, error) {
	return d.RefreshToken(ctx, d.currentToken, d.exchangeToken, classifyRefreshError)
}

func (d *Driver) exchangeToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	refresh := d.refresh
	d.mu.Unlock()
	if refresh == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "缺少 refresh_token，无法刷新访问令牌")
	}
	form := urlValues(map[string]string{"refresh_token": refresh})
	var data refreshData
	if err := d.postPassport(ctx, refreshURL, form, &data); err != nil {
		return "", err
	}
	if data.AccessToken == "" {
		return "", domain.Errorf(domain.CodeDriverError, "115 刷新响应缺少 access_token")
	}
	d.mu.Lock()
	d.token = data.AccessToken
	if data.RefreshToken != "" {
		d.refresh = data.RefreshToken
	}
	token := d.token
	refreshTok := d.refresh
	d.mu.Unlock()
	if d.persist != nil {
		if err := d.persist(ctx, domain.AuthCredentials{
			AccessToken:  token,
			RefreshToken: refreshTok,
		}); err != nil {
			return "", domain.Wrap(domain.CodeInternal, err)
		}
	}
	return token, nil
}

func urlValues(values map[string]string) url.Values {
	out := url.Values{}
	for k, v := range values {
		out.Set(k, v)
	}
	return out
}
