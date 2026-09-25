package javdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSignatureFormat 钉死签名的字面格式。
//
// 这不是「测实现细节」。签名是整个客户端的唯一凭据：SECRET 一旦被官方轮换，
// 所有请求都会 403，而 403 不会告诉你「签名过期了」—— 只会看起来像
// 「网络不通」或「账号被封」。这条测试至少能在别人改到这里时立刻变红，
// 提醒他「你正在动一个和远端约定绑死的东西」。
//
// 期望值是用 `printf '1700000000' + SECRET | md5sum` 独立算出来的，
// 不是从实现里抄的 —— 抄的话这测试就恒真了。
func TestSignatureFormat(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fixed := time.Unix(1700000000, 0)
	c.SetNow(func() time.Time { return fixed })

	got := c.Signature()
	want := "1700000000." + salt + ".dacaffcd8b4e1b35c2752f065e906f3a"
	if got != want {
		t.Fatalf("Signature() = %q\nwant %q\n（改了 secret / salt / 拼法？那会让所有请求 403）", got, want)
	}

	// 同一秒内多次调用必须一致：时间戳是秒级的，签名不随时间抖动。
	if again := c.Signature(); again != got {
		t.Errorf("同一秒内签名不稳定: %q vs %q", again, got)
	}
}

func TestNormalizeMovieTagsAndPreviews(t *testing.T) {
	m := Movie{
		ID:       "ZY5eq",
		Number:   "SSIS-001",
		Title:    "标题",
		CoverURL: "https://example.test/c.jpg",
		Tags:     []Tag{{ID: "1", Name: "高清"}, {ID: "2", Name: "单体"}, {ID: "3", Name: "高清"}},
		PreviewImages: []PreviewImage{
			{LargeURL: "https://example.test/p1.jpg", ThumbURL: "https://example.test/t1.jpg"},
			{LargeURL: "  "}, // 空值要被丢掉
			{LargeURL: "https://example.test/p2.jpg"},
		},
		HasCNSub: 1,
	}

	got := NormalizeMovie(m)

	// 标签取名字、去重、保序。
	if len(got.Tags) != 2 || got.Tags[0] != "高清" || got.Tags[1] != "单体" {
		t.Errorf("tags = %v, want [高清 单体]", got.Tags)
	}
	// 空的预览图 URL 要丢掉，否则前端会渲染一堆碎图。
	if len(got.PreviewImages) != 2 {
		t.Errorf("preview = %v, want 2 条", got.PreviewImages)
	}
	// 上游用 0/1 表示布尔，要归一成 true。
	if !got.HasCNSub {
		t.Error("has_cnsub=1 应当归一成 true")
	}
	// 有预览图时这个标记也该为真 —— 上游经常只给图不给标记。
	if !got.HasPreviewImages {
		t.Error("有预览图时 has_preview_images 应为真")
	}
}

func TestTruthyHandlesAllUpstreamShapes(t *testing.T) {
	// 同一个字段在不同接口里有 0/1、true/false、"1" 三种表示，
	// 不统一的话「中字」角标会时有时无。
	yes := []any{1, float64(1), true, "1", "true"}
	for _, v := range yes {
		if !Truthy(v) {
			t.Errorf("Truthy(%#v) = false, want true", v)
		}
	}
	no := []any{0, float64(0), false, "0", "", nil}
	for _, v := range no {
		if Truthy(v) {
			t.Errorf("Truthy(%#v) = true, want false", v)
		}
	}
}

func TestMovieTypeOf(t *testing.T) {
	if got := MovieTypeOf(Movie{Type: "1"}); got != "1" {
		t.Errorf("上游给了 type 就该用它，got %q", got)
	}
	// FC2 系在上游常常 type 为空，不兜底的话「FC2」筛选永远是空的。
	if got := MovieTypeOf(Movie{Number: "FC2PPV1234567"}); got != "3" {
		t.Errorf("FC2 番号应当兜底成类型 3，got %q", got)
	}
	if got := MovieTypeOf(Movie{Number: "fc2-123456"}); got != "3" {
		t.Errorf("小写 fc2 也该认，got %q", got)
	}
	if got := MovieTypeOf(Movie{Number: "SSIS-001"}); got != "0" {
		t.Errorf("普通番号兜底成有码，got %q", got)
	}
}

func TestTop250RequiresToken(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 没有 token 时要立刻返回 ErrNoToken，而不是发一次注定 401 的请求 ——
	// 那既浪费一次外站调用，返回的报错也不如这句清楚。
	_, err = c.Top250(context.Background(), "all", "", 1, 40)
	if err == nil {
		t.Fatal("没有 token 时应当报错")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("错误信息应当提到 token，got %v", err)
	}
}

func TestAPIErrorMessageIsPreferred(t *testing.T) {
	err := &APIError{Status: 400, Message: "账号或密码错误"}
	if got := err.Error(); got != "账号或密码错误" {
		t.Errorf("Error() = %q，应当优先用服务端的 message", got)
	}
	// 没有 message 时退回状态码。
	if got := (&APIError{Status: 502}).Error(); got != "HTTP 502" {
		t.Errorf("Error() = %q, want HTTP 502", got)
	}
}

func TestNormalizeImageURLIsIdentity(t *testing.T) {
	const u = "https://tp.spfcas.com/abc/xyz.jpg"
	// 刻意不做重写：原脚本会改写成 c0.jdbstatic.com，但那个域名实测不可达。
	if got := NormalizeImageURL(u); got != u {
		t.Errorf("NormalizeImageURL 不该改动 URL，got %q", got)
	}
}

// TestHotHitsRankingsNotPlayback 日/周/月榜必须打 `/v1/rankings`。
//
// 这两个端点名字像、内容完全不是一回事：`/v1/rankings/playback` 是**播放热度榜**
// （实测 MD0299 SZL028 …），与官网日榜（ABF-387 LUXU-1900 …）零重叠。
// 改回 playback 会静默地把「日榜」换回一份错的榜 —— 不报错，只是内容不对。
// 顺带钉住：`type` 必须发出去（上游不传会回「參數不能爲空: type」），
// 且这个请求**不带 authorization**（它本来就不需要 token）。
func TestHotHitsRankingsNotPlayback(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("authorization")
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[{"id":"x","number":"ABF-387"}]}}`))
	}))
	defer srv.Close()

	c, err := New(Options{APIBase: srv.URL, Retries: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	movies, err := c.Hot(context.Background(), "daily", "1")
	if err != nil {
		t.Fatalf("Hot: %v", err)
	}
	if len(movies) != 1 || movies[0].Number != "ABF-387" {
		t.Fatalf("解析结果不对：%+v", movies)
	}
	if gotPath != "/v1/rankings" {
		t.Errorf("路径应当是 /v1/rankings，got %q（playback 那份是另一个榜）", gotPath)
	}
	if !strings.Contains(gotQuery, "type=1") {
		t.Errorf("type 必须发出去，got query %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "period=daily") {
		t.Errorf("period 必须发出去，got query %q", gotQuery)
	}
	if gotAuth != "" {
		t.Errorf("这个端点不需要 token，不该带 authorization，got %q", gotAuth)
	}
}

// TestHotDefaultsAndRejectsBadParams 参数白名单：上游对未知值是**静默回落**的，
// 本地必须先拦 —— 否则「无码」格子会显示有码的内容，不报错、只是内容不对。
func TestHotDefaultsAndRejectsBadParams(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"success":1,"data":{"movies":[]}}`))
	}))
	defer srv.Close()

	c, err := New(Options{APIBase: srv.URL, Retries: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 空类型 → 有码（0），空周期 → daily。
	if _, err := c.Hot(context.Background(), "", ""); err != nil {
		t.Fatalf("空参数应当回落而不是报错：%v", err)
	}
	if !strings.Contains(gotQuery, "type=0") || !strings.Contains(gotQuery, "period=daily") {
		t.Errorf("空参数应当回落成 type=0&period=daily，got %q", gotQuery)
	}
	// 未知分类 / 未知周期都要在本地报错。
	if _, err := c.Hot(context.Background(), "daily", "zzz"); err == nil {
		t.Error("未知分类应当报错（上游会静默给有码）")
	}
	if _, err := c.Hot(context.Background(), "yearly", "0"); err == nil {
		t.Error("未知周期应当报错")
	}
}

// TestActorRankNeedsNoToken 演员榜匿名可用。
//
// 上游实测不带 authorization 也能取 type=0/1/2（结果与官网逐项相同），
// 客户端里原本挂着的 requireToken 比上游严 —— 结果是「没登录就看不见演员榜」。
func TestActorRankNeedsNoToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		_, _ = w.Write([]byte(`{"success":1,"data":{"actors":[{"id":"a1","name":"某演员"}]}}`))
	}))
	defer srv.Close()

	c, err := New(Options{APIBase: srv.URL, Retries: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	actors, err := c.ActorRank(context.Background(), "1", 1, 500)
	if err != nil {
		t.Fatalf("演员榜不该要 token：%v", err)
	}
	if len(actors) != 1 || actors[0].Name != "某演员" {
		t.Fatalf("解析结果不对：%+v", actors)
	}
	if gotAuth != "" {
		t.Errorf("没配 token 时不该带 authorization，got %q", gotAuth)
	}
}
