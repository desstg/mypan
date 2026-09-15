package upload_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
	"litepan/internal/upload"
)

func TestUploadTaskRestoreAfterRestart(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	dataDir := t.TempDir()
	localPath := filepath.Join(dataDir, "upload_tasks", "sample.bin")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &domain.UploadTaskRecord{
		TaskID:           "abc123",
		AccountID:        1,
		AccountName:      "a",
		DriverType:       "mock",
		FileName:         "sample.bin",
		TargetPath:       "0",
		Status:           upload.StatusRunning,
		TotalBytes:       5,
		Message:          "running",
		CleanupLocalMode: upload.CleanupLocalPathOnSuccess,
		CleanupLocalPath: filepath.Dir(localPath),
		QueueOrder:       1,
		LocalPath:        localPath,
		ConflictPolicy:   "overwrite",
	}
	if err := st.UploadTasks.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}

	m := upload.NewManager(upload.Options{
		Repo:    st.UploadTasks,
		DataDir: dataDir,
	})
	task, ok := m.Get(ctx, "abc123")
	if !ok {
		t.Fatal("task not restored")
	}
	if task.Status != upload.StatusPaused {
		t.Fatalf("status=%q want paused", task.Status)
	}
	if task.Message != "进程重启，上传已暂停" {
		t.Fatalf("message=%q", task.Message)
	}
	if task.CleanupLocalMode != upload.CleanupLocalPathOnSuccess {
		t.Fatalf("cleanup mode=%q", task.CleanupLocalMode)
	}
	if task.CleanupLocalPath != filepath.Dir(localPath) {
		t.Fatalf("cleanup path=%q", task.CleanupLocalPath)
	}
}
