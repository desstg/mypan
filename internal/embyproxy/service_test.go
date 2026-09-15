package embyproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"litepan/internal/playback"
	"litepan/internal/proxybase"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/strm"
)

func TestUpdateRequestAcceptsNumericPort(t *testing.T) {
	var in UpdateRequest
	if err := json.Unmarshal([]byte(`{"proxy_port":18097}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Port.String() != "18097" {
		t.Fatalf("端口=%q", in.Port.String())
	}
}

func testEmbyProxyService(t *testing.T, embyURL string) *Service {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repos := store.New(db)
	settingsSvc, err := settings.New(ctx, repos.Configs)
	if err != nil {
		t.Fatal(err)
	}
	if err := settingsSvc.Update(ctx, map[string]string{
		settings.KeyEmbyEnabled: "true",
		settings.KeyEmbyProxyInstances: fmt.Sprintf(
			`[{"id":"default","name":"Emby","emby_url":%q,"api_key":"test-key","proxy_port":"8097"}]`,
			embyURL,
		),
	}); err != nil {
		t.Fatal(err)
	}
	return New(Options{Settings: settingsSvc})
}

func TestRedirectSTRMStreamResolvesLitePanSTRMOnServer(t *testing.T) {
	fileID := "file-1"
	litepanURL := fmt.Sprintf("http://192.168.60.8:5211/api/strm/play/12/%s/t/token/n/demo.mkv", strm.EncodeFileKey(fileID))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Items") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"Items":[{"Id":"123","MediaSources":[{"Id":"ms1","Path":%q}]}]}`, litepanURL))
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	var gotAccountID int64
	var gotFileID string
	var gotUA string
	svc.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		gotAccountID = req.AccountID
		gotFileID = req.FileID
		gotUA = r.UserAgent()
		w.Header().Set("Location", "https://cdn.example/video.mkv")
		w.WriteHeader(http.StatusFound)
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://litepan.test:8097/Videos/123/stream?MediaSourceId=ms1&api_key=test-key", nil)
	req.Header.Set("User-Agent", "MediaClient/8.5")
	rec := httptest.NewRecorder()
	svc.handle(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码=%d，响应=%s", resp.StatusCode, rec.Body.String())
	}
	if got := resp.Header.Get("Location"); got != "https://cdn.example/video.mkv" {
		t.Fatalf("Location=%q", got)
	}
	if gotAccountID != 12 || gotFileID != fileID {
		t.Fatalf("播放请求解析错误：account=%d file=%q", gotAccountID, gotFileID)
	}
	if gotUA != "MediaClient/8.5" {
		t.Fatalf("播放请求 UA=%q，期望透传 MediaClient/8.5", gotUA)
	}
}

func TestRedirectSTRMStreamMatchedClientReadsOriginalSTRM(t *testing.T) {
	fileID := "file-direct-client"
	litepanURL := fmt.Sprintf("https://litepan.example:8888/api/strm/play/12/%s/t/token/n/demo.mkv", strm.EncodeFileKey(fileID))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Items") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"Items":[{"Id":"123","MediaSources":[{"Id":"ms1","Path":%q}]}]}`, litepanURL))
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	svc.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		t.Fatal("命中的 STRM 直读客户端不应由 LitePan 代取地址")
		return nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://litepan.test:8097/Videos/123/stream?MediaSourceId=ms1&api_key=test-key", nil)
	req.Header.Set("User-Agent", "XXXPlay/1.0")
	req.Header.Set("Range", "bytes=0-")
	rec := httptest.NewRecorder()
	svc.redirectSTRMStream(rec, req, Config{EmbyURL: upstream.URL, APIKey: "test-key", DirectSTRMClients: "XXXPlay"}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码=%d，期望 206", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), litepanURL+"\n"; got != want {
		t.Fatalf("STRM 文本=%q，期望 %q", got, want)
	}
}

func TestRedirectSTRMStreamUsesPlaybackResponseForProxyMode(t *testing.T) {
	litepanURL := fmt.Sprintf("http://192.168.60.8:5211/api/strm/play/9/%s/t/token/n/demo.mkv", strm.EncodeFileKey("file-9"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Items") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"Items":[{"Id":"123","MediaSources":[{"Id":"ms1","Path":%q}]}]}`, litepanURL))
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	svc.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "proxied")
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://litepan.test:8097/Videos/123/stream?MediaSourceId=ms1&api_key=test-key", nil)
	rec := httptest.NewRecorder()
	svc.handle(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码=%d，响应=%s", resp.StatusCode, string(body))
	}
	if got := resp.Header.Get("Location"); got != "" {
		t.Fatalf("Location=%q，期望为空", got)
	}
	if got := string(body); got != "proxied" {
		t.Fatalf("响应体=%q", got)
	}
}

func TestPlaybackInfoCacheKeepsSelectedMultiVersionSource(t *testing.T) {
	firstURL := fmt.Sprintf("http://192.168.60.8:5211/api/strm/play/3/%s/t/token/n/1080p.mkv", strm.EncodeFileKey("file-1080p"))
	secondURL := fmt.Sprintf("http://192.168.60.8:5211/api/strm/play/3/%s/t/token/n/2160p.mkv", strm.EncodeFileKey("file-2160p"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/Items/2257/PlaybackInfo"):
			_, _ = io.WriteString(w, fmt.Sprintf(`{"MediaSources":[{"Id":"mediasource_2257","Path":%q},{"Id":"mediasource_2258","Path":%q}]}`, firstURL, secondURL))
		case strings.HasSuffix(r.URL.Path, "/Items"):
			// Emby 的条目查询在合并版本场景下可能只返回主版本。
			_, _ = io.WriteString(w, fmt.Sprintf(`{"Items":[{"Id":"2257","Path":%q,"MediaSources":[{"Id":"mediasource_2257","Path":%q}]}]}`, firstURL, firstURL))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	var gotFileID string
	svc.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		gotFileID = req.FileID
		w.WriteHeader(http.StatusFound)
		return nil
	}
	cfg := Config{ID: "default", EmbyURL: upstream.URL, APIKey: "test-key"}

	playbackReq := httptest.NewRequest(http.MethodPost, "http://litepan.test:8097/Items/2257/PlaybackInfo?api_key=test-key", nil)
	playbackRec := httptest.NewRecorder()
	svc.modifyPlaybackInfo(playbackRec, playbackReq, cfg, "Items/2257/PlaybackInfo")
	if playbackRec.Code != http.StatusOK {
		t.Fatalf("PlaybackInfo 状态码=%d，响应=%s", playbackRec.Code, playbackRec.Body.String())
	}

	streamReq := httptest.NewRequest(http.MethodGet, "http://litepan.test:8097/emby/Videos/2257/stream?MediaSourceId=mediasource_2258&api_key=test-key", nil)
	streamRec := httptest.NewRecorder()
	svc.redirectSTRMStream(streamRec, streamReq, cfg, strings.TrimPrefix(streamReq.URL.RequestURI(), "/"))
	if gotFileID != "file-2160p" {
		t.Fatalf("切换第二版本后 file_id=%q，期望 file-2160p", gotFileID)
	}
}

func TestRedirectSTRMStreamAcceptsLitePanURLWithSpaces(t *testing.T) {
	fileID := "file-with-spaces"
	fileName := "10间敢死队 (2026) [2160p].mkv"
	litepanURL := fmt.Sprintf("http://192.168.60.8:5211/api/strm/play/12/%s/t/token/n/%s", strm.EncodeFileKey(fileID), url.PathEscape(fileName))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Items") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"Items":[{"Id":"123","MediaSources":[{"Id":"ms1","Path":%q}]}]}`, litepanURL))
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	var gotReq playback.Request
	svc.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		gotReq = req
		w.WriteHeader(http.StatusFound)
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://litepan.test:8097/Videos/123/stream?MediaSourceId=ms1&api_key=test-key", nil)
	rec := httptest.NewRecorder()
	svc.handle(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码=%d，响应=%s", resp.StatusCode, rec.Body.String())
	}
	if gotReq.AccountID != 12 || gotReq.FileID != fileID {
		t.Fatalf("带空格文件名的 LitePan URL 解析错误：account=%d file=%q", gotReq.AccountID, gotReq.FileID)
	}
}

func TestParseLitePanSTRMURLFilenameRegressionCases(t *testing.T) {
	cases := []struct {
		name     string
		fileName string
	}{
		{name: "中文空格括号", fileName: "10间敢死队 (2026) [2160p].mkv"},
		{name: "英文加号百分号", fileName: "Movie.Name.2024.2160p.HDR10+ 100%.mkv"},
		{name: "波浪线与符号", fileName: "A&B ~ Director's Cut, Final!.mp4"},
		{name: "全角符号混排", fileName: "全角～波浪＋中文＆英文【特别版】.mkv"},
		{name: "井号分号等号", fileName: "Episode 01; part=2 #remux!.mkv"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileID := fmt.Sprintf("file-regression-%d", i)
			playURL := fmt.Sprintf(
				"http://127.0.0.1:5211/api/strm/play/12/%s/t/token/n/%s",
				strm.EncodeFileKey(fileID),
				url.PathEscape(tc.fileName),
			)
			accountID, gotFileID, ok := proxybase.ParseLitePanSTRMURL(playURL)
			if !ok {
				t.Fatalf("proxybase.ParseLitePanSTRMURL 返回 false，url=%q", playURL)
			}
			if accountID != 12 || gotFileID != fileID {
				t.Fatalf("解析结果错误：account=%d file=%q", accountID, gotFileID)
			}
			if gotPath := proxybase.LitePanPath(playURL); strings.Contains(gotPath, " ") {
				t.Fatalf("编码路径不应出现空格：%q", gotPath)
			}
		})
	}
}

func TestListLibrariesAndRefreshSpecificLibrary(t *testing.T) {
	var gotSelectable bool
	var gotRefreshPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/Library/SelectableMediaFolders"):
			gotSelectable = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"Id":"lib-1","Name":"电影"},{"Id":"lib-2","Name":"剧集"}]`)
		case strings.HasSuffix(r.URL.Path, "/Items/lib-2/Refresh"):
			gotRefreshPath = r.URL.RequestURI()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	svc := testEmbyProxyService(t, upstream.URL)
	libraries, err := svc.ListLibraries(context.Background())
	if err != nil {
		t.Fatalf("ListLibraries 返回错误: %v", err)
	}
	if !gotSelectable {
		t.Fatalf("未请求 SelectableMediaFolders")
	}
	if len(libraries) != 2 || libraries[1].ID != "lib-2" {
		t.Fatalf("媒体库列表异常: %#v", libraries)
	}
	result, err := svc.RefreshLibrary(context.Background(), RefreshRequest{Mode: "library", LibraryID: "lib-2"})
	if err != nil {
		t.Fatalf("RefreshLibrary 返回错误: %v", err)
	}
	if !strings.Contains(gotRefreshPath, "/Items/lib-2/Refresh?") {
		t.Fatalf("指定库刷新路径异常: %q", gotRefreshPath)
	}
	if result.Mode != "library" || result.LibraryID != "lib-2" || result.LibraryName != "剧集" {
		t.Fatalf("刷新结果异常: %#v", result)
	}
}

func TestReplaceConfigsKeepsFirstAndMaskedSecret(t *testing.T) {
	svc := testEmbyProxyService(t, "http://emby.test:8096")
	state, err := svc.Replace(context.Background(), false, []UpdateRequest{
		{Name: "主 Emby", EmbyURL: "http://primary.test:8096", APIKey: "primary-secret"},
		{Name: "备用 Emby", EmbyURL: "http://backup.test:8096", APIKey: "backup-secret"},
	})
	if err != nil {
		t.Fatalf("保存多条 Emby 配置: %v", err)
	}
	configs := state.Items
	if state.Enabled || len(configs) != 2 || configs[0].ID == "" || configs[1].ID == "" {
		t.Fatalf("配置状态异常: %#v", state)
	}
	if configs[0].APIKey == "primary-secret" || !strings.Contains(configs[0].APIKey, "****") {
		t.Fatalf("API Key 未脱敏: %q", configs[0].APIKey)
	}

	configs[0].Name = "家庭 Emby"
	updated, err := svc.Replace(context.Background(), false, []UpdateRequest{
		{ID: configs[0].ID, Name: configs[0].Name, EmbyURL: configs[0].EmbyURL, APIKey: configs[0].APIKey},
		{ID: configs[1].ID, Name: configs[1].Name, EmbyURL: configs[1].EmbyURL, APIKey: configs[1].APIKey},
	})
	if err != nil {
		t.Fatalf("使用脱敏 Key 更新配置: %v", err)
	}
	raw, err := svc.resolveConfig(updated.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if raw.APIKey != "primary-secret" || raw.Name != "家庭 Emby" {
		t.Fatalf("更新后配置=%#v", raw)
	}
}

func TestRefreshWithoutConfigIDUsesFirst(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer secondary.Close()

	svc := testEmbyProxyService(t, primary.URL)
	state, err := svc.Replace(context.Background(), false, []UpdateRequest{
		{Name: "Emby A", EmbyURL: primary.URL, APIKey: "key-a"},
		{Name: "Emby B", EmbyURL: secondary.URL, APIKey: "key-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.RefreshLibrary(context.Background(), RefreshRequest{Mode: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigID != state.Items[0].ID || result.ConfigName != "Emby A" {
		t.Fatalf("旧自动联动未使用第一条配置: %#v", result)
	}
	if firstCalls.Load() == 0 || secondCalls.Load() != 0 {
		t.Fatalf("请求分发错误: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
}

func TestReplaceConfigsRejectsDuplicateEnabledPort(t *testing.T) {
	svc := testEmbyProxyService(t, "http://emby.test:8096")
	_, err := svc.Replace(context.Background(), true, []UpdateRequest{
		{Name: "Emby A", EmbyURL: "http://a.test:8096", APIKey: "a", Port: "18097"},
		{Name: "Emby B", EmbyURL: "http://b.test:8096", APIKey: "b", Port: "18097"},
	})
	if err == nil || !strings.Contains(err.Error(), "同一个端口") {
		t.Fatalf("重复端口错误=%v", err)
	}
}

func TestReplaceConfigsRejectsMissingPortWhenEnabled(t *testing.T) {
	svc := testEmbyProxyService(t, "http://emby.test:8096")
	_, err := svc.Replace(context.Background(), true, []UpdateRequest{
		{Name: "Emby A", EmbyURL: "http://a.test:8096", APIKey: "a"},
	})
	if err == nil || !strings.Contains(err.Error(), "所有配置填写反代端口") {
		t.Fatalf("缺少端口错误=%v", err)
	}
}

func TestIsExpectedClientDisconnect(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if !isExpectedClientDisconnect(canceled, context.Canceled) {
		t.Fatal("context canceled 应识别为客户端取消")
	}
	for _, err := range []error{
		fmt.Errorf("write tcp: broken pipe"),
		fmt.Errorf("readfrom tcp: connection reset by peer"),
		fmt.Errorf("use of closed network connection"),
	} {
		if !isExpectedClientDisconnect(context.Background(), err) {
			t.Fatalf("%q 应识别为客户端取消", err)
		}
	}
	if isExpectedClientDisconnect(context.Background(), fmt.Errorf("upstream returned 401")) {
		t.Fatal("真实上游错误不应被当成客户端取消")
	}
}
