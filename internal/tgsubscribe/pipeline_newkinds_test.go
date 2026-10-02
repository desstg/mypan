package tgsubscribe

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"

	"litepan/internal/tgsubscribe/telegram"
)

// 百度/迅雷分享链发进频道时，要真的落出一条记录（而不是被忽略）。
//
// 过去它们连记录都不产生 —— 而实测频道里这两种占了多数链接，
// 用户看到的是「这个频道抓不到东西」，却无从知道抓到了什么。
func TestChannelPostKeepsUnsupportedShareKinds(t *testing.T) {
	s, st := newSearchServiceForTest(t, nil)
	chID := seedSearchChannel(t, st, "sharetester")
	ch, err := st.TGChannels.Get(context.Background(), chID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	sub := newActiveSub(7)
	sub.MediaType = domain.TGMediaTypeMovie
	sub.TMDBID = "baidu-test"
	sub.Title = "测试影片"
	sub.Year = 2026
	if _, err := st.TGSubscriptions.Create(context.Background(), sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}

	msg := &telegram.Message{
		MessageID: 1,
		Chat:      telegram.Chat{ID: -1001, Username: "sharetester", Type: "channel"},
		Text: "测试影片 (2026) 1080p\n" +
			"百度：https://pan.baidu.com/s/1abcDEFghi?pwd=Yu88\n" +
			"迅雷：https://pan.xunlei.com/s/VP0NGiPRx5CjNtWw1dgm7RAZA1?pwd=u5xn",
	}

	s.handlePost(context.Background(), ch, msg, []*domain.TGSubscription{sub})

	records, _, err := st.TGMatchRecords.List(context.Background(), domain.TGMatchRecordFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	found := map[string]string{}
	for _, rec := range records {
		found[rec.ResourceKind] = rec.Magnet
	}
	for kind, wantHost := range map[string]string{
		KindShareBaidu:  "pan.baidu.com",
		KindShareXunlei: "pan.xunlei.com",
	} {
		got, ok := found[kind]
		if !ok {
			t.Errorf("没有落出 %s 记录（实际落了 %v）", kind, keysOf(found))
			continue
		}
		if !strings.Contains(got, wantHost) {
			t.Errorf("%s 的链接不对：%q", kind, got)
		}
	}
}

// 只识别不投递的类型，**不该**被推出去 —— 落库可以，投递必须被拦住。
//
// 这条是安全底线：新加的十一种 kind 只要有一个漏出投递器，
// 就会把一条百度链接当成磁力扔给 115。
func TestUnsupportedShareKindsAreBlockedAtWindow(t *testing.T) {
	ctx := context.Background()
	s := newDispatcherServiceForTest(t, proberWith("magnet"))
	d := &flakyDeliverer{kinds: []string{KindMagnet}, failFor: 0}
	s.deliverers = []Deliverer{d}
	s.autoPushFn = func() bool { return true }

	sub := newActiveSub(7)
	if _, err := s.subs.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	// 把十一种走一遍，一条都不许到达投递器。
	for i, kind := range []string{
		KindShareBaidu, KindShareXunlei, KindShareAliyun, KindShareUC,
		KindShare123, KindShare189, KindSharePikPak, KindShareLanzou,
		KindShareGDrive, KindShareOneDrive, KindShareMega,
	} {
		seedPending(t, s, sub.ID, kind, fmt.Sprintf("hash-%d", i), "测试")
	}

	kept := s.partitionByDeliverable(ctx, sub, mustListPending(t, s, sub.ID))
	if len(kept) != 0 {
		t.Fatalf("不该留下任何可投递候选，实际留下 %d 条", len(kept))
	}
}

// 频道体检报告里要认得这些新类型（含中文名与「不可投递」标记）。
func TestProbeReportCoversNewShareKinds(t *testing.T) {
	for _, kind := range probeKindOrder {
		if labelKind(kind) == kind {
			t.Errorf("probeKindOrder 里的 %s 没有中文名", kind)
		}
	}
	// 顺序里不能有重复。
	seen := map[string]bool{}
	for _, kind := range probeKindOrder {
		if seen[kind] {
			t.Errorf("probeKindOrder 里 %s 重复", kind)
		}
		seen[kind] = true
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
