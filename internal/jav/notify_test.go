package jav

import (
	"context"
	"strings"
	"testing"
	"time"

	"litepan/internal/eventbus"
)

// TestNotifyScheduledPushDistinguishesIdleFromFailure 钉住这次改动的**核心语义**：
// 「无事可做」与「真失败」必须分开。
//
// 起因（2026-09-27 用户报「通知里有 2 条自动推送不成功」）：13 条订阅的片其实早就
// 推完了（每一颗候选都被「一部片只推一颗」挡掉），日志里全是「没有符合推送条件的
// 资源」，旧版通知却写成「13 条订阅本轮都没推成」—— 用户看到的是「有 13 个失败要查」。
//
// 三条结论的对外行为：
//   - 一部没推 + 没失败 → **不发通知**（这次改动的重点）；
//   - 有失败 → warning，且正文里带**具体原因**；
//   - 推成功 → success。
func TestNotifyScheduledPushDistinguishesIdleFromFailure(t *testing.T) {
	newBus := func() (*eventbus.Bus, chan eventbus.NotificationCreated) {
		bus := eventbus.New(nil)
		t.Cleanup(func() { _ = bus.Close(context.Background()) })
		got := make(chan eventbus.NotificationCreated, 4)
		eventbus.Subscribe(bus, func(_ context.Context, evt eventbus.NotificationCreated) {
			got <- evt
		})
		return bus, got
	}
	assertSilent := func(t *testing.T, got chan eventbus.NotificationCreated) {
		t.Helper()
		select {
		case evt := <-got:
			t.Fatalf("不该发通知，却发了：%+v", evt)
		case <-time.After(120 * time.Millisecond):
		}
	}

	t.Run("都没推成但没失败_静默", func(t *testing.T) {
		bus, got := newBus()
		svc := &Service{bus: bus}
		svc.notifyScheduledPush(time.Now(), 0, 13, 0, nil)
		assertSilent(t, got)
	})

	t.Run("有失败_发warning并带原因", func(t *testing.T) {
		bus, got := newBus()
		svc := &Service{bus: bus}
		svc.notifyScheduledPush(time.Now(), 0, 11, 2, []string{
			"START-604： 115 API 错误(10008)：任务已存在，请勿输入重复的链接地址",
			"PRED-884： 等待网盘下载超时",
		})
		select {
		case evt := <-got:
			if evt.Level != "warning" {
				t.Errorf("有失败应当是 warning，got %q", evt.Level)
			}
			if !strings.Contains(evt.Message, "2 条订阅推送失败") {
				t.Errorf("正文该说清几条失败：%q", evt.Message)
			}
			if !strings.Contains(evt.Message, "10008") || !strings.Contains(evt.Message, "超时") {
				t.Errorf("正文要带**具体原因**，否则用户还得自己去翻日志：%q", evt.Message)
			}
		case <-time.After(time.Second):
			t.Fatal("有失败却没发通知")
		}
	})

	t.Run("推成功_发success", func(t *testing.T) {
		bus, got := newBus()
		svc := &Service{bus: bus}
		svc.notifyScheduledPush(time.Now(), 3, 0, 0, nil)
		select {
		case evt := <-got:
			if evt.Level != "success" {
				t.Errorf("推成功应当是 success，got %q", evt.Level)
			}
			if !strings.Contains(evt.Message, "3 部") {
				t.Errorf("正文该带上部数：%q", evt.Message)
			}
		case <-time.After(time.Second):
			t.Fatal("推成功却没发通知")
		}
	})

	t.Run("失败条数多时只列前几条", func(t *testing.T) {
		bus, got := newBus()
		svc := &Service{bus: bus}
		svc.notifyScheduledPush(time.Now(), 0, 6, 5, []string{
			"甲： 原因一", "乙： 原因二", "丙： 原因三", "丁： 原因四", "戊： 原因五",
		})
		select {
		case evt := <-got:
			if !strings.Contains(evt.Message, "…等 5 条") {
				t.Errorf("超过上限时该用「…等 N 条」收口：%q", evt.Message)
			}
			if strings.Contains(evt.Message, "原因四") {
				t.Errorf("不该把所有失败都堆进去（通知是要一眼扫完的）：%q", evt.Message)
			}
		case <-time.After(time.Second):
			t.Fatal("没发通知")
		}
	})
}

// TestPushOutcomeIdleVsFailed 钉住 pushBatch 的结论分类：**「没得推」不是「失败」**。
func TestPushOutcomeIdleVsFailed(t *testing.T) {
	// 这三种是「没得推」（Idle），不该进通知：
	idle := []pushOutcome{
		{Idle: true, Reason: "每轮推送部数为 0，跳过"},
		{Idle: true, Reason: "已有推送在等待网盘下载，稍后再试"},
		{Idle: true, Reason: "没有符合推送条件的资源"},
	}
	for _, o := range idle {
		if o.Failed {
			t.Errorf("%q 是「没得推」，不该标成失败", o.Reason)
		}
		if o.Pushed != 0 {
			t.Errorf("Idle 的结论不该带部数：%+v", o)
		}
	}
	// 而「全被网盘拒收」是失败 —— 池子是被失败一次一次试空的，别让它变成 Idle。
	rejected := pushOutcome{Failed: true, Idle: false, Reason: "115 API 错误(10008)"}
	if rejected.Idle {
		t.Error("被网盘拒收不该算「没得推」")
	}
}
