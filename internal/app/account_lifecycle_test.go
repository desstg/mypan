package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/favorites"
	"litepan/internal/store"
	"litepan/internal/upload"
)

func TestAccountLifecycleDeleteRemovesFavorites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	favoritesSvc := favorites.NewService(filepath.Join(dir, "litepan.db"), logger)
	ctx := context.Background()
	for _, accountID := range []int64{11, 22} {
		if _, err := favoritesSvc.Put(ctx, accountID, favorites.AccountState{
			Items: []favorites.Item{{
				ID:   "folder",
				Name: "收藏目录",
				Crumbs: []favorites.Crumb{{
					ID:   "root",
					Name: "根目录",
				}},
			}},
		}); err != nil {
			t.Fatalf("保存账号 %d 收藏失败: %v", accountID, err)
		}
	}

	lifecycle := accountLifecycle{favorites: favoritesSvc}
	if err := lifecycle.OnAccountDeleted(ctx, 11); err != nil {
		t.Fatalf("执行账号删除生命周期失败: %v", err)
	}
	deleted, err := favoritesSvc.Get(ctx, 11)
	if err != nil {
		t.Fatalf("读取目标账号收藏失败: %v", err)
	}
	if len(deleted.Items) != 0 {
		t.Fatalf("账号删除生命周期未清理收藏: %#v", deleted)
	}
	kept, err := favoritesSvc.Get(ctx, 22)
	if err != nil {
		t.Fatalf("读取其他账号收藏失败: %v", err)
	}
	if len(kept.Items) != 1 {
		t.Fatalf("其他账号收藏被误清理: %#v", kept)
	}
}

func TestAccountLifecycleDeleteRemovesRelatedUploadTasks(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stores := store.New(db)
	root := t.TempDir()
	paths := map[string]string{
		"target":          filepath.Join(root, "target.bin"),
		"source-download": filepath.Join(root, "source-download.bin"),
		"source-upload":   filepath.Join(root, "source-upload.bin"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("demo"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rows := []*domain.UploadTaskRecord{
		{
			TaskID: "target", AccountID: 11, AccountName: "待删除目标盘", DriverType: "mock",
			FileName: "target.bin", SourceType: upload.SourceTypeOfflineHandoff, Status: upload.StatusFailed,
			Phase: upload.PhaseUploading, LocalPath: paths["target"], CleanupLocalMode: upload.CleanupLocalFileOnSuccess,
			CleanupLocalPath: paths["target"], CreatedAt: 1, UpdatedAt: 1,
		},
		{
			TaskID: "source-download", AccountID: 22, AccountName: "保留目标盘", DriverType: "mock",
			FileName: "source-download.bin", SourceType: upload.SourceTypeCrossTransfer, SourceAccountID: 11,
			Status: upload.StatusFailed, Phase: upload.PhaseDownloading, LocalPath: paths["source-download"],
			CleanupLocalMode: upload.CleanupLocalFileOnSuccess, CleanupLocalPath: paths["source-download"], CreatedAt: 2, UpdatedAt: 2,
		},
		{
			TaskID: "source-upload", AccountID: 22, AccountName: "保留目标盘", DriverType: "mock",
			FileName: "source-upload.bin", SourceType: upload.SourceTypeCrossTransfer, SourceAccountID: 11,
			Status: upload.StatusFailed, Phase: upload.PhaseUploading, LocalPath: paths["source-upload"],
			CleanupLocalMode: upload.CleanupLocalFileOnSuccess, CleanupLocalPath: paths["source-upload"], CreatedAt: 3, UpdatedAt: 3,
		},
	}
	for _, row := range rows {
		if err := stores.UploadTasks.Upsert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	uploads := upload.NewManager(upload.Options{Repo: stores.UploadTasks, DataDir: t.TempDir()})
	lifecycle := accountLifecycle{uploads: uploads}
	if err := lifecycle.OnAccountDeleted(ctx, 11); err != nil {
		t.Fatal(err)
	}
	remaining := uploads.List(ctx, 0)
	if len(remaining) != 1 || remaining[0].TaskID != "source-upload" {
		t.Fatalf("账号删除后的上传任务不正确: %#v", remaining)
	}
	for _, key := range []string{"target", "source-download"} {
		if _, err := os.Stat(paths[key]); !os.IsNotExist(err) {
			t.Fatalf("关联任务本地文件 %s 未清理: %v", key, err)
		}
	}
	if _, err := os.Stat(paths["source-upload"]); err != nil {
		t.Fatalf("已进入目标盘上传阶段的任务不应因源账号删除而清理: %v", err)
	}
}
