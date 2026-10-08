package strmscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/mediaorganize/tmdb"
)

func TestGroupWorks_TVSeasonsCollapse(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "三体 (2023)")
	s1 := filepath.Join(show, "Season 01")
	s2 := filepath.Join(show, "Season 02")
	mustMkdir(t, s1)
	mustMkdir(t, s2)
	mustWrite(t, filepath.Join(s1, "E01.strm"), "x")
	mustWrite(t, filepath.Join(s1, "E02.strm"), "x")
	mustWrite(t, filepath.Join(s2, "E01.strm"), "x")

	entries, err := scanStrmFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	works := groupWorks(root, entries)
	if len(works) != 1 {
		t.Fatalf("want 1 work, got %d", len(works))
	}
	if len(works[0].entries) != 3 {
		t.Fatalf("want 3 files in work, got %d", len(works[0].entries))
	}
	if inferMediaType(works[0]) != MediaTypeTV {
		t.Fatalf("want tv, got %s", inferMediaType(works[0]))
	}
	item := buildItem(1, root, works[0])
	if item.FileCount != 3 {
		t.Fatalf("file_count=%d", item.FileCount)
	}
	nfo, poster := workMetaPaths(works[0], MediaTypeTV)
	if filepath.Base(nfo) != "tvshow.nfo" || filepath.Base(poster) != "poster.jpg" {
		t.Fatalf("meta paths = %s / %s", nfo, poster)
	}
	if seasonPosterPath(show, 1) != filepath.Join(show, "season01-poster.jpg") {
		t.Fatalf("season poster path wrong")
	}
}

func TestMovieMetaPathsMatchStrmStem(t *testing.T) {
	root := t.TempDir()
	movie := filepath.Join(root, "哪吒之魔童闹海 (2025)")
	mustMkdir(t, movie)
	strm := filepath.Join(movie, "Ne.Zha.2.2025.strm")
	mustWrite(t, strm, "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	nfo, poster := workMetaPaths(works[0], MediaTypeMovie)
	if nfo != strings.TrimSuffix(strm, ".strm")+".nfo" {
		t.Fatalf("nfo=%s", nfo)
	}
	if poster != filepath.Join(movie, "poster.jpg") {
		t.Fatalf("poster=%s", poster)
	}
}

func TestInferMediaType_MovieFolderIgnoresAudioFalseEpisode(t *testing.T) {
	root := t.TempDir()
	movie := filepath.Join(root, "哪吒之魔童闹海 (2025)")
	mustMkdir(t, movie)
	mustWrite(t, filepath.Join(movie, "Ne.Zha.2.2025.2160p.WEB-DL.DDP2.0.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 {
		t.Fatalf("works=%d", len(works))
	}
	if got := inferMediaType(works[0]); got != MediaTypeMovie {
		t.Fatalf("want movie, got %s", got)
	}
}

func TestGroupWorks_MovieFolders(t *testing.T) {
	root := t.TempDir()
	m1 := filepath.Join(root, "Inception (2010)")
	m2 := filepath.Join(root, "Interstellar (2014)")
	mustMkdir(t, m1)
	mustMkdir(t, m2)
	mustWrite(t, filepath.Join(m1, "Inception.strm"), "x")
	mustWrite(t, filepath.Join(m2, "Interstellar.strm"), "x")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("want 2 works, got %d", len(works))
	}
	for _, g := range works {
		if inferMediaType(g) != MediaTypeMovie {
			t.Fatalf("%s: want movie", g.relKey)
		}
		if len(g.entries) != 1 {
			t.Fatalf("%s: want 1 file", g.relKey)
		}
	}
}

func TestSpecialPrefixedMovieStaysInOwnDirectory(t *testing.T) {
	root := t.TempDir()
	wantTitle := "特别篇 吹响吧！上低音号～合奏比赛～"
	name := "特别篇 吹响吧！上低音号～合奏比赛～ (2023){tmdb-1108306}"
	movie := filepath.Join(root, "电影", name)
	mustMkdir(t, movie)
	mustWrite(t, filepath.Join(movie, "Hibike Euphonium Ensemble Contest.strm"), "x")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 {
		t.Fatalf("works=%d", len(works))
	}
	if works[0].absDir != movie {
		t.Fatalf("work dir=%q want %q", works[0].absDir, movie)
	}
	item := buildItem(1, root, works[0])
	if item.MediaType != MediaTypeMovie || item.TMDBID != "1108306" || item.Title != wantTitle {
		t.Fatalf("item=%+v", item)
	}
	nfo, _ := workMetaPaths(works[0], MediaTypeMovie)
	if filepath.Dir(nfo) != movie || filepath.Base(nfo) == "tvshow.nfo" {
		t.Fatalf("movie nfo=%q", nfo)
	}
}

func TestPureSpecialsDirectoryStillCollapsesIntoTVShow(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "剧集", "测试剧 (2023)")
	specials := filepath.Join(show, "特别篇")
	mustMkdir(t, specials)
	mustWrite(t, filepath.Join(specials, "S00E01.strm"), "x")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || works[0].absDir != show {
		t.Fatalf("works=%+v", works)
	}
	if got := inferMediaType(works[0]); got != MediaTypeTV {
		t.Fatalf("media type=%q", got)
	}
}

func TestGroupWorks_FlatFilesStaySeparate(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "A.strm"), "x")
	mustWrite(t, filepath.Join(root, "B.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("want 2 flat works, got %d", len(works))
	}
}

func TestPathToItemIDStable(t *testing.T) {
	a := pathToItemID("Movies/Inception (2010)")
	b := pathToItemID(filepath.FromSlash("Movies/Inception (2010)"))
	if a == "" || a != b {
		t.Fatalf("item id unstable: %q vs %q", a, b)
	}
}

func TestNormalizeWriteMode(t *testing.T) {
	if normalizeWriteMode("") != WriteModeMissingOnly {
		t.Fatal("empty should be missing_only")
	}
	if normalizeWriteMode("overwrite") != WriteModeOverwrite {
		t.Fatal("overwrite expected")
	}
}

func TestParseStrmSeasonEpisode(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "三体 (2023)")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	p := filepath.Join(s1, "三体.S01E02.strm")
	mustWrite(t, p, "x")
	sn, en := parseStrmSeasonEpisode(p)
	if sn == nil || *sn != 1 {
		t.Fatalf("season=%v", sn)
	}
	if en == nil || *en != 2 {
		t.Fatalf("episode=%v", en)
	}
}

func TestWriteSeasonAndEpisodeNFO(t *testing.T) {
	root := t.TempDir()
	seasonNFO := filepath.Join(root, "season.nfo")
	if err := writeSeasonNFO(seasonNFO, 1, "第一季", "简介", "2023-01-01"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(seasonNFO)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "<season>") || !strings.Contains(text, "<seasonnumber>1</seasonnumber>") {
		t.Fatalf("season nfo unexpected: %s", text)
	}
	epNFO := filepath.Join(root, "Show.S01E01.nfo")
	if err := writeEpisodeNFOFull(epNFO, episodeNFOInput{
		Title:     "开端",
		ShowTitle: "三体",
		Plot:      "本集简介",
		Aired:     "2023-01-15",
		TMDBID:    "123",
		Season:    1,
		Episode:   1,
		Runtime:   45,
		Score:     7.5,
		ScoreMax:  tmdbScoreMax,
		Votes:     12,
		Directors: []string{"某导演"},
		ThumbName: "Show.S01E01-thumb.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(epNFO)
	if err != nil {
		t.Fatal(err)
	}
	text = string(body)
	if !strings.Contains(text, "<episodedetails>") || !strings.Contains(text, "<episode>1</episode>") {
		t.Fatalf("episode nfo unexpected: %s", text)
	}
	if !strings.Contains(text, "<showtitle>三体</showtitle>") {
		t.Fatalf("missing showtitle: %s", text)
	}
	// 完整版多出来的那几项（都在**已有的那次**季详情请求里，不额外花请求）。
	for _, want := range []string{
		"<runtime>45</runtime>",
		`<rating name="tmdb" max="10" default="true">`,
		"<votes>12</votes>",
		"<director>某导演</director>",
		"<thumb>Show.S01E01-thumb.jpg</thumb>",
		`<uniqueid type="tmdb" default="true">123</uniqueid>`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("episode nfo 缺少 %s：\n%s", want, text)
		}
	}
}

// TestWriteFullMovieNFO 钉住电影那份完整 nfo 的元素。
//
// 与番号那面（internal/jav/emby）**刻意是两套**：那边有 `<num>` / `<maker>` /
// `<label>` / 5 分制换算那些只属于番号的东西。这里只放标准 Emby / Kodi 的 `<movie>`。
func TestWriteFullMovieNFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "movie.nfo")
	year := 2026
	if err := writeFullMovieNFO(path, nfoInput{
		MediaType:   MediaTypeMovie,
		Title:       "怒之杀",
		Original:    "Mutiny",
		Plot:        "简介",
		Tagline:     "血染水域",
		Year:        &year,
		ReleaseDate: "2026-08-19",
		Runtime:     96,
		Score:       7.4,
		ScoreMax:    tmdbScoreMax,
		Votes:       806,
		Genres:      []string{"动作"},
		Keywords:    []string{"复仇"},
		Countries:   []string{"United Kingdom"},
		Studios:     []string{"MadRiver Pictures"},
		MPAA:        "R",
		Directors:   []string{"让-弗朗索瓦·里歇"},
		Writers:     []string{"Lindsay Michel"},
		Actors: []nfoActor{
			{Name: "杰森·斯坦森", Role: "Cole Reed", Thumb: "../../media/actors/976.jpg"},
		},
		TMDBID:     "1288445",
		IMDBID:     "tt32338669",
		Collection: "某合集",
		TrailerURL: "https://www.youtube.com/watch?v=AAA",
		ThumbName:  "poster.jpg",
		FanartName: "fanart.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "<movie>") {
		t.Fatalf("根元素不是 movie：%s", text)
	}
	for _, want := range []string{
		"<title>怒之杀</title>",
		"<originaltitle>Mutiny</originaltitle>",
		"<sorttitle>怒之杀</sorttitle>",
		"<tagline>血染水域</tagline>",
		"<runtime>96</runtime>",
		"<premiered>2026-08-19</premiered>",
		"<year>2026</year>",
		"<mpaa>R</mpaa>",
		"<director>让-弗朗索瓦·里歇</director>",
		"<credits>Lindsay Michel</credits>",
		`<role>Cole Reed</role>`,
		"<thumb>../../media/actors/976.jpg</thumb>",
		"<thumb>poster.jpg</thumb>",
		"<fanart>fanart.jpg</fanart>",
		`<uniqueid type="imdb">tt32338669</uniqueid>`,
		"<set>",
		"<name>某合集</name>",
		"<trailer>https://www.youtube.com/watch?v=AAA</trailer>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("movie nfo 缺少 %s：\n%s", want, text)
		}
	}
	// 类型与关键词都进 <genre>（关键词在后）。
	if !strings.Contains(text, "<genre>动作</genre>") || !strings.Contains(text, "<genre>复仇</genre>") {
		t.Fatalf("genre 应含类型与关键词：\n%s", text)
	}
}

// TestWriteFullTVShowNFO 钉住剧集那份多的三个元素（status / network）。
func TestWriteFullTVShowNFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tvshow.nfo")
	if err := writeFullTVShowNFO(path, nfoInput{
		MediaType: MediaTypeTV,
		Title:     "御廷谣",
		Status:    "Ended",
		Networks:  []string{"Hunan Television"},
		TMDBID:    "295599",
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "<tvshow>") {
		t.Fatalf("根元素不是 tvshow：%s", text)
	}
	if !strings.Contains(text, "<status>Ended</status>") {
		t.Fatalf("缺 status：%s", text)
	}
	if !strings.Contains(text, "<network>Hunan Television</network>") {
		t.Fatalf("缺 network：%s", text)
	}
}

// TestFullNFOReadableBySimpleParser 钉住「完整 nfo 能被详情抽屉的读侧读回来」。
//
// 这是写侧与读侧之间那份契约：改了元素名而没改读侧，表现是详情抽屉里那几块**静默变空**。
func TestFullNFOReadableBySimpleParser(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "电影", "怒之杀 (2026) {tmdb-1288445}")
	mustMkdir(t, show)
	mustWrite(t, filepath.Join(show, "怒之杀.strm"), "x")
	path := filepath.Join(show, "怒之杀.nfo")
	year := 2026
	if err := writeFullMovieNFO(path, nfoInput{
		MediaType: MediaTypeMovie,
		Title:     "怒之杀",
		Original:  "Mutiny",
		Plot:      "简介",
		Year:      &year,
		Runtime:   96,
		Score:     7.4,
		ScoreMax:  tmdbScoreMax,
		Votes:     806,
		Genres:    []string{"动作"},
		Directors: []string{"某导演"},
		Actors:    []nfoActor{{Name: "杰森·斯坦森", Role: "Cole Reed"}},
		TMDBID:    "1288445",
	}); err != nil {
		t.Fatal(err)
	}
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 {
		t.Fatalf("扫出 %d 个作品", len(works))
	}
	got := readTMDBWallNFO(works[0], MediaTypeMovie)
	if got == nil {
		t.Fatal("读不到完整 nfo")
	}
	if got.Title != "怒之杀" || got.Runtime != 96 || got.Premiered != "" {
		t.Fatalf("读回来 = %+v", got)
	}
	if got.Ratings == nil || got.Ratings.Rating.Value != 7.4 || got.Ratings.Rating.Max != tmdbScoreMax {
		t.Fatalf("评分读回来 = %+v（TMDB 是 10 分制，别按 5 分制读）", got.Ratings)
	}
	if len(got.Actors) != 1 || got.Actors[0].Role != "Cole Reed" {
		t.Fatalf("演员读回来 = %+v", got.Actors)
	}
	// 关键：**完整 nfo 也必须能被简版读侧读出标题与 tmdbid** ——
	// 索引、状态判定（workHasNFO / readWorkNFOMeta）走的都是那一条。
	if meta, ok := readWorkNFOMeta(works[0], MediaTypeMovie); !ok || meta.TMDBID != "1288445" {
		t.Fatalf("简版读侧读不出 tmdbid：%+v ok=%v", meta, ok)
	}
}

func TestWriteMatchedPropagatesTVExtrasError(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "三体 (2023)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "三体.S01E01.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{}
	client := tmdb.NewClient(tmdb.Options{}) // 空 API Key 会让季详情请求稳定失败
	_, err = svc.writeMatchedOpts(context.Background(), client, works[0], tmdbInfo{
		TMDBID:       "123",
		Title:        "三体",
		MediaType:    MediaTypeTV,
		EpisodeCount: 1,
	}, true, true, false)
	if err == nil || !strings.Contains(err.Error(), "补写季/集元数据失败") {
		t.Fatalf("季集补写失败必须向上返回，实际 err=%v", err)
	}
}

func TestStatusMissWhenOnlyNFO(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "现在就出发 (2023)")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	mustWrite(t, filepath.Join(s1, "E01.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>现在就出发</title><tmdbid>1</tmdbid></tvshow>\n")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusMiss {
		t.Fatalf("status=%s want miss (nfo only)", item.Status)
	}
}

func TestStatusDoubtWithPending(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "天龙八部")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	ep := filepath.Join(s1, "S01E01.strm")
	mustWrite(t, ep, "x")
	mustWrite(t, strings.TrimSuffix(ep, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>天龙八部</title><tmdbid>1</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingState(works[0], scrapeState{Status: PendingDoubt, EpLocal: 1, EpTMDB: 40})
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusDoubt {
		t.Fatalf("status=%s want doubt", item.Status)
	}
	if item.EpLocal != 1 || item.EpTMDB != 40 {
		t.Fatalf("ep local=%d tmdb=%d want from pending", item.EpLocal, item.EpTMDB)
	}
	clearPendingMarker(works[0])
	item = buildItem(1, root, works[0])
	if item.Status != ItemStatusOK {
		t.Fatalf("after clear pending status=%s want ok", item.Status)
	}
}

func TestInferMediaType_FlatTVWithoutSeasonDir(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "现在就出发 (2023)")
	mustMkdir(t, show)
	mustWrite(t, filepath.Join(show, "S01E01.strm"), "x")
	mustWrite(t, filepath.Join(show, "S01E02.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := inferMediaType(works[0]); got != MediaTypeTV {
		t.Fatalf("want tv for flat episodes, got %s", got)
	}
}

func TestInferMediaType_SingleExplicitEpisodeInYearFolder(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "三体 (2023)")
	mustMkdir(t, show)
	mustWrite(t, filepath.Join(show, "三体.S01E01.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := inferMediaType(works[0]); got != MediaTypeTV {
		t.Fatalf("want tv for one explicit episode, got %s", got)
	}
}

func TestRootReadySkipsEvenIfEpisodeIncomplete(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "海贼王")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	e1 := filepath.Join(s1, "S01E01.strm")
	e2 := filepath.Join(s1, "S01E02.strm")
	mustWrite(t, e1, "x")
	mustWrite(t, e2, "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>海贼王</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	mustWrite(t, strings.TrimSuffix(e1, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusOK {
		t.Fatalf("status=%s want ok", item.Status)
	}
	if item.TVState != TVStateEnded {
		t.Fatalf("tv_state=%s want ended", item.TVState)
	}
	if workNeedsScrape(works[0], MediaTypeTV) {
		t.Fatal("no pending + root ready should skip")
	}
}

func TestPendingForcesScrapeAndUpdatingState(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "罚罪 (2022)")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	mustWrite(t, filepath.Join(s1, "S01E01.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>罚罪</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingState(works[0], scrapeState{Status: PendingUpdating, EpLocal: 1, EpTMDB: 40})
	if !workNeedsScrape(works[0], MediaTypeTV) {
		t.Fatal("pending must scrape")
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusOK {
		t.Fatalf("status=%s want ok (追更且根齐备)", item.Status)
	}
	if !item.HasPending {
		t.Fatal("updating should keep pending marker")
	}
	if item.TVState != TVStateUpdating {
		t.Fatalf("tv_state=%s", item.TVState)
	}
	if item.EpTMDB != 40 || item.EpLocal != 1 {
		t.Fatalf("ep local=%d tmdb=%d", item.EpLocal, item.EpTMDB)
	}
}

func TestMarkNormalClearsPending(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "短剧")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	mustWrite(t, filepath.Join(s1, "S01E01.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>短剧</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingState(works[0], scrapeState{Status: PendingIncomplete, EpLocal: 1, EpTMDB: 1})
	if err := markWorkNormal(works[0], MediaTypeTV); err != nil {
		t.Fatal(err)
	}
	if hasPendingMarker(works[0]) {
		t.Fatal("pending should be cleared")
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusOK || item.TVState != TVStateEnded {
		t.Fatalf("status=%s tv_state=%s", item.Status, item.TVState)
	}
	if workNeedsScrape(works[0], MediaTypeTV) {
		t.Fatal("after mark normal should skip")
	}
}

func TestManualCompleteSkipsUnmatchedWork(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "自制短剧")
	mustMkdir(t, show)
	mustWrite(t, filepath.Join(show, "S01E01.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := writeManualComplete(g, MediaTypeTV); err != nil {
		t.Fatal(err)
	}
	if workNeedsScrape(g, MediaTypeTV) {
		t.Fatal("手动完成的未匹配作品不应再次进入自动刮削")
	}
	item := buildItem(1, root, g)
	if item.Status != ItemStatusOK || !item.ManualDone || item.TVState != TVStateEnded || item.TMDBID != "" {
		t.Fatalf("手动完成状态错误：%+v", item)
	}
}

func TestClearScrapedMetadataByType(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "错误匹配")
	mustMkdir(t, show)
	strmPath := filepath.Join(show, "S01E01.strm")
	ownedNFO := filepath.Join(show, "tvshow.nfo")
	ownedPoster := filepath.Join(show, "poster.jpg")
	userPoster := filepath.Join(show, "fanart.jpg")
	subtitle := filepath.Join(show, "S01E01.srt")
	for path, body := range map[string]string{
		strmPath:    "x",
		ownedNFO:    "nfo",
		ownedPoster: "poster",
		userPoster:  "user",
		subtitle:    "sub",
	} {
		mustWrite(t, path, body)
	}
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := clearScrapedMetadata(g); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ownedNFO, ownedPoster, userPoster} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("元数据未清理：%s", path)
		}
	}
	for _, path := range []string{strmPath, subtitle} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("非元数据不应被清理：%s: %v", path, err)
		}
	}
}

func TestFinalizeKeepsPendingWhenLocalExceedsTMDB(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "短剧")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	for i := 1; i <= 3; i++ {
		p := filepath.Join(s1, fmt.Sprintf("S01E%02d.strm", i))
		mustWrite(t, p, "x")
		if i == 1 {
			mustWrite(t, strings.TrimSuffix(p, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
		}
	}
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>短剧</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingMarker(works[0])
	finalizeAfterScrape(works[0], MediaTypeTV, 1, false) // TMDB 只 1 集，本地 3 集
	if !hasPendingMarker(works[0]) {
		t.Fatal("short drama should keep pending (miss)")
	}
	st, _ := readPendingState(works[0])
	if st.Status != PendingIncomplete {
		t.Fatalf("status=%s want incomplete", st.Status)
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusMiss {
		t.Fatalf("item status=%s want miss", item.Status)
	}
	if item.TVState == TVStateUpdating {
		t.Fatal("short drama should not be labeled updating")
	}
}

func TestFinalizeKeepsPendingWhenUpdating(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "更新中")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	e1 := filepath.Join(s1, "S01E01.strm")
	mustWrite(t, e1, "x")
	mustWrite(t, strings.TrimSuffix(e1, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>x</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingMarker(works[0])
	finalizeAfterScrape(works[0], MediaTypeTV, 40, false)
	if !hasPendingMarker(works[0]) {
		t.Fatal("updating should keep pending")
	}
	st, ok := readPendingState(works[0])
	if !ok || st.Status != PendingUpdating || st.EpTMDB != 40 {
		t.Fatalf("pending state=%+v ok=%v", st, ok)
	}
}

func TestFinalizeKeepsPendingWhenDoubt(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "天龙八部")
	s1 := filepath.Join(show, "Season 01")
	mustMkdir(t, s1)
	e1 := filepath.Join(s1, "S01E01.strm")
	mustWrite(t, e1, "x")
	mustWrite(t, strings.TrimSuffix(e1, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>x</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = writePendingMarker(works[0])
	finalizeAfterScrape(works[0], MediaTypeTV, 1, true)
	st, ok := readPendingState(works[0])
	if !ok || st.Status != PendingDoubt || st.EpLocal != 1 || st.EpTMDB != 1 {
		t.Fatalf("pending state=%+v ok=%v", st, ok)
	}
	item := buildItem(1, root, works[0])
	if item.Status != ItemStatusDoubt {
		t.Fatalf("status=%s want doubt", item.Status)
	}
}

func TestSeasonDirConflictSkippedInProgress(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "罚罪 (2022)")
	s1 := filepath.Join(show, "Season 1")
	s2 := filepath.Join(show, "Season 2")
	mustMkdir(t, s1)
	mustMkdir(t, s2)
	mustWrite(t, filepath.Join(s1, "罚罪.S01E29.strm"), "x")
	mustWrite(t, filepath.Join(s2, "逍遥.S01E29.strm"), "x")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>罚罪</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	total, scraped := countTVEpisodeProgress(works[0])
	if total != 1 {
		t.Fatalf("conflict file should be skipped, total=%d", total)
	}
	if scraped != 0 {
		t.Fatalf("scraped=%d", scraped)
	}
}

func TestCountTVEpisodeProgressSkipsSeasonZeroAndSpecials(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "海贼王 (1999)")
	s0 := filepath.Join(show, "Season 00")
	s1 := filepath.Join(show, "Season 01")
	sp := filepath.Join(show, "Specials")
	mustMkdir(t, s0)
	mustMkdir(t, s1)
	mustMkdir(t, sp)
	mustWrite(t, filepath.Join(s0, "S00E01.strm"), "x")
	mustWrite(t, filepath.Join(s0, "S00E02.strm"), "x")
	mustWrite(t, filepath.Join(sp, "特别篇01.strm"), "x")
	e1 := filepath.Join(s1, "S01E01.strm")
	e2 := filepath.Join(s1, "S01E02.strm")
	mustWrite(t, e1, "x")
	mustWrite(t, e2, "x")
	mustWrite(t, strings.TrimSuffix(e1, ".strm")+".nfo", "<episodedetails><title>1</title></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>航海王</title></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "img")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	total, scraped := countTVEpisodeProgress(works[0])
	if total != 2 {
		t.Fatalf("want 2 main-season eps, total=%d", total)
	}
	if scraped != 1 {
		t.Fatalf("scraped=%d want 1", scraped)
	}
}

func TestListLocalRegularSeasonNumbersOnlyLocal(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "亲爱的·客栈 (2017)")
	s4 := filepath.Join(show, "Season 04")
	sp := filepath.Join(show, "Specials")
	mustMkdir(t, s4)
	mustMkdir(t, sp)
	mustWrite(t, filepath.Join(s4, "S04E01.strm"), "x")
	mustWrite(t, filepath.Join(sp, "S00E01.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	got := listLocalRegularSeasonNumbers(works[0])
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("seasons=%v want [4]", got)
	}
}

func TestSumTMDBSeasonEpisodeCountsLocalSeasonsOnly(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"season_number":0,"episode_count":3}`),
		json.RawMessage(`{"season_number":1,"episode_count":12}`),
		json.RawMessage(`{"season_number":2,"episode_count":12}`),
		json.RawMessage(`{"season_number":3,"episode_count":13}`),
		json.RawMessage(`{"season_number":4,"episode_count":12}`),
	}
	if got := sumTMDBSeasonEpisodeCounts(raw, []int{4}); got != 12 {
		t.Fatalf("got %d want 12", got)
	}
	if got := sumTMDBSeasonEpisodeCounts(raw, []int{1, 4}); got != 24 {
		t.Fatalf("got %d want 24", got)
	}
}

func TestEffectiveSeasonEpisodeCountUsesFinale(t *testing.T) {
	eps := make([]tmdbEpisodeDetail, 0, 46)
	for i := 1; i <= 46; i++ {
		ep := tmdbEpisodeDetail{EpisodeNumber: i, EpisodeType: "standard"}
		if i == 25 {
			ep.EpisodeType = "finale"
		}
		eps = append(eps, ep)
	}
	detail := &tmdbSeasonDetail{Episodes: eps}
	if got := effectiveSeasonEpisodeCount(detail, 46); got != 25 {
		t.Fatalf("got %d want 25 (finale truncates shells)", got)
	}
}

func TestEffectiveSeasonEpisodeCountFinaleWithAbsoluteEpisodeNumbers(t *testing.T) {
	// 海贼王等：季内集号延续上季，finale 集号是绝对号，不能当成本季集数去累加
	eps := make([]tmdbEpisodeDetail, 0, 52)
	for i := 1000; i <= 1050; i++ {
		ep := tmdbEpisodeDetail{EpisodeNumber: i, EpisodeType: "standard"}
		if i == 1050 {
			ep.EpisodeType = "finale"
		}
		eps = append(eps, ep)
	}
	for i := 1051; i <= 1060; i++ {
		eps = append(eps, tmdbEpisodeDetail{EpisodeNumber: i, EpisodeType: "standard"})
	}
	detail := &tmdbSeasonDetail{Episodes: eps}
	if got := effectiveSeasonEpisodeCount(detail, 61); got != 51 {
		t.Fatalf("got %d want 51 (count eps <= finale, not finale number %d)", got, 1050)
	}
}

func TestEffectiveSeasonEpisodeCountKeepsFallbackWithoutFinale(t *testing.T) {
	detail := &tmdbSeasonDetail{
		Episodes: []tmdbEpisodeDetail{
			{EpisodeNumber: 1, EpisodeType: "standard"},
			{EpisodeNumber: 12, EpisodeType: "mid_season"},
			{EpisodeNumber: 13, EpisodeType: "standard"},
		},
	}
	// 无 finale：不砍预占空壳，沿用 episode_count
	if got := effectiveSeasonEpisodeCount(detail, 24); got != 24 {
		t.Fatalf("got %d want 24", got)
	}
	if got := finaleEpisodeNumber(detail); got != 0 {
		t.Fatalf("mid_season must not count as finale, got %d", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBackfillNeeded 钉住「哪部作品要补」。
//
// 判据错的表现很讨厌：要么**每次点补齐都全量重跑**（把「已有 nfo」当成缺），
// 要么**点了一点反应都没有**（把缺的当成齐了）。
func TestBackfillNeeded(t *testing.T) {
	newWork := func(t *testing.T) (string, workGroup) {
		t.Helper()
		root := t.TempDir()
		movie := filepath.Join(root, "电影", "怒之杀 (2026) {tmdb-1288445}")
		mustMkdir(t, movie)
		mustWrite(t, filepath.Join(movie, "怒之杀.strm"), "x")
		works, err := scanWorks(root)
		if err != nil || len(works) != 1 {
			t.Fatalf("scanWorks: %v, %d", err, len(works))
		}
		return root, works[0]
	}

	// ① 没有 nfo —— 那是「还没刮过」，走正常刮削，补齐不抢它的活。
	_, g := newWork(t)
	if backfillNeeded(g, MediaTypeMovie) {
		t.Fatal("没有 nfo 的不该被补齐挑中（那是正常刮削的活）")
	}

	// ② 老版简版 nfo（没有 <actor>）—— 要补。
	root, g := newWork(t)
	movie := filepath.Join(root, "电影", "怒之杀 (2026) {tmdb-1288445}")
	mustWrite(t, filepath.Join(movie, "怒之杀.nfo"),
		"<movie><title>怒之杀</title><year>2026</year><tmdbid>1288445</tmdbid><plot>x</plot></movie>")
	if !backfillNeeded(g, MediaTypeMovie) {
		t.Fatal("简版 nfo（没有 actor）该被挑中")
	}

	// ③ 完整 nfo + 剧照 + 背景图 —— 齐了，不该再跑。
	year := 2026
	if err := writeFullMovieNFO(filepath.Join(movie, "怒之杀.nfo"), nfoInput{
		MediaType: MediaTypeMovie,
		Title:     "怒之杀",
		Year:      &year,
		Actors:    []nfoActor{{Name: "杰森·斯坦森"}},
		TMDBID:    "1288445",
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(movie, "fanart.jpg"), "img")
	mustMkdir(t, filepath.Join(movie, "extrafanart"))
	mustWrite(t, filepath.Join(movie, "extrafanart", "fanart1.jpg"), "img")
	if backfillNeeded(g, MediaTypeMovie) {
		t.Fatal("齐了的作品不该再被挑中（否则每次点补齐都全量重跑）")
	}

	// ④ 完整 nfo 但缺背景图 —— 要补。
	if err := os.Remove(filepath.Join(movie, "fanart.jpg")); err != nil {
		t.Fatal(err)
	}
	if !backfillNeeded(g, MediaTypeMovie) {
		t.Fatal("缺背景图该被挑中")
	}
}

// TestMergeNFOInputKeepsExisting 钉住补抓的**合并语义**：已有的一律不动，只补缺的。
//
// 这是补抓最要紧的一条：用户可能手改过标题/简介/类型，整份重写会把那些改动悄悄抹掉，
// 而 Emby 那边只表现为「信息变了」。
func TestMergeNFOInputKeepsExisting(t *testing.T) {
	old := &tmdbNFO{
		Title:     "我自己改的标题",
		Plot:      "我自己改的简介",
		Runtime:   120,
		Genres:    []string{"我删剩的类型"},
		Directors: []string{"我改的导演"},
		Actors: []struct {
			Name  string `xml:"name"`
			Role  string `xml:"role"`
			Thumb string `xml:"thumb"`
		}{{Name: "我留的演员", Role: "某角色"}},
		Ratings: &struct {
			Rating struct {
				Max   int     `xml:"max,attr"`
				Value float64 `xml:"value"`
				Votes int     `xml:"votes"`
			} `xml:"rating"`
		}{},
	}
	old.Ratings.Rating.Value = 9.9
	old.Ratings.Rating.Max = 10
	old.Ratings.Rating.Votes = 5

	in := nfoInput{
		Title:     "TMDB 的标题",
		Original:  "Mutiny",
		Plot:      "TMDB 的简介",
		Runtime:   96,
		Score:     7.4,
		ScoreMax:  tmdbScoreMax,
		Votes:     806,
		Genres:    []string{"动作"},
		Keywords:  []string{"复仇"},
		Directors: []string{"TMDB 的导演"},
		Actors:    []nfoActor{{Name: "TMDB 的演员"}},
	}

	got := mergeNFOInput(old, in)

	if got.Title != "我自己改的标题" || got.Plot != "我自己改的简介" {
		t.Fatalf("已有的标题/简介被覆盖了：%q / %q", got.Title, got.Plot)
	}
	if got.Runtime != 120 {
		t.Fatalf("已有片长被覆盖：%d", got.Runtime)
	}
	if len(got.Genres) != 1 || got.Genres[0] != "我删剩的类型" {
		t.Fatalf("已有类型没整列保留：%v", got.Genres)
	}
	if len(got.Keywords) != 0 {
		t.Fatalf("类型整列保留时不该再并进关键词（用户删掉的词会回来）：%v", got.Keywords)
	}
	if len(got.Actors) != 1 || got.Actors[0].Name != "我留的演员" {
		t.Fatalf("已有演员没保留：%v", got.Actors)
	}
	if got.Score != 9.9 || got.Votes != 5 {
		t.Fatalf("已有评分没保留：%v/%d", got.Score, got.Votes)
	}
	// 老那份**没有**的字段要补上（否则补抓就白跑了）。
	if got.Original != "Mutiny" {
		t.Fatalf("原始标题该从新数据补上：%q", got.Original)
	}
	if len(got.Directors) != 1 || got.Directors[0] != "我改的导演" {
		t.Fatalf("导演该保留老那份：%v", got.Directors)
	}
}
