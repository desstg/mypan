package strmscrape

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/mediaorganize/recognition"
	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
)

// AI 辅助识别（海报墙刮削的「补认」那一档）。
//
// # 它解决什么
//
// 规则解析「乱命名」很弱：目录叫 `护肝人 2026 WEB-DL` 还行，叫一堆发布组前缀、
// 或用外文原名时经常解析不出片名，TMDB 也就搜不到。这一档把**规则认不出的那批**
// 攒起来一次性问 AI，AI 回片名/年份后再去 TMDB 搜 —— **只补认不出的**，
// 规则能搜到的作品一秒都不多等。
//
// # 三条硬约束（用户明确要求）
//
//   - **用户没开 AI 不能出错**：开关就是「系统设置 → AI 整理」那份配置
//     （`aiorganize.Available()`）。没配时 `s.ai == nil` 或 `Available()==false`，
//     整条链路连同「攒待识别」这一步都不会进入，走的还是今天那条老路。
//   - **不能影响现有功能**：AI **只对本地目录源**生效（`run()` 里按 `src.isLocal()` 判）。
//     STRM 任务那套逐字不变 —— 任务刮削的速度与结果与改造前完全一致。
//   - **失败降级**：AI 请求失败（网络、超时、返回坏 JSON）只记一条日志，
//     待识别的那批按**原来那条**失败路径收尾（同一个失败原因、同一条通知）。
//
// # 为什么不复用 planner 的识别循环
//
// `mediaorganize/planner/recognition.go` 那条循环产出的是「整理动作」（要建目录、
// 改文件名），而这里要的是「按认出来的片名去搜 TMDB 再写 nfo/海报」。能复用的只有
// `recognition.Enhancer` 这个接口与「ErrUnavailable 静默」那条规矩，循环本身各是各的。

// AIEnhancer 是 AI 识别的最小依赖面（`*aiorganize.Service` 天然满足）。
//
// 用接口而不是直接持有 `*aiorganize.Service`：strmscrape 只需要「认片名」这一件事，
// 不需要认识它的实例管理 / 密钥脱敏 / 协议回退那一整套。同时它**可以为 nil**
// （测试、或某个装配路径没接上），nil 时行为与改造前一致。
type AIEnhancer interface {
	Available() bool
	EnhanceWithProgress(ctx context.Context, req recognition.BatchRequest, progress recognition.ProgressFunc) (recognition.BatchResult, error)
}

// aiEnabledFor 判断这次刮削要不要走 AI 补认。
//
// 四条同时成立才开：用户点了那颗按钮（`UseAI`）、源是**本地目录**、AI 已装配、
// AI 配好了。任何一条不成立都返回 false —— 而 false 意味着 `run()` 里连
// 「攒待识别」那一步都不进入，进度、失败计数、日志与改造前逐字相同。
//
// ⚠️ **任务源永远不开**（用户原话「不能影响现在所有的功能」）：任务刮削多等一次
// AI 网络往返就是行为变化，哪怕结果一样。抽成函数是为了让这条规矩能被直接测到
// （TestAIOnlyForLocalSource），而不是藏在 run() 的一行判断里。
func (s *Service) aiEnabledFor(src *wallSrc, req RunRequest) bool {
	return req.UseAI && src.isLocal() && s.ai != nil && s.ai.Available()
}

// aiPendingWork 是「规则没认出来、留给 AI 再试一次」的一部作品。
type aiPendingWork struct {
	// idx 是它在 works 里的下标 —— 结果写回时按它定位（WorkID 可能被模型改坏，
	// validateResults 只会校验「这个 id 在不在这一块里」）。
	idx     int
	g       workGroup
	display string // 给人看的名字（文件夹名 / 文件主干）
	itemID  string // 索引里的条目 id（进度条上的 CurrentItemID）
	reason  string // 规则那条路失败的原因（AI 也救不回来时原样报出来）
}

// aiWorkID 是发给模型的 works 标识。纯十进制下标最不容易被模型改形。
func aiWorkID(idx int) string { return fmt.Sprintf("w%d", idx) }

// recognitionWorkFor 把一部作品摊成 aiorganize 认识的形状。
//
// Directory 用**相对库根**的路径而不是绝对路径：绝对路径里带着用户的盘符与目录结构，
// 对识别没有帮助，少发一点也更省 token。
func recognitionWorkFor(p aiPendingWork) recognition.Work {
	dir := p.g.relKey
	if p.g.flatFile != "" {
		dir = filepath.ToSlash(filepath.Dir(p.g.relKey))
		if dir == "." {
			dir = ""
		}
	}
	// 候选标题取「规则从目录名解析出来的那部分」（通常为空 —— 非空时规则多半已经搜到了）。
	// 这里有比没有好：模型手里多一条线索，猜错的概率更低。
	parsed := rules.NormalizeParsedMedia(rules.ParseDirName(p.display))
	files := make([]recognition.File, 0, len(p.g.entries))
	for i, e := range p.g.entries {
		files = append(files, recognition.File{
			SourceID:     fmt.Sprintf("%s_%d", aiWorkID(p.idx), i+1),
			Name:         filepath.Base(e.absPath),
			RelativePath: filepath.ToSlash(e.relPath),
		})
	}
	return recognition.Work{
		WorkID:         aiWorkID(p.idx),
		Directory:      strings.Trim(strings.TrimSpace(dir), "/"),
		MediaTypeHint:  inferMediaType(p.g),
		CandidateTitle: strings.TrimSpace(parsed.Title),
		CandidateYear:  parsed.Year,
		Files:          files,
	}
}

// enhancePending 一次性把这批作品交给 AI，回一个「作品下标 → 结果」的表。
//
// 返回 nil 表示「AI 没用上」（没装配 / 没开 / 整批失败）：调用方据此按原失败路径收尾。
// 注意 **ErrUnavailable 不记日志** —— 那是「用户没开 AI」的正常状态，
// 把它写成 Warn 会在每次刮削时刷一行看起来像故障的日志（与 planner 同一条规矩）。
func (s *Service) enhancePending(ctx context.Context, pending []aiPendingWork) map[int]recognition.WorkResult {
	if s.ai == nil || len(pending) == 0 {
		return nil
	}
	byWorkID := make(map[string]int, len(pending))
	batch := recognition.BatchRequest{Works: make([]recognition.Work, 0, len(pending))}
	for _, p := range pending {
		byWorkID[aiWorkID(p.idx)] = p.idx
		batch.Works = append(batch.Works, recognitionWorkFor(p))
	}
	res, err := s.ai.EnhanceWithProgress(ctx, batch, func(state recognition.BatchProgress) {
		total := state.Total
		if total <= 0 {
			total = len(pending)
		}
		s.setProgress(func(p *Progress) {
			p.Message = fmt.Sprintf("AI 识别中：%d/%d", state.Completed, total)
		})
	})
	if err != nil {
		if !errors.Is(err, recognition.ErrUnavailable) {
			s.log.Warn("strm scrape ai recognition failed", "err", err)
		}
		return nil
	}
	out := make(map[int]recognition.WorkResult, len(res.Items))
	for _, item := range res.Items {
		if idx, ok := byWorkID[item.WorkID]; ok {
			out[idx] = item
		}
	}
	return out
}

// flushAIPending 处理循环里攒下的那批「规则没认出来」的作品。
//
// # 顺序为什么是「先问 AI，再按原样收尾」
//
// 循环里已经为每一部推进了 Done 并记下了失败原因。这里只补两件事：能救回来的写进去，
// 救不回来的把**原来那条**失败（同一个 stage、同一句 reason）交回 failures。于是：
//   - AI 有结果 → 失败少几条；
//   - AI 没结果 → 与改造前完全一致（通知、日志、Failed 计数都回到原值）。
//
// `pending` 为空时直接返回 —— 没开 AI、或全都一次搜到了，这条路径零开销。
func (s *Service) flushAIPending(
	ctx context.Context,
	client *tmdb.Client,
	src *wallSrc,
	root string,
	pending []aiPendingWork,
	failures *[]ScrapeFailure,
	interval time.Duration,
	overwrite bool,
) {
	if len(pending) == 0 {
		return
	}
	results := s.enhancePending(ctx, pending)
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return
		}
		res, ok := results[p.idx]
		title := strings.TrimSpace(res.Title)
		if ok && res.Recognized && title != "" {
			mediaType := strings.TrimSpace(res.MediaType)
			if mediaType != MediaTypeTV && mediaType != MediaTypeMovie {
				mediaType = inferMediaType(p.g)
			}
			s.setProgress(func(pr *Progress) {
				pr.CurrentItemID = p.itemID
				pr.Message = "AI 已识别，正在刮削：" + title
			})
			info, err := searchTMDBByRecognition(ctx, client, title, res.Year, mediaType)
			if err != nil {
				// 认出来了但 TMDB 搜不到：报 AI 那条（比原来的「无法解析标题」有用）
				reason := fmt.Sprintf("AI 识别为「%s」但 TMDB 未搜到：%v", title, err)
				*failures = append(*failures, s.logScrapeFailure(src, p.g, p.display, ScrapeFailureStageMatch, reason))
				s.setProgress(func(pr *Progress) {
					pr.Failed++
					pr.CurrentItemID = ""
					pr.Message = "AI 识别后匹配失败：" + p.display
				})
				time.Sleep(interval)
				continue
			}
			if err := s.writeMatched(ctx, client, p.g, *info, overwrite); err != nil {
				*failures = append(*failures, s.logScrapeFailure(src, p.g, title, ScrapeFailureStageWrite, err.Error()))
				s.setProgress(func(pr *Progress) {
					pr.Failed++
					pr.CurrentItemID = ""
					pr.Message = "写入失败：" + title
				})
				time.Sleep(interval)
				continue
			}
			s.upsertIndexItem(ctx, src, root, p.g)
			updated := buildItem(src.PosterRef(), root, p.g)
			s.setProgress(func(pr *Progress) {
				pr.ItemRevision++
				pr.UpdatedItem = &updated
				pr.CurrentItemID = ""
				pr.Message = "已刮削（AI 识别）：" + title
			})
			time.Sleep(interval)
			continue
		}
		// AI 没救回来（或整批失败）：按循环里记下的那条失败原样收尾。
		*failures = append(*failures, s.logScrapeFailure(src, p.g, p.display, ScrapeFailureStageMatch, p.reason))
		s.setProgress(func(pr *Progress) {
			pr.Failed++
			pr.CurrentItemID = ""
			pr.Message = "匹配失败：" + p.display
		})
		time.Sleep(interval)
	}
}

// searchTMDBByRecognition 用 AI 认出来的片名/年份去搜 TMDB。
//
// 与 `matchWork` 尾部同一套回退：剧集搜不到时再按电影搜一次（命名里没有 SxxExx
// 的作品很容易被判成剧集）；搜到之后补齐剧集总集数（与 matchWork 同一条规矩，
// 否则 `ep_tmdb` 会缺一档）。
func searchTMDBByRecognition(ctx context.Context, client *tmdb.Client, title string, year *int, mediaType string) (*tmdbInfo, error) {
	info, err := searchTMDBInfo(ctx, client, title, year, mediaType)
	if err != nil && mediaType == MediaTypeTV {
		info, err = searchTMDBInfo(ctx, client, title, year, MediaTypeMovie)
	}
	if err != nil {
		return nil, err
	}
	if info.EpisodeCount == 0 && info.MediaType == MediaTypeTV {
		if raw, _, lerr := client.LookupFull(ctx, info.TMDBID, MediaTypeTV); lerr == nil {
			if full, derr := decodeTMDBInfo(raw, MediaTypeTV); derr == nil && full.EpisodeCount > 0 {
				info.EpisodeCount = full.EpisodeCount
			}
		}
	}
	return info, nil
}
