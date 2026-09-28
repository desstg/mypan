package jav

import (
	"context"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

// 推送结果的三个出口都汇到本文件：
//
//   - 定时那一轮（loops.go 的 pushAllSubscriptions）；
//   - 详情页手动推一颗磁链；
//   - 分享者弹窗里手动推一颗磁链。
//
// 后两个在服务端是**同一个方法**（PushMagnetManually），所以实际只有两处调用点。
// 订阅页那个「执行订阅」按钮反而不报 —— 用户正盯着界面看结果，再发一条通知是重复。

// notifyPush 往通知中心投一条推送结果。
//
// 走事件总线而不是直接调 notification.Service：番号模块本来就持有 bus（投递完成事件
// 走的是同一条），而通知服务订阅了 NotificationCreated（见 notification.Service.Register），
// 省一层依赖注入。
//
// ctx 固定用 Background：通知是在推送**已经有结论之后**才发的，不该因为 HTTP 连接断开
// （手动路径）或调度循环退出（定时路径）而丢掉。与 strm / quarktv 的写法一致。
func (s *Service) notifyPush(level, title, message string, refID int64) {
	if s == nil || s.bus == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.bus.Publish(context.Background(), eventbus.NotificationCreated{
		Level:    level,
		Category: domain.NotificationCategoryJavPush,
		Title:    title,
		Message:  message,
		RefID:    refID,
	})
}

// notifyScheduledPush 报一轮定时推送的结果。
//
// 三种结论分开报（2026-09-27 重写）：
//
//   - **有真失败** → warning，正文带**具体原因**（订阅名 + 网盘/上游的原话）。
//     这才是用户需要去查的那一条。
//   - **一部都没推出去，但也没失败** → **不发通知**。这就是「这些订阅的片都推过了」
//     或「没有新的合格资源」—— 是正常状态。以前这里发 warning
//     「N 条订阅本轮都没推成」，用户看到 13 条要查，实际 13 条全都无事可做。
//   - **推出去了** → success。有失败时也会一起写进正文（不掩盖）。
//
// level 取 success / warning 是照通知中心那几个图标来的（AdminNotificationBell 的
// levelIcon：success ✓、warning !、其余一律 i）。
func (s *Service) notifyScheduledPush(started time.Time, pushed, skipped, failed int, failedSubs []string) {
	at := clockOf(started)
	if failed > 0 {
		shown := capLines(failedSubs, maxNotifyFailures)
		message := at + " 执行推送任务，成功 " + strconv.Itoa(pushed) + " 部；" +
			strconv.Itoa(failed) + " 条订阅推送失败：" + strings.Join(shown, "；")
		if failed > len(shown) {
			message += "；…等 " + strconv.Itoa(failed) + " 条"
		}
		s.notifyPush("warning", "番号订阅推送有失败", message, 0)
		return
	}
	// 没推出去、也没失败：**静默**。「无事可做」不该打扰用户 ——
	// 定时推送是无人值守的，而这条通知以前每轮都发，含义却被他当成「出错了」。
	if pushed == 0 {
		return
	}
	s.notifyPush("success", "番号订阅推送完成",
		at+" 执行推送任务，成功推送 "+strconv.Itoa(pushed)+" 部", 0)
}

// maxNotifyFailures 是通知正文里最多列几条失败（多了扫不完，剩下的用「…等 N 条」收口）。
const maxNotifyFailures = 3

// capLines 只留前 n 条（通知是要一眼扫完的，堆十几条等于没写）。
// n 由调用方给（见 maxNotifyFailures）。
func capLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[:n]
}

// notifyManualPush 报一次手动推送成功。
//
// 只在**真的提交出去了**才调（判据见调用点）：幂等命中「这颗资源之前已经推送过了」
// 也是 OK=true，但那一次没往网盘塞任何东西，报成「成功推送」是假的。
func (s *Service) notifyManualPush(movie *domain.JavMovie, recordID int64) {
	label := ""
	if movie != nil {
		label = strings.TrimSpace(movie.Number)
		if label == "" {
			label = strings.TrimSpace(movie.Title)
		}
		if label == "" {
			label = movie.ID
		}
	}
	if label == "" {
		label = "未知影片"
	}
	s.notifyPush("success", "手动推送成功", clockOf(time.Now())+" 成功推送 "+label, recordID)
}

// clockOf 是通知里那个「几点」。两条路径要的形状一样，都是 HH:MM。
func clockOf(t time.Time) string { return t.Format("15:04") }
