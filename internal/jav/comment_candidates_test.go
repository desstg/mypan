package jav

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/quality"
	"litepan/internal/offlinedownload"
)

// hasReason 判一串拒收原因里有没有某一条。
//
// 本包没有这个工具（quality 包自己的测试里有一份，跨包用不了），而这几条用例
// 的心智就是「该不该带某个理由」，逐条展开会淹掉断言本身。
func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// ————————————————————— 评论区链接进候选池 —————————————————————

// 评论里贴的链接会变成候选，且**一个上游请求都不发**。
//
// 「零上游」是这条路线的硬约束（见 commentMagnets 的注释）：check 一轮要遍历
// 几百部片，在这儿按需抓评论会把手动「检查」拖到分钟级，还会和用户自己的请求
// 挤同一条 JAVDB 限流通道。stub 的 reviewCalls 是这条约束的机器化证据。
func TestCommentLinksBecomeCandidates(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	// JAVBUS 给一颗（btih 全 b），评论里贴的是**另一颗**（btih 全 a）。
	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("b", 40), "SSIS-001 1080p", "5GB")})

	// 评论正文照上游的样子写：HTML 转义 + xl= 体积参数 —— 这两件都得在
	// commentlink.Extract 里被还原，候选拿到的才是能推的链接。
	commentLink := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&amp;dn=SSIS-001%204K%20中字&amp;xl=6444544844"
	seedUserReviews(t, f, 7, "甲", &domain.JavReview{
		ID: 11, MovieID: "m1", Content: commentLink + " 我下过，速度不错",
	})
	f.db.reviewCalls = 0

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, IncludeCommentLinks: true,
	})

	cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: view.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("JAVBUS 一颗 + 评论一颗应当有 2 条候选，got %d", len(cands))
	}

	var fromComment, fromBus *domain.JavCandidate
	for _, c := range cands {
		if c.Source == domain.JavSourceComment {
			fromComment = c
		} else {
			fromBus = c
		}
	}
	if fromComment == nil {
		t.Fatal("评论里那颗应当落成 source=comment 的候选")
	}
	if fromBus == nil {
		t.Fatal("JAVBUS 那颗的来源应当仍是空串")
	}
	if !strings.Contains(fromComment.MagnetURI, "&dn=") {
		t.Errorf("HTML 转义应当被还原，got %q", fromComment.MagnetURI)
	}
	if !fromComment.HasSize || fromComment.SizeBytes != 6444544844 {
		t.Errorf("xl= 里的体积应当认出来，got hasSize=%v size=%d",
			fromComment.HasSize, fromComment.SizeBytes)
	}
	if f.db.reviewCalls != 0 {
		t.Errorf("检查路径不该碰上游评论接口，got %d 次请求", f.db.reviewCalls)
	}
}

// 两条路的判定语义不许串：同一轮、同一份 Criteria、同一个 movieOK 下，
// JAVBUS 那颗照旧严格判，评论那颗缺的项跳过。
//
// 这条是整套改动的核心断言 —— 它同时证明了「放宽只作用于评论链接」
// 与「PromptOK 的语义没被改」。
func TestCommentLinkSkipsMissingMetadata(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	// JAVBUS 那颗 1GB，低于下面设的 2GB 下限。
	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("b", 40), "SSIS-001 1080p", "1GB")})
	// 评论那颗既没有 xl=、正文也不写体积。
	seedUserReviews(t, f, 7, "甲", &domain.JavReview{
		ID: 21, MovieID: "m1",
		Content: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=SSIS-001",
	})

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, IncludeCommentLinks: true,
		MinSizeMB: intPtr(2048), MaxFileCount: intPtr(3),
	})

	cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: view.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}

	for _, c := range cands {
		if c.Source == domain.JavSourceComment {
			if !c.PushOK {
				t.Errorf("评论那颗缺的项该跳过，不该被拒，reasons=%v", c.RejectionReasons)
			}
			if hasReason(c.RejectionReasons, quality.ReasonSizeUnknown) ||
				hasReason(c.RejectionReasons, quality.ReasonFileCountUnknown) {
				t.Errorf("评论那颗不该带「未知即拒」的理由，reasons=%v", c.RejectionReasons)
			}
			continue
		}
		// JAVBUS 那颗：体积知道但太低，文件数未知 —— 两条都该拒。
		if c.PushOK {
			t.Error("JAVBUS 那颗 1GB 低于 2GB 下限，不该通过")
		}
		if !hasReason(c.RejectionReasons, quality.ReasonBelowMinSize) {
			t.Errorf("JAVBUS 那颗应当带 below_min_size，reasons=%v", c.RejectionReasons)
		}
		if !hasReason(c.RejectionReasons, quality.ReasonFileCountUnknown) {
			t.Errorf("JAVBUS 那颗应当带 file_count_unknown，reasons=%v", c.RejectionReasons)
		}
	}
}

// 放宽不越过「合集」那条线：它是正面判出来的内容不对，不是信息不全。
func TestCommentLinkKeepsPackRule(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	seedUserReviews(t, f, 7, "甲", &domain.JavReview{
		ID: 31, MovieID: "m1",
		Content: "SSIS-001%20全集 10部合集 magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=SSIS-001%20全集",
	})

	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, IncludeCommentLinks: true,
	})

	cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: view.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("应当只有评论那一颗，got %d", len(cands))
	}
	if cands[0].PushOK || !hasReason(cands[0].RejectionReasons, quality.ReasonPackNotMovie) {
		t.Errorf("合集仍该被拒，reasons=%v", cands[0].RejectionReasons)
	}
}

// 目标网盘不支持 ed2k 时：候选**照样生成**（用户要在候选弹窗里看到「评论区有这条」），
// 但标成推不了 —— 自动推送就不挑它，也不会在推送记录里留一串「网盘不支持」的失败记录。
func TestCommentLinkUnsupportedTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caps   offlinedownload.Capabilities
		pushOK bool
	}{
		{
			name: "目标不支持 ed2k",
			caps: offlinedownload.Capabilities{
				Supported: true, SupportsURLs: true, URLSchemes: []string{"magnet"},
			},
			pushOK: false,
		},
		{
			name: "115 那样支持 ed2k",
			caps: offlinedownload.Capabilities{
				Supported: true, SupportsURLs: true, URLSchemes: []string{"magnet", "ed2k"},
			},
			pushOK: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, off := fixtureWithPush(t)
			off.caps = tc.caps
			ctx := context.Background()

			seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
			seedUserReviews(t, f, 7, "甲", &domain.JavReview{
				ID: 41, MovieID: "m1",
				Content: "ed2k://|file|SSIS-001.mp4|1073741824|0123456789ABCDEF0123456789ABCDEF|/",
			})

			view := seedSubWithCandidate(t, f, SubscriptionInput{
				TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
				DownloadMode: "strict", Enabled: true, IncludeCommentLinks: true,
				// 必须给个推送目标：没有目标时 resolveTarget 会报错，
				// 预判按「拿不准就别拦」放行（见 targetSupportsEd2k）。
				TargetAccountID: 7,
			})

			cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
				SubscriptionID: view.ID, Limit: 100,
			})
			if err != nil {
				t.Fatalf("list candidates: %v", err)
			}
			if len(cands) != 1 {
				t.Fatalf("ed2k 也应当落成候选，got %d 条", len(cands))
			}
			if cands[0].PushOK != tc.pushOK {
				t.Errorf("PushOK = %v, 期望 %v，reasons=%v",
					cands[0].PushOK, tc.pushOK, cands[0].RejectionReasons)
			}
			gotUnsupported := hasReason(cands[0].RejectionReasons, quality.ReasonTargetUnsupported)
			if !tc.pushOK && !gotUnsupported {
				t.Errorf("推不了时应当带 target_unsupported，reasons=%v", cands[0].RejectionReasons)
			}
			if tc.pushOK && gotUnsupported {
				t.Errorf("推得了时不该带 target_unsupported，reasons=%v", cands[0].RejectionReasons)
			}
		})
	}
}

// 开关默认关：评论链接一条都不进候选池，且这个默认值能原样往返一次
// （钉住迁移的 DEFAULT 0 与 scanJavSubscription 的接线）。
func TestCommentLinksOffByDefault(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("b", 40), "SSIS-001 1080p", "5GB")})
	commentURI := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) + "&dn=SSIS-001%204K"
	seedUserReviews(t, f, 7, "甲", &domain.JavReview{ID: 51, MovieID: "m1", Content: commentURI})

	// 刻意不写 IncludeCommentLinks（零值 = 关）。
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true,
	})

	cands, _, err := f.st.JavCandidates.List(ctx, domain.JavCandidateFilter{
		SubscriptionID: view.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("开关关着时只该有 JAVBUS 那颗，got %d 条", len(cands))
	}
	if cands[0].Source == domain.JavSourceComment {
		t.Error("开关关着时不该有评论来源的候选")
	}

	// 从库里再读一次：这才是真的往返，证明列接对了。
	stored, err := f.st.JavSubscriptions.Get(ctx, view.ID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if stored.IncludeCommentLinks {
		t.Error("默认值应当是关")
	}
}
