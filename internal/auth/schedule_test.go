package auth

import (
	"context"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestSeedInitialScheduleTokenDriver(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	st := &domain.AuthState{AccountID: 1, Status: domain.AuthActive, AccessToken: "t", RefreshToken: "r"}
	SeedInitialSchedule(st, "refresh_test", now)
	if st.LastRefreshAt != now {
		t.Fatalf("last_refresh_at=%v", st.LastRefreshAt)
	}
	want := now.Add(30 * 24 * time.Hour)
	if !st.TokenExpires.Equal(want) {
		t.Fatalf("token_expires=%v want %v", st.TokenExpires, want)
	}
}

func TestCalcNextCheckUsesTokenExpiresDespiteFirstBoot(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, _, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	repo.states[1] = &domain.AuthState{
		AccountID:     1,
		Status:        domain.AuthActive,
		LastRefreshAt: now,
		TokenExpires:  now.Add(30 * 24 * time.Hour),
	}
	next := svc.calcNextCheck(context.Background(), 1, now, true)
	want := now.Add(30*24*time.Hour - 10*time.Hour)
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
}
