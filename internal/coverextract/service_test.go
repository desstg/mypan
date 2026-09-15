package coverextract

import "testing"

func TestExtractionTimes(t *testing.T) {
	tests := []struct {
		name     string
		req      ExtractRequest
		duration int64
		want     int
	}{
		{name: "默认随机三帧", req: ExtractRequest{Mode: "random"}, duration: 60_000, want: 3},
		{name: "片头一秒取一帧", req: ExtractRequest{Mode: "head"}, duration: 60_000, want: 1},
		{name: "极短视频取有效时间", req: ExtractRequest{Mode: "head"}, duration: 1000, want: 1},
		{name: "精确时间", req: ExtractRequest{Mode: "timestamp", TimestampMS: 900}, duration: 1000, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractionTimes(tt.req, tt.duration)
			if err != nil {
				t.Fatalf("extractionTimes() error = %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("len = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestDefaultTargetUsesMediaOrganizeSeasonRules(t *testing.T) {
	for _, season := range []string{"Season 1", "Season 01", "S01", "第一季"} {
		id, path := defaultTarget("season-id", []DirectoryRef{
			{ID: "root-id", Name: "根目录"},
			{ID: "show-id", Name: "胜与败 (2025)"},
			{ID: "season-id", Name: season},
		})
		if id != "show-id" || path != "/胜与败 (2025)" {
			t.Fatalf("%s: id=%q path=%q", season, id, path)
		}
	}
	id, path := defaultTarget("movie-id", []DirectoryRef{{ID: "movie-id", Name: "电影"}})
	if id != "movie-id" || path != "/电影" {
		t.Fatalf("非季目录应保存到视频同目录: id=%q path=%q", id, path)
	}
}

func TestRemoveAlsoDropsCandidateImages(t *testing.T) {
	s := &Service{
		files: map[string]*SessionFile{
			"session-id": {ID: "session-id", Frames: []Frame{{ID: "frame-1"}, {ID: "frame-2"}}},
		},
		frames: map[string]*imageEntry{
			"frame-1": {Data: []byte("one")},
			"frame-2": {Data: []byte("two")},
		},
		imageLen: 6,
	}
	s.Remove("session-id")
	if len(s.files) != 0 || len(s.frames) != 0 || s.imageLen != 0 {
		t.Fatalf("移除视频后候选数据未清空: files=%d frames=%d bytes=%d", len(s.files), len(s.frames), s.imageLen)
	}
}

func TestCloneFileKeepsEmptyFramesAsArray(t *testing.T) {
	cloned := cloneFile(&SessionFile{})
	if cloned.Frames == nil {
		t.Fatal("空候选图必须保持为空数组，不能序列化为 null")
	}
}

func TestFrameMustBelongToSessionFile(t *testing.T) {
	file := &SessionFile{Frames: []Frame{{ID: "frame-1"}}}
	if !frameBelongsToFile(file, "frame-1") {
		t.Fatal("应识别归属当前视频的候选帧")
	}
	if frameBelongsToFile(file, "frame-2") {
		t.Fatal("不能用其它视频的候选帧保存海报")
	}
}

func TestSaveComposedRejectsInvalidSizeBeforeIO(t *testing.T) {
	s := &Service{}
	if _, err := s.SaveComposed(t.Context(), SaveRequest{}, nil); err == nil {
		t.Fatal("空合成海报应被拒绝")
	}
	tooLarge := make([]byte, MaxPosterBytes+1)
	if _, err := s.SaveComposed(t.Context(), SaveRequest{}, tooLarge); err == nil {
		t.Fatal("超过上限的合成海报应被拒绝")
	}
}

func TestExtractionTimesRejectsInvalidInput(t *testing.T) {
	if _, err := extractionTimes(ExtractRequest{Mode: "timestamp", TimestampMS: 60_000}, 60_000); err == nil {
		t.Fatal("超过时长的时间点应拒绝")
	}
}

func TestRandomExtractionTimesUseThreeSeparatedBands(t *testing.T) {
	times, err := randomExtractionTimes(60_000, 3)
	if err != nil || len(times) != 3 {
		t.Fatalf("随机三帧生成失败: times=%v err=%v", times, err)
	}
	bands := [][2]int64{{6_000, 22_000}, {22_000, 38_000}, {38_000, 54_000}}
	for i, ts := range times {
		if ts < bands[i][0] || ts >= bands[i][1] {
			t.Fatalf("第 %d 帧不在预期随机区段内: %d", i+1, ts)
		}
	}
}

func TestHeadFrameUsesOneSecondWithinDuration(t *testing.T) {
	regular, err := extractionTimes(ExtractRequest{Mode: "head"}, 60_000)
	if err != nil || len(regular) != 1 || regular[0] != 1000 {
		t.Fatalf("普通视频应截取片头 1 秒: times=%v err=%v", regular, err)
	}
	short, err := extractionTimes(ExtractRequest{Mode: "head"}, 800)
	if err != nil || len(short) != 1 || short[0] != 799 {
		t.Fatalf("极短视频应落在有效时长内: times=%v err=%v", short, err)
	}
}

func TestFrameAttempts(t *testing.T) {
	fast := frameAttempts(500, 60_000, false)
	if len(fast) < 3 || !fast[0].KeyframeOnly {
		t.Fatalf("快速取帧应全部走关键帧路径、快速失败: %#v", fast)
	}
	for _, attempt := range fast {
		if !attempt.KeyframeOnly {
			t.Fatalf("非精确取帧不得回落精确 seek（避免限速网盘拖长）: %#v", attempt)
		}
		if attempt.TimeMS < 0 || attempt.TimeMS >= 60_000 {
			t.Fatalf("邻近时间必须在视频范围内: %#v", attempt)
		}
	}
	exact := frameAttempts(30_000, 60_000, true)
	if len(exact) != 1 || exact[0].KeyframeOnly || exact[0].TimeMS != 30_000 {
		t.Fatalf("指定时间必须只做准确定位且不得偏移: %#v", exact)
	}
}

func TestLoopbackGuard(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		if !isLoopback(addr) {
			t.Fatalf("%s 应识别为回环", addr)
		}
	}
	if isLoopback("192.168.1.2:1234") {
		t.Fatal("局域网地址不应通过回环检查")
	}
}

func TestSupportedVideoExtensions(t *testing.T) {
	for _, name := range []string{"a.mp4", "A.MKV", "a.mov", "a.webm"} {
		if !IsSupported(name) {
			t.Fatalf("%s 应支持", name)
		}
	}
	if IsSupported("a.avi") {
		t.Fatal("AVI 不在首版产品范围")
	}
}

func TestDurationPattern(t *testing.T) {
	match := durationPattern.FindStringSubmatch("Duration: 01:02:03.45, start: 0.000000")
	if len(match) != 4 || match[1] != "01" || match[2] != "02" || match[3] != "03.45" {
		t.Fatalf("时长解析不正确: %#v", match)
	}
}
