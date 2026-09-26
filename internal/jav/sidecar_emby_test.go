package jav

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
)

// 侧车是**写侧**（本文件所在包）与**读侧**（internal/jav/emby）之间的契约。
// 两份结构体声明会漂移，而漂移是静默的 —— 读侧少一个字段就是 nfo 里少一个元素，
// 不报错、不 panic，Emby 那边只是那一栏空着。所以这里有两道闸门。
//
// 本文件在 package jav 里（不是 jav_test），因为要够到未导出的 sidecarDoc ——
// 而那正是 emby 包不能 import jav 的原因（会成环）。

// TestEmbySidecarCoversWriteSchema 闸门一：写侧的每个字段路径，读侧要么声明了、
// 要么在 emby 的忽略白名单里。
//
// 写侧**新增或改名**一个字段而读侧没跟上 → 当场红。读侧多声明了写侧没有的路径
// （改了名却两边都不删）也红 —— 那种「僵尸字段」会让这道闸门看起来是过的。
func TestEmbySidecarCoversWriteSchema(t *testing.T) {
	writePaths := jsonPathsOf(reflect.TypeOf(sidecarDoc{}))
	readPaths := jsonPathsOf(reflect.TypeOf(emby.SidecarDoc{}))
	ignored := emby.IgnoredPaths()

	// 白名单的键可以是**整棵子树**：命中自己、或自己的子孙都算
	// （写 `dest.files` 就盖住 `dest.files[]` 与 `dest.files[].name`）。
	isIgnored := func(path string) bool {
		for key := range ignored {
			if path == key || strings.HasPrefix(path, key+".") || strings.HasPrefix(path, key+"[") {
				return true
			}
		}
		return false
	}

	// 白名单里的每一句都必须真的存在（改了名却留着旧条目 = 闸门形同虚设），
	// 而且必须写明理由。
	for path, reason := range ignored {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("白名单 %q 没写理由 —— 理由不清楚的白名单等于把闸门拆了", path)
		}
		if !hasPathOrDescendant(writePaths, path) {
			t.Errorf("白名单里的 %q 在写侧已经不存在了（字段改名了？那这条该删）", path)
		}
	}

	for path := range writePaths {
		if _, ok := readPaths[path]; ok {
			continue
		}
		if isIgnored(path) {
			continue
		}
		t.Errorf("侧车字段 %q 读侧（internal/jav/emby）没声明，也没在忽略白名单里 —— "+
			"nfo 会少一个元素且不报错", path)
	}
	for path := range readPaths {
		if _, ok := writePaths[path]; !ok {
			t.Errorf("读侧声明了写侧没有的 %q（字段改名了？）", path)
		}
	}
}

// TestSidecarRoundTripFeedsEmbyBuilder 闸门二：用**真写侧**造一份灌满哨兵值的侧车，
// Marshal 出来给读侧 Parse、BuildNFO，逐项断言哨兵值出现在 XML 里。
//
// 哪一边改了名字，那一项就从 nfo 里消失 —— 这是唯一能挡住「静默空元素」的测试。
func TestSidecarRoundTripFeedsEmbyBuilder(t *testing.T) {
	const (
		sentNumber   = "SNT-777"
		sentTitle    = "哨兵标题"
		sentOrigin   = "哨兵原名"
		sentSummary  = "哨兵剧情简介"
		sentDirector = "哨兵导演"
		sentMaker    = "哨兵片商"
		sentActor    = "哨兵演员"
		sentSeries   = "哨兵系列"
		sentTag      = "哨兵标签"
		sentCover    = "https://tp.spfcas.com/rhe951l4q/covers/sn/snt777.jpg"
		sentPreview  = "https://tp.spfcas.com/rhe951l4q/samples/sn/p1.jpg"
		sentTrailer  = "https://example.test/sentinel-pv.mp4"
		sentJavdbID  = "SnT77"
	)
	addedAt := time.Date(2019, 5, 6, 7, 8, 9, 0, time.FixedZone("CST", 8*3600))

	raw, _, err := buildSidecar(sidecarInput{
		Movie: &domain.JavMovie{
			ID: sentJavdbID, Number: sentNumber, NumberLetter: "SNT",
			Title: sentTitle, OriginTitle: sentOrigin,
			Summary: sentSummary, ReleaseDate: "2019-05-06", Duration: 137,
			Score: 3.5, ReviewsCount: 4321, HasCNSub: true,
			CoverURL: sentCover, PreviewImages: []string{sentPreview},
			PreviewVideoURL: sentTrailer,
			DirectorName:    sentDirector, MakerName: sentMaker,
			PublisherName: sentMaker, SeriesName: sentSeries,
			Tags: []string{sentTag}, Type: domain.JavTypeUncensored,
		},
		Actors: []*domain.JavActor{{ID: "a1", Name: sentActor}},
		// 资源名带着画质标记：侧车的 quality 是**从磁链名推出来的**
		// （quality.DetectTags），不带 4K/破解/中字这三个词就一个标记都不会有。
		Record: &domain.JavPushRecord{
			ID: 1, MovieID: sentJavdbID, Code: sentNumber,
			Name:   sentNumber + "-U 4K REMUX 中文字幕",
			Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("b", 40),
		},
		InfoHash: strings.Repeat("b", 40),
		SiteBase: "https://javdb.com/",
		Dest: sidecarDest{
			AccountID: 1, ParentID: "p", Path: "/x", AddedAt: addedAt,
			Files: []*domain.FileItem{{ID: "f1", Name: sentNumber + "-4K.mp4", Size: 123}},
		},
		Now: addedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("buildSidecar 失败：%v", err)
	}

	doc, err := emby.Parse(raw)
	if err != nil {
		t.Fatalf("读侧认不出写侧刚写出来的侧车：%v", err)
	}

	// 读侧的取值先逐项对一遍（能定位到具体字段，比只在 XML 里搜字符串清楚）
	if doc.Number != sentNumber {
		t.Errorf("Number = %q", doc.Number)
	}
	if doc.Title != sentTitle || doc.OriginTitle != sentOrigin {
		t.Errorf("标题字段串了：%q / %q", doc.Title, doc.OriginTitle)
	}
	if doc.Summary != sentSummary {
		t.Errorf("Summary = %q", doc.Summary)
	}
	if doc.Director.Name != sentDirector || doc.Maker.Name != sentMaker ||
		doc.Publisher.Name != sentMaker || doc.Series.Name != sentSeries {
		t.Errorf("片商那一族串了：%+v %+v %+v %+v", doc.Director, doc.Maker, doc.Publisher, doc.Series)
	}
	if len(doc.Actors) != 1 || doc.Actors[0].Name != sentActor {
		t.Errorf("Actors = %+v", doc.Actors)
	}
	if len(doc.Tags) != 1 || doc.Tags[0] != sentTag {
		t.Errorf("Tags = %+v", doc.Tags)
	}
	if doc.CoverURL() != sentCover {
		t.Errorf("CoverURL = %q", doc.CoverURL())
	}
	// 剧照是 extrafanart 的输入，必须原样活着
	if len(doc.Images.Previews) != 1 || doc.Images.Previews[0] != sentPreview {
		t.Errorf("Previews = %+v", doc.Images.Previews)
	}
	if doc.PreviewVideoURL != sentTrailer {
		t.Errorf("PreviewVideoURL = %q", doc.PreviewVideoURL)
	}
	if doc.Duration != 137 || doc.ReviewsCount != 4321 || !doc.HasCNSub || !doc.Quality.FourK {
		t.Errorf("标量字段串了：%+v", doc)
	}
	if doc.IsCensored() {
		t.Error("type=1 是无码，不该判成有码（判错会让每张海报都切到右边）")
	}
	if got := doc.DateAdded(); !got.Equal(addedAt) {
		t.Errorf("DateAdded = %v, 期望 %v", got, addedAt)
	}

	// 再过一遍 nfo：哨兵值必须出现在输出里
	out, err := emby.BuildNFO(doc, emby.NFOOptions{
		Names:     emby.TargetNames(doc.Number, false),
		DateAdded: doc.DateAdded(),
	})
	if err != nil {
		t.Fatalf("BuildNFO 失败：%v", err)
	}
	xml := string(out)
	for _, want := range []string{
		"<num>" + sentNumber + "</num>",
		"<title>" + sentNumber + " " + sentTitle + "</title>",
		"<originaltitle>" + sentNumber + " " + sentOrigin + "</originaltitle>",
		"<![CDATA[" + sentSummary + "]]>",
		"<director>" + sentDirector + "</director>",
		"<studio>" + sentMaker + "</studio>",
		"<name>" + sentActor + "</name>",
		"<series>" + sentSeries + "</series>",
		"<genre>" + sentTag + "</genre>",
		"<cover>" + sentCover + "</cover>",
		"<trailer>" + sentTrailer + "</trailer>",
		"<dateadded>2019-05-06 07:08:09</dateadded>",
		"<rating>7</rating>",
		"<criticrating>70</criticrating>",
		"<votes>4321</votes>",
		"<runtime>137</runtime>",
		"<premiered>2019-05-06</premiered>",
		"<year>2019</year>",
		"<fileinfo>",
		"<poster>poster.jpg</poster>",
		sentJavdbID, // javdb_url 里带着详情页 id
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("nfo 里找不到 %s\n---\n%s", want, xml)
		}
	}
}

// jsonPathsOf 把一个结构体类型的所有 json 字段路径摊平：
//
//	{"images", "images.cover", "actors[]", "actors[].name", …}
//
// 切片按「元素类型」展开并带上 `[]`，所以「整块忽略」只要在白名单里写 `resource`
// 或 `dest.files[]` 就能盖住它的子孙。
func jsonPathsOf(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	collectJSONPaths(t, "", out)
	return out
}

func collectJSONPaths(t reflect.Type, prefix string, out map[string]struct{}) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		out[path] = struct{}{}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Slice, reflect.Array:
			et := ft.Elem()
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				out[path+"[]"] = struct{}{}
				collectJSONPaths(et, path+"[].", out)
			}
		case reflect.Struct:
			collectJSONPaths(ft, path+".", out)
		}
	}
}

// hasPathOrDescendant 报告集合里有没有这个路径本身或它的子孙（白名单有效性检查用）。
func hasPathOrDescendant(paths map[string]struct{}, path string) bool {
	for p := range paths {
		if p == path || strings.HasPrefix(p, path+".") || strings.HasPrefix(p, path+"[") {
			return true
		}
	}
	return false
}
