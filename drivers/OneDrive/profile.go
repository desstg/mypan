package onedrive

import (
	"context"
	"net/http"
	"strings"

	"litepan/internal/domain"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	var drive struct {
		Owner struct {
			User struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
		} `json:"owner"`
		Quota struct {
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"quota"`
	}
	if err := d.apiRequest(ctx, http.MethodGet, "/me/drive", nil, nil, &drive); err != nil {
		return nil, err
	}
	return &domain.AccountProfile{
		UserID:     strings.TrimSpace(drive.Owner.User.ID),
		Nickname:   strings.TrimSpace(drive.Owner.User.DisplayName),
		UsedBytes:  drive.Quota.Used,
		TotalBytes: drive.Quota.Total,
	}, nil
}
