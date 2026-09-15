package strm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/file"
)

func TestScanTaskSkipsOversizedDirectoryWithoutFailingTask(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("界", 86)
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {
			{ID: "long-dir", Name: longDir, IsDir: true},
		},
		"long-dir": {
			{ID: "season-1", Name: "Season 1", IsDir: true},
		},
		"season-1": {
			{ID: "episode", Name: "第01集.mkv", Size: 1024},
			{ID: "iso", Name: "特别篇.iso", Size: 2048},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeFullSync,
		Extensions:   "mkv;iso",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:   files,
		StrmDir: t.TempDir(),
		Settings: ScanSettings{
			ISOFilenameEnabled: true,
		},
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("超长目录只应跳过，不应导致任务失败：%v", err)
	}
	if result.ScannedCount != 2 {
		t.Fatalf("扫描文件数=%d，期望=2", result.ScannedCount)
	}
	if result.GeneratedCount != 0 {
		t.Fatalf("超长目录下不应生成 STRM，实际=%d", result.GeneratedCount)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("同一超长目录应汇总一个失败项，实际=%d：%v", len(result.Failures), result.Failures)
	}
	wantPath := filepath.ToSlash(filepath.Join("任务", longDir))
	if result.Failures[0].Path != wantPath || result.Failures[0].Reason != pathTooLongDirReason {
		t.Fatalf("失败项=%+v，期望路径=%q", result.Failures[0], wantPath)
	}
}

func TestScanTaskReportsOversizedFileName(t *testing.T) {
	t.Parallel()

	longFile := strings.Repeat("影", 86) + ".mkv"
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {
			{ID: "movie", Name: longFile, Size: 1024},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           2,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: t.TempDir()}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("超长文件名只应跳过，不应导致任务失败：%v", err)
	}
	if len(result.Failures) != 1 || result.Failures[0].Reason != pathTooLongFileReason {
		t.Fatalf("失败项=%+v", result.Failures)
	}
}
