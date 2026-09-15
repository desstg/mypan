package startupwait

import (
	"context"
	"testing"
	"time"
)

func TestReadyWaitsForGate(t *testing.T) {
	gate := make(chan struct{})
	done := make(chan bool, 1)
	go func() { done <- Ready(context.Background(), gate) }()
	select {
	case <-done:
		t.Fatal("闸门关闭时不应放行")
	case <-time.After(20 * time.Millisecond):
	}
	close(gate)
	select {
	case ready := <-done:
		if !ready {
			t.Fatal("闸门放行后应继续执行")
		}
	case <-time.After(time.Second):
		t.Fatal("等待闸门超时")
	}
}

func TestReadyStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})
	cancel()
	if Ready(ctx, gate) {
		t.Fatal("上下文取消后不应放行")
	}
}
