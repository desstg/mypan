package tgsubscribe

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"litepan/internal/tgsubscribe/preview"
)

// errNoFetcher 表示抓取器没构造出来（通常是设置读取失败）。
var errNoFetcher = errors.New("TG 预览抓取器不可用，请到「系统设置 → 其他设置 → 网络代理」检查代理")

// 翻页常量。
const (
	// hardCatchUpPages 是「追新」时往回翻的硬上限（每页 20 条 → 最多 200 帖）。
	//
	// 追新正常情况下只翻一页就接上了；会继续往回翻只有一种原因：两次轮询之间
	// 频道发了超过 20 条。停机太久（比如代理挂了几天）时不能无限往回翻 ——
	// 既会把 t.me 打疼，也会一次性灌进几百条老资源。翻到上限就停，并把
	// 「更早的帖子未处理」写进频道状态让用户看见。
	hardCatchUpPages = 10

	// maxBackfillPages 是用户可配的回填页数的钳制上限。
	maxBackfillPages = 20

	// recentRescanPosts 是每轮额外重扫的最新帖子数，用于兜住「先发占位、
	// 几分钟后补上磁链」这种编辑场景。它们在最新页里，不产生额外请求，
	// 代价只是几条会被唯一索引挡掉的重复落库。
	recentRescanPosts = 3
)

// pageFetcher 抽成函数类型，让翻页逻辑能在测试里用假数据驱动，不碰网络。
type pageFetcher func(before int64) (*preview.Page, error)

// catchUpOptions 是一次追新/回填的停止条件。
type catchUpOptions struct {
	// MaxPages 是往回收敛的页数上限。追新用 hardCatchUpPages，回填用用户配置值。
	MaxPages int
	// RecentRescan 是额外重扫的最新帖子条数。
	RecentRescan int
}

// catchUpPlan 按频道当前的进度算出该用哪种模式。
//
// 纯函数，不碰网络也不碰数据库，所以可以直接单测 —— 「新频道必须回填而不是
// 无限往回翻」这条最容易写错的规则由它单独守着。
func catchUpPlan(lastMessageID int64, configuredBackfillPages int) catchUpOptions {
	if lastMessageID <= 0 {
		pages := configuredBackfillPages
		if pages < 0 {
			pages = 0
		}
		if pages > maxBackfillPages {
			pages = maxBackfillPages
		}
		// 回填页数为 0 表示「只追新」，但新频道没有 lastID 可接，至少要抓一页，
		// 否则这个频道永远停在零条。所以下限是 1。
		if pages < 1 {
			pages = 1
		}
		return catchUpOptions{MaxPages: pages, RecentRescan: 0}
	}
	return catchUpOptions{MaxPages: hardCatchUpPages, RecentRescan: recentRescanPosts}
}

// walkResult 是翻页收集的结果。
type walkResult struct {
	Posts []preview.Post // 按 MessageID 升序，已去重
	Pages int
	// Truncated 表示是撞到页数上限停的，更早的帖子没处理。
	Truncated bool
	// Backfilled 表示这次是「新频道首次回填」（上次没有任何进度），
	// 而不是「追新时没接上」。两者的 Truncated 含义完全不同：
	// 前者是用户配置的正常行为，后者才是「可能漏帖」需要提示的异常。
	Backfilled bool
	// NewestID 是本轮见过的最大 message id（含被跳过的），用于推进频道进度。
	NewestID int64
}

// walkPages 从最新一页开始往回翻，收集 lastMessageID 之后的帖子。
//
// **增量追新与历史回填共用这一条路径**，区别只在 catchUpPlan 给出的 MaxPages：
// 追新模式正常情况下第一页的最小 id 就 ≤ lastID，循环立刻结束（零次回退）；
// 只有真的断档了才会继续往回翻。回填模式 lastID 为 0，会一路翻满配置的页数。
//
// 停止条件三选一：已接上连续区间 / 没有更早的页 / 撞到页数上限。
func walkPages(ctx context.Context, fetch pageFetcher, username string, expectChatID, lastMessageID int64, opt catchUpOptions, gap time.Duration) (walkResult, error) {
	result := walkResult{Backfilled: lastMessageID <= 0}
	if fetch == nil {
		return result, errNoFetcher
	}

	seen := make(map[int64]struct{}, 64)
	before := int64(0) // 0 = 最新页

	for {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		page, err := fetch(before)
		if err != nil {
			return result, err
		}
		result.Pages++

		// 身份核对：@用户名 在 Telegram 上会被回收再分配（改名、删号、重新注册）。
		// 抓到的必须还是当初那个频道，否则等于把另一个人的帖子当成订阅源。
		if expectChatID != 0 && page.ChannelID != 0 && page.ChannelID != expectChatID {
			return result, fmt.Errorf("该地址现在指向另一个频道（期望 %s，实际 %d）",
				strconv.FormatInt(expectChatID, 10), page.ChannelID)
		}
		if len(page.Posts) == 0 {
			return result, nil
		}

		oldestOnPage := page.Posts[0].Message.MessageID
		for _, p := range page.Posts {
			id := p.Message.MessageID
			if id > result.NewestID {
				result.NewestID = id
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			if !isNewPost(id, lastMessageID, opt.RecentRescan) {
				continue
			}
			result.Posts = append(result.Posts, p)
		}

		// 已经接上上次处理到的位置 —— 连续性建立，可以停了。
		//
		// ⚠️ 判据必须是 <= 而不是 == lastMessageID+1：帖子被删除会在 id 序列上
		// 留下永久空洞，要求严格 +1 会永远翻不到头（每轮都白翻到硬上限）。
		if oldestOnPage <= lastMessageID {
			break
		}
		if page.PrevBefore == 0 {
			// 翻到频道可见历史的最早一页了。
			break
		}
		if opt.MaxPages > 0 && result.Pages >= opt.MaxPages {
			result.Truncated = true
			break
		}

		// 往回退一页。游标传本页最小 id —— 实测无法区分 before 的「含/不含」
		// 边界语义，重复一条的代价是零（上面的 seen 会挡掉），漏一条是永久丢帖。
		before = oldestOnPage
		if gap > 0 && !sleepCtx(ctx, gap) {
			return result, ctx.Err()
		}
	}

	// 升序处理：高优先级频道先落库、先占去重位，匹配与画质的先来后到依赖这个顺序。
	sort.Slice(result.Posts, func(i, j int) bool {
		return result.Posts[i].Message.MessageID < result.Posts[j].Message.MessageID
	})
	return result, nil
}

// isNewPost 判断一条帖子是否要处理。
func isNewPost(id, lastMessageID int64, recentRescan int) bool {
	if id > lastMessageID {
		return true
	}
	if recentRescan <= 0 {
		return false
	}
	// 已处理过的帖子也允许重扫最近几条 —— 频道常「先发占位、过几分钟补磁链」，
	// 旧路径靠 edited_channel_post 更新能拿到，网页预览只能靠重扫。
	return id > lastMessageID-int64(recentRescan)
}
