package notification

import (
	"context"
	"fmt"
	"log/slog"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

type Options struct {
	Repo     domain.NotificationRepository
	Accounts domain.AccountRepository
	Log      *slog.Logger
}

type Service struct {
	repo     domain.NotificationRepository
	accounts domain.AccountRepository
	log      *slog.Logger
}

func NewService(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: opts.Repo, accounts: opts.Accounts, log: log}
}

func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, s.onAuthFailed)
	eventbus.Subscribe(bus, s.onAuthRecovered)
	eventbus.Subscribe(bus, s.onCreated)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]*domain.Notification, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.List(ctx, limit, offset)
}

func (s *Service) UnreadCount(ctx context.Context) (int, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.UnreadCount(ctx)
}

func (s *Service) MarkRead(ctx context.Context, id int64) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.MarkRead(ctx, id)
}

func (s *Service) MarkAllRead(ctx context.Context) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.MarkAllRead(ctx)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) DeleteAll(ctx context.Context) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.DeleteAll(ctx)
}

func (s *Service) DeleteByRef(ctx context.Context, category string, refID int64) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.DeleteByRef(ctx, category, refID)
}

func (s *Service) Notify(ctx context.Context, level, category, title, message string, accountID, refID int64) {
	if s == nil {
		return
	}
	s.persist(ctx, level, category, title, message, accountID, refID)
}

func (s *Service) onAuthFailed(ctx context.Context, e eventbus.AccountAuthFailed) {
	if !e.Fatal {
		return
	}
	msg := e.Reason
	if name := s.accountName(ctx, e.AccountID); name != "" {
		msg = name + "：" + e.Reason
	}
	s.persist(ctx, "error", "auth", "存储账号认证已失效", msg, e.AccountID, 0)
}

func (s *Service) onAuthRecovered(ctx context.Context, e eventbus.AccountAuthRecovered) {
	name := s.accountName(ctx, e.AccountID)
	if name == "" {
		name = fmt.Sprintf("账号 #%d", e.AccountID)
	}
	s.persist(ctx, "success", "auth", "存储账号认证已恢复", name+" 认证已恢复正常", e.AccountID, 0)
}

func (s *Service) onCreated(ctx context.Context, e eventbus.NotificationCreated) {
	category := e.Category
	if category == "" {
		category = "system"
	}
	level := e.Level
	if level == "" {
		level = "info"
	}
	s.persist(ctx, level, category, e.Title, e.Message, e.AccountID, e.RefID)
}

func (s *Service) persist(ctx context.Context, level, category, title, message string, accountID, refID int64) {
	if s.repo == nil {
		return
	}
	_, err := s.repo.Create(ctx, &domain.Notification{
		Level:     level,
		Category:  category,
		Title:     title,
		Message:   message,
		AccountID: accountID,
		RefID:     refID,
	})
	if err != nil {
		s.log.Warn("persist notification failed", "title", title, "err", err)
	}
}

func (s *Service) accountName(ctx context.Context, accountID int64) string {
	if s.accounts == nil || accountID <= 0 {
		return ""
	}
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil || acc == nil {
		return ""
	}
	return acc.Name
}
