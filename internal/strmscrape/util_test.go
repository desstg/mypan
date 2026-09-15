package strmscrape

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRelUnderAbsRootWithRelativeFull(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "strm_out")
	show := filepath.Join(root, "航海王 (1999)")
	if err := os.MkdirAll(show, 0o755); err != nil {
		t.Fatal(err)
	}
	posterAbs := filepath.Join(show, "poster.jpg")
	if err := os.WriteFile(posterAbs, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	posterRel, err := filepath.Rel(cwd, posterAbs)
	if err != nil {
		t.Fatal(err)
	}
	got := relUnder(root, posterRel)
	want := filepath.ToSlash(filepath.Join("航海王 (1999)", "poster.jpg"))
	if filepath.ToSlash(got) != want {
		t.Fatalf("relUnder=%q want %q（绝对 root + 相对 full 不应退化成 basename）", got, want)
	}
}
