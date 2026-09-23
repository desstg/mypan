package javdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// listPageFixture 是从 https://javdb.com/lists/1BAzv?page=1 上**逐字抄下来**的
// 真实片段（2026-09-21），只裁掉了第二张卡之前的内容。
//
// 用真片段而不是手搓的 HTML：这套正则的全部风险就在于「官网的类名和嵌套随时会变」，
// 手搓的样本永远会跟着正则一起写对，钉不住任何东西。
const listPageFixture = `
    <div class="list-justified">
      <span class="actor-section-name">5分推荐</span>
      <br>
      <span class="section-meta">459部影片</span>
    </div>
    <div class="movie-list h cols-4">
      <div class="item">
      <a href="/v/vezpEn" class="box" title="【FANZA限定】雨の日にコインランドリーに現れ、洗濯が終わるまでエッチな体が完全無防備な隙だらけお姉さん 楓ふうあ 生写真3枚セット">
    <div class="cover ">
        <span class="tag-can-play cnsub">
          中字可播放
        </span>
      <img loading="lazy" src="https://c0.jdbstatic.com/covers/ve/vezpEn.jpg" />
    </div>
    <div class="video-title"><strong>SNOS-283</strong> 【FANZA限定】雨の日にコインランドリーに現れ…</div>
    <div class="score">
      <span class="value"><span class="score-stars"><i class="icon-star"></i><i class="icon-star"></i><i class="icon-star"></i><i class="icon-star"></i><i class="icon-star gray"></i></span>
          &nbsp;
        4.33分, 由2420人評價</span>
    </div>
    <div class="meta">
      2026-06-10
    </div>
    <div class="tags has-addons">
            <span class="tag is-warning">含中字磁鏈</span>
</div>
      </a>
      </div>
      <div class="item">
      <a href="/v/2bkZ9" class="box" title="もう一度、妻と。">
    <div class="cover ">
      <img loading="lazy" src="https://c0.jdbstatic.com/covers/2b/2bkZ9.jpg" />
    </div>
    <div class="video-title"><strong>ABC-123</strong> もう一度、妻と。</div>
    <div class="meta">
      2025-01-02
    </div>
      </a>
      </div>
    </div>`

func TestParseListPage(t *testing.T) {
	movies, total := ParseListPage(listPageFixture)

	if total != 459 {
		t.Errorf("总数应当从「459部影片」里认出来，got %d", total)
	}
	if len(movies) != 2 {
		t.Fatalf("应当解出 2 部影片，got %d: %+v", len(movies), movies)
	}

	first := movies[0]
	if first.ID != "vezpEn" {
		t.Errorf("id 应当从 /v/ 后面取，got %q", first.ID)
	}
	if first.Number != "SNOS-283" {
		t.Errorf("番号应当取自 <strong>，got %q", first.Number)
	}
	// 封面取自 <img src>，但**换了域名**：页面写的是 c0.jdbstatic.com，
	// 而那个域名在本机连不上（连接被重置），同一个文件只有
	// tp.spfcas.com/rhe951l4q/… 取得回来。见 NormalizeListCover。
	if first.CoverURL != "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg" {
		t.Errorf("封面应当换成可达的那个 CDN，got %q", first.CoverURL)
	}
	// 日期那个 div 里的换行与缩进要靠 `\s*` 吃掉。
	if first.ReleaseDate != "2026-06-10" {
		t.Errorf("上映日期应当取自 <div class=meta>，got %q", first.ReleaseDate)
	}
	if first.Title == "" {
		t.Error("标题应当取自 <a title>")
	}

	if movies[1].ID != "2bkZ9" || movies[1].Number != "ABC-123" {
		t.Errorf("第二张卡解错了: %+v", movies[1])
	}
}

// TestNormalizeListCover 封面换域名。
//
// 官网页面与 API 给的是**两个不同的 CDN 域名**，只有一个在本机取得到 ——
// 不换的话清单页的卡片会是一排空图（URL 在库里躺着，图片接口一直失败）。
func TestNormalizeListCover(t *testing.T) {
	cases := map[string]string{
		// 页面形态 → 换成可达的那个（带上混淆路径前缀）。
		"https://c0.jdbstatic.com/covers/ve/vezpEn.jpg": "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg",
		"https://c1.jdbstatic.com/covers/ab/x.jpg":      "https://tp.spfcas.com/rhe951l4q/covers/ab/x.jpg",
		"http://c2.jdbstatic.com/covers/ab/x.jpg":       "https://tp.spfcas.com/rhe951l4q/covers/ab/x.jpg",
		// 已经是可达域名（含子域）的，原样返回，别重复套前缀。
		"https://tp.spfcas.com/rhe951l4q/covers/ve/a.jpg": "https://tp.spfcas.com/rhe951l4q/covers/ve/a.jpg",
		"https://tp.spfcas.com/covers/ve/a.jpg":           "https://tp.spfcas.com/covers/ve/a.jpg",
		// 别的域名不碰 —— 换了反而可能换出个 404。
		"https://pics.javbus.com/x.jpg": "https://pics.javbus.com/x.jpg",
		// 认不出来的地址原样返回，不要吞掉。
		"":                 "",
		"/covers/局部路径.jpg": "/covers/局部路径.jpg",
	}
	for in, want := range cases {
		if got := NormalizeListCover(in); got != want {
			t.Errorf("NormalizeListCover(%q)\n  = %q\n  want %q", in, got, want)
		}
	}
}

// 卡片里缺字段时留空，不能 panic、也不能把整页丢掉。
func TestParseListPageToleratesMissingFields(t *testing.T) {
	html := `<a href="/v/abc12" class="box" title="光秃秃的一张卡"></a>`
	movies, total := ParseListPage(html)
	if len(movies) != 1 {
		t.Fatalf("应当解出 1 部，got %d", len(movies))
	}
	if movies[0].Number != "" || movies[0].CoverURL != "" || movies[0].ReleaseDate != "" {
		t.Errorf("缺字段应当是空值，got %+v", movies[0])
	}
	if total != 0 {
		t.Errorf("没有「N部影片」时总数应当是 0，got %d", total)
	}
}

// TestListPageCachesPerPage 同一个清单的同一页只打上游一次。
//
// 官网清单页比 API 脆得多（实测一会儿 200、一会儿 403、一会儿超时，连抓七八次
// 就会被挡一阵），而翻页、来回翻、重开同一个清单都在重复请求同一页。
func TestListPageCachesPerPage(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// 只认第 2 页，其余给空页 —— 顺便验「空页也进缓存」。
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, listPageFixture)
			return
		}
		_, _ = fmt.Fprint(w, `<div class="movie-list"></div>`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	// 第一次：真打上游。
	first, total, err := c.ListPage(ctx, "1BAzv", 2)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(first) != 2 || total != 459 {
		t.Fatalf("解错了: %d 部 / 总数 %d", len(first), total)
	}
	if hits.Load() != 1 {
		t.Fatalf("第一次应当打一次上游，got %d", hits.Load())
	}

	// 第二次同一页：命中缓存，不再打上游。
	again, total2, err := c.ListPage(ctx, "1BAzv", 2)
	if err != nil {
		t.Fatalf("ListPage(cached): %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("同一页第二次不该再打上游，got %d 次", hits.Load())
	}
	if len(again) != 2 || total2 != 459 {
		t.Errorf("缓存应当返回同一份结果，got %d 部 / %d", len(again), total2)
	}

	// 另一页是另一条缓存 —— 不能被上一页顶掉。
	if _, _, err := c.ListPage(ctx, "1BAzv", 7); err != nil {
		t.Fatalf("ListPage(另一页): %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("换一页应当重新打上游，got %d 次", hits.Load())
	}

	// 空页也进缓存：翻到末页之后来回翻不该反复去打那个空页。
	if _, _, err := c.ListPage(ctx, "1BAzv", 7); err != nil {
		t.Fatalf("ListPage(空页缓存): %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("空页也该进缓存，got %d 次", hits.Load())
	}

	// 另一个清单不共用缓存。
	if _, _, err := c.ListPage(ctx, "ZZ9wJ", 2); err != nil {
		t.Fatalf("ListPage(另一个清单): %v", err)
	}
	if hits.Load() != 3 {
		t.Errorf("不同清单不该共用缓存，got %d 次", hits.Load())
	}
}

// TestListPageDoesNotCacheFailure 失败不进缓存，也不反复重试 403。
//
// 两件事一起钉：
//   - 把一次 403 记十分钟，等于让用户干等十分钟 —— 而它下一分钟可能就好了；
//   - 403 是被挡的信号，重试只会把封锁拖长（连抓七八次就会稳定 403）。
func TestListPageDoesNotCacheFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, listPageFixture)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	if _, _, err := c.ListPage(ctx, "L1", 1); err == nil {
		t.Fatal("第一次应当失败")
	}
	// 403 只打一次 —— 不当成「值得重试」的状态。
	if hits.Load() != 1 {
		t.Errorf("403 不该重试，got %d 次请求", hits.Load())
	}
	// 第二次应当重打上游并成功 —— 说明失败没被缓存。
	movies, _, err := c.ListPage(ctx, "L1", 1)
	if err != nil {
		t.Fatalf("失败不该被缓存，第二次应当重试: %v", err)
	}
	if len(movies) != 2 {
		t.Errorf("第二次应当拿到内容，got %d 部", len(movies))
	}
}

// newTestClient 造一个把官网指向本地测试服务器的客户端。
//
// 不传 ProxyURL：Go 的 ProxyFromEnvironment 对 127.0.0.1 本来就不走代理，
// 传一个相对地址（比如 "direct"）反而会让 url.Parse 解析出一个坏代理。
func newTestClient(t *testing.T, siteBase string) *Client {
	t.Helper()
	c, err := New(Options{SiteBase: siteBase, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// 翻到末页时官网给的是一张卡都没有的页面 —— 那不是错误，是「到底了」。
func TestParseListPageEmpty(t *testing.T) {
	movies, total := ParseListPage(`<div class="movie-list"></div>`)
	if len(movies) != 0 || total != 0 {
		t.Errorf("空页应当是 0 部 0 总数，got %d / %d", len(movies), total)
	}
}
