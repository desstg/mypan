package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStrmDirDefaultsBesideData(t *testing.T) {
	t.Setenv("LITEPAN_DATA_DIR", "/app/data")
	t.Setenv("LITEPAN_STRM_DIR", "")
	_ = os.Unsetenv("LITEPAN_STRM_DIR")
	cfg := Load()
	if cfg.StrmDir != "/app/strm" {
		t.Fatalf("StrmDir=%q, want /app/strm", cfg.StrmDir)
	}
	if cfg.DataDir != "/app/data" {
		t.Fatalf("DataDir=%q", cfg.DataDir)
	}
}

func TestLoadStrmDirExplicitOverride(t *testing.T) {
	t.Setenv("LITEPAN_DATA_DIR", "/app/data")
	t.Setenv("LITEPAN_STRM_DIR", "/custom/strm")
	cfg := Load()
	if cfg.StrmDir != "/custom/strm" {
		t.Fatalf("StrmDir=%q, want /custom/strm", cfg.StrmDir)
	}
}

func TestLoadStrmDirDefaultsBesideCustomDataDir(t *testing.T) {
	t.Setenv("LITEPAN_DATA_DIR", "/srv/litepan-state")
	t.Setenv("LITEPAN_STRM_DIR", "")
	cfg := Load()
	if cfg.StrmDir != "/srv/strm" {
		t.Fatalf("StrmDir=%q, want /srv/strm", cfg.StrmDir)
	}
}

func TestDefaultLocalStrmBesideCWD(t *testing.T) {
	t.Setenv("LITEPAN_DATA_DIR", "")
	t.Setenv("LITEPAN_STRM_DIR", "")
	_ = os.Unsetenv("LITEPAN_DATA_DIR")
	_ = os.Unsetenv("LITEPAN_STRM_DIR")
	cfg := Load()
	if cfg.DataDir != "./data" || cfg.StrmDir != "./strm" {
		t.Fatalf("got data=%q strm=%q", cfg.DataDir, cfg.StrmDir)
	}
	if filepath.Base(cfg.StrmDir) != "strm" {
		t.Fatalf("unexpected strm base: %q", cfg.StrmDir)
	}
}
