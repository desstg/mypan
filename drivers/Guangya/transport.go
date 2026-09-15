package guangya

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
	"litepan/pkg/strutil"
)

const (
	accountBaseURL = "https://account.guangyapan.com"
	apiBaseURL     = "https://api.guangyapan.com"
	webBaseURL     = "https://www.guangyapan.com"

	pathUserMe           = "/v1/user/me"
	pathUserAssets       = "/assets/v1/get_assets"
	pathFileList         = "/userres/v1/file/get_file_list"
	pathFileInfoByID     = "/userres/v1/file/get_info_by_file_id"
	pathFileDetail       = "/userres/v1/file/get_file_detail"
	pathDownloadURL      = "/userres/v1/get_res_download_url"
	pathUploadToken      = "/userres/v1/get_res_center_token"
	pathCheckFlashUpload = "/userres/v1/check_can_flash_upload"
	pathUploadTaskInfo   = "/userres/v1/file/get_info_by_task_id"
	pathCreateDir        = "/userres/v1/file/create_dir"
	pathRename           = "/userres/v1/file/rename"
	pathMoveFile         = "/userres/v1/file/move_file"
	pathDeleteFile       = "/userres/v1/file/delete_file"
	pathCopyFile         = "/userres/v1/file/copy_file"
	pathTaskStatus       = "/userres/v1/get_task_status"

	listPageSize            = 50
	listOrderByDefault      = 3
	listSortTypeDefault     = 1
	defaultOperationDelayMS = 300

	defaultClientID = "aMe-8VSlkrbQXpUR"
)

func (d *Driver) apiBase() string { return apiBaseURL }

func (d *Driver) accountBase() string { return accountBaseURL }

func (d *Driver) rootParentID() string {
	return strings.TrimSpace(d.add.RootFolderID)
}

func (d *Driver) resolveParent(parentID string) string {
	p := strings.TrimSpace(parentID)
	if p == "" || p == "0" || p == "/" || p == "root" {
		return d.rootParentID()
	}
	return p
}

func (d *Driver) clientID() string {
	if id := strings.TrimSpace(d.add.ClientID); id != "" {
		return id
	}
	return defaultClientID
}

func (d *Driver) buildAPIHeaders(token string) map[string]string {
	h := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9",
		"Content-Type":    "application/json",
		"did":             d.deviceID(),
		"dt":              "4",
		"Origin":          webBaseURL,
		"Referer":         webBaseURL + "/",
	}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	return h
}

func (d *Driver) buildAccountHeaders() map[string]string {
	deviceID := d.deviceID()
	return map[string]string{
		"Accept":             "application/json, text/plain, */*",
		"Content-Type":       "application/json",
		"X-Device-Model":     "chrome%2F147.0.0.0",
		"X-Device-Name":      "PC-Chrome",
		"X-Device-Sign":      "wdi10." + deviceID + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"X-Net-Work-Type":    "NONE",
		"X-OS-Version":       "MacIntel",
		"X-Platform-Version": "1",
		"X-Protocol-Version": "301",
		"X-Provider-Name":    "NONE",
		"X-SDK-Version":      "9.0.2",
		"X-Client-Id":        d.clientID(),
		"X-Client-Version":   "0.0.1",
		"X-Device-Id":        deviceID,
	}
}

func (d *Driver) accountPOST(ctx context.Context, path string, body map[string]any, out any) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	req, err := httpx.NewJSONRequest(ctx, http.MethodPost, d.accountBase()+path, nil, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, d.buildAccountHeaders())
	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.Errorf(domain.CodeRateLimited, "光鸭认证请求过于频繁，请稍后重试")
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.Errorf(domain.CodeAuthExpired, "%s", accountErrorMessage(data))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.Errorf(domain.CodeDriverError, "光鸭认证 HTTP %d: %s", resp.StatusCode, accountErrorMessage(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func accountErrorMessage(data []byte) string {
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Message          string `json:"message"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if msg := strutil.FirstNonEmpty(payload.ErrorDescription, payload.Error, payload.Message); msg != "" {
			return httpx.Truncate([]byte(msg), 300)
		}
	}
	return httpx.Truncate(data, 300)
}

func (d *Driver) waitOperationDelay(ctx context.Context) error {
	return driver.WaitRequestInterval(ctx, d.intervalGate, defaultOperationDelayMS)
}

func (d *Driver) apiRequest(ctx context.Context, path string, body map[string]any, out any, allowedCodes ...int) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	err := d.rawAPIRequest(ctx, path, d.currentToken(), body, out, allowedCodes...)
	if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
		token, rerr := d.doRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		if err := d.waitOperationDelay(ctx); err != nil {
			return err
		}
		return d.rawAPIRequest(ctx, path, token, body, out, allowedCodes...)
	}
	return err
}

func (d *Driver) rawAPIRequest(ctx context.Context, path, token string, body map[string]any, out any, allowedCodes ...int) error {
	req, err := httpx.NewJSONRequest(ctx, http.MethodPost, d.apiBase()+path, nil, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, d.buildAPIHeaders(token))

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "光鸭 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 500))
	}

	var env apiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Code != 0 && !containsInt(allowedCodes, env.Code) {
		return mapAPIError(env.Code, strutil.FirstNonEmpty(env.Msg, "光鸭业务请求失败"))
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func (d *Driver) accountGET(ctx context.Context, path string, out any) error {
	if err := d.waitOperationDelay(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.accountBase()+path, nil)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	h := d.buildAccountHeaders()
	h["Authorization"] = "Bearer " + d.currentToken()
	httpx.SetHeaders(req, h)

	resp, data, err := httpx.Execute(d.client, req, httpx.DefaultReadLimit)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode == http.StatusForbidden {
		return domain.Errf(domain.CodePermissionDenied)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "光鸭账户 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func mapAPIError(code int, msg string) error {
	switch code {
	case 401, 403:
		return domain.Errorf(domain.CodeAuthExpired, "光鸭认证失败：%s", msg)
	case 429:
		return domain.Errorf(domain.CodeRateLimited, "光鸭接口限流：%s", msg)
	case 354:
		return domain.Errorf(domain.CodeRateLimited, "%s", msg)
	default:
		return domain.Errorf(domain.CodeDriverError, "光鸭 API 错误(%d)：%s", code, msg)
	}
}

func containsInt(list []int, target int) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
