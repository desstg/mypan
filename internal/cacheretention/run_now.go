package cacheretention

import "strconv"

type RunNowResult struct {
	State             string
	StartupRemaining  int
	RetryAfterSeconds int
	CacheTTLMinutes   int
}

func (r RunNowResult) Message() string {
	switch r.State {
	case "already_running":
		return "任务已在执行中"
	case "blocked_by_strm":
		return "同账号有其他任务正在占用（STRM 或媒体整理），已加入队列，占用结束后自动执行"
	case "queued_account":
		return "同账号的缓存任务正在执行，已加入队列，前一个任务完成后自动执行"
	case "cache_disabled":
		return "该账号目录缓存已关闭（TTL=0），缓存保持任务无法生效，请在账号或全局设置中开启缓存"
	case "too_soon":
		if r.CacheTTLMinutes > 0 && r.RetryAfterSeconds > 0 {
			return "账号缓存 " + strconv.Itoa(r.CacheTTLMinutes) + " 分钟，约 " + formatRetryAfter(r.RetryAfterSeconds) + " 后可再试"
		}
		return "缓存冷却中，请稍后再试"
	case "queued_startup":
		return "已加入执行队列，启动退避结束后（约 " + strconv.Itoa(r.StartupRemaining) + " 秒）自动执行"
	case "running":
		return "已触发执行"
	default:
		return "已触发执行"
	}
}
