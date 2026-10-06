package tgsubscribe

import "testing"

// 从**已落盘的文件名**里解集号：只认明确写着集号的形态。
//
// 错记一集比漏记一集糟得多 —— episodes 表是订阅进度的唯一依据，
// 错记会让详情页把没有的集标成绿的，而且再也纠正不回来。
func TestParseEpisodeFromFileName(t *testing.T) {
	cases := []struct {
		name          string
		season        int
		episode       int
		ok            bool
	}{
		{"Show.S01E05.1080p.mkv", 1, 5, true},
		{"Show.s01e05.mkv", 1, 5, true},
		{"Show.1x05.mkv", 1, 5, true},
		{"Show.E05.mkv", 0, 5, true},
		{"Show.EP05.mkv", 0, 5, true},
		{"剧名.第05集.mp4", 0, 5, true},
		{"剧名 第5话.mp4", 0, 5, true},
		{"剧名 第5話.mp4", 0, 5, true},
		// 路径段带季号
		{"Season 1/Show.E05.mkv", 1, 5, true},
		{"第一季/剧名.第5集.mp4", 1, 5, true},
		{"S01/Show.E05.mkv", 1, 5, true},

		// 认不出的：裸数字最容易错记，宁可漏。
		{"电影.2026.1080p.WEB-DL.mkv", 0, 0, false},
		{"Show.2160p.DDP5.1.Atmos.mkv", 0, 0, false},
		{"Show.HEVC.MAXX.mkv", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		s, e, ok := parseEpisodeFromFileName(c.name)
		if ok != c.ok || (ok && (s != c.season || e != c.episode)) {
			t.Errorf("parseEpisodeFromFileName(%q) = (%d,%d,%v)，want (%d,%d,%v)",
				c.name, s, e, ok, c.season, c.episode, c.ok)
		}
	}
}

// 发布组名里带 E 的数字不能被当成集号 —— 这是最容易错记的一类。
func TestParseEpisodeIgnoresReleaseGroups(t *testing.T) {
	for _, name := range []string{
		"Show.2026.1080p.WEB-DL.DDP5.1.H.264-MAXX.mkv",
		"Show.1080p.BluRay.x264-EPSiLON.mkv",
		"Show.2160p.WEB-DL.DV.HDR.DDP5.1.Atmos.H.265-DreamHD.mkv",
	} {
		if _, e, ok := parseEpisodeFromFileName(name); ok {
			t.Errorf("%q 不该解出集号，实际 E%d", name, e)
		}
	}
}
