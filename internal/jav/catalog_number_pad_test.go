package jav

import (
	"context"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
)

// 2026-10-31 真机：群晖上 `MIAE-0071_LTX.strm`（分享者把序号补成 4 位）
// 点「开始刮削」一张卡都刮不到；把名字改成 `MIAE-071` 立刻就好了。
//
// 根因在 `ingestByNumber` 那道「精确命中」的闸：它比 `NormalizeNumberKey`，
// 而 `MIAE-0071` 与 `MIAE-071` 归一出两个不同的键。上游搜索对 `MIAE-0071`
// 返回的第一条**恰好**是 `MIAE-071`（上游按相似度排的），却被判成
// 「上游有 1 条近似结果，番号都对不上」→ 整部片被静默记成「上游没有这一部」，
// 海报墙那张卡一张图都没补。
//
// 这个用例钉住「补零形态要能命中」。
func TestIngestByNumberToleratesExtraLeadingZeros(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 上游只有 3 位的那一条（真实上游就是这样）。
	f.db.searchResult = []javdb.Movie{{ID: "qpzM", Number: "MIAE-071", Title: "深喉嚨女教師榨精口爆 輝月杏梨"}}
	f.db.movieResult = javdb.Movie{ID: "qpzM", Number: "MIAE-071", Title: "深喉嚨女教師榨精口爆 輝月杏梨"}

	got, err := f.svc.IngestByNumberFresh(ctx, "MIAE-0071")
	if err != nil {
		t.Fatalf("补零形态该命中 MIAE-071，got err %v", err)
	}
	if got == nil || got.Number != "MIAE-071" {
		t.Fatalf("应当抓回 MIAE-071，got %+v", got)
	}
	// 命中就必须真抓详情（不抓等于什么都没入库）。
	if len(f.db.movieCalls) != 1 || f.db.movieCalls[0] != "qpzM" {
		t.Errorf("应当抓 qpzM 的详情，got %v", f.db.movieCalls)
	}
}

// 反面：**少一位不是补零**，不许因此把 `SS-003` 认成 `SS-3`。
//
// 全库实测这类是 30 组不同影片（同一天不同序号），放宽成「全削零」会把它们
// 合成同一部 —— 那比刮不到更糟：用户会拿到别人的封面与演员。
func TestIngestByNumberRejectsShorterNumber(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.searchResult = []javdb.Movie{{ID: "x1", Number: "SS-3", Title: "另一部片"}}
	f.db.movieResult = javdb.Movie{ID: "x1", Number: "SS-3", Title: "另一部片"}

	_, err := f.svc.IngestByNumberFresh(ctx, "SS-003")
	ae, ok := domain.AsAppError(err)
	if !ok || ae.Code != domain.CodeNotFound {
		t.Fatalf("SS-003 不该匹配 SS-3，期望 NotFound，got %v", err)
	}
	if len(f.db.movieCalls) != 0 {
		t.Errorf("不该抓任何详情，got %v", f.db.movieCalls)
	}
}
