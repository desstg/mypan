package tgsubscribe

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

// fakePage 造一页帖子：id 从 high 递减到 low（页面显示顺序就是新→旧）。
func fakePage(chatID int64, high, low int64, prevBefore int64) *preview.Page {
	page := &preview.Page{ChannelID: chatID, Username: "ch", PrevBefore: prevBefore}
	for id := low; id <= high; id++ {
		msg := &telegram.Message{MessageID: id, Chat: telegram.Chat{ID: chatID, Type: "channel"}}
		page.Posts = append(page.Posts, preview.Post{Message: msg, HasText: true})
	}
	return page
}

// feedFetcher 按 before 游标返回预设的页，并记录每次请求的游标。
func feedFetcher(t *testing.T, pages map[int64]*preview.Page, calls *[]int64) pageFetcher {
	t.Helper()
	return func(before int64) (*preview.Page, error) {
		if calls != nil {
			*calls = append(*calls, before)
		}
		page, ok := pages[before]
		if !ok {
			return nil, fmt.Errorf("没有为游标 %d 准备页面", before)
		}
		return page, nil
	}
}

func idsOf(posts []preview.Post) []int64 {
	out := make([]int64, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.Message.MessageID)
	}
	return out
}

func assertIDs(t *testing.T, got []int64, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// 新频道必须走「回填有限页」而不是「无限往回翻」—— 这是最容易写错的一条。
func TestCatchUpPlan(t *testing.T) {
	if got := catchUpPlan(0, 3); got.MaxPages != 3 || got.RecentRescan != 0 {
		t.Errorf("新频道(回填3页) = %+v, want {3 0}", got)
	}
	if got := catchUpPlan(0, 0); got.MaxPages != 1 {
		t.Errorf("回填页数填 0 时至少抓一页，实得 %+v", got)
	}
	if got := catchUpPlan(0, 999); got.MaxPages != maxBackfillPages {
		t.Errorf("回填页数要钳到 %d，实得 %+v", maxBackfillPages, got)
	}
	if got := catchUpPlan(0, -5); got.MaxPages != 1 {
		t.Errorf("负数回填页数要钳到 1，实得 %+v", got)
	}
	if got := catchUpPlan(100, 3); got.MaxPages != hardCatchUpPages || got.RecentRescan != recentRescanPosts {
		t.Errorf("老频道 = %+v, want {%d %d}", got, hardCatchUpPages, recentRescanPosts)
	}
}

// 追新模式下正常情况下第一页就接上了，零次回退。
func TestWalkPagesStopsWhenCaughtUp(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0: fakePage(-100, 130, 111, 111),
	}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 115, catchUpOptions{MaxPages: hardCatchUpPages, RecentRescan: 0}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	assertIDs(t, idsOf(got.Posts), []int64{116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130})
	if len(calls) != 1 || calls[0] != 0 {
		t.Errorf("请求游标 = %v, want [0]（一次都不该回退）", calls)
	}
	if got.Truncated {
		t.Error("接上了不该标记截断")
	}
	if got.NewestID != 130 {
		t.Errorf("NewestID = %d, want 130", got.NewestID)
	}
}

// id 序列有空洞（帖子被删）时不能死循环。
func TestWalkPagesToleratesIDHoles(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0: fakePage(-100, 130, 111, 111),
	}, &calls)

	// lastID=125，本页最小 111 <= 125 → 立刻判定已接上。
	got, err := walkPages(context.Background(), fetch, "ch", -100, 125, catchUpOptions{MaxPages: hardCatchUpPages}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	assertIDs(t, idsOf(got.Posts), []int64{126, 127, 128, 129, 130})
	if len(calls) != 1 {
		t.Errorf("请求游标 = %v, want 只请求一次", calls)
	}
}

// 断档（新帖超过一页）时要接着往回翻，直到接上。
func TestWalkPagesPagesBackOnGap(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0:   fakePage(-100, 130, 111, 111), // 第一页最小 111 > lastID(95)
		111: fakePage(-100, 110, 91, 91),   // 第二页最小 91 <= 95 → 停
	}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 95, catchUpOptions{MaxPages: hardCatchUpPages}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	if len(calls) != 2 || calls[0] != 0 || calls[1] != 111 {
		t.Fatalf("请求游标 = %v, want [0 111]", calls)
	}
	if len(got.Posts) != 35 {
		t.Fatalf("帖子数 = %d, want 35（96..130）", len(got.Posts))
	}
	if got.Posts[0].Message.MessageID != 96 || got.Posts[len(got.Posts)-1].Message.MessageID != 130 {
		t.Errorf("区间 = %d..%d, want 96..130",
			got.Posts[0].Message.MessageID, got.Posts[len(got.Posts)-1].Message.MessageID)
	}
}

// 撞到页数上限要停，并标记截断。
func TestWalkPagesRespectsMaxPages(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0:   fakePage(-100, 130, 111, 111),
		111: fakePage(-100, 110, 91, 91),
		91:  fakePage(-100, 90, 71, 71),
	}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 1, catchUpOptions{MaxPages: 2}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("请求页数 = %d, want 2", len(calls))
	}
	if !got.Truncated {
		t.Error("撞到上限应当标记 Truncated")
	}
	if len(got.Posts) != 40 {
		t.Errorf("帖子数 = %d, want 40", len(got.Posts))
	}
}

// 没有更早的页（rel=prev 消失）时提前停，不算截断。
func TestWalkPagesStopsOnNoPrev(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0: fakePage(-100, 130, 111, 0), // PrevBefore = 0
	}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 1, catchUpOptions{MaxPages: hardCatchUpPages}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("请求页数 = %d, want 1", len(calls))
	}
	if got.Truncated {
		t.Error("翻到最早一页不是截断")
	}
	if len(got.Posts) != 20 {
		t.Errorf("帖子数 = %d, want 20", len(got.Posts))
	}
}

// 相邻两页在边界 id 上重复时不能产出两条。
func TestWalkPagesDedupesBoundaryOverlap(t *testing.T) {
	page1 := fakePage(-100, 130, 111, 111)
	page2 := fakePage(-100, 115, 100, 100) // 与 page1 重叠 111..115
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{0: page1, 111: page2}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 100, catchUpOptions{MaxPages: hardCatchUpPages}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	seen := map[int64]int{}
	for _, id := range idsOf(got.Posts) {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("message id %d 出现 %d 次", id, n)
		}
	}
	assertIDs(t, idsOf(got.Posts), func() []int64 {
		out := make([]int64, 0, 30)
		for id := int64(101); id <= 130; id++ {
			out = append(out, id)
		}
		return out
	}())
}

// @用户名 被回收后指向了另一个频道 → 必须报错，不能把别人的帖子当订阅源。
func TestWalkPagesRejectsDifferentChannel(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0: fakePage(-100999, 130, 111, 111),
	}, &calls)

	_, err := walkPages(context.Background(), fetch, "ch", -100111, 1, catchUpOptions{MaxPages: 1}, 0)
	if err == nil {
		t.Fatal("频道 id 不匹配时应当报错")
	}
}

// 翻出来的帖子必须按 id 升序交给下游 —— 匹配与画质的先来后到依赖这个顺序。
func TestWalkPagesSortsAscending(t *testing.T) {
	page := fakePage(-100, 130, 111, 111)
	// 打乱页内顺序
	page.Posts[0], page.Posts[5] = page.Posts[5], page.Posts[0]
	page.Posts[2], page.Posts[7] = page.Posts[7], page.Posts[2]
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{0: page}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 110, catchUpOptions{MaxPages: 1}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	ids := idsOf(got.Posts)
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("未按升序: %v", ids)
		}
	}
}

// 最近的若干条已处理帖子要允许重扫（兜住「先发占位、后补磁链」）。
func TestWalkPagesRecentRescan(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0: fakePage(-100, 130, 111, 111),
	}, &calls)

	got, err := walkPages(context.Background(), fetch, "ch", -100, 130,
		catchUpOptions{MaxPages: hardCatchUpPages, RecentRescan: 3}, 0)
	if err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	// lastID=130，重扫 3 条 → 只保留 128/129/130
	assertIDs(t, idsOf(got.Posts), []int64{128, 129, 130})
}

// 翻页之间要限速。
func TestWalkPagesPacesBetweenPages(t *testing.T) {
	var calls []int64
	fetch := feedFetcher(t, map[int64]*preview.Page{
		0:   fakePage(-100, 130, 111, 111),
		111: fakePage(-100, 110, 91, 91),
	}, &calls)

	start := time.Now()
	if _, err := walkPages(context.Background(), fetch, "ch", -100, 95, catchUpOptions{MaxPages: hardCatchUpPages}, 20*time.Millisecond); err != nil {
		t.Fatalf("walkPages 失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("两页之间没有等够限速间隔，耗时 %v", elapsed)
	}
}

// 上游报错要原样冒出来，不能吞成「没有新帖」。
func TestWalkPagesPropagatesFetchError(t *testing.T) {
	wantErr := errors.New("网络炸了")
	fetch := func(int64) (*preview.Page, error) { return nil, wantErr }
	_, err := walkPages(context.Background(), fetch, "ch", -100, 1, catchUpOptions{MaxPages: 2}, 0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// ctx 取消要立刻返回。
func TestWalkPagesHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fetch := func(int64) (*preview.Page, error) {
		t.Error("ctx 已取消，不该再发请求")
		return nil, nil
	}
	if _, err := walkPages(ctx, fetch, "ch", -100, 1, catchUpOptions{MaxPages: 2}, 0); err == nil {
		t.Fatal("ctx 取消时应当返回错误")
	}
}
