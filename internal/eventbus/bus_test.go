package eventbus

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type closeTestEvent struct {
	value int
}

func newTestBus() *Bus {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCloseDrainsQueuedEvents(t *testing.T) {
	bus := newTestBus()
	started := make(chan struct{})
	release := make(chan struct{})
	processed := make(chan int, 2)

	Subscribe(bus, func(_ context.Context, evt closeTestEvent) {
		if evt.value == 1 {
			close(started)
			<-release
		}
		processed <- evt.value
	})
	bus.Publish(context.Background(), closeTestEvent{value: 1})
	<-started
	bus.Publish(context.Background(), closeTestEvent{value: 2})

	closed := make(chan error, 1)
	go func() { closed <- bus.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("处理函数未释放前 Close 不应返回: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close 返回错误: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close 排空队列超时")
	}
	for want := 1; want <= 2; want++ {
		select {
		case got := <-processed:
			if got != want {
				t.Fatalf("事件处理顺序异常: got=%d want=%d", got, want)
			}
		default:
			t.Fatalf("事件 %d 未被处理", want)
		}
	}
}

func TestCloseHonorsContextAndCanWaitAgain(t *testing.T) {
	bus := newTestBus()
	started := make(chan struct{})
	release := make(chan struct{})
	Subscribe(bus, func(context.Context, closeTestEvent) {
		close(started)
		<-release
	})
	bus.Publish(context.Background(), closeTestEvent{})
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := bus.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close 应服从 context 超时，实际 err=%v", err)
	}

	close(release)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := bus.Close(waitCtx); err != nil {
		t.Fatalf("处理完成后再次 Close 应成功: %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	bus := newTestBus()
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("首次 Close 失败: %v", err)
	}
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("重复 Close 失败: %v", err)
	}
}
