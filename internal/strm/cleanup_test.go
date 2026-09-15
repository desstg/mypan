package strm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupScopedStaleFilesRemovesSameStemSidecarsOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepStrm := filepath.Join(dir, "保留.strm")
	staleStrm := filepath.Join(dir, "过期.strm")
	staleNFO := filepath.Join(dir, "过期.nfo")
	stalePoster := filepath.Join(dir, "过期-poster.jpg")
	staleThumb := filepath.Join(dir, "过期-thumb.jpg")
	folderNFO := filepath.Join(dir, "tvshow.nfo")
	folderPoster := filepath.Join(dir, "poster.jpg")
	keepNFO := filepath.Join(dir, "保留.nfo")
	for _, p := range []string{keepStrm, staleStrm, staleNFO, stalePoster, staleThumb, folderNFO, folderPoster, keepNFO} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]struct{}{
		"任务/电影/保留.strm": {},
	}
	removed, err := cleanupScopedStaleFiles(
		root,
		"任务",
		seen,
		[]cleanupScope{{relDirs: nil, recursive: true}},
		nil,
		NewFailureCollector(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	for _, p := range []string{staleStrm, staleNFO, stalePoster, staleThumb} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", filepath.Base(p))
		}
	}
	for _, p := range []string{keepStrm, keepNFO, folderNFO, folderPoster} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 应保留: %v", filepath.Base(p), err)
		}
	}
}

func TestCleanupMovedMediaSidecars(t *testing.T) {
	for _, keepVersion := range []bool{false, true} {
		t.Run(map[bool]string{false: "移走后删除空目录", true: "保留其他版本字幕"}[keepVersion], func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "任务", "旧目录")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			names := []string{"影片.strm", "影片-fanart.JPG", "影片.ass", "影片.zh-Hans.ass", "影片.en.forced.srt", "影片.idx", "影片.sub"}
			names = append(names, "poster.jpg", "fanart.jpg", "movie.nfo")
			seen := map[string]struct{}{}
			if keepVersion {
				names = append(names, "影片.加长版.strm", "影片.加长版.zh.ass")
				seen["任务/旧目录/影片.加长版.strm"] = struct{}{}
			}
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			n, err := cleanupScopedStaleFiles(root, "任务", seen, []cleanupScope{{recursive: true}}, nil, NewFailureCollector())
			if err != nil || n != 1 {
				t.Fatalf("清理结果 %d, %v", n, err)
			}
			if !keepVersion {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("旧目录仍然存在: %v", err)
				}
			} else {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 5 {
					t.Fatalf("应只保留另一版本及共用海报: %v, %v", entries, err)
				}
			}
		})
	}
}
