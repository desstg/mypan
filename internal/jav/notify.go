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
// 「一部都没推出去」也要报，且降级成 warning：定时推送是无人值守的，用户唯一能知道
// 「这轮怎么没动静」的地方就是通知 —— 什么都不发的话，「没有新资源」和「整轮报错了」
// 在界面上长得一模一样。
//
// level 取 success / warning 是照通知中心那几个图标来的（AdminNotificationBell 的
// levelIcon：success ✓、warning !、其余一律 i）。定时这一轮是用户特意等的结果，
// 给 ✓ 比给 i 更贴合它的分量。
func (s *Service) notifyScheduledPush(started time.Time, pushed, skipped int) {
	at := clockOf(started)
	// skipped 是「这一轮没推成的订阅条数」（见 pushAllSubscriptions 里对 n == 0 的计数）。
	if pushed == 0 {
		message := at + " 执行推送任务，没有可推送的订阅"
		if skipped > 0 {
			message = at + " 执行推送任务，" + strconv.Itoa(skipped) + " 条订阅本轮都没推成"
		}
		s.notifyPush("warning", "番号订阅推送没推出去", message, 0)
		return
	}
	message := at + " 执行推送任务，成功推送 " + strconv.Itoa(pushed) + " 部"
	if skipped > 0 {
		message += "；另有 " + strconv.Itoa(skipped) + " 条订阅本轮没推成"
	}
	s.notifyPush("success", "番号订阅推送完成", message, 0)
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
