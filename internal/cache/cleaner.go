package cache

import (
	"context"
	"log/slog"

	"litepan/internal/eventbus"
)

type Cleaner struct {
	cache *Service
	log   *slog.Logger
}

func NewCleaner(c *Service, log *slog.Logger) *Cleaner {
	if log == nil {
		log = slog.Default()
	}
	return &Cleaner{cache: c, log: log}
}

func (cl *Cleaner) Register(bus *eventbus.Bus) {
	eventbus.Subscribe(bus, func(_ context.Context, e eventbus.FileMutated) {
		cl.handle(e)
	})
}

func (cl *Cleaner) handle(e eventbus.FileMutated) {
	ApplyMutation(cl.cache, e)
}

func ApplyMutation(c *Service, e eventbus.FileMutated) {
	if c == nil {
		return
	}
	acc := e.AccountID
	InvalidateWebDAVAccountCaches(c, acc)

	switch e.Op {
	case "create":
		if tryUpsertDirOnCreate(c, e) {
			c.ClearDirCooling(acc, e.ParentID)
			break
		}
		invalidateMutationDirs(c, acc, e.ParentID)
	case "delete", "rename", "move", "copy":
		invalidateMutationFiles(c, acc, mutationFileIDs(e))
		parentIDs := []string{e.ParentID}
		if (e.Op == "move" || e.Op == "copy") && e.OldParentID != e.ParentID {
			parentIDs = append(parentIDs, e.OldParentID)
		}
		invalidateMutationDirs(c, acc, parentIDs...)
	default:
		c.InvalidateAccount(acc)
	}
}

func mutationFileIDs(e eventbus.FileMutated) []string {
	if len(e.FileIDs) > 0 {
		return e.FileIDs
	}
	if e.FileID != "" {
		return []string{e.FileID}
	}
	return nil
}

func invalidateMutationFiles(c *Service, accountID int64, fileIDs []string) {
	for _, fileID := range fileIDs {
		if fileID == "" {
			continue
		}
		c.InvalidateKey(FileInfoKey(accountID, fileID))
		c.InvalidatePrefix(DownloadURLPrefix(accountID, fileID))
	}
}

func invalidateMutationDirs(c *Service, accountID int64, parentIDs ...string) {
	for _, parentID := range parentIDs {
		InvalidateDirKeys(c, accountID, parentID)
	}
	c.MarkDirCooling(accountID, parentIDs...)
}
