package strm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
)

func createOutputMoveTask(t *testing.T, svc *Service, st *store.Store, groupDir string) *domain.StrmTask {
	t.Helper()
	ctx := context.Background()
	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name:       "测试账号",
		DriverType: "LocalFs",
		Config:     "{}",
		IsActive:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, &domain.StrmTask{
		Name:         "夸克电影",
		AccountID:    accountID,
		ParentID:     "0",
		Path:         "/电影",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		OutputFolder: "夸克电影",
		GroupDir:     groupDir,
		ScheduleMode: domain.StrmScheduleWindow,
		Status:       domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestUpdateTaskMovesOutputDirectoryAndCleansEmptyParents(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "测试/电影")

	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	oldSeasonDir := filepath.Join(oldDir, "Season 1")
	if err := os.MkdirAll(oldSeasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldSeasonDir, "EP01.strm")
	if err := os.WriteFile(oldFile, []byte("https://example.test/1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, metadataPath := range []string{
		filepath.Join(filepath.Dir(oldDir), ".DS_Store"),
		filepath.Join(filepath.Dir(oldDir), "desktop.ini"),
		filepath.Join(filepath.Dir(filepath.Dir(oldDir)), ".directory"),
	} {
		if err := os.WriteFile(metadataPath, []byte("system metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	input := *task
	input.GroupDir = ""
	updated, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GroupDir != "" {
		t.Fatalf("分组目录 = %q，期望为空", updated.GroupDir)
	}

	newFile := filepath.Join(svc.strmDir, task.OutputFolder, "Season 1", "EP01.strm")
	content, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatalf("新路径未保留 STRM 文件：%v", err)
	}
	if string(content) != "https://example.test/1\n" {
		t.Fatalf("移动后文件内容 = %q", string(content))
	}
	if _, err := os.Stat(filepath.Join(svc.strmDir, "测试")); !os.IsNotExist(err) {
		t.Fatalf("旧空分组目录应被清理，得到 err=%v", err)
	}
	if _, err := os.Stat(svc.strmDir); err != nil {
		t.Fatalf("STRM 根目录不应被删除：%v", err)
	}
}

func TestRemovableSystemMetadataNames(t *testing.T) {
	dir := t.TempDir()
	removable := []string{
		".DS_Store",
		"._poster.jpg",
		".localized",
		".LSOverride",
		".VolumeIcon.icns",
		"Icon\r",
		"Thumbs.db",
		"ehthumbs.db",
		"ehthumbs_vista.db",
		"desktop.ini",
		".directory",
		".hidden",
		".xdg-volume-info",
	}
	for _, name := range removable {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !isRemovableSystemMetadata(entry) {
			t.Errorf("系统元数据 %q 应可忽略", entry.Name())
		}
	}

	for _, name := range []string{".keep", ".env", ".nfs123", "用户文件.txt"} {
		t.Run(name, func(t *testing.T) {
			testDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(testDir, name), []byte("user data"), 0o644); err != nil {
				t.Fatal(err)
			}
			userEntries, err := os.ReadDir(testDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(userEntries) != 1 || isRemovableSystemMetadata(userEntries[0]) {
				t.Fatalf("用户文件 %q 不应被当作系统元数据", name)
			}
		})
	}
}

func TestUpdateTaskKeepsNonEmptyOldParent(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影/国产")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(svc.strmDir, "电影", "其他任务", "keep.strm")
	if err := os.MkdirAll(filepath.Dir(keepFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := *task
	input.GroupDir = ""
	if _, err := svc.UpdateTask(context.Background(), task.ID, &input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("清理旧空目录时不应影响其他任务：%v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.strmDir, "电影", "国产")); !os.IsNotExist(err) {
		t.Fatalf("已变空的旧子目录应被清理，得到 err=%v", err)
	}
}

func TestUpdateTaskChangesPathWhenOldOutputDoesNotExist(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")

	input := *task
	input.GroupDir = ""
	updated, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GroupDir != "" {
		t.Fatalf("旧输出目录不存在时仍应保存新路径，得到 %q", updated.GroupDir)
	}
	if _, err := os.Stat(TaskOutputDir(svc.strmDir, task.OutputFolder)); !os.IsNotExist(err) {
		t.Fatalf("保存配置不应预先创建空的任务目录，得到 err=%v", err)
	}
}

func TestUpdateTaskRejectsWhileTaskFileOperationIsBusy(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	release, ok := svc.TryBeginTaskFileOperation(task.ID)
	if !ok {
		t.Fatal("测试文件锁加锁失败")
	}
	defer release()

	input := *task
	input.GroupDir = ""
	_, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err == nil || !strings.Contains(err.Error(), "当前任务正在进行，请停止后再修改设置") {
		t.Fatalf("任务忙碌时应立即拒绝修改，得到 err=%v", err)
	}
	stored, getErr := svc.GetTask(context.Background(), task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.GroupDir != "电影" {
		t.Fatalf("被拒绝后不应改变分组目录，得到 %q", stored.GroupDir)
	}
}

func TestUpdateTaskRejectsExistingDestination(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	newDir := TaskOutputDir(svc.strmDir, task.OutputFolder)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	input := *task
	input.GroupDir = ""
	_, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err == nil || !strings.Contains(err.Error(), "新的 STRM 输出目录已存在") {
		t.Fatalf("目标目录已存在时应拒绝合并，得到 err=%v", err)
	}
	stored, getErr := svc.GetTask(context.Background(), task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.GroupDir != "电影" {
		t.Fatalf("目录冲突时不应保存新配置，得到 %q", stored.GroupDir)
	}
}

type failingStrmTaskUpdateRepo struct {
	domain.StrmTaskRepository
}

func (r failingStrmTaskUpdateRepo) Update(context.Context, *domain.StrmTask) error {
	return errors.New("模拟数据库保存失败")
}

func TestUpdateTaskRollsDirectoryBackWhenDatabaseUpdateFails(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	oldFile := filepath.Join(oldDir, "movie.strm")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.repo = failingStrmTaskUpdateRepo{StrmTaskRepository: st.StrmTasks}

	input := *task
	input.GroupDir = ""
	if _, err := svc.UpdateTask(context.Background(), task.ID, &input); err == nil {
		t.Fatal("期望返回数据库保存错误")
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("数据库保存失败后应回移原目录：%v", err)
	}
	newDir := TaskOutputDir(svc.strmDir, task.OutputFolder)
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("数据库保存失败后不应留下新目录，得到 err=%v", err)
	}
}
