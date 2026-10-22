package strm

import "testing"

// 同一集在网盘上有几份不同画质的文件：只留一份。
func TestSelectEpisodeWinnersDropsLowerQuality(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "无可替代 (2026) S01E01 [1080p H.264 AAC].mkv", size: 900, relDirs: []string{"电视剧", "无可替代 (2026) {tmdb-1}", "Season 01"}},
		{fileID: "b", fileName: "无可替代 (2026) S01E01 [1080p H.264 DDP 5.1].mkv", size: 1100, relDirs: []string{"电视剧", "无可替代 (2026) {tmdb-1}", "Season 01"}},
		{fileID: "c", fileName: "无可替代 (2026) S01E01 [2160p H.265 DTS 5.1].mkv", size: 800, relDirs: []string{"电视剧", "无可替代 (2026) {tmdb-1}", "Season 01"}},
	}
	got, skipped := selectEpisodeWinners(items, true, "size_desc", nil)
	if skipped != 2 || len(got) != 1 {
		t.Fatalf("skipped=%d len=%d，期望丢掉 2 份、留 1 份", skipped, len(got))
	}
	// 2160p 体积最小但最清楚 —— 分辨率优先于体积。
	if got[0].fileID != "c" {
		t.Fatalf("留下的是 %s，期望 c（2160p 优先于体积更大的 1080p）", got[0].fileID)
	}
}

// 不同集不能互相吞掉。
func TestSelectEpisodeWinnersKeepsDifferentEpisodes(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "某剧 (2026) S01E01 [1080p].mkv", relDirs: []string{"剧"}},
		{fileID: "b", fileName: "某剧 (2026) S01E02 [1080p].mkv", relDirs: []string{"剧"}},
		{fileID: "c", fileName: "某剧 (2026) S01E02 [2160p].mkv", relDirs: []string{"剧"}},
	}
	got, skipped := selectEpisodeWinners(items, true, "size_desc", nil)
	if skipped != 1 || len(got) != 2 {
		t.Fatalf("skipped=%d len=%d，期望只合并 E02 那两份", skipped, len(got))
	}
}

// 电影名里带数字（`John Wick 4`）不能被当成 S01E04 合并掉。
func TestSelectEpisodeWinnersIgnoresMovieWithNumber(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "John Wick 4 (2023) 1080p.mkv", size: 900, relDirs: []string{"电影"}},
		{fileID: "b", fileName: "John Wick 4 (2023) 2160p.mkv", size: 900, relDirs: []string{"电影"}},
	}
	got, skipped := selectEpisodeWinners(items, true, "size_desc", nil)
	if skipped != 0 || len(got) != 2 {
		t.Fatalf("skipped=%d len=%d，电影目录不该被当剧集合并且（只有 1 个集号、无显式集号标记）", skipped, len(got))
	}
}

// 洗版关着时本地已有的那份优先，哪怕新来的是 2160p。
func TestSelectEpisodeWinnersPrefersLocalWhenUpgradeOff(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "某剧 (2026) S01E01 [1080p].mkv", relDirs: []string{"剧"}},
		{fileID: "b", fileName: "某剧 (2026) S01E01 [2160p].mkv", relDirs: []string{"剧"}},
	}
	exists := func(item mediaCandidate) bool { return item.fileID == "a" }
	got, skipped := selectEpisodeWinners(items, false, "size_desc", exists)
	if skipped != 1 || len(got) != 1 || got[0].fileID != "a" {
		t.Fatalf("skipped=%d god=%v，期望保留本地已有的 a", skipped, got)
	}

	// 同一批候选、洗版开着：换成 2160p 的 b。
	got, _ = selectEpisodeWinners(items, true, "size_desc", exists)
	if len(got) != 1 || got[0].fileID != "b" {
		t.Fatalf("洗版开着时应换成 b，实际 %v", got)
	}
}

// 本地一份都没有时（首次扫描）洗版关着也要挑最好的。
func TestSelectEpisodeWinnersPicksBestOnFirstScan(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "某剧 (2026) S01E01 [1080p].mkv", relDirs: []string{"剧"}},
		{fileID: "b", fileName: "某剧 (2026) S01E01 [2160p].mkv", relDirs: []string{"剧"}},
	}
	exists := func(mediaCandidate) bool { return false }
	got, skipped := selectEpisodeWinners(items, false, "size_desc", exists)
	if skipped != 1 || len(got) != 1 || got[0].fileID != "b" {
		t.Fatalf("skipped=%d god=%v，首次扫描应挑 2160p 的 b", skipped, got)
	}
}

// 文件名叫 `第01集` / 在 Season 目录里，都算显式集号，不需要「多个集号」信号。
func TestSelectEpisodeWinnersExplicitEpisodeSignals(t *testing.T) {
	cn := []mediaCandidate{
		{fileID: "a", fileName: "某剧 第01集 1080p.mkv", relDirs: []string{"剧"}},
		{fileID: "b", fileName: "某剧 第01集 2160p.mkv", relDirs: []string{"剧"}},
	}
	if got, skipped := selectEpisodeWinners(cn, true, "size_desc", nil); skipped != 1 {
		t.Fatalf("`第01集` 应被认作同一集，skipped=%d god=%v", skipped, got)
	}

	seasonDir := []mediaCandidate{
		{fileID: "a", fileName: "01.mkv", relDirs: []string{"剧", "Season 01"}},
		{fileID: "b", fileName: "01.mp4", relDirs: []string{"剧", "Season 01"}},
	}
	if _, skipped := selectEpisodeWinners(seasonDir, true, "size_desc", nil); skipped != 1 {
		t.Fatalf("Season 目录里的 01 应被认作同一集，skipped=%d", skipped)
	}
}

// 番号那种 `ABP-123` 被解析成 S01E123 的，不该被同目录的其它番号吞掉
// —— 它们集号不同，本来也不会同组；这里钉住的是「不会误合并」。
func TestSelectEpisodeWinnersJavNumbersStayDistinct(t *testing.T) {
	items := []mediaCandidate{
		{fileID: "a", fileName: "ABP-123.mkv", relDirs: []string{"番号"}},
		{fileID: "b", fileName: "ABP-124.mkv", relDirs: []string{"番号"}},
		{fileID: "c", fileName: "SSIS-001.mkv", relDirs: []string{"番号"}},
	}
	if _, skipped := selectEpisodeWinners(items, true, "size_desc", nil); skipped != 0 {
		t.Fatalf("不同番号不该被合并，skipped=%d", skipped)
	}
}
