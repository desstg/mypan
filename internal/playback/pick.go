package playback

import (
	"litepan/internal/domain"
)

type Action uint8

const (
	ActionRedirect Action = iota
	ActionStream
)

type Intent struct {
	ForceProxy   bool
	FileName     string
	Inline       bool
	WebDAV       bool
	OriginalFile bool
	// SkipRangeLimit：绕过同账号 Range 并发限流（视频海报取帧用）。
	// 提取本身串行（内部已限并发 1），ffmpeg 会并发发起多个请求，
	// 若全部计入限流名额会互相饿死（一个流的并行分片即可占满）。
	SkipRangeLimit bool
}

// allowsPlaybackResolve 只有真实播放请求才允许增强工具替换原始下载地址。
// WebDAV 和视频海报取帧都需要原始文件字节。
func (intent Intent) allowsPlaybackResolve() bool {
	return !intent.WebDAV && !intent.OriginalFile
}

func PickAction(mode domain.DownloadMode, link domain.DownloadInfo, intent Intent) Action {
	if intent.ForceProxy || link.ForceProxy {
		return ActionStream
	}
	switch mode {
	case domain.DownloadRedirect:
		return ActionRedirect
	default:
		return ActionStream
	}
}
