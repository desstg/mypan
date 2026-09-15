package quark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/httpx"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	account, err := d.getProfileAccountInfo(ctx)
	if err != nil {
		return nil, err
	}

	var member struct {
		MemberType    string `json:"member_type"`
		UsedCapacity  int64  `json:"use_capacity"`
		TotalCapacity int64  `json:"total_capacity"`
	}
	if _, err := d.apiRequestTo(ctx, profileMemberURL, http.MethodGet, "/member", nil, nil, &member); err != nil {
		return nil, err
	}

	return &domain.AccountProfile{
		UserID:     jsonID(account.UID),
		Nickname:   strings.TrimSpace(account.Nickname),
		Membership: quarkMembership(member.MemberType),
		UsedBytes:  member.UsedCapacity,
		TotalBytes: member.TotalCapacity,
	}, nil
}

type profileAccountInfo struct {
	UID      json.RawMessage `json:"uid"`
	Nickname string          `json:"nickname"`
}

// getProfileAccountInfo 适配账号中心接口；它与网盘接口的响应外壳不同。
func (d *Driver) getProfileAccountInfo(ctx context.Context) (profileAccountInfo, error) {
	if err := d.waitInterval(ctx); err != nil {
		return profileAccountInfo{}, err
	}
	query := url.Values{"platform": {"pc"}, "fr": {"pc"}}
	req, err := httpx.NewJSONRequest(ctx, http.MethodGet, profileAccountURL+"/account/info", query, nil)
	if err != nil {
		return profileAccountInfo{}, domain.Wrap(domain.CodeInternal, err)
	}
	httpx.SetHeaders(req, map[string]string{
		"User-Agent": clientUA,
		"Referer":    referer,
		"Accept":     "application/json, text/plain, */*",
	})
	if cookie := d.currentCookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, body, err := httpx.Execute(d.client, req, 16<<20)
	if err != nil {
		return profileAccountInfo{}, domain.Wrap(domain.CodeDriverError, err)
	}
	d.absorbSetCookie(ctx, resp.Header)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return profileAccountInfo{}, domain.Errorf(domain.CodeAuthExpired, "夸克 Cookie 认证失败，请重新获取 Cookie")
	case resp.StatusCode == http.StatusForbidden:
		return profileAccountInfo{}, domain.Errorf(domain.CodePermissionDenied, "夸克访问被拒绝，Cookie 权限不足")
	case resp.StatusCode >= http.StatusBadRequest:
		return profileAccountInfo{}, domain.Errorf(domain.CodeDriverError, "夸克账号信息请求失败：HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Success bool               `json:"success"`
		Data    profileAccountInfo `json:"data"`
		Message string             `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return profileAccountInfo{}, domain.Errorf(domain.CodeDriverError, "夸克账号信息返回异常：%s", httpx.Truncate(body, 300))
	}
	if !envelope.Success {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = "账号信息接口未返回成功状态"
		}
		return profileAccountInfo{}, domain.Errorf(domain.CodeDriverError, "夸克账号信息获取失败：%s", message)
	}
	return envelope.Data, nil
}

func quarkMembership(memberType string) string {
	switch strings.ToUpper(strings.TrimSpace(memberType)) {
	case "VIP":
		return "VIP"
	case "SUPER_VIP":
		return "SVIP"
	case "Z_VIP":
		return "SVIP+"
	case "EXP_SVIP":
		return "88VIP"
	case "MINI_VIP":
		return "迷你 VIP"
	default:
		return ""
	}
}

func jsonID(raw json.RawMessage) string {
	v := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	return v
}
