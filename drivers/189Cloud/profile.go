package cloud189

import (
	"context"
	"strings"

	"litepan/internal/domain"
)

func (d *Driver) GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error) {
	return d.sessionProfile(), nil
}

func (d *Driver) sessionProfile() *domain.AccountProfile {
	d.mu.Lock()
	loginName := d.loginName
	d.mu.Unlock()
	return &domain.AccountProfile{Nickname: strings.TrimSpace(loginName)}
}
