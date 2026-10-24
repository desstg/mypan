package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// newJavActorWallTestService 起一个只挂了设置的 Service，并把演员索引**同步**建好。
//
// 索引平时是后台建的（见 javwall_actor_index.go）：请求路径上绝不扫盘，所以线上
// 第一次打开演员墙拿到的是 `building: true`。测试要的是确定性的结果，这里直接
// 同步建一轮 —— `warmJavActorIndexNow` 就是为此暴露的。
func newJavActorWallTestService(t *testing.T, settingSvc *settings.Service) *Service {
	t.Helper()
	s := &Service{
		settings:      settingSvc,
		javWallCache:  map[string]*javWallSnapshot{},
		javTitleCache: map[string]javTitleEntry{},
	}
	if s.warmJavActorIndexNow() == nil {
		t.Fatal("演员索引没建起来")
	}
	return s
}

// 演员索引是**后台**建的：第一次打开拿到的是 `building: true` + 空表，
// 而这个空表绝不能读成「墙上一演员都没有」（用户 2026-10-24 报的 502 就是从
// 「同步建索引」改过来的）。
//
// 这里钉住三条：
//  1. 手上一份都没有时报 building（前端据此显示「正在整理」而不是空态）；
//  2. 已有一份时**立刻**给（哪怕在重建中），绝不阻塞请求；
//  3. 墙上的卡变了，旧索引立刻不算数 —— 否则会一直显示删掉那部片的演员。
func TestJavActorIndexServesCacheAndInvalidatesOnChange(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("有码/NIMA-086/NIMA-086.strm", "url")
	writeFile("有码/NIMA-086/NIMA-086.nfo",
		`<movie><num>NIMA-086</num><actor><name>彩月七緒</name></actor></movie>`)

	settingSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{
			{ID: "aaaa1111", Name: "影库", Root: root, MediaKind: settings.WallLocalKindJav},
		}),
	})
	// 注意：这里**不**用 newJavActorWallTestService —— 要的就是「索引还没建」那个状态。
	s := &Service{
		settings:      settingSvc,
		javWallCache:  map[string]*javWallSnapshot{},
		javTitleCache: map[string]javTitleEntry{},
	}

	// 第一趟：一份都没有，必须说「在建」，而不是给一个空结果让前端说「没有演员」。
	if idx, _ := s.javActorIndexSnapshot(context.Background()); idx != nil {
		t.Fatal("索引还没建，不该有东西可给")
	}
	list := s.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if !list.Building || len(list.Items) != 0 {
		t.Fatalf("第一次打开应当回 building=true 的空页：%+v", list)
	}

	// 详情类请求等不到索引时：说「正在整理」，**不是**「没有这位演员」。
	// （这里是**直接**验那句话：真跑 `javActorLookup` 会等那一轮后台建完 ——
	// 本地临时目录几百毫秒就好，等得到，测不出「等不到」那一支。）
	if msg := errJavActorIndexBuilding().Error(); !strings.Contains(msg, "正在整理") {
		t.Fatalf("索引没建好时详情要报「正在整理」，got %q", msg)
	}
	if appErr, ok := domain.AsAppError(errJavActorIndexBuilding()); ok && appErr.Code == domain.CodeNotFound {
		t.Fatal("不能用 NOT_FOUND：前端会说成「没有这位演员」，而人明明在墙上")
	}

	// 建好之后：请求路径上**立刻**能拿到（同步再建一次）。
	if s.warmJavActorIndexNow() == nil {
		t.Fatal("索引没建起来")
	}
	list = s.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if list.Building || list.Total != 1 || list.Items[0].Name != "彩月七緒" {
		t.Fatalf("建好之后应当直接给名单：%+v", list)
	}

	// 墙上的卡变了（这里模拟「快照被作废」）：旧索引立刻不算数了 —— 但**照旧给**
	// 那一份旧名单（stale-while-revalidate），同时起一轮后台重建。
	// 给一句「正在整理」反而更糟：用户刚才还看得见这些人。
	s.InvalidateJavWallAll()
	list = s.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if list.Total != 1 || list.Items[0].Name != "彩月七緒" {
		t.Fatalf("重建期间应当先把旧名单给出去：%+v", list)
	}
	s.javActorIdxMu.Lock()
	rebuilding := s.javActorIdxBuilding != nil
	s.javActorIdxMu.Unlock()
	if !rebuilding {
		t.Fatal("墙变了却没起重建：索引会一直是旧的")
	}
	// 等那一轮跑完（它读的是 t.TempDir 里的本地盘，很快），别把 goroutine 留到下一个用例。
	s.waitJavActorIndex(context.Background(), 10*time.Second)
}

// 演员墙：数据来自**墙上的影片**（侧车 json 优先、nfo 兜底），不是上游演员库。
//
// 这里摆两棵独立的「墙」（两个本地目录源），验三件事：
//  1. 两个源上的演员都被聚合进来（不是只看当前那一面）；
//  2. 「N 部」数的是**墙上的卡片数**（同一部片的两个分片算两部，这是墙的口径）；
//  3. nfo-only 的那位演员会并到侧车那批的 id 上（不裂成两张卡）。
func TestJavWallActorsAggregateAllSources(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeFile := func(root, rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sidecar := `{"schema":"litepan.jav.sidecar/1","number":"NIMA-086","title":"テスト","number_letter":"NIMA",
"actors":[{"id":"ACT-1","name":"彩月七緒","gender":0,"avatar":"https://x/a.jpg"},
          {"id":"ACT-2","name":"デカ吉","gender":1}],
"dest":{"added_at":"2026-09-20T10:00:00+08:00"}}`
	writeFile(rootA, "有码/NIMA-086/NIMA-086.json", sidecar)
	writeFile(rootA, "有码/NIMA-086/NIMA-086.strm", "url")
	writeFile(rootA, "有码/NIMA-086/NIMA-086.nfo", `<movie><num>NIMA-086</num><title>テスト</title>
<actor><name>彩月七緒</name></actor><actor><name>デカ吉</name></actor></movie>`)

	// 第二个源：一部只有 nfo 的片（名字与上面那位相同 —— 用来验「按名字并到 id 上」）。
	writeFile(rootB, "无码/ABC-001/ABC-001.strm", "url")
	writeFile(rootB, "无码/ABC-001/ABC-001.nfo", `<movie><num>ABC-001</num><title>別の作品</title>
<actor><name>彩月七緒</name></actor><actor><name>新人 花子</name></actor></movie>`)
	// 同一目录的第二个分片：墙上就是两张卡，「N 部」也跟着 +1（墙的口径，不是上游的参演数）。
	writeFile(rootB, "无码/ABC-001/ABC-001-cd2.strm", "url")
	writeFile(rootB, "无码/ABC-001/ABC-001-cd2.nfo", `<movie><num>ABC-001</num><title>別の作品 cd2</title>
<actor><name>彩月七緒</name></actor></movie>`)

	settingSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{
			{ID: "aaaa1111", Name: "影库A", Root: rootA, MediaKind: settings.WallLocalKindJav},
			{ID: "bbbb2222", Name: "影库B", Root: rootB, MediaKind: settings.WallLocalKindJav},
			// TMDB 档的那面墙不该被算进来（它没有番号演员这个概念）。
			{ID: "cccc3333", Name: "电影", Root: t.TempDir(), MediaKind: settings.WallLocalKindTmdb},
		}),
	})
	s := newJavActorWallTestService(t, settingSvc)

	// 默认口径：**不列男优**（用户要求）。3 位里有一位是男优（デカ吉），所以默认只剩 2 位。
	list := s.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if list.Sources != 2 {
		t.Errorf("只算两面粉号墙（TMDB 那面不算），got %d", list.Sources)
	}
	if list.Movies != 3 {
		t.Errorf("墙上的卡片数应当是 3（1 + 2 个分片），got %d", list.Movies)
	}
	if list.Total != 2 {
		t.Fatalf("默认不列男优：应当有 2 位演员，got %d：%+v", list.Total, list.Items)
	}
	if list.MaleCount != 1 {
		t.Errorf("被挡掉的男优数要报出去（前端用它提示），got %d", list.MaleCount)
	}
	for _, c := range list.Items {
		if c.Gender == 1 {
			t.Fatalf("默认列表里不该出现男优：%+v", c)
		}
	}

	// 勾上「显示男演员」之后才是全量。
	list = s.ListJavWallActors(context.Background(), "", 1, 60, true, "")
	if list.Total != 3 {
		t.Fatalf("应当有 3 位演员，got %d：%+v", list.Total, list.Items)
	}
	if list.MaleCount != 1 {
		t.Errorf("男优数不随开关变（它报的是「挡掉了多少」），got %d", list.MaleCount)
	}

	byName := map[string]JavWallActorCard{}
	for _, c := range list.Items {
		byName[c.Name] = c
	}
	// 排序：部数降序。
	if list.Items[0].Name != "彩月七緒" || list.Items[0].MoviesCount != 3 {
		t.Fatalf("排头应当是参演 3 部的彩月七緒，got %+v", list.Items[0])
	}
	qiyo := byName["彩月七緒"]
	if qiyo.ID != "ACT-1" || qiyo.AvatarURL != "https://x/a.jpg" {
		t.Errorf("侧车那份的 id / 头像要跟着并进来：%+v", qiyo)
	}
	if byName["デカ吉"].Gender != 1 || byName["デカ吉"].MoviesCount != 1 {
		t.Errorf("男优标记与部数不对：%+v", byName["デカ吉"])
	}
	// nfo-only 的那位：没有 id / 头像，但必须在榜上（不许因为 sources 只扫一面而漏掉）。
	if got := byName["新人 花子"]; got.MoviesCount != 1 || got.ID != "" {
		t.Errorf("只出现在 nfo 里的演员也要列出来：%+v", got)
	}

	// 关键词按名字过滤（大小写不敏感，中文原样）。名字命中一位男优时，
	// 默认口径下结果为空 —— 这正是 MaleCount 存在的理由（前端要说「有一位男演员，
	// 勾上开关就能看到」，而不是「没找到」）。
	if got := s.ListJavWallActors(context.Background(), "デカ", 1, 60, false, ""); got.Total != 0 || got.MaleCount != 1 {
		t.Errorf("只命中男优时默认应为空、且报出 male_count：%+v", got)
	}
	if got := s.ListJavWallActors(context.Background(), "デカ", 1, 60, true, ""); got.Total != 1 || got.Items[0].Name != "デカ吉" {
		t.Errorf("关键词过滤不对：%+v", got)
	}

	// 点名（`anchor`）：详情页里点别名就是这条路。别名常常来自 avbase / JAVDB，
	// 墙上的索引里根本没有这个写法 —— 只按关键词过滤会得到一个空列表，用户看到
	// 的就是「点别名搜索结果为空」。点名之后必须与搜主名看到的是同一张卡。
	if got := s.ListJavWallActors(context.Background(), "Nana Saatsuki", 1, 60, false, qiyo.Key); got.Total != 1 || got.Items[0].Key != qiyo.Key {
		t.Errorf("点名的那位必须在结果里（别名搜不到也算命中）：%+v", got)
	}
	// 点名的是一位男优时同样不受「不列男优」的闸影响 —— 用户点的是他本人。
	if got := s.ListJavWallActors(context.Background(), "unknown-alias", 1, 60, false, byName["デカ吉"].Key); got.Total != 1 || got.Items[0].Name != "デカ吉" {
		t.Errorf("男优被点名时也要列出来：%+v", got)
	}

	// 详情：这位演员参演的**墙上影片**，带齐打开那一面墙详情抽屉的三件套。
	detail, err := s.JavWallActorDetail(context.Background(), qiyo.Key)
	if err != nil {
		t.Fatalf("JavWallActorDetail: %v", err)
	}
	if detail.Total != 3 || len(detail.Items) != 3 {
		t.Fatalf("彩月七緒 应当有 3 部墙上的片，got %d", detail.Total)
	}
	seen := map[string]bool{}
	for _, mv := range detail.Items {
		if mv.SourceName == "" || mv.Stem == "" {
			t.Errorf("详情里的影片要能定位：%+v", mv)
		}
		if mv.LocalDirID == "" {
			t.Errorf("本地目录源必须带 local_dir_id（否则前端打不开那一面墙）：%+v", mv)
		}
		seen[mv.LocalDirID] = true
	}
	if len(seen) != 2 {
		t.Errorf("两部片来自 A、一部来自 B，应当出现两个数据源：%v", seen)
	}

	// 名字当 key 也要能查（前端可能只拿得到名字那一种写法）。
	if _, err := s.JavWallActorDetail(context.Background(), "name:新人 花子"); err != nil {
		t.Errorf("按名字查不到：%v", err)
	}
	if _, err := s.JavWallActorDetail(context.Background(), ""); err == nil {
		t.Error("空 key 应当报校验错误")
	}
	if _, err := s.JavWallActorDetail(context.Background(), "nobody-here"); err == nil {
		t.Error("查不到的人应当报 NOT_FOUND")
	}
}

// 关键词过滤要**认别名**：主名被换成了索引表的中文统一名之后，用户往往只记得
// 侧车里那个原名（日文 / 罗马音），按原名搜必须也能搜到这一位。
func TestJavActorMatchesKeywordAliases(t *testing.T) {
	card := JavWallActorCard{
		Key:     "ACT-1",
		ID:      "ACT-1",
		Name:    "彩月七緒",
		Aliases: []string{"Nana Saatsuki", "さあつきなな"},
	}
	cases := []struct {
		kw   string
		want bool
	}{
		{"彩月", true},    // 主名
		{"nana", true},  // 别名（大小写不敏感）
		{"さあつき", true},  // 别名（日文片段）
		{"act-1", true}, // 上游 id 小写
		{"デカ", false},   // 谁都不是
	}
	for _, c := range cases {
		if got := javActorMatchesKeyword(card, c.kw); got != c.want {
			t.Errorf("javActorMatchesKeyword(%q) = %v，期望 %v", c.kw, got, c.want)
		}
	}

	// 详情按**别名**也要能查到人（前端手上那份可能只有原名）。
	if !javActorHasAlias(card, "nana saatsuki") {
		t.Error("别名精确匹配失败")
	}
	if javActorHasAlias(card, "nana") {
		t.Error("javActorHasAlias 是精确匹配，不该对片段返回真")
	}
}

// 隐藏档（默认是「未匹配」）里的片不是墙上的片，它的演员也不该进演员墙。
func TestJavWallActorsSkipHiddenDirs(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("有码/NIMA-086/NIMA-086.strm", "url")
	write("有码/NIMA-086/NIMA-086.nfo", `<movie><num>NIMA-086</num><actor><name>可见 演员</name></actor></movie>`)
	write("未匹配/XXXX-001/XXXX-001.strm", "url")
	write("未匹配/XXXX-001/XXXX-001.nfo", `<movie><num>XXXX-001</num><actor><name>藏起来 的</name></actor></movie>`)

	settingSvc := newWallLocalDirsSettings(t, map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{
			{ID: "aaaa1111", Name: "影库", Root: root, MediaKind: settings.WallLocalKindJav},
		}),
		// 显式藏掉「未匹配」：设过就不走兜底。
		settings.KeyStrmJavWallHiddenDirs: `["未匹配"]`,
	})
	s := newJavActorWallTestService(t, settingSvc)
	list := s.ListJavWallActors(context.Background(), "", 1, 60, false, "")
	if list.Total != 1 || list.Items[0].Name != "可见 演员" {
		t.Fatalf("隐藏档的演员不该进榜：%+v", list.Items)
	}
	if list.Movies != 1 {
		t.Errorf("墙上的影片数也不该算隐藏档，got %d", list.Movies)
	}
}
