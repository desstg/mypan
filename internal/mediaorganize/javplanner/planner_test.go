package javplanner

import (
	"context"
	"sort"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/mediaorganize/moplan"
)

// 本文件的用例移植自 115-auto/tests/test_organize.py:284-458 ——
// 那是这条流水线的黄金验收集（含一棵有代表性的测试树）。

const mb = 1024 * 1024

// fakeFS 是一棵内存目录树。key 是 parentID，value 是它下面的条目。
type fakeFS struct {
	items map[string][]domain.FileItem
}

func newFakeFS() *fakeFS {
	return &fakeFS{items: map[string][]domain.FileItem{}}
}

func (f *fakeFS) add(parentID, name string, size int64, isDir bool) string {
	id := name
	if parentID != "" {
		id = parentID + "/" + name
	}
	f.items[parentID] = append(f.items[parentID], domain.FileItem{
		ID: id, Name: name, Size: size, IsDir: isDir,
	})
	return id
}

func (f *fakeFS) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return f.items[parentID], nil
}

// buildGoldTree 构造与 115-auto 测试完全相同的树：
//
//	根/
//	  hhd800.com@ABP-123 中文字幕.mp4   5GB  有番号 → 改名 + 建目录 + 移入
//	  hhd800.com@ABP-123 中文字幕.nfo          元数据 → 跟随进新目录
//	  ABP-123.srt                             元数据（按新基名匹配）
//	  小样片.mp4                         5MB  小文件 → 删
//	  tiny.mp4                          20MB  小文件 → 删
//	  普通家庭录像.mp4                    2GB  无番号 → 不改名，但会被建目录移入
//	  FC2PPV-1234567 无码/                     命中「素人」→ 分类搬到 目标根/FC2
//	    FC2PPV-1234567.mp4              3GB
//	  空目录/                                   本来就是空 → 不该出现在计划里
//	  整理库/                                   分类目标根（就在源目录下）
func buildGoldTree() (*fakeFS, string, string) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "hhd800.com@ABP-123 中文字幕.mp4", 5*1024*mb, false)
	fs.add(root, "hhd800.com@ABP-123 中文字幕.nfo", 1024, false)
	fs.add(root, "ABP-123.srt", 2048, false)
	fs.add(root, "小样片.mp4", 5*mb, false)
	fs.add(root, "tiny.mp4", 20*mb, false)
	fs.add(root, "普通家庭录像.mp4", 2*1024*mb, false)
	sub := fs.add(root, "FC2PPV-1234567 无码", 0, true)
	fs.add(sub, "FC2PPV-1234567.mp4", 3*1024*mb, false)
	fs.add(root, "空目录", 0, true)
	target := fs.add(root, "整理库", 0, true)
	return fs, root, target
}

func buildPlan(t *testing.T, fs *fakeFS, sourceID, targetID string, deleteSmall bool) *moplan.Plan {
	t.Helper()
	cfg := Config{
		SourceDirID:  sourceID,
		TargetRootID: targetID,
		ActionType:   "move",
		DeleteSmall:  deleteSmall,
		SmallFileMB:  300,
		// 删小文件现在完全由「可删除类型 / 不删除类型」两张表决定，两张都留空
		// 就是「满足大小就删」。这里填上排除表，复现「字幕/NFO/海报不删」这一期望
		// （设置项的出厂默认值也是同一张表）。
		DeleteExcludeTypes: defaultMetadataExtensions,
		CleanEmptyDirs:     true,
		MaxDirs:            500,
		FileExtensions:     defaultVideoExtensions,
		MetadataExtensions: defaultMetadataExtensions,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	return plan
}

func byStage(plan *moplan.Plan) map[string][]moplan.PlanAction {
	out := map[string][]moplan.PlanAction{}
	for _, a := range plan.Actions {
		stage, _ := a.Metadata["stage"].(string)
		out[stage] = append(out[stage], a)
	}
	return out
}

func namesOf(actions []moplan.PlanAction, pick func(moplan.PlanAction) string) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, pick(a))
	}
	sort.Strings(out)
	return out
}

func TestPlanSchemeAndStages(t *testing.T) {
	fs, root, target := buildGoldTree()
	plan := buildPlan(t, fs, root, target, true)
	stages := byStage(plan)

	if plan.Scheme != moplan.SchemeJAV {
		t.Errorf("计划方案 = %q, 期望 %q", plan.Scheme, moplan.SchemeJAV)
	}
	if plan.TargetRootID != target {
		t.Errorf("计划目标根 = %q, 期望 %q", plan.TargetRootID, target)
	}

	// ── 阶段 1：删小文件（5MB 和 20MB 两个）──
	dels := stages[stageDeleteSmall]
	if len(dels) != 2 {
		t.Fatalf("删小文件条数 = %d, 期望 2", len(dels))
	}
	for _, a := range dels {
		if a.Kind != moplan.ActionKindDeleteFile {
			t.Errorf("删文件 kind = %q, 期望 %q", a.Kind, moplan.ActionKindDeleteFile)
		}
	}
	if got := namesOf(dels, func(a moplan.PlanAction) string { return a.SourceName }); strings.Join(got, ",") != "tiny.mp4,小样片.mp4" {
		t.Errorf("被删的小文件名 = %v", got)
	}
	// nfo(1KB)/srt(2KB) 也在阈值内，但它们的扩展名在「不删除的文件类型」表里，
	// 要跟着视频搬走 —— 不是小文件垃圾。
	if kept, _ := plan.Diagnostics["kept_by_exclude"].(int); kept != 2 {
		t.Errorf("命中排除表的小文件应被保留并计数，得到 %v", plan.Diagnostics["kept_by_exclude"])
	}
	if !anySkippedContains(plan, "不删除的文件类型") {
		t.Error("被排除表保留下来的文件要出现在 skipped 里让用户看得见")
	}
}

func TestPlanRename(t *testing.T) {
	fs, root, target := buildGoldTree()
	stages := byStage(buildPlan(t, fs, root, target, true))

	renames := stages[stageRename]
	// 视频和它的 nfo 都带番号，都要改；无番号的不改
	want := "hhd800.com@ABP-123 中文字幕.mp4,hhd800.com@ABP-123 中文字幕.nfo"
	if got := strings.Join(namesOf(renames, func(a moplan.PlanAction) string { return a.SourceName }), ","); got != want {
		t.Fatalf("需要改名的文件 = %q, 期望 %q", got, want)
	}
	bySrc := map[string]moplan.PlanAction{}
	for _, a := range renames {
		bySrc[a.SourceName] = a
	}
	mp4 := bySrc["hhd800.com@ABP-123 中文字幕.mp4"]
	if mp4.TargetName != "ABP-123.mp4" {
		t.Errorf("改名结果 = %q, 期望 ABP-123.mp4", mp4.TargetName)
	}
	// 同目录改名：目标父目录 == 源父目录（执行器据此走纯改名分支）
	if mp4.TargetParentID != mp4.SourceParentID {
		t.Errorf("同目录改名要求目标父目录 == 源父目录，得到 %q vs %q", mp4.TargetParentID, mp4.SourceParentID)
	}
	if got := bySrc["hhd800.com@ABP-123 中文字幕.nfo"].TargetName; got != "ABP-123.nfo" {
		t.Errorf("nfo 改名 = %q, 期望 ABP-123.nfo", got)
	}
}

func TestPlanEnsureDirAndMoveIn(t *testing.T) {
	fs, root, target := buildGoldTree()
	stages := byStage(buildPlan(t, fs, root, target, true))

	ensures := stages[stageMoveIn]
	// 目录名 = 改名后的去扩展名；普通家庭录像无番号但也建目录
	var ensureOnly []moplan.PlanAction
	for _, a := range ensures {
		if a.Kind == moplan.ActionKindEnsureDir {
			ensureOnly = append(ensureOnly, a)
		}
	}
	got := strings.Join(namesOf(ensureOnly, func(a moplan.PlanAction) string { return a.TargetName }), ",")
	if got != "ABP-123,普通家庭录像" {
		t.Fatalf("建目录的目标名 = %q, 期望 %q", got, "ABP-123,普通家庭录像")
	}
	for _, a := range ensureOnly {
		if a.SourceID != "" {
			t.Errorf("建目录不该有源对象，得到 %q", a.SourceID)
		}
	}

	var moves []moplan.PlanAction
	for _, a := range ensures {
		if a.Kind == moplan.ActionKindRelocate {
			moves = append(moves, a)
		}
	}
	if len(moves) != 2 {
		t.Fatalf("移入动作条数 = %d, 期望 2", len(moves))
	}
	ensureIDs := map[string]struct{}{}
	for _, a := range ensureOnly {
		ensureIDs[a.ID] = struct{}{}
	}
	for _, a := range moves {
		if !strings.HasPrefix(a.TargetParentID, moplan.RefPrefix) {
			t.Errorf("移入目标必须是 ref: 引用（依赖建目录的结果），得到 %q", a.TargetParentID)
		}
		if len(a.DependsOn) == 0 || a.DependsOn[0] != strings.TrimPrefix(a.TargetParentID, moplan.RefPrefix) {
			t.Errorf("移入动作要 declare 依赖同一个建目录动作：%v vs %q", a.DependsOn, a.TargetParentID)
		}
		if a.TargetName != a.SourceName {
			t.Errorf("移入不改名（改名已在阶段 2 做过）：%q vs %q", a.TargetName, a.SourceName)
		}
	}
	// 移入引用的应恰好是那两个建目录动作
	if len(moves) == 2 {
		for _, a := range moves {
			if _, ok := ensureIDs[strings.TrimPrefix(a.TargetParentID, moplan.RefPrefix)]; !ok {
				t.Errorf("移入引用了不存在的建目录动作：%q", a.TargetParentID)
			}
		}
	}
}

// 元数据跟随是本方案相对旧面板的改进之一：同目录的 srt/nfo/海报要跟着视频走。
func TestPlanMetadataFollowers(t *testing.T) {
	fs, root, target := buildGoldTree()
	plan := buildPlan(t, fs, root, target, true)

	followers, ok := plan.Diagnostics["meta_followers"].([]map[string]any)
	if !ok {
		t.Fatalf("meta_followers 必须是 []map[string]any（执行器对它做类型断言），得到 %T",
			plan.Diagnostics["meta_followers"])
	}
	if len(followers) != 2 {
		t.Fatalf("元数据跟随条目数 = %d, 期望 2（每个被移入的视频一条）", len(followers))
	}

	moveIDs := map[string]struct{}{}
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindRelocate {
			stage, _ := a.Metadata["stage"].(string)
			if stage == stageMoveIn {
				moveIDs[a.ID] = struct{}{}
			}
		}
	}
	var abp map[string]any
	for _, f := range followers {
		if depend, _ := f["depend_on"].(string); depend != "" {
			if _, ok := moveIDs[depend]; !ok {
				t.Errorf("跟随项要挂在移入动作上，得到 %q", depend)
			}
		}
		exts, _ := f["meta_exts"].([]string)
		if !containsStr(exts, "nfo") || !containsStr(exts, "srt") {
			t.Errorf("元数据扩展名不对：%v", exts)
		}
		if newBase, _ := f["new_base"].(string); newBase == "ABP-123" {
			abp = f
		}
	}
	if abp == nil {
		t.Fatal("没找到 ABP-123 的跟随项")
	}
	// 匹配基名要**新旧都给**：nfo/srt 会被阶段 2 一起改名（旧基名就匹配不上了），
	// 而执行器是在改名之后才回来找它们的。
	bases, _ := abp["match_bases"].([]string)
	if !containsStr(bases, "ABP-123") || !containsStr(bases, "hhd800.com@ABP-123 中文字幕") {
		t.Errorf("匹配基名要同时给旧名和新名，得到 %v", bases)
	}
}

func TestPlanRenameDir(t *testing.T) {
	fs, root, target := buildGoldTree()
	stages := byStage(buildPlan(t, fs, root, target, true))

	dren := stages[stageRenameDir]
	if len(dren) != 1 {
		t.Fatalf("需要改名的子目录数 = %d, 期望 1", len(dren))
	}
	a := dren[0]
	if a.SourceName != "FC2PPV-1234567 无码" {
		t.Errorf("被改名的目录 = %q", a.SourceName)
	}
	if a.TargetName != "FC2PPV-1234567" {
		t.Errorf("目录改名结果 = %q, 期望 FC2PPV-1234567", a.TargetName)
	}
	if a.TargetParentID != a.SourceParentID {
		t.Error("目录改名必须是同目录改名")
	}
}

// 目录名必须跟着**视频**走，不能被同目录里的广告文本带偏。
//
// 115-auto 取的是「目录里第一个文件」，真实库里第一个常常是广告 .txt。
// 那种文件没有番号，清理函数会原样返回（连中文一起），于是目录被改成一个广告标题。
func TestPlanRenameDirFollowsVideoNotAdFile(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	dir := fs.add(root, "SNOS-169.[4K]@R90s", 0, true)
	// 故意把广告文件放在前面（真实场景里列表就是按名字排的）
	fs.add(dir, "『 115-ED2k-百度网盘 』免费下载 169bt.com.txt", 1260, false)
	fs.add(dir, "snos00169pl.jpg", 857646, false)
	fs.add(dir, "169bbs.com@SNOS-169_[4K].mkv", 24*1024*mb, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		SmallFileMB: 300, CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}

	var renamedTo string
	for _, a := range plan.Actions {
		if stage, _ := a.Metadata["stage"].(string); stage == stageRenameDir && a.SourceID == dir {
			renamedTo = a.TargetName
		}
	}
	if renamedTo == "" {
		t.Fatal("目录应当被改名（里面有带番号的视频）")
	}
	if strings.Contains(renamedTo, "免费下载") || strings.Contains(renamedTo, "百度网盘") {
		t.Errorf("目录名被广告文件带偏了：%q", renamedTo)
	}
	if !strings.Contains(renamedTo, "SNOS-169") {
		t.Errorf("目录名应跟着视频走（含 SNOS-169），得到 %q", renamedTo)
	}
}

// 目录里只有广告文件、没有视频时，保持原样不动 —— 不能拿广告名去改目录。
func TestPlanRenameDirSkipsDirWithoutVideo(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	dir := fs.add(root, "老目录名", 0, true)
	fs.add(dir, "『 广告 』免费下载.txt", 1200, false)
	fs.add(dir, "poster.jpg", 200000, false)

	cfg := Config{SourceDirID: root, ActionType: "rename", SmallFileMB: 300, CleanEmptyDirs: true, MaxDirs: 500}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	for _, a := range plan.Actions {
		if stage, _ := a.Metadata["stage"].(string); stage == stageRenameDir && a.SourceID == dir {
			t.Errorf("没有视频的目录不该被改名，却改成了 %q", a.TargetName)
		}
	}
}

func TestPlanClassify(t *testing.T) {
	fs, root, target := buildGoldTree()
	stages := byStage(buildPlan(t, fs, root, target, true))

	cl := stages[stageClassify]
	var moves, dirs []moplan.PlanAction
	for _, a := range cl {
		if a.Kind == moplan.ActionKindRelocate {
			moves = append(moves, a)
		} else if a.Kind == moplan.ActionKindEnsureDir {
			dirs = append(dirs, a)
		}
	}
	if len(moves) != 1 {
		t.Fatalf("分类移动条数 = %d, 期望 1（FC2PPV 那个目录）", len(moves))
	}
	if moves[0].SourceName != "FC2PPV-1234567 无码" {
		t.Errorf("被分类的目录名 = %q", moves[0].SourceName)
	}
	// 分类动作的 target_name 必须是**改完名之后**的名字。带旧名去搬，执行器会在落地后
	// 照 target_name 再改一次名，把阶段 5 刚改好的目录名又改回去。
	if moves[0].TargetName != "FC2PPV-1234567" {
		t.Errorf("分类移动要带改名后的名字，得到 %q", moves[0].TargetName)
	}
	if len(dirs) != 1 {
		t.Fatalf("分类目标目录条数 = %d, 期望 1", len(dirs))
	}
	if dirs[0].TargetName != "FC2" {
		t.Errorf("分类目标目录名 = %q, 期望 FC2（来自规则 target_name）", dirs[0].TargetName)
	}
	if dirs[0].TargetParentID != target {
		t.Errorf("分类目标应建在任务的目标根下，得到 %q", dirs[0].TargetParentID)
	}
	if moves[0].TargetParentID != moplan.RefPrefix+dirs[0].ID {
		t.Errorf("分类移动应引用分类目标目录：%q vs %q", moves[0].TargetParentID, moplan.RefPrefix+dirs[0].ID)
	}
}

func TestPlanCleanEmptyAndScanStats(t *testing.T) {
	fs, root, target := buildGoldTree()
	plan := buildPlan(t, fs, root, target, true)
	stages := byStage(plan)

	// 被分类搬走的目录不该再单独报一条删空目录
	if n := len(stages[stageCleanEmpty]); n != 0 {
		t.Errorf("clean_empty 条数 = %d, 期望 0", n)
	}
	// 本来就空的目录不该出现在计划里（它是源目录直属的，且没有内容可搬）
	for _, a := range plan.Actions {
		if a.SourceName == "空目录" {
			t.Error("本来就空的目录不该出现在计划里")
		}
	}
	if got, _ := plan.Diagnostics["scanned_files"].(int); got != 7 {
		t.Errorf("扫到的文件数 = %v, 期望 7", plan.Diagnostics["scanned_files"])
	}
	// 3 个目录 = FC2PPV + 空目录 + 目标根「整理库」
	if got, _ := plan.Diagnostics["scanned_dirs"].(int); got != 3 {
		t.Errorf("扫到的目录数 = %v, 期望 3", plan.Diagnostics["scanned_dirs"])
	}
	if got, _ := plan.Diagnostics["delete_small"].(bool); !got {
		t.Error("计划要自带「本次要真删」标记")
	}
}

// 两张文件类型表都留空 → 只要大小满足就删，不管扩展名。
func TestPlanDeleteSmallBothTypeListsEmpty(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "ABP-123.mp4", 5*1024*mb, false) // 大文件，不删
	fs.add(root, "ad.txt", 1200, false)
	fs.add(root, "cover.jpg", 200000, false)
	fs.add(root, "sub.srt", 2000, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		DeleteSmall: true, SmallFileMB: 300,
		DeleteTypes: "", DeleteExcludeTypes: "", // 都不填
		CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	got := namesOf(byStage(plan)[stageDeleteSmall], func(a moplan.PlanAction) string { return a.SourceName })
	want := "ad.txt,cover.jpg,sub.srt"
	if strings.Join(got, ",") != want {
		t.Errorf("两张表都留空时应删掉所有满足大小的文件，得到 %v，期望 %v", got, want)
	}
}

// 「可删除的文件类型」是白名单：填了就只删这些类型。
func TestPlanDeleteSmallIncludeList(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "ABP-123.mp4", 5*1024*mb, false)
	fs.add(root, "ad.txt", 1200, false)
	fs.add(root, "cover.jpg", 200000, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		DeleteSmall: true, SmallFileMB: 300,
		DeleteTypes:    "txt", // 只删 txt
		CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	got := namesOf(byStage(plan)[stageDeleteSmall], func(a moplan.PlanAction) string { return a.SourceName })
	if strings.Join(got, ",") != "ad.txt" {
		t.Errorf("白名单只填 txt 时只该删 ad.txt，得到 %v", got)
	}
}

// 「不删除的文件类型」优先于「可删除的文件类型」。
func TestPlanDeleteSmallExcludeWinsOverInclude(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	fs.add(root, "ABP-123.mp4", 5*1024*mb, false)
	fs.add(root, "ad.txt", 1200, false)
	fs.add(root, "keep.txt", 1300, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		DeleteSmall: true, SmallFileMB: 300,
		DeleteTypes:        "txt",
		DeleteExcludeTypes: "keep.txt", // 排除表填的是完整文件名时不该当扩展名用
		CleanEmptyDirs:     true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	// 排除表按扩展名匹配，"keep.txt" 不是扩展名 → 两个 txt 都该被删
	got := namesOf(byStage(plan)[stageDeleteSmall], func(a moplan.PlanAction) string { return a.SourceName })
	if strings.Join(got, ",") != "ad.txt,keep.txt" {
		t.Errorf("排除表按扩展名匹配，得到 %v", got)
	}

	// 换成扩展名才生效
	cfg.DeleteExcludeTypes = "txt"
	p2 := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan2, err := p2.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	if n := len(byStage(plan2)[stageDeleteSmall]); n != 0 {
		t.Errorf("排除 txt 后不该有删除动作，得到 %d", n)
	}
}

// delete_small=False 时一条删文件动作都不该有。
func TestPlanDeleteSmallOff(t *testing.T) {
	fs, root, target := buildGoldTree()
	plan := buildPlan(t, fs, root, target, false)

	stages := byStage(plan)
	if n := len(stages[stageDeleteSmall]); n != 0 {
		t.Errorf("关闭清理小文件时不该有删除动作，得到 %d 条", n)
	}
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindDeleteFile {
			t.Errorf("不该出现 delete_file：%+v", a)
		}
	}
	if got, _ := plan.Diagnostics["delete_small"].(bool); got {
		t.Error("diagnostics.delete_small 应为 false")
	}
}

// 「原地重命名」模式：只做删小文件 + 番号改名 + 目录名跟随，
// 不建目录、不搬入、不分类（LitePan 的 rename 模式没有目标根）。
func TestPlanRenameModeSkipsMoveStages(t *testing.T) {
	fs, root, _ := buildGoldTree()
	cfg := Config{
		SourceDirID: root,
		ActionType:  "rename",
		DeleteSmall: true,
		SmallFileMB: 300,
		// 显式排除元数据扩展名 —— 删小文件现在完全由「可删除类型 / 不删除类型」两张
		// 表决定，两张都留空就是「满足大小就删」。这里填上排除表来复现
		// 「字幕/NFO/海报不删」这一期望（设置项出厂默认值也是这张表）。
		DeleteExcludeTypes: defaultMetadataExtensions,
		CleanEmptyDirs:     true,
		MaxDirs:            500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	stages := byStage(plan)
	if n := len(stages[stageDeleteSmall]); n != 2 {
		t.Errorf("删小文件应有 2 条，得到 %d", n)
	}
	if n := len(stages[stageRename]); n != 2 {
		t.Errorf("改名应有 2 条，得到 %d", n)
	}
	if n := len(stages[stageRenameDir]); n != 1 {
		t.Errorf("目录改名应有 1 条，得到 %d", n)
	}
	for _, stage := range []string{stageMoveIn, stageClassify} {
		if n := len(stages[stage]); n != 0 {
			t.Errorf("原地重命名模式不该有 %s 阶段的动作，得到 %d 条", stage, n)
		}
	}
}

// 分类目标根（及其祖先）不能被本任务改名或搬走 —— 用户把库根选在待整理目录
// 里面是很常见的配法。
//
// 保护范围**只到祖先，不含后代**（与 115-auto 一致）：库根自身不能被改名/搬走
// （路径型网盘上改了它，target_root_id 当场失效），但库**里面**的内容继续被正常
// 规范化是好事 —— 用户要的就是「库里的东西也归整」。
func TestPlanProtectsTargetRoot(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 源目录里放一个库根，库根里再放一个带番号的子目录
	lib := fs.add(root, "整理库", 0, true)
	inner := fs.add(lib, "FC2PPV-1234567 无码", 0, true)
	fs.add(inner, "FC2PPV-1234567.mp4", 3*1024*mb, false)
	fs.add(root, "ABP-123 中文字幕.mp4", 5*1024*mb, false)

	cfg := Config{
		SourceDirID: root, TargetRootID: lib, ActionType: "move",
		SmallFileMB: 300, CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}

	for _, a := range plan.Actions {
		// 库根自己既不能被改名（同目录 relocate）也不能被搬走
		if a.SourceID == lib {
			t.Errorf("分类目标根不该被本任务改动：%+v", a)
		}
		// 库里的东西不能被「分类移动」搬出去 —— 分类只扫源目录的一级子目录，
		// 加上库根本身受保护，所以一条都不该有
		if stage, _ := a.Metadata["stage"].(string); stage == stageClassify {
			t.Errorf("库根受保护时不该产生任何分类动作：%+v", a)
		}
	}
	if !anySkippedContains(plan, "分类目标根") {
		t.Error("受保护的目录要出现在 skipped 里让用户看得见")
	}
	// 库里那个子目录仍应被正常改名（规范化的收益要保留）
	var renamedInner bool
	for _, a := range plan.Actions {
		if a.SourceID == inner && a.TargetName == "FC2PPV-1234567" {
			renamedInner = true
		}
	}
	if !renamedInner {
		t.Error("库里的目录名仍应被规范化（保护只针对目标根自身，不针对其内容）")
	}
}

// 源目录 == 目标根时不能原地自搬。
func TestPlanClassifySkippedWhenSameRoot(t *testing.T) {
	fs, root, _ := buildGoldTree()
	cfg := Config{
		SourceDirID: root, TargetRootID: root, ActionType: "move",
		SmallFileMB: 300, CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	for _, a := range plan.Actions {
		stage, _ := a.Metadata["stage"].(string)
		if stage == stageClassify {
			t.Errorf("源目录与目标根相同时不该生成分类动作：%+v", a)
		}
	}
	if !anySkippedContains(plan, "源目录与目标根相同") {
		t.Error("应该有一条说明告诉用户为什么跳过了分类")
	}
}

// 每个 action 都必须带非空 group_uid：前端的计划分组完全依赖它，
// 缺了会退化成按标题分组，把不相关的条目混在一起。
func TestPlanEveryActionHasGroupUID(t *testing.T) {
	fs, root, target := buildGoldTree()
	plan := buildPlan(t, fs, root, target, true)
	if len(plan.Actions) == 0 {
		t.Fatal("计划为空，测试无意义")
	}
	for _, a := range plan.Actions {
		uid, _ := a.Metadata["group_uid"].(string)
		if strings.TrimSpace(uid) == "" {
			t.Errorf("动作 %s（%s）缺少 group_uid", a.ID, a.Kind)
		}
		scheme, _ := a.Metadata["jav_scheme"].(string)
		if scheme != moplan.SchemeJAV {
			t.Errorf("动作 %s 缺少 jav_scheme 标记，得到 %q", a.ID, scheme)
		}
	}
}

// 清理空目录的两条边界，都是容易被改错的地方：
//  1. 目录里的文件全被删掉 → 该目录变空 → 要清理
//  2. 目录里的文件只是**改了个名**（还在原地）→ 不算变空 → 绝不能清理
//
// 第 2 条曾经真的写错过：把「同目录改名」误当成「文件离开了目录」，
// 结果把分类目标根排进了 delete_empty_dir。
func TestPlanCleanEmpty(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	// 这是要被清掉的：里面只有一个 5MB 的小垃圾文件
	junkDir := fs.add(root, "废片", 0, true)
	fs.add(junkDir, "junk.mp4", 5*mb, false)
	// 这个**不该**被清：里面的文件只是改个名，还留在原地
	keepDir := fs.add(root, "老片", 0, true)
	fs.add(keepDir, "hhd800.com@ABP-123 中文字幕.mp4", 5*1024*mb, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		DeleteSmall: true, SmallFileMB: 300,
		CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}

	cleaned := map[string]bool{}
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindDeleteEmptyDir {
			cleaned[a.SourceID] = true
		}
	}
	if !cleaned[junkDir] {
		t.Error("文件全被删光的目录应当被清理")
	}
	if cleaned[keepDir] {
		t.Error("只是改名、文件还在原地的目录绝不能当成空目录清理掉")
	}
	if cleaned[root] {
		t.Error("源目录自身永远不能被清理（否则下次任务就没源了）")
	}
}

// 本来就空的目录不该被清理 —— 那不是整理的锅，用户可能刚建了个待用目录。
func TestPlanCleanEmptySkipsAlreadyEmptyDirs(t *testing.T) {
	fs := newFakeFS()
	const root = "/root"
	empty := fs.add(root, "空目录", 0, true)
	fs.add(root, "hhd800.com@ABP-123 中文字幕.mp4", 5*1024*mb, false)

	cfg := Config{
		SourceDirID: root, ActionType: "rename",
		DeleteSmall: true, SmallFileMB: 300,
		CleanEmptyDirs: true, MaxDirs: 500,
	}
	p := New(context.Background(), fs, 1, cfg, javrules.Defaults(), "task-1", nil, nil)
	plan, err := p.Build()
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	for _, a := range plan.Actions {
		if a.Kind == moplan.ActionKindDeleteEmptyDir && a.SourceID == empty {
			t.Error("本来就是空的目录不该被整理任务删掉")
		}
	}
}

func TestBuildRejectsMissingSourceDir(t *testing.T) {
	fs := newFakeFS()
	p := New(context.Background(), fs, 1, Config{ActionType: "move"}, javrules.Defaults(), "t", nil, nil)
	if _, err := p.Build(); err == nil {
		t.Error("缺少整理目录时应当报错，而不是生成一份空计划")
	}
}

func anySkippedContains(plan *moplan.Plan, needle string) bool {
	for _, s := range plan.Skipped {
		if reason, _ := s["reason"].(string); strings.Contains(reason, needle) {
			return true
		}
	}
	return false
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
