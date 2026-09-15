package domain

import (
	"context"
	"strings"
	"time"
)

// QuarkTVBinding 是夸克 TV 播放接管插件绑定的 TV 凭证，与某个夸克网盘账号一一对应。
type QuarkTVBinding struct {
	AccountID           int64
	DeviceID            string
	RefreshToken        string
	AccessToken         string
	TokenExpiresAt      time.Time
	TVUID               string
	TVNickname          string
	PreferredResolution string
	AllowDolby          bool
	BoundAt             time.Time
}

const (
	QuarkTVResolutionAuto   = "auto"
	QuarkTVResolution4K     = "4k"
	QuarkTVResolution2K     = "2k"
	QuarkTVResolutionSuper  = "super"
	QuarkTVResolutionHigh   = "high"
	QuarkTVResolutionNormal = "normal"
	QuarkTVResolutionLow    = "low"
)

// NormalizeQuarkTVResolution 把前端传入的清晰度偏好归一化；未知值回落到 auto。
func NormalizeQuarkTVResolution(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", QuarkTVResolutionAuto:
		return QuarkTVResolutionAuto
	case "4k", "uhd", "2160p", "2160":
		return QuarkTVResolution4K
	case "2k", "qhd", "1440p", "1440":
		return QuarkTVResolution2K
	case "super", "1080p", "1080", "fhd":
		return QuarkTVResolutionSuper
	case "high", "720p", "720":
		return QuarkTVResolutionHigh
	case "normal", "480p", "480":
		return QuarkTVResolutionNormal
	case "low", "360p", "360":
		return QuarkTVResolutionLow
	default:
		return QuarkTVResolutionAuto
	}
}

// QuarkTVBindingRepository 维护夸克 TV 绑定的持久化记录。
type QuarkTVBindingRepository interface {
	Get(ctx context.Context, accountID int64) (*QuarkTVBinding, error)
	Upsert(ctx context.Context, b *QuarkTVBinding) error
	Delete(ctx context.Context, accountID int64) error
}
