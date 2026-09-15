package fusereadcache

import (
	"context"
	"time"
)

func (s *Service) maintain(cfg Config) error {
	if s == nil || s.store == nil {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour).Unix()
	return s.store.expireBefore(cutoff)
}

func (s *Service) ensureSpace(cfg Config, incoming int64) error {
	used, _, err := s.store.stats()
	if err != nil {
		return err
	}
	if used+incoming <= cfg.MaxBytes {
		return nil
	}
	for used+incoming > cfg.MaxBytes {
		meta, ok, err := s.pickEvict(cfg)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.store.deleteBlock(meta); err != nil {
			return err
		}
		used -= meta.ByteLen
		if used < 0 {
			used = 0
		}
	}
	return nil
}

func (s *Service) pickEvict(cfg Config) (blockMeta, bool, error) {
	if cfg.EvictionPolicy == PolicyLargeFile {
		return s.store.pickEvictLargeFile()
	}
	return s.store.pickEvictLRU()
}

func (s *Service) putBlockWithPolicy(ctx context.Context, accountID int64, fileID string, blockIdx int64, data []byte) error {
	cfg := LoadConfig(ctx, s.settings)
	if err := s.maintain(cfg); err != nil {
		return err
	}
	if err := s.ensureSpace(cfg, int64(len(data))); err != nil {
		return err
	}
	return s.store.putBlock(accountID, fileID, blockIdx, data)
}
