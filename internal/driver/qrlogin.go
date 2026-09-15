package driver

import (
	"context"

	"litepan/internal/domain"
)

// QRStatus 是扫码登录轮询的结构化状态。
type QRStatus string

const (
	QRWaiting QRStatus = "waiting" // 等待扫码/确认
	QRSuccess QRStatus = "success" // 已确认，凭证就绪
	QRFailed  QRStatus = "failed"  // 登录失败（取消/拒绝等）
	QRExpired QRStatus = "expired" // 二维码过期
)

// QRStartResult 开始扫码登录的返回；Token 为不透明续询令牌。
type QRStartResult struct {
	Token         string // 不透明续询令牌
	QRImageBase64 string // data:image/png;base64,...（驱动渲染好的二维码图）
	QRURL         string // 二维码内容 URL（供前端自渲染或兜底展示）
	ExpiresIn     int    // 二维码有效期（秒）
	Title         string // 弹窗标题（驱动文案，前端不特判）
	Hint          string // 二维码下方提示（驱动文案，前端不特判）
}

// QRPollResult 是一次轮询的结果；Status=Success 时 Credentials 填充运行凭证（如 Cookie）。
type QRPollResult struct {
	Status      QRStatus
	Credentials domain.AuthCredentials
	Fields      map[string]string // 需要一并回填到账号配置的驱动字段
	Message     string
}

// QRLoginProvider 可选：扫码登录获取运行凭证。
type QRLoginProvider interface {
	StartQRLogin(ctx context.Context) (*QRStartResult, error)
	PollQRLogin(ctx context.Context, token string) (*QRPollResult, error)
}
