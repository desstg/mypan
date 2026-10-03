package emby

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nfo 的读写往返。
//
// **往返逐字节相同是「编辑器可以上线」的唯一凭据**：它保证「打开抽屉、什么都不改、
// 保存」不会在文件里留下任何变化 —— 用户改标题时只会多出那一处 diff。

func TestParseNFORoundTripByteEqual(t *testing.T) {
	docs := []struct {
		label string
		doc   *SidecarDoc
		names Names
		added time.Time
		hints NFOReadHints
	}{
		{
			label: "全字段（4K + 破解 + 中字 + 演员 + 标签 + 片商 + 系列）",
			doc:   mustParse(t, sampleJSON),
			names: TargetNames("SSIS-001", false),
			added: time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC),
			hints: NFOReadHints{NumberLetter: "SSIS", Censored: true},
		},
		{
			label: "平铺命名（多分片那种）",
			doc:   mustParse(t, sampleJSON),
			names: TargetNames("SSIS-001-cd2", true),
			added: time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC),
			hints: NFOReadHints{NumberLetter: "SSIS", Censored: true},
		},
		{
			label: "只有番号（没有摘要 / 演员 / 标签 / 片商 / 评分）",
			doc:   mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"MGL0002"}`),
			names: TargetNames("MGL-0002", false),
			hints: NFOReadHints{NumberLetter: "", Censored: false},
		},
		{
			label: "无码 + 无 4K",
			doc:   mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"Tushy.2026.09.20","title":"A & B","summary":"含 & 与 < 的剧情","type":"1","score":4.2,"score_max":5,"reviews_count":7,"actors":[{"name":"演员甲"}],"tags":["标签一"]}`),
			names: TargetNames("Tushy.2026.09.20", false),
			hints: NFOReadHints{NumberLetter: "", Censored: false},
		},
		{
			label: "带标记的日期序号型（字母为空、有破解）",
			doc:   mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"092526-001","number_letter":"","title":"标题","quality":{"uncensored":true,"subtitle":true,"four_k":false}}`),
			names: TargetNames("092526-001", false),
			hints: NFOReadHints{NumberLetter: "", Censored: false},
		},
	}
	for _, c := range docs {
		t.Run(c.label, func(t *testing.T) {
			opts := NFOOptions{Names: c.names, DateAdded: c.added}
			first, err := BuildNFO(c.doc, opts)
			if err != nil {
				t.Fatalf("BuildNFO：%v", err)
			}
			meta, err := ParseNFO(first, c.hints)
			if err != nil {
				t.Fatalf("ParseNFO：%v", err)
			}
			second, err := BuildNFOFromMeta(meta)
			if err != nil {
				t.Fatalf("BuildNFOFromMeta：%v", err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("往返后字节不同 —— 编辑一次就会在文件里留下无意的改动\n--- 第一次\n%s\n--- 第二次\n%s", first, second)
			}
		})
	}
}

// TestParseNFORealLibraryShape 拿**真库那份 nfo**（拷进 testdata 的 NIMA-086-U.nfo）验：
// 字段还原、合成项剥离、以及往返仍逐字节相同。
func TestParseNFORealLibraryShape(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "NIMA-086-U.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseNFO(raw, NFOReadHints{NumberLetter: "NIMA", Censored: true})
	if err != nil {
		t.Fatalf("ParseNFO：%v", err)
	}

	if meta.Number != "NIMA-086" {
		t.Errorf("Number = %q", meta.Number)
	}
	if strings.HasPrefix(meta.Title, "NIMA-086 ") {
		t.Errorf("标题应当去掉番号前缀，got %q", meta.Title)
	}
	if len(meta.Actors) != 2 {
		t.Errorf("演员应当是 2 个，got %v", meta.Actors)
	}
	if meta.Maker != "Fitch" || meta.Label != "Fitch" {
		t.Errorf("片商还原错了：%+v", meta)
	}
	// 合成项必须被剥掉，只留真标签
	wantTags := []string{"巨乳", "单体作品", "原作改编", "中出", "潮吹"}
	if strings.Join(meta.Tags, "|") != strings.Join(wantTags, "|") {
		t.Errorf("标签还原 = %v，期望 %v", meta.Tags, wantTags)
	}
	if meta.NumberLetter != "NIMA" || !meta.Uncensored || meta.FourK {
		t.Errorf("标记还原错了：letter=%q uncensored=%v fourK=%v", meta.NumberLetter, meta.Uncensored, meta.FourK)
	}
	// 这份样本**没有** <fileinfo>（这部片没中字）—— 中字那条路在往返测试的
	// 「全字段」用例里覆盖（那份带 subtitle）。
	if meta.HasSubtitle {
		t.Error("这份 nfo 里没有 <fileinfo>，不该还原出中字标记")
	}
	if meta.Duration != 120 || meta.ReleaseDate != "2026-09-29" {
		t.Errorf("时长/发行日期还原错了：%d / %q", meta.Duration, meta.ReleaseDate)
	}
	if meta.Score != 4.61 || meta.ScoreMax != 5 {
		t.Errorf("评分还原错了：%v / %d", meta.Score, meta.ScoreMax)
	}
	if meta.Names.Poster != "poster.jpg" || meta.Names.Thumb != "thumb.jpg" || meta.Names.Fanart != "fanart.jpg" {
		t.Errorf("图片文件名要原样保留，got %+v", meta.Names)
	}
	if meta.AddedAt.IsZero() {
		t.Error("<dateadded> 没读出来")
	}

	// 往返：真文件 → 解析 → 重建，必须逐字节相同
	again, err := BuildNFOFromMeta(meta)
	if err != nil {
		t.Fatalf("BuildNFOFromMeta：%v", err)
	}
	if !bytes.Equal(raw, again) {
		t.Errorf("真库 nfo 往返后有差异（%d → %d 字节）", len(raw), len(again))
	}
}

// TestParseNFOWithoutHints 没有侧车（拿不到番号字母）时的取舍：
// 字母**留在标签里**（看得见、能删），标记仍然被识别。
func TestParseNFOWithoutHints(t *testing.T) {
	doc := mustParse(t, sampleJSON)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("SSIS-001", false)})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseNFO(out, NFOReadHints{})
	if err != nil {
		t.Fatal(err)
	}
	// 拿不到侧车时，字母从番号本体推出来（`SSIS-001` → `SSIS`）**且它真的在
	// genre 列表里** → 与有 hints 时等价：剥得掉、也加得回来。
	if meta.NumberLetter != "SSIS" {
		t.Errorf("应当从番号本体推出字母 SSIS，got %q", meta.NumberLetter)
	}
	for _, tag := range meta.Tags {
		if tag == "SSIS" {
			t.Errorf("字母不该留在标签里，got %v", meta.Tags)
		}
	}
	// 这份样本的侧车 uncensored=false，所以不该有破解标记（反过来验一次）
	if meta.Uncensored {
		t.Error("侧车里 uncensored=false，不该还原出破解标记")
	}
	// 往返仍然成立：字母留在标签里 → 重建成同一份字节
	again, err := BuildNFOFromMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, again) {
		t.Errorf("无 hints 的往返也应当字节相同")
	}
}

// TestParseNFOTagEqualsActorName 记录一条**已知且接受**的损失，不是 bug。
//
// buildGenres 会去重，所以「真标签恰好等于演员名」时那份词只出现一次，读回来
// 只能还出一份 —— 到底该算标签还是演员，从 nfo 里无从判断。
func TestParseNFOTagEqualsActorName(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","actors":[{"name":"同名"}],"tags":["同名","别的"]}`)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("X-1", false)})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ParseNFO(out, NFOReadHints{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(meta.Tags, "|") != "别的" {
		t.Errorf("同名的那个词会被当成演员剥掉（已知损失），got %v", meta.Tags)
	}
	// 词没丢（它在演员表里），但 **genre 的顺序会变**：原来「同名」在标签段，
	// 重建后落入合成段。这是这条歧义的必然代价 —— 如实记下，不去猜。
	again, err := BuildNFOFromMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(out, again) {
		t.Log("往返字节相同了 —— 说明这条歧义已被别的方式消解，可以删掉这条记录")
	}
	if !bytes.Contains(again, []byte("<genre>同名</genre>")) {
		t.Error("词不该丢：同名仍应出现在 genre 里")
	}
	if !bytes.Contains(again, []byte("<genre>别的</genre>")) {
		t.Error("别的标签不该丢")
	}
}

// TestParseNFOMultipleSetsRoundTrip 多 `<set>` 必须原样收下、原样写回。
//
// 这是编辑器「打开不改保存」的字节稳定里最容易破的一环：<set> 在扫描那条路上是
// **算出来**的（一位女演员一个，看性别、有上限），如果重建时也走那套算法，
// 用户手工加/删过合集的 nfo 一保存就会被重算覆盖 —— 而 Emby 那边只表现为
// 「合集变了」，没人会想到是编辑器干的。
//
// 用仓库根那份真产物 `BBAN-548.nfo` 的形状（两个 <set>，两位女演员）。
func TestParseNFOMultipleSetsRoundTrip(t *testing.T) {
	const raw = `<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
  <title>BBAN-548 标题</title>
  <actor>
    <name>二羽紗愛</name>
    <type>Actor</type>
  </actor>
  <actor>
    <name>弥生美月</name>
    <type>Actor</type>
  </actor>
  <set>
    <name>二羽紗愛</name>
  </set>
  <set>
    <name>弥生美月</name>
  </set>
  <num>BBAN-548</num>
</movie>
`
	meta, err := ParseNFO([]byte(raw), NFOReadHints{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(meta.Sets, "|") != "二羽紗愛|弥生美月" {
		t.Fatalf("多 <set> 没读全或顺序错了：%v", meta.Sets)
	}
	if len(meta.Actors) != 2 {
		t.Fatalf("演员应当是 2 个：%v", meta.Actors)
	}
	again, err := BuildNFOFromMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	// 两个 <set> 都要在，且**顺序不变**
	for _, name := range []string{"二羽紗愛", "弥生美月"} {
		if !bytes.Contains(again, []byte("<set>\n    <name>"+name+"</name>\n  </set>")) {
			t.Errorf("重建后丢了 <set><name>%s</name>：\n%s", name, again)
		}
	}
	if got := bytes.Count(again, []byte("<set>")); got != 2 {
		t.Errorf("<set> 条数 = %d，want 2\n%s", got, again)
	}
}

func TestParseNFORejectsGarbage(t *testing.T) {
	cases := []struct {
		label, raw string
	}{
		{"空", ""},
		{"不是 XML", "hello"},
		{"根元素不是 movie", `<?xml version="1.0"?><tvshow><title>x</title></tvshow>`},
		{"坏 XML", `<movie><title>x</movie>`},
	}
	for _, c := range cases {
		if _, err := ParseNFO([]byte(c.raw), NFOReadHints{}); err == nil {
			t.Errorf("%s：应当报错", c.label)
		}
	}
}

func TestTitleAndNumber(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"SSIS-001","title":"A & B <标题>"}`)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("SSIS-001", false)})
	if err != nil {
		t.Fatal(err)
	}
	title, number, err := TitleAndNumber(out)
	if err != nil {
		t.Fatalf("TitleAndNumber：%v", err)
	}
	if number != "SSIS-001" {
		t.Errorf("number = %q", number)
	}
	// 转义必须由 XML 解析器还原（手写扫描会显示出 `A &amp; B`）
	if !strings.Contains(title, "A & B <标题>") {
		t.Errorf("标题里的实体没还原：%q", title)
	}
}

// TestParseNFOBOMTolerated 写侧带 UTF-8 BOM，读侧必须能吃下自己的产物。
//
// 这条单独列出来是因为真机上验过坑：不带 BOM 的 nfo 在 Windows 编辑器里按 GBK 解、
// 日文乱码，所以我们加了 BOM；而「加了 BOM 会不会读不回来」必须有测试兜着。
func TestParseNFOBOMTolerated(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","title":"テスト"}`)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("X-1", false)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, utf8BOM) {
		t.Fatal("前置条件：写出来的 nfo 应当带 BOM")
	}
	meta, err := ParseNFO(out, NFOReadHints{})
	if err != nil {
		t.Fatalf("带 BOM 的 nfo 解析失败：%v", err)
	}
	if meta.Title != "テスト" {
		t.Errorf("标题 = %q", meta.Title)
	}
}
