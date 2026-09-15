package file

import (
	"context"
	"testing"
	"time"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type uploadRootDriver struct{}

func (uploadRootDriver) Config() driver.Config      { return driver.Config{Name: "test"} }
func (uploadRootDriver) GetAddition() any           { return struct{}{} }
func (uploadRootDriver) Init(context.Context) error { return nil }
func (uploadRootDriver) Drop(context.Context) error { return nil }
func (uploadRootDriver) Ping(context.Context) error { return nil }
func (uploadRootDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (uploadRootDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	return &driver.LocalUploadResult{
		FileID:   "new-file",
		ParentID: "configured-root",
		FileName: req.FileName,
		Size:     128,
	}, nil
}

type uploadRootProvider struct{ drv driver.Driver }

func (p uploadRootProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}

func TestUploadLocalRefreshesLogicalAndResolvedRootCaches(t *testing.T) {
	const accountID int64 = 7
	c := cache.NewService(cache.Options{MaxItems: 16})
	t.Cleanup(c.Close)
	c.Set(cache.DirKey(accountID, "0"), cache.DirList{{ID: "old-file", Name: "old.mkv"}}, time.Hour)
	c.Set(cache.DirKey(accountID, "configured-root"), cache.DirList{{ID: "stale-file", Name: "stale.mkv"}}, time.Hour)

	svc := NewService(
		driverexec.New(uploadRootProvider{drv: uploadRootDriver{}}, nil),
		c, nil, nil, nil, nil,
	)
	_, err := svc.UploadLocal(context.Background(), accountID, driver.LocalUploadRequest{
		ParentID: "0",
		FileName: "new.mkv",
	})
	if err != nil {
		t.Fatalf("UploadLocal() error = %v", err)
	}

	raw, ok := c.Get(cache.DirKey(accountID, "0"))
	if !ok {
		t.Fatal("logical root cache was removed instead of refreshed")
	}
	items, ok := raw.(cache.DirList)
	if !ok || len(items) != 2 {
		t.Fatalf("logical root cache = %#v", raw)
	}
	found := false
	for _, item := range items {
		found = found || item.ID == "new-file"
	}
	if !found {
		t.Fatalf("logical root cache = %#v", raw)
	}
	if _, ok := c.Get(cache.DirKey(accountID, "configured-root")); ok {
		t.Fatal("resolved root cache was not invalidated")
	}
}
