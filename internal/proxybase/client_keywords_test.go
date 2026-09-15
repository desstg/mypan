package proxybase

import "testing"

func TestMatchesClientText(t *testing.T) {
	tests := []struct {
		name       string
		keywords   string
		candidates []string
		want       bool
	}{
		{name: "忽略大小写命中 UA", keywords: "vidhub;infuse", candidates: []string{"VidHub/2.0"}, want: true},
		{name: "命中第二候选文本", keywords: "vidhub;infuse", candidates: []string{"unknown", "Infuse/8.5"}, want: true},
		{name: "兼容中文分号", keywords: "vidhub；infuse", candidates: []string{"vidhub"}, want: true},
		{name: "未命中", keywords: "vidhub;infuse", candidates: []string{"Emby/4.9"}, want: false},
		{name: "空关键字不命中", keywords: "", candidates: []string{"Emby/4.9"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesClientText(tt.keywords, tt.candidates...); got != tt.want {
				t.Fatalf("MatchesClientText(%q, %q)=%v，期望 %v", tt.keywords, tt.candidates, got, tt.want)
			}
		})
	}
}

func TestNormalizeClientKeywords(t *testing.T) {
	if got := NormalizeClientKeywords(" emby；VidHub;EMBY "); got != "emby;VidHub" {
		t.Fatalf("NormalizeClientKeywords()=%q，期望 %q", got, "emby;VidHub")
	}
}
