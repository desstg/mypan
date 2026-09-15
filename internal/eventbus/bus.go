package eventbus

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
)

// Handler 是某事件类型 T 的处理函数。
type Handler[T any] func(ctx context.Context, evt T)

type subscription struct {
	call func(ctx context.Context, evt any)
}

type envelope struct {
	ctx context.Context
	evt any
	typ reflect.Type
}

const defaultQueueSize = 256

type Bus struct {
	log    *slog.Logger
	mu     sync.RWMutex
	subs   map[reflect.Type][]subscription
	queue  chan envelope
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
}

// New 创建并启动事件总线。log 为 nil 时使用 slog.Default()。
func New(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	b := &Bus{
		log:    log,
		subs:   make(map[reflect.Type][]subscription),
		queue:  make(chan envelope, defaultQueueSize),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go b.run()
	return b
}

// Subscribe 注册某事件类型的处理函数。应在装配期、发布之前完成注册。
func Subscribe[T any](b *Bus, h Handler[T]) {
	t := reflect.TypeFor[T]()
	b.mu.Lock()
	b.subs[t] = append(b.subs[t], subscription{
		call: func(ctx context.Context, evt any) { h(ctx, evt.(T)) },
	})
	b.mu.Unlock()
}

// Publish 异步发布事件；总线已关闭时静默丢弃。
func (b *Bus) Publish(ctx context.Context, evt any) {
	env := envelope{ctx: ctx, evt: evt, typ: reflect.TypeOf(evt)}
	select {
	case b.queue <- env:
	case <-b.closed:
	}
}

func (b *Bus) run() {
	defer close(b.done)
	for {
		select {
		case env := <-b.queue:
			b.dispatch(env)
		case <-b.closed:
			b.drain()
			return
		}
	}
}

func (b *Bus) drain() {
	for {
		select {
		case env := <-b.queue:
			b.dispatch(env)
		default:
			return
		}
	}
}

func (b *Bus) dispatch(env envelope) {
	b.mu.RLock()
	subs := b.subs[env.typ]
	b.mu.RUnlock()
	for _, s := range subs {
		b.safeCall(s, env)
	}
}

func (b *Bus) safeCall(s subscription, env envelope) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("event handler panicked", "event", env.typ.String(), "panic", r)
		}
	}()
	s.call(env.ctx, env.evt)
}

// Close 停止接收新事件，排空队列后等待分发协程退出。可重复调用。
func (b *Bus) Close(ctx context.Context) error {
	b.once.Do(func() { close(b.closed) })
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
