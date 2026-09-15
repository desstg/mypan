package strm

import "testing"

func TestClassifyScanFileSharedRules(t *testing.T) {
	exts := map[string]struct{}{"mkv": {}}
	metaExts := map[string]struct{}{"nfo": {}}
	relDirs := []string{"电视剧", "Season 1"}

	t.Run("media file", func(t *testing.T) {
		got := classifyScanFile("video-1", "第01集.mkv", "任务", 128, relDirs, exts, metaExts, 64, 1024, true)
		if !got.hasMedia || got.hasMetadata {
			t.Fatalf("媒体文件分类错误: %+v", got)
		}
		if got.media.fileID != "video-1" || got.media.fileName != "第01集.mkv" {
			t.Fatalf("媒体文件信息错误: %+v", got.media)
		}
	})

	t.Run("metadata file", func(t *testing.T) {
		got := classifyScanFile("meta-1", "tvshow.nfo", "任务", 32, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || !got.hasMetadata {
			t.Fatalf("元数据文件分类错误: %+v", got)
		}
		if got.metadata.relPath == "" || got.metadata.fileName != "tvshow.nfo" {
			t.Fatalf("元数据文件信息错误: %+v", got.metadata)
		}
	})

	t.Run("small media filtered", func(t *testing.T) {
		got := classifyScanFile("video-2", "第02集.mkv", "任务", 32, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("小媒体文件应被过滤: %+v", got)
		}
	})

	t.Run("large metadata filtered", func(t *testing.T) {
		got := classifyScanFile("meta-2", "movie.nfo", "任务", 2048, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("过大元数据文件应被过滤: %+v", got)
		}
	})

	t.Run("metadata sync disabled", func(t *testing.T) {
		got := classifyScanFile("meta-3", "movie.nfo", "任务", 32, relDirs, exts, metaExts, 64, 1024, false)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("关闭元数据同步后应忽略 nfo: %+v", got)
		}
	})
}
