package jav

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
)

// fakeMovieRepo 只实现 `GetByNumber`，其余方法一律 panic —— 它只服务
// `SidecarTitleByNumber` 这一条路（那一条只查一次库）。
//
// 接口有三十来个方法，为一条测试把整个假库写出来不值当；**嵌入接口**让其余方法
// 自动「被满足」（调用即 nil panic），而这条路上它们永远不会被调到。
type fakeMovieRepo struct {
	domain.JavMovieRepository

	byNumber map[string]*domain.JavMovie
	calls    int
}

func (f *fakeMovieRepo) GetByNumber(_ context.Context, number string) (*domain.JavMovie, error) {
	f.calls++
	m, ok := f.byNumber[number]
	if !ok {
		return nil, nil // 库里没有这一部：生产那侧是 err != nil || m == nil
	}
	return m, nil
}

// TestSidecarTitleByNumber 钉住「新建侧车那份标题的取值判据」。
//
// 这是四那条缺口（新建侧车里连 `title` 键都没有 → nfo 的 `<title>` /
// `<originaltitle>` 永远空着）的取值端。判据必须与写侧 `buildSidecar`
// **同一条**（`pickTitleZH`）：中文标题非空就用中文，否则回落原标题；
// `origin_title` 恒为日文原名，不受中文影响。
func TestSidecarTitleByNumber(t *testing.T) {
	repo := &fakeMovieRepo{byNumber: map[string]*domain.JavMovie{
		"IPZZ-909": {Number: "IPZZ-909", Title: "Original", TitleZH: "中文标题", OriginTitle: "日本語タイトル"},
		"SSIS-001": {Number: "SSIS-001", Title: "Only Original", OriginTitle: "原名"},
	}}
	svc := &Service{movies: repo}

	// ① 中文优先：title 取中文，origin_title 仍是日文原名。
	title, origin := svc.SidecarTitleByNumber(context.Background(), "IPZZ-909")
	if title != "中文标题" || origin != "日本語タイトル" {
		t.Errorf("title=%q origin=%q，期望 中文标题 / 日本語タイトル", title, origin)
	}

	// ② 没有中文时回落原标题（`pickTitleZH` 那条判据）。
	title, origin = svc.SidecarTitleByNumber(context.Background(), "SSIS-001")
	if title != "Only Original" || origin != "原名" {
		t.Errorf("title=%q origin=%q，期望 Only Original / 原名", title, origin)
	}

	// ③ 番号空 / 查不到 → 两个空串，调用方据此**不写这两个键**（写空串会让读取方
	// 以为「这份侧车说了标题是空」，而事实只是「不知道」）。
	if got, got2 := svc.SidecarTitleByNumber(context.Background(), ""); got != "" || got2 != "" {
		t.Errorf("空番号该返回两个空串，got %q / %q", got, got2)
	}
	before := repo.calls
	if got, got2 := svc.SidecarTitleByNumber(context.Background(), "NOT-IN-LIB"); got != "" || got2 != "" {
		t.Errorf("库里没有的番号该返回两个空串，got %q / %q", got, got2)
	}
	if repo.calls != before+1 {
		t.Errorf("空番号那次不该查库：calls %d → %d", before, repo.calls)
	}

	// ④ 没装配库时**不 panic**（这个函数会在扫盘那条路上被无条件调到）。
	bare := &Service{}
	if got, got2 := bare.SidecarTitleByNumber(context.Background(), "IPZZ-909"); got != "" || got2 != "" {
		t.Errorf("未装配库该返回两个空串，got %q / %q", got, got2)
	}
}

// TestSidecarFieldPathsExistInWriteSchema 是回写那条路的**契约闸门**。
//
// sidecarFieldsFor 用的是**字符串键**（`director.name`、`images.cover`…），
// 而写侧（sidecar.go 的 sidecarDoc）用的是结构体 tag。两边一分家就是**静默**失败：
// 回写会把一个谁都不认的键写进侧车 json（多一个垃圾字段、该补的没补上），
// 而界面上只表现为「还是空的」。
//
// 所以这里拿写侧**真的序列化一遍**出来的 JSON 当判据，逐键核对：
// 每一个键都必须在那份 JSON 里**已经存在**（作为叶子或作为一层）。
//
// 反过来不查（写侧有、这里没有）—— 那是**有意**的：resource / quality / dest
// 三块描述的是「这一颗资源」与「落盘现场」，库里没有对应数据，见文件头的说明。
func TestSidecarFieldPathsExistInWriteSchema(t *testing.T) {
	movie := &domain.JavMovie{
		ID: "x", Number: "SSIS-001", Title: "t",
		Summary: "简介", Review: "简评", ReleaseDate: "2024-01-01",
		PreviewVideoURL: "https://example.com/p.mp4",
		DirectorID:      "d1", DirectorName: "导演",
		MakerID: "m1", MakerName: "片商",
		PublisherID: "p1", PublisherName: "发行",
		SeriesID: "s1", SeriesName: "系列",
		CoverURL:      "https://example.com/c.jpg",
		ThumbURL:      "https://example.com/t.jpg",
		JavbusCover:   "https://example.com/j.jpg",
		PreviewImages: []string{"https://example.com/1.jpg"},
		Duration:      120, Score: 4.5, ReviewsCount: 12,
		Tags: []string{"标签"},
	}
	actors := []*domain.JavActor{{ID: "a1", Name: "演员", Gender: 0, AvatarURL: "https://example.com/a.jpg"}}

	number, fields, ok := sidecarFieldsFor(movie, actors)
	if !ok || number != "SSIS-001" {
		t.Fatalf("打包失败：number=%q ok=%v", number, ok)
	}
	if len(fields) == 0 {
		t.Fatal("一个字段都没打包出来")
	}

	// 写侧真的写一份出来（灌满值，否则 omitempty 会把字段省掉）
	raw, _, err := buildSidecar(sidecarInput{
		Movie:    movie,
		Actors:   actors,
		SiteBase: "https://javdb.com",
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("buildSidecar: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("侧车不是合法 JSON：%v", err)
	}

	for key := range fields {
		if !jsonPathExists(doc, key) {
			t.Errorf("回写键 %q 在写侧的侧车 JSON 里不存在 —— "+
				"写进去就是一个谁都不认的垃圾字段，而该补的没补上", key)
		}
	}
}

// jsonPathExists 报告 `a.b.c` 这条路径在 doc 里**存在**（中间层是对象即可，
// 叶子是 nil 也算存在 —— 那正是「空」的形态，我们就是要补它）。
func jsonPathExists(doc map[string]any, path string) bool {
	var cur any = doc
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		next, ok := obj[seg]
		if !ok {
			return false
		}
		cur = next
	}
	return true
}

// TestSidecarFieldsForOmitsEmpty 空值不进包。
//
// 判据与回写那边（只补空）是**同一条**：推一个空值过去，回写那边也会因为
// isEmptyJSONValue 而跳过 —— 白白多一次序列化，还让「这次推了什么」的日志失真。
func TestSidecarFieldsForOmitsEmpty(t *testing.T) {
	_, fields, ok := sidecarFieldsFor(&domain.JavMovie{ID: "x", Number: "SSIS-001"}, nil)
	if !ok {
		t.Fatal("有番号就该 ok")
	}
	if len(fields) != 0 {
		t.Errorf("什么元数据都没有时不该打包出字段，got %v", fields)
	}

	// 没有番号 → 无从回写（侧车是按番号找的）
	if _, _, ok := sidecarFieldsFor(&domain.JavMovie{ID: "x"}, nil); ok {
		t.Error("没有番号时应当 ok=false")
	}
	if _, _, ok := sidecarFieldsFor(nil, nil); ok {
		t.Error("nil 影片应当 ok=false")
	}
}

// TestSidecarFieldsForKeepsGenderAndScoreMax 两个**容易漏、漏了会静默出错**的字段。
//
//   - gender：nfo 的 <set>（演员合集）要按性别把男优剔掉。少推它，补出来的合集里
//     就会混进男优 —— 那正是迁移 0050 修过的真机 bug。
//   - score_max：<rating> 是 10 分制而 JAVDB 是 5 分制，不写分母的读取方只能猜，
//     猜错一次整库评分都偏。
func TestSidecarFieldsForKeepsGenderAndScoreMax(t *testing.T) {
	movie := &domain.JavMovie{ID: "x", Number: "SSIS-001", Score: 4.5}
	actors := []*domain.JavActor{{ID: "a1", Name: "女优", Gender: 0}, {ID: "a2", Name: "男优", Gender: 1}}

	_, fields, ok := sidecarFieldsFor(movie, actors)
	if !ok {
		t.Fatal("ok=false")
	}
	if got, _ := fields["score_max"].(int); got != javdbScoreMax {
		t.Errorf("score_max = %v，期望 %d", fields["score_max"], javdbScoreMax)
	}
	list, _ := fields["actors"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("演员应当有两个，got %v", fields["actors"])
	}
	for _, a := range list {
		if _, has := a["gender"]; !has {
			t.Errorf("演员 %v 少了 gender —— <set> 会因此把男优也写进去", a)
		}
	}
	if g, _ := list[1]["gender"].(int); g != 1 {
		t.Errorf("男优的 gender 应当是 1，got %v", list[1]["gender"])
	}
}

// TestSidecarFieldsCoverWriteSchemaLeaves 反向核对：**该补的字段一个都不能漏在表外**。
//
// 与 TestSidecarFieldPathsExistInWriteSchema 是一对：那条查「表里的键写侧认不认」，
// 这条查「写侧这些字段，表里有没有」。少一项就是「那栏永远补不上」，
// 而它同样静默 —— 用户看到的只是「nfo 里还是没有演员」。
//
// 只覆盖**库里能提供**的那几个（见文件头：resource / quality / dest 三块不在内）。
func TestSidecarFieldsCoverWriteSchemaLeaves(t *testing.T) {
	movie := &domain.JavMovie{
		ID: "x", Number: "SSIS-001", Title: "t",
		Summary: "简介", Review: "简评", ReleaseDate: "2024-01-01",
		PreviewVideoURL: "https://example.com/p.mp4",
		DirectorID:      "d1", DirectorName: "导演",
		MakerID: "m1", MakerName: "片商",
		PublisherID: "p1", PublisherName: "发行",
		SeriesID: "s1", SeriesName: "系列",
		CoverURL:      "https://example.com/c.jpg",
		ThumbURL:      "https://example.com/t.jpg",
		JavbusCover:   "https://example.com/j.jpg",
		PreviewImages: []string{"https://example.com/1.jpg"},
		Duration:      120, Score: 4.5, ReviewsCount: 12,
		Tags: []string{"标签"},
		// `type` 必须给：它决定海报怎么裁（有码取右边），不是纯展示字段。
		Type: domain.JavTypeCensored,
	}
	_, fields, _ := sidecarFieldsFor(movie, []*domain.JavActor{{ID: "a1", Name: "演员"}})

	// 「库里能提供的」那张清单。加字段时**两处一起改**，改漏了这条会红。
	want := []string{
		"summary", "review", "release_date", "preview_video_url",
		"type", "type_label",
		"director.id", "director.name", "maker.id", "maker.name",
		"publisher.id", "publisher.name", "series.id", "series.name",
		"images.cover", "images.thumb", "images.javbus_cover", "images.previews",
		"duration", "score", "score_max", "reviews_count", "tags", "actors",
	}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("字段 %q 没进包 —— 那一栏在侧车/nfo 里就永远补不上", key)
		}
	}
	// 反面：不该推的别推（它们描述的是「这一颗资源」，库里没有）
	for _, key := range []string{"resource", "quality", "dest", "javdb_url"} {
		for k := range fields {
			if k == key || strings.HasPrefix(k, key+".") {
				t.Errorf("%q 不该进包：它描述的是推送那一刻的事实，库里没有", k)
			}
		}
	}
}

// TestLookupTitleZHForSidecar 钉住「点名写一份侧车」那条路的**取值判据**。
//
// 它存在的理由（2026-10-17 真机）：10 部片里有 7 部**上游早就给了中文标题**
// （库里 `title_zh` 是 `missav` / `freejavbt` 那几家补来的），而当时的实现只走
// 「强制翻译」那一步 —— 于是那 7 部被重翻了一遍，还盖掉了上游那份更贴切的译名。
// 用户的原话是「厍里太多要翻译，ai的token消耗太多」，所以这一步的 KPI 是
// **零 AI 请求**。
//
// 判据里有两条容易写错的对称情形（下面 ② 与 ③），它们长得很像但结论相反：
//
//   - `title_zh` 与 `title` **不同** → 库里那份是别人给的，**但还得看它像不像中文**
//     （上游的译名栏里混日文是实测存在的，`dmm` 那档）；
//   - `title_zh` 与 `title` **相同** → 那栏只是原文的副本，**不能再拿「像不像中文」
//     当判据**（一部日文原文本就可能是汉字堆，`nukitei` 系）。这时只看来源标记。
func TestLookupTitleZHForSidecar(t *testing.T) {
	const jp = "超高級中出し専門ソープ 七瀬アリス"
	repo := &fakeMovieRepo{byNumber: map[string]*domain.JavMovie{
		// ① 上游给的中文（**要用的那一档**）
		"ABF-387": {Number: "ABF-387", Title: jp, TitleZH: "超高级中出专门泡泡浴 七濑爱丽丝", TitleZHSource: "missav"},
		// ② 译文栏是日文（`dmm` 那档塞进来的）→ 不能要
		"DMM-001": {Number: "DMM-001", Title: jp, TitleZH: "超高級中出し専門ソープ 七瀬アリス", TitleZHSource: "dmm"},
		// ③ 两栏同值、来源是别站给的中文标题 → 要（只能靠来源判）
		"MAD016": {Number: "MAD016", Title: "某个全汉字标题", TitleZH: "某个全汉字标题", TitleZHSource: "missav"},
		// ④ 两栏同值、来源是原文站 → 不要（宁可留着侧车那份，等翻译那条路）
		"MAD017": {Number: "MAD017", Title: "某个全汉字标题", TitleZH: "某个全汉字标题", TitleZHSource: "dmm"},
		// ⑤ 译文栏空 → 没有可用的
		"EMPTY-1": {Number: "EMPTY-1", Title: "タイトル"},
	}}
	svc := &Service{movies: repo}
	ctx := context.Background()

	for _, tc := range []struct {
		number string
		want   string
		ok     bool
		why    string
	}{
		{"ABF-387", "超高级中出专门泡泡浴 七濑爱丽丝", true, "上游给的中文"},
		{"DMM-001", "", false, "译文栏里是日文，不许拿它盖掉侧车那份"},
		{"MAD016", "某个全汉字标题", true, "两栏同值但来源是别站的中文标题"},
		{"MAD017", "", false, "两栏同值且来源是原文站"},
		{"EMPTY-1", "", false, "译文栏是空的"},
		{"NOT-IN-LIB", "", false, "库里没有这一部"},
		{"", "", false, "番号是空的"},
	} {
		got, ok := svc.LookupTitleZHForSidecar(ctx, tc.number)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s：got (%q, %v)，期望 (%q, %v) —— %s", tc.number, got, ok, tc.want, tc.ok, tc.why)
		}
	}

	// 没装配库时不 panic（这条路会被刮削那三条分支无条件调到）。
	if got, ok := (&Service{}).LookupTitleZHForSidecar(ctx, "ABF-387"); ok || got != "" {
		t.Errorf("未装配库该返回空，got (%q, %v)", got, ok)
	}
}

// TestRunSidecarSyncNowFixesTitles 钉住**整批那趟也要修标题**这条（2026-10-17 补的缺口）。
//
// # 它防的是什么
//
// `SyncSidecarsFromRepo` 走的是 `writeFieldsIntoFile`，而那个函数的字段清单
// （`sidecarFieldsFor`）里**刻意没有 `title`** —— 标题是「无条件替换」通道，
// 判据与「只补空」不同。代价是：**已经存在的侧车**里那份 `title` / nfo `<title>`
// 没有任何一条路会去修正，除非那一部恰好再被补缺链推一次。
//
// 真机后果（2026-10-17 全量审计，300 份侧车）：49 份 json 标题至今是日文，
// 其中 **28 份库里 `title_zh` 已经是中文** —— 译文在库里躺了很久，侧车从没收到过。
// 这条用例就是钉「只靠每小时那趟也能把它推下去」。
func TestRunSidecarSyncNowFixesTitles(t *testing.T) {
	// ① 库里有中文标题（`sidecarTitleZH` 判据过）→ 该被推下去。
	zhMovie := &domain.JavMovie{Number: "SNOS-186", Title: "日本語のタイトル", TitleZH: "女儿的朋友"}
	// ② 库里那栏还是日文（上游站点塞的）→ 判据不过，**不该**推下去
	//    （推了等于拿日文盖掉侧车里已有的中文）。
	jpMovie := &domain.JavMovie{Number: "ABF-387", Title: "毒づきウェイトレス", TitleZH: "毒づきウェイトレス 八掛うみ"}
	// ③ 库里没有这一部 → 不推、也不 panic。
	svc := &Service{
		movies: &fakeMovieRepo{byNumber: map[string]*domain.JavMovie{
			"SNOS-186": zhMovie,
			"ABF-387":  jpMovie,
		}},
	}
	svc.SetSidecarSyncSink(func(_ context.Context, _ func(string) (map[string]any, bool)) (SidecarSyncResult, error) {
		return SidecarSyncResult{
			Scanned: 3,
			Written: 0,
			// 顺序刻意乱、且有重复：遍历那条路是按目录走的，调用方不该假设有序。
			Numbers: []string{"ABF-387", "SNOS-186", "NOT-IN-LIB", "SNOS-186"},
		}, nil
	})
	var pushed []string
	svc.SetTitleZHSidecarSink(func(_ string, titleZH string) (bool, error) {
		pushed = append(pushed, titleZH)
		return true, nil // 就当每次都真改了
	})

	res, err := svc.RunSidecarSyncNow(context.Background())
	if err != nil {
		t.Fatalf("RunSidecarSyncNow: %v", err)
	}
	if len(pushed) != 1 || pushed[0] != "女儿的朋友" {
		t.Fatalf("推下去的该只有库里那份中文标题，实际 %q", pushed)
	}
	if res.TitleFixed != 1 {
		t.Errorf("TitleFixed=%d，期望 1（含重复的那份也只该写一次）", res.TitleFixed)
	}
	// Written 与 TitleFixed 是**两本账**：这一趟字段一个都没补，标题修了一份。
	if res.Written != 0 {
		t.Errorf("Written=%d，期望 0（字段那本账不该被标题记进来）", res.Written)
	}

	// ④ 没注入标题通道（比如没接 strm 模块）→ 不 panic，TitleFixed 仍是 0。
	bare := &Service{
		movies: &fakeMovieRepo{byNumber: map[string]*domain.JavMovie{"SNOS-186": zhMovie}},
	}
	bare.SetSidecarSyncSink(func(_ context.Context, _ func(string) (map[string]any, bool)) (SidecarSyncResult, error) {
		return SidecarSyncResult{Scanned: 1, Numbers: []string{"SNOS-186"}}, nil
	})
	res2, err := bare.RunSidecarSyncNow(context.Background())
	if err != nil {
		t.Fatalf("没注入标题通道时不该报错：%v", err)
	}
	if res2.TitleFixed != 0 {
		t.Errorf("没注入标题通道时 TitleFixed 该是 0，got %d", res2.TitleFixed)
	}
}
