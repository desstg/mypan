package strm

import (
	"testing"

	"litepan/internal/eventbus"
)

func TestDirectoryMutationInvalidatesCachedSubtree(t *testing.T) {
	for _, op := range []string{"rename", "move", "delete"} {
		t.Run(op, func(t *testing.T) {
			cache := newMemDirCache()
			cache.m["show"] = "电视剧/藏锋 (2026)"
			cache.m["season1"] = "电视剧/藏锋 (2026)/Season 01"
			cache.m["episodeExtras"] = "电视剧/藏锋 (2026)/花絮"
			cache.m["other"] = "电视剧/藏锋2 (2026)"
			svc := NewService(ServiceOptions{DirCache: cache})

			event := eventbus.FileMutated{AccountID: 1, Op: op}
			if op == "rename" {
				event.FileID = "show"
			} else {
				event.FileIDs = []string{"show"}
			}
			svc.OnFileMutated(t.Context(), event)

			for _, id := range []string{"show", "season1", "episodeExtras"} {
				if _, ok, _ := cache.Get(t.Context(), 1, id); ok {
					t.Fatalf("%s 后目录及子孙映射应失效，仍存在 %s", op, id)
				}
			}
			if path, ok, _ := cache.Get(t.Context(), 1, "other"); !ok || path != "电视剧/藏锋2 (2026)" {
				t.Fatalf("%s 不应误删同名前缀目录，实际 path=%q ok=%v", op, path, ok)
			}
		})
	}
}

func TestFileMutationDoesNotInvalidateDirectoryCache(t *testing.T) {
	cache := newMemDirCache()
	cache.m["show"] = "电视剧/藏锋 (2026)"
	svc := NewService(ServiceOptions{DirCache: cache})

	svc.OnFileMutated(t.Context(), eventbus.FileMutated{
		AccountID: 1,
		Op:        "rename",
		FileID:    "video-file-id",
	})

	if path, ok, _ := cache.Get(t.Context(), 1, "show"); !ok || path != "电视剧/藏锋 (2026)" {
		t.Fatalf("普通文件改名不应影响目录映射，实际 path=%q ok=%v", path, ok)
	}
}
