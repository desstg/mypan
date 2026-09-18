package api

import (
	"context"
	"net/http"
	"strings"
	"time"

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
	started := time.Now()
	p, release, err := qrProvider(r.Context(), in.DriverType, in.Config, h.qrEphemeralConfig())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer release(r.Context())

	res, err := p.StartQRLogin(r.Context())
	if err != nil {
		requestLogger(r.Context()).Warn("扫码登录：取二维码失败",
			"driver", in.DriverType, "elapsed_ms", time.Since(started).Milliseconds(), "err", err)
		writeErr(w, err)
		return
	}
	requestLogger(r.Context()).Info("扫码登录：已下发二维码",
		"driver", in.DriverType, "elapsed_ms", time.Since(started).Milliseconds())
	writeOK(w, qrStartResp{
		Token:         res.Token,
		QRImageBase64: res.QRImageBase64,
		QRURL:         res.QRURL,
		ExpiresIn:     res.ExpiresIn,
		Title:         res.Title,
		Hint:          res.Hint,
	})
}

// pollQRLogin 查一次扫码状态。
//
// **这个接口会挂住约 30 秒**（115 的状态查询是长轮询）。所以它单独记一条耗时日志：
// 「扫了码但没反应」这类问题，判断依据只能是「这次请求到底花了多久、返回了什么状态」——
// 光看前端表现区分不了「请求被谁掐断了」和「上游一直说没扫」。
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
	started := time.Now()
	p, release, err := qrProvider(r.Context(), in.DriverType, "", h.qrEphemeralConfig())
	if err != nil {
		writeErr(w, err)
		return
	}
	defer release(r.Context())

	res, err := p.PollQRLogin(r.Context(), in.Token)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		requestLogger(r.Context()).Warn("扫码登录：轮询失败",
			"driver", in.DriverType, "elapsed_ms", elapsed, "err", err)
		writeErr(w, err)
		return
	}
	// 客户端已经走了（浏览器超时、代理掐断、用户关了弹窗）—— 这是关键信号：
	// 它意味着「后端拿到了结果但送不回去」。用 Warn 让它显眼。
	if ctxErr := r.Context().Err(); ctxErr != nil {
		requestLogger(r.Context()).Warn("扫码登录：客户端已断开，结果无法送达",
			"driver", in.DriverType, "elapsed_ms", elapsed,
			"status", string(res.Status), "ctx_err", ctxErr)
		return
	}
	requestLogger(r.Context()).Info("扫码登录：轮询结束",
		"driver", in.DriverType, "elapsed_ms", elapsed,
		"status", string(res.Status), "has_cookie", res.Credentials.Cookie != "")
	writeOK(w, qrPollResp{
		Status:       string(res.Status),
		Cookie:       res.Credentials.Cookie,
		AccessToken:  res.Credentials.AccessToken,
		RefreshToken: res.Credentials.RefreshToken,
		Fields:       res.Fields,
		Message:      res.Message,
	})
}
