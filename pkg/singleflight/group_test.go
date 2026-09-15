package singleflight

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type intResult struct {
	value int
	err   error
}

func TestDoCtxSharesInFlightResult(t *testing.T) {
	var g Group[int]
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})

	leader := make(chan intResult, 1)
	go func() {
		value, err := g.DoCtx(context.Background(), "same-key", func(context.Context) (int, error) {
			calls.Add(1)
			close(started)
			<-release
			return 42, nil
		})
		leader <- intResult{value: value, err: err}
	}()
	<-started

	follower := make(chan intResult, 1)
	go func() {
		value, err := g.DoCtx(context.Background(), "same-key", func(context.Context) (int, error) {
			calls.Add(1)
			return -1, nil
		})
		follower <- intResult{value: value, err: err}
	}()

	select {
	case result := <-follower:
		t.Fatalf("主调用完成前等待者不应返回: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)

	for name, ch := range map[string]<-chan intResult{"leader": leader, "follower": follower} {
		select {
		case result := <-ch:
			if result.err != nil || result.value != 42 {
				t.Fatalf("%s 结果异常: value=%d err=%v", name, result.value, result.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("等待 %s 结果超时", name)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("同一 key 应只执行一次，实际执行 %d 次", got)
	}
}

func TestDoCtxCanceledWaiterDoesNotCancelLeader(t *testing.T) {
	var g Group[int]
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	leader := make(chan intResult, 1)

	go func() {
		value, err := g.DoCtx(context.Background(), "same-key", func(context.Context) (int, error) {
			calls.Add(1)
			close(started)
			<-release
			return 7, nil
		})
		leader <- intResult{value: value, err: err}
	}()
	<-started

	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := g.DoCtx(waitCtx, "same-key", func(context.Context) (int, error) {
		calls.Add(1)
		return -1, nil
	})
	if value != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的等待者结果异常: value=%d err=%v", value, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("等待者取消不应启动第二次调用，实际执行 %d 次", got)
	}

	close(release)
	select {
	case result := <-leader:
		if result.err != nil || result.value != 7 {
			t.Fatalf("主调用结果异常: value=%d err=%v", result.value, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("主调用未在释放后完成")
	}

	value, err = g.DoCtx(context.Background(), "same-key", func(context.Context) (int, error) {
		calls.Add(1)
		return 9, nil
	})
	if err != nil || value != 9 || calls.Load() != 2 {
		t.Fatalf("前一轮结束后应可重新执行: value=%d calls=%d err=%v", value, calls.Load(), err)
	}
}
