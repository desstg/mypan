package upload

import (
	"context"
	"testing"

	"litepan/internal/domain"
)

type targetDirTestFiles struct {
	items       map[string][]domain.FileItem
	listCalls   map[string]int
	createCalls []string
}

func (f *targetDirTestFiles) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	if f.listCalls == nil {
		f.listCalls = make(map[string]int)
	}
	f.listCalls[parentID]++
	return append([]domain.FileItem(nil), f.items[parentID]...), nil
}

func (f *targetDirTestFiles) CreateFolder(_ context.Context, _ int64, parentID, name string) (*domain.FileItem, error) {
	item := domain.FileItem{ID: parentID + "-" + name, Name: name, IsDir: true}
	f.createCalls = append(f.createCalls, parentID+"/"+name)
	f.items[parentID] = append(f.items[parentID], item)
	return &item, nil
}

func TestFindByClientTaskIDRestoredAndDeleted(t *testing.T) {
	repo := &failingUploadTaskRepo{
		rows: map[string]*domain.UploadTaskRecord{
			"task-1": {
				TaskID:         "task-1",
				ClientTaskID:   "client:1",
				AccountID:      1,
				AccountName:    "目标盘",
				DriverType:     "mock",
				FileName:       "demo.mkv",
				TargetPath:     "0",
				Status:         StatusSuccess,
				QueueOrder:     1,
				ConflictPolicy: "overwrite",
			},
		},
	}
	m := NewManager(Options{Repo: repo, DataDir: t.TempDir()})

	got := m.FindByClientTaskID("client:1")
	if got == nil || got.TaskID != "task-1" {
		t.Fatalf("restored task not found by clientTaskID: %#v", got)
	}

	found, err := m.Delete(context.Background(), "task-1", false)
	if err != nil || !found {
		t.Fatalf("delete task failed: found=%v err=%v", found, err)
	}
	if got := m.FindByClientTaskID("client:1"); got != nil {
		t.Fatalf("deleted task should not remain indexed: %#v", got)
	}
}

func TestEnsureUploadTargetDirCachesFullPathAndPrefix(t *testing.T) {
	files := &targetDirTestFiles{
		items: map[string][]domain.FileItem{
			"root": {{ID: "tv", Name: "tv", IsDir: true}},
			"tv":   {{ID: "s1", Name: "season1", IsDir: true}},
		},
	}
	cache := newUploadTargetDirCache()

	id, err := ensureUploadTargetDir(context.Background(), files, cache, 1, "root", "tv/season1")
	if err != nil || id != "s1" {
		t.Fatalf("resolve season1 failed: id=%q err=%v", id, err)
	}
	if files.listCalls["root"] != 1 || files.listCalls["tv"] != 1 {
		t.Fatalf("unexpected first list calls: %#v", files.listCalls)
	}

	id, err = ensureUploadTargetDir(context.Background(), files, cache, 1, "root", "tv/season1")
	if err != nil || id != "s1" {
		t.Fatalf("resolve season1 second time failed: id=%q err=%v", id, err)
	}
	if files.listCalls["root"] != 1 || files.listCalls["tv"] != 1 {
		t.Fatalf("full-path cache did not hit: %#v", files.listCalls)
	}

	id, err = ensureUploadTargetDir(context.Background(), files, cache, 1, "root", "tv/season2")
	if err != nil {
		t.Fatalf("resolve season2 failed: %v", err)
	}
	if id == "" {
		t.Fatal("season2 folder id should not be empty")
	}
	if files.listCalls["root"] != 1 {
		t.Fatalf("prefix cache should skip root relist: %#v", files.listCalls)
	}
	if files.listCalls["tv"] != 2 {
		t.Fatalf("season2 should only list tv once more: %#v", files.listCalls)
	}
	if len(files.createCalls) != 1 || files.createCalls[0] != "tv/season2" {
		t.Fatalf("unexpected create calls: %#v", files.createCalls)
	}
}
