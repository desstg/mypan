package backuprestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"litepan/internal/store"
)

type ApplyOptions struct {
	DataDir string
	DBPath  string
	Log     *slog.Logger
}

type rollbackManifest struct {
	Version          int    `json:"version"`
	CreatedAt        string `json:"created_at"`
	DatabaseExisted  bool   `json:"database_existed"`
	SecretExisted    bool   `json:"secret_existed"`
	FavoritesExisted bool   `json:"favorites_existed"`
}

// ApplyPending 在主 Store 打开前应用已准备的恢复，失败时自动回滚。
func ApplyPending(ctx context.Context, opts ApplyOptions) (Status, error) {
	restoreDir := filepath.Join(opts.DataDir, "restore")
	pendingPath := filepath.Join(restoreDir, "restore.pending")
	resultPath := filepath.Join(restoreDir, "restore.result.json")
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	var plan pendingPlan
	if err := readJSONFile(pendingPath, &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{State: StateIdle}, nil
		}
		return Status{}, fmt.Errorf("read pending restore: %w", err)
	}
	if plan.Version != 1 || !validRecordID(plan.ID) || plan.StageDir != plan.ID || !validRecordID(plan.SourceID) || (plan.Scope != ScopeSettings && plan.Scope != ScopeFull) {
		return Status{}, fmt.Errorf("pending restore manifest is invalid")
	}
	stageDir := filepath.Join(restoreDir, "staging", plan.StageDir)
	stageDBPath := filepath.Join(stageDir, "database", "litepan.db")
	if err := validateStagedDatabase(ctx, stageDBPath); err != nil {
		return Status{}, fmt.Errorf("validate staged restore: %w", err)
	}
	if plan.ReplaceSecret {
		secret, err := os.ReadFile(filepath.Join(stageDir, "data", "secret.key"))
		if err != nil || len(secret) < 16 || len(secret) > 4096 {
			return Status{}, fmt.Errorf("staged secret.key is invalid")
		}
	}
	if plan.ReplaceFavs {
		if _, err := readFavoritesOrEmpty(filepath.Join(stageDir, "data", "litepan_favorites.json")); err != nil {
			return Status{}, fmt.Errorf("staged favorites are invalid: %w", err)
		}
	}

	rollbackDir := filepath.Join(opts.DataDir, "backups", ".rollback", time.Now().UTC().Format("20060102-150405")+"-"+plan.ID)
	if err := os.MkdirAll(rollbackDir, 0o700); err != nil {
		return Status{}, fmt.Errorf("create rollback directory: %w", err)
	}
	rollback, err := createRollback(ctx, opts.DataDir, opts.DBPath, rollbackDir)
	if err != nil {
		return Status{}, fmt.Errorf("create restore rollback: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(rollbackDir, "manifest.json"), rollback); err != nil {
		return Status{}, fmt.Errorf("write rollback manifest: %w", err)
	}

	applyErr := applyStagedFiles(opts.DataDir, opts.DBPath, stageDir, plan)
	if applyErr == nil {
		applyErr = validateStagedDatabase(ctx, opts.DBPath)
	}
	if applyErr != nil {
		log.Error("备份恢复失败，开始自动回滚", "backup_id", plan.SourceID, "err", applyErr)
		if rollbackErr := restoreRollback(ctx, opts.DataDir, opts.DBPath, rollbackDir, rollback); rollbackErr != nil {
			return Status{}, fmt.Errorf("restore failed: %v; rollback failed: %w", applyErr, rollbackErr)
		}
		result := restoreResult{
			State:        StateRestoreRollback,
			Message:      "备份恢复失败，已自动回滚到恢复前的数据",
			BackupID:     plan.SourceID,
			BackupNote:   plan.BackupNote,
			Scope:        plan.Scope,
			RestoreAdmin: plan.RestoreAdmin,
			UpdatedAt:    nowText(),
		}
		if err := finishPendingRestore(pendingPath, resultPath, stageDir, result); err != nil {
			return Status{}, err
		}
		log.Warn("备份恢复失败，已自动回滚", "backup_id", plan.SourceID)
		return statusFromResult(result), nil
	}

	result := restoreResult{
		State:        StateRestoreSuccess,
		Message:      "备份恢复成功",
		BackupID:     plan.SourceID,
		BackupNote:   plan.BackupNote,
		Scope:        plan.Scope,
		RestoreAdmin: plan.RestoreAdmin,
		UpdatedAt:    nowText(),
	}
	if err := finishPendingRestore(pendingPath, resultPath, stageDir, result); err != nil {
		return Status{}, err
	}
	pruneRollbackDirs(filepath.Dir(rollbackDir), 3, log)
	log.Info("备份恢复成功", "backup_id", plan.SourceID, "scope", plan.Scope)
	return statusFromResult(result), nil
}

func createRollback(ctx context.Context, dataDir, dbPath, rollbackDir string) (rollbackManifest, error) {
	manifest := rollbackManifest{Version: 1, CreatedAt: nowText()}
	if _, err := os.Stat(dbPath); err == nil {
		manifest.DatabaseExisted = true
		db, err := store.Open(ctx, store.Options{Path: dbPath})
		if err != nil {
			return rollbackManifest{}, err
		}
		snapshotErr := db.SnapshotTo(ctx, filepath.Join(rollbackDir, "litepan.db"))
		closeErr := db.Close()
		if snapshotErr != nil {
			return rollbackManifest{}, snapshotErr
		}
		if closeErr != nil {
			return rollbackManifest{}, closeErr
		}
	} else if !os.IsNotExist(err) {
		return rollbackManifest{}, err
	}
	secretPath := filepath.Join(dataDir, "secret.key")
	secretExisted, err := copyOptionalFile(secretPath, filepath.Join(rollbackDir, "secret.key"), 0o600)
	if err != nil {
		return rollbackManifest{}, err
	}
	manifest.SecretExisted = secretExisted
	favoritesPath := filepath.Join(filepath.Dir(dbPath), "litepan_favorites.json")
	favoritesExisted, err := copyOptionalFile(favoritesPath, filepath.Join(rollbackDir, "litepan_favorites.json"), 0o600)
	if err != nil {
		return rollbackManifest{}, err
	}
	manifest.FavoritesExisted = favoritesExisted
	return manifest, nil
}

func copyOptionalFile(source, destination string, mode os.FileMode) (bool, error) {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := atomicReplaceFile(source, destination, mode); err != nil {
		return false, err
	}
	return true, nil
}

func applyStagedFiles(dataDir, dbPath, stageDir string, plan pendingPlan) error {
	if err := atomicReplaceFile(filepath.Join(stageDir, "database", "litepan.db"), dbPath, 0o600); err != nil {
		return fmt.Errorf("replace database: %w", err)
	}
	removeSQLiteSidecars(dbPath)
	if plan.ReplaceSecret {
		if err := atomicReplaceFile(filepath.Join(stageDir, "data", "secret.key"), filepath.Join(dataDir, "secret.key"), 0o600); err != nil {
			return fmt.Errorf("replace secret key: %w", err)
		}
	}
	if plan.ReplaceFavs {
		if err := atomicReplaceFile(filepath.Join(stageDir, "data", "litepan_favorites.json"), filepath.Join(filepath.Dir(dbPath), "litepan_favorites.json"), 0o644); err != nil {
			return fmt.Errorf("replace favorites: %w", err)
		}
	}
	return nil
}

func restoreRollback(ctx context.Context, dataDir, dbPath, rollbackDir string, manifest rollbackManifest) error {
	if manifest.DatabaseExisted {
		if err := atomicReplaceFile(filepath.Join(rollbackDir, "litepan.db"), dbPath, 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	removeSQLiteSidecars(dbPath)
	if err := restoreOptionalFile(filepath.Join(rollbackDir, "secret.key"), filepath.Join(dataDir, "secret.key"), manifest.SecretExisted, 0o600); err != nil {
		return err
	}
	if err := restoreOptionalFile(filepath.Join(rollbackDir, "litepan_favorites.json"), filepath.Join(filepath.Dir(dbPath), "litepan_favorites.json"), manifest.FavoritesExisted, 0o644); err != nil {
		return err
	}
	if manifest.DatabaseExisted {
		return validateStagedDatabase(ctx, dbPath)
	}
	return nil
}

func restoreOptionalFile(source, destination string, existed bool, mode os.FileMode) error {
	if existed {
		return atomicReplaceFile(source, destination, mode)
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateStagedDatabase(ctx context.Context, path string) error {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.IntegrityCheck(ctx); err != nil {
		_ = db.Close()
		return err
	}
	return db.Close()
}

func atomicReplaceFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".restore-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return err
	}
	ok = true
	return nil
}

func removeSQLiteSidecars(dbPath string) {
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
}

func finishPendingRestore(pendingPath, resultPath, stageDir string, result restoreResult) error {
	if err := writeJSONAtomic(resultPath, result); err != nil {
		return fmt.Errorf("write restore result: %w", err)
	}
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending restore: %w", err)
	}
	if err := os.RemoveAll(stageDir); err != nil {
		return fmt.Errorf("remove restore staging: %w", err)
	}
	return nil
}

func statusFromResult(result restoreResult) Status {
	return Status(result)
}

func pruneRollbackDirs(root string, keep int, log *slog.Logger) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		oldest := names[0]
		names = names[1:]
		if err := os.RemoveAll(filepath.Join(root, oldest)); err != nil && log != nil {
			log.Warn("清理旧恢复回滚副本失败", "dir", oldest, "err", err)
		}
	}
}
