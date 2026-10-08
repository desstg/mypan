package strmscrape

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/mediaorganize/tmdb"
)

// 存量补抓：给**已经刮过**的作品补上后来才有的那批数据（背景图 / 剧照 / 演员 /
// 评分 / 时长 / 完整 nfo）。
//
// # 为什么需要它
//
// 正常刮削在 `missing_only` 策略下会跳过「已有 nfo + 已有海报」的作品
// （见 item.go 的 workNeedsScrape）—— 那是**对的**，否则每次刮削都要全量重跑。
// 但结果是：加了新抓取范围之后，库里已经有的那些作品永远拿不到新数据，
// 只能靠用户把写入策略切成「覆盖」再全量重刮一次（那会把所有 nfo 重写）。
//
// 所以给一个**独立入口**：只补缺，不覆盖已有正文（writeMatchedOpts 的 backfillOnly）。

// BackfillRequest 是「补齐剧照与演员」的入参。
type BackfillRequest struct {
	StrmTaskID int64 `json:"strm_task_id"`
}

// BackfillImages 扫一遍任务，给缺新数据的作品补抓。
//
// 与 RunAsync 同构：起一个后台任务、复用同一份 Progress（前端那套进度轮询直接用）。
// 也共用同一条 operationMu —— 与正常刮削**不能并发**（两套都会写同一批目录）。
func (s *Service) BackfillImages(ctx context.Context, req BackfillRequest) error {
	if req.StrmTaskID <= 0 {
		return domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	if _, _, err := s.resolveTask(ctx, req.StrmTaskID); err != nil {
		return err
	}
	if err := s.requireTmdbTask(ctx, req.StrmTaskID); err != nil {
		return err
	}
	if s.newTMDBClient() == nil {
		return domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	_ = ctx // 后台任务不随启动请求结束
	return s.startAsyncOperation(req.StrmTaskID, 0, "准备补齐", "补齐完成", "strm backfill failed", func(runCtx context.Context) error {
		return s.backfill(runCtx, req.StrmTaskID)
	})
}

func (s *Service) backfill(ctx context.Context, strmTaskID int64) error {
	task, root, err := s.resolveTask(ctx, strmTaskID)
	if err != nil {
		return err
	}
	failures := make([]ScrapeFailure, 0)
	defer func() {
		s.notifyScrapeFailures(task, failures)
	}()
	client := s.newTMDBClient()
	if client == nil {
		return domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key，请先在设置中填写")
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	works, err := scanWorks(root)
	if err != nil {
		return err
	}
	works = filterWorksByScope(works, s.GetScope(strmTaskID).ExcludedDirs)

	// 先挑出要处理的（这一步只读盘，很便宜），进度条才有总数。
	targets := make([]workGroup, 0, len(works))
	for _, g := range works {
		if backfillNeeded(g, resolveWorkMediaType(g)) {
			targets = append(targets, g)
		}
	}
	if len(targets) == 0 {
		s.setProgress(func(p *Progress) {
			p.Total = 0
			p.Done = 0
			p.Skipped = 0
			p.Failed = 0
			p.CurrentItemID = ""
			p.Message = "没有需要补齐的作品"
			p.Error = ""
		})
		return nil
	}
	s.setProgress(func(p *Progress) {
		p.Total = len(targets)
		p.Done = 0
		p.Skipped = 0
		p.Failed = 0
		p.Error = ""
		p.Message = fmt.Sprintf("待补齐 %d 部", len(targets))
	})

	interval := time.Duration(s.GetSettings().TmdbRequestIntervalMS) * time.Millisecond
	if interval < 200*time.Millisecond {
		interval = 300 * time.Millisecond
	}

	for i, g := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		mediaType := resolveWorkMediaType(g)
		displayName := workDisplayName(g)
		s.setProgress(func(p *Progress) {
			p.CurrentItemID = pathToItemID(g.relKey)
			p.Message = "正在补齐：" + displayName
		})

		if err := s.backfillOne(ctx, client, root, g, mediaType); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			failures = append(failures, s.logScrapeFailure(task, g, displayName, ScrapeFailureStageWrite, err.Error()))
			s.setProgress(func(p *Progress) {
				p.Done = i + 1
				p.Failed++
				p.CurrentItemID = ""
				p.Message = "补齐失败：" + displayName
			})
			time.Sleep(interval)
			continue
		}
		s.upsertIndexItem(ctx, strmTaskID, root, g)
		updated := buildItem(strmTaskID, root, g)
		s.setProgress(func(p *Progress) {
			p.Done = i + 1
			p.CurrentItemID = ""
			p.ItemRevision++
			p.UpdatedItem = &updated
			p.Message = "已补齐：" + displayName
		})
		time.Sleep(interval)
	}
	s.setProgress(func(p *Progress) {
		p.CurrentItemID = ""
		p.Message = fmt.Sprintf("补齐完成：成功 %d，跳过 %d，失败 %d",
			p.Done-p.Skipped-p.Failed, p.Skipped, p.Failed)
	})
	return nil
}

// backfillOne 处理一部作品。
//
// # 已经有 TMDB ID 的直接用它，不重新搜索
//
// 这是**关键**：重新搜索有可能挑到另一部片（年份差一年、同名），而用户的库里那份
// nfo 记着的 ID 是上次匹配的结论（可能还是他手工「重新匹配」选定的）。补齐是
// 「把缺的图与字段补上」，**不是重新匹配** —— 想换匹配有专门的「重新匹配」入口。
//
// 没有 ID 的（nfo 缺失或手写）才走 matchWork 那套搜索。
func (s *Service) backfillOne(ctx context.Context, client *tmdb.Client, root string, g workGroup, mediaType string) error {
	var info *tmdbInfo
	if meta, ok := readWorkNFOMeta(g, mediaType); ok && strings.TrimSpace(meta.TMDBID) != "" {
		got, err := lookupTMDBInfo(ctx, client, meta.TMDBID, mediaType)
		if err != nil {
			return fmt.Errorf("按已有 TMDB ID %s 取详情失败：%w", meta.TMDBID, err)
		}
		// nfo 里的标题/年份是用户库里那份（可能是他改过的），优先用它。
		if meta.Title != "" {
			got.Title = meta.Title
		}
		if meta.Year != nil {
			got.Year = meta.Year
		}
		got.Doubt = false // 已有 ID 说明上次匹配已经定了，补齐不该把它变回「存疑」
		info = got
	} else {
		matched, err := s.matchWork(ctx, client, g)
		if err != nil {
			return fmt.Errorf("匹配失败：%w", err)
		}
		info = matched
	}
	if info == nil || strings.TrimSpace(info.TMDBID) == "" {
		return fmt.Errorf("未取得有效的 TMDB 结果")
	}
	// backfillOnly=true：图只补缺的，nfo 把磁盘上已有那份**合并**进来再写
	// （见 mergeNFOInput —— 已有的一律不动，只补缺）。
	if _, err := s.writeMatchedOpts(ctx, client, g, *info, false, true, true); err != nil {
		return err
	}
	return nil
}

// backfillNeeded 判断一部作品要不要补。
//
// 判据是**「有 nfo 但缺新数据」**：
//   - 没有 nfo —— 那是「还没刮过」，走正常刮削（`workNeedsScrape` 已经会挑它），
//     补齐不该抢它的活；
//   - 有 nfo，但缺 `extrafanart/`（剧照）**或** nfo 里没有 `<actor>`（演员）
//     **或** 缺 `fanart.jpg`（背景图）—— 就是这次要补的。
//
// 用 `<actor>` 当「nfo 是新版」的判据：旧版简版 nfo 只有 title/year/tmdbid/plot
// 四个元素，一个 `<actor>` 都不会有；新版至少写演员（TMDB 上几乎每部片都有演员表）。
func backfillNeeded(g workGroup, mediaType string) bool {
	if !workHasNFO(g, mediaType) {
		return false
	}
	if !hasNFOActors(g, mediaType) {
		return true
	}
	if g.flatFile != "" {
		// 平铺布局没有 extrafanart 目录（目录级约定），只判背景图。
		return !fileExists(filepath.Join(filepath.Dir(g.flatFile), strings.TrimSuffix(filepath.Base(g.flatFile), filepath.Ext(g.flatFile))+"-fanart.jpg"))
	}
	if !fileExists(filepath.Join(g.absDir, "fanart.jpg")) {
		return true
	}
	return !hasLocalStills(g.absDir)
}

// hasNFOActors 报告这份 nfo 里有没有 `<actor>`。
//
// 只解那一个元素（用一个只声明它的最小结构体），不建整个 tmdbNFO ——
// 这一步要对全库每一部作品跑一次，解整份 nfo 是白费。
func hasNFOActors(g workGroup, mediaType string) bool {
	for _, p := range workNFOCandidates(g, mediaType) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var probe struct {
			Actors []struct{} `xml:"actor"`
		}
		if xml.Unmarshal(data, &probe) != nil {
			continue
		}
		if len(probe.Actors) > 0 {
			return true
		}
		return false
	}
	return false
}

// hasLocalStills 报告 extrafanart/ 里有没有图。
func hasLocalStills(absDir string) bool {
	dir := filepath.Join(absDir, emby.ExtraFanartDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && isImageExt(filepath.Ext(e.Name())) {
			return true
		}
	}
	return false
}
