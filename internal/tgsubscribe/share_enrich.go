package tgsubscribe

import (
	"context"
	"strings"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

// maxPeekPerFlush 是一次窗口冲刷里最多偷看几条分享。
//
// 每次偷看都是一次真实的 115 接口调用。窗口里的候选可能一次有几十条
// （一条订阅追整季时尤其明显），不封顶就会在选优前打出一串请求 ——
// 那正是「短时间内获取次数太多」触发风控的姿势。排在前面的候选先看，
// 反正选优只取一条。
const maxPeekPerFlush = 8

// peekTimeout 是单次偷看的超时。窗口冲刷在派发循环里跑，不能被它拖住。
const peekTimeout = 8 * time.Second

// enrichShareRecords 用**不需要凭据**的 share/snap 补全分享链的真实文件名与大小。
//
// 为什么需要：分享链本身不带文件名与大小（`115:<code>` 就是全部信息），
// 现在只能拿正文首行当发布名猜、大小记 0。而 115 的 share/snap 是公开接口
// （实测 2026-09-17：裸请求、不带 Cookie 也能拿到完整响应），白得的信息没有理由不要。
//
// 拿真实值有两个实际好处：
//   - 匹配历史里显示的是真片名，而不是「第一行恰好是发布名」这种巧合；
//   - SizeBytes 参与 RankCandidates 的体积 tie-break（同分时大的优先），
//     分享链之前恒为 0，等于在排序上天生吃亏。
//
// 三条保守规则：
//   - 只处理 share_115（其它类型的名字本来就来自链接自带，不需要补）；
//   - 只在**窗口冲刷前**做（不是每条帖子入库时做）—— 后者会让 QukanMovie 这种
//     一页 20 条分享链的频道每次轮询多打 20 次请求；
//   - 失败一律忽略：补不上信息不该影响这次推送。
func (s *Service) enrichShareRecords(ctx context.Context, accountID int64, records []*domain.TGMatchRecord) {
	if s == nil || s.exec == nil || accountID <= 0 || len(records) == 0 {
		return
	}

	targets := make([]*domain.TGMatchRecord, 0, maxPeekPerFlush)
	for _, rec := range records {
		if len(targets) >= maxPeekPerFlush {
			break
		}
		if recordKind(rec) != KindShare115 {
			continue
		}
		// 大小已经有了就没什么可补的（名字一般也一起来了）。
		if rec.SizeBytes > 0 {
			continue
		}
		targets = append(targets, rec)
	}
	if len(targets) == 0 {
		return
	}

	enriched := 0
	for _, rec := range targets {
		if ctx.Err() != nil {
			return
		}
		share, err := parseShareResource(resourceFromRecord(rec))
		if err != nil {
			continue
		}
		peekCtx, cancel := context.WithTimeout(ctx, peekTimeout)
		meta, err := s.peekShare(peekCtx, accountID, share)
		cancel()
		if err != nil {
			s.log.Debug("tg subscribe peek share failed",
				"record", rec.ID, "code", share.code, "err", err)
			continue
		}
		if !applyPeekedMeta(rec, meta) {
			continue
		}
		enriched++
		if err := s.records.Update(ctx, rec); err != nil {
			s.log.Warn("tg subscribe save peeked share meta failed", "record", rec.ID, "err", err)
		}
	}
	if enriched > 0 {
		s.log.Info("tg subscribe enriched share candidates", "count", enriched, "account", accountID)
	}
}

// peekShare 通过驱动偷看一条分享。抽出来只为让测试能注入桩。
func (s *Service) peekShare(ctx context.Context, accountID int64, share shareRef) (driver.SharePeekResult, error) {
	var out driver.SharePeekResult
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		peeker, err := driverexec.Require[driver.ShareMetaPeeker](drv)
		if err != nil {
			// 这个驱动不支持偷看（不是 115）：静默跳过，不是错误。
			return nil
		}
		got, err := peeker.PeekShare(ctx, driver.SharePeekRequest{
			ShareCode:   share.code,
			ReceiveCode: share.password,
		})
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// applyPeekedMeta 把偷看结果写进记录，报告是否真的改动了什么。
//
// 名字的处理刻意保守：**只在新的名字看起来像发布名、且不比现有的差时才换**。
// 现在记录里的名字来自正文首行（NameSource=text），对很多频道来说那就是发布名，
// 换掉它反而可能更糟（分享标题是文件夹名，常带「合集」「更新至」这类前缀）。
func applyPeekedMeta(rec *domain.TGMatchRecord, meta driver.SharePeekResult) bool {
	changed := false

	if meta.TotalSize > 0 && rec.SizeBytes <= 0 {
		rec.SizeBytes = meta.TotalSize
		changed = true
	}

	candidate := strings.TrimSpace(meta.FileName)
	if candidate == "" {
		candidate = strings.TrimSpace(meta.Title)
	}
	// 只有新名字确实像发布名、且现在这条的名字不像时才替换。
	// 两者都像的话保留现有的 —— 正文首行通常已经带上了画质与季集，更完整。
	if candidate != "" && ParseReleaseName(candidate).LooksLikeRelease() &&
		!ParseReleaseName(rec.RawName).LooksLikeRelease() {
		rel := ParseReleaseName(candidate)
		rel.SizeBytes = rec.SizeBytes
		rec.RawName = candidate
		// dn = 链接/接口自带的名字，与 magnet 的 dn= 同一可信层（打分时不扣分）。
		rec.NameSource = "dn"
		rec.ParsedTitle = firstTitle(rel)
		if rel.Year != nil {
			rec.ParsedYear = *rel.Year
		}
		rec.Season = intOr(rel.Season, -1)
		rec.Episode = intOr(rel.Episode, -1)
		rec.EpisodeEnd = intOr(rel.EpisodeEnd, -1)
		rec.IsBatch = rel.IsBatch
		rec.Resolution = rel.Resolution
		rec.VideoCodec = rel.VideoCodec
		rec.SourceTag = rel.Source
		changed = true
	}
	return changed
}
