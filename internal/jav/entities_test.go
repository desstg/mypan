package jav

import (
	"context"
	"errors"
	"strings"
	"testing"

	"litepan/internal/jav/javdb"
)

// ————————————————————— 按实体搜 —————————————————————

func TestSearchEntitiesTranslatesListKind(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.entityResult = []javdb.Entity{
		{ID: "4DGdR", Name: "驾驶双马尾", MoviesCount: 397, Privacy: "open"},
		{ID: "", Name: "没有 id 的要丢掉"},
	}

	items, err := f.svc.SearchEntities(ctx, "双马尾", "lists")
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应当只剩 1 个（没有 id 的被丢掉），got %d", len(items))
	}
	// ⚠️ 这条是整个改造最容易漏的一处：界面用 `lists`，上游用单数 `list`。
	// 传错了不会报错，只会「搜清单搜不出东西」。
	if len(f.db.entityCalls) != 1 || f.db.entityCalls[0] != javdb.EntityList {
		t.Fatalf("清单应当翻成上游的单数 list，got %v", f.db.entityCalls)
	}
	if f.db.entityCalls[0] == "lists" {
		t.Fatal("把界面的 lists 原样传给上游了 —— 那是「模糊匹配影片标题」")
	}

	got := items[0]
	if got.Kind != "lists" {
		t.Errorf("返回的 kind 应当是**界面取值**（lists），got %q", got.Kind)
	}
	// 清单的作品数在 movies_count 上，videos_count 是空的 —— 取错就是恒 0。
	if got.Count != 397 {
		t.Errorf("作品数应当读 movies_count，got %d", got.Count)
	}
}

func TestSearchEntitiesActorReadsVideosCount(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.entityResult = []javdb.Entity{
		{ID: "7R", Name: "三上悠亚", NameZHT: "三上悠亞", VideosCount: 120, Gender: 0},
	}

	items, err := f.svc.SearchEntities(ctx, "三上", "actor")
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应当返回 1 个，got %d", len(items))
	}
	if items[0].Count != 120 {
		t.Errorf("演员的作品数应当读 videos_count，got %d", items[0].Count)
	}
	// 显示名优先 name（与库里其它地方口径一致），只有 name 为空时才用 name_zht。
	if items[0].Name != "三上悠亚" {
		t.Errorf("显示名应当是 name，got %q", items[0].Name)
	}
}

func TestSearchEntitiesUnknownKindFails(t *testing.T) {
	f := newCatalogFixture(t)

	// 未知档位要**报错**，不能静默回落成「搜影片」—— 那会让人以为按导演搜生效了、
	// 只是恰好没有导演匹配。
	_, err := f.svc.SearchEntities(context.Background(), "三上", "directors")
	if err == nil {
		t.Fatal("未知的搜索类型应当报错")
	}
	if !strings.Contains(err.Error(), "未知的搜索类型") {
		t.Errorf("错误文案应当点明是类型不认识，got %v", err)
	}
	if len(f.db.entityCalls) != 0 {
		t.Errorf("校验没过就不该打上游，got %v", f.db.entityCalls)
	}
}

func TestSearchEntitiesUpstreamErrorIsNotSilentlyEmpty(t *testing.T) {
	f := newCatalogFixture(t)
	f.db.entityErr = errors.New("boom")

	if _, err := f.svc.SearchEntities(context.Background(), "三上", "actor"); err == nil {
		t.Fatal("上游失败应当报错，而不是返回空候选 —— 空列表会被读成「没有这个演员」")
	}
}

// ————————————————————— 实体的作品页 —————————————————————

func TestEntityMoviesUsesKindSlug(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.listPage = []javdb.Movie{{ID: "m1", Number: "SSIS-001", Title: "甲"}}

	items, total, err := f.svc.EntityMovies(ctx, "series", "abc", 1)
	if err != nil {
		t.Fatalf("EntityMovies: %v", err)
	}
	if len(items) != 1 || total != 0 {
		t.Fatalf("应当拿到 1 部、total 0（系列页没有总数），got %d / %d", len(items), total)
	}
	if len(f.db.entityPageCalls) != 1 || f.db.entityPageCalls[0] != "series/abc" {
		t.Errorf("应当按 series/abc 抓，got %v", f.db.entityPageCalls)
	}
}

// 空页不是错误：那是「翻到末页了」。清单（有总数）与演员/系列（没总数）
// 都靠这一条收尾，混成错误的话翻页会停不下来或者报假错。
func TestEntityMoviesEmptyPageIsNotError(t *testing.T) {
	f := newCatalogFixture(t)
	f.db.listPages = [][]javdb.Movie{{{ID: "m1", Number: "A-1"}}}

	items, _, err := f.svc.EntityMovies(context.Background(), "lists", "L1", 2)
	if err != nil {
		t.Fatalf("空页不该报错: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("第 2 页应当为空，got %d", len(items))
	}
}

func TestEntityMoviesRejectsUnknownKind(t *testing.T) {
	f := newCatalogFixture(t)

	if _, _, err := f.svc.EntityMovies(context.Background(), "list", "L1", 1); err == nil {
		t.Fatal("上游取值（单数 list）不该在界面这一层被接受 —— 那里用的是 lists")
	}
}
