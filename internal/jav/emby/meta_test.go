package emby

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAPIFacingStructsUseSnakeCaseJSON 钉住「这几个结构体是直接出给前端的」。
//
// 这一族坑在本项目里踩过好几次（见 memory 的「静默变空」）：Go 结构体**没有 json tag**
// 时序列化出去是大驼峰，而前端读的是 snake_case —— 全是 undefined，不报错，只是
// 「哪儿都不对」。海报墙的裁剪框就这么栽过一次：`CropRect` 出去是 `{"X":…,"W":…}`，
// 前端拿 `rect.w` 得到 undefined，拼出来的样式是 `undefinedpx`，**框根本不显示**。
func TestAPIFacingStructsUseSnakeCaseJSON(t *testing.T) {
	blob, err := json.Marshal(struct {
		Meta   *MovieMeta `json:"meta"`
		Rect   CropRect   `json:"rect"`
		Names  Names      `json:"names"`
		Detail struct {
			Poster struct {
				Rect CropRect `json:"rect"`
			} `json:"poster"`
		} `json:"detail"`
	}{
		Meta:  &MovieMeta{Number: "NIMA-086", Title: "x", ReleaseDate: "2026-01-01", FourK: true},
		Rect:  CropRect{X: 1, Y: 2, W: 3, H: 4},
		Names: TargetNames("NIMA-086-U", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(blob)
	// 前端真正读的那些键，一个都不能少
	for _, want := range []string{
		`"number":`, `"number_letter":`, `"title":`, `"release_date":`, `"four_k":`,
		`"x":`, `"y":`, `"w":`, `"h":`,
		`"nfo":`, `"poster":`, `"thumb":`, `"fanart":`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON 里缺少 %s —— 前端读的就是这个键：\n%s", want, got)
		}
	}
	// 反面：不该出现大驼峰（漏 tag 的典型症状）
	for _, bad := range []string{`"Number":`, `"ReleaseDate":`, `"X":`, `"W":`, `"NFO":`, `"Poster":`} {
		if strings.Contains(got, bad) {
			t.Errorf("JSON 里出现了大驼峰键 %s（结构体漏了 json tag）：\n%s", bad, got)
		}
	}
}

// TestEmptySlicesMarshalAsArray 空切片出 JSON 必须是 `[]`，不能是 `null`。
//
// 前端那两个 chip 列表（演员 / 标签）读的是 `modelValue.length` —— 拿到 null 会抛
// TypeError，而那是**渲染期**的异常：整个抽屉的内容区都渲染不出来，用户看到的是
// 「空白页，连提示都没有」。实测就栽在这：没有演员也没有标签的那批片全打不开。
func TestEmptySlicesMarshalAsArray(t *testing.T) {
	meta := &MovieMeta{Number: "X-1", Title: "x"}
	for _, raw := range []string{`{"schema":"litepan.jav.sidecar/1","number":"X-1"}`, `<movie><num>X-1</num><title>x</title></movie>`} {
		var parsed *MovieMeta
		var err error
		if raw[0] == '{' {
			doc, perr := Parse([]byte(raw))
			if perr != nil {
				t.Fatal(perr)
			}
			parsed = MovieMetaFromSidecar(doc, NFOOptions{Names: TargetNames("X-1", false)})
		} else {
			parsed, err = ParseNFO([]byte(raw), NFOReadHints{})
			if err != nil {
				t.Fatal(err)
			}
		}
		blob, err := json.Marshal(parsed)
		if err != nil {
			t.Fatal(err)
		}
		got := string(blob)
		if !strings.Contains(got, `"actors":[]`) || !strings.Contains(got, `"tags":[]`) {
			t.Errorf("空列表应当序列化成 []，got：\n%s", got)
		}
	}
	_ = meta
}
