package strmscrape

import (
	"encoding/json"
	"testing"
)

// TMDB 档案是**补充资料**，所以「对上人」这件事必须严：名字只要不是逐字相等，
// 宁可没有档案，也不能把同名另一个人的生平贴到这位演员头上。
func TestPickJavActorPersonExactOnly(t *testing.T) {
	results := []json.RawMessage{
		json.RawMessage(`{"id":1,"name":"Nana Saatsuki","original_name":"さあつきなな"}`),
		json.RawMessage(`{"id":2,"name":"AIKA","original_name":"AIKA","also_known_as":["優木あいか"]}`),
		json.RawMessage(`{"id":3,"name":"Other Person"}`),
	}

	cases := []struct {
		name string
		want int
	}{
		// 主名逐字命中。
		{"AIKA", 2},
		// 别名命中（TMDB 的 also_known_as）。
		{"優木あいか", 2},
		// 大小写不敏感（TMDB 那边往往全大写）。
		{"aika", 2},
		// original_name 命中。
		{"さあつきなな", 1},
		// **片段不算**：这是这条测试的重点，子串匹配会把同姓的人全捞进来。
		{"Nana", 0},
		{"Other", 0},
		// 根本没有这个人。
		{"查无此人", 0},
	}
	for _, c := range cases {
		if got := pickJavActorPerson(results, []string{c.name}); got != c.want {
			t.Errorf("pickJavActorPerson(%q) = %d，期望 %d", c.name, got, c.want)
		}
	}

	// 候选名有多个时，任意一个命中即可（统一名 + 别名的查法）。
	if got := pickJavActorPerson(results, []string{"查无此人", "優木あいか"}); got != 2 {
		t.Errorf("多候选时应当命中第二位的别名，got %d", got)
	}
	// 空候选 / 空结果不该 panic，也不该挑出人来。
	if got := pickJavActorPerson(results, nil); got != 0 {
		t.Errorf("没有候选名时应当返回 0，got %d", got)
	}
	if got := pickJavActorPerson(nil, []string{"AIKA"}); got != 0 {
		t.Errorf("空结果应当返回 0，got %d", got)
	}
}

// parseJavActorPerson：只要有 id 与名字就算拿到（有些人的简介是空的）。
func TestParseJavActorPerson(t *testing.T) {
	raw := json.RawMessage(`{"id":42,"name":"AIKA","profile_path":"/x.jpg",
"birthday":"1990-01-01","place_of_birth":"Tokyo","biography":"简介",
"also_known_as":["AIKA","優木あいか","優木あいか"]}`)
	got := parseJavActorPerson(raw)
	if got == nil {
		t.Fatal("应当解析出档案")
	}
	if got.ID != 42 || got.ProfilePath != "/x.jpg" || got.Birthday != "1990-01-01" {
		t.Errorf("字段没解全：%+v", got)
	}
	// 别名去重、去掉与主名相同的那一条。
	if len(got.AlsoKnownAs) != 1 || got.AlsoKnownAs[0] != "優木あいか" {
		t.Errorf("别名去重不对：%v", got.AlsoKnownAs)
	}

	if parseJavActorPerson(json.RawMessage(`{"id":0,"name":"x"}`)) != nil {
		t.Error("没有 id 的不算")
	}
	if parseJavActorPerson(json.RawMessage(`{"id":1,"name":"  "}`)) != nil {
		t.Error("没有名字的不算")
	}
	if parseJavActorPerson(json.RawMessage(`not json`)) != nil {
		t.Error("坏正文不算")
	}
}

// 人像尺寸只放行 TMDB 认识的那几档 —— 原样透传等于让调用方拉 `original`。
func TestNormalizeActorPhotoSize(t *testing.T) {
	cases := map[string]string{
		"":         "w185",
		"w45":      "w45",
		"w185":     "w185",
		"h632":     "h632",
		"w300":     "w300",
		"w500":     "w300",
		"original": "w185",
		"w1000":    "w185",
	}
	for in, want := range cases {
		if got := normalizeActorPhotoSize(in); got != want {
			t.Errorf("normalizeActorPhotoSize(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// applyActorUnifiedName：主名换成索引表的中文统一名，原名落进别名。
func TestApplyActorUnifiedName(t *testing.T) {
	card := JavWallActorCard{Name: "優木あいか"}
	applyActorUnifiedName(&card)
	if card.Name != "AIKA" {
		t.Errorf("主名应当是索引表的统一名 AIKA，got %q", card.Name)
	}
	found := false
	for _, a := range card.Aliases {
		if a == "優木あいか" {
			found = true
		}
	}
	if !found {
		t.Errorf("原来的写法要留在别名里：%v", card.Aliases)
	}

	// 表里没有的演员：原样不动（不能把名字弄空、也不能塞一堆别名）。
	unknown := JavWallActorCard{Name: "完全不存在的演员XYZ"}
	applyActorUnifiedName(&unknown)
	if unknown.Name != "完全不存在的演员XYZ" || len(unknown.Aliases) != 0 {
		t.Errorf("表里没有时应当原样不动：%+v", unknown)
	}

	// nil 安全。
	applyActorUnifiedName(nil)
}
