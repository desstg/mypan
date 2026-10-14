package strmscrape

import (
	"context"
	"testing"

	"litepan/internal/settings"
)

// memConfigRepo 是最小的 ConfigRepository 假实现，只为把 settings.Service 立起来
// （与 internal/jav/config_test.go 那份同一个套路）。
type memConfigRepo struct{ values map[string]string }

func (r *memConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}
func (r *memConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *memConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

// newWallLocalDirsSettings 造一个能读写设置键的 settings.Service（内存库）。
func newWallLocalDirsSettings(t *testing.T, initial map[string]string) *settings.Service {
	t.Helper()
	if initial == nil {
		initial = map[string]string{}
	}
	svc, err := settings.New(context.Background(), &memConfigRepo{values: initial})
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return svc
}
