package guangya

import (
	"context"
	"strings"

	"litepan/internal/domain"
)

// GetAccountProfile 返回光鸭账号的公开资料；资料和容量分别来自账户与网盘接口。
func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	var user struct {
		Sub  string `json:"sub"`
		Name string `json:"name"`
	}
	if err := d.accountGET(ctx, pathUserMe, &user); err != nil {
		return nil, err
	}

	var assets struct {
		UsedSpaceSize  int64 `json:"usedSpaceSize"`
		TotalSpaceSize int64 `json:"totalSpaceSize"`
		VIPStatus      int   `json:"vipStatus"`
		SVIPStatus     int   `json:"svipStatus"`
	}
	if err := d.apiRequest(ctx, pathUserAssets, nil, &assets); err != nil {
		return nil, err
	}

	return &domain.AccountProfile{
		UserID:     strings.TrimSpace(user.Sub),
		Nickname:   strings.TrimSpace(user.Name),
		Membership: guangyaMembership(assets.VIPStatus, assets.SVIPStatus),
		UsedBytes:  assets.UsedSpaceSize,
		TotalBytes: assets.TotalSpaceSize,
	}, nil
}

func guangyaMembership(vipStatus, svipStatus int) string {
	if vipStatus == 2 || svipStatus == 2 {
		return "VIP"
	}
	return ""
}
