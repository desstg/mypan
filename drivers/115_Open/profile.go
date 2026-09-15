package pan115open

import (
	"context"
	"litepan/internal/domain"
	"net/http"
	"strconv"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	var v struct {
		UserID   int64  `json:"user_id"`
		UserName string `json:"user_name"`
		RTSpace  struct {
			Total struct {
				Size int64 `json:"size"`
			} `json:"all_total"`
			Used struct {
				Size int64 `json:"size"`
			} `json:"all_use"`
		} `json:"rt_space_info"`
		VIP struct {
			Name   string `json:"level_name"`
			Expire int64  `json:"expire"`
		} `json:"vip_info"`
	}
	if err := d.apiCall(ctx, http.MethodGet, pathUserInfo, nil, nil, &v); err != nil {
		return nil, err
	}
	p := &domain.AccountProfile{UserID: strconv.FormatInt(v.UserID, 10), Nickname: v.UserName, UsedBytes: v.RTSpace.Used.Size, TotalBytes: v.RTSpace.Total.Size}
	if v.VIP.Name != "" && v.VIP.Name != "原石会员" {
		p.Membership = v.VIP.Name
	}
	return p, nil
}
