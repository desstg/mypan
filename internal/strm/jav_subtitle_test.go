package strm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"litepan/internal/jav/emby"
	"litepan/internal/jav/subtitle"
	"litepan/internal/settings"
)

// 字幕那一步的用例。
//
// 桩记账的不只是「调了几次」，还有**关键词是什么** —— 「番号在前、标题在后」这条
// 兜底顺序是实测踩出来的（纯番号经常搜不到），它不该在重构里悄悄消失。

// stubSubtitles 记账的字幕桩。
type stubSubtitles struct {
	mu    sync.Mutex
	calls int
	// seen 记下每次调用收到的关键词串（用于断言兜底顺序）。
	seen [][]string
	// result 非 nil 时原样返回；nil 表示「搜不到」。
	result *subtitle.Result
	err    error
}

func (s *stubSubtitles) BestSubtitle(_ context.Context, keywords []string) (*subtitle.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.seen = append(s.seen, append([]string(nil), keywords...))
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func (s *stubSubtitles) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubSubtitles) lastKeywords() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		return nil
	}
	return s.seen[len(s.seen)-1]
}

const sampleSRT = "1\n00:00:01,000 --> 00:00:02,000\n这是一个测试字幕，说明时间够长了\n"

func zhSubtitle() *subtitle.Result {
	return &subtitle.Result{
		Data: []byte(sampleSRT),
		Ext:  "srt",
		Lang: subtitle.LangZHCN,
		Item: subtitle.Item{Name: "SSIS-001.srt", Duration: 1000},
	}
}

// javSubtitleRequest 造一个「开了字幕」的请求。
func javSubtitleRequest(t *testing.T, root, rel string, items settings.JavMetaItems, sub JavSubtitleFetcher) javArtifactRequest {
	t.Helper()
	return javArtifactRequest{
		Root:      root,
		StrmFiles: []string{rel},
		Items:     items,
		Images:    newStubFetcher(),
		Subtitles: sub,
		Log:       testLogger(t),
	}
}

// 勾了字幕 → 写出 `<主干>.zh-CN.srt`，且上游只打一次。
func TestJavSubtitleWritesEmbyNamedFile(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	stub := &stubSubtitles{result: zhSubtitle()}

	res := generateJavArtifacts(context.Background(),
		javSubtitleRequest(t, root, rel, allOn(), stub))

	if res.Subtitle != 1 {
		t.Fatalf("该写出 1 份字幕，got %d", res.Subtitle)
	}
	// 文件名必须是 Emby 认的形态：主干逐字同名 + 语言码 + 扩展名。
	want := filepath.Join(root, "SSIS-001-UC-4K.zh-CN.srt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("字幕没写到 %s：%v", want, err)
	}
	if string(data) != sampleSRT {
		t.Errorf("字幕内容不对：%q", data)
	}
	if stub.callCount() != 1 {
		t.Errorf("上游该只打一次，got %d", stub.callCount())
	}
	// 关键词顺序：番号在前、标题在后（样本侧车的 number=SSIS-001、title=样本标题）。
	kw := stub.lastKeywords()
	if len(kw) != 2 || kw[0] != "SSIS-001" || kw[1] != "样本标题" {
		t.Errorf("关键词该是 [番号, 标题]，got %v", kw)
	}
}

// nfo 里 <subtitle> 的 codec/language 必须与旁边那份文件一致 ——
// 写死会在「nfo 说 srt、文件是 ass」时留下静默不一致。
func TestJavSubtitleFeedsNFO(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	stub := &stubSubtitles{result: &subtitle.Result{
		Data: []byte("1\n00:00:01,000 --> 00:00:02,000\n這是一個測試字幕，說明時間夠長\n"),
		Ext:  "ass",
		Lang: subtitle.LangZHTW,
	}}
	generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), stub))

	nfo, err := os.ReadFile(filepath.Join(root, "SSIS-001-UC-4K.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	xml := string(nfo)
	if !strings.Contains(xml, "<codec>ass</codec>") || !strings.Contains(xml, "<language>zh-TW</language>") {
		t.Errorf("nfo 的 <subtitle> 该跟着实际落盘的字幕走：\n%s", xml)
	}
}

// 目录里已经有字幕（哪怕名字与我们写的不同）→ **零上游请求**。
//
// 这条是整个功能最该保守的地方：用户手改过时间轴的那份，覆盖掉不可恢复。
func TestJavSubtitleSkipsWhenLocalExists(t *testing.T) {
	for _, existing := range []string{"SSIS-001-UC-4K.chs.srt", "SSIS-001-UC-4K.简中.srt", "SSIS-001-UC-4K.ass"} {
		root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
		if err := os.WriteFile(filepath.Join(root, existing), []byte(sampleSRT), 0o644); err != nil {
			t.Fatal(err)
		}
		stub := &stubSubtitles{result: zhSubtitle()}

		res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), stub))

		if stub.callCount() != 0 {
			t.Errorf("已有 %s 时不该打上游，got %d 次", existing, stub.callCount())
		}
		if res.Subtitle != 0 {
			t.Errorf("已有 %s 时不该再写，got %d", existing, res.Subtitle)
		}
		// 既有那份必须原封不动。
		data, err := os.ReadFile(filepath.Join(root, existing))
		if err != nil || string(data) != sampleSRT {
			t.Errorf("既有字幕 %s 被动了：%v %q", existing, err, data)
		}
	}
}

// 既有字幕的信息要喂给 nfo（跳过下载不等于 nfo 里什么都不写）。
//
// ⚠️ 语言段要**转成 Emby 认的代码**再写：既有字幕是别人起的名字，`chs` 这种
// Emby 不认的写法直接抄进 <language> 等于把 nfo 也写坏。
func TestJavSubtitleExistingFeedsNFO(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC-4K.chs.ass"), []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), &stubSubtitles{}))

	nfo, err := os.ReadFile(filepath.Join(root, "SSIS-001-UC-4K.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	xml := string(nfo)
	if !strings.Contains(xml, "<codec>ass</codec>") {
		t.Errorf("既有字幕的扩展名该写进 nfo：\n%s", xml)
	}
	if !strings.Contains(xml, "<language>zh-CN</language>") {
		t.Errorf("既有字幕的 `chs` 该被转成 Emby 认的 zh-CN：\n%s", xml)
	}
}

// 文件名里的语言标记 → Emby 代码。
func TestEmbyLanguageFromTag(t *testing.T) {
	cases := map[string]string{
		"zh-CN": "zh-CN", "chs": "zh-CN", "zh": "zh-CN", "简中": "zh-CN", "sc": "zh-CN",
		"zh-TW": "zh-TW", "cht": "zh-TW", "big5": "zh-TW", "繁": "zh-TW",
		"eng": "eng", "en": "eng", "english": "eng",
		"jpn": "jpn", "jp": "jpn", "kor": "kor", "kr": "kor",
		// 认不出的（比如某些组自造的标记）返回空串：nfo 那边会回落到样本那套值，
		// 比抄一个 Emby 认不出的代码进去好。
		"": "", "v2": "", "whatever": "",
	}
	for tag, want := range cases {
		if got := embyLanguageFromTag(tag); got != want {
			t.Errorf("embyLanguageFromTag(%q) = %q，期望 %q", tag, got, want)
		}
	}
}

// 「字幕」没勾 → 一次都不打上游。
func TestJavSubtitleDisabledByToggle(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	items := allOn()
	items.Subtitle = false
	stub := &stubSubtitles{result: zhSubtitle()}

	res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, items, stub))

	if stub.callCount() != 0 {
		t.Errorf("没勾字幕时不该打上游，got %d 次", stub.callCount())
	}
	if res.Subtitle != 0 {
		t.Errorf("没勾字幕时不该写文件，got %d", res.Subtitle)
	}
}

// 没有字幕抓取器（老测试 / 未接线）→ 静默跳过，不 panic。
func TestJavSubtitleNilFetcher(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), nil))
	if res.Subtitle != 0 {
		t.Errorf("没有抓取器时不该写文件，got %d", res.Subtitle)
	}
}

// 上游报错 → 任务**不失败**（这是锦上添花的那一步，见文件头的失败哲学）。
func TestJavSubtitleErrorDoesNotFailTask(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	stub := &stubSubtitles{err: errors.New("上游挂了")}

	res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), stub))

	if res.Subtitle != 0 {
		t.Errorf("上游报错时不该写出文件，got %d", res.Subtitle)
	}
	// nfo 与封面照旧生成 —— 一次字幕失败不该翻掉整部片的元数据。
	if !artifactExists(filepath.Join(root, "SSIS-001-UC-4K.nfo")) {
		t.Error("字幕失败不该影响 nfo 生成")
	}
	if !artifactExists(filepath.Join(root, "thumb.jpg")) {
		t.Error("字幕失败不该影响封面生成")
	}
}

// 搜不到 → 不写文件、不报错。
func TestJavSubtitleNoResult(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, rel, allOn(), &stubSubtitles{}))
	if res.Subtitle != 0 {
		t.Errorf("搜不到时不该写文件，got %d", res.Subtitle)
	}
}

// 全量扫描（Overwrite）**不覆盖**既有字幕；只有手动重刮（RefetchImages）才换。
//
// 理由与 thumb 同形：Overwrite 来自 scan_mode == full_sync，那是任务级持久设置、
// 会被定时扫描反复触发；让它覆盖字幕等于每轮都把用户手改的那份干掉。
func TestJavSubtitleOverwritePolicy(t *testing.T) {
	// 全量扫描：既有字幕留着。
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC-4K.chs.srt"), []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &stubSubtitles{result: zhSubtitle()}
	req := javSubtitleRequest(t, root, rel, allOn(), stub)
	req.Overwrite = true
	generateJavArtifacts(context.Background(), req)
	if stub.callCount() != 0 {
		t.Errorf("全量扫描不该重下字幕，got %d 次", stub.callCount())
	}

	// 手动重刮：重下并覆盖。
	stub2 := &stubSubtitles{result: &subtitle.Result{
		Data: []byte(sampleSRT + "2\n00:00:03,000 --> 00:00:04,000\n新的\n"),
		Ext:  "srt",
		Lang: subtitle.LangZHCN,
	}}
	req2 := javSubtitleRequest(t, root, rel, allOn(), stub2)
	req2.RefetchImages = true
	res := generateJavArtifacts(context.Background(), req2)
	if stub2.callCount() != 1 {
		t.Errorf("重刮该重下字幕，got %d 次", stub2.callCount())
	}
	if res.Subtitle != 1 {
		t.Errorf("重刮该写出 1 份，got %d", res.Subtitle)
	}
	// 写的是**同一个**文件名（不是又添一份）—— 覆盖语义。
	data, err := os.ReadFile(filepath.Join(root, "SSIS-001-UC-4K.zh-CN.srt"))
	if err != nil {
		t.Fatalf("重刮后该有 zh-CN 那份：%v", err)
	}
	if !strings.Contains(string(data), "新的") {
		t.Errorf("重刮该覆盖成新内容，got %q", data)
	}
}

// 侧车里既没番号也没标题 → 不打上游（搜也没用）。
func TestJavSubtitleNoKeywords(t *testing.T) {
	root := t.TempDir()
	const bare = `{"schema":"litepan.jav.sidecar/1","number":"","title":""}`
	if err := os.WriteFile(filepath.Join(root, "X-1.strm"), []byte("http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "X-1.json"), []byte(bare), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &stubSubtitles{result: zhSubtitle()}
	// 侧车没有番号会被 emby.Parse 判为非法，所以这条走的是「没有可用侧车」那条路
	// —— 断言的重点是「不 panic、不写文件」。
	res := generateJavArtifacts(context.Background(), javSubtitleRequest(t, root, "X-1.strm", allOn(), stub))
	if res.Subtitle != 0 {
		t.Errorf("没有可用侧车时不该写字幕，got %d", res.Subtitle)
	}
}

// subtitleKeywords 本身：去空、去重、番号在前。
func TestSubtitleKeywords(t *testing.T) {
	doc := &emby.SidecarDoc{Number: "SSIS-001", Title: "样本标题"}
	if got := subtitleKeywords(doc); len(got) != 2 || got[0] != "SSIS-001" || got[1] != "样本标题" {
		t.Errorf("该是 [番号, 标题]，got %v", got)
	}
	// 标题就是番号时不重复打接口。
	doc = &emby.SidecarDoc{Number: "SSIS-001", Title: "ssis-001"}
	if got := subtitleKeywords(doc); len(got) != 1 {
		t.Errorf("标题与番号相同该去重，got %v", got)
	}
	// 只有番号。
	doc = &emby.SidecarDoc{Number: "SSIS-001"}
	if got := subtitleKeywords(doc); len(got) != 1 || got[0] != "SSIS-001" {
		t.Errorf("只有番号时该只给一个，got %v", got)
	}
	if got := subtitleKeywords(nil); got != nil {
		t.Errorf("nil 侧车该返回 nil，got %v", got)
	}
}

// findLocalSubtitle 的判据：主干 + 字幕扩展名，中间那段（语言码）不看。
func TestFindLocalSubtitle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"SSIS-001-UC-4K.zh-CN.srt",
		"SSIS-001-UC-4K.chs.ass",
		"SSIS-001-UC-4K.srt",
		"SSIS-001-UC-4K.nfo",
		"别的片子.srt",
		"SSIS-001-UC-4Kx.srt", // 主干不是前缀（少了分隔点），不该算
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := findLocalSubtitle(entries, "SSIS-001-UC-4K")
	if !ok {
		t.Fatal("该找到一份既有字幕")
	}
	if info.Ext != "srt" && info.Ext != "ass" {
		t.Errorf("扩展名不该是 %q", info.Ext)
	}

	if _, ok := findLocalSubtitle(entries, "不存在的片子"); ok {
		t.Error("主干对不上时不该找到")
	}
}
