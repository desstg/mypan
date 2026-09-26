package emby

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"unicode/utf8"
)

func buildSampleNFO(t *testing.T) string {
	t.Helper()
	doc := mustParse(t, sampleJSON)
	out, err := BuildNFO(doc, NFOOptions{
		Names:     TargetNames(doc.Number, false),
		DateAdded: doc.DateAdded(),
	})
	if err != nil {
		t.Fatalf("BuildNFO 失败：%v", err)
	}
	return string(out)
}

// TestBuildNFOElementMapping 逐元素对齐仓库根那份真样本 `JUR-019-U.nfo` 的形状。
//
// 这份测试的读法是「哪一行红了就是哪个元素变了」—— 每一条都写清它对应样本里的什么，
// 因为 nfo 元素的缺失是**静默**的：Emby 只是那一栏空着，不会报错。
func TestBuildNFOElementMapping(t *testing.T) {
	xml := buildSampleNFO(t)
	cases := []struct {
		label, want string
	}{
		{"番号（JAV 库配对的关键）", "<num>SSIS-001</num>"},
		{"标题：番号 + 空格 + 标题", "<title>SSIS-001 样本标题</title>"},
		{"原名", "<originaltitle>SSIS-001 サンプル</originaltitle>"},
		{"排序名（样本里等于 title）", "<sorttitle>SSIS-001 样本标题</sorttitle>"},
		{"剧情（CDATA）", "<plot><![CDATA[剧情简介]]></plot>"},
		{"原名剧情", "<originalplot><![CDATA[剧情简介]]></originalplot>"},
		{"发行日期那一行", "<outline><![CDATA[发行日期: 2024-01-01]]></outline>"},
		{"同上（tagline）", "<tagline>发行日期: 2024-01-01</tagline>"},
		{"分级", "<customrating>JP-18+</customrating>"},
		{"分级（mpaa）", "<mpaa>JP-18+</mpaa>"},
		{"地区", "<countrycode>JP</countrycode>"},
		{"未锁定", "<lockdata>false</lockdata>"},
		{"入库时间（dest.added_at）", "<dateadded>2026-09-24 01:02:03</dateadded>"},
		{"年份", "<year>2024</year>"},
		{"片长", "<runtime>120</runtime>"},
		{"演员", "<name>演员甲</name>"},
		{"演员类型", "<type>Actor</type>"},
		{"导演", "<director>导演甲</director>"},
		{"预告", "<trailer>https://example.test/pv.mp4</trailer>"},
		{"5 分制 → 10 分制", "<rating>9.38</rating>"},
		{"5 分制 → 百分制", "<criticrating>93.8</criticrating>"},
		{"评分条（来源 javdb、满分 5）", `<rating name="javdb" max="5" default="true">`},
		{"评分条的原值", "<value>4.69</value>"},
		{"票数", "<votes>12</votes>"},
		{"片商", "<studio>Madonna</studio>"},
		{"片商（maker）", "<maker>Madonna</maker>"},
		{"厂牌（label）", "<label>Madonna</label>"},
		{"发行（publisher）", "<publisher>Madonna</publisher>"},
		{"系列", "<series>系列甲</series>"},
		{"封面地址", "<cover>https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg</cover>"},
		{"详情页", "<website>https://javdb.com/v/ZY5eq</website>"},
		{"中字声明", "<subtitle>"},
		{"图片文件名与生成的文件名同源", "<poster>poster.jpg</poster>"},
		{"同上（thumb）", "<thumb>thumb.jpg</thumb>"},
		{"同上（fanart）", "<fanart>fanart.jpg</fanart>"},
	}
	for _, c := range cases {
		if !strings.Contains(xml, c.want) {
			t.Errorf("%s：nfo 里找不到 %s\n---\n%s", c.label, c.want, xml)
		}
	}
	// <set> 用演员名（样本就是这么写的，不是系列名）
	if !strings.Contains(xml, "<set>\n    <name>演员甲</name>\n  </set>") {
		t.Errorf("<set> 应当用第一个演员的名字：\n%s", xml)
	}
	// 刻意不给 <tmdbid>：那是 TMDB 的人物 id，拿 JAVDB 的填进去会让 Emby 认到别人
	if strings.Contains(xml, "<tmdbid>") {
		t.Error("不该写 <tmdbid> —— 侧车里只有 JAVDB 自家的演员 id")
	}
}

// TestBuildNFOHasUTF8BOM nfo 必须以 UTF-8 BOM 开头。
//
// 用户拿真机产物验过：没有 BOM 时 Windows 的编辑器按 GBK 解，**日文假名直接乱码**
// （他库里那份 `JUR-019-U.nfo` 就有 BOM）。这条钉住的是「别再漏掉那三个字节」，
// 顺带钉住正文确实是 UTF-8（含日文名）。
func TestBuildNFOHasUTF8BOM(t *testing.T) {
	out := []byte(buildSampleNFO(t))
	if len(out) < 3 || out[0] != 0xEF || out[1] != 0xBB || out[2] != 0xBF {
		t.Errorf("nfo 缺少 UTF-8 BOM（前 3 字节应当 ef bb bf），got % x", out[:min(3, len(out))])
	}
	if !utf8.Valid(out) {
		t.Error("nfo 不是合法 UTF-8")
	}
	if !bytes.Contains(out, []byte("サンプル")) {
		t.Error("日文原名应当原样写在文件里（不是数字实体、也不是乱码）")
	}
	// BOM 必须在声明**之前**
	if !bytes.HasPrefix(out, append([]byte{0xEF, 0xBB, 0xBF}, []byte(xml.Header)...)) {
		t.Errorf("BOM 应当在 <?xml 之前，got %q", out[:40])
	}
}

// TestBuildNFORatingFollowsScoreMax 评分换算**按 score_max**，不是写死 ×2/×20。
//
// 写死系数的后果：上游哪天换成 10 分制，所有评分翻倍，而界面上看起来只是「评分不对」。
func TestBuildNFORatingFollowsScoreMax(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","score":2.5,"score_max":5}`)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("X-1", false)})
	if err != nil {
		t.Fatal(err)
	}
	xml := string(out)
	if !strings.Contains(xml, "<rating>5</rating>") {
		t.Errorf("2.5/5 → 5/10，got:\n%s", xml)
	}
	if !strings.Contains(xml, "<criticrating>50</criticrating>") {
		t.Errorf("2.5/5 → 50/100，got:\n%s", xml)
	}

	// score_max 缺失时**不能除零**：那会写出 +Inf，Emby 会把整份 nfo 当坏数据。
	doc = mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","score":4.69,"score_max":0}`)
	out, err = BuildNFO(doc, NFOOptions{Names: TargetNames("X-1", false)})
	if err != nil {
		t.Fatal(err)
	}
	if xml = string(out); strings.Contains(xml, "Inf") || strings.Contains(xml, "NaN") {
		t.Errorf("score_max=0 时写出了非数：\n%s", xml)
	}
}

// TestBuildNFOGenreTagSameSet genre 与 tag 是同一批词的两种排法（样本如此）。
func TestBuildNFOGenreTagSameSet(t *testing.T) {
	doc := mustParse(t, sampleJSON)
	out, _ := BuildNFO(doc, NFOOptions{Names: TargetNames(doc.Number, false)})
	xml := string(out)

	genres := elementsOf(xml, "genre")
	tags := elementsOf(xml, "tag")
	if len(genres) == 0 || len(tags) == 0 {
		t.Fatalf("genre/tag 不该为空：genres=%v tags=%v", genres, tags)
	}
	seen := map[string]int{}
	for _, g := range genres {
		seen[g]++
	}
	for _, tg := range tags {
		seen[tg]--
	}
	for word, diff := range seen {
		if diff != 0 {
			t.Errorf("%q 在 genre 与 tag 里出现次数不一致（差 %d）", word, diff)
		}
	}
	// 合成的三条（系列 / 片商 / 发行）与画质标记、番号字母、演员名都在
	inGenres := map[string]bool{}
	for _, g := range genres {
		inGenres[g] = true
	}
	for _, want := range []string{"4K", "SSIS", "演员甲", "系列: 系列甲", "片商: Madonna", "发行: Madonna"} {
		if !inGenres[want] {
			t.Errorf("genre 里缺少 %q：%v", want, genres)
		}
	}
	// tag 是按码位排序的（样本就是这个顺序）
	if !sortedCopy(tags) {
		t.Errorf("tag 应当有序：%v", tags)
	}
}

// TestBuildNFOOmitsEmpty 空值不该产出空元素。
//
// 空元素（`<director></director>`）会被部分刮削器当成「这个字段存在且为空」，
// 从而不再去别处找 —— 比缺元素更糟。
func TestBuildNFOOmitsEmpty(t *testing.T) {
	doc := mustParse(t, `{"schema":"litepan.jav.sidecar/1","number":"X-1","title":"只有标题"}`)
	out, err := BuildNFO(doc, NFOOptions{Names: TargetNames("X-1", false)})
	if err != nil {
		t.Fatal(err)
	}
	xml := string(out)
	for _, absent := range []string{"<director>", "<series>", "<rating>", "<votes>", "<trailer>", "<cover>", "<dateadded>", "<fileinfo>", "<set>", "<premiered>"} {
		if strings.Contains(xml, absent) {
			t.Errorf("空值的元素不该出现：%s\n%s", absent, xml)
		}
	}
	// 但 poster/thumb/fanart 三行**必须有**（Emby 靠它们找图，缺了就是默认名找不到）
	for _, present := range []string{"<poster>poster.jpg</poster>", "<thumb>thumb.jpg</thumb>", "<fanart>fanart.jpg</fanart>"} {
		if !strings.Contains(xml, present) {
			t.Errorf("图片文件名不能省：%s", present)
		}
	}
}

// TestBuildNFOFlatNamesFollowLayout 平铺布局下那三个文件名也要跟着变 ——
// nfo 与生成器必须给出同一个答案，否则 Emby 拿着 nfo 去找一个不存在的文件。
func TestBuildNFOFlatNamesFollowLayout(t *testing.T) {
	doc := mustParse(t, sampleJSON)
	out, _ := BuildNFO(doc, NFOOptions{Names: TargetNames("SSIS-001", true)})
	xml := string(out)
	for _, want := range []string{"<poster>SSIS-001-poster.jpg</poster>", "<thumb>SSIS-001-thumb.jpg</thumb>", "<fanart>SSIS-001-fanart.jpg</fanart>"} {
		if !strings.Contains(xml, want) {
			t.Errorf("平铺布局缺少 %s\n%s", want, xml)
		}
	}
}

func TestBuildNFOTitleNotDoubleNumbered(t *testing.T) {
	// JAVDB 的标题有时自带番号，别拼成 `JUR-019 JUR-019 脱衣舞剧场…`
	if got := withNumber("JUR-019", "JUR-019 脱衣舞剧场跳舞的人妻"); got != "JUR-019 脱衣舞剧场跳舞的人妻" {
		t.Errorf("标题已带番号时不该重复拼：%q", got)
	}
	if got := withNumber("JUR-019", "jur-019 小写也算"); got != "jur-019 小写也算" {
		t.Errorf("大小写不敏感：%q", got)
	}
	if got := withNumber("JUR-019", ""); got != "JUR-019" {
		t.Errorf("没标题时只留番号：%q", got)
	}
}

func TestRound2AvoidsFloatNoise(t *testing.T) {
	// 4.69 * 10 / 5 在浮点里是 9.380000000000001，直接格式化会把这个尾数写进 nfo
	if got := trimFloat(round2(4.69 * 10 / 5)); got != "9.38" {
		t.Errorf("trimFloat(round2(9.38…)) = %q", got)
	}
	if got := trimFloat(round2(4.69 * 100 / 5)); got != "93.8" {
		t.Errorf("got %q", got)
	}
	zero := 0.0
	if got := trimFloat(round2(2.0 / zero)); got != "0" {
		t.Errorf("Inf 应当被压成 0，got %q", got)
	}
}

// elementsOf 取形如 <name>…</name> 的元素文本（这里只用于单行元素）。
func elementsOf(xml, name string) []string {
	var out []string
	open, closeTag := "<"+name+">", "</"+name+">"
	rest := xml
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			return out
		}
		rest = rest[i+len(open):]
		j := strings.Index(rest, closeTag)
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j+len(closeTag):]
	}
}

func sortedCopy(in []string) bool {
	for i := 1; i < len(in); i++ {
		if in[i-1] > in[i] {
			return false
		}
	}
	return true
}
