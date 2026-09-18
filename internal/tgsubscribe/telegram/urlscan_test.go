package telegram

import (
	"reflect"
	"testing"
)

// cutURL 的入参总是从 "http" 开始的一段（起点由 urlStarts 给出），
// 所以这里只测「尾部到哪结束」，不测前导字符。
func TestCutURL(t *testing.T) {
	cases := map[string]string{
		"https://a.com/Movie.2024.1080p.mkv":            "https://a.com/Movie.2024.1080p.mkv",
		"https://a.com/x.mkv，提取码 1234":                  "https://a.com/x.mkv",
		"https://a.com/x.mkv（备用）":                       "https://a.com/x.mkv",
		`https://a.com/x.mkv"`:                          "https://a.com/x.mkv",
		"https://a.com/x.mkv>":                          "https://a.com/x.mkv",
		"https://a.com/x.mkv)":                          "https://a.com/x.mkv",
		"https://a.com/x.mkv))":                         "https://a.com/x.mkv",
		"https://a.com/Movie[2024].mkv":                 "https://a.com/Movie[2024].mkv",
		"https://a.com/x(a).mkv":                        "https://a.com/x(a).mkv",
		"https://a.com/x(a).mkv)":                       "https://a.com/x(a).mkv",
		"https://cdn.x.com/a/b/c.mkv?token=abc&e=12345": "https://cdn.x.com/a/b/c.mkv?token=abc&e=12345",
		"https://a.com/x.mkv。":                          "https://a.com/x.mkv",
		"https://a.com/x.mkv 后面还有字":                     "https://a.com/x.mkv",
	}
	for in, want := range cases {
		if got := cutURL(in); got != want {
			t.Errorf("cutURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// 正文里 `(https://...)` 的右括号在链外 —— urlStarts 从 http 处切，尾部由 cutURL 削。
func TestCutURLBracketBalance(t *testing.T) {
	line := "(https://a.com/x.mkv)"
	idx := urlStarts(line)[0]
	if got := cutURL(line[idx:]); got != "https://a.com/x.mkv" {
		t.Errorf("got %q", got)
	}
}

func TestScanTextForURLs(t *testing.T) {
	text := "第一行 https://a.com/one.mkv 后面还有字\n" +
		"第二行没有链接\n" +
		"两条 https://b.com/two.mkv 和 https://c.com/three.mkv\n" +
		"相对路径 ?q=%23剧情 不算"
	got := scanTextForURLs(text)
	want := []string{
		"https://a.com/one.mkv",
		"https://b.com/two.mkv",
		"https://c.com/three.mkv",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scanTextForURLs =\n  %v\nwant\n  %v", got, want)
	}
}

func TestScanTextForURLsUnescapesHTMLEntities(t *testing.T) {
	got := scanTextForURLs(`https://a.com/x.mkv?token=1&amp;e=2`)
	want := []string{"https://a.com/x.mkv?token=1&e=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHostMatches(t *testing.T) {
	cases := []struct {
		host string
		list []string
		want bool
	}{
		{"115.com", []string{"115.com"}, true},
		{"www.115.com", []string{"115.com"}, true},
		{"cdn.115.com", []string{"115.com"}, true},
		{"115cdn.com", []string{"115.com"}, false},
		{"evil-115.com", []string{"115.com"}, false},
		{"EVIL.COM", []string{"115.com"}, false},
		{"evil.com", nil, false},
		{"", []string{"115.com"}, false},
	}
	for _, tc := range cases {
		if got := hostMatches(tc.host, tc.list); got != tc.want {
			t.Errorf("hostMatches(%q, %v) = %v, want %v", tc.host, tc.list, got, tc.want)
		}
	}
}

func TestURLStarts(t *testing.T) {
	line := "看 http://a.com/x 和 https://b.com/y 还有 https://c.com/z"
	got := urlStarts(line)
	want := []int{
		len("看 "),
		len("看 http://a.com/x 和 "),
		len("看 http://a.com/x 和 https://b.com/y 还有 "),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("urlStarts = %v, want %v", got, want)
	}
	if n := len(urlStarts("https no scheme here")); n != 0 {
		t.Errorf("非 URL 文本应返回空，得到 %v", n)
	}
}
