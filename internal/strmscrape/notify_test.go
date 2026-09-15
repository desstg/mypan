package strmscrape

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

func TestNotifyScrapeFailuresPublishesBellDetails(t *testing.T) {
	t.Parallel()

	bus := eventbus.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = bus.Close(ctx)
	})

	gotEvent := make(chan eventbus.NotificationCreated, 1)
	eventbus.Subscribe(bus, func(_ context.Context, evt eventbus.NotificationCreated) {
		gotEvent <- evt
	})

	failures := []ScrapeFailure{
		{Stage: ScrapeFailureStageMatch, Name: "天龙八部", Path: "电视剧/天龙八部 (2003)", Reason: "没有标题和年份完全匹配的结果"},
		{Stage: ScrapeFailureStageWrite, Name: "海贼王", Path: "动漫剧/海贼王 (1999)", Reason: "下载第 2 季海报：连接超时"},
	}
	svc := &Service{bus: bus}
	svc.notifyScrapeFailures(&domain.StrmTask{ID: 7, AccountID: 3, Name: "媒体库"}, failures)

	select {
	case evt := <-gotEvent:
		if evt.Category != domain.NotificationCategoryStrmScrapeWarn || evt.Title != "STRM 刮削部分失败" {
			t.Fatalf("通知分类或标题不正确：%+v", evt)
		}
		parts := strings.SplitN(evt.Message, scrapeFailureDetailSep, 2)
		if len(parts) != 2 || !strings.Contains(parts[0], "共 2 部作品") {
			t.Fatalf("通知摘要不正确：%q", evt.Message)
		}
		var details []ScrapeFailure
		if err := json.Unmarshal([]byte(parts[1]), &details); err != nil {
			t.Fatalf("通知明细不是有效 JSON：%v", err)
		}
		if len(details) != 2 || details[0].Stage != ScrapeFailureStageMatch || details[1].Stage != ScrapeFailureStageWrite {
			t.Fatalf("通知明细=%+v", details)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到铃铛通知事件")
	}
}
