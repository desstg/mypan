package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"litepan/internal/config"
	"litepan/internal/domain"
	"litepan/internal/logx"
	"litepan/internal/strm"
)

// 真机联调：拿**生产装配**跑一遍「在线刮削」，输出目录用临时的。
//
// 默认跳过 —— 它要联网打 JAVDB。跑它（PowerShell）：
//
//	$env:LITEPAN_LIVE_JAV="1"; $env:LITEPAN_LIVE_DATA="E:\claude code\LitePan-main\data";
//	go test ./internal/app/ -run TestLiveJavOnlineScrape -v -timeout 30m
//
// 留着它是因为这条链路上最要紧的几件事**只有真机看得见**：番号能不能从带质量
// 后缀的主干里取出来（`IPZZ-909-U` → `IPZZ-909`）、上游字段能不能落成一份
// `emby.Parse` 认的侧车、图与剧照会不会真的下下来、水印有没有按设置生效。
// 单元测试用的是桩，挡不住「我把上游响应的形状记错了」。
func TestLiveJavOnlineScrape(t *testing.T) {
	if os.Getenv("LITEPAN_LIVE_JAV") == "" {
		t.Skip("未设置 LITEPAN_LIVE_JAV，跳过真机测试")
	}
	dataDir := os.Getenv("LITEPAN_LIVE_DATA")
	if dataDir == "" {
		t.Skip("未设置 LITEPAN_LIVE_DATA（指向含 115 账号与 JAV 配置的 data 目录）")
	}
	taskName := os.Getenv("LITEPAN_LIVE_TASK")
	if taskName == "" {
		taskName = "116"
	}
	// maxWorks：--LIMIT 没给时的默认值。镜像真实目录（全量）时默认不截断，
	// 否则默认只跑两个写死的样本。
	maxWorks := 2
	if os.Getenv("LITEPAN_LIVE_SEED_DIR") != "" {
		maxWorks = 0
	}
	if v := os.Getenv("LITEPAN_LIVE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxWorks = n // 0 = 不截断
		}
	}

	ctx := context.Background()
	logs, err := logx.New(logx.Options{Level: "info", DisableFile: true})
	if err != nil {
		t.Fatalf("建日志失败：%v", err)
	}
	// 输出目录默认临时；给了 LITEPAN_LIVE_OUT 就用它（跑完能进去翻产物）。
	outDir := os.Getenv("LITEPAN_LIVE_OUT")
	if outDir == "" {
		outDir = t.TempDir()
	}
	cfg := config.Config{
		DataDir:    dataDir,
		DBPath:     filepath.Join(dataDir, "litepan.db"),
		StrmDir:    outDir, // 默认临时：绝不往用户媒体库里写
		ListenAddr: "127.0.0.1:0",
	}
	st, err := openStore(ctx, cfg, logs)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	t.Cleanup(func() { _ = st.db.Close() })
	core, err := wireCore(ctx, cfg, logs, st)
	if err != nil {
		t.Fatalf("wireCore: %v", err)
	}
	svc := wireServices(cfg, logs, st, core)

	task := liveJavTask(t, ctx, st, taskName)
	root := strm.TaskOutputDir(cfg.StrmDir, strm.TaskRelDir(task.GroupDir, task.OutputFolder))

	// 铺「光秃秃的 .strm」。两种模式：
	//
	//   LITEPAN_LIVE_SEED_DIR 非空 → **镜像**那个真实目录（只搬 `.strm`，
	//       不搬 json / nfo / 图）—— 这才是「对 116 全量跑一轮」该有的样子：
	//       上游那条路每一步都真打，而用户的媒体库一个字节都不动。
	//   否则 → 铺两个写死的样本（主干刻意带质量后缀，正是要验的那条）。
	var seeds []string
	if src := os.Getenv("LITEPAN_LIVE_SEED_DIR"); src != "" {
		werr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			// 搬 `.strm`（目标来源）、`.json` 与 `.nfo`（「已经在那儿」的那部分现场）。
			//
			// **不搬图片与字幕**：那些正是这一轮要补的东西，搬过去就测不出来了。
			// json/nfo 必须搬 —— 「上游查不到时用本地那份 json 兜底」那条路
			// （`localJavRepair`）没它们就跑不到，实测第一版就是漏了这一步，
			// 于是 13 部全被算成「跳过」。
			switch strings.ToLower(filepath.Ext(p)) {
			case ".strm", ".json", ".nfo":
			default:
				return nil
			}
			rel, rerr := filepath.Rel(src, p)
			if rerr != nil {
				return rerr
			}
			seeds = append(seeds, filepath.ToSlash(rel))
			return nil
		})
		if werr != nil {
			t.Fatalf("镜像 %s 失败：%v", src, werr)
		}
	} else {
		seeds = []string{"有码/IPZZ-909/IPZZ-909-U.strm", "有码/DSOD-028-4K/DSOD-028-4K.strm"}
	}
	// 截断**按作品目录**（不是按文件数）：一个目录可能有好几个 `.strm` / `.json` /
	// `.nfo`，按文件数截会把目录切一半 —— 那正是「目标数对不上」的来源。
	if maxWorks > 0 {
		seen := map[string]struct{}{}
		var dirs []string
		for _, rel := range seeds {
			d := filepath.ToSlash(filepath.Dir(rel))
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
		if len(dirs) > maxWorks {
			keep := map[string]struct{}{}
			for _, d := range dirs[:maxWorks] {
				keep[d] = struct{}{}
			}
			var filtered []string
			for _, rel := range seeds {
				if _, ok := keep[filepath.ToSlash(filepath.Dir(rel))]; ok {
					filtered = append(filtered, rel)
				}
			}
			seeds = filtered
		}
	}

	if srcDir := os.Getenv("LITEPAN_LIVE_SEED_DIR"); srcDir != "" {
		// **镜像**：`.strm` / `.json` / `.nfo` 原样搬过去（不覆盖成假内容）。
		for _, rel := range seeds {
			dst := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			data, rerr := os.ReadFile(filepath.Join(srcDir, filepath.FromSlash(rel)))
			if rerr != nil {
				t.Fatalf("读 %s 失败：%v", rel, rerr)
			}
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				t.Fatal(werr)
			}
		}
	} else {
		// 写死的样本：只铺一个空的 `.strm`（上游那条路自己会补 json / nfo）。
		for _, rel := range seeds {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("http://example.invalid/api/strm/play/1/x/t/y/n/a.mkv\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	start := time.Now()
	// LITEPAN_LIVE_MODE=rebuild → 走**重刮**（`RebuildJavArtifacts`）：它用本地那份
	// 侧车重建，**海报走异步裁切队列**（线上那条路），所以能验水印到底贴没贴上。
	// 在线刮削那条路在测试里验不了水印：队列没起，poster 全被丢弃。
	if os.Getenv("LITEPAN_LIVE_MODE") == "rebuild" {
		// 队列是 Start 里起的（线上那条路）；测试里自己起一次。
		svc.strm.StartPictureQueueForTest(ctx)
		if _, rerr := svc.strm.RebuildJavArtifacts(ctx, task, seeds); rerr != nil {
			t.Fatalf("重刮失败：%v", rerr)
		}
		// 等海报队列排空 —— 裁切是**异步**的，不等就去看文件会得出
		// 「水印没贴上」的错结论（实际是还没轮到）。
		if !svc.strm.WaitJavPostersForTest(3 * time.Minute) {
			t.Errorf("海报队列没排空（还剩 %d 张）", svc.strm.PendingJavPostersForTest())
		}
		for _, rel := range seeds {
			if !strings.EqualFold(filepath.Ext(rel), ".strm") {
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(filepath.Dir(rel)))
			thumb := filepath.Join(abs, "thumb.jpg")
			poster := filepath.Join(abs, "poster.jpg")
			t.Logf("%s: thumb=%v poster=%v", filepath.Dir(rel), fileExistsForTest(thumb), fileExistsForTest(poster))
		}
		return
	}
	res, err := svc.strmScrape.JavWallOnlineScrape(ctx, task.ID)
	if err != nil {
		t.Fatalf("在线刮削失败：%v", err)
	}
	t.Logf("结果：共 %d，补到 %d，跳过 %d，失败 %d，图 %d 张（耗时 %s）",
		res.Total, res.Scraped, res.Skipped, res.Failed, res.Images, time.Since(start).Round(time.Second))

	reported := map[string]struct{}{}
	for _, rel := range seeds {
		if !strings.EqualFold(filepath.Ext(rel), ".strm") {
			continue // 只按 `.strm` 报，一个目录报一次
		}
		dirKey := filepath.ToSlash(filepath.Dir(rel))
		if _, ok := reported[dirKey]; ok {
			continue
		}
		reported[dirKey] = struct{}{}
		abs := filepath.Join(root, filepath.FromSlash(filepath.Dir(rel)))
		entries, _ := os.ReadDir(abs)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("%s → %v", filepath.Dir(rel), names)

		// 侧车必须是 emby.Parse 认的那份（schema + number 两条硬校验）。
		// 侧车名与 .strm 同主干，所以剥扩展名再拼 .json。
		stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		jp := filepath.Join(abs, stem+".json")
		data, rerr := os.ReadFile(jp)
		if rerr != nil {
			t.Errorf("没有写出侧车 json：%v", rerr)
			continue
		}
		var probe struct {
			Schema string `json:"schema"`
			Number string `json:"number"`
			Title  string `json:"title"`
		}
		_ = json.Unmarshal(data, &probe)
		t.Logf("  %s: schema=%q number=%q title=%.30s 字节=%d",
			filepath.Base(jp), probe.Schema, probe.Number, probe.Title, len(data))
		if probe.Schema == "" || probe.Number == "" {
			t.Errorf("侧车缺 schema/number，emby.Parse 会把它当「不是侧车」跳过")
		}
	}
}

func liveJavTask(t *testing.T, ctx context.Context, st *storeBundle, name string) *domain.StrmTask {
	t.Helper()
	tasks, err := st.store.StrmTasks.List(ctx)
	if err != nil {
		t.Fatalf("列任务失败：%v", err)
	}
	for _, task := range tasks {
		if task.MediaKind == domain.StrmMediaKindJav && (name == "" || task.Name == name) {
			return task
		}
	}
	t.Skipf("库里没有名为 %q 的番号任务", name)
	return nil
}

var _ = slog.Default

// fileExistsForTest 是测试里的小判据（不引 os.Stat 的噪音）。
func fileExistsForTest(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
