package cacheretention

import "context"

func (s *Service) RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error) {
	if s == nil || s.repo == nil || accountID <= 0 {
		return 0, nil
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, task := range tasks {
		if task == nil || task.AccountID != accountID {
			continue
		}
		if err := s.DeleteTask(ctx, task.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
