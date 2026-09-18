package tgsubscribe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

const probeShareURL = "https://115cdn.com/s/swsa2t23zrk?password=t58d"

// 夸克分享链。它至今没有投递器，是「识别得到但投不出去」这一类唯一的现实样本
// （115 分享已经能自动转存了，前提是账号配了网页 Cookie）。
const probeQuarkURL = "https://pan.quark.cn/s/abcdef123456"

func newProbeService() *Service {
	s := &Service{registry: newRegistry()}
	s.pusher = &Pusher{svc: s}
	s.shareSaver = &ShareSaveDeliverer{svc: s}
	s.deliverers = []Deliverer{s.pusher, s.shareSaver}
	return s
}

func pageWith(msgs ...*telegram.Message) *preview.Page {
	posts := make([]preview.Post, 0, len(msgs))
	for _, m := range msgs {
		posts = append(posts, preview.Post{Message: m})
	}
	return &preview.Page{Posts: posts}
}

// 实测 QukanMovie 的真实形态：标题在第一行，下载地址在「点击跳转」这个
// 指向机器人的 url 按钮上 —— 按钮一大堆，但一条资源都抽不出来。
func botDeepLinkPost() *telegram.Message {
	return &telegram.Message{
		MessageID: 11140,
		Text:      "📺 生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1\n💾 大小： 1.18 GB\n🔗 链接： 点击跳转",
		ReplyMarkup: &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{
			{
				{Text: "点击跳转", URL: "https://t.me/tougao115guaguale_bot?start=qtrans_3836"},
				{Text: "频道", URL: "https://t.me/QukanMovie"},
			},
			{
				{Text: "搜索", URL: "https://t.me/jisou2?start=a_912917457"},
				{Text: "合作", URL: "https://t.me/Movie888035"},
			},
		}},
	}
}

// 实测 oneonefivewpfx 的形态：正文链接全指向第三方中转站。
func transitSitePost() *telegram.Message {
	return &telegram.Message{
		MessageID: 42335,
		Text:      "影片名 (2024) 1080p 点击查看",
		Entities: []telegram.MessageEntity{{
			Type: "text_link", Offset: 0, Length: 4,
			URL: "https://re0.me/resource/115/5cf2c05d2c9b4b12ab7571e0c20e1c70",
		}},
	}
}

func sharePost(msgID int64, title string) *telegram.Message {
	return &telegram.Message{
		MessageID: msgID,
		Text:      title + "\n💾 大小： 1.18 GB\n🔗 链接： 点击跳转",
		Entities: []telegram.MessageEntity{{
			Type: "text_link", Offset: 0, Length: 4, URL: probeShareURL,
		}},
	}
}

func quarkPost(msgID int64, title string) *telegram.Message {
	return &telegram.Message{
		MessageID: msgID,
		Text:      title + "\n💾 大小： 1.18 GB\n🔗 链接： 点击跳转",
		Entities: []telegram.MessageEntity{{
			Type: "text_link", Offset: 0, Length: 4, URL: probeQuarkURL,
		}},
	}
}

func TestProbeWarningBotDeepLinks(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(botDeepLinkPost()))

	if scan.total() != 0 {
		t.Fatalf("机器人深链不该抽出资源，实际 %d", scan.total())
	}
	// 4 个按钮里 2 个是机器人深链（tougao..._bot?start= 与 jisou2?start=），
	// 另 2 个是频道互推，不该被数进来。
	if scan.botLinks != 2 {
		t.Fatalf("机器人深链数 = %d, want 2", scan.botLinks)
	}
	warnings := s.probeWarnings(scan, 4)
	if len(warnings) != 1 {
		t.Fatalf("应给出一条结论，实际 %v", warnings)
	}
	for _, want := range []string{"机器人", "@xxx_bot", "建议换一个"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("结论应包含 %q：%s", want, warnings[0])
		}
	}
}

// 有第三方域名时优先解释中转站，而不是笼统地说「没有链接」。
func TestProbeWarningTransitSites(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(transitSitePost()))

	if len(scan.hosts) != 1 {
		t.Fatalf("第三方域名 = %v, want [re0.me]", scan.hosts)
	}
	if scan.botLinks != 0 {
		t.Fatalf("这条没有机器人深链，实际 %d", scan.botLinks)
	}
	warnings := s.probeWarnings(scan, 0)
	if len(warnings) != 1 {
		t.Fatalf("应给出一条结论，实际 %v", warnings)
	}
	if !strings.Contains(warnings[0], "re0.me") {
		t.Errorf("结论应点名域名：%s", warnings[0])
	}
	if !strings.Contains(warnings[0], "第三方站点") {
		t.Errorf("结论应说明是第三方站点：%s", warnings[0])
	}
}

// 分享域不算「第三方站点」——它是有资源、只是投不出去的那一类。
func TestProbeScanDoesNotCountShareHostAsExternal(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(sharePost(1, "生逢其时 (2026) S01E15 4K WEB-DL")))
	if len(scan.hosts) != 0 {
		t.Fatalf("115 分享域不该被算成第三方站点，实际 %v", scan.hosts)
	}
}

// 一条资源都抽不到、也没有第三方域名与机器人时，退回「点击复制按钮」这条老结论。
func TestProbeWarningNoLinksAtAll(t *testing.T) {
	s := newProbeService()
	msg := &telegram.Message{MessageID: 1, Text: "📺 某剧 (2026) S01E01 4K WEB-DL"}
	scan := s.scanPage(pageWith(msg))

	warnings := s.probeWarnings(scan, 0)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "点击复制") {
		t.Fatalf("应退回「点击复制」的结论，实际 %v", warnings)
	}
}

// 按钮有、但全是频道互推 —— 既不是机器人深链，也没有第三方域名。
func TestProbeWarningButtonsAreAllCrossPromo(t *testing.T) {
	s := newProbeService()
	msg := &telegram.Message{
		MessageID: 1,
		Text:      "📺 某剧 (2026) S01E01 4K WEB-DL",
		ReplyMarkup: &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
			{Text: "频道", URL: "https://t.me/otherchannel"},
		}}},
	}
	scan := s.scanPage(pageWith(msg))

	if scan.botLinks != 0 {
		t.Fatalf("频道互推不是机器人深链，实际数成 %d", scan.botLinks)
	}
	warnings := s.probeWarnings(scan, 1)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "互推") {
		t.Fatalf("应指出按钮都是互推，实际 %v", warnings)
	}
}

// 只识别到分享链时，要说清「能不能自动转存取决于什么」。
//
// 115 分享已经能自动转存，但前提是目标账号配了网页 Cookie —— 体检是按频道做的，
// 没有账号上下文，所以只能把前提讲清楚，不能下「投不了」的结论。
func TestProbeWarningOnlyShares(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(
		sharePost(1, "生逢其时 (2026) S01E15 4K WEB-DL"),
		sharePost(2, "绿灯军团 (2026) S01E05 4K WEB-DL"),
	))

	if scan.total() != 2 || scan.shares() != 2 {
		t.Fatalf("应抽到 2 条分享，实际 total=%d shares=%d", scan.total(), scan.shares())
	}
	warnings := s.probeWarnings(scan, 0)
	if len(warnings) == 0 {
		t.Fatal("应给出结论")
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "115 分享") {
		t.Errorf("结论应点名类型：%s", joined)
	}
	if !strings.Contains(joined, "Cookie") {
		t.Errorf("结论应说明自动转存的前提：%s", joined)
	}
	if !strings.Contains(joined, "暂不支持投递") {
		t.Errorf("结论应说明没配 Cookie 时会是什么样：%s", joined)
	}
}

// 混合情况（既有能推的、也有投不了的）也必须报出来。
//
// 实测 QukanMovie 就是这样：16 条资源里 15 条是分享链，只有 1 条 ed2k 能推。
// 只报「有能推的」会让用户以为自己没漏东西 —— 那恰恰是这次改造要解决的困惑。
func TestProbeWarnsAboutUndeliverableEvenWhenSomethingIsDeliverable(t *testing.T) {
	s := newProbeService()
	magnet := &telegram.Message{
		MessageID: 1,
		Text:      "生逢其时 (2026) S01E15 4K WEB-DL\nmagnet:?xt=urn:btih:" + strings.Repeat("a", 40),
	}
	scan := s.scanPage(pageWith(magnet, quarkPost(2, "绿灯军团 (2026) S01E05 4K WEB-DL")))

	warnings := s.probeWarnings(scan, 0)
	if len(warnings) != 1 {
		t.Fatalf("应给出「有投不了的资源」这一条，实际 %v", warnings)
	}
	if !strings.Contains(warnings[0], "夸克分享") {
		t.Errorf("结论应点名投不了的类型：%s", warnings[0])
	}
	if strings.Contains(warnings[0], "没有可投递的资源") {
		t.Errorf("有磁力可推，不该说「没有可投递的资源」：%s", warnings[0])
	}
}

// 全部可投递时不产生任何告警。
func TestProbeNoWarningWhenEverythingIsDeliverable(t *testing.T) {
	s := newProbeService()
	magnet := &telegram.Message{
		MessageID: 1,
		Text:      "生逢其时 (2026) S01E15 4K WEB-DL\nmagnet:?xt=urn:btih:" + strings.Repeat("a", 40),
	}
	scan := s.scanPage(pageWith(magnet))
	if warnings := s.probeWarnings(scan, 0); len(warnings) != 0 {
		t.Fatalf("全部可投递时不该有告警，实际 %v", warnings)
	}
}

// 计数要给前端带上中文名与「能不能投递」，前端据此把两类分开显示。
func TestProbeResourceCounts(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(
		sharePost(1, "生逢其时 (2026) S01E15 4K WEB-DL"),
		&telegram.Message{
			MessageID: 2,
			Text:      "迪迦奥特曼 (2000) 1080p\ned2k://|file|Movie.mkv|100|4d517deece354c11fe7e497999956663|/",
		},
	))

	counts := s.probeResourceCounts(scan)
	if len(counts) != len(probeKindOrder) {
		t.Fatalf("应给出全部 %d 种类型（含 0 的），实际 %d", len(probeKindOrder), len(counts))
	}
	// 顺序固定，前端才能稳定渲染。
	for i, item := range counts {
		if item.Kind != probeKindOrder[i] {
			t.Errorf("第 %d 项是 %q，want %q", i, item.Kind, probeKindOrder[i])
		}
		if item.Label == "" {
			t.Errorf("%s 缺中文名", item.Kind)
		}
	}
	byKind := map[string]ProbeResourceCount{}
	for _, item := range counts {
		byKind[item.Kind] = item
	}
	if got := byKind[KindShare115]; got.Count != 1 || !got.Deliverable {
		t.Errorf("115 分享 = %+v, want count=1 deliverable=true（能转存，前提是账号配了网页 Cookie）", got)
	}
	if got := byKind[KindED2K]; got.Count != 1 || !got.Deliverable {
		t.Errorf("ed2k = %+v, want count=1 deliverable=true", got)
	}
	if got := byKind[KindMagnet]; got.Count != 0 || !got.Deliverable {
		t.Errorf("磁力 = %+v, want count=0 deliverable=true", got)
	}
}

// 抽到资源、但发布名不像影视资源的帖子要单独计数 ——
// 否则用户会看到「识别到 1 条 115 分享」却在匹配历史里一条都找不到。
func TestProbeCountsDiscardedPosts(t *testing.T) {
	s := newProbeService()
	scan := s.scanPage(pageWith(
		sharePost(1, "生逢其时 (2026) S01E15 4K WEB-DL"),
		sharePost(2, "《某某》更新啦"),
	))

	if scan.total() != 2 {
		t.Fatalf("两条都该抽出资源，实际 %d", scan.total())
	}
	if scan.discarded != 1 {
		t.Fatalf("应数出 1 条被丢弃的帖子，实际 %d", scan.discarded)
	}
	warnings := s.probeWarnings(scan, 0)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "不会进入匹配历史") {
		t.Errorf("应说明有帖子被丢弃：%v", warnings)
	}
}

func TestIsBotDeepLink(t *testing.T) {
	cases := map[string]bool{
		// 用户名带 _bot 后缀。
		"https://t.me/tougao115guaguale_bot?start=qtrans_3836": true,
		"https://t.me/some_bot":                                true,
		"https://telegram.me/other_bot/x":                      true,
		// 名字不带后缀，但 `?start=` 是机器人深链的标准参数（实测 jisou2 就是这样）。
		"https://t.me/jisou2?start=a_912917457": true,
		// 普通频道链接与频道内消息链接都不是。
		"https://t.me/QukanMovie":       false,
		"https://t.me/QukanMovie/11140": false,
		"https://t.me/otherchannel":     false,
		"https://evil.com/x_bot":        false,
		"https://evil.com/x?start=1":    false,
		"https://t.me/":                 false,
		"":                              false,
	}
	for raw, want := range cases {
		if got := isBotDeepLink(raw); got != want {
			t.Errorf("isBotDeepLink(%q) = %v, want %v", raw, got, want)
		}
	}
}

// 体检在**真实抓取的页面**上的表现定盘。
//
// 这条测试存在的理由：体检的全部价值就是「把静默失败说清楚」，而它对不对
// 只有在真实频道上才看得出来。两个 fixture 恰好各是一种典型，而且老实现
// 在两者上都是错的（QukanMovie 不告警、oneonefivewpfx 报错原因）。
//
// 数字写死是刻意的 —— 改动者必须解释为什么变了，而不是让回归悄悄溜过去。
func TestProbeOnRealFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		want    map[string]int
		// wantHosts 是正文里指向第三方站点的域名（体检据此判断「链接都在中转站上」）。
		wantHosts []string
		// wantBotLinks 是指向机器人的按钮/深链数。
		wantBotLinks int
		// wantWarn 是结论里必须出现的关键词。
		wantWarn []string
	}{
		{
			// 按钮一大堆（64 个），但下载地址要点进机器人私聊才拿得到；
			// 正文里另有 15 条 115 分享链（能转存，前提是账号配了网页 Cookie）+ 1 条 ed2k。
			//
			// 断言的是「分享转存的前提被说清楚了」，而不是「115 分享 15」这种计数行 ——
			// 计数只在「投不了」那一档里出现，115 分享已经不在那一档了。
			fixture:      "qukanmovie_newest.html",
			want:         map[string]int{telegram.ResourceKindShare115: 15, telegram.ResourceKindED2K: 1},
			wantHosts:    []string{"nekocloud.host", "www.ezer.cc"},
			wantBotLinks: 32,
			wantWarn:     []string{"115 分享链", "网页 Cookie"},
		},
		{
			// 按钮 0 个，正文链接全指向中转站 —— 老实现会错误地归咎于「点击复制按钮」。
			fixture:  "oneonefivewpfx_newest.html",
			want:     map[string]int{},
			wantWarn: []string{"第三方站点", "re0.me"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			s := newProbeService()
			page := fetchFixture(t, tc.fixture)
			scan := s.scanPage(page)

			if len(scan.counts) != len(tc.want) {
				t.Fatalf("资源类型集合 = %v, want %v", scan.counts, tc.want)
			}
			for kind, want := range tc.want {
				if scan.counts[kind] != want {
					t.Errorf("%s 命中 %d 条, want %d", kind, scan.counts[kind], want)
				}
			}
			if scan.botLinks != tc.wantBotLinks {
				t.Errorf("机器人深链数 = %d, want %d", scan.botLinks, tc.wantBotLinks)
			}
			for _, host := range tc.wantHosts {
				if _, ok := scan.hosts[host]; !ok {
					t.Errorf("第三方域名缺 %q，实际 %v", host, scan.topHosts(5))
				}
			}
			// 直链抽取器在真实页面上必须零误报 —— 白名单判据的关键证据。
			if n := scan.counts[telegram.ResourceKindHTTP]; n != 0 {
				t.Errorf("直链误报 %d 条", n)
			}

			urlButtons := 0
			for i := range page.Posts {
				urlButtons += page.Posts[i].URLButtonCount
			}
			joined := strings.Join(s.probeWarnings(scan, urlButtons), "\n")
			if len(tc.wantWarn) > 0 && joined == "" {
				t.Fatal("应当给出结论，实际没有")
			}
			for _, want := range tc.wantWarn {
				if !strings.Contains(joined, want) {
					t.Errorf("结论应包含 %q：%s", want, joined)
				}
			}
		})
	}
}

// fetchFixture 用真实的 preview 客户端去抓 testdata 里的页面（换成 httptest 供给），
// 这样「抓取 → 解析 → 抽取 → 体检」走的是生产同一条链路。
func fetchFixture(t *testing.T, name string) *preview.Page {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("preview", "testdata", name))
	if err != nil {
		t.Fatalf("读取 fixture %s 失败: %v", name, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	page, err := preview.NewClient(preview.ClientOptions{Host: srv.URL}).Fetch(context.Background(), "x", 0)
	if err != nil {
		t.Fatalf("抓取 fixture %s 失败: %v", name, err)
	}
	return page
}

// 体检是只读的：跑一遍不应该改动任何东西（这里只验证它不 panic 且不依赖
// 未注入的依赖 —— registry / deliverers 之外一个都不碰）。
func TestScanPageToleratesMissingDeps(t *testing.T) {
	s := &Service{} // 连 registry 都没有
	scan := s.scanPage(pageWith(botDeepLinkPost(), sharePost(3, "某剧 (2026) 4K")))
	if scan.posts != 2 {
		t.Fatalf("posts = %d, want 2", scan.posts)
	}
	if scan.total() != 0 {
		t.Fatalf("没有 registry 时抽不出任何东西，实际 %d", scan.total())
	}
	// 没有投递器 → 一切都不可投递，这时不该 panic。
	if counts := s.probeResourceCounts(scan); len(counts) != len(probeKindOrder) {
		t.Fatalf("counts = %d", len(counts))
	}
	_ = s.probeWarnings(scan, 0)
}
