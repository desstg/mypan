package tgsubscribe

import (
	"testing"

	"litepan/internal/domain"
)

// 短 ASCII 排除词必须整词命中 —— 这是「TS 吃掉一整类正常资源」的回归测试。
//
// 子串匹配下，本地库里 24 条被判「命中排除词 TS」的记录中 21 条是误伤：
// CHS-ENG.BTSJ6（压制组）、DTS-HD.MA（音轨）、YTS.GG（站点）、[SweetSub&VCB-Studio]
// （字幕组）、RDTSHDMA（音轨）、-Kitsune / -Tsun（发布组）。它们全是
// 「ts 恰好出现在别的词里」。
func TestShortExcludeKeywordNeedsWholeWord(t *testing.T) {
	cfg := DefaultQualityConfig()
	falsePositives := []string{
		"Clash.of.the.Thundermans.2026.1080p.AMZN.WEB-DL.DDP5.1.H.264-Kitsune",
		"Pinocchio.Unstrung.2026.1080p.BluRay.AVC.DTS-HD.MA.5.1-DIY@XHX",
		"Reacher.S03E02.Truckin.1080p.Blu-ray.RDTSHDMA5.1.H264-d3g",
		"夜巡毒枭.2026.HD1080P.AAC.H264.CHS.BTSJ6",
		"[SweetSub&VCB-Studio] Mononoke The Movie Chapter II 10-bit 1080p",
		"Toy Story 5 (2026) [1080p] [WEBRip] [5.1] [YTS.GG - YTS.BZ][1.9G]",
		"Mononoke.the.Movie.2025.MULTi.1080p.WEB.x264-E-AC-3-Tsun",
	}
	for _, name := range falsePositives {
		v := Evaluate(ParseReleaseName(name), cfg)
		if !v.Passed {
			t.Errorf("%q 被误判成排除词：%s", name, v.Reason)
		}
	}
}

// 反过来，真正的枪版/TS 必须继续被拦下 —— 整词化不能把这道门槛一起放掉。
func TestShortExcludeKeywordStillCatchesRealOnes(t *testing.T) {
	cfg := DefaultQualityConfig()
	real := []string{
		"Coyote.vs.Acme.2026.TS.1080p.mkv",
		"Gunche.2026.PLSUBBED.AI.1080p.WEBRip.XviD-MAXX.avi.ts",
		"Spider-Man.Brand.New.Day.2026.1080p.HQ.HDTS.x264",
		"The.Uprising.2026.TCAM.1080p.MULTi.mkv",
		"Dune.2021.1080p.HDCAM.x264",
		"Heart.of.the.Beast.2026.camrip.720p.mp4",
		"Ice.Cream.Man.2026.1080p.CAMRip.Legendado.mkv",
		"Colony.Gunche.2026.CAM-Rip-CinemaCity.1080p.Korean.mp4",
	}
	for _, name := range real {
		v := Evaluate(ParseReleaseName(name), cfg)
		if v.Passed {
			t.Errorf("%q 应当被排除词拦下，实际通过了（%s）", name, v.Reason)
		}
		if !containsAny(v.Reason, "排除词") {
			t.Errorf("%q 的原因该说明命中排除词，实际 %q", name, v.Reason)
		}
	}
}

// 长词与中文词继续按子串匹配 —— 整词化只针对短 ASCII 词，别把这条也改了。
func TestLongAndCJKExcludeKeywordsStaySubstring(t *testing.T) {
	cfg := DefaultQualityConfig()
	for _, name := range []string{
		"沙丘2.2024.预告.1080p.WEB-DL",
		"Dune.2021.Official.Trailer.1080p",
		"Dune.2021.Sample.1080p",
		"沙丘2.2024.枪版.1080p",
		"沙丘2.2024.抢先版.1080p",
	} {
		if v := Evaluate(ParseReleaseName(name), cfg); v.Passed {
			t.Errorf("%q 应当被排除词拦下", name)
		}
	}
}

// isShortASCIIKeyword 的边界：中文词、带符号的词、超长词都不走整词那条路。
func TestIsShortASCIIKeyword(t *testing.T) {
	short := []string{"ts", "TS", "cam", "CAM", "scr", "r5", "ts1"}
	for _, kw := range short {
		if !isShortASCIIKeyword(kw) {
			t.Errorf("%q 应当算短 ASCII 词", kw)
		}
	}
	notShort := []string{"枪版", "预告", "Trailer", "Sample", "抢先", "hdcam", "", "ts-", "ts "}
	for _, kw := range notShort {
		if isShortASCIIKeyword(kw) {
			t.Errorf("%q 不该算短 ASCII 词", kw)
		}
	}
}

// 片源全等那条路：粘连写法认不出来整词，但解析器早就把它归成了 TS/CAM。
func TestShortExcludeMatchesParsedSource(t *testing.T) {
	rel := ParseReleaseName("Spider-Man.Brand.New.Day.2026.1080p.HQ.HDTS.x264")
	if rel.Source != "TS" {
		t.Fatalf("前置条件不成立：HDTS 该被解析成 TS，实际 %q", rel.Source)
	}
	if !matchShortExcludeKeyword(rel, NormalizeName(rel.Raw), "TS") {
		t.Error("片源全等这条判据该命中")
	}
	// TCAM 会被 sourceFromRaw 认成 CAM（候选表里 cam 排在 hdts 前面），
	// 那也一样该被拦下 —— CAM 与 TS 都在默认排除词里。
	if got := ParseReleaseName("The.Uprising.2026.TCAM.1080p.MULTi.mkv").Source; got != "CAM" {
		t.Errorf("TCAM 的片源 = %q，want CAM", got)
	}
}

// 手动搜索命中的记录也要过画质门槛 —— 这是「枪版别摆到用户面前」的落点。
//
// 过去只有自动搜索调 applyQualityAndDedupe，手动「搜网盘」直接标 ambiguous，
// 于是方案里的 exclude_keywords 对手动搜来的结果完全失效：实测搜「起义」搜到
// `The.Uprising.2026.CAM.1080p`，用户点一下「立即推送」就推进了网盘。
func TestSearchWebFiltersCamRelease(t *testing.T) {
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {
			webHitFor("生逢其时 (2026) S01E05 1080p HDCAM x264", 7001),
			webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 7002),
		},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchWeb(t.Context(), sub.ID)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if res.HitsScanned != 2 {
		t.Fatalf("HitsScanned = %d, want 2（搜到的都要如实报数）", res.HitsScanned)
	}
	if res.HitRecords != 1 {
		t.Fatalf("只该落 WEB-DL 那一条，实际落了 %d 条", res.HitRecords)
	}

	rows := listWebRecords(t, st, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("库里应有 1 条，实际 %d", len(rows))
	}
	if rows[0].Status != domain.TGRecordAmbiguous {
		t.Errorf("状态 = %q，want ambiguous", rows[0].Status)
	}
	if containsAny(rows[0].RawName, "CAM") {
		t.Errorf("枪版落库了：%q", rows[0].RawName)
	}
}

// 手动搜索仍然不能被画质分拦住：画质分只决定排序，方案没设最低分辨率时
// 一条 480p 也该照常落成「待确认」交给用户决定。
func TestSearchWebKeepsLowQualityButValidRelease(t *testing.T) {
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("生逢其时 (2026) S01E05 480p HDTV H.264", 7101)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchWeb(t.Context(), sub.ID)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if res.HitRecords != 1 {
		t.Fatalf("480p 不该被拦（方案没设最低分辨率），实际落了 %d 条", res.HitRecords)
	}
}
