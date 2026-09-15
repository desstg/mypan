package announcement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseJSON(t *testing.T) {
	raw := []byte(`{
  "notice_version": "2026-08-20",
  "badge": "重要公告",
  "dialog_title": "LitePan v0.5.2 发布",
  "banner": "本版本包含重要安全更新",
  "lead": "请尽快升级到最新版本",
  "issues": [
    { "title": "新增", "body": "新增后台公告功能" },
    { "title": "修复", "body": "修复若干问题" }
  ],
  "footnote": "如遇问题请查看文档站"
}`)
	a := parse(raw)
	if a.Version != "2026-08-20" {
		t.Fatalf("version=%q", a.Version)
	}
	if a.Badge != "重要公告" || a.Title != "LitePan v0.5.2 发布" || a.Banner != "本版本包含重要安全更新" {
		t.Fatalf("badge/title/banner mismatch: %+v", a)
	}
	if a.Lead != "请尽快升级到最新版本" {
		t.Fatalf("lead=%q", a.Lead)
	}
	if len(a.Sections) != 2 || a.Sections[0].Title != "新增" || a.Sections[1].Body != "修复若干问题" {
		t.Fatalf("sections mismatch: %+v", a.Sections)
	}
	if a.Footnote != "如遇问题请查看文档站" {
		t.Fatalf("footnote=%q", a.Footnote)
	}
}

func TestParseJSONMissingVersionFallsBackToHash(t *testing.T) {
	raw := []byte(`{"dialog_title":"公告","lead":"正文"}`)
	a := parse(raw)
	if a.Version == "" || a.Version == "2026-08-20" {
		t.Fatalf("version should fall back to content hash: %q", a.Version)
	}
	if a.Title != "公告" {
		t.Fatalf("title=%q", a.Title)
	}
}

func TestParseJSONMissingTitleDefaults(t *testing.T) {
	raw := []byte(`{"notice_version":"2026-08-21","lead":"只有引导"}`)
	a := parse(raw)
	if a.Title != "公告" {
		t.Fatalf("title=%q want 公告", a.Title)
	}
	if a.Version != "2026-08-21" {
		t.Fatalf("version=%q", a.Version)
	}
}

func TestNormalizeVisibleHiddenValues(t *testing.T) {
	for _, v := range []string{"", "none", "false", "None", "FALSE", "  none  "} {
		if got := normalizeVisible(v); got != "" {
			t.Fatalf("normalizeVisible(%q)=%q want empty", v, got)
		}
	}
	for _, v := range []string{"重要公告", "重要警告", "NEW"} {
		if got := normalizeVisible(v); got == "" {
			t.Fatalf("normalizeVisible(%q) should keep value", v)
		}
	}
}

func TestParseJSONSpecialNoneHidden(t *testing.T) {
	a := parse([]byte(`{"dialog_title":"标题","special":"none"}`))
	if a.Special != "" {
		t.Fatalf("special=none should be hidden, got %q", a.Special)
	}
	a2 := parse([]byte(`{"dialog_title":"标题","banner":"false"}`))
	if a2.Banner != "" {
		t.Fatalf("banner=false should be hidden, got %q", a2.Banner)
	}
}

func TestParseJSONBadgeNoneHidden(t *testing.T) {
	a := parse([]byte(`{"dialog_title":"标题","badge":"none"}`))
	if a.Badge != "" {
		t.Fatalf("badge=none should be hidden, got %q", a.Badge)
	}
}

func TestParseJSONSpecialSection(t *testing.T) {
	raw := []byte(`{
  "notice_version": "2026-08-20",
  "dialog_title": "标题",
  "banner": "警示文字",
  "special": "最近发生的事\n请查看文档站了解详情",
  "lead": "引导"
}`)
	a := parse(raw)
	if a.Banner != "警示文字" {
		t.Fatalf("banner=%q", a.Banner)
	}
	if !strings.Contains(a.Special, "最近发生的事") || !strings.Contains(a.Special, "请查看文档站") {
		t.Fatalf("special=%q", a.Special)
	}
	if a.Lead != "引导" {
		t.Fatalf("lead=%q", a.Lead)
	}
}

func TestParseJSONIgnoresUnknownFields(t *testing.T) {
	// 样板文件里的 _comment 等未知字段应被忽略
	raw := []byte(`{"_comment":"用法说明","notice_version":"2026-08-20","dialog_title":"标题","lead":"正文"}`)
	a := parse(raw)
	if a.Version != "2026-08-20" || a.Title != "标题" || a.Lead != "正文" {
		t.Fatalf("unknown fields should be ignored: %+v", a)
	}
}

func TestParseJSONBlankSectionsFiltered(t *testing.T) {
	raw := []byte(`{"dialog_title":"t","issues":[{"title":"","body":""},{"title":"有效","body":"b"}]}`)
	a := parse(raw)
	if len(a.Sections) != 1 || a.Sections[0].Title != "有效" {
		t.Fatalf("blank section should be filtered: %+v", a.Sections)
	}
}

func TestParsePlainText(t *testing.T) {
	a := parse([]byte("第一行\n第二行\n第三行"))
	if a != nil {
		t.Fatalf("纯文本不应被当作公告展示: %+v", a)
	}
}

func TestParseMarkdownHeadingTitle(t *testing.T) {
	a := parse([]byte("# 维护公告\n正文一\n正文二"))
	if a != nil {
		t.Fatalf("Markdown 不应被当作公告展示: %+v", a)
	}
}

func TestParseInvalidJSONIgnored(t *testing.T) {
	a := parse([]byte("{这不是 JSON\n但确实是文本"))
	if a != nil {
		t.Fatalf("非法 JSON 不应被展示: %+v", a)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	h1 := contentHash("hello")
	h2 := contentHash("hello ")
	h3 := contentHash("world")
	if h1 == h3 {
		t.Fatal("different content should produce different hash")
	}
	if len(h1) != 16 || h1 != contentHash("hello") {
		t.Fatalf("hash unstable: %q", h1)
	}
	if h1 == h2 {
		t.Fatalf("trailing space should change hash: %q vs %q", h1, h2)
	}
}

func TestDisabledWhenURLBlank(t *testing.T) {
	s := New("   ")
	if s.Enabled() {
		t.Fatal("blank url should be disabled")
	}
	item, err := s.Fetch(context.Background())
	if err != nil || item != nil {
		t.Fatalf("disabled service should return nil,nil got item=%v err=%v", item, err)
	}
}

func TestFetchFromRemoteAndCache(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"notice_version":"2026-08-20","dialog_title":"标题","lead":"正文"}`))
	}))
	defer ts.Close()

	s := New(ts.URL)
	ctx := context.Background()

	first, err := s.Fetch(ctx)
	if err != nil || first == nil {
		t.Fatalf("first fetch: item=%v err=%v", first, err)
	}
	if first.Title != "标题" {
		t.Fatalf("title=%q", first.Title)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}

	second, err := s.Fetch(ctx)
	if err != nil || second == nil {
		t.Fatalf("second fetch: item=%v err=%v", second, err)
	}
	if second.Version != first.Version {
		t.Fatalf("version mismatch: %q vs %q", second.Version, first.Version)
	}
	if hits.Load() != 1 {
		t.Fatalf("cache should avoid re-fetch, hits=%d", hits.Load())
	}
}

func TestFetchInvalidContentSilentlyIgnored(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dialog_title":`))
	}))
	defer ts.Close()

	s := New(ts.URL)
	item, err := s.Fetch(context.Background())
	if err != nil || item != nil {
		t.Fatalf("异常公告应静默返回暂无公告: item=%v err=%v", item, err)
	}
}

func TestFetchFailureKeepsPrevious(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"dialog_title":"标题","lead":"正文"}`))
	}))
	defer ts.Close()

	s := New(ts.URL)
	ctx := context.Background()

	// 首次失败：无缓存可保留，返回 nil 且不报错
	if item, err := s.Fetch(ctx); err != nil || item != nil {
		t.Fatalf("first failed fetch should degrade to nil: item=%v err=%v", item, err)
	}

	// 冷却期结束后远端恢复 → 成功拿到内容
	s.failedAt = time.Time{}
	fail.Store(false)
	item, err := s.Fetch(ctx)
	if err != nil || item == nil {
		t.Fatalf("second fetch: item=%v err=%v", item, err)
	}

	// 远端再次失败 → 返回上次成功内容而不是错误
	s.cachedAt = time.Time{} // 强制绕过 TTL 缓存
	s.failedAt = time.Time{}
	fail.Store(true)
	item2, err := s.Fetch(ctx)
	if err != nil || item2 == nil || item2.Version != item.Version {
		t.Fatalf("failed fetch should keep previous, item=%v err=%v", item2, err)
	}
}

func TestFetchOversizeRejected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, maxBodyBytes+1))
	}))
	defer ts.Close()

	s := New(ts.URL)
	item, err := s.Fetch(context.Background())
	if err != nil || item != nil {
		t.Fatalf("oversize should degrade to nil: item=%v err=%v", item, err)
	}
}

// 换地址必须立刻拿到新内容：沿用上一个地址的缓存会让「改了设置没用」，
// 而且是要等满 cacheTTL（10 分钟）才恢复的那种静默故障。
func TestDynamicURLChangeBypassesCache(t *testing.T) {
	serveTitle := func(title string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"notice_version":"` + title + `","dialog_title":"` + title + `"}`))
		}))
	}
	first := serveTitle("旧地址")
	defer first.Close()
	second := serveTitle("新地址")
	defer second.Close()

	current := first.URL
	s := NewDynamic(func() string { return current })
	ctx := context.Background()

	got, err := s.Fetch(ctx)
	if err != nil || got == nil || got.Title != "旧地址" {
		t.Fatalf("首次拉取: item=%v err=%v", got, err)
	}

	current = second.URL
	got, err = s.Fetch(ctx)
	if err != nil || got == nil || got.Title != "新地址" {
		t.Fatalf("换地址后应立刻生效: item=%v err=%v", got, err)
	}

	got, err = s.Fetch(ctx)
	if err != nil || got == nil || got.Title != "新地址" {
		t.Fatalf("同地址应继续走缓存: item=%v err=%v", got, err)
	}
}

// 失败冷却同样要随地址重置：否则在冷却窗口内换到可用地址，仍会被旧地址的失败记录挡住。
func TestDynamicURLChangeClearsFailureCooldown(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dialog_title":"新地址通了"}`))
	}))
	defer good.Close()

	current := bad.URL
	s := NewDynamic(func() string { return current })
	ctx := context.Background()

	if item, _ := s.Fetch(ctx); item != nil {
		t.Fatalf("失败地址不应返回内容: %v", item)
	}

	current = good.URL
	item, err := s.Fetch(ctx)
	if err != nil || item == nil || item.Title != "新地址通了" {
		t.Fatalf("换地址应重置失败冷却: item=%v err=%v", item, err)
	}
}

func TestDynamicURLBlankDisables(t *testing.T) {
	current := ""
	s := NewDynamic(func() string { return current })
	if s.Enabled() {
		t.Fatal("空地址应视为未启用")
	}
	item, err := s.Fetch(context.Background())
	if err != nil || item != nil {
		t.Fatalf("空地址应返回 nil,nil: item=%v err=%v", item, err)
	}
}
