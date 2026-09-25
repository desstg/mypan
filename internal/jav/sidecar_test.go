package jav

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/jav/javbus"
	"litepan/internal/jav/quality"
	"litepan/internal/settings"
)

// ————————————————————— 桩 —————————————————————

// stubFolders 是 FolderStore 的可控实现。
type stubFolders struct {
	infoItem *domain.FileItem
	infoErr  error

	listItems []domain.FileItem
	listErr   error

	uploadErr  error
	uploadDone chan struct{}

	infoIDs   []string
	listCalls []string
	uploads   []driver.LocalUploadRequest
	// uploaded 是上传那一刻读到的临时文件内容，按文件名存。
	uploaded map[string][]byte
}

func (s *stubFolders) CreateFolder(_ context.Context, _ int64, parentID, name string) (*domain.FileItem, error) {
	return &domain.FileItem{ID: "new-" + name, Name: name, IsDir: true}, nil
}

func (s *stubFolders) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	s.listCalls = append(s.listCalls, parentID)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listItems, nil
}

func (s *stubFolders) Info(_ context.Context, _ int64, fileID string) (*domain.FileItem, error) {
	s.infoIDs = append(s.infoIDs, fileID)
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	return s.infoItem, nil
}

func (s *stubFolders) UploadLocal(_ context.Context, _ int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	s.uploads = append(s.uploads, req)
	// 临时文件在上传返回后才被 defer 删掉，这里读得到。
	if data, err := os.ReadFile(req.LocalPath); err == nil {
		if s.uploaded == nil {
			s.uploaded = map[string][]byte{}
		}
		s.uploaded[req.FileName] = data
	}
	if s.uploadDone != nil {
		close(s.uploadDone)
		s.uploadDone = nil
	}
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return &driver.LocalUploadResult{FileID: "f1", FileName: req.FileName}, nil
}

// ————————————————————— buildSidecar：字段 —————————————————————

// sampleMovie 是一部「抓过详情」的影片：有演员、有剧照、有片商。
func sampleMovie() *domain.JavMovie {
	return &domain.JavMovie{
		ID: "ZY5eq", Number: "SSIS-001", NumberLetter: "SSIS",
		Title: "样本标题", OriginTitle: "サンプル",
		CoverURL: "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg",
		ThumbURL: "https://tp.spfcas.com/rhe951l4q/small_covers/ve/vezpEn.jpg",
		Duration: 120, ReleaseDate: "2024-01-01",
		Score: 4.69, Summary: "剧情简介", Review: "简评",
		DirectorID: "d1", DirectorName: "导演甲",
		MakerID: "k1", MakerName: "Madonna",
		PublisherID: "p1", PublisherName: "Madonna",
		SeriesID: "s1", SeriesName: "系列甲",
		Tags:            []string{"巨乳", "单体作品"},
		PreviewImages:   []string{"https://tp.spfcas.com/rhe951l4q/samples/aq/a1.jpg", "https://tp.spfcas.com/rhe951l4q/samples/aq/a2.jpg"},
		PreviewVideoURL: "https://example.test/pv.mp4",
		MagnetsCount:    8, ReviewsCount: 12, HasCNSub: true,
		Type:      domain.JavTypeCensored,
		FetchedAt: time.Date(2026, 9, 23, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)),
	}
}

func sampleActors() []*domain.JavActor {
	return []*domain.JavActor{
		{ID: "a1", Name: "演员甲", Gender: 1, AvatarURL: "https://tp.spfcas.com/rhe951l4q/avatars/x.jpg"},
		{ID: "a2", Name: "演员乙", Gender: 1},
	}
}

func sampleRecord() *domain.JavPushRecord {
	return &domain.JavPushRecord{
		ID: 9, MovieID: "ZY5eq", Code: "SSIS-001",
		Name:     "SSIS-001-U 4K REMUX HEVC 中文字幕",
		SizeText: "31GB",
		Magnet:   "magnet:?xt=urn:btih:" + strings.Repeat("a", 40),
		Source:   domain.JavSourceComment,
	}
}

// 一份「什么都有」的输入：质量标记、演员、剧照、片商、落盘现场都齐。
func sampleInput() sidecarInput {
	return sidecarInput{
		Movie:    sampleMovie(),
		Actors:   sampleActors(),
		Record:   sampleRecord(),
		InfoHash: strings.Repeat("a", 40),
		SiteBase: "https://javdb.com/",
		Dest: sidecarDest{
			AccountID: 1, ParentID: "p-1", Path: "/番号/SSIS-001",
			AddedAt: time.Date(2026, 9, 24, 1, 2, 3, 0, time.FixedZone("CST", 8*3600)),
			Files: []*domain.FileItem{
				{ID: "f1", Name: "SSIS-001-4K.mp4", Size: 6979321856},
				// 目录不收 —— 侧车已经和视频同层，再往下的目录不属于这部片。
				{ID: "d1", Name: "字幕", IsDir: true},
			},
		},
		Now: time.Date(2026, 9, 24, 2, 0, 0, 0, time.FixedZone("CST", 8*3600)),
	}
}

func decodeSidecar(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("侧车不是合法 JSON: %v\n%s", err, data)
	}
	return out
}

// TestBuildSidecarQualityAndMetadata 钉住质量标记与各元数据字段。
//
// 全部判定都必须来自 quality 包（界面上那颗磁链显示 4K，侧车里就不能是别的）——
// 这里断言的正是「同一颗资源两处说法一致」。
func TestBuildSidecarQualityAndMetadata(t *testing.T) {
	data, _, err := buildSidecar(sampleInput())
	if err != nil {
		t.Fatalf("buildSidecar: %v", err)
	}
	doc := decodeSidecar(t, data)

	if got := doc["schema"]; got != sidecarSchema {
		t.Errorf("schema = %v，读取方靠它认版本", got)
	}
	if got := doc["number"]; got != "SSIS-001" {
		t.Errorf("number = %v", got)
	}
	if got := doc["number_letter"]; got != "SSIS" {
		t.Errorf("number_letter = %v", got)
	}
	// 基址尾部那个斜杠要被吃掉，否则拼出 //v/。
	if got := doc["javdb_url"]; got != "https://javdb.com/v/ZY5eq" {
		t.Errorf("javdb_url = %v", got)
	}
	if got := doc["type_label"]; got != "有码" {
		t.Errorf("type_label = %v", got)
	}
	// 量纲必须显式记：nfo 的 <rating> 是 10 分制、<criticrating> 是 ×20。
	if got := doc["score"]; got != 4.69 {
		t.Errorf("score = %v", got)
	}
	if got := doc["score_max"]; got != float64(5) {
		t.Errorf("score_max = %v —— 不记它，生成器只能猜量纲", got)
	}
	// 影片级中字与资源级字幕是两件事，各记一份。
	if got := doc["has_cnsub"]; got != true {
		t.Errorf("has_cnsub = %v", got)
	}
	// preview_video_url 是 nfo 的 <trailer>；简评界面上从没展示过，一并留档。
	if got := doc["preview_video_url"]; got != "https://example.test/pv.mp4" {
		t.Errorf("preview_video_url = %v", got)
	}
	if got := doc["review"]; got != "简评" {
		t.Errorf("review = %v —— 上游单独给的简评，漏记就等于丢弃", got)
	}

	q := doc["quality"].(map[string]any)
	if got := q["resolution"]; got != "4K" {
		t.Errorf("quality.resolution = %v", got)
	}
	if got := q["tier"]; got != "超清" {
		t.Errorf("quality.tier = %v", got)
	}
	for _, key := range []string{"four_k", "uhd", "hd", "uncensored", "subtitle"} {
		if got := q[key]; got != true {
			t.Errorf("quality.%s = %v，应当为 true", key, got)
		}
	}
	if got := q["edited"]; got != false {
		t.Errorf("quality.edited = %v，名字里没有剪辑标记", got)
	}
	if got := q["source"]; got != "REMUX" {
		t.Errorf("quality.source = %v", got)
	}
	if got := q["codec"]; got != "HEVC" {
		t.Errorf("quality.codec = %v", got)
	}
	if got := q["pack"]; got != false {
		t.Errorf("quality.pack = %v", got)
	}

	// 演员顺序与内容：nfo 的 <actor> 是一条条排下来的，顺序就是侧车里的顺序。
	actors := doc["actors"].([]any)
	if len(actors) != 2 {
		t.Fatalf("actors 应当有 2 条，got %d", len(actors))
	}
	first := actors[0].(map[string]any)
	if first["name"] != "演员甲" || first["id"] != "a1" || first["gender"] != float64(1) {
		t.Errorf("actors[0] = %v", first)
	}
	if first["avatar"] != "https://tp.spfcas.com/rhe951l4q/avatars/x.jpg" {
		t.Errorf("avatar 要留着当 nfo 的 <actor><thumb>，got %v", first["avatar"])
	}
	if actors[1].(map[string]any)["name"] != "演员乙" {
		t.Errorf("actors[1] = %v", actors[1])
	}

	images := doc["images"].(map[string]any)
	if got := images["cover"]; got != "https://tp.spfcas.com/rhe951l4q/covers/ve/vezpEn.jpg" {
		t.Errorf("images.cover = %v", got)
	}
	if got := len(images["previews"].([]any)); got != 2 {
		t.Errorf("previews 应当有 2 张，got %d", got)
	}
	// 取图说明要写进文件本身：拿到它的可能是外部脚本，看不到这段代码。
	note := images["note"].(string)
	if !strings.Contains(note, "/rhe951l4q/") || !strings.Contains(note, "异或") {
		t.Errorf("images.note 必须写清混淆的判据与解法，got %q", note)
	}

	// 片商与发行分开记：样本里同名，但只推 publisher 的生成器不能拿到空值。
	if doc["maker"].(map[string]any)["name"] != "Madonna" {
		t.Errorf("maker = %v", doc["maker"])
	}
	if doc["publisher"].(map[string]any)["name"] != "Madonna" {
		t.Errorf("publisher = %v", doc["publisher"])
	}
	if doc["series"].(map[string]any)["name"] != "系列甲" {
		t.Errorf("series = %v", doc["series"])
	}
	if doc["director"].(map[string]any)["name"] != "导演甲" {
		t.Errorf("director = %v", doc["director"])
	}
	if len(doc["tags"].([]any)) != 2 {
		t.Errorf("tags = %v", doc["tags"])
	}

	res := doc["resource"].(map[string]any)
	if res["info_hash"] != strings.Repeat("a", 40) {
		t.Errorf("resource.info_hash = %v", res["info_hash"])
	}
	// SizeBytes 由服务端解析一次，别让每个读取方各解析一遍。
	if res["size_bytes"] != float64(31*1024*1024*1024) {
		t.Errorf("resource.size_bytes = %v", res["size_bytes"])
	}
	if res["source"] != domain.JavSourceComment || res["from_comment"] != true {
		t.Errorf("评论分享来源没记对: %v / %v", res["source"], res["from_comment"])
	}

	dest := doc["dest"].(map[string]any)
	if dest["parent_id"] != "p-1" || dest["path"] != "/番号/SSIS-001" {
		t.Errorf("dest 落盘现场 = %v", dest)
	}
	if dest["added_at"] != "2026-09-24T01:02:03+08:00" {
		t.Errorf("dest.added_at = %v —— nfo 的 <dateadded> 靠它", dest["added_at"])
	}
	files := dest["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("dest.files 只该收文件，got %v", files)
	}
	only := files[0].(map[string]any)
	if only["name"] != "SSIS-001-4K.mp4" || only["size"] != float64(6979321856) {
		t.Errorf("dest.files[0] = %v", only)
	}
	if only["is_dir"] != false {
		t.Errorf("dest.files[0].is_dir = %v", only["is_dir"])
	}
}

// TestBuildSidecarResolutionFallbackBySize 钉住体积兜底那条既有行为：
// 名字里一个分辨率标记都没有（4K 片源常常只写番号）时，> 18GB 算超清。
// 而名字**明写** 1080p 时体积不说话 —— 31GB 的 1080p remux 不是 4K。
func TestBuildSidecarResolutionFallbackBySize(t *testing.T) {
	cases := []struct {
		name       string
		linkName   string
		sizeText   string
		resolution string
		tier       string
	}{
		{"名字没表态 + 超过门槛", "SSIS-001", "20GB", "4K", "超清"},
		{"名字没表态 + 没到门槛", "SSIS-001", "10GB", "", ""},
		{"名字明写 1080p", "SSIS-001 1080p REMUX", "31GB", "HD", "高清"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := sampleInput()
			in.Record.Name = c.linkName
			in.Record.SizeText = c.sizeText
			data, _, err := buildSidecar(in)
			if err != nil {
				t.Fatalf("buildSidecar: %v", err)
			}
			q := decodeSidecar(t, data)["quality"].(map[string]any)
			if q["resolution"] != c.resolution {
				t.Errorf("resolution = %v，want %q", q["resolution"], c.resolution)
			}
			// 档位与角标同源：角标说 4K 而档位是空，外部读取方会当成两个事实。
			if q["tier"] != c.tier {
				t.Errorf("tier = %v，want %q", q["tier"], c.tier)
			}
		})
	}
}

// TestBuildSidecarEmptyInputs 空输入不 panic，且空集合写成 [] 而不是 null。
//
// null 在 Go 里能反序列化成空切片，**在别的语言里不是** —— python 拿到 None
// 之后 `for x in items` 当场炸。侧车是要出这个应用的文件，按最保守的形状写。
func TestBuildSidecarEmptyInputs(t *testing.T) {
	data, _, err := buildSidecar(sidecarInput{Now: time.Now()})
	if err != nil {
		t.Fatalf("buildSidecar: %v", err)
	}
	doc := decodeSidecar(t, data)

	for _, key := range []string{"actors", "tags"} {
		v, ok := doc[key]
		if !ok {
			t.Fatalf("%s 字段必须存在", key)
		}
		items, isSlice := v.([]any)
		if !isSlice {
			t.Errorf("%s 应当是数组，got %T（null 会被别的语言当成 None）", key, v)
			continue
		}
		if len(items) != 0 {
			t.Errorf("%s 应当为空数组，got %v", key, items)
		}
	}
	images := doc["images"].(map[string]any)
	if items, ok := images["previews"].([]any); !ok || len(items) != 0 {
		t.Errorf("images.previews 应当是空数组，got %v", images["previews"])
	}
	dest := doc["dest"].(map[string]any)
	if items, ok := dest["files"].([]any); !ok || len(items) != 0 {
		t.Errorf("dest.files 应当是空数组，got %v", dest["files"])
	}
	// 番号为空时可以没有文件名，但字段本身要在。零值时间一律写空串 ——
	// 「0001-01-01」会被读取方当成一个真实（且荒谬）的时间。
	if doc["number"] != "" || doc["fetched_at"] != "" || dest["added_at"] != "" {
		t.Errorf("空输入下 number/fetched_at/added_at 都该是空串: %v / %v / %v",
			doc["number"], doc["fetched_at"], dest["added_at"])
	}
	if doc["score_max"] != float64(5) {
		t.Errorf("score_max 是常量，空输入下也必须有：%v", doc["score_max"])
	}
	// 记录为空时质量判定一律按「名字是空串」走，不该 panic 也不该全 true。
	q := doc["quality"].(map[string]any)
	if q["resolution"] != "" || q["four_k"] != false || q["uncensored"] != false {
		t.Errorf("空输入下的质量标记 = %v", q)
	}
}

// TestSidecarFileName 侧车文件名带上质量后缀：`<番号>[-U|-C|-UC][-4K].json`。
//
// 这些后缀是给**目录整理**读的：整理靠文件名把番号与标记取回去，不必再把 json
// 从网盘读下来（一次计划几十个作品，逐个下载是几十次网络往返，而且每次都可能失败）。
func TestSidecarFileName(t *testing.T) {
	cases := []struct {
		name   string
		number string
		marks  quality.Marks
		want   string
	}{
		{"什么标记都没有", "SSIS-001", quality.Marks{}, "SSIS-001.json"},
		{"中字", "SSIS-001", quality.Marks{Subtitle: true}, "SSIS-001-C.json"},
		{"破解", "SSIS-001", quality.Marks{Uncensored: true}, "SSIS-001-U.json"},
		{"破解 + 中字", "SSIS-001", quality.Marks{Uncensored: true, Subtitle: true}, "SSIS-001-UC.json"},
		{"只有 4K", "SSIS-001", quality.Marks{FourK: true}, "SSIS-001-4K.json"},
		{"破解 + 4K", "SSIS-001", quality.Marks{Uncensored: true, FourK: true}, "SSIS-001-U-4K.json"},
		{"三样都占", "SSIS-444", quality.Marks{Uncensored: true, Subtitle: true, FourK: true},
			"SSIS-444-UC-4K.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sidecarFileName(c.number, c.marks)
			if got != c.want {
				t.Errorf("sidecarFileName = %q，want %q", got, c.want)
			}
			// 名字必须能被整理那边**拆回来** —— 这是整套做法成立的前提。
			number, marks, ok := quality.ParseJavFileName(strings.TrimSuffix(got, ".json"))
			if !ok {
				t.Fatalf("自己写出的名字自己拆不回来：%q", got)
			}
			if number != c.number {
				t.Errorf("拆回的番号 = %q，want %q", number, c.number)
			}
			if marks != c.marks {
				t.Errorf("拆回的标记 = %+v，want %+v", marks, c.marks)
			}
		})
	}

	// 番号里带斜杠的写法存在（FC2 那类），而斜杠会让「写一个文件」变成「写一串目录」。
	if got := sidecarFileName("FC2/PPV 123", quality.Marks{}); strings.ContainsAny(got, `/\`) {
		t.Errorf("消毒之后仍含路径分隔符: %q", got)
	}
	if got := sidecarFileName("   ", quality.Marks{}); got != "" {
		t.Errorf("空番号应当取不出文件名，got %q", got)
	}
}

// ————————————————————— 写入：定层 —————————————————————

// TestWriteSidecarIntoSeedFolder 多文件种子那层目录：115 会另建一层以种子名
// 命名的目录，那时 FileID 是目录、TargetParentID 是它的上一层。
// 侧车必须进**种子那层**，否则将来按同层视频主名配 nfo 就配不上。
func TestWriteSidecarIntoSeedFolder(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 1080p", "5GB")})

	folders := &stubFolders{
		infoItem: &domain.FileItem{ID: "seed-dir", Name: "SSIS-001-4K", IsDir: true},
		listItems: []domain.FileItem{
			{ID: "v1", Name: "SSIS-001-4K.mp4", Size: 123},
			{ID: "d1", Name: "字幕", IsDir: true},
		},
	}
	f.svc.folders = folders

	rec := sampleRecord()
	rec.MovieID = "m1"
	err := f.svc.writeSidecar(ctx, rec, strings.Repeat("a", 40), offlineCompletedEvent{
		AccountID: 7, TargetParentID: "parent-1", FileID: "file-1",
		DisplayPath: "/番号/SSIS-001",
	})
	if err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}

	if len(folders.listCalls) != 1 || folders.listCalls[0] != "seed-dir" {
		t.Errorf("应当只在种子那层列一次目录，got %v", folders.listCalls)
	}
	if len(folders.uploads) != 1 {
		t.Fatalf("应当只上传一次，got %d", len(folders.uploads))
	}
	up := folders.uploads[0]
	if up.ParentID != "seed-dir" {
		t.Errorf("侧车必须落在种子那层目录，got parent=%q", up.ParentID)
	}
	// 重推同一部片时侧车要被覆盖，不能攒出「SSIS-001 (1).json」。
	if up.ConflictPolicy != "overwrite" {
		t.Errorf("ConflictPolicy = %q，重推时会攒出副本", up.ConflictPolicy)
	}
	// 样品那颗磁链名是「SSIS-001-U 4K REMUX HEVC 中文字幕」→ 破解 + 中字 + 4K，
	// 于是文件名带上对应的后缀 —— 目录整理就是靠读它把番号与标记取回去的。
	if up.FileName != "SSIS-001-UC-4K.json" {
		t.Errorf("FileName = %q", up.FileName)
	}

	doc := decodeSidecar(t, folders.uploaded[up.FileName])
	dest := doc["dest"].(map[string]any)
	if dest["parent_id"] != "seed-dir" {
		t.Errorf("dest.parent_id = %v，应当与上传落点一致", dest["parent_id"])
	}
	// 显示路径要带上种子那层 —— 否则记录里看到的路径指不到文件真正在的地方。
	if dest["path"] != "/番号/SSIS-001/SSIS-001-4K" {
		t.Errorf("dest.path = %v", dest["path"])
	}
	files := dest["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["name"] != "SSIS-001-4K.mp4" {
		t.Errorf("dest.files 只收文件、名字要与网盘上逐字一致: %v", files)
	}
	// 演员是从库里查出来的：这部片没抓过详情，所以是空数组（已知天花板）。
	if actors, ok := doc["actors"].([]any); !ok || len(actors) != 0 {
		t.Errorf("没抓过详情的影片应当得到空演员数组，got %v", doc["actors"])
	}
}

// TestWriteSidecarFallsBackToTargetParent 两种情况都走 TargetParentID：
//   - Info 报错（网盘抖了一下、或条目已被删）；
//   - Info 拿到的是**文件**而不是目录 —— 内置下载器交棒那条路文件直接落在
//     TargetPath，走的正是这一支。
func TestWriteSidecarFallsBackToTargetParent(t *testing.T) {
	cases := []struct {
		name   string
		info   *domain.FileItem
		infoEr error
	}{
		{"Info 报错", nil, errors.New("网盘超时")},
		{"Info 拿到文件", &domain.FileItem{ID: "f9", Name: "SSIS-001.mp4"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCatalogFixture(t)
			seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
			folders := &stubFolders{infoItem: c.info, infoErr: c.infoEr}
			f.svc.folders = folders

			rec := sampleRecord()
			rec.MovieID = "m1"
			err := f.svc.writeSidecar(context.Background(), rec, "", offlineCompletedEvent{
				AccountID: 7, TargetParentID: "parent-1", FileID: "file-1",
				DisplayPath: "/番号",
			})
			if err != nil {
				t.Fatalf("writeSidecar: %v", err)
			}
			if len(folders.uploads) != 1 {
				t.Fatalf("应当照常上传，got %d 次", len(folders.uploads))
			}
			if folders.uploads[0].ParentID != "parent-1" {
				t.Errorf("应当回落到 TargetParentID，got %q", folders.uploads[0].ParentID)
			}
			if folders.listCalls[0] != "parent-1" {
				t.Errorf("列目录也应当在回落后那一层，got %v", folders.listCalls)
			}
			// 没有子目录可加，显示路径保持原样。
			doc := decodeSidecar(t, folders.uploaded["SSIS-001-UC-4K.json"])
			if doc["dest"].(map[string]any)["path"] != "/番号" {
				t.Errorf("dest.path = %v", doc["dest"].(map[string]any)["path"])
			}
		})
	}
}

// TestWriteSidecarListFailureStillWrites 列目录失败不该丢掉整份侧车。
//
// dest.files 缺了，但番号/演员/画质/封面地址仍然是真的 —— 而重抓一遍上游的
// 代价（JAVDB 会封号）远高于这一份信息不全的文件。
func TestWriteSidecarListFailureStillWrites(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	folders := &stubFolders{listErr: errors.New("列目录失败")}
	f.svc.folders = folders

	rec := sampleRecord()
	rec.MovieID = "m1"
	if err := f.svc.writeSidecar(context.Background(), rec, "", offlineCompletedEvent{
		AccountID: 7, TargetParentID: "parent-1", DisplayPath: "/番号",
	}); err != nil {
		t.Fatalf("列目录失败不该让整份侧车写不出去: %v", err)
	}
	if len(folders.uploads) != 1 {
		t.Fatalf("应当照常上传，got %d 次", len(folders.uploads))
	}
	doc := decodeSidecar(t, folders.uploaded["SSIS-001-UC-4K.json"])
	if files, ok := doc["dest"].(map[string]any)["files"].([]any); !ok || len(files) != 0 {
		t.Errorf("列目录失败时 files 应当是空数组，got %v", files)
	}
	if doc["number"] != "SSIS-001" {
		t.Errorf("番号仍然要在，got %v", doc["number"])
	}
}

// TestWriteSidecarUploadFailureSurfaces 上传失败要**报错**（由调用方记 warn），
// 而不是静默吞掉 —— 否则「侧车没写出来」这件事没有任何线索。
func TestWriteSidecarUploadFailureSurfaces(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovie(t, f, "m1", "SSIS-001", "甲", nil)
	folders := &stubFolders{uploadErr: errors.New("网盘拒绝")}
	f.svc.folders = folders

	rec := sampleRecord()
	rec.MovieID = "m1"
	err := f.svc.writeSidecar(context.Background(), rec, "", offlineCompletedEvent{
		AccountID: 7, TargetParentID: "parent-1",
	})
	if err == nil {
		t.Fatal("上传失败必须报错")
	}
	if !strings.Contains(err.Error(), "网盘拒绝") {
		t.Errorf("原始错误要带出来，got %v", err)
	}
}

// TestWriteSidecarWithoutNumberSkips 取不出番号时不写 —— 没有文件名就没有
// 「将来按名字找回来」这条路，写出去只是一个网盘里的垃圾文件。
func TestWriteSidecarWithoutNumberSkips(t *testing.T) {
	f := newCatalogFixture(t)
	folders := &stubFolders{}
	f.svc.folders = folders

	// 记录里也没有番号。
	rec := &domain.JavPushRecord{ID: 1, Name: "没有番号的磁链名", SizeText: "5GB"}
	if err := f.svc.writeSidecar(context.Background(), rec, "", offlineCompletedEvent{
		AccountID: 7, TargetParentID: "parent-1",
	}); err != nil {
		t.Fatalf("取不出番号不该报错，只该跳过: %v", err)
	}
	if len(folders.uploads) != 0 {
		t.Errorf("不该上传，got %v", folders.uploads)
	}
}

// ————————————————————— 挂载：异步与失败隔离 —————————————————————

// TestSidecarSpawnIsNoopWithoutFolders Folders 为 nil 时静默早退 ——
// 既有那批 onOfflineDownloadCompleted 测试（桩里没有 folders）全靠这条。
func TestSidecarSpawnIsNoopWithoutFolders(t *testing.T) {
	f := newCatalogFixture(t)
	rec := sampleRecord()
	// 不 panic、不阻塞、不写任何东西。
	f.svc.spawnSidecarWrite(rec, nil, offlineCompletedEvent{AccountID: 7})
}

// TestOfflineCompletedWritesSidecarAndKeepsStatusOnFailure 端到端：
// 完成事件 → 侧车异步写出；**上传失败不改推送记录**。
//
// 这条守着「侧车是锦上添花」这个定位：一次上传抖动不能把「已推送」翻掉，
// 也不该发通知打扰用户。
func TestOfflineCompletedWritesSidecarAndKeepsStatusOnFailure(t *testing.T) {
	f, _ := fixtureWithPush(t)
	ctx := context.Background()

	seedMovie(t, f, "m1", "SSIS-001", "甲",
		[]javbus.Magnet{magnet(strings.Repeat("a", 40), "SSIS-001 2160p 8GB", "8GB")})
	view := seedSubWithCandidate(t, f, SubscriptionInput{
		TargetType: "movie", TargetID: "m1", TargetName: "SSIS-001",
		DownloadMode: "strict", Enabled: true, TargetAccountID: 7,
	})
	if _, err := f.svc.AutoPush(ctx, view.ID, false); err != nil {
		t.Fatalf("AutoPush: %v", err)
	}

	// 上传必然失败，且用一个 channel 通知测试「它已经跑过了」——
	// 侧车是异步写的，不这样等就变成在赛跑。
	folders := &stubFolders{
		uploadErr:  errors.New("网盘拒绝"),
		uploadDone: make(chan struct{}),
	}
	f.svc.folders = folders
	// 开关默认开（registry 里的 Default 是 true），这里钉一下。
	if !f.svc.sidecarEnabled() {
		t.Fatal("侧车开关默认应当是开的")
	}

	f.svc.onOfflineDownloadCompleted(ctx, eventbus.OfflineDownloadCompleted{
		TaskID: "task-1", AccountID: 7, TargetParentID: "parent-1",
		FileID: "file-1", TargetDisplayPath: "/番号/SSIS-001",
	})

	select {
	case <-folders.uploadDone:
	case <-time.After(5 * time.Second):
		t.Fatal("侧车写入没有跑起来")
	}

	if len(folders.uploads) != 1 {
		t.Fatalf("应当尝试上传一次，got %d", len(folders.uploads))
	}
	// 这条用的是 seedMovie 里那颗「SSIS-001 2160p 8GB」—— 只有 4K，没有破解与中字。
	if folders.uploads[0].FileName != "SSIS-001-4K.json" {
		t.Errorf("FileName = %q", folders.uploads[0].FileName)
	}

	records, _, err := f.svc.PushRecords(ctx, domain.JavPushRecordFilter{})
	if err != nil {
		t.Fatalf("PushRecords: %v", err)
	}
	if records[0].Status != domain.JavPushPushed {
		t.Errorf("侧车写失败不该改动推送记录，got %q", records[0].Status)
	}
}

// TestSidecarDisabledBySetting 关掉开关后连尝试都不该有。
func TestSidecarDisabledBySetting(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()

	if err := f.set.Update(ctx, map[string]string{
		settings.KeyJavSidecarEnabled: "false",
	}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	if f.svc.sidecarEnabled() {
		t.Fatal("关掉之后应当报告为关")
	}
}

// TestSidecarSubtitleFromUpstreamBadge 上游角标要参与命名 —— **名字里看不出来的那种**。
//
// 实测 MRSS-104 推的那颗叫 `mrss-104ch`：名字里没有任何中字标记（reChinese 要求
// `-` 或 `_` 打头，而这里的 `ch` 紧跟在数字 `4` 后面），但它在磁链表上 has_sub=1，
// 界面上的「字幕」角标就是用它算的。侧车若只按名字猜就会写成 `MRSS-104.json` ——
// 于是卡片上写着字幕、文件名里没有，而 Emby 会把那个文件当成没有字幕的版本。
func TestSidecarSubtitleFromUpstreamBadge(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovie(t, f, "m1", "MRSS-104", "甲", nil)

	// 40 位 btih；库里大小写不统一，这里特意存大写、按小写查。
	btih := "68558249de4125069ed4d73b277fa81eb0d12345"
	rec := &domain.JavPushRecord{
		ID: 1, MovieID: "m1", Code: "MRSS-104",
		Name: "mrss-104ch", SizeText: "5.5 GB",
		Magnet: "magnet:?xt=urn:btih:" + strings.ToUpper(btih),
	}
	if _, err := f.st.JavMagnets.Upsert(ctx, &domain.JavMagnet{
		Fingerprint: btih, Btih: btih, MovieID: "m1", Code: "MRSS-104",
		Name: "mrss-104ch", SizeText: "5.5 GB", Magnet: rec.Magnet,
		HasHD: true, HasSub: true,
	}); err != nil {
		t.Fatalf("seed magnet: %v", err)
	}

	folders := &stubFolders{}
	f.svc.folders = folders
	if err := f.svc.writeSidecar(ctx, rec, btih, offlineCompletedEvent{
		AccountID: 4, TargetParentID: "p1",
	}); err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}
	if len(folders.uploads) != 1 {
		t.Fatalf("应当上传一次，got %d", len(folders.uploads))
	}
	if got := folders.uploads[0].FileName; got != "MRSS-104-C.json" {
		t.Errorf("文件名 = %q，want MRSS-104-C.json（上游 has_sub=1 必须体现在名字里）", got)
	}
	doc := decodeSidecar(t, folders.uploaded["MRSS-104-C.json"])
	if q := doc["quality"].(map[string]any); q["subtitle"] != true {
		t.Errorf("quality.subtitle 应当为 true，got %v", q["subtitle"])
	}
}

// TestSidecarNoMagnetRowMeansNoBadge 磁链表里查不到就按「没有角标」算，不报错。
//
// 评论区分享来的链接常常不在磁链表里，那种情况界面上也不会有角标 ——
// 两边一致。宁可少标一个后缀，也不要标一个资源本身没有的标记。
func TestSidecarNoMagnetRowMeansNoBadge(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := context.Background()
	seedMovie(t, f, "m1", "MRSS-104", "甲", nil)

	rec := &domain.JavPushRecord{
		ID: 2, MovieID: "m1", Code: "MRSS-104",
		Name: "mrss-104ch", SizeText: "5.5 GB",
		Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("f", 40),
	}
	folders := &stubFolders{}
	f.svc.folders = folders
	if err := f.svc.writeSidecar(ctx, rec, strings.Repeat("f", 40), offlineCompletedEvent{
		AccountID: 4, TargetParentID: "p1",
	}); err != nil {
		t.Fatalf("查不到磁链不该报错: %v", err)
	}
	if got := folders.uploads[0].FileName; got != "MRSS-104.json" {
		t.Errorf("文件名 = %q，want MRSS-104.json（没有角标就不该有 -C）", got)
	}
}

// TestWriteSidecarFileIDEqualsParent 单文件任务：115 返回的 file_id 就是目标目录自己。
//
// 实测 ABF-179：`file_id` 与 `target_parent_id` 是同一个值（都是 3414031719816154922），
// 视频直接落在 `/CMS影库/冗余`。定层逻辑若只看「FileID 指向目录就下沉」，
// 会把目标目录当成新建的种子目录，dest.path 记成 `/CMS影库/冗余/冗余` ——
// 多一层，而文件根本不在那儿。
func TestWriteSidecarFileIDEqualsParent(t *testing.T) {
	f := newCatalogFixture(t)
	seedMovie(t, f, "m1", "ABF-179", "甲", nil)

	folders := &stubFolders{
		// Info 返回的就是目标目录自己。
		infoItem: &domain.FileItem{ID: "parent-1", Name: "冗余", IsDir: true},
		listItems: []domain.FileItem{
			{ID: "v1", Name: "www.98T.la@ABF-179@x.mp4", Size: 33866835383},
		},
	}
	f.svc.folders = folders

	rec := sampleRecord()
	rec.MovieID = "m1"
	rec.Code = "ABF-179"
	rec.Name = "4K破解115网盘"
	if err := f.svc.writeSidecar(context.Background(), rec, "", offlineCompletedEvent{
		AccountID: 4, TargetParentID: "parent-1", FileID: "parent-1",
		DisplayPath: "/CMS影库/冗余",
	}); err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}
	if len(folders.uploads) != 1 {
		t.Fatalf("应当上传一次，got %d", len(folders.uploads))
	}
	// 落点仍是目标目录本身（没被「下沉」到别的目录）。
	if got := folders.uploads[0].ParentID; got != "parent-1" {
		t.Errorf("落点 = %q，want parent-1", got)
	}
	doc := decodeSidecar(t, folders.uploaded[folders.uploads[0].FileName])
	dest := doc["dest"].(map[string]any)
	if dest["path"] != "/CMS影库/冗余" {
		t.Errorf("dest.path = %v，多拼了一层（应当是 /CMS影库/冗余）", dest["path"])
	}
	if dest["parent_id"] != "parent-1" {
		t.Errorf("dest.parent_id = %v", dest["parent_id"])
	}
	// 顺手钉住这次的质量标记来源：磁链名是分享者敲的广告词。
	q := doc["quality"].(map[string]any)
	if q["four_k"] != true || q["uncensored"] != true {
		t.Errorf("「4K破解115网盘」应当认出 4K + 破解，got %v", q)
	}
	if q["subtitle"] != false {
		t.Errorf("这个名字里没有中字标记，subtitle 应当为 false，got %v", q["subtitle"])
	}
}
