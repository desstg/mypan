package tgsubscribe

import (
	"context"
	"strings"
	"time"

	"litepan/internal/settings"
	"litepan/internal/tgsubscribe/preview"
)

// PageFetcher 是取消息层的接口。
//
// 抽成接口只有一个目的：让排程与翻页逻辑能在测试里用假数据驱动，不碰网络。
type PageFetcher interface {
	Fetch(ctx context.Context, username string, before int64) (*preview.Page, error)
}

// PageSearcher 可选：频道内关键词搜索。
//
// 与 PageFetcher 分开是因为它**不是所有实现都该有**的能力 —— 定时抓取那条路
// 用不到它，测试里的假抓取器也不必实现。用类型断言探测，避免给每个实现加空方法。
//
// ⚠️ 真实实现必须只在**用户手动触发**时用它。t.me/s/ 是给浏览器看的公开页面，
// 没有面向程序的配额；把它接进定时循环等于对每个频道反复发搜索请求，很快会被限流。
type PageSearcher interface {
	Search(ctx context.Context, username, keyword string) (*preview.Page, error)
}

// previewFor 按当前设置构造预览客户端。设置变了就重建 —— 代理与超时都可能改。
func (s *Service) previewFor() PageFetcher {
	if s == nil || s.settings == nil {
		return nil
	}
	// 测试注入口：生产代码从不设置 fetcher（New 里留空）。
	//
	// 有了它，定时抓取与历史搜索两条路都能用假数据驱动 —— 否则每个测试都得
	// 真去连 t.me，既慢又不稳。判断放在 settings 检查之后：生产里 settings 永远在，
	// 而测试里两者都注入，这条分支不会掩盖任何真实配置。
	if s.fetcher != nil {
		return s.fetcher
	}
	// 代理走全局设置（「系统设置 → 其他设置 → 网络代理」），与 TMDB 共用同一份。
	proxy := settings.ProxyURL(s.settings)
	timeout := s.previewTimeout()

	key := proxy + "\x00" + timeout.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.preview != nil && s.previewKey == key {
		return s.preview
	}
	s.preview = preview.NewClient(preview.ClientOptions{ProxyURL: proxy, Timeout: timeout})
	s.previewKey = key
	return s.preview
}

// resetPreviewClient 丢弃缓存的客户端，下次 previewFor 会按新设置重建。
func (s *Service) resetPreviewClient() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.preview = nil
	s.previewKey = ""
	s.mu.Unlock()
}

// previewTimeout 是单个请求的超时。
func (s *Service) previewTimeout() time.Duration {
	seconds := 20
	if s != nil && s.settings != nil {
		if v := s.settings.Int(settings.KeyTGPreviewTimeoutSec); v > 0 {
			seconds = v
		}
	}
	return time.Duration(seconds) * time.Second
}

// pollInterval 是每个频道的基准抓取间隔（实际间隔还要按优先级与频道数放大）。
func (s *Service) pollInterval() time.Duration {
	seconds := 600
	if s != nil && s.settings != nil {
		if v := s.settings.Int(settings.KeyTGPreviewPollIntervalSec); v > 0 {
			seconds = v
		}
	}
	return time.Duration(seconds) * time.Second
}

// backfillPages 是新频道首次订阅时往回翻的页数。
func (s *Service) backfillPages() int {
	pages := 1
	if s != nil && s.settings != nil {
		pages = s.settings.Int(settings.KeyTGPreviewBackfillPages)
	}
	if pages < 0 {
		pages = 0
	}
	if pages > maxBackfillPages {
		pages = maxBackfillPages
	}
	return pages
}

// requestGap 是两次请求之间的强制间隔。全局串行 + 固定间隔是唯一的自我保护 ——
// t.me/s/ 是给浏览器看的公开页面，没有面向程序的配额。
func (s *Service) requestGap() time.Duration {
	ms := 2000
	if s != nil && s.settings != nil {
		ms = s.settings.Int(settings.KeyTGPreviewRequestGapMs)
	}
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}

// lastPollAt 读最近一次成功抓取时间，用于状态展示。
func (s *Service) lastPollAt() string {
	if s == nil || s.settings == nil {
		return ""
	}
	return strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotLastPollAt))
}

// saveLastPollAt 记录最近一次成功抓取的时间。
func (s *Service) saveLastPollAt(ctx context.Context, at time.Time) {
	if s == nil || s.settings == nil {
		return
	}
	if err := s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGBotLastPollAt: at.UTC().Format(time.RFC3339),
	}); err != nil {
		s.log.Warn("tg subscribe save last poll time failed", "err", err)
	}
}
