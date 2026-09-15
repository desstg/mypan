package tmdb

import (
	"strings"
	"testing"
)

func TestNormalizeHostWithSuffix(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		suffix string
		want   string
	}{
		{"官方 api 域名", "https://api.themoviedb.org", "/3", "https://api.themoviedb.org/3"},
		{"官方 image 域名", "https://image.tmdb.org", "/t/p", "https://image.tmdb.org/t/p"},
		{"反代带尾部斜杠", "https://tmdb.example.com/", "/3", "https://tmdb.example.com/3"},
		{"反代无协议自动补 https", "tmdb.example.com", "/3", "https://tmdb.example.com/3"},
		{"反代带路径前缀", "https://proxy.example.com/tmdb", "/3", "https://proxy.example.com/tmdb/3"},
		{"空串", "", "/3", ""},
		{"非法 host", "http://", "/3", ""},
		{"非法 scheme", "ftp://x.com", "/3", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHostWithSuffix(tt.raw, tt.suffix)
			if got != tt.want {
				t.Fatalf("normalizeHostWithSuffix(%q, %q) = %q, want %q", tt.raw, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestResolveAPIBaseURL(t *testing.T) {
	if got := resolveAPIBaseURL(Options{}); got != defaultBaseURL {
		t.Fatalf("empty opts should fall back to default, got %q", got)
	}
	if got := resolveAPIBaseURL(Options{APIBaseHost: "tmdb.example.com"}); got != "https://tmdb.example.com/3" {
		t.Fatalf("api host should get /3 suffix, got %q", got)
	}
	// 非法设置值回落默认
	if got := resolveAPIBaseURL(Options{APIBaseHost: "http://"}); got != defaultBaseURL {
		t.Fatalf("invalid api host should fall back, got %q", got)
	}
	// 等于官方默认视为未配置
	if got := resolveAPIBaseURL(Options{APIBaseHost: "https://api.themoviedb.org"}); got != defaultBaseURL {
		t.Fatalf("official host should fall back to default, got %q", got)
	}
}

func TestResolveImageBaseURL(t *testing.T) {
	if got := resolveImageBaseURL(Options{}); got != imageBaseURL {
		t.Fatalf("empty opts should fall back to official image base, got %q", got)
	}
	if got := resolveImageBaseURL(Options{ImageBaseHost: "img.example.com"}); got != "https://img.example.com/t/p" {
		t.Fatalf("image host should get /t/p suffix, got %q", got)
	}
	if got := resolveImageBaseURL(Options{ImageBaseHost: "http://"}); got != imageBaseURL {
		t.Fatalf("invalid image host should fall back, got %q", got)
	}
}

func TestClientImageBaseNilSafe(t *testing.T) {
	if got := (*Client)(nil).imageBase(); got != imageBaseURL {
		t.Fatalf("nil client imageBase should fall back, got %q", got)
	}
	c := NewClient(Options{ImageBaseHost: "img.example.com"})
	if !strings.HasPrefix(c.imageBase(), "https://img.example.com/t/p") {
		t.Fatalf("client imageBase should use configured host, got %q", c.imageBase())
	}
}
