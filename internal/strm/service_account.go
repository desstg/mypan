package strm

import "context"

func (s *Service) RemoveTasksByAccount(ctx context.Context, accountID int64) (int, error) {
	if s == nil || s.repo == nil || accountID <= 0 {
		return 0, nil
	}
	tasks, err := s.repo.ListByAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := s.DeleteTask(ctx, task.ID, false); err != nil {
			return removed, err
		}
		removed++
	}
	s.mu.Lock()
	delete(s.dirtyAccounts, accountID)
	s.mu.Unlock()
	return removed, nil
}
