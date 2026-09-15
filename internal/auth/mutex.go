package auth

import (
	"context"
	"sync"
)

type keyedMutex struct {
	mu    sync.Mutex
	locks map[int64]chan struct{}
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[int64]chan struct{})}
}

func (k *keyedMutex) Lock(ctx context.Context, id int64) (func(), error) {
	k.mu.Lock()
	m, ok := k.locks[id]
	if !ok {
		m = make(chan struct{}, 1)
		k.locks[id] = m
	}
	k.mu.Unlock()
	select {
	case m <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m
			return nil, err
		}
		return func() { <-m }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
