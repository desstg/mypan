//go:build fuse

package fuse

import (
	"context"
	"os"
	"syscall"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/upload"
)

type recordingUploadManager struct {
	tempDir string
	params  []upload.CreateParams
	renamed []string
	deleted []string
}

func (m *recordingUploadManager) Create(_ context.Context, params upload.CreateParams) (*upload.Task, error) {
	m.params = append(m.params, params)
	return &upload.Task{TaskID: "empty-upload"}, nil
}

func (m *recordingUploadManager) TempDir() string { return m.tempDir }

func (m *recordingUploadManager) TempRegistry() *upload.TempRegistry { return nil }

func (m *recordingUploadManager) RenameTask(_ context.Context, taskID, newName, _ string, _ string) (bool, error) {
	m.renamed = append(m.renamed, taskID+":"+newName)
	return true, nil
}

func (m *recordingUploadManager) Delete(_ context.Context, taskID string, _ bool) (bool, error) {
	m.deleted = append(m.deleted, taskID)
	return true, nil
}

func TestStagingUploadFlushQueuesEmptyFile(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "empty-*")
	if err != nil {
		t.Fatalf("创建空文件失败: %v", err)
	}
	manager := &recordingUploadManager{tempDir: t.TempDir()}
	handle := &stagingUploadHandle{
		node:      &stagingFile{},
		uploads:   manager,
		accountID: 1,
		parentID:  "root",
		fileName:  "empty.txt",
		tempPath:  tmp.Name(),
		file:      tmp,
		flags:     syscall.O_WRONLY,
	}
	t.Cleanup(func() { _ = handle.Release(context.Background()) })

	if errno := handle.Flush(context.Background()); errno != 0 {
		t.Fatalf("空文件 Flush 不应失败，errno=%v", errno)
	}
	if len(manager.params) != 1 {
		t.Fatalf("期望创建一个上传任务，实际 %d", len(manager.params))
	}
	if manager.params[0].TotalBytes != 0 {
		t.Fatalf("空文件任务大小应为 0，实际 %d", manager.params[0].TotalBytes)
	}
}

func TestStagingRenameUpdatesNodeAndTask(t *testing.T) {
	manager := &recordingUploadManager{tempDir: t.TempDir()}
	mount := &domain.FuseMount{ID: 1, AccountID: 1}
	dir := &cloudDir{
		b: &backend{deps: Deps{}, mount: mount},
		item: domain.FileItem{
			ID:    "parent-id",
			Name:  "dir",
			IsDir: true,
		},
	}
	st := &stagingFile{
		b:           dir.b,
		name:        "movie.tmp",
		tempPath:    t.TempDir() + "/movie.tmp",
		uploads:     manager,
		taskID:      "task-1",
		parentID:    "parent-id",
		displayPath: "dir",
	}

	if errno := dir.renameStaging(context.Background(), st, dir, "movie.mp4"); errno != 0 {
		t.Fatalf("renameStaging 不应失败，errno=%v", errno)
	}
	if st.name != "movie.mp4" {
		t.Fatalf("节点名应更新为 movie.mp4，实际 %q", st.name)
	}
	if len(manager.renamed) != 1 || manager.renamed[0] != "task-1:movie.mp4" {
		t.Fatalf("应联动更新上传任务名，实际 %v", manager.renamed)
	}
}

func TestStagingUnlinkCancelsTask(t *testing.T) {
	manager := &recordingUploadManager{tempDir: t.TempDir()}
	mount := &domain.FuseMount{ID: 1, AccountID: 1}
	dir := &cloudDir{b: &backend{deps: Deps{}, mount: mount}}
	st := &stagingFile{
		b:        dir.b,
		name:     "movie.tmp",
		tempPath: t.TempDir() + "/movie.tmp",
		uploads:  manager,
		taskID:   "task-1",
	}

	if errno := dir.unlinkStaging(st); errno != 0 {
		t.Fatalf("unlinkStaging 不应失败，errno=%v", errno)
	}
	if len(manager.deleted) != 1 || manager.deleted[0] != "task-1" {
		t.Fatalf("应取消上传任务，实际 %v", manager.deleted)
	}
}
