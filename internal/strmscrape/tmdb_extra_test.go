package strmscrape

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组测试钉住 tmdb_extra.go 里那几处**实测撞到的形状差异**。
//
// 它们不是「跑一遍看看有没有 panic」，而是每条都对应一个真机上会静默出错的地方：
// 形状认错了不报错，只是字段变空 —— 那种错在界面上看起来只是「信息不全」。

const tvAppendJSON = `{
  "id": 295599, "name": "御廷谣", "status": "Ended",
  "vote_average": 8.2, "vote_count": 6,
  "backdrop_path": "/qKGSZaLLbfCaoTbE88oOwdAsTm8.jpg",
  "tagline": "",
  "genres": [{"id":18,"name":"剧情"}],
  "production_companies": [{"name":"Company A"}],
  "production_countries": [{"iso_3166_1":"CN","name":"China"}],
  "networks": [{"name":"Hunan Television"}],
  "content_ratings": {"results":[{"iso_639_1":"KR","rating":"15"}]},
  "aggregate_credits": {
    "cast": [
      {"id":2091760,"name":"吴谨言","profile_path":"/a.jpg","order":0,
       "total_episode_count":32,"roles":[{"character":"Meng Tinghui","episode_count":32}]},
      {"id":2439184,"name":"陈哲远","profile_path":"/b.jpg","order":1,
       "total_episode_count":32,"roles":[{"character":"Ying Gua","episode_count":32}]}
    ],
    "crew": [{"id":1,"name":"某导演","jobs":[{"job":"Director","episode_count":32}]}]
  },
  "images": {"backdrops":[
    {"file_path":"/bd0.jpg","width":1920,"height":1080,"iso_639_1":null,"vote_average":5.0},
    {"file_path":"/bd1.jpg","width":1920,"height":1080,"iso_639_1":null,"vote_average":7.0},
    {"file_path":"/tall.jpg","width":1080,"height":1920,"iso_639_1":null,"vote_average":9.9}
  ]},
  "videos": {"results":[
    {"key":"AAA","site":"YouTube","type":"Teaser","name":"片花","official":false},
    {"key":"BBB","site":"YouTube","type":"Trailer","name":"预告","official":true}
  ]},
  "keywords": {"results":[{"name":"based on novel"}]},
  "external_ids": {"imdb_id":"tt37937929","tvdb_id":467405}
}`

func TestParseTMDBExtraTVShape(t *testing.T) {
	got := parseTMDBExtra(json.RawMessage(tvAppendJSON), MediaTypeTV, "zh-CN")

	if got.VoteAverage != 8.2 || got.VoteCount != 6 {
		t.Fatalf("评分 = %v/%d，期望 8.2/6", got.VoteAverage, got.VoteCount)
	}
	if got.Status != "Ended" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Genres) != 1 || got.Genres[0] != "剧情" {
		t.Fatalf("genres = %v", got.Genres)
	}
	if len(got.Networks) != 1 || got.Networks[0] != "Hunan Television" {
		t.Fatalf("networks = %v", got.Networks)
	}
	// 剧集的分级走 content_ratings（电影走 release_dates）。本国 CN 没有分级 → 回落 US，
	// US 也没有 → 空。**不能**拿 KR 那条填上去（那是给韩国看的）。
	if got.Certification != "" {
		t.Fatalf("certification = %q，期望空（只有 KR 有分级，不该拿它填）", got.Certification)
	}
	if got.IMDBID != "tt37937929" || got.TVDBID != "467405" {
		t.Fatalf("external ids = %q/%q", got.IMDBID, got.TVDBID)
	}
	if len(got.Keywords) != 1 || got.Keywords[0] != "based on novel" {
		t.Fatalf("keywords = %v（剧集侧的键是 results）", got.Keywords)
	}
	// 官方 Trailer 要压过 Teaser。
	if got.TrailerURL != "https://www.youtube.com/watch?v=BBB" {
		t.Fatalf("trailer = %q", got.TrailerURL)
	}
}

// TestParseTMDBExtraTVAggregateCast 钉住「剧集用 aggregate_credits」。
//
// 两套形状不同：aggregate 的 cast **没有 character**，角色名在 `roles[].character` 里。
// 认错形状的表现是「演员有、角色名全是空」—— 不报错。
func TestParseTMDBExtraTVAggregateCast(t *testing.T) {
	got := parseTMDBExtra(json.RawMessage(tvAppendJSON), MediaTypeTV, "zh-CN")
	if len(got.Cast) != 2 {
		t.Fatalf("演员 %d 位，期望 2", len(got.Cast))
	}
	if got.Cast[0].Name != "吴谨言" || got.Cast[0].Character != "Meng Tinghui" {
		t.Fatalf("第一位 = %+v（角色名要从 roles[0].character 取）", got.Cast[0])
	}
	if got.Cast[0].ID != 2091760 {
		t.Fatalf("演员 id = %d（下载头像的判据）", got.Cast[0].ID)
	}
	if len(got.Directors) != 1 || got.Directors[0] != "某导演" {
		t.Fatalf("导演 = %v（剧集的 job 藏在 jobs[] 里）", got.Directors)
	}
}

// TestParseTMDBExtraStillsFilter 钉住选图的两条口径：**排除背景图那张**、**排除竖图**。
func TestParseTMDBExtraStillsFilter(t *testing.T) {
	got := parseTMDBExtra(json.RawMessage(tvAppendJSON), MediaTypeTV, "zh-CN")
	if got.BackdropPath != "/qKGSZaLLbfCaoTbE88oOwdAsTm8.jpg" {
		t.Fatalf("背景图 = %q（取顶层的 backdrop_path，不是 images 里排序后的第一张）", got.BackdropPath)
	}
	// 三张里：bd0 / bd1 是 16:9，tall.jpg 是 9:16（必须被排除）。
	if len(got.Stills) != 2 {
		t.Fatalf("剧照 %d 张，期望 2（竖图那张要排除）：%v", len(got.Stills), got.Stills)
	}
	// 按 vote_average 降序：bd1(7.0) 在 bd0(5.0) 前面。
	if got.Stills[0] != "/bd1.jpg" || got.Stills[1] != "/bd0.jpg" {
		t.Fatalf("剧照顺序 = %v，期望按评分降序", got.Stills)
	}
}

const movieAppendJSON = `{
  "id": 1288445, "title": "怒之杀", "original_title": "Mutiny",
  "runtime": 96, "vote_average": 7.4, "vote_count": 806,
  "backdrop_path": "/e2QAGr.jpg", "tagline": "血染水域",
  "genres": [{"id":28,"name":"动作"},{"id":53,"name":"惊悚"}],
  "belongs_to_collection": {"id":1,"name":"某合集"},
  "release_dates": {"results":[
    {"iso_3166_1":"US","release_dates":[{"certification":"R","type":3}]},
    {"iso_3166_1":"CN","release_dates":[{"certification":"","type":3}]}
  ]},
  "credits": {
    "cast": [
      {"id":976,"name":"杰森·斯坦森","character":"Cole Reed","profile_path":"/s.jpg","order":0}
    ],
    "crew": [
      {"id":58324,"name":"让-弗朗索瓦·里歇","job":"Director","department":"Directing"},
      {"id":9,"name":"Lindsay Michel","job":"Writer","department":"Writing"}
    ]
  },
  "images": {"backdrops":[
    {"file_path":"/e2QAGr.jpg","width":3840,"height":2160,"vote_average":8.0},
    {"file_path":"/still.jpg","width":1920,"height":1080,"vote_average":6.0}
  ]},
  "keywords": {"keywords":[{"name":"revenge"}]},
  "external_ids": {"imdb_id":"tt32338669"}
}`

func TestParseTMDBExtraMovieShape(t *testing.T) {
	got := parseTMDBExtra(json.RawMessage(movieAppendJSON), MediaTypeMovie, "zh-CN")

	if got.Runtime != 96 {
		t.Fatalf("时长 = %d（电影在顶层 runtime）", got.Runtime)
	}
	if got.Collection != "某合集" {
		t.Fatalf("合集 = %q", got.Collection)
	}
	// 语言是 zh-CN → 国家 CN，而 CN 那条分级是空串 → 回落 US 的 R。
	if got.Certification != "R" {
		t.Fatalf("分级 = %q，期望回落到 US 的 R", got.Certification)
	}
	if len(got.Cast) != 1 || got.Cast[0].Character != "Cole Reed" {
		t.Fatalf("演员 = %+v（电影用 credits，character 直接是字符串）", got.Cast)
	}
	if len(got.Directors) != 1 || len(got.Writers) != 1 {
		t.Fatalf("导演/编剧 = %v / %v（电影的 job 在顶层）", got.Directors, got.Writers)
	}
	if len(got.Keywords) != 1 || got.Keywords[0] != "revenge" {
		t.Fatalf("keywords = %v（电影侧的键是 keywords）", got.Keywords)
	}
	// 图库里两张：`/e2QAGr.jpg` 就是顶层那张背景图（要排除），只剩 `/still.jpg` 当剧照。
	if len(got.Stills) != 1 || got.Stills[0] != "/still.jpg" {
		t.Fatalf("剧照 = %v，期望只剩 /still.jpg（背景图那张要排除）", got.Stills)
	}
}

// TestParseTMDBExtraIgnoresTopLevelResults 钉住「只读自己那份子对象」。
//
// append_to_response 会把子资源平铺到顶层，而 `release_dates` 里也有一个 `results`
// 键。若把整个 payload 当一个 map 去取 `results`，拿到的是哪一个取决于解析顺序 ——
// 那种错不报错，只是分级忽有忽无。
func TestParseTMDBExtraIgnoresTopLevelResults(t *testing.T) {
	payload := `{
      "vote_average": 1.0,
      "release_dates": {"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13","type":3}]}]},
      "results": [{"name":"这一个是别的东西"}]
    }`
	got := parseTMDBExtra(json.RawMessage(payload), MediaTypeMovie, "en-US")
	if got.Certification != "PG-13" {
		t.Fatalf("分级 = %q，期望 PG-13（从 release_dates 子对象里取）", got.Certification)
	}
}

func TestCountryFromLanguage(t *testing.T) {
	cases := map[string]string{
		"zh-CN": "CN",
		"zh-TW": "TW",
		"en-US": "US",
		"en":    "",
		"":      "",
		"zh-cn": "CN",
		"pt_BR": "BR",
	}
	for in, want := range cases {
		if got := countryFromLanguage(in); got != want {
			t.Errorf("countryFromLanguage(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestPickBackdropsPrefersHigherVote(t *testing.T) {
	imgs := []tmdbImageEntry{
		{FilePath: "/a.jpg", Width: 1920, Height: 1080, VoteAverage: 3},
		{FilePath: "/b.jpg", Width: 1920, Height: 1080, VoteAverage: 9},
		{FilePath: "/skip.jpg", Width: 1000, Height: 1000}, // 1:1，要排除
	}
	got := pickBackdrops(imgs, "")
	if len(got) != 2 || got[0] != "/b.jpg" {
		t.Fatalf("pickBackdrops = %v，期望按评分降序且排除 1:1", got)
	}
	// skip 参数要把背景图那张剔掉。
	got = pickBackdrops(imgs, "/b.jpg")
	if len(got) != 1 || got[0] != "/a.jpg" {
		t.Fatalf("pickBackdrops(skip) = %v", got)
	}
}

// TestBuildNFOCommonMergesKeywordsIntoGenres 钉住类型与关键词合并去重、且类型在前。
func TestBuildNFOCommonMergesKeywordsIntoGenres(t *testing.T) {
	common := buildNFOCommon(nfoInput{
		Title:    "怒之杀",
		Genres:   []string{"动作", "惊悚"},
		Keywords: []string{"复仇", "动作"},
	})
	want := []string{"动作", "惊悚", "复仇"}
	if len(common.Genres) != len(want) {
		t.Fatalf("genres = %v, 期望 %v", common.Genres, want)
	}
	for i := range want {
		if common.Genres[i] != want[i] {
			t.Fatalf("genres = %v, 期望 %v（类型在前、关键词在后、去重）", common.Genres, want)
		}
	}
}

// TestBuildNFOCommonOmitsEmptyImageNames 钉住「图没下下来时整个元素不写」。
//
// 写一个指向不存在文件的 `<thumb>`，Emby 会显示破图，比没有更糟。
func TestBuildNFOCommonOmitsEmptyImageNames(t *testing.T) {
	common := buildNFOCommon(nfoInput{Title: "x"})
	if common.Thumb != "" || common.Fanart != "" {
		t.Fatalf("thumb/fanart = %q/%q，期望空串", common.Thumb, common.Fanart)
	}
	common = buildNFOCommon(nfoInput{Title: "x", ThumbName: "poster.jpg", FanartName: "fanart.jpg"})
	if common.Thumb != "poster.jpg" || common.Fanart != "fanart.jpg" {
		t.Fatalf("thumb/fanart = %q/%q", common.Thumb, common.Fanart)
	}
}

// TestTMDBShowStatusMapping 钉住「不认识的按 Continuing」。
func TestTMDBShowStatusMapping(t *testing.T) {
	cases := map[string]string{
		"Ended":            "Ended",
		"Canceled":         "Ended",
		"Returning Series": "Continuing",
		"In Production":    "Continuing",
		"":                 "Continuing",
	}
	for in, want := range cases {
		if got := tmdbShowStatus(in); got != want {
			t.Errorf("tmdbShowStatus(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestRootOfWorkGroup 钉住「从 workGroup 反推任务根」。
//
// workGroup 只存 relKey / absDir / flatFile（没有存根），而演员头像目录要写成
// `<根>/media/actors`。算错的表现是头像目录建到别的地方、nfo 里的相对路径全断。
func TestRootOfWorkGroup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "strm_out")
	movie := filepath.Join(root, "电影", "怒之杀 (2026) {tmdb-1288445}")
	got := rootOf(workGroup{relKey: "电影/怒之杀 (2026) {tmdb-1288445}", absDir: movie})
	if got != root {
		t.Fatalf("rootOf(电影) = %q, 期望 %q", got, root)
	}
	// 平铺：relKey 是单个 .strm 的相对路径，absDir 就是根。
	flat := filepath.Join(root, "a.strm")
	got = rootOf(workGroup{relKey: "a.strm", absDir: root, flatFile: flat})
	if got != root {
		t.Fatalf("rootOf(平铺) = %q, 期望 %q", got, root)
	}
}

// TestActorThumbNameIsRelativeToNFODir 钉住 nfo 里 `<actor><thumb>` 的写法。
//
// Emby / Kodi 按 **nfo 所在目录**解析相对路径，所以头像在任务根的 media/actors 下时
// 必须写成 `../../media/actors/976.jpg` 这种。写成绝对路径或相对根写，Emby 都找不到。
func TestActorThumbNameIsRelativeToNFODir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "strm_out")
	show := filepath.Join(root, "电视剧", "某剧 (2026) {tmdb-1}")
	nfo := filepath.Join(show, "tvshow.nfo")
	actors := filepath.Join(root, "media", "actors")
	got := actorThumbName(nfo, actors, 976)
	want := "../../media/actors/976.jpg"
	if filepath.ToSlash(got) != want {
		t.Fatalf("actorThumbName = %q, 期望 %q", got, want)
	}
	if strings.Contains(got, ":") {
		t.Fatalf("不该出现盘符（绝对路径）：%q", got)
	}
}
