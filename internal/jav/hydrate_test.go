package jav

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
	"litepan/internal/jav/synopsis"
)

// full 是「整条补缺」那件活（详情页点开时排的）。
func full(id string) hydrateTask { return hydrateTask{id: id, chain: hydrateChainFull} }

// mags 是「只抓磁链」那件活。
func mags(id string) hydrateTask { return hydrateTask{id: id, chain: hydrateChainMagnets} }

// TestHydrateQueueDedupAndCooldown 钉住后台补缺队列的纪律。
//
// 都是「别拖慢用户」的推论：来回翻同一部不该重复排队（去重）、反复开关同一部
// 不该反复打上游（冷却）、翻得快时只保最近点开的几部（满了丢最旧）。
func TestHydrateQueueDedupAndCooldown(t *testing.T) {
	q := newHydrateQueue()

	// ① 去重：同一件活只留一份
	if !q.push(full("a")) {
		t.Fatal("第一次入队应当成功")
	}
	if q.push(full("a")) {
		t.Error("同一件活重复入队应当被挡")
	}
	// 但**同一部的另一条链**是另一件活，不该被对方挤掉
	if !q.push(mags("a")) {
		t.Error("「只抓磁链」与「整条补缺」是两件活，不该互相去重")
	}

	// ② 磁链优先：先出的必须是磁链那件
	if got := q.pop(); got.chain != hydrateChainMagnets {
		t.Fatalf("磁链的活应当插队首，got chain=%q", got.chain)
	}
	if got := q.pop(); got.chain != hydrateChainFull {
		t.Fatalf("整条的活排在后面，got chain=%q", got.chain)
	}

	// ③ 冷却：刚做过的（冷却期内）不许再排
	if q.push(mags("a")) {
		t.Error("刚做过的活（冷却期内）不该再入队")
	}
	// 别的片不受影响
	if !q.push(full("b")) {
		t.Error("冷却只对同一件活生效，别的片该能入队")
	}

	// ④ 空了给零值（消费者据此歇着）
	if got := q.pop(); got.id != "b" {
		t.Fatalf("pop = %q，期望 b", got.id)
	}
	if got := q.pop(); got.id != "" {
		t.Errorf("队列空时应当返回零值，got %q", got.id)
	}
}

// TestHydrateQueueMagnetsAlwaysAhead 磁链的活**永远**排在整条前面 ——
// 用户切到「磁力链接」那一档时，那一档正转着圈等它。
func TestHydrateQueueMagnetsAlwaysAhead(t *testing.T) {
	q := newHydrateQueue()
	q.push(full("a"))
	q.push(full("b"))
	q.push(mags("c"))
	q.push(mags("d"))

	var order []string
	for {
		got := q.pop()
		if got.id == "" {
			break
		}
		order = append(order, got.chain+":"+got.id)
	}
	want := []string{"magnets:c", "magnets:d", "full:a", "full:b"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("顺序不对：got %v，期望 %v", order, want)
		}
	}
}

// TestHydrateQueueDropsOldestWhenFull 满了丢最旧的：用户翻得快时只补最近点开的。
func TestHydrateQueueDropsOldestWhenFull(t *testing.T) {
	q := newHydrateQueue()
	for i := 0; i < hydrateQueueSize+5; i++ {
		q.push(full(string(rune('A' + i))))
	}
	// 最先进去的几个应当已经被挤掉
	first := q.pop()
	if first.id == "A" {
		t.Fatal("最先入队的早该被挤掉（满了丢最旧），却还在队首")
	}
	if len(q.ids) > hydrateQueueSize {
		t.Errorf("队列不该超过容量：%d", len(q.ids))
	}
}

// TestHydrateMagnetsIsCheap 磁链那件活只调 MagnetsLocal 那条路能覆盖的东西：
// 用桩断言「只抓磁链」不会顺手跑整条补缺链（节流那条链很贵，用户切个 tab 不该触发它）。
func TestHydrateMagnetsUsesSeparateChain(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 注入一个「被调用就记一笔」的补缺桩：整条链要是被跑了，这里会记上。
	called := &countingJavEnricher{}
	f.svc.testEnrichers = []synopsis.Enricher{called}
	f.svc.testDisableEnrich = false

	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "mg1", Number: "SSIS-555", Title: "标题", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.db.movieResult = javdb.Movie{ID: "mg1", Number: "SSIS-555", Title: "标题"}

	// 磁链那件活跑的是 runMagnetFetch → Magnets（只碰磁链表/上游磁链接口），
	// **不该**碰到补缺链。
	f.svc.runMagnetFetch(ctx, "mg1")
	if called.n != 0 {
		t.Errorf("只抓磁链的活不该跑补缺链，被调了 %d 次", called.n)
	}
}

type countingJavEnricher struct{ n int }

func (c *countingJavEnricher) Name() string { return "counting" }
func (c *countingJavEnricher) Enrich(context.Context, string) (synopsis.FieldPatch, error) {
	c.n++
	return synopsis.FieldPatch{}, nil
}

// TestMagnetsSweepOnlyTouchesEmptyOnes 钉住用户明确要求的那条：
// **只补本地没有的，不为空的不抓**。
func TestMagnetsSweepOnlyTouchesEmptyOnes(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 一部有磁链的、一部没有的
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "has", Number: "SSIS-111", Title: "有磁链", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "none", Number: "SSIS-222", Title: "没磁链", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.JavMagnets.Upsert(ctx, &domain.JavMagnet{
		Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Btih:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MovieID:     "has", Code: "SSIS-111", Magnet: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}); err != nil {
		t.Fatal(err)
	}

	// 候选里只该有「没磁链」那部
	ids, err := f.st.JavMagnets.PendingSweepMovieIDs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == "has" {
			t.Fatal("本地已经有磁链的片不该进候选（用户要求：不为空的不抓）")
		}
	}
	found := false
	for _, id := range ids {
		if id == "none" {
			found = true
		}
	}
	if !found {
		t.Errorf("没磁链的那部该在候选里，got %v", ids)
	}
}

// TestMagnetsSweepLedgerSkipsAnsweredOnes 记账之后不再被挑中 ——
// 这正是那 6454 部「确实没有磁链」的片不会被反复问的保证。
func TestMagnetsSweepLedgerSkipsAnsweredOnes(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "q1", Number: "SSIS-333", Title: "问过没有", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatal(err)
	}
	ids, err := f.st.JavMagnets.PendingSweepMovieIDs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "q1" {
		t.Fatalf("还没问过时应当被挑中，got %v", ids)
	}
	// 记一笔（模拟「上游回了：这部确实没有磁链」）
	if err := f.st.JavMagnets.MarkSwept(ctx, "q1"); err != nil {
		t.Fatal(err)
	}
	ids, err = f.st.JavMagnets.PendingSweepMovieIDs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("记过账之后不该再被挑中（否则 6454 部每天被重问一遍），got %v", ids)
	}
}
