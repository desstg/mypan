package quarktv

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"litepan/internal/domain"
)

func TestPickStreamingCandidatePrefersDolbyOnlyWhenEnabled(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "4k", Accessable: 1, Format: "mp4", URL: "https://example/4k"},
		{Resolution: "dolby_vision", Accessable: 1, Format: "matroska,webm", URL: "https://example/dv"},
	}

	gotOff, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionAuto,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("dolby 关闭时未选出候选")
	}
	if gotOff.Resolution != "4k" {
		t.Fatalf("dolby 关闭时应选 4k，实际为 %q", gotOff.Resolution)
	}

	gotOn, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionAuto,
		AllowDolby:          true,
	})
	if !ok {
		t.Fatal("dolby 开启时未选出候选")
	}
	if gotOn.Resolution != "dolby_vision" {
		t.Fatalf("dolby 开启时应选 dolby_vision，实际为 %q", gotOn.Resolution)
	}
}

func TestPickStreamingCandidateRespectsResolutionCap(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "4k", Accessable: 1, Format: "mp4", URL: "https://example/4k"},
		{Resolution: "super", Accessable: 1, Format: "mp4", URL: "https://example/super"},
		{Resolution: "high", Accessable: 1, Format: "mp4", URL: "https://example/high"},
	}

	got, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionHigh,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("受限清晰度下未选出候选")
	}
	if got.Resolution != "high" {
		t.Fatalf("清晰度上限为 high 时应选 high，实际为 %q", got.Resolution)
	}
}

func TestPickStreamingCandidateTreats2KAsSuperBucket(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "4k", Accessable: 1, Format: "mp4", URL: "https://example/4k"},
		{Resolution: "2k", Accessable: 1, Format: "mp4", URL: "https://example/2k"},
		{Resolution: "super", Accessable: 1, Format: "mp4", URL: "https://example/super"},
		{Resolution: "high", Accessable: 1, Format: "mp4", URL: "https://example/high"},
	}

	got, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionSuper,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("超清档未选出候选")
	}
	if got.Resolution != "2k" {
		t.Fatalf("超清档应优先命中 2k，实际为 %q", got.Resolution)
	}
}

func TestPickStreamingCandidateFallsBackFrom4KTo2K(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "2k", Accessable: 1, Format: "mp4", URL: "https://example/2k"},
		{Resolution: "super", Accessable: 1, Format: "mp4", URL: "https://example/super"},
	}

	got, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolution4K,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("4k 档未选出候选")
	}
	if got.Resolution != "2k" {
		t.Fatalf("4k 档缺失时应回落到 2k，实际为 %q", got.Resolution)
	}
}

func TestParseQuarkTVHTTPErrorMessageMapsDeviceLimit(t *testing.T) {
	body := []byte(`{"code":400,"message":"device limit exceeded"}`)
	got := parseQuarkTVHTTPErrorMessage(body)
	want := "设备数超限"
	if got != want {
		t.Fatalf("parseQuarkTVHTTPErrorMessage() = %q, want %q", got, want)
	}
}

func TestExchangeTokenReturnsBodyMessageOnHTTP400(t *testing.T) {
	client := NewClient("device-id", "", "", tokenExpiresAt(3600))
	client.http = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != codeAPI+"/token" {
				t.Fatalf("unexpected url: %s", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":400,"message":"device limit exceeded"}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err := client.exchangeToken(context.Background(), "device-id", "bind-code", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "DRIVER_ERROR: 设备数超限"
	if err.Error() != want {
		t.Fatalf("exchangeToken error = %q, want %q", err.Error(), want)
	}
}

func TestDoOnceReturnsBodyErrorMessageOnHTTP400(t *testing.T) {
	client := NewClient("device-id", "refresh-token", "access-token", tokenExpiresAt(3600))
	client.http = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/user" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"status":-1,"errno":32009,"error_info":"设备数超限"}`)),
				Request:    req,
			}, nil
		}),
	}

	_, err := client.doOnce(context.Background(), http.MethodGet, "/user", nil, nil, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "DRIVER_ERROR: 设备数超限"
	if err.Error() != want {
		t.Fatalf("doOnce error = %q, want %q", err.Error(), want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPickStreamingCandidateAllM3U8FallsBackToBest(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "4k", Accessable: 1, Format: "m3u8", URL: "https://example/4k.m3u8"},
		{Resolution: "super", Accessable: 1, Format: "m3u8", URL: "https://example/super.m3u8"},
		{Resolution: "high", Accessable: 1, Format: "m3u8", URL: "https://example/high.m3u8"},
		{Resolution: "low", Accessable: 1, Format: "m3u8", URL: "https://example/low.m3u8"},
	}
	got, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionAuto,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("全 m3u8 档位应兜底选中最高档，实际未选中")
	}
	if got.Resolution != "4k" {
		t.Fatalf("全 m3u8 应选 4k，实际为 %q", got.Resolution)
	}
}

func TestPickStreamingCandidatePrefersMP4OverM3U8(t *testing.T) {
	infos := []streamingVideoInfo{
		{Resolution: "4k", Accessable: 1, Format: "m3u8", URL: "https://example/4k.m3u8"},
		{Resolution: "super", Accessable: 1, Format: "mp4", URL: "https://example/super.mp4"},
		{Resolution: "4k", Accessable: 1, Format: "mp4", URL: "https://example/4k.mp4"},
	}
	got, ok := pickStreamingCandidate("fid", infos, StreamingPreference{
		PreferredResolution: domain.QuarkTVResolutionAuto,
		AllowDolby:          false,
	})
	if !ok {
		t.Fatal("应选中档位")
	}
	if got.Format != "mp4" {
		t.Fatalf("有 mp4 时应优先 mp4，实际选 %q(%s)", got.Resolution, got.Format)
	}
	if got.Resolution != "4k" {
		t.Fatalf("mp4 中应选 4k，实际为 %q", got.Resolution)
	}
}

func TestNormalizePlayMode(t *testing.T) {
	tests := map[string]string{
		"split":    PlayModeSplit,
		"DIRECT":   PlayModeDirect,
		"adaptive": PlayModeAdaptive,
		"":         PlayModeAdaptive,
		"unknown":  PlayModeAdaptive,
	}
	for input, want := range tests {
		if got := normalizePlayMode(input); got != want {
			t.Fatalf("normalizePlayMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsHLSFormat(t *testing.T) {
	for _, format := range []string{"m3u8", "HLS", " hls "} {
		if !isHLSFormat(format) {
			t.Fatalf("isHLSFormat(%q) = false, want true", format)
		}
	}
	for _, format := range []string{"mp4", "matroska,webm", ""} {
		if isHLSFormat(format) {
			t.Fatalf("isHLSFormat(%q) = true, want false", format)
		}
	}
}

func TestPlaybackModesAreIndependent(t *testing.T) {
	if shouldBypassTVBeforeResolve(PlayModeSplit, ClientListDirect, "vidhub", "VidHub/2.0") {
		t.Fatal("直连名单命中时应走夸克 TV")
	}
	if !shouldBypassTVBeforeResolve(PlayModeSplit, ClientListDirect, "vidhub", "Lavf/59.27.100") {
		t.Fatal("直连名单未命中时应走本机代理")
	}
	if !shouldBypassTVBeforeResolve(PlayModeSplit, ClientListProxy, "vidhub", "VidHub/2.0") {
		t.Fatal("代理名单命中时应走本机代理")
	}
	if shouldBypassTVBeforeResolve(PlayModeSplit, ClientListProxy, "vidhub", "OtherPlayer/1.0") {
		t.Fatal("代理名单未命中时应走夸克 TV")
	}
	if shouldBypassTVBeforeResolve(PlayModeAdaptive, ClientListProxy, "vidhub", "VidHub/2.0") {
		t.Fatal("智能变轨不应使用例外客户端")
	}
	if shouldBypassTVBeforeResolve(PlayModeDirect, ClientListProxy, "vidhub", "VidHub/2.0") {
		t.Fatal("全部走 TV 不应使用例外客户端")
	}
	if !shouldFallbackSelectedFormat(PlayModeAdaptive, "m3u8") {
		t.Fatal("智能变轨应在 HLS/M3U8 档位回落")
	}
	if shouldFallbackSelectedFormat(PlayModeAdaptive, "mp4") {
		t.Fatal("智能变轨不应回落 MP4 档位")
	}
	if shouldFallbackSelectedFormat(PlayModeSplit, "m3u8") {
		t.Fatal("策略分流不应按档位格式回落")
	}
	if shouldFallbackSelectedFormat(PlayModeDirect, "m3u8") {
		t.Fatal("全部走 TV 不应按档位格式回落")
	}
}

func TestNormalizeClientListMode(t *testing.T) {
	if got := normalizeClientListMode("direct_list"); got != ClientListDirect {
		t.Fatalf("normalizeClientListMode(direct_list) = %q", got)
	}
	for _, value := range []string{"", "proxy_list", "unknown"} {
		if got := normalizeClientListMode(value); got != ClientListProxy {
			t.Fatalf("normalizeClientListMode(%q) = %q, want %q", value, got, ClientListProxy)
		}
	}
}
