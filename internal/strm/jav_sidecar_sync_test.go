package strm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
)

// ————————————————————— 侧车字段回写（2026-10-04）—————————————————————
//
// 这一组补的是「库里补齐了、侧车与 nfo 却还是空的」那个断点：
//
//   推送那一刻写侧车（只读本地库，不抓上游）→ 侧车 actors: []（永远补不上）
//   → nfo 从**本地那份 json** 生成 → nfo 里也永远没有 <actor>
//
// 回写这条路把库里**已经有**的元数据补回本地 json，并顺手补上 nfo 里**缺的元素**。

// sidecarFixture 造一份**生成器写的**侧车与同目录的 nfo。
//
// nfo 用真生成器写（`emby.BuildNFO`），不是手拼的字符串 —— 补缺那条路要求
// 「补出来的元素与生成器逐字节一致」，夹具就必须是生成器产出的那一份。
func sidecarFixture(t *testing.T, dir, stem string, doc *emby.SidecarDoc) {
	t.Helper()
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stem+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	names := emby.TargetNames(stem, false)
	body, err := emby.BuildNFO(doc, emby.NFOOptions{Names: names})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, names.NFO), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWriteFieldsIntoFileFillsOnlyEmpty 回写的**核心判据**：只补空。
//
// 侧车是别的工具也可能读、用户也可能手改的文件 —— 已经有值的一律不动。
// 反过来说，空的那几个（null / 空串 / 空数组 / 0 / {"id":"","name":""}）要能被补上。
func TestWriteFieldsIntoFileFillsOnlyEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "A.json")
	body := `{
  "schema": "litepan.jav.sidecar/1",
  "number": "SSIS-001",
  "summary": "已有的简介",
  "duration": 0,
  "tags": [],
  "actors": [],
  "director": {"id": "", "name": ""},
  "images": {"cover": "", "thumb": "已有的缩略图"}
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := writeFieldsIntoFile(path, map[string]any{
		"summary":       "不该覆盖",
		"duration":      120,
		"tags":          []string{"标签"},
		"actors":        []map[string]any{{"name": "演员", "gender": 0}},
		"director.name": "导演",
		"director.id":   "d1",
		"images.cover":  "https://example.com/c.jpg",
		"images.thumb":  "不该覆盖",
		"reviews_count": 12,
	})
	if err != nil {
		t.Fatalf("writeFieldsIntoFile: %v", err)
	}
	if !changed {
		t.Fatal("有该补的字段，应当报告改了")
	}

	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// ① 已有的值一个都没被动
	if got, _ := doc["summary"].(string); got != "已有的简介" {
		t.Errorf("summary 被覆盖了：%q", got)
	}
	images, _ := doc["images"].(map[string]any)
	if thumb, _ := images["thumb"].(string); thumb != "已有的缩略图" {
		t.Errorf("images.thumb 被覆盖了：%q", thumb)
	}
	// ② 空的那几个补上了
	if got, _ := doc["duration"].(float64); got != 120 {
		t.Errorf("duration = %v", doc["duration"])
	}
	if tags, _ := doc["tags"].([]any); len(tags) != 1 {
		t.Errorf("tags = %v", doc["tags"])
	}
	if actors, _ := doc["actors"].([]any); len(actors) != 1 {
		t.Errorf("actors = %v", doc["actors"])
	}
	if got, _ := images["cover"].(string); got != "https://example.com/c.jpg" {
		t.Errorf("images.cover = %v", images["cover"])
	}
	director, _ := doc["director"].(map[string]any)
	if got, _ := director["name"].(string); got != "导演" {
		t.Errorf("director.name = %v", director["name"])
	}
	if got, _ := director["id"].(string); got != "d1" {
		t.Errorf("director.id = %v", director["id"])
	}

	// ③ 幂等：再来一次不该有任何改动（免得白刷 mtime 触发下游重算）
	again, err := writeFieldsIntoFile(path, map[string]any{
		"duration": 120, "tags": []string{"标签"}, "director.name": "导演",
	})
	if err != nil || again {
		t.Errorf("第二次不该再改：changed=%v err=%v", again, err)
	}
}

// TestWriteFieldsIntoFileKeepsUnknownFields 其余字段一个都不能丢。
//
// 侧车里记着磁链指纹、落盘现场、画质档位这些**与库无关**的东西 —— 用 map 解析
// 而不是读侧车结构体，就是为了它们。
func TestWriteFieldsIntoFileKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "A.json")
	body := `{
  "schema": "litepan.jav.sidecar/1", "number": "SSIS-001",
  "resource": {"magnet": "magnet:?xt=urn:btih:aaa", "info_hash": "aaa"},
  "dest": {"path": "/CMS影库/冗余", "files": [{"name": "a.mp4", "size": 1}]},
  "quality": {"four_k": true, "tier": "超清"}
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFieldsIntoFile(path, map[string]any{"summary": "补的简介"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"resource", "dest", "quality", "schema", "number"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("写回之后丢了字段 %q", key)
		}
	}
	if got, _ := doc["dest"].(map[string]any)["path"].(string); got != "/CMS影库/冗余" {
		t.Errorf("dest.path 被改了：%q", got)
	}
}

// TestFillNFOFieldsIfMissingOnlyAddsMissingElements 补 nfo 的判据：
// **元素不存在**才补，元素在（哪怕是空的）一律不动。
//
// 这条区分是「绝不覆盖用户的手改」的全部依据 —— 编辑器保存过的 nfo 一定带元素。
func TestFillNFOFieldsIfMissingOnlyAddsMissingElements(t *testing.T) {
	dir := t.TempDir()
	stem := "SSIS-001-U"

	// ① 生成器写的（侧车里没有演员/导演/时长/评分）→ 补上
	sidecarFixture(t, dir, stem, &emby.SidecarDoc{
		Schema: emby.SupportedSchema, Number: "SSIS-001", NumberLetter: "SSIS",
		Title: "标题", Type: "0",
	})
	sidecarPath := filepath.Join(dir, stem+".json")

	raw, _ := os.ReadFile(sidecarPath)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// 灌进「库里补齐了」的那几个字段，交给回写那条路去写
	doc["actors"] = []any{map[string]any{"id": "a1", "name": "演员甲", "gender": float64(0)}}
	doc["director"] = map[string]any{"id": "d1", "name": "导演甲"}
	doc["duration"] = float64(120)
	doc["score"] = float64(4.5)
	doc["score_max"] = float64(5)
	doc["tags"] = []any{"标签一"}
	if _, err := writeFieldsIntoFile(sidecarPath, doc); err != nil {
		t.Fatal(err)
	}

	nfoPath := filepath.Join(dir, stem+".nfo")
	got, _ := os.ReadFile(nfoPath)
	for _, want := range []string{"<actor>", "演员甲", "<director>导演甲</director>", "<runtime>120</runtime>", "<ratings>"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("nfo 里应当出现 %q：\n%s", want, got)
		}
	}

	// ② 已经有 <actor> 元素的 nfo（用户在编辑器里保存过的）→ 一个字都不动
	edited := string(got)
	if _, err := writeFieldsIntoFile(sidecarPath, map[string]any{
		"actors":   []any{map[string]any{"id": "a9", "name": "不该被写进去", "gender": float64(0)}},
		"director": map[string]any{"name": "不该被覆盖"},
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(nfoPath)
	if string(after) != edited {
		t.Errorf("已经有 <actor>/<director> 的 nfo 不该被动：\n前\n%s\n后\n%s", edited, after)
	}
}

// TestFillNFOFieldsIfMissingKeepsEditedGenres 用户删过 genre 的 nfo，补缺时不能把
// 合成项加回来。
//
// 判据：nfo 里**已经有** `<genre>` 就整块不动 —— 重算会把用户删掉的
// 「片商: X」「系列: Y」这些加回去，而 Emby 那边只表现为「标签怎么又回来了」。
func TestFillNFOFieldsIfMissingKeepsEditedGenres(t *testing.T) {
	dir := t.TempDir()
	stem := "SSIS-002-U"
	sidecarFixture(t, dir, stem, &emby.SidecarDoc{
		Schema: emby.SupportedSchema, Number: "SSIS-002", NumberLetter: "SSIS",
		Title: "标题", Type: "0",
	})

	nfoPath := filepath.Join(dir, stem+".nfo")
	// 模拟用户在编辑器里删得只剩一个标签（元素还在 → 那是他的编辑）
	edited := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <num>SSIS-002</num>\n  <genre>只留这一个</genre>\n  <title>标题</title>\n</movie>\n"
	if err := os.WriteFile(nfoPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// 侧车里补上了片商与演员 —— 但 nfo 里已经有 genre，一个都不该动
	if _, err := writeFieldsIntoFile(filepath.Join(dir, stem+".json"), map[string]any{
		"maker":  map[string]any{"name": "片商甲"},
		"actors": []any{map[string]any{"name": "演员乙", "gender": float64(0)}},
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(nfoPath)
	if strings.Contains(string(after), "片商: 片商甲") {
		t.Errorf("用户删过 genre 的 nfo 不该被重算：\n%s", after)
	}
	if !strings.Contains(string(after), "只留这一个") {
		t.Errorf("用户留下的那个标签不该丢：\n%s", after)
	}
}

// TestSyncSidecarsFromRepoWalksAndReports 整批遍历：扫到侧车、按番号回写、报数。
//
// 这是**存量补齐的主路** —— 那批「推送时库里还没演员」的片子不会自己再走补缺链，
// 只能靠这条遍历把它们捞出来。
func TestSyncSidecarsFromRepoWalksAndReports(t *testing.T) {
	// 任务输出目录 = strmDir + TaskRelDir(GroupDir, OutputFolder)。这里用一个
	// 名为 TASK 的输出文件夹把它钉住 —— TaskRelDir 对空串会兜底成 `_`，
	// 拿不到「就是根目录」这种形状。
	root := t.TempDir()
	taskDir := filepath.Join(root, "TASK")
	sub := filepath.Join(taskDir, "A")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, number string) {
		t.Helper()
		body := `{"schema":"litepan.jav.sidecar/1","number":"` + number + `","actors":[]}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(taskDir, "SSIS-001-U.json", "SSIS-001")
	write(sub, "SSIS-002.json", "SSIS-002")
	// 一个**不是侧车**的 json（网盘上顺带同步下来的配置）：不该被扫进来
	if err := os.WriteFile(filepath.Join(taskDir, "config.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &Service{strmDir: root, repo: stubStrmTaskRepo{tasks: []*domain.StrmTask{
		{ID: 1, MediaKind: domain.StrmMediaKindJav, OutputFolder: "TASK"},
	}}, log: testLogger(t)}

	asked := []string{}
	res, err := svc.SyncSidecarsFromRepo(context.Background(), func(number string) (map[string]any, bool) {
		asked = append(asked, number)
		if number == "SSIS-002" {
			return nil, false // 库里没有这一部 → 跳过，不凭空造
		}
		return map[string]any{"actors": []map[string]any{{"name": "演员甲", "gender": 0}}}, true
	})
	if err != nil {
		t.Fatalf("SyncSidecarsFromRepo: %v", err)
	}
	if res.Scanned != 2 {
		t.Errorf("Scanned = %d，期望 2（config.json 不该算）", res.Scanned)
	}
	if res.Written != 1 {
		t.Errorf("Written = %d，期望 1（SSIS-002 库里没有）", res.Written)
	}
	if len(res.Numbers) != 2 {
		t.Errorf("Numbers = %v，期望两个番号", res.Numbers)
	}
	if strings.Join(asked, ",") != "SSIS-001,SSIS-002" && strings.Join(asked, ",") != "SSIS-002,SSIS-001" {
		t.Errorf("问过的番号 = %v", asked)
	}
	// 真的写进去了
	raw, _ := os.ReadFile(filepath.Join(taskDir, "SSIS-001-U.json"))
	if !strings.Contains(string(raw), "演员甲") {
		t.Errorf("侧车没被回写：\n%s", raw)
	}
	// 库里没有的那一部原样不动
	raw2, _ := os.ReadFile(filepath.Join(sub, "SSIS-002.json"))
	if strings.Contains(string(raw2), "演员甲") {
		t.Errorf("库里没有这一部，不该凭空造字段：\n%s", raw2)
	}
}

// stubStrmTaskRepo 是 SyncSidecarsFromRepo 要的最小仓储桩（只用到 List）。
type stubStrmTaskRepo struct {
	domain.StrmTaskRepository
	tasks []*domain.StrmTask
}

func (r stubStrmTaskRepo) List(context.Context) ([]*domain.StrmTask, error) { return r.tasks, nil }
