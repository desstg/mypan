package fusereadcache

import (
	"context"
	"io"
	"testing"

	"litepan/internal/settings"
)

type memoryConfigRepo struct {
	values map[string]string
}

func (r *memoryConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}

func (r *memoryConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memoryConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestReadAtCachesBlockAndReadsRequestedRange(t *testing.T) {
	ctx := context.Background()
	settingSvc, err := settings.New(ctx, &memoryConfigRepo{values: map[string]string{
		"fuse_read_cache_enabled": "true",
	}})
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	svc, err := New(ctx, Options{DataDir: t.TempDir(), Settings: settingSvc})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	data := make([]byte, BlockSize)
	for i := range data {
		data[i] = byte(i % 241)
	}
	fetchCalls := 0
	fetch := func(dest []byte, off int64) (int, error) {
		fetchCalls++
		n := copy(dest, data[off:])
		if n < len(dest) {
			return n, io.EOF
		}
		return n, nil
	}

	first := make([]byte, 128*1024)
	if n, err := svc.ReadAt(ctx, 1, "file", first, 0, fetch); err != nil || n != len(first) {
		t.Fatalf("first ReadAt = %d, %v", n, err)
	}
	secondOff := int64(2*1024*1024 + 17)
	second := make([]byte, 128*1024)
	if n, err := svc.ReadAt(ctx, 1, "file", second, secondOff, fetch); err != nil || n != len(second) {
		t.Fatalf("cached ReadAt = %d, %v", n, err)
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetchCalls)
	}
	for i := range second {
		if second[i] != data[int(secondOff)+i] {
			t.Fatalf("cached data mismatch at %d", i)
		}
	}
}
