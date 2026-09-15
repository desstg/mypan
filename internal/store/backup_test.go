package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"litepan/internal/store"
)

func TestSnapshotToCreatesConsistentDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(root, "source.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	if err := st.Configs.Set(ctx, "snapshot_test", "ok"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "snapshot.db")
	if err := db.SnapshotTo(ctx, destination); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Open(ctx, store.Options{Path: destination})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := snapshot.IntegrityCheck(ctx); err != nil {
		t.Fatal(err)
	}
	value, ok, err := store.New(snapshot).Configs.Get(ctx, "snapshot_test")
	if err != nil || !ok || value != "ok" {
		t.Fatalf("snapshot value=%q ok=%v err=%v", value, ok, err)
	}
}
