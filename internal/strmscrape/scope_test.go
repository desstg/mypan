package strmscrape

import "testing"

func TestNormalizeScopeDirsRemovesDuplicatesAndCoveredChildren(t *testing.T) {
	got := normalizeScopeDirs([]string{" 电影/临时 ", "电影", "电影/临时", "../非法", "综艺"})
	want := []string{"电影", "综艺"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestFilterWorksByScope(t *testing.T) {
	works := []workGroup{
		{relKey: "电影/阿凡达 (2009)"},
		{relKey: "电视剧/藏锋 (2026)"},
		{relKey: "临时测试/片段"},
	}
	got := filterWorksByScope(works, []string{"临时测试", "电影/阿凡达 (2009)"})
	if len(got) != 1 || got[0].relKey != "电视剧/藏锋 (2026)" {
		t.Fatalf("unexpected works: %#v", got)
	}
}
