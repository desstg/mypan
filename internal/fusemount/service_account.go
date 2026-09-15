package fusemount

import "context"

func (s *Service) OnAccountDeleted(ctx context.Context, accountID int64) error {
	if s == nil || s.repo == nil || accountID <= 0 {
		return nil
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, m := range list {
		if m == nil || m.AccountID != accountID {
			continue
		}
		if err := s.Delete(ctx, m.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
