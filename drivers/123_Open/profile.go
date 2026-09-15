package pan123open

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"litepan/internal/domain"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	var v struct {
		UID            int64  `json:"uid"`
		Nickname       string `json:"nickname"`
		SpaceUsed      int64  `json:"spaceUsed"`
		SpacePermanent int64  `json:"spacePermanent"`
		SpaceTemp      int64  `json:"spaceTemp"`
		VIPInfo        []struct {
			Level int    `json:"vipLevel"`
			Label string `json:"vipLabel"`
			End   string `json:"endTime"`
		} `json:"vipInfo"`
	}
	if err := d.apiCall(ctx, http.MethodGet, pathUserInfo, nil, nil, &v); err != nil {
		return nil, err
	}
	p := &domain.AccountProfile{UserID: strconv.FormatInt(v.UID, 10), Nickname: v.Nickname, UsedBytes: v.SpaceUsed, TotalBytes: v.SpacePermanent + v.SpaceTemp}
	now := time.Now()
	highestLevel := -1
	for _, vip := range v.VIPInfo {
		if end, err := time.ParseInLocation(timeLayout, vip.End, time.Local); err == nil && !end.Before(now) && vip.Level >= 0 && vip.Label != "" {
			if vip.Level > highestLevel {
				highestLevel = vip.Level
				p.Membership = vip.Label
			}
		}
	}
	return p, nil
}
