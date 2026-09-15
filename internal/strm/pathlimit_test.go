package strm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOversizedPathFailureUsesUTF8BytesAndDirectoryPrefix(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("界", 86) // 258 字节
	relPath := filepath.Join("任务", longDir, "第01集.strm")
	gotPath, gotReason, oversized := oversizedPathFailure(relPath, false)
	if !oversized {
		t.Fatal("超长中文目录应按 UTF-8 字节数识别")
	}
	wantPath := filepath.Join("任务", longDir)
	if gotPath != wantPath {
		t.Fatalf("失败路径=%q，期望=%q", gotPath, wantPath)
	}
	if gotReason != pathTooLongDirReason {
		t.Fatalf("失败原因=%q，期望=%q", gotReason, pathTooLongDirReason)
	}
}

func TestPathComponentLimitAllowsExactly255Bytes(t *testing.T) {
	t.Parallel()

	allowed := strings.Repeat("a", 250) + ".strm"
	if pathHasOversizedComponent(filepath.Join("任务", allowed)) {
		t.Fatal("恰好 255 字节的文件名应允许")
	}
	tooLong := strings.Repeat("a", 251) + ".strm"
	if !pathHasOversizedComponent(filepath.Join("任务", tooLong)) {
		t.Fatal("256 字节的文件名应识别为超限")
	}
}

func TestFailureCollectorDeduplicatesOversizedDirectory(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	failures := NewFailureCollector()
	for _, name := range []string{"第01集.strm", "第02集.strm", "海报.jpg"} {
		kind := ScanFailureStrm
		if strings.HasSuffix(name, ".jpg") {
			kind = ScanFailureMetadata
		}
		addOversizedPathFailure(failures, kind, filepath.Join("任务", longDir, name), false)
	}

	items := failures.Items()
	if len(items) != 2 {
		t.Fatalf("相同超长目录应按类型各汇总一项，实际=%d：%v", len(items), items)
	}
	if items[0].Path != filepath.ToSlash(filepath.Join("任务", longDir)) {
		t.Fatalf("STRM 汇总路径=%q", items[0].Path)
	}
	if items[1].Kind != ScanFailureMetadata {
		t.Fatalf("第二项类型=%q，期望 metadata", items[1].Kind)
	}
}

func TestCleanupFunctionsIgnoreOversizedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	longDir := strings.Repeat("超", 86)
	failures := NewFailureCollector()

	removed, err := cleanupScopedStaleFiles(
		root,
		"任务",
		nil,
		[]cleanupScope{{relDirs: []string{longDir}, recursive: true}},
		nil,
		failures,
	)
	if err != nil {
		t.Fatalf("全量清理不应因超长目录失败：%v", err)
	}
	if removed != 0 {
		t.Fatalf("删除数=%d，期望=0", removed)
	}
	if failures.Len() != 1 {
		t.Fatalf("超长清理目录应记录一次，实际=%d", failures.Len())
	}

	if _, err := cleanupCurrentDirectoryStrm(root, "任务", []string{longDir}, nil, nil); err != nil {
		t.Fatalf("当前目录清理不应因超长目录失败：%v", err)
	}
}

func TestMigrateLegacyISOIgnoresOversizedPath(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	migrated, err := MigrateLegacyISOStrmFile(t.TempDir(), "任务", []string{longDir}, "影片.iso", "file-id", true)
	if err != nil {
		t.Fatalf("ISO 迁移不应访问超长路径：%v", err)
	}
	if migrated {
		t.Fatal("不存在的超长旧文件不应报告已迁移")
	}
}

func TestPendingMetadataRecordsOversizedDirectoryOnce(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	items := []metadataItem{
		{relPath: filepath.Join("任务", longDir, "poster.jpg")},
		{relPath: filepath.Join("任务", longDir, "movie.nfo")},
	}
	failures := NewFailureCollector()
	if got := pendingMetadataItems(t.TempDir(), items, failures); len(got) != 0 {
		t.Fatalf("超长元数据不应进入下载队列：%v", got)
	}
	if failures.Len() != 1 {
		t.Fatalf("同一超长元数据目录应汇总一次，实际=%d", failures.Len())
	}
	if got := failures.Items()[0]; got.Kind != ScanFailureMetadata || got.Reason != pathTooLongDirReason {
		t.Fatalf("元数据失败项=%+v", got)
	}
}
