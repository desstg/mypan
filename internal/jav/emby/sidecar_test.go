package emby

import (
	"strings"
	"testing"
)

const sampleJSON = `{
  "schema": "litepan.jav.sidecar/1",
  "generated_at": "2026-09-24T02:00:00+08:00",
  "number": "SSIS-001",
  "number_letter": "SSIS",
  "title": "样本标题",
  "origin_title": "サンプル",
  "javdb_url": "https://javdb.com/v/ZY5eq",
  "release_date": "2024-01-01",
  "duration": 120,
  "score": 4.69,
  "score_max": 5,
  "reviews_count": 12,
  "has_cnsub": true,
  "type": "0",
  "type_label": "有码",
  "summary": "剧情简介",
  "preview_video_url": "https://example.test/pv.mp4",
  "dest": {"account_id": 1, "parent_id": "p-1", "path": "/番号/SSIS-001",
           "added_at": "2026-09-24T01:02:03+08:00",
           "files": [{"name": "SSIS-001-4K.mp4", "size": 6979321856, "is_dir": false}]},
  "director": {"id": "d1", "name": "导演甲"},
  "maker": {"id": "k1", "name": "Madonna"},
  "publisher": {"id": "p1", "name": "Madonna"},
  "series": {"id": "s1", "name": "系列甲"},
  "actors": [{"id": "a1", "name": "演员甲", "gender": 1, "avatar": "https://x/a.jpg"}],
  "tags": ["巨乳", "单体作品"],
  "images": {
    "cover": "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg",
    "thumb": "https://tp.spfcas.com/rhe951l4q/small_covers/ve/vezpEn.jpg",
    "javbus_cover": "",
    "previews": ["https://tp.spfcas.com/rhe951l4q/samples/aq/a1.jpg"],
    "note": "说明"
  },
  "resource": {"name": "SSIS-001-U 4K", "magnet": "magnet:?xt=urn:btih:aa"},
  "quality": {"resolution": "4K", "tier": "超清", "four_k": true, "uhd": true, "hd": false,
              "uncensored": false, "subtitle": true, "edited": false, "source": "REMUX",
              "codec": "HEVC", "pack": false}
}`

func mustParse(t *testing.T, raw string) *SidecarDoc {
	t.Helper()
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse 失败：%v", err)
	}
	return doc
}

func TestParseAcceptsSample(t *testing.T) {
	doc := mustParse(t, sampleJSON)
	if doc.Number != "SSIS-001" {
		t.Errorf("number = %q", doc.Number)
	}
	if !doc.IsCensored() {
		t.Error("type=0 是有码")
	}
	if got := doc.CoverURL(); !strings.Contains(got, "covers/ve/vezpEn.jpg") {
		t.Errorf("CoverURL = %q", got)
	}
	if got := doc.ReleaseYear(); got != "2024" {
		t.Errorf("ReleaseYear = %q", got)
	}
	if got := doc.DateAdded().Format("2006-01-02 15:04:05"); got != "2026-09-24 01:02:03" {
		t.Errorf("DateAdded = %q（应当取 dest.added_at）", got)
	}
	if len(doc.Images.Previews) != 1 {
		t.Errorf("previews 条数 = %d", len(doc.Images.Previews))
	}
}

// TestParseRejects 三种必须拒绝的输入。
//
// **must reject 的理由**：读侧存在的意义就是不信写侧 —— 上游换了格式版本、
// 或者一份根本不是侧车的 json（网盘上满地都是），都必须在这里停下，
// 而不是拿零值往下走、生成一份看起来「信息不全」的 nfo。
func TestParseRejects(t *testing.T) {
	cases := []struct {
		label, raw string
	}{
		{"别的 schema", `{"schema":"litepan.jav.sidecar/2","number":"SSIS-001"}`},
		{"没有 schema", `{"number":"SSIS-001"}`},
		{"没有番号", `{"schema":"litepan.jav.sidecar/1","number":"  "}`},
		{"不是 JSON", `not json at all`},
		{"空文件", ``},
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c.raw)); err == nil {
			t.Errorf("%s：应当报错", c.label)
		}
	}
}

// TestParseToleratesMissingOptional 可选块缺失 / 为 null 时不能 panic，
// 也不能把「没给」变成「给了一个零值」以外的怪东西。
func TestParseToleratesMissingOptional(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"MGL0002","images":{"previews":null},"actors":null,"tags":null}`)
	if len(doc.Images.Previews) != 0 || len(doc.Actors) != 0 || len(doc.Tags) != 0 {
		t.Errorf("null 应当等同于空：%+v", doc)
	}
	if doc.IsCensored() {
		t.Error("type 缺失时不能猜有码 —— 猜错会让无码片每张海报都切到右边")
	}
	if doc.CoverURL() != "" {
		t.Error("没有任何封面地址时应当是空串")
	}
	// 没有时间字段时回落「现在」，不能是零值（那会写出 0001-01-01）
	if doc.DateAdded().Year() < 2000 {
		t.Errorf("DateAdded 回落到零值了：%v", doc.DateAdded())
	}
}

func TestDateAddedFallsBack(t *testing.T) {
	// dest.added_at 缺失 → 用 generated_at
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","generated_at":"2026-01-02T03:04:05Z","dest":{"added_at":""}}`)
	if got := doc.DateAdded().UTC().Format("2006-01-02 15:04:05"); got != "2026-01-02 03:04:05" {
		t.Errorf("应当回落到 generated_at，got %q", got)
	}
	// 时间写法容错：nfo 自己那种 `2006-01-02 15:04:05`
	doc = mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","dest":{"added_at":"2026-08-24 00:05:08"}}`)
	if got := doc.DateAdded().Format("2006-01-02 15:04:05"); got != "2026-08-24 00:05:08" {
		t.Errorf("应当时刻原样认出来，got %q", got)
	}
}

func TestTargetNames(t *testing.T) {
	solo := TargetNames("SSIS-001", false)
	want := Names{NFO: "SSIS-001.nfo", Poster: "poster.jpg", Thumb: "thumb.jpg", Fanart: "fanart.jpg", ExtraDir: "extrafanart"}
	if solo != want {
		t.Errorf("独占目录 = %+v, 期望 %+v", solo, want)
	}
	flat := TargetNames("SSIS-001", true)
	if flat.Poster != "SSIS-001-poster.jpg" || flat.Thumb != "SSIS-001-thumb.jpg" || flat.Fanart != "SSIS-001-fanart.jpg" {
		t.Errorf("平铺 = %+v", flat)
	}
	if flat.NFO != "SSIS-001.nfo" {
		t.Errorf("nfo 名与布局无关（Emby 按主干配对），got %q", flat.NFO)
	}
	if flat.ExtraDir != "" {
		t.Error("平铺时不该写 extrafanart/ —— 那是目录级约定，几部片共用必然互相覆盖")
	}
}

func TestExtraFanartName(t *testing.T) {
	if got := ExtraFanartName(1); got != "fanart1.jpg" {
		t.Errorf("ExtraFanartName(1) = %q", got)
	}
	if got := ExtraFanartName(12); got != "fanart12.jpg" {
		t.Errorf("ExtraFanartName(12) = %q", got)
	}
}
