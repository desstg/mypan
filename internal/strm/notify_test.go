package strm

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

func TestNotifyScanFailuresPublishesBellDetails(t *testing.T) {
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

	longDir := strings.Repeat("长", 86)
	failures := NewFailureCollector()
	addOversizedPathFailure(failures, ScanFailureStrm, "任务/"+longDir+"/第01集.strm", false)
	addOversizedPathFailure(failures, ScanFailureStrm, "任务/"+longDir+"/第02集.strm", false)

	svc := &Service{bus: bus}
	svc.notifyScanFailures(&domain.StrmTask{ID: 7, AccountID: 3, Name: "测试任务"}, failures.Items())

	select {
	case evt := <-gotEvent:
		if evt.Category != domain.NotificationCategoryStrmScanWarn || evt.Title != "STRM 扫描部分失败" {
			t.Fatalf("通知分类或标题不正确：%+v", evt)
		}
		parts := strings.SplitN(evt.Message, scanFailureDetailSep, 2)
		if len(parts) != 2 || !strings.Contains(parts[0], "共 1 项未成功处理") {
			t.Fatalf("通知摘要不正确：%q", evt.Message)
		}
		var details []ScanFailure
		if err := json.Unmarshal([]byte(parts[1]), &details); err != nil {
			t.Fatalf("通知明细不是有效 JSON：%v", err)
		}
		if len(details) != 1 || details[0].Reason != pathTooLongDirReason {
			t.Fatalf("通知明细=%+v", details)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到铃铛通知事件")
	}
}
