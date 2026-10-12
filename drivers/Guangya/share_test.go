package guangya

import (
	"strings"
	"testing"
)

// 目录路径比对的归一化：转存前拿它挡「ID 与路径对不上」。
// 与 115/123/夸克 的同名测试是同一份契约。
func TestSameDirPath(t *testing.T) {
	same := [][2]string{
		{"/影视", "影视"},
		{"/影视/", "/影视"},
		{"//a//b//", "/a/b"},
		{"/", ""},
		{"/", "/"},
	}
	for _, c := range same {
		if !sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = false，应当视为同一目录", c[0], c[1])
		}
	}
	diff := [][2]string{
		{"/影视", "/电影"},
		{"/a/b", "/a/c"},
		{"/影视", "/影视/子目录"},
	}
	for _, c := range diff {
		if sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = true，这是两个不同目录", c[0], c[1])
		}
	}
}

// 分享条目的字段解析必须**宽松**：真机上字段名与自己的文件列表不是同一套，
// 而分享侧接口是另一份契约 —— 少认一个名字就等于整份分享转不了。
func TestShareFileEntryPicksCandidateNames(t *testing.T) {
	cases := []struct {
		raw      string
		wantID   string
		wantName string
		wantDir  bool
	}{
		// 真机实测的形状（2026-10-12）：fileId / fileName / resType。
		{`{"fileId":"1940","fileName":"F凡。人。修。仙。传 动漫（2026）","resType":2}`, "1940", "F凡。人。修。仙。传 动漫（2026）", true},
		// 候选名：fid / name / fileType。
		{`{"fid":"abc","name":"片子.mkv","fileType":1}`, "abc", "片子.mkv", false},
		// 候选名：id / title / type 与显式 dir。
		{`{"id":"xyz","title":"另一份","type":2,"dir":true}`, "xyz", "另一份", true},
		// 数字型 id 也要收（接口可能回数字）。
		{`{"fileId":123456789,"fileName":"num.mkv"}`, "123456789", "num.mkv", false},
	}
	for _, tc := range cases {
		var e shareFileEntry
		if err := e.UnmarshalJSON([]byte(tc.raw)); err != nil {
			t.Fatalf("解析 %s 失败: %v", tc.raw, err)
		}
		if e.id() != tc.wantID {
			t.Errorf("%s: id = %q，期望 %q", tc.raw, e.id(), tc.wantID)
		}
		if e.name() != tc.wantName {
			t.Errorf("%s: name = %q，期望 %q", tc.raw, e.name(), tc.wantName)
		}
		if e.isDirectory() != tc.wantDir {
			t.Errorf("%s: isDirectory = %v，期望 %v", tc.raw, e.isDirectory(), tc.wantDir)
		}
	}
}

// 字段全认不出来时，describe() 要能把原始键值打出来 ——
// 排查「接口又改字段名了」全靠它（真机就是靠这句定位的）。
func TestShareFileEntryDescribeKeepsRawFields(t *testing.T) {
	var e shareFileEntry
	raw := `{"unknownKey":"v1","another":42}`
	if err := e.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	got := e.describe()
	for _, want := range []string{"unknownKey", "v1", "another", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe() = %q，应当包含 %q", got, want)
		}
	}
}
