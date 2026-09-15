package cache

import (
	"context"
	"fmt"
	"time"
)

func GetAs[T any](s *Service, key string) (T, bool) {
	var zero T
	if s == nil {
		return zero, false
	}
	v, ok := s.Get(key)
	if !ok {
		return zero, false
	}
	if typed, ok := v.(T); ok {
		return typed, true
	}
	if coerced, ok := coerceFileInfo[T](v); ok {
		return coerced, true
	}
	return zero, false
}

func SetAs[T any](s *Service, key string, val T, ttl time.Duration) {
	if s == nil {
		return
	}
	s.Set(key, val, ttl)
}

func GetOrLoadAs[T any](ctx context.Context, s *Service, key string, ttl time.Duration, loader func(context.Context) (T, error)) (T, bool, error) {
	var zero T
	if s == nil {
		val, err := loader(ctx)
		return val, false, err
	}
	raw, hit, err := s.GetOrLoad(ctx, key, ttl, func(callCtx context.Context) (any, error) {
		return loader(callCtx)
	})
	if err != nil {
		return zero, hit, err
	}
	if typed, ok := raw.(T); ok {
		return typed, hit, nil
	}
	if coerced, ok := coerceFileInfo[T](raw); ok {
		return coerced, hit, nil
	}
	return zero, hit, typeMismatchErr[T](raw)
}

func CoalesceAs[T any](ctx context.Context, s *Service, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if s == nil {
		return fn(ctx)
	}
	raw, err := s.Coalesce(ctx, key, func(callCtx context.Context) (any, error) {
		return fn(callCtx)
	})
	if err != nil {
		return zero, err
	}
	if typed, ok := raw.(T); ok {
		return typed, nil
	}
	if coerced, ok := coerceFileInfo[T](raw); ok {
		return coerced, nil
	}
	return zero, typeMismatchErr[T](raw)
}

func typeMismatchErr[T any](raw any) error {
	var zero T
	return fmt.Errorf("cache value type mismatch: got %T, want %T", raw, zero)
}
