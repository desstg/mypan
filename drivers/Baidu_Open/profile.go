package baiduopen

import (
	"context"
	"litepan/internal/domain"
	"net/http"
	"strconv"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	var info struct {
		UK          int64  `json:"uk"`
		NetdiskName string `json:"netdisk_name"`
		BaiduName   string `json:"baidu_name"`
		VIPType     int    `json:"vip_type"`
	}
	if err := d.apiCall(ctx, http.MethodGet, opUserInfo, nil, nil, &info); err != nil {
		return nil, err
	}
	var quota struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
	}
	if err := d.apiCall(ctx, http.MethodGet, opQuota, nil, nil, &quota); err != nil {
		return nil, err
	}
	nick := info.NetdiskName
	if nick == "" {
		nick = info.BaiduName
	}
	p := &domain.AccountProfile{UserID: strconv.FormatInt(info.UK, 10), Nickname: nick, UsedBytes: quota.Used, TotalBytes: quota.Total}
	switch info.VIPType {
	case 2:
		p.Membership = "SVIP"
	case 1:
		p.Membership = "VIP"
	}
	return p, nil
}
