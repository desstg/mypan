package auth

import (
	"context"
	"fmt"

	"litepan/internal/driver"
)

// LoadManagedAccounts 启动时注册所有活跃且支持刷新的账号。
func (s *Service) LoadManagedAccounts(ctx context.Context) error {
	list, err := s.accounts.List(ctx)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a == nil || !a.IsActive {
			continue
		}
		if s.supportsRefresh(a.DriverType) {
			s.ensureSchedule(ctx, a.ID)
			s.Register(a.ID)
		}
	}
	return nil
}

// Register 将账号纳入认证调度（不触发重算；由调用方在适当时机 TriggerRecalculation）。
func (s *Service) Register(accountID int64) {
	s.mu.Lock()
	s.managed[accountID] = struct{}{}
	s.mu.Unlock()
}

// Unregister 将账号移出认证调度；返回是否曾纳入调度。
func (s *Service) Unregister(accountID int64) bool {
	s.mu.Lock()
	_, ok := s.managed[accountID]
	if ok {
		delete(s.managed, accountID)
	}
	s.mu.Unlock()
	return ok
}

func (s *Service) supportsRefresh(driverType string) bool {
	drv, ok := driver.New(driverType)
	if !ok {
		return false
	}
	_, ok = drv.(driver.AuthRefresher)
	return ok
}

func (s *Service) managedIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.managed))
	for id := range s.managed {
		out = append(out, id)
	}
	return out
}

func (s *Service) accountName(ctx context.Context, accountID int64) string {
	if s.accounts == nil {
		return fmt.Sprintf("账号%d", accountID)
	}
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil || acc == nil {
		return fmt.Sprintf("账号%d", accountID)
	}
	return acc.Name
}
