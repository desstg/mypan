package javplanner

import (
	"context"
	"sort"
	"strings"
	"testing"

	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/mediaorganize/moplan"
)

// 侧车驱动的那条路：名字、目录名、json 的归属。
//
// 这些用例对着**真实数据**写：MOIL-001 那颗种子实测是
// `MOIL-001.mp4`（358MB 正片）+ `manko.fun.mp4`（11MB 广告）+ 一个 json，
// 115 还为它建了一层以种子名命名的目录。三件事都在下面的树里。

// buildPlanWith 用给定配置跑一次计划。与 planner_test.go 的 buildPlan 同形，
// 但允许改配置（那一条固定用出厂默认的排除表）。
func buildPlanWith(t *testing.T, fs *fakeFS, sourceID, targetID string, cfg Config) *moplan.Plan {
	t.Helper()
	cfg.SourceDirID = sourceID
	cfg.TargetRootID = targetID
	if cfg.ActionType == "" {
		cfg.ActionType = "move"
	}
	if cfg.MaxDirs == 0 {
		cfg.MaxDirs = 500
	}
	if cfg.FileExtensions == "" {
		cfg.FileExtensions = defaultVideoExtensions
	}
	if cfg.MetadataExtensions == "" {
		cfg.MetadataExtensions = defaultMetadataExtensions
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	return plan
}

// seedConfig 是「按侧车整理」的常规配置：小文件阈值 300MB，不删小文件。
func seedConfig() Config {
	return Config{
		SmallFileMB:        300,
		CleanEmptyDirs:     true,
		FileExtensions:     defaultVideoExtensions,
		MetadataExtensions: defaultMetadataExtensions,
	}
}

func actionsFor(plan *moplan.Plan, stage string) []moplan.PlanAction {
	return byStage(plan)[stage]
}

// TestParseSidecarName 侧车名识别：认识的认，噪声不许认。
//
// 「噪声不许认」这一半比前一半重要：网盘上顺带带着的 json 满地都是，
// 少这道闸就会拿 `readme` 当番号去改视频名、建目录。
func TestParseSidecarName(t *testing.T) {
	accept := []struct {
		name   string
		number string
		marks  string
	}{
		{"MOIL-001.json", "MOIL-001", ""},
		{"MOIL-001-C.json", "MOIL-001", "-C"},
		{"MOIL-001-U.json", "MOIL-001", "-U"},
		{"SSIS-444-UC-4K.json", "SSIS-444", "-UC-4K"},
		{"SSIS-444-4K.json", "SSIS-444", "-4K"},
		{"FC2-PPV-1234567-U.json", "FC2-PPV-1234567", "-U"},
		{"123456-789.json", "123456-789", ""},
	}
	for _, c := range accept {
		t.Run("认=>"+c.name, func(t *testing.T) {
			number, marks, ok := parseSidecarName(c.name)
			if !ok {
				t.Fatalf("%s 应当认出是侧车", c.name)
			}
			if number != c.number {
				t.Errorf("番号 = %q，want %q", number, c.number)
			}
			if got := marks.Suffix(); got != c.marks {
				t.Errorf("后缀 = %q，want %q", got, c.marks)
			}
		})
	}

	reject := []string{
		"", "   ",
		"config.json",    // 配置
		"readme-4K.json", // 拆得开但 readme 不是番号
		"notes-u.json",
		"data.json",
		"4K.json",           // 「只有 4K 段」，没有番号
		"MOIL-001.json.bak", // 不是 .json 结尾
		"MOIL-001.txt",      // 不是 json
		"普通家庭录像.json",       // 无番号
	}
	for _, name := range reject {
		t.Run("不认=>"+name, func(t *testing.T) {
			if number, _, ok := parseSidecarName(name); ok {
				t.Errorf("%q 不该认成侧车，却给出番号 %q", name, number)
			}
		})
	}
}

// TestSidecarFlatRenameCreateAndMoveIn 扁平那一路：视频与侧车直接躺在整理目录里。
//
// 断言四件事：目录名是**纯番号**（不是带后缀的主名）、视频改成标准名、
// 广告视频**不改名但照旧移入**、侧车跟着进番号目录。
func TestSidecarFlatRenameCreateAndMoveIn(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "MOIL-001-UC-4K.json", 3776, false)   // 侧车：破解 + 中字 + 4K
	fs.add(root, "manko.fun.mp4", 358*mb, false)       // 正片（名字里什么都没有）
	fs.add(root, "manko.fun.mp4.ad.mp4", 11*mb, false) // 广告：够不上阈值
	fs.add(root, "manko.fun.url", 42, false)           // 与整理无关
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// —— 建目录：名字是纯番号 ——
	ensures := actionsFor(plan, stageMoveIn)
	var dirNames []string
	for _, a := range ensures {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirNames = append(dirNames, a.TargetName)
		}
	}
	if len(dirNames) != 1 || dirNames[0] != "MOIL-001" {
		t.Fatalf("应当只建一个名为 MOIL-001 的目录，got %v", dirNames)
	}

	// —— 改名：正片改成标准名；广告不动；侧车不动 ——
	renames := actionsFor(plan, stageRename)
	gotRenamed := namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{"manko.fun.mp4→MOIL-001-UC-4K.mp4"}
	if len(gotRenamed) != 1 || gotRenamed[0] != want[0] {
		t.Errorf("改名动作 = %v，want %v\n（广告与侧车都不该改名）", gotRenamed, want)
	}

	// —— 移入：正片 + 广告 + 侧车三样都进去 ——
	moves := actionsFor(plan, stageMoveIn)
	moved := map[string]string{}
	for _, a := range moves {
		if a.Kind == moplan.ActionKindRelocate {
			moved[a.SourceName] = a.TargetName
		}
	}
	// 正片以**改后的名字**移入。
	//
	// 注意移入动作的两个名字都是新名（SourceName 也是）—— 这是既有行为：
	// 执行器按 ID 认文件，名字只用于显示，而「改名 + 移入」在预览里应当显示
	// 它最终叫什么。改名那一步是单独一条动作，前一条断言已经覆盖了新旧对应关系。
	if name, ok := moved["MOIL-001-UC-4K.mp4"]; !ok || name != "MOIL-001-UC-4K.mp4" {
		t.Errorf("正片应当以标准名移入，got %q（在不在：%v）", name, ok)
	}
	// 广告：**不改名**但要移进去（用户定的「按阈值挑，但照旧移入」）。
	if name, ok := moved["manko.fun.mp4.ad.mp4"]; !ok || name != "manko.fun.mp4.ad.mp4" {
		t.Errorf("够不上阈值的广告应当原样移入，got %q（在不在：%v）", name, ok)
	}
	// 侧车：要用它自己的名字进番号目录，并且标出来。
	if name, ok := moved["MOIL-001-UC-4K.json"]; !ok || name != "MOIL-001-UC-4K.json" {
		t.Errorf("侧车应当一起移入，got %q（在不在：%v）", name, ok)
	}
	var sidecarMove *moplan.PlanAction
	for i := range moves {
		if moves[i].SourceName == "MOIL-001-UC-4K.json" {
			sidecarMove = &moves[i]
		}
	}
	if sidecarMove == nil {
		t.Fatal("找不到侧车的移入动作")
	}
	if flag, _ := sidecarMove.Metadata["is_sidecar"].(bool); !flag {
		t.Error("侧车的移入动作应当带 is_sidecar 标记，否则预览里看着像把 json 当视频搬了")
	}
	if sidecarMove.TargetParentID != moplan.RefPrefix+dirNames[0] {
		// 这里只断言它指向那条 ensure_dir，具体 ID 由 nextID 生成。
		if !strings.HasPrefix(sidecarMove.TargetParentID, moplan.RefPrefix) {
			t.Errorf("侧车应当移进新建的番号目录，got %q", sidecarMove.TargetParentID)
		}
	}
	// 与整理无关的文件（.url）不参与任何动作。
	for _, a := range plan.Actions {
		if a.SourceName == "manko.fun.url" {
			t.Errorf("非视频非侧车的文件不该有动作，got %+v", a)
		}
	}
}

// TestSidecarSeedDirRenameInPlace 嵌套那一路：115 为多文件种子建了一层目录。
//
// 这一路最容易出错的地方是目录名 —— 照「改名后的整个主名」会把它改成
// `MOIL-001-UC-4K/`，而用户要的是纯番号。另外这条也顺手修掉一个既有 bug：
// 目录里的视频若叫 `manko.fun.mp4`（无番号），旧逻辑会把目录改成 `manko.fun`。
func TestSidecarSeedDirRenameInPlace(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 种子目录名故意不合法，看它会不会被纠正。
	seed := fs.add(root, "manko.fun", 0, true)
	fs.add(seed, "MOIL-001-UC-4K.json", 3776, false)
	fs.add(seed, "manko.fun.mp4", 358*mb, false)
	fs.add(root, "另一部-DVAJ-725.mp4", 900*mb, false) // 无侧车，走旧命名
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 目录改名：`manko.fun` → `MOIL-001`（纯番号，不是主名）。
	dirRenames := actionsFor(plan, stageRenameDir)
	if len(dirRenames) != 1 {
		t.Fatalf("应当只有一条目录改名，got %d：%v", len(dirRenames), dirRenames)
	}
	if dirRenames[0].SourceName != "manko.fun" || dirRenames[0].TargetName != "MOIL-001" {
		t.Errorf("目录应当改名为纯番号 MOIL-001，got %q → %q",
			dirRenames[0].SourceName, dirRenames[0].TargetName)
	}

	// 种子目录里的视频改名（原地），并且**不**发生移入 —— 它已经在作品目录里了。
	renames := actionsFor(plan, stageRename)
	var seedRenames []string
	for _, a := range renames {
		if a.SourceParentID == seed {
			seedRenames = append(seedRenames, a.SourceName+"→"+a.TargetName)
		}
	}
	if len(seedRenames) != 1 || seedRenames[0] != "manko.fun.mp4→MOIL-001-UC-4K.mp4" {
		t.Errorf("种子目录里的视频应当原地改名，got %v", seedRenames)
	}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.SourceParentID == seed {
			t.Errorf("种子目录里的文件不该被移入（已经在作品目录里），got %+v", a)
		}
	}

	// 顶层那个没有侧车的视频仍走旧命名 —— 两条路并存，互不干扰。
	var oldPath []string
	for _, a := range renames {
		if a.SourceName == "另一部-DVAJ-725.mp4" {
			oldPath = append(oldPath, a.TargetName)
		}
	}
	if len(oldPath) != 1 {
		t.Fatalf("无侧车的视频应当仍走清理式改名，got %v", oldPath)
	}
	// 清理式的结果是「删汉字 + 转大写 + 去空格」。
	if oldPath[0] != "DVAJ-725.mp4" {
		t.Errorf("旧命名的结果 = %q，want DVAJ-725.mp4", oldPath[0])
	}
}

// TestSidecarMultiPartNumbering 多分片：按体积降序编 cd1/cd2，且都进同一个番号目录。
func TestSidecarMultiPartNumbering(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "SSIS-444-UC-4K.json", 3776, false)
	fs.add(root, "part-b.mp4", 400*mb, false) // 小的
	fs.add(root, "part-a.mp4", 900*mb, false) // 大的 → cd1
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	renames := actionsFor(plan, stageRename)
	got := namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{"part-a.mp4→SSIS-444-UC-4K-cd1.mp4", "part-b.mp4→SSIS-444-UC-4K-cd2.mp4"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("分片命名 = %v，want %v", got, want)
	}
}

// TestSidecarSingleVideoNoCD 只有一个视频**不编号** —— 加个 -cd1 是噪声，
// 而且会让「补一个分片」与「换一版」的文件名对不上。
func TestSidecarSingleVideoNoCD(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "SSIS-444-U.json", 3776, false)
	fs.add(root, "whatever.mp4", 900*mb, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.TargetName })
	if len(got) != 1 || got[0] != "SSIS-444-U.mp4" {
		t.Errorf("单个视频不该带 cd 编号，got %v", got)
	}
}

// TestSidecarJSONNeverDeleted 侧车**永不删**，即使排除表是空的。
//
// 这是真库上的现状：`mo_jav_delete_small=true` 而 `mo_jav_delete_exclude_types`
// 落库值是空串 —— 排除表形同不存在。若只靠那个设置项，这份 3.7KB 的侧车
// 会在下一次整理时被当小文件删掉，连同里面记的演员、片商、封面地址一起没了，
// 而且不报错。
func TestSidecarJSONNeverDeleted(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "SSIS-444-UC-4K.json", 3776, false) // 侧车
	fs.add(root, "notes.json", 100, false)           // 别人的 json，同样不该删
	fs.add(root, "广告.txt", 100, false)               // 普通小文件，该删
	fs.add(root, "SSIS-444.mp4", 900*mb, false)
	target := fs.add(root, "整理库", 0, true)

	cfg := seedConfig()
	cfg.DeleteSmall = true
	// **两张表都留空** —— 复现真库现状：不限类型、也不排除任何类型。
	cfg.DeleteTypes = ""
	cfg.DeleteExcludeTypes = ""

	plan := buildPlanWith(t, fs, root, target, cfg)

	deleted := map[string]bool{}
	for _, a := range actionsFor(plan, stageDeleteSmall) {
		deleted[a.SourceName] = true
	}
	for _, name := range []string{"SSIS-444-UC-4K.json", "notes.json"} {
		if deleted[name] {
			t.Errorf("%s 被删了 —— json 必须无条件保留（排除表可能是空的）", name)
		}
	}
	if !deleted["广告.txt"] {
		t.Error("普通小文件该删（否则这条测试没有证明力：阈值判据本身失效了）")
	}
	if n, ok := plan.Diagnostics["kept_sidecar"]; !ok || n.(int) != 2 {
		t.Errorf("应当记下保住了几个 json，got %v", plan.Diagnostics["kept_sidecar"])
	}
}

// TestSidecarDuplicateTakesFirst 同一层有两份侧车时取第一个，且不报错。
//
// 场景是真实的：重推一颗质量不同的磁链时文件名会变，`overwrite` 就不生效，
// 新老两份并排躺在那一层。**不自动删旧的**（删文件不可逆），但要能继续干活。
func TestSidecarDuplicateTakesFirst(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "MOIL-001.json", 3776, false)    // 旧的那份
	fs.add(root, "MOIL-001-4K.json", 3776, false) // 新推的那份
	fs.add(root, "manko.fun.mp4", 900*mb, false)
	target := fs.add(root, "整理库", 0, true)

	var logs []string
	cfg := seedConfig()
	p := New(context.Background(), fs, 1, func() Config {
		c := cfg
		c.SourceDirID = root
		c.TargetRootID = target
		c.ActionType = "move"
		c.MaxDirs = 500
		return c
	}(), javrules.Defaults(), "task-1", func(s string) { logs = append(logs, s) }, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}

	// 扫描顺序里 `MOIL-001.json` 在前，于是它被选中 → 没有 4K 标记。
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.TargetName })
	if len(got) != 1 || got[0] != "MOIL-001.mp4" {
		t.Errorf("应当用先扫到的那份侧车（无 4K），got %v", got)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "同一番号的多份侧车") {
		t.Errorf("重复侧车应当留一条日志，got %q", joined)
	}
	// 两份 json 都不该被删、也不该被当成视频。
	for _, a := range actionsFor(plan, stageDeleteSmall) {
		if strings.HasSuffix(a.SourceName, ".json") {
			t.Errorf("json 不该被删：%s", a.SourceName)
		}
	}
}

// TestSidecarRenamesSiblingMetadata 同层的字幕/图片跟着视频改名。
//
// 这一路特别容易漏：没有侧车时清理式改名字幕一起改（字幕名里带番号），
// 两条名字自然对齐；有了侧车之后只认视频，字幕就会留在原地，而 Emby 是靠
// **同名**把字幕配到视频上的 —— 配不上就是「明明下载了字幕却不显示」。
func TestSidecarRenamesSiblingMetadata(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "MOIL-001-UC-4K.json", 3776, false)
	fs.add(root, "manko.fun.mp4", 358*mb, false)
	fs.add(root, "manko.fun.srt", 40*1024, false)    // 同主名的字幕
	fs.add(root, "manko.fun-1.jpg", 200*1024, false) // 同主名的图片（带后缀）
	fs.add(root, "别的片.srt", 40*1024, false)          // 与本次改名无关
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{
		"manko.fun-1.jpg→MOIL-001-UC-4K-1.jpg",
		"manko.fun.mp4→MOIL-001-UC-4K.mp4",
		"manko.fun.srt→MOIL-001-UC-4K.srt",
	}
	if len(got) != len(want) {
		t.Fatalf("改名动作 = %v，want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("改名动作[%d] = %q，want %q（全部：%v）", i, got[i], want[i], got)
		}
	}
}

// TestSidecarNeverRenamesItself 侧车**自己绝不被改名**，哪怕它的主名是视频旧主名的前缀。
//
// 真实场景：视频叫 `MOIL-001.mp4`，侧车叫 `MOIL-001-UC-4K.json` ——
// `MOIL-001` 正是 `MOIL-001-UC-4K` 的前缀。元数据跟随若不加排除，
// 侧车会被改成 `MOIL-001-UC-4K-UC-4K.json`，下一轮整理就读不出番号了。
func TestSidecarNeverRenamesItself(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "MOIL-001-UC-4K.json", 3776, false)
	fs.add(root, "MOIL-001.mp4", 900*mb, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())
	for _, a := range plan.Actions {
		if a.SourceName == "MOIL-001-UC-4K.json" && a.TargetName != a.SourceName {
			t.Fatalf("侧车被改名了：%q → %q", a.SourceName, a.TargetName)
		}
	}
	// 视频自己该改（加标记后缀）。
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	if len(got) != 1 || got[0] != "MOIL-001.mp4→MOIL-001-UC-4K.mp4" {
		t.Errorf("视频应当改成 MOIL-001-UC-4K.mp4，got %v", got)
	}
}

// TestContainerPairsVideoByNumber 一目录两份 json + 两个乱名字的视频 —— 用户报的那个场景。
//
// 下载目录根下平铺着两部片（手动推送不建子目录），视频名是发布组的乱名字。
// 早先的实现取「第一份 json」给**所有**视频用，两部片会被串成一部
// （`ABF-179-U-4K-cd1.mp4` / `-cd2.mp4`）。现在按**视频名里抽出的番号**配对。
func TestContainerPairsVideoByNumber(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 两份侧车，各带不同标记
	fs.add(root, "ABF-179-U-4K.json", 4128, false)
	fs.add(root, "MRSS-104-C.json", 3900, false)
	// 两个真实形态的乱名字：发布组水印 + 番号
	fs.add(root, "www.98T.la@ABF-179@BVPP1XdfBVPP4X(STD)_apo8_iris2.mp4", 33_866_835_383, false)
	fs.add(root, "hhd800.com@MRSS-104 中文字幕.mp4", 5_900_000_000, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 改名：各按各的侧车，**不编号**（每个番号组只有一个视频）。
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{
		"hhd800.com@MRSS-104 中文字幕.mp4→MRSS-104-C.mp4",
		"www.98T.la@ABF-179@BVPP1XdfBVPP4X(STD)_apo8_iris2.mp4→ABF-179-U-4K.mp4",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("改名 = %v\nwant %v", got, want)
	}

	// 建目录：**两个**番号目录，不是一个。
	var dirs []string
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirs = append(dirs, a.TargetName)
		}
	}
	sort.Strings(dirs)
	if len(dirs) != 2 || dirs[0] != "ABF-179" || dirs[1] != "MRSS-104" {
		t.Errorf("应当各建各的番号目录，got %v", dirs)
	}

	// 移入：每个视频 + 每份 json 都进**自己**的番号目录。
	movedTo := map[string]string{}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindRelocate {
			movedTo[strings.SplitN(a.Reason, "| ", 2)[1]] = a.SourceName
		}
	}
	if movedTo["ABF-179"] != "ABF-179-U-4K.mp4" {
		t.Errorf("ABF-179 目录里应当是 ABF-179-U-4K.mp4，got %q", movedTo["ABF-179"])
	}
	if movedTo["MRSS-104"] != "MRSS-104-C.mp4" {
		t.Errorf("MRSS-104 目录里应当是 MRSS-104-C.mp4，got %q", movedTo["MRSS-104"])
	}
}

// TestContainerUnmatchedVideoIsLeftAlone 容器目录里认不出番号的视频：一个都不动。
//
// 目录里有两份 json，而这个名字里没有番号 —— 没有任何信息能判定它属于哪一份。
// 宁可漏整理一部，也不要靠猜把两部片串成一部；同时要**如实报进 Skipped**，
// 让用户在预览里看见「这几个没被处理」。
func TestContainerUnmatchedVideoIsLeftAlone(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "ABF-179-U-4K.json", 4128, false)
	fs.add(root, "MRSS-104-C.json", 3900, false)
	fs.add(root, "manko.fun.mp4", 900*mb, false) // 名字里没有番号
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	for _, a := range actionsFor(plan, stageRename) {
		if a.SourceName == "manko.fun.mp4" {
			t.Errorf("认不出番号的视频不该被改名，got %+v", a)
		}
	}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.SourceName == "manko.fun.mp4" {
			t.Errorf("认不出番号的视频不该被移入，got %+v", a)
		}
	}
	if !anySkippedContains(plan, "认不出番号") {
		t.Error("应当把它写进 Skipped，让用户在预览里看见")
	}
	// 两份 json 也不该因为「配不上视频」而被乱处理。
	for _, a := range plan.Actions {
		if strings.HasSuffix(a.SourceName, ".json") && a.Kind == moplan.ActionKindDeleteFile {
			t.Errorf("json 不该被删：%s", a.SourceName)
		}
	}
}

// TestContainerDirIsNotRenamed 容器子目录**不改名**。
//
// 一个子目录里有两份 json（装了两部作品），它不能叫任何一个番号 ——
// 早先的实现会拿第一份的番号把整个目录改名。
//
// **范围说明**：这里是「原地改名」，不在子目录里再造 `<番号>/`。
// 建目录那一步（阶段 3）只处理**整理目录的直属文件**（它的前提是「子目录本身
// 就是一个作品目录」，容器打破了这个前提但没到必须处理的程度）——
// 两部片改名后并排躺在 `待整理/` 里，各自带自己的 json，Emby 按文件配 nfo 照样
// 认得出来。真要「一部片一个子目录」，那是另一件事（见计划文档里的记录）。
func TestContainerDirIsNotRenamed(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	box := fs.add(root, "待整理", 0, true)
	fs.add(box, "ABF-179-U-4K.json", 4128, false)
	fs.add(box, "MRSS-104-C.json", 3900, false)
	fs.add(box, "www.98T.la@ABF-179@x.mp4", 900*mb, false)
	fs.add(box, "MRSS-104 中文字幕.mp4", 800*mb, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	for _, a := range actionsFor(plan, stageRenameDir) {
		t.Errorf("容器目录不该被改名，got %+v", a)
	}
	// 两部片各自按自己的侧车改名（原地，不编号）。
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{
		"MRSS-104 中文字幕.mp4→MRSS-104-C.mp4",
		"www.98T.la@ABF-179@x.mp4→ABF-179-U-4K.mp4",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("改名 = %v\nwant %v", got, want)
	}
	// 两份 json 都不改名、也不该有任何目录被建/被搬。
	for _, a := range actionsFor(plan, stageMoveIn) {
		t.Errorf("容器子目录里不该发生移入，got %+v", a)
	}
}

// TestSoleSidecarStillGovernsDir 一份 json 的目录仍然「整层归它」——
// 种子目录的常态，也是 manko.fun.mp4 那种没番号的名字唯一的活路。
func TestSoleSidecarStillGovernsDir(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	seed := fs.add(root, "MOIL-001", 0, true)
	fs.add(seed, "MOIL-001-UC-4K.json", 3776, false)
	fs.add(seed, "manko.fun.mp4", 358*mb, false) // 没有番号，靠「唯一一份」认出来
	fs.add(seed, "manko.fun.srt", 40*1024, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{
		"manko.fun.mp4→MOIL-001-UC-4K.mp4",
		"manko.fun.srt→MOIL-001-UC-4K.srt",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("改名 = %v\nwant %v", got, want)
	}
	if anySkippedContains(plan, "认不出番号") {
		t.Error("只有一份侧车时不该出现「认不出番号」的跳过")
	}
}

// TestDeepVideoKeepsItsDepth 三级目录里的视频：**只改名、不动层级**（用户 2026-09-24 定）。
//
// 建目录那一步只处理整理目录的直属文件（前提是「子目录本身就是一个作品目录」），
// 所以深处的视频不会被上提到下载目录下。它只做两件事：自己改成标准番号名、
// 所在那一层目录改成番号，深度一模一样。
//
// **这个行为是确认过的，不是遗漏** —— 有两种「更整齐」的做法（把深处的作品上提到
// 下载目录下平铺、或者干脆不参与分类移动），用户明确选了保持现状。改动它之前先问。
//
// 顺带把那个副作用也钉住：分类移动只看整理目录的**一级子目录**、按**它的名字**
// 匹配规则。`合集` 这种名字里没有番号 → 命中兜底规则 → **整棵子树**被搬走。
// 也就是说深度不变，但整棵树的根换了。
func TestDeepVideoKeepsItsDepth(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	l1 := fs.add(root, "合集", 0, true)
	l2 := fs.add(l1, "2024", 0, true)
	l3 := fs.add(l2, "乱七八糟名字", 0, true)
	fs.add(l3, "ABF-179-U-4K.json", 4128, false)
	fs.add(l3, "www.98T.la@ABF-179@x.mp4", 900*mb, false)
	target := fs.add(root, "整理库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 1) 视频原地改名（父目录不变）。
	var renamed []string
	for _, a := range actionsFor(plan, stageRename) {
		if a.SourceName == "www.98T.la@ABF-179@x.mp4" {
			renamed = append(renamed, a.TargetName)
			if a.SourceParentID != l3 || a.TargetParentID != l3 {
				t.Errorf("深层视频只该原地改名，got 父目录 %q → %q", a.SourceParentID, a.TargetParentID)
			}
		}
	}
	if len(renamed) != 1 || renamed[0] != "ABF-179-U-4K.mp4" {
		t.Errorf("深层视频应当改成 ABF-179-U-4K.mp4，got %v", renamed)
	}

	// 2) 它所在那一层目录改成番号；上两层**不动**（它们的直属子项是目录，不是视频）。
	var dirRenames []string
	for _, a := range actionsFor(plan, stageRenameDir) {
		dirRenames = append(dirRenames, a.SourceName+"→"+a.TargetName)
	}
	if len(dirRenames) != 1 || dirRenames[0] != "乱七八糟名字→ABF-179" {
		t.Errorf("只该把视频所在那层改成番号，got %v", dirRenames)
	}

	// 3) 深度不变：**没有**为它建番号目录、也没有移入。
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.SourceName == "www.98T.la@ABF-179@x.mp4" ||
			(a.Kind == moplan.ActionKindEnsureDir && a.TargetName == "ABF-179") {
			t.Errorf("深层视频不该被上提，got %+v", a)
		}
	}

	// 4) 已确认的副作用：一级子目录按**自己的名字**分类，`合集` 没有番号 → 兜底规则。
	var classified []string
	for _, a := range actionsFor(plan, stageClassify) {
		if a.Kind == moplan.ActionKindRelocate && a.SourceName == "合集" {
			classified = append(classified, a.Reason)
		}
	}
	if len(classified) != 1 {
		t.Fatalf("`合集` 应当被分类移动（现状如此），got %v", classified)
	}
	if !strings.Contains(classified[0], "无匹配") {
		t.Errorf("`合集` 没有番号，应当落进兜底分类，got %q", classified[0])
	}
}

// TestSidecarNumberWinsOverDirtyDirName 目录名再脏也不影响 —— 有侧车时目录名按**侧车的番号**走。
//
// 这一条回答的是「带点状水印的目录名认不出番号」那个担心：那条路**根本不在有侧车的
// 路径上**。`HasCode` 按最后一个点切主名（`www.98T.la@SNOS-373` → `www.98T` → 判成无番号）
// 只影响两件事：要不要给这个名字改名、以及「无番号」兜底规则认不认它。
// 而有侧车时：
//   - 目录改名取的是 sidecarEntry.Number（来自 **json 文件名**），与目录自己叫什么无关；
//   - 分类第二轮吃的是 dirFinal（= 那个番号），所以照样命中「日本」。
//
// 所以那个 bug 只咬「**没有侧车**、且名字带点状水印」的目录 —— 实测用户库里这类目录
// 一个都没有（连带点的目录名都没有）。这条测试把这个边界钉住。
func TestSidecarNumberWinsOverDirtyDirName(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 115 给的种子目录名：带点状水印，最后一个点之后才是番号
	dirty := fs.add(root, "www.98T.la@SNOS-373", 0, true)
	fs.add(dirty, "SNOS-373-U.json", 4000, false)
	fs.add(dirty, "www.98T.la@SNOS-373-U.mp4", 900*mb, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// ① 目录名按侧车的番号改（不看目录自己叫什么）
	var dirRenames []string
	for _, a := range actionsFor(plan, stageRenameDir) {
		dirRenames = append(dirRenames, a.SourceName+"→"+a.TargetName)
	}
	if len(dirRenames) != 1 || dirRenames[0] != "www.98T.la@SNOS-373→SNOS-373" {
		t.Errorf("目录名应当按侧车的番号改成 SNOS-373，got %v", dirRenames)
	}

	// ② 视频按侧车的番号 + 标记改
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.TargetName })
	if len(got) != 1 || got[0] != "SNOS-373-U.mp4" {
		t.Errorf("视频应当改成 SNOS-373-U.mp4，got %v", got)
	}

	// ③ 分类命中日本（第二轮吃的是 dirFinal = 侧车的番号）
	var cls []string
	for _, a := range actionsFor(plan, stageClassify) {
		if a.Kind == moplan.ActionKindRelocate {
			cls = append(cls, a.SourceName+" → "+a.Reason)
		}
	}
	if len(cls) != 1 || !strings.Contains(cls[0], "日本") {
		t.Errorf("应当按侧车的番号归到日本AV，got %v", cls)
	}
	if anySkippedContains(plan, "认不出番号") {
		t.Error("有侧车时不该出现「认不出番号」的跳过")
	}
}

// TestDirtyDirNameWithoutSidecar 没有侧车时，带点状水印的**目录名**会怎样。
//
// 用户问的「以防万一没有 json」就是这个。结论是**不坏**：
//   - 目录名本身确实认不出番号（`HasCode` 按最后一个点切主名），但目录改名取的是
//     **视频**改完名后的主名 —— 视频有真扩展名，切得对，水印会被清掉；
//   - 分类的第二轮吃的是这个干净的目录名，于是照样命中「日本」。
//
// 注意这与设置页的**试跑**不同：试跑（ExplainName）拿目录名自己算 DirName，
// 那一步会因为同一个误判提前返回、给出「无番号/兜底」。**以本测试为准** ——
// 它走的是真正的计划生成路径。
func TestDirtyDirNameWithoutSidecar(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	dirty := fs.add(root, "www.98T.la@SNOS-373", 0, true)
	fs.add(dirty, "www.98T.la@SNOS-373-U.mp4", 900*mb, false) // 无 json
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	var dirRenames []string
	for _, a := range actionsFor(plan, stageRenameDir) {
		dirRenames = append(dirRenames, a.SourceName+"→"+a.TargetName)
	}
	if len(dirRenames) != 1 {
		t.Fatalf("目录应当改名（跟随视频的新主名），got %v", dirRenames)
	}
	get := strings.SplitN(dirRenames[0], "→", 2)[1]
	if strings.Contains(get, "98T") || strings.Contains(get, ".") {
		t.Errorf("目录名应当被清干净，got %q", get)
	}
	if !strings.HasPrefix(get, "SNOS-373") {
		t.Errorf("目录名应当含番号，got %q", get)
	}

	var cls []string
	for _, a := range actionsFor(plan, stageClassify) {
		if a.Kind == moplan.ActionKindRelocate {
			cls = append(cls, a.Reason)
		}
	}
	if len(cls) != 1 || !strings.Contains(cls[0], "日本") {
		t.Errorf("分类应当命中日本（第二轮吃的是清理后的目录名），got %v", cls)
	}
}

// TestSurvivingMetadataFollowsVideo 被「排除表」保下来的元数据文件，会不会跟着视频走。
//
// 两条路机制不同，所以分开钉：
//   - **建目录 + 移入**（平铺那一路）：只有视频与侧车有显式动作，字幕/图片靠
//     「元数据跟随」（Diagnostics["meta_followers"]），而跟随只认
//     `mo_metadata_extensions` 里列出的扩展名。
//   - **分类移动**：搬的是**整个目录**，所以那一层里的一切都跟着，不依赖跟随表。
//
// 用的排除表就是用户真库现在那份（nfo;srt;jpg;json）。
func TestSurvivingMetadataFollowsVideo(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "MOIL-001-UC-4K.json", 3776, false) // 侧车
	fs.add(root, "manko.fun.mp4", 358*mb, false)     // 正片
	fs.add(root, "manko.fun.srt", 40*1024, false)    // 字幕
	fs.add(root, "manko.fun.nfo", 2*1024, false)     // nfo
	fs.add(root, "manko.fun.jpg", 200*1024, false)   // 封面
	fs.add(root, "readme.txt", 500, false)           // 不在排除表里 → 该删
	target := fs.add(root, "库", 0, true)

	cfg := seedConfig()
	cfg.DeleteSmall = true
	cfg.DeleteTypes = ""
	cfg.DeleteExcludeTypes = "nfo;srt;jpg;json"
	plan := buildPlanWith(t, fs, root, target, cfg)

	// ① 排除表里的保下来，不在表里的删掉
	deleted := map[string]bool{}
	for _, a := range actionsFor(plan, stageDeleteSmall) {
		deleted[a.SourceName] = true
	}
	for _, n := range []string{"manko.fun.srt", "manko.fun.nfo", "manko.fun.jpg"} {
		if deleted[n] {
			t.Errorf("%s 在排除表里，不该被删", n)
		}
	}
	if !deleted["readme.txt"] {
		t.Error("readme.txt 不在排除表里，应当被删（否则这条测试没证明力）")
	}

	// ② 元数据跟随条目存在，且扩展名表覆盖字幕/nfo/图片
	fol, _ := plan.Diagnostics["meta_followers"].([]map[string]any)
	if len(fol) != 1 {
		t.Fatalf("应当有一条元数据跟随（对应那个移入的视频），got %d", len(fol))
	}
	exts, _ := fol[0]["meta_exts"].([]string)
	joined := strings.Join(exts, ";")
	for _, want := range []string{"srt", "nfo", "jpg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("meta_exts 应当含 %s（否则字幕不会跟着走），got %q", want, joined)
		}
	}

	// ③ 视频与侧车都进新建的番号目录
	movedIn := map[string]bool{}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindRelocate {
			movedIn[a.SourceName] = true
		}
	}
	for _, n := range []string{"MOIL-001-UC-4K.mp4", "MOIL-001-UC-4K.json"} {
		if !movedIn[n] {
			t.Errorf("%s 应当被移入番号目录，got %v", n, movedIn)
		}
	}
}

// TestClassifyMovesWholeDirWithMetadata 分类移动搬的是整个目录 ——
// 那一层里的字幕/图片/nfo 不需要任何跟随机制，跟着目录一起走。
func TestClassifyMovesWholeDirWithMetadata(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	seed := fs.add(root, "SNOS-373-U", 0, true)
	fs.add(seed, "SNOS-373-U.json", 4000, false)
	fs.add(seed, "SNOS-373-U.mp4", 900*mb, false)
	fs.add(seed, "SNOS-373-U.srt", 40*1024, false) // 字幕留在那一层
	fs.add(seed, "SNOS-373-U.jpg", 200*1024, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 分类只发一条**针对目录本身**的 relocate —— 目录里的一切自然都跟着。
	var dirMoves []moplan.PlanAction
	for _, a := range actionsFor(plan, stageClassify) {
		if a.Kind == moplan.ActionKindRelocate {
			dirMoves = append(dirMoves, a)
		}
	}
	if len(dirMoves) != 1 {
		t.Fatalf("应当只有一条「搬目录」动作，got %d", len(dirMoves))
	}
	if !strings.Contains(dirMoves[0].Reason, "日本") {
		t.Errorf("应当归到日本AV，got %q", dirMoves[0].Reason)
	}
	// 字幕/图片**不该**有自己的搬运动作 —— 它们是随目录走的。
	for _, a := range plan.Actions {
		if strings.HasSuffix(a.SourceName, ".srt") || strings.HasSuffix(a.SourceName, ".jpg") {
			t.Errorf("字幕/图片不该有独立动作（它们随目录走），got %+v", a)
		}
	}
}

// ── 日期序号型：改名时把站名拼进番号 ──
//
// 真实场景（用户手动推送，目标 /CMS影库/冗余，不建子目录）：
//
//	第一轮  091926-001-carib.mp4 平铺在源目录 + 091926-001.json
//	        → 改成 091926-001-CARIB.mp4 与 091926-001-CARIB.json
//	        → 建 091926-001-CARIB/ 并移入（两者一起）
//	第二轮  目录已在树里 → 命中「素人」→ 搬进 FC2
//
// 不补站名的话，`091926-001` 匹配不上任何规则（日期序号型没有 pattern 兜底），
// 片会**静默**留在原地 —— 不报错、Skipped 里也没有。

// TestSidecarDateSeqAddsStation 日期序号型：站名从视频名里捞出来拼进番号，
// **视频与侧车一起改名**（用户要求两者同名）。
func TestSidecarDateSeqAddsStation(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "091926-001-carib.mp4", 900*mb, false)
	fs.add(root, "091926-001.json", 4000, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// —— 改名：番号后面拼上站名，**视频与侧车各一条** ——
	renames := actionsFor(plan, stageRename)
	gotRenamed := namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	want := []string{
		"091926-001-carib.mp4→091926-001-CARIB.mp4",
		"091926-001.json→091926-001-CARIB.json",
	}
	if len(gotRenamed) != 2 || gotRenamed[0] != want[0] || gotRenamed[1] != want[1] {
		t.Errorf("改名动作 = %v，期望 %v", gotRenamed, want)
	}

	// —— 建目录：名字也带站名（与视频名同一个来源，不能各算各的）——
	var dirNames []string
	ensureIDs := map[string]string{}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirNames = append(dirNames, a.TargetName)
			ensureIDs[a.TargetName] = a.ID
		}
	}
	if len(dirNames) != 1 || dirNames[0] != "091926-001-CARIB" {
		t.Fatalf("应当只建一个 091926-001-CARIB 目录，got %v", dirNames)
	}

	// —— 移入：视频与侧车进的是同一个目录（分家就会建出两个目录）——
	// 移入动作的 SourceName 是**改后的名字**（既有行为，见 TestSidecarFlatRenameCreateAndMoveIn）。
	moved := map[string]string{}
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindRelocate {
			moved[a.SourceName] = a.TargetParentID
		}
	}
	wantParent := moplan.RefPrefix + ensureIDs["091926-001-CARIB"]
	if got, ok := moved["091926-001-CARIB.mp4"]; !ok || got != wantParent {
		t.Errorf("视频应当移进 091926-001-CARIB，got %q（在不在：%v）", got, ok)
	}
	if got, ok := moved["091926-001-CARIB.json"]; !ok || got != wantParent {
		t.Errorf("侧车应当与视频进同一个目录，got %q（在不在：%v）", got, ok)
	}

	// —— 分类：**一轮到位**。刚建出来的目录也在分类阶段一并判（见 stageClassify），
	// 所以同一份计划里就有「建目录 → 移入 → 搬进 FC2」 —— 以前要跑两轮。——
	classifyMoves := actionsFor(plan, stageClassify)
	var classified []string
	for _, a := range classifyMoves {
		if a.Kind == moplan.ActionKindRelocate {
			classified = append(classified, a.SourceName+"→"+a.Reason)
		}
	}
	if len(classified) != 1 {
		t.Fatalf("应当有一条分类搬运动作（把新建的目录搬进 FC2），got %v", classified)
	}
	if !strings.Contains(classified[0], "素人") {
		t.Errorf("应当命中「素人」，got %q", classified[0])
	}
	// 搬的是**本轮新建的那个目录**：源 ID 是 ref:<ensure_dir>，不是树里的真 ID。
	if !strings.HasPrefix(classifyMoves[len(classifyMoves)-1].SourceID, moplan.RefPrefix) {
		t.Errorf("搬的应当是本轮新建的目录（ref: 引用），got %q",
			classifyMoves[len(classifyMoves)-1].SourceID)
	}

	// 第二轮：把第一轮的结果摆回树里（目录已在 FC2 下、名字是 091926-001-CARIB/，
	// 里面是**两个都改过名**的文件），再跑一次 —— 应当**零动作**（完全幂等）。
	fs2 := newFakeFS()
	const root2 = "/root"
	lib := fs2.add(root2, "库", 0, true)
	fc2 := fs2.add(lib, "FC2", 0, true)
	seed := fs2.add(fc2, "091926-001-CARIB", 0, true)
	fs2.add(seed, "091926-001-CARIB.mp4", 900*mb, false)
	fs2.add(seed, "091926-001-CARIB.json", 4000, false)
	plan2 := buildPlanWith(t, fs2, root2, lib, seedConfig())

	// 幂等：第二轮**不该**再产生任何改名动作（视频与侧车都已是目标名）。
	if renames := actionsFor(plan2, stageRename); len(renames) != 0 {
		t.Errorf("第二轮不该再改名（幂等），got %v",
			namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName }))
	}
	// 目录名已经对了，也不该再改名。
	if renames := actionsFor(plan2, stageRenameDir); len(renames) != 0 {
		t.Errorf("第二轮不该再改目录名（幂等），got %v",
			namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName }))
	}
	// 它已经在 FC2 里了（不在源目录下），不该再被搬一次。
	if moves := actionsFor(plan2, stageClassify); len(moves) != 0 {
		t.Errorf("第二轮不该再有分类动作，got %v", reasonsOf(plan2, stageClassify))
	}
}

// TestSidecarDateSeqContainerWithStations 容器目录（一目录多份侧车）里的日期序号型。
//
// 这是**侧车也改名**之后最要紧的一条回归：第二轮读到的侧车名是
// `091926-001-CARIB.json`（带站名），而 ownerFor 从视频名抽出的番号是
// `091926-001`（ExtractCode 对日期序号型只给到序号）—— 若不把站名从侧车名里
// 拆回去，两者比不中，整部片会被判成「认不出番号、保持原样」。
// 单份侧车的目录有 sole 兜着看不出来，所以必须用**容器目录**才测得到。
func TestSidecarDateSeqContainerWithStations(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 两份侧车、两个视频 —— 手动推送不建子目录，下载目录就是这样平铺积累的。
	fs.add(root, "091926-001-carib.mp4", 900*mb, false)
	fs.add(root, "091926-001.json", 4000, false)
	fs.add(root, "ABF-179.mp4", 800*mb, false)
	fs.add(root, "ABF-179.json", 4000, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 两个视频各自改名；日期序号型那个带上站名，字母番号那个不带。
	got := namesOf(actionsFor(plan, stageRename),
		func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	wantRenames := []string{
		"091926-001-carib.mp4→091926-001-CARIB.mp4",
		"091926-001.json→091926-001-CARIB.json",
		"ABF-179.mp4→ABF-179.mp4", // 字母番号已是标准名 → 不产生动作
		"ABF-179.json→ABF-179.json",
	}
	_ = wantRenames
	// 只断言两条真动作（另两个名字没变、不会有动作）。
	if len(got) != 2 ||
		got[0] != "091926-001-carib.mp4→091926-001-CARIB.mp4" ||
		got[1] != "091926-001.json→091926-001-CARIB.json" {
		t.Errorf("改名动作 = %v，期望日期序号型那两条", got)
	}

	// 两个目录各一个，名字各是各自的番号（没串片）。
	var dirNames []string
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirNames = append(dirNames, a.TargetName)
		}
	}
	if len(dirNames) != 2 {
		t.Fatalf("应当建两个作品目录，got %v", dirNames)
	}
	joined := strings.Join(dirNames, ",")
	if !strings.Contains(joined, "091926-001-CARIB") || !strings.Contains(joined, "ABF-179") {
		t.Errorf("两个目录应当是 091926-001-CARIB 与 ABF-179，got %v", dirNames)
	}

	// —— 第二轮：**侧车名已带站名**，这一轮配对必须仍然成立 ——
	fs2 := newFakeFS()
	const root2 = "/root"
	fs2.add(root2, "091926-001-CARIB.mp4", 900*mb, false)
	fs2.add(root2, "091926-001-CARIB.json", 4000, false)
	fs2.add(root2, "ABF-179.mp4", 800*mb, false)
	fs2.add(root2, "ABF-179.json", 4000, false)
	target2 := fs2.add(root2, "库", 0, true)
	plan2 := buildPlanWith(t, fs2, root2, target2, seedConfig())

	// 不该有任何改名（都已是目标名）——若 SplitStationSuffix 没做，这里会冒出
	// 「侧车改回纯番号」的动作，而且视频会被判成认不出番号。
	if renames := actionsFor(plan2, stageRename); len(renames) != 0 {
		t.Errorf("第二轮不该再改名，got %v",
			namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName }))
	}
	// 也不该有任何「认不出番号」的 Skipped —— 那是配对失效的信号。
	for _, s := range plan2.Skipped {
		if reason, _ := s["reason"].(string); strings.Contains(reason, "认不出番号") {
			t.Errorf("第二轮不该出现「认不出番号」：%v", s)
		}
	}
	// 两个视频都该被移进各自的目录（各建一个 ensure_dir + 各两条 relocate）。
	var dirs2 []string
	for _, a := range actionsFor(plan2, stageMoveIn) {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirs2 = append(dirs2, a.TargetName)
		}
	}
	joined2 := strings.Join(dirs2, ",")
	if !strings.Contains(joined2, "091926-001-CARIB") || !strings.Contains(joined2, "ABF-179") {
		t.Errorf("第二轮应当仍建出两个目录，got %v", dirs2)
	}
}

// containsAny 报告这一串里有没有哪一条含 needle。
func containsAny(list []string, needle string) bool {
	for _, s := range list {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// TestSidecarDateSeqStationFromSeedDir 站名在**目录名**上、视频名里没有：
// 实测种子目录会叫 `030323-001-carib`，而里面的视频可能叫 `manko.fun.mp4`。
//
// 这一路与平铺那一路的区别：目录名本来就已经带站名，所以「素人」规则**在改动之前
// 就命中得了**（分类匹配拿 d.Name 作原名）。这里真正要钉的是**视频与目录被改成
// 标准形态时也带上站名** —— 否则目录会被改成 `030323-001`，把唯一的线索抹掉。
func TestSidecarDateSeqStationFromSeedDir(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	seed := fs.add(root, "030323-001-carib", 0, true)
	fs.add(seed, "030323-001.json", 4000, false)
	fs.add(seed, "manko.fun.mp4", 900*mb, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	// 视频名里什么都没有，但有侧车 → 按侧车命名，且番号带站名。
	renames := actionsFor(plan, stageRename)
	gotRenamed := namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName })
	wantRenamed := []string{
		"030323-001.json→030323-001-CARIB.json",
		"manko.fun.mp4→030323-001-CARIB.mp4",
	}
	if len(gotRenamed) != 2 || gotRenamed[0] != wantRenamed[0] || gotRenamed[1] != wantRenamed[1] {
		t.Errorf("改名动作 = %v，期望 %v（视频与侧车一起改）", gotRenamed, wantRenamed)
	}
	// 目录名改成带站名的番号（不是纯 030323-001）。
	var dirNames []string
	for _, a := range actionsFor(plan, stageRenameDir) {
		dirNames = append(dirNames, a.TargetName)
	}
	if len(dirNames) != 1 || dirNames[0] != "030323-001-CARIB" {
		t.Fatalf("目录应当改名成 030323-001-CARIB，got %v", dirNames)
	}
	// 分类：命中「素人」→ FC2。
	if !containsAny(reasonsOf(plan, stageClassify), "素人") {
		t.Errorf("应当命中「素人」，got %v", reasonsOf(plan, stageClassify))
	}
}

// reasonsOf 取某个阶段全部动作的 Reason，便于断言「命中了哪条分类规则」。
func reasonsOf(plan *moplan.Plan, stage string) []string {
	var out []string
	for _, a := range actionsFor(plan, stage) {
		out = append(out, a.Reason)
	}
	return out
}

// TestSidecarDateSeqNoStationUnchanged 名字里没带站名 → **保持现状**（用户定的）：
// 不拼、不改名、目录名仍是纯番号。这是与「补站名」并列的另一半契约。
func TestSidecarDateSeqNoStationUnchanged(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "091926-001.mp4", 900*mb, false)
	fs.add(root, "091926-001.json", 4000, false)
	target := fs.add(root, "库", 0, true)

	plan := buildPlanWith(t, fs, root, target, seedConfig())

	if renames := actionsFor(plan, stageRename); len(renames) != 0 {
		t.Errorf("没带站名时不该改名，got %v",
			namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName + "→" + a.TargetName }))
	}
	var dirNames []string
	for _, a := range actionsFor(plan, stageMoveIn) {
		if a.Kind == moplan.ActionKindEnsureDir {
			dirNames = append(dirNames, a.TargetName)
		}
	}
	if len(dirNames) != 1 || dirNames[0] != "091926-001" {
		t.Fatalf("目录名应当是纯番号 091926-001，got %v", dirNames)
	}
}

// TestSidecarLetterCodeNeverGetsStation 字母番号**永远不补站名** ——
// 它自带字母，改名后照样命中「日本」，本来就没问题。
// （反例：`HEYZO-` 也在「素人」的 includes 里，若不加 IsDateSeqCode 这道闸，
// 就会拼出 `HEYZO-3837-HEYZO`。）
func TestSidecarLetterCodeNeverGetsStation(t *testing.T) {
	for _, name := range []string{"HEYZO-3837.mp4", "SIRO-5071.mp4", "FC2-4939195.mp4", "336KNB-408.mp4"} {
		fs := newFakeFS()
		const root = "/root"
		number := strings.TrimSuffix(name, ".mp4")
		fs.add(root, name, 900*mb, false)
		fs.add(root, number+".json", 4000, false)
		target := fs.add(root, "库", 0, true)

		plan := buildPlanWith(t, fs, root, target, seedConfig())
		var dirNames []string
		for _, a := range actionsFor(plan, stageMoveIn) {
			if a.Kind == moplan.ActionKindEnsureDir {
				dirNames = append(dirNames, a.TargetName)
			}
		}
		if len(dirNames) != 1 || dirNames[0] != number {
			t.Errorf("%s：目录名应当是纯番号 %q，got %v", name, number, dirNames)
		}
	}
}
