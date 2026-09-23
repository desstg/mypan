package mediaserver

import "testing"

func TestExtractCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SSIS-001.mkv", "SSIS-001"},
		{"SSIS-001-1080p.mkv", "SSIS-001"},
		{"/media/movies/SSIS-001/SSIS-001.mkv", "SSIS-001"},
		{"ABC_123.mp4", "ABC_123"},
		{"FC2PPV1234567.mp4", "FC2PPV1234567"},
		{"ssis-001 中文字幕.mkv", "SSIS-001"},
		{"", ""},
		// 路径里的目录名不该被当成番号：这里是拿文件名去提的。
		{"/media/Studio Name/SSIS-001.mkv", "SSIS-001"},
	}
	for _, c := range cases {
		if got := ExtractCode(c.in); got != c.want {
			t.Errorf("ExtractCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractCodeFallbackAndExclusions(t *testing.T) {
	// 没有标准番号形态时退化成第一个词。
	if got := ExtractCode("abcdefg.mkv"); got != "ABCDEFG" {
		t.Errorf("兜底提取 = %q, want ABCDEFG", got)
	}
	// 这些词长得像番号但其实是片名里的常见词，兜底路径必须跳过它们。
	for _, word := range []string{"THE.mkv", "this-file.mkv", "what.mp4", "with_me.mkv"} {
		if got := ExtractCode(word); got != "" {
			t.Errorf("ExtractCode(%q) = %q，排除词不该被当成番号", word, got)
		}
	}
}

func TestItemQuality(t *testing.T) {
	cases := []struct {
		name       string
		heights    []int
		sizes      []int64
		resolution int
		size       int64
	}{
		{"2160p", []int{2160}, []int64{8 << 30}, 2, 8 << 30},
		{"1080p", []int{1080}, []int64{4 << 30}, 1, 4 << 30},
		{"480p", []int{480}, []int64{700 << 20}, 0, 700 << 20},
		{"无信息", nil, nil, 0, 0},
		// 一个条目同时挂 direct 与 hls 两个源时，取最大那个才是真实体积。
		{"多源", []int{1080, 1080}, []int64{4 << 30, 1 << 30}, 1, 4 << 30},
		// AI 修复过的库会出现 4K 与 1080p 两个源，取高的。
		{"混合", []int{1080, 2160}, []int64{2 << 30, 9 << 30}, 2, 9 << 30},
	}
	for _, c := range cases {
		res, size := ItemQuality(c.heights, c.sizes)
		if res != c.resolution || size != c.size {
			t.Errorf("%s: ItemQuality = (%d, %d), want (%d, %d)", c.name, res, size, c.resolution, c.size)
		}
	}
}
