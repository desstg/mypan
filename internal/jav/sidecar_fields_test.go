package jav

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
)

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
	}
	_, fields, _ := sidecarFieldsFor(movie, []*domain.JavActor{{ID: "a1", Name: "演员"}})

	// 「库里能提供的」那张清单。加字段时**两处一起改**，改漏了这条会红。
	want := []string{
		"summary", "review", "release_date", "preview_video_url",
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
