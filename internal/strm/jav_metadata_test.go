package strm

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"litepan/internal/jav/emby"
	"litepan/internal/settings"
)

// testLogger 丢弃日志：这些用例断言的是文件与调用次数，日志只是噪声。
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 番号元数据生成那一半（拿到**本地** json 之后的全部行为）。
//
// 「json 是怎么下到本地的」那一半不在这里测：它走的是既有的元数据同步链路
// （metadata_test.go / metadata_reconcile_test.go 覆盖），本文件只钉「接口处」
// —— 即 `javMetaExtensions` 确实把 json 加进了扩展名集合（见文件末尾那条）。

const sampleSidecar = `{
  "schema": "litepan.jav.sidecar/1",
  "generated_at": "2026-09-24T02:00:00+08:00",
  "number": "SSIS-001", "number_letter": "SSIS",
  "title": "样本标题", "origin_title": "サンプル",
  "javdb_url": "https://javdb.com/v/ZY5eq", "release_date": "2024-01-01",
  "duration": 120, "score": 4.69, "score_max": 5, "reviews_count": 12,
  "has_cnsub": true, "type": "0", "summary": "剧情简介",
  "images": {
    "cover": "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg",
    "previews": ["https://tp.spfcas.com/rhe951l4q/samples/aq/a1.jpg",
                 "https://tp.spfcas.com/rhe951l4q/samples/aq/a2.jpg"]
  },
  "quality": {"four_k": true, "uncensored": false, "subtitle": true}
}`

// stubFetcher 记账的图片抓取桩：既回答「生成了什么」，也回答「打了几次上游」。
type stubFetcher struct {
	mu    sync.Mutex
	calls int
	byURL map[string][]byte
}

func newStubFetcher() *stubFetcher {
	return &stubFetcher{byURL: map[string][]byte{}}
}

func (f *stubFetcher) FetchImage(_ context.Context, rawURL string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if data, ok := f.byURL[rawURL]; ok {
		return data, "image/jpeg", nil
	}
	return sampleJPEG(800, 538), "image/jpeg", nil
}

func (f *stubFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func allOn() settings.JavMetaItems { return settings.DefaultJavMetaItems() }

func sampleJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

// newJavTaskDir 摆一个「一部片 + 它的侧车」的目录，返回 strm 相对路径。
func newJavTaskDir(t *testing.T, sidecarName string) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC-4K.strm"), []byte("http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, sidecarName), []byte(sampleSidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, "SSIS-001-UC-4K.strm"
}

// TestGenerateJavArtifactsWritesFullSet 一套齐活的生成：nfo + thumb + fanart + poster + 剧照。
func TestGenerateJavArtifactsWritesFullSet(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	fetcher := newStubFetcher()

	res := generateJavArtifacts(context.Background(), javArtifactRequest{
		Root:      root,
		StrmFiles: []string{rel},
		Items:     allOn(),
		Images:    fetcher,
		Log:       testLogger(t),
	})
	if res.NoSidecar != 0 {
		t.Fatalf("侧车该配得上，NoSidecar=%d", res.NoSidecar)
	}
	// 1 张封面 + 2 张剧照 = 3 次；poster 从**本地** thumb 裁，不再联网
	if fetcher.callCount() != 3 {
		t.Errorf("上游请求次数 = %d，期望 3（封面 1 + 剧照 2；poster 必须从本地裁）", fetcher.callCount())
	}

	// 文件落位
	for _, name := range []string{"SSIS-001-UC-4K.nfo", "thumb.jpg", "fanart.jpg", "poster.jpg"} {
		if !artifactExists(filepath.Join(root, name)) {
			t.Errorf("缺少 %s", name)
		}
	}
	for _, name := range []string{"fanart1.jpg", "fanart2.jpg"} {
		if !artifactExists(filepath.Join(root, "extrafanart", name)) {
			t.Errorf("缺少 extrafanart/%s", name)
		}
	}

	// fanart 是 thumb 的**字节复制**（用户明确要求）
	thumb, _ := os.ReadFile(filepath.Join(root, "thumb.jpg"))
	fanart, _ := os.ReadFile(filepath.Join(root, "fanart.jpg"))
	if !bytes.Equal(thumb, fanart) {
		t.Error("fanart.jpg 应当与 thumb.jpg 逐字节相同")
	}

	// poster：有码 → 取右侧，高度与封面相同；宽度用**放宽后**的比例
	// （比 Emby 标准的 2:3 宽一点，为了别把人物卡太紧 —— 见 emby.PosterWideRatio）
	poster := decodeJPEGFile(t, filepath.Join(root, "poster.jpg"))
	w, h := poster.Bounds().Dx(), poster.Bounds().Dy()
	if diff := float64(w)/float64(h) - emby.PosterWideRatio; diff > 0.01 || diff < -0.01 {
		t.Errorf("poster 比例 %d/%d 不是放宽后的 %v", w, h, emby.PosterWideRatio)
	}
	if h != 538 {
		t.Errorf("poster 高度应当与封面同高（538），got %d", h)
	}

	// nfo 的内容来自**本地那份 json**
	nfo, _ := os.ReadFile(filepath.Join(root, "SSIS-001-UC-4K.nfo"))
	if !bytes.Contains(nfo, []byte("<num>SSIS-001</num>")) || !bytes.Contains(nfo, []byte("<poster>poster.jpg</poster>")) {
		t.Errorf("nfo 内容不对：\n%s", nfo)
	}
}

// TestGenerateJavArtifactsIdempotent 第二轮：一个文件都不写、**一次上游都不打**。
//
// 这条比「文件没变」更重要：扫描是**定时**跑的，每轮重下一遍封面会把图床打毛
// （而 JAVDB 的图床被打了是要封号的）。
func TestGenerateJavArtifactsIdempotent(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	req := func(f *stubFetcher) javArtifactRequest {
		return javArtifactRequest{Root: root, StrmFiles: []string{rel}, Items: allOn(), Images: f, Log: testLogger(t)}
	}
	first := generateJavArtifacts(context.Background(), req(newStubFetcher()))
	if first.Written == 0 {
		t.Fatal("第一轮应当写出文件")
	}

	fetcher := newStubFetcher()
	second := generateJavArtifacts(context.Background(), req(fetcher))
	if second.Written != 0 {
		t.Errorf("第二轮不该再写文件，got %d", second.Written)
	}
	if fetcher.callCount() != 0 {
		t.Errorf("第二轮不该打上游，got %d 次", fetcher.callCount())
	}
}

// TestGenerateJavArtifactsHealsMissingThumb 删掉 thumb 之后只补它自己：
// 逐文件幂等天然给出断点续传 —— 上一轮第 3 张剧照 404 了，这轮只重试那一张。
func TestGenerateJavArtifactsHealsMissingThumb(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	_ = generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{rel}, Items: allOn(), Images: newStubFetcher(), Log: testLogger(t),
	})
	// 删掉 thumb 与一张剧照
	for _, p := range []string{"thumb.jpg", filepath.Join("extrafanart", "fanart2.jpg")} {
		if err := os.Remove(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	fetcher := newStubFetcher()
	res := generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{rel}, Items: allOn(), Images: fetcher, Log: testLogger(t),
	})
	if res.Written != 2 {
		t.Errorf("应当只补 2 个文件，got %d", res.Written)
	}
	if fetcher.callCount() != 2 {
		t.Errorf("应当只打 2 次上游（thumb 1 + 剧照 1），got %d", fetcher.callCount())
	}
	// fanart 不该被重写（它还在），poster 也不该重裁
	if !artifactExists(filepath.Join(root, "fanart.jpg")) || !artifactExists(filepath.Join(root, "poster.jpg")) {
		t.Error("已存在的文件不该消失")
	}
}

// TestGenerateJavArtifactsRespectsToggles 关掉哪一项就不生成哪一项，其余照旧。
func TestGenerateJavArtifactsRespectsToggles(t *testing.T) {
	root, rel := newJavTaskDir(t, "SSIS-001-UC-4K.json")
	items := settings.DefaultJavMetaItems()
	items.NFO = false
	items.Preview = false

	generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{rel}, Items: items, Images: newStubFetcher(), Log: testLogger(t),
	})
	if artifactExists(filepath.Join(root, "SSIS-001-UC-4K.nfo")) {
		t.Error("关掉 nfo 之后不该生成 nfo")
	}
	if artifactExists(filepath.Join(root, "extrafanart")) {
		t.Error("关掉剧照之后不该建 extrafanart/")
	}
	for _, name := range []string{"thumb.jpg", "fanart.jpg", "poster.jpg"} {
		if !artifactExists(filepath.Join(root, name)) {
			t.Errorf("其余项照旧：缺少 %s", name)
		}
	}
}

// TestGenerateJavArtifactsUncensoredRunsFaceDetect 无码走人脸识别这条路（不崩、出图）。
//
// 有码那份取右侧的**纯函数**行为在 emby 包里穷举过了；这里只确认整条链
// 在无码分支上跑得通 —— 它比有码多一步级联检测。
func TestGenerateJavArtifactsUncensoredRunsFaceDetect(t *testing.T) {
	root := t.TempDir()
	uncensored := strings.Replace(sampleSidecar, `"type": "0"`, `"type": "1"`, 1)
	if err := os.WriteFile(filepath.Join(root, "MGL-0002.strm"), []byte("http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MGL-0002.json"), []byte(uncensored), 0o644); err != nil {
		t.Fatal(err)
	}
	res := generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{"MGL-0002.strm"}, Items: allOn(),
		Images: newStubFetcher(), Log: testLogger(t),
	})
	if res.Written == 0 || !artifactExists(filepath.Join(root, "poster.jpg")) {
		t.Fatal("无码那一路也要出 poster")
	}
}

// TestGenerateJavArtifactsKeepsStemCase nfo 的文件名必须与 .strm **逐字同名**（含大小写）。
//
// 这条是真机跑出来的：一开始用了小写化的配对键去命名，写出 `ssis-001.nfo`，
// 而 Emby 是按「视频主干同名」找 nfo 的 —— Windows 上大小写不敏感看不出问题，
// Linux（Docker 部署）上就是「nfo 明明在那儿却认不出来」，不报错。
func TestGenerateJavArtifactsKeepsStemCase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC-4K.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ssis-001-uc-4k.json"), []byte(sampleSidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{"SSIS-001-UC-4K.strm"}, Items: allOn(),
		Images: newStubFetcher(), Log: testLogger(t),
	})
	// 按**目录里的真实条目名**断言，不能用 os.Stat：Windows 的文件系统大小写不敏感，
	// `Stat("ssis-001-uc-4k.nfo")` 会命中 `SSIS-001-UC-4K.nfo`，这条断言就等于没写。
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name()] = true
	}
	if !found["SSIS-001-UC-4K.nfo"] {
		t.Errorf("nfo 必须与 .strm 同名（含大小写），目录里是：%v", found)
	}
	if found["ssis-001-uc-4k.nfo"] {
		t.Error("不该写出小写化的名字")
	}
}

// TestGenerateJavArtifactsNoSidecar 没有 json 是**正常状态**（绝大多数片还没推送过），
// 不记失败、不生成任何东西。
func TestGenerateJavArtifactsNoSidecar(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "NHDTA-001.strm"), []byte("http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failures := NewFailureCollector()
	res := generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{"NHDTA-001.strm"}, Items: allOn(),
		Images: newStubFetcher(), Failures: failures, Log: testLogger(t),
	})
	if res.Written != 0 || res.NoSidecar != 1 {
		t.Errorf("应当「没有侧车」记 1，got %+v", res)
	}
	if len(failures.Items()) != 0 {
		t.Errorf("没有侧车不该记失败：%+v", failures.Items())
	}
}

// TestGenerateJavArtifactsFlatLayoutSkipsExtrafanart 平铺目录不写 extrafanart/ 与裸名图片 ——
// 目录里几部片共用一套目录级名字必然互相覆盖。
func TestGenerateJavArtifactsFlatLayoutSkipsExtrafanart(t *testing.T) {
	root := t.TempDir()
	for _, stem := range []string{"SSIS-001", "SSIS-002"} {
		if err := os.WriteFile(filepath.Join(root, stem+".strm"), []byte("http://x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "SSIS-001.json"), []byte(sampleSidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	generateJavArtifacts(context.Background(), javArtifactRequest{
		Root: root, StrmFiles: []string{"SSIS-001.strm"}, Items: allOn(),
		Images: newStubFetcher(), Log: testLogger(t),
	})
	if artifactExists(filepath.Join(root, "extrafanart")) {
		t.Error("平铺目录不该写 extrafanart/")
	}
	for _, name := range []string{"SSIS-001-thumb.jpg", "SSIS-001-poster.jpg", "SSIS-001-fanart.jpg", "SSIS-001.nfo"} {
		if !artifactExists(filepath.Join(root, name)) {
			t.Errorf("平铺布局应当写 %s", name)
		}
	}
	if artifactExists(filepath.Join(root, "thumb.jpg")) {
		t.Error("平铺目录不该写裸名 thumb.jpg —— 那是整个目录的，会串味")
	}
}

// TestPairSidecar 三级配对判据，逐级放宽，任何一级都不猜。
func TestPairSidecar(t *testing.T) {
	sidecar := func(name string) localSidecar {
		return localSidecar{name: name, doc: &emby.SidecarDoc{Number: "X"}}
	}
	cases := []struct {
		label    string
		strms    []string
		scs      []localSidecar
		soleStrm bool // 该目录里只有一个 .strm
		wantHit  bool
	}{
		{"主干全等", []string{"SSIS-001-UC-4K.strm"}, []localSidecar{sidecar("SSIS-001-UC-4K.json")}, false, true},
		{"主干全等（大小写无关）", []string{"SSIS-001.strm"}, []localSidecar{sidecar("ssis-001.json")}, false, true},
		{"前缀相容（分片）", []string{"SSIS-001-UC-4K-cd1.strm"}, []localSidecar{sidecar("SSIS-001-UC-4K.json")}, false, true},
		{"整层唯一（种子目录那种乱名字）", []string{"manko.fun.strm"}, []localSidecar{sidecar("MOIL-001.json")}, true, true},
		{"两个候选一个侧车 → 不猜", []string{"A.strm", "B.strm"}, []localSidecar{sidecar("MOIL-001.json")}, false, false},
		{"一个候选两个侧车 → 不猜", []string{"manko.fun.strm"}, []localSidecar{sidecar("A.json"), sidecar("B.json")}, false, false},
		{"主干只是前缀但没断在分隔符 → 主干判据不配",
			[]string{"SSIS-0012.strm"}, []localSidecar{sidecar("SSIS-001.json")}, false, false},
		// ⚠️ 这条钉的是「整层唯一」数的是**目录里的 .strm 总数**而不是「本轮要处理的条数」：
		// 扫描那一路只把本轮新增/更新的传进来，若拿它当判据，一个新片会被配到同目录
		// 另一部老片的侧车上 —— 生成一份完全无关的 nfo 与封面，而且不报错。
		{"目录里有老片（本轮只处理一个新片）→ 不配",
			[]string{"SSIS-0012.strm"}, []localSidecar{sidecar("SSIS-001.json")}, false, false},
	}
	for _, c := range cases {
		got := pairSidecar(c.strms, c.scs, c.soleStrm)
		if c.wantHit != (len(got) > 0) {
			t.Errorf("%s：配对结果 %+v，期望命中=%v", c.label, got, c.wantHit)
		}
	}
}

// TestJavArtifactNamesIsWhatWeGenerate 守卫名单与生成端**同源**：拿生成出来的文件名
// 去问名单，必须全中 —— 各写一遍就会出现「生成 A、守卫认 B」，表现是文件删了又生成。
func TestJavArtifactNamesIsWhatWeGenerate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SSIS-001.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := javArtifactNames(entries)
	for _, want := range []string{"poster.jpg", "thumb.jpg", "fanart.jpg", "ssis-001.nfo", "ssis-001-poster.jpg"} {
		if _, ok := names[want]; !ok {
			t.Errorf("守卫名单里缺少 %s：%v", want, names)
		}
	}
	// 目录里没有 .strm 时是 nil（没有片子，孤儿图该被清理）
	if got := javArtifactNames(nil); got != nil {
		t.Errorf("没有 .strm 时应当返回 nil，got %v", got)
	}
}

// TestJavMetaExtensions 接口处那一颗钉：番号任务的元数据扩展名里必须有 json，
// 别的任务**一个都不能多**。
func TestJavMetaExtensions(t *testing.T) {
	base := map[string]struct{}{"nfo": {}, "jpg": {}}
	jav := javMetaExtensions("jav", map[string]struct{}{"nfo": {}})
	if _, ok := jav["json"]; !ok {
		t.Error("番号任务必须收 json —— 侧车是生成 nfo / 图片的唯一输入")
	}
	tmdb := javMetaExtensions("tmdb", map[string]struct{}{"nfo": {}})
	if _, ok := tmdb["json"]; ok {
		t.Error("非番号任务不该多收 json（会把网盘上无关的 json 拖进媒体库）")
	}
	// 空集合也要能兜住（调用方可能传 nil）
	if got := javMetaExtensions("jav", nil); len(got) != 1 {
		t.Errorf("nil 集合也该补上 json，got %v", got)
	}
	_ = base
}

func decodeJPEGFile(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解不开 %s：%v", path, err)
	}
	return img
}
