package strm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalRelPathKeepsExistingNamingWhenISOCompatibilityDisabled(t *testing.T) {
	got := LocalRelPath("影音库", []string{"电影"}, "电影.iso", false)
	want := "影音库/电影/电影.strm"
	if got != want {
		t.Fatalf("LocalRelPath() = %q, want %q", got, want)
	}
}

func TestNormalizeGroupDir(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"电影":        "电影",
		"/电影":       "电影",
		"电影/":       "电影",
		"/电影/港台":    "电影/港台",
		"电影//港台":    "电影/港台",
		"电影/../港台":  "电影/港台",
		"电影\\港台":    "电影/港台",
		" 电影 / 港台 ": "电影/港台",
	}
	for in, want := range cases {
		if got := NormalizeGroupDir(in); got != want {
			t.Errorf("NormalizeGroupDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskRelDirAndLocalRelPathWithGroup(t *testing.T) {
	if got := TaskRelDir("电影/港台", "电影1"); got != "电影/港台/电影1" {
		t.Fatalf("TaskRelDir = %q", got)
	}
	if got := TaskRelDir("", "电影1"); got != "电影1" {
		t.Fatalf("TaskRelDir 空分组 = %q", got)
	}
	rel := LocalRelPath("电影/港台/电影1", []string{"Season 1"}, "1.mp4", false)
	if rel != filepath.Join("电影", "港台", "电影1", "Season 1", "1.strm") {
		t.Fatalf("LocalRelPath 多级 = %q", rel)
	}
}

func TestMetadataRelPathAndLocalTaskDirKeepGroupSegments(t *testing.T) {
	if got := metadataRelPath("电影/光鸭测试", []string{"Season 1"}, "poster.jpg"); got != filepath.Join("电影", "光鸭测试", "Season 1", "poster.jpg") {
		t.Fatalf("metadataRelPath 多级 = %q", got)
	}
	if got := localTaskDir("/tmp/root", "电影/光鸭测试", []string{"Season 1"}); got != filepath.Join("/tmp/root", "电影", "光鸭测试", "Season 1") {
		t.Fatalf("localTaskDir 多级 = %q", got)
	}
	if got := TaskOutputDir("/tmp/root", "电影/光鸭测试"); got != filepath.Join("/tmp/root", "电影", "光鸭测试") {
		t.Fatalf("TaskOutputDir 多级 = %q", got)
	}
}

func TestLocalRelPathUsesISOSuffixWhenCompatibilityEnabled(t *testing.T) {
	got := LocalRelPath("影音库", []string{"电影"}, "电影.iso", true)
	want := "影音库/电影/电影.iso.strm"
	if got != want {
		t.Fatalf("LocalRelPath() = %q, want %q", got, want)
	}
}

func TestAlignMetadataItemsForISO(t *testing.T) {
	items := []metadataItem{
		newMetadataItem("nfo", "电影.nfo", "影音库", []string{"电影"}),
		newMetadataItem("sub", "电影.zh-CN.srt", "影音库", []string{"电影"}),
		newMetadataItem("poster", "电影-poster.jpg", "影音库", []string{"电影"}),
		newMetadataItem("thumb", "电影-thumb.jpg", "影音库", []string{"电影"}),
		newMetadataItem("folder-poster", "poster.jpg", "影音库", []string{"电影"}),
		newMetadataItem("other", "电影2.nfo", "影音库", []string{"电影"}),
		newMetadataItem("direct", "电影.iso.nfo", "影音库", []string{"电影"}),
		newMetadataItem("direct-poster", "电影.iso-poster.jpg", "影音库", []string{"电影"}),
	}
	media := []mediaCandidate{{fileID: "iso", fileName: "电影.iso", relDirs: []string{"电影"}}}

	got := alignMetadataItems("影音库", media, items, true)
	want := []string{
		"影音库/电影/电影.iso.nfo",
		"影音库/电影/电影.iso.zh-CN.srt",
		"影音库/电影/电影.iso-poster.jpg",
		"影音库/电影/电影.iso-thumb.jpg",
		"影音库/电影/poster.jpg",
		"影音库/电影/电影2.nfo",
		"影音库/电影/电影.iso.nfo",
		"影音库/电影/电影.iso-poster.jpg",
	}
	for i, item := range got {
		if item.relPath != want[i] {
			t.Fatalf("item %d relPath = %q, want %q", i, item.relPath, want[i])
		}
	}
	if got[0].legacyRelPath != "影音库/电影/电影.nfo" {
		t.Fatalf("legacyRelPath = %q, want original metadata path", got[0].legacyRelPath)
	}
	if got[6].legacyRelPath != "" || got[7].legacyRelPath != "" {
		t.Fatalf("direct ISO metadata should not be rewritten")
	}
}

func TestAlignMetadataItemsKeepsCompleteDottedISOStem(t *testing.T) {
	items := []metadataItem{
		newMetadataItem("nfo", "电影.2024.nfo", "影音库", []string{"电影"}),
		newMetadataItem("other", "电影.2025.nfo", "影音库", []string{"电影"}),
	}
	media := []mediaCandidate{{fileID: "iso", fileName: "电影.2024.iso", relDirs: []string{"电影"}}}

	got := alignMetadataItems("影音库", media, items, true)
	if got[0].relPath != "影音库/电影/电影.2024.iso.nfo" {
		t.Fatalf("dotted ISO metadata = %q", got[0].relPath)
	}
	if got[1].relPath != "影音库/电影/电影.2025.nfo" {
		t.Fatalf("different metadata = %q", got[1].relPath)
	}
}

func TestMigrateLegacyISOStrmFileMovesOnlyMatchingFile(t *testing.T) {
	root := t.TempDir()
	legacyRelPath := LegacyLocalRelPath("影音库", []string{"电影"}, "电影.iso")
	legacyPath := filepath.Join(root, legacyRelPath)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "https://pan.example.com/api/strm/play/1/" + EncodeFileKey("iso-file") + "/t/token/n/%E7%94%B5%E5%BD%B1.iso\n"
	if err := os.WriteFile(legacyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyISOStrmFile(root, "影音库", []string{"电影"}, "电影.iso", "iso-file", true)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected legacy ISO STRM to migrate")
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy file still exists: %v", err)
	}
	currentPath := filepath.Join(root, LocalRelPath("影音库", []string{"电影"}, "电影.iso", true))
	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("migrated content = %q, want %q", got, content)
	}
}

func TestMigrateLegacyISOStrmFileKeepsDifferentSourceFile(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, LegacyLocalRelPath("影音库", nil, "电影.iso"))
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("https://example.com/"+EncodeFileKey("other-file")), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyISOStrmFile(root, "影音库", nil, "电影.iso", "iso-file", true)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("different source STRM must not migrate")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy file should remain: %v", err)
	}
}

func TestMetadataSyncerMigratesAlignedISOFileWithoutDownload(t *testing.T) {
	root := t.TempDir()
	legacyRelPath := "影音库/电影/电影.nfo"
	legacyPath := filepath.Join(root, legacyRelPath)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("metadata"), 0o644); err != nil {
		t.Fatal(err)
	}

	item := metadataItem{relPath: "影音库/电影/电影.iso.nfo", legacyRelPath: legacyRelPath}
	created, err := (&metadataSyncer{}).syncOne(t.Context(), nil, 1, root, item)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected local metadata migration")
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy metadata still exists: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, item.relPath)); err != nil || string(got) != "metadata" {
		t.Fatalf("migrated metadata = %q, err=%v", got, err)
	}
}
