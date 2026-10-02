package jav

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/javdb"
)

// TestIngestMagnetsDoesNotMarkSweptWhenJavbusUnasked 钉住「JAVBUS 那一半没问成时，
// 不许把『这部确实没有磁链』记进台账」。
//
// 为什么重要：台账决定「下一轮还问不问这部」。JAVBUS 是它独有的那批资源的
// 唯一来源（实测 SSIS-001 两边重叠只有 25/44），记了账这部片就**永远不会再被问**，
// 那批资源永久丢失。
//
// 这条路径现在由「JAVBUS 非 404 错误 → lastErr → 不记账」兜着；这个用例把它钉住，
// 免得将来有人为了「少刷一行 warn」把那个 lastErr 顺手去掉 —— 那会**静默**变成
// 「拿 JAVBUS 的失败当结论」。
func TestIngestMagnetsDoesNotMarkSweptWhenJavbusUnasked(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// JAVDB 正常回、但空（上游说这部没有）；JAVBUS 连不上。
	f.db.magnetsByID = nil
	f.db.magnetByIDErr = nil
	f.bus.magnetErr = errors.New("dial tcp: i/o timeout")

	seed := &domain.JavMovie{ID: "m1", Number: "SSIS-001", Title: "甲", RawJSON: `{"relative_movies":[]}`}
	if err := f.st.JavMovies.Upsert(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 连不上时**如实报错**（调用方 `ensureMagnets` 会记一条 warn 并按订阅
	// 决定要不要整部跳过），但关键是**不记账** —— 下一轮还要再问。
	err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m1"))
	if err == nil {
		t.Fatal("有来源连不上时应当把错误报出去（调用方据此记 warn）")
	}

	// 台账里**不该**有它 —— 下一轮还要再问。
	ids, err := f.st.JavMagnets.PendingSweepMovieIDs(ctx, 10)
	if err != nil {
		t.Fatalf("PendingSweepMovieIDs: %v", err)
	}
	for _, id := range ids {
		if id == "m1" {
			return // 还在待问名单里，正确
		}
	}
	t.Fatal("JAVBUS 没问成时不该记账，这部片应当还在待问名单里")
}

// TestIngestMagnetsMarksSweptWhenBothSourcesSayNo 两个来源都**明确**回了「没有」时
// 才记账 —— 那是有效结论，不记的话那几千部没磁链的片每天都要被重新问一遍。
func TestIngestMagnetsMarksSweptWhenBothSourcesSayNo(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	f.db.magnetsByID = nil
	f.db.magnetByIDErr = &javdb.APIError{Status: http.StatusNotFound, Message: "资源未找到"}
	f.bus.magnets = nil
	f.bus.magnetErr = javbus.ErrCodeNotFound

	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "m2", Number: "ZZZ-999", Title: "乙", RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "m2"))
	var ae *domain.AppError
	if !errors.As(err, &ae) || ae.Code != domain.CodeNotFound {
		t.Fatalf("两个来源都说没有时应当回 NOT_FOUND（调用方据此不当失败看），got %v", err)
	}

	ids, _ := f.st.JavMagnets.PendingSweepMovieIDs(ctx, 10)
	for _, id := range ids {
		if id == "m2" {
			t.Fatal("明确结论应当记账，这部片不该还在待问名单里")
		}
	}
}

// TestIngestMagnetsJavdb404IsDefinitive JAVDB 回 404 是**明确结论**，
// 不是「上游挂了」—— 日志该走 info 而不是 warn（否则真正的故障被淹没）。
func TestIngestMagnetsJavdb404IsDefinitive(t *testing.T) {
	if !isDefinitiveNoMagnets(&javdb.APIError{Status: http.StatusNotFound}) {
		t.Error("HTTP 404 应当判成「上游明确说没有」")
	}
	if isDefinitiveNoMagnets(&javdb.APIError{Status: http.StatusForbidden}) {
		t.Error("403（签名失效）不该判成「没有」")
	}
	if isDefinitiveNoMagnets(errors.New("dial tcp: i/o timeout")) {
		t.Error("网络错误不该判成「没有」")
	}
}

// TestIngestMagnetsSkipsJavbusForNonCensored 非有码档**不去问 JAVBUS** ——
// 它根本没有那三档的页面，去问就是一次白发的请求 + 一次限流等待。
func TestIngestMagnetsSkipsJavbusForNonCensored(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	// 一部 FC2（type=3）：JAVBUS 那边一个请求都不该发。
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "fc2", Number: "FC2-1234567", Title: "丙", Type: domain.JavTypeFC2,
		RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// FC2 这部两个来源都没资源 → 回 NOT_FOUND（那是「确实没有」的有效结论，
	// 调用方不当失败看）。关键是**一个 JAVBUS 请求都没发**。
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "fc2")); err != nil {
		var ae *domain.AppError
		if !errors.As(err, &ae) || ae.Code != domain.CodeNotFound {
			t.Fatalf("两个来源都没资源时应当是 NOT_FOUND，got %v", err)
		}
	}
	if len(f.bus.calls) != 0 {
		t.Errorf("FC2 不该去问 JAVBUS，却问了 %v", f.bus.calls)
	}

	// 有码那档照旧问（不能因为这次优化把有码片的 JAVBUS 补充弄丢）。
	f.bus.calls = nil
	f.bus.magnets = []javbus.Magnet{magnet("b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", "SSIS-777 1080p", "4GB")}
	if err := f.st.JavMovies.Upsert(ctx, &domain.JavMovie{
		ID: "cen", Number: "SSIS-777", Title: "丁", Type: domain.JavTypeCensored,
		RawJSON: `{"relative_movies":[]}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.svc.ingestMagnets(ctx, mustMovie(t, f, "cen")); err != nil {
		t.Fatalf("ingestMagnets: %v", err)
	}
	if len(f.bus.calls) != 1 || f.bus.calls[0] != "SSIS-777" {
		t.Errorf("有码片应当照旧问 JAVBUS，got %v", f.bus.calls)
	}
	stored, err := f.st.JavMagnets.ListByMovie(ctx, "cen")
	if err != nil {
		t.Fatalf("ListByMovie: %v", err)
	}
	if len(stored) != 1 {
		t.Errorf("有码片的 JAVBUS 补充不该丢，got %d 颗", len(stored))
	}
}
