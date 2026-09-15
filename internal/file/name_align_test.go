package file

import (
	"path"
	"testing"

	"litepan/internal/domain"
)

// 对齐时只替换目标集号，其余样本文字保持不变。
func TestAlignTemplateBuild(t *testing.T) {
	cases := []struct {
		name      string
		sample    string
		sampleEp  int
		targetEp  int
		wantName  string
		wantStyle string
	}{
		{"sxe", "Show.Name.S01E05.1080p.mkv", 5, 3, "Show.Name.S01E03.1080p.mkv", "sxe"},
		{"episode-only", "Anime EP12 [1080p].mp4", 12, 3, "Anime EP03 [1080p].mp4", "episode-only"},
		{"bracket", "Show [05].mkv", 5, 12, "Show [12].mkv", "bracket"},
		{"bare-leading", "03.mp4", 3, 7, "07.mp4", "bare"},
		{"cn-arabic", "剧集 第5集.mp4", 5, 12, "剧集 第12集.mp4", "cn-episode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tpl := buildAlignTemplate(c.sample, alignMeta{episode: c.sampleEp})
			if tpl == nil {
				t.Fatalf("样本 %q 未识别出模板", c.sample)
			}
			if tpl.style != c.wantStyle {
				t.Fatalf("style = %q，期望 %q", tpl.style, c.wantStyle)
			}
			got, ok := tpl.build(alignMeta{episode: c.targetEp}, path.Ext(c.sample))
			if !ok {
				t.Fatalf("build 失败")
			}
			if got != c.wantName {
				t.Fatalf("build = %q，期望 %q", got, c.wantName)
			}
		})
	}
}

// 中文集号按 SxxEyy 样本格式对齐。
func TestAlignChineseInputToSxeSample(t *testing.T) {
	meta, ok := extractAlignMeta("第二十八集.mp4")
	if !ok || meta.episode != 28 {
		t.Fatalf("中文数字集号应解析出 28，得到 %+v ok=%v", meta, ok)
	}
	sample := buildAlignTemplate("xxx.2026.S01E27.H265.mp4", alignMeta{episode: 27})
	if sample == nil {
		t.Fatalf("样本未识别出模板")
	}
	got, okB := sample.build(meta, ".mp4")
	if !okB || got != "xxx.2026.S01E28.H265.mp4" {
		t.Fatalf("对齐结果 = %q ok=%v，期望 xxx.2026.S01E28.H265.mp4", got, okB)
	}
}

// 反过来：中文数字集号被选为样本时，反向拼名退化为不补零阿拉伯数字，不重建中文。
func TestAlignChineseSampleDegradesToArabic(t *testing.T) {
	sample := buildAlignTemplate("剧集 第二十七集.mp4", alignMeta{episode: 27})
	if sample == nil {
		t.Fatalf("中文数字集号样本应能识别出模板（用于分组/候选）")
	}
	got, ok := sample.build(alignMeta{episode: 28}, ".mp4")
	if !ok || got != "剧集 第28集.mp4" {
		t.Fatalf("中文样本退化结果 = %q ok=%v，期望 剧集 第28集.mp4", got, ok)
	}
}

func TestAlignStackedEpisodeMarkersShareSignature(t *testing.T) {
	name6 := "中国奇谭 - S01E06 - 第 6 集.mkv"
	name7 := "中国奇谭 - S01E07 - 第 7 集.mkv"
	meta6, ok6 := extractAlignMeta(name6)
	meta7, ok7 := extractAlignMeta(name7)
	if !ok6 || !ok7 || meta6.episode != 6 || meta7.episode != 7 {
		t.Fatalf("堆叠集号解析失败：meta6=%+v ok6=%v meta7=%+v ok7=%v", meta6, ok6, meta7, ok7)
	}
	tpl6 := buildAlignTemplate(name6, meta6)
	tpl7 := buildAlignTemplate(name7, meta7)
	if tpl6 == nil || tpl7 == nil {
		t.Fatalf("堆叠集号模板识别失败：tpl6=%+v tpl7=%+v", tpl6, tpl7)
	}
	if sig6, sig7 := tpl6.signature(meta6), tpl7.signature(meta7); sig6 != sig7 {
		t.Fatalf("第六、七集应识别为同一命名格式：sig6=%q sig7=%q", sig6, sig7)
	}

	sample := buildAlignTemplate("中国奇谭.S01E01.2023.1080p.BluRay.H.264.DTS-HD MA 5.1.mkv", alignMeta{episode: 1})
	if sample == nil {
		t.Fatal("未识别到现有文件的命名样本")
	}
	got6, _ := sample.build(meta6, ".mkv")
	got7, _ := sample.build(meta7, ".mkv")
	if got6 != "中国奇谭.S01E06.2023.1080p.BluRay.H.264.DTS-HD MA 5.1.mkv" ||
		got7 != "中国奇谭.S01E07.2023.1080p.BluRay.H.264.DTS-HD MA 5.1.mkv" {
		t.Fatalf("对齐结果异常：got6=%q got7=%q", got6, got7)
	}
}

func TestUniqueNameAlignSampleCandidatesPreferHigherEpisodeInDisplayOrder(t *testing.T) {
	items := []alignAnalyzedFile{
		{
			item:      domain.FileItem{ID: "sig-a-03", Name: "Alpha.S01E03.mkv"},
			meta:      alignMeta{episode: 3},
			signature: "sig-a",
			score:     500,
		},
		{
			item:      domain.FileItem{ID: "sig-a-12", Name: "Alpha.S01E12.mkv"},
			meta:      alignMeta{episode: 12},
			signature: "sig-a",
			score:     500,
		},
		{
			item:      domain.FileItem{ID: "sig-b-09", Name: "Bravo 第9集.mkv"},
			meta:      alignMeta{episode: 9},
			signature: "sig-b",
			score:     500,
		},
		{
			item:      domain.FileItem{ID: "sig-c-02", Name: "Charlie EP02.mkv"},
			meta:      alignMeta{episode: 2},
			signature: "sig-c",
			score:     620,
		},
	}

	got := uniqueNameAlignSampleCandidates(items)
	if len(got) != 3 {
		t.Fatalf("候选数 = %d，期望 3", len(got))
	}
	if got[0].item.ID != "sig-a-12" {
		t.Fatalf("第 1 个候选 = %s，期望 sig-a-12（更大集数应优先）", got[0].item.ID)
	}
	if got[1].item.ID != "sig-b-09" {
		t.Fatalf("第 2 个候选 = %s，期望 sig-b-09", got[1].item.ID)
	}
	if got[2].item.ID != "sig-c-02" {
		t.Fatalf("第 3 个候选 = %s，期望 sig-c-02（更低集数即使分高也应排后）", got[2].item.ID)
	}
}
