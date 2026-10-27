package jav

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/quality"
)

// ————————————————————— LTX 挑选策略 —————————————————————
//
// 一个订阅级的 `ltx_mode`，三条语义：
//   - ""      与升级前逐字节一致；
//   - prefer  有 LTX 就挑 LTX，没有就退回原来的挑法；
//   - only    只推 LTX；一颗都没有时**这一轮什么都不推**（Idle，不是失败）。
//
// LTX 的判据是「链接自带的文件名里带 LTX」（见 quality.LTXMarked），
// 不参与打分 —— 所以下面每条用例都刻意让 LTX 那颗**分数更低**，
// 好证明「挑中它」是策略起的作用、不是排序碰巧。

// ltxSub 造一条开了评论区链接 + 勾了破解（LTX 策略的两条前提）的影片订阅。
func ltxSub(mode string) SubscriptionInput {
	return SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
		IncludeCommentLinks: true,
		Qualities:           []string{"uncensored"},
		LTXMode:             mode,
	}
}

// 4K 的普通版 + 1080p 的 LTX 版：默认挑 4K，prefer / only 该挑 LTX。
func ltxPair() []javbus.Magnet {
	return []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 2160p 破解 8GB", "8GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 1080p 破解_LTX 3GB", "3GB"),
	}
}

// TestLTXOffKeepsOldBehavior 空串 = 不启用，挑法与升级前一致（清晰度优先）。
func TestLTXOffKeepsOldBehavior(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", ltxPair())
	view := seedSubWithCandidate(t, f, ltxSub(""))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("推送应当成功，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("a", 40)) {
		t.Errorf("不启用 LTX 时应当照旧挑 4K 那颗，got %v", off.calls)
	}
}

// TestLTXPreferPicksLTX 「LTX 优先」：哪怕分数更低也挑 LTX。
func TestLTXPreferPicksLTX(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", ltxPair())
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModePrefer))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("推送应当成功，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("b", 40)) {
		t.Errorf("LTX 优先应当挑 LTX 那颗，got %v", off.calls)
	}
}

// TestLTXPreferFallsBack 池里一颗 LTX 都没有时退回原来的挑法 —— 用户原话
// 「如果没有 LTX 资源，就还保持以前那种方法选择」。
func TestLTXPreferFallsBack(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 1080p 破解 3GB", "3GB"),
		magnet(strings.Repeat("b", 40), "SSIS-001 2160p 破解 8GB", "8GB"),
	})
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModePrefer))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("没有 LTX 时应当退回原来的挑法并推送成功，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("b", 40)) {
		t.Errorf("退回原挑法应当挑 4K 那颗，got %v", off.calls)
	}
}

// TestLTXPreferIgnoresUnqualifiedLTX 那颗 LTX **不合格**（体积低于下限）时不能再
// 把整池收窄成它自己。
//
// 这是「先分组后筛合格」那个顺序的守门用例：顺序反了，池子会被这颗不合格的 LTX
// 收窄、随后被筛空 → 订阅报「没有符合推送条件的资源」，而旁边明明有一颗合格的普通资源。
func TestLTXPreferIgnoresUnqualifiedLTX(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 2160p 破解 8GB", "8GB"),
		// 带 LTX 但只有 1GB，低于下面设的 2GB 下限。
		magnet(strings.Repeat("b", 40), "SSIS-001 1080p 破解_LTX 1GB", "1GB"),
	})
	in := ltxSub(domain.JavLTXModePrefer)
	in.MinSizeMB = intPtr(2048)
	view := seedSubWithCandidate(t, f, in)

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("不合格的 LTX 不该把整池收窄空，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("a", 40)) {
		t.Errorf("应当退到合格的普通资源，got %v", off.calls)
	}
}

// TestLTXOnlyWithNoLTXIsIdle 「仅 LTX」但一颗都没有：不推、不报错、不留失败记录。
//
// 用户原话是「暂时不选择，不推送，直到有 LTX 资源才推送」。落到代码上必须走
// (nil, 原因) 那条返回 —— 返回 error 会被 pushBatch 当成「整条订阅推不动」。
func TestLTXOnlyWithNoLTXIsIdle(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 2160p 破解 8GB", "8GB"),
	})
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModeOnly))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("没有 LTX 时不该返回 error（那会被当成推送失败），got %v", err)
	}
	if res.OK {
		t.Fatal("没有 LTX 时不该推送")
	}
	if !strings.Contains(res.Message, "LTX") {
		t.Errorf("提示里应当说清是在等 LTX，got %q", res.Message)
	}
	// 关键：**一个字节都没送到网盘**，也没留下推送记录与尝试。
	if len(off.calls) != 0 {
		t.Errorf("不该提交离线任务，got %v", off.calls)
	}
	records, total, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if total != 0 {
		t.Errorf("不该留下推送记录，got %d: %+v", total, records)
	}
}

// TestLTXOnlyPicksLTXWhenItAppears 有 LTX 时正常推。
func TestLTXOnlyPicksLTXWhenItAppears(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", ltxPair())
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModeOnly))

	res, err := f.svc.AutoPush(ctx, view.ID, false)
	if err != nil {
		t.Fatalf("AutoPush: %v", err)
	}
	if !res.OK {
		t.Fatalf("有 LTX 时应当推送，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("b", 40)) {
		t.Errorf("仅 LTX 应当挑 LTX 那颗，got %v", off.calls)
	}
}

// TestLTXSubscribeMoviePicksLTX 「执行订阅」（演员/清单订阅里点某一部）走的是另一条
// 挑选路径（SubscribeMovie → SQL PickBest），这里单独钉一次，免得两条路挑出不同的那颗。
func TestLTXSubscribeMoviePicksLTX(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", ltxPair())
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModePrefer))

	res, err := f.svc.SubscribeMovie(ctx, view.ID, "m1")
	if err != nil {
		t.Fatalf("SubscribeMovie: %v", err)
	}
	if !res.OK {
		t.Fatalf("推送应当成功，got %q", res.Message)
	}
	if len(off.calls) != 1 || !strings.Contains(off.calls[0].URLs[0], strings.Repeat("b", 40)) {
		t.Errorf("LTX 优先在单部这条路也该挑 LTX 那颗，got %v", off.calls)
	}
}

// TestLTXSubscribeMovieOnlyIsIdle 单部推送在「仅 LTX」且没有 LTX 时同样必须是
// Idle 口径 —— OK:false + 一句话，**不能报错**（报错会被上层当成整条订阅推不动）。
func TestLTXSubscribeMovieOnlyIsIdle(t *testing.T) {
	f, off := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", []javbus.Magnet{
		magnet(strings.Repeat("a", 40), "SSIS-001 2160p 破解 8GB", "8GB"),
	})
	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModeOnly))

	res, err := f.svc.SubscribeMovie(ctx, view.ID, "m1")
	if err != nil {
		t.Fatalf("没有 LTX 时不该返回 error，got %v", err)
	}
	if res.OK {
		t.Fatal("没有 LTX 时不该推送普通资源")
	}
	if !strings.Contains(res.Message, "LTX") {
		t.Errorf("提示里应当说清是在等 LTX，got %q", res.Message)
	}
	if len(off.calls) != 0 {
		t.Errorf("不该提交离线任务，got %v", off.calls)
	}
}

// TestLTXMarkedOnCommentCandidate 「带 LTX」这个事实落在**链接自带的文件名**上：
// 磁链的 dn= 里有 LTX 才算；只写在评论正文里的不算（用户 2026-10-27 的决定）。
func TestLTXMarkedOnCommentCandidate(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	seedUserReviews(t, f, 7, "甲",
		// 链接名里带 LTX → 算。
		&domain.JavReview{ID: 61, MovieID: "m1",
			Content: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=SSIS-001%20LTX_restored.mp4"},
		// 链接名里没有、只有正文写了 LTX → 不算。
		&domain.JavReview{ID: 62, MovieID: "m1",
			Content: "这个是 LTX 修复版 magnet:?xt=urn:btih:" + strings.Repeat("b", 40) + "&dn=SSIS-001.mp4"},
	)

	view := seedSubWithCandidate(t, f, ltxSub(domain.JavLTXModePrefer))

	cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: view.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("两条评论应当各出一条候选，got %d", len(cands))
	}
	for _, c := range cands {
		got := hasTagOf(c, domain.JavQualityLTX)
		want := strings.Contains(c.MagnetURI, strings.Repeat("a", 40))
		if got != want {
			t.Errorf("候选 %q 的 LTX 标记 = %v，期望 %v（只看链接自带的文件名）",
				c.MagnetName, got, want)
		}
		// 它**不参与打分**：三元组的长度与内容都不该因为 LTX 变。
		if len(c.ResourceScore) != 3 {
			t.Errorf("ResourceScore 应当仍是三元组，got %v", c.ResourceScore)
		}
	}
}

// TestLTXModeRoundTrip 订阅上的 ltx_mode 存得进、读得回，且同一目标不会被二次创建。
func TestLTXModeRoundTrip(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	view := createSub(t, f, ltxSub(domain.JavLTXModeOnly))
	if view.LTXMode != domain.JavLTXModeOnly {
		t.Fatalf("视图里的 LTXMode = %q", view.LTXMode)
	}
	stored, err := f.st.JavSubscriptions.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if stored.LTXMode != domain.JavLTXModeOnly {
		t.Errorf("库里的 ltx_mode = %q，期望 only", stored.LTXMode)
	}

	// 改回不启用（前提也一起去掉）：归一成空串并落库。
	in := ltxSub("")
	in.ID = view.ID
	in.IncludeCommentLinks = false
	in.Qualities = []string{"hd"}
	updated, err := f.svc.UpdateSubscription(ctx, in)
	if err != nil {
		t.Fatalf("UpdateSubscription: %v", err)
	}
	if updated.LTXMode != "" {
		t.Errorf("取消前提后 LTXMode 应当回落成空串，got %q", updated.LTXMode)
	}
}

// hasTagOf 判候选的质量标签里有没有某一项。
func hasTagOf(c *domain.JavCandidate, want string) bool {
	return c != nil && containsTag(c.QualityTags, want)
}

// 编译期钉一下：ValidLTXModes 与 domain 里的常量必须同一组取值 ——
// 两边各写一份字面量，改一处漏一处就会「前端能选、后端报非法」。
var _ = func() bool {
	for _, m := range []string{domain.JavLTXModePrefer, domain.JavLTXModeOnly} {
		if _, ok := quality.ValidLTXModes[m]; !ok {
			panic("domain 的 LTX 常量与 quality.ValidLTXModes 不一致：" + m)
		}
	}
	return true
}()
