package api

import (
	"context"
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/settings"
)

type qrStartReq struct {
	DriverType string `json:"driver_type"`
	Config     string `json:"config"`
}

type qrStartResp struct {
	Token         string `json:"token"`
	QRImageBase64 string `json:"qr_image_base64"`
	QRURL         string `json:"qr_url"`
	ExpiresIn     int    `json:"expires_in"`
	Title         string `json:"title,omitempty"`
	Hint          string `json:"hint,omitempty"`
}

type qrPollReq struct {
	DriverType string `json:"driver_type"`
	Token      string `json:"token"`
}

type qrPollResp struct {
	Status       string            `json:"status"`
	Cookie       string            `json:"cookie,omitempty"`
	AccessToken  string            `json:"access_token,omitempty"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
	Message      string            `json:"message,omitempty"`
}

func (h *Handler) qrEphemeralConfig() driver.EphemeralConfig {
	return driver.EphemeralConfig{
		OAuthServerURL: func(ctx context.Context) string {
			if h.settings == nil {
				return domain.NormalizeOAuthServerURL("")
			}
			return domain.NormalizeOAuthServerURL(h.settings.String(settings.KeyOAuthServerURL))
		},
	}
}

func qrProvider(ctx context.Context, driverType, config string, cfg driver.EphemeralConfig) (driver.QRLoginProvider, func(context.Context), error) {
	dt := strings.TrimSpace(driverType)
	if dt == "" {
		return nil, nil, domain.Errorf(domain.CodeValidation, "缺少 driver_type")
	}
	drv, release, err := driver.OpenEphemeral(ctx, dt, config, cfg)
	if err != nil {
		return nil, nil, err
	}
	p, ok := drv.(driver.QRLoginProvider)
	if !ok {
		release(ctx)
		return nil, nil, domain.Errorf(domain.CodeValidation, "该驱动不支持扫码登录")
	}
	return p, release, nil
}

func (h *Handler) startQRLogin(w http.ResponseWriter, r *http.Request) {
	var in qrStartReq
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	p, release, err := qrProvider(r.Context(), in.DriverType, in.Config, h.qrEphemeralConfig())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer release(r.Context())

	res, err := p.StartQRLogin(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, qrStartResp{
		Token:         res.Token,
		QRImageBase64: res.QRImageBase64,
		QRURL:         res.QRURL,
		ExpiresIn:     res.ExpiresIn,
		Title:         res.Title,
		Hint:          res.Hint,
	})
}

func (h *Handler) pollQRLogin(w http.ResponseWriter, r *http.Request) {
	var in qrPollReq
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if strings.TrimSpace(in.Token) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少扫码会话 token"))
		return
	}
	p, release, err := qrProvider(r.Context(), in.DriverType, "", h.qrEphemeralConfig())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer release(r.Context())

	res, err := p.PollQRLogin(r.Context(), in.Token)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, qrPollResp{
		Status:       string(res.Status),
		Cookie:       res.Credentials.Cookie,
		AccessToken:  res.Credentials.AccessToken,
		RefreshToken: res.Credentials.RefreshToken,
		Fields:       res.Fields,
		Message:      res.Message,
	})
}
