package fnosproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
	"litepan/internal/playback"
	"litepan/internal/proxybase"
	"litepan/internal/settings"
	"litepan/internal/strm"
	"litepan/pkg/jsonvalue"
	"litepan/pkg/strutil"
)

const (
	sourceCacheMaxEntries = 256
	sourceCacheTTL        = 24 * time.Hour
)

var (
	videoStreamPathRE    = regexp.MustCompile(`(?i)^(?:/?emby)?/?Videos/([^/]+)/(stream|original)(?:\.\w+)?$`)
	playbackInfoPathRE   = regexp.MustCompile(`(?i)^(?:/?emby)?/?Items/([^/]+)/PlaybackInfo$`)
	itemFilePathRE       = regexp.MustCompile(`(?i)^(?:/?emby)?/?Items/([^/]+)/(Download|File)$`)
	baseHTMLPlayerPathRE = regexp.MustCompile(`(?i)^(?:/?emby)?/?web/modules/htmlvideoplayer/basehtmlplayer\.js$`)
	htmlCrossOriginRE    = regexp.MustCompile(`mediaSource\.IsRemote\s*&&\s*(?:"DirectPlay"\s*===\s*playMethod|playMethod\s*===\s*"DirectPlay")\s*\?\s*null\s*:\s*"anonymous"`)
	playbackInfoRetries  = 2
	playbackInfoRetryGap = 500 * time.Millisecond
)

type cachedSource struct {
	MediaSourceID string
	ItemID        string
	Path          string
	URL           string
	LastUsed      time.Time
}

type Service struct {
	settings       *settings.Service
	playback       *playback.Service
	strm           *strm.Service
	strmDir        string
	log            *slog.Logger
	client         *http.Client
	portUsedByEmby func(string) bool

	servePlayback func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error

	mu     sync.Mutex
	server *http.Server
	port   int
	err    string

	cacheMu                sync.Mutex
	byMS                   map[string]*cachedSource
	byItem                 map[string]*cachedSource
	cacheEntries           map[*cachedSource]struct{}
	cacheConfig            string
	cacheConfigInitialized bool
}

type Options struct {
	Settings       *settings.Service
	Playback       *playback.Service
	Strm           *strm.Service
	StrmDir        string
	Log            *slog.Logger
	PortUsedByEmby func(string) bool
}

type Config struct {
	Enabled           bool   `json:"enabled"`
	Name              string `json:"name"`
	FnosURL           string `json:"fnos_url"`
	Port              string `json:"proxy_port"`
	PathMaps          string `json:"strm_path_maps"`
	DirectSTRMClients string `json:"direct_strm_clients"`
	StrmDir           string `json:"strm_dir"`
	ProxyURL          string `json:"proxy_url"`
	Running           bool   `json:"running"`
	LastError         string `json:"last_error,omitempty"`
}

type UpdateRequest struct {
	Enabled           bool                     `json:"enabled"`
	Name              string                   `json:"name"`
	FnosURL           string                   `json:"fnos_url"`
	Port              jsonvalue.FlexibleString `json:"proxy_port"`
	PathMaps          string                   `json:"strm_path_maps"`
	DirectSTRMClients string                   `json:"direct_strm_clients"`
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	client := httpx.NewClient(httpx.ClientOptions{DisableCompression: true})
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	strmDir := strings.TrimSpace(opts.StrmDir)
	return &Service{
		settings:       opts.Settings,
		playback:       opts.Playback,
		strm:           opts.Strm,
		strmDir:        strmDir,
		log:            log,
		client:         client,
		portUsedByEmby: opts.PortUsedByEmby,
		servePlayback: func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
			if opts.Playback == nil {
				return domain.Errf(domain.CodeNotImplement)
			}
			return opts.Playback.ServeHTTP(w, r, req, intent)
		},
		byMS:         map[string]*cachedSource{},
		byItem:       map[string]*cachedSource{},
		cacheEntries: map[*cachedSource]struct{}{},
	}
}

func (s *Service) Snapshot(r *http.Request) Config {
	cfg := s.configFromSettings()
	cfg.StrmDir = s.strmDir
	if cfg.Port != "" {
		cfg.ProxyURL = proxybase.PublicBase(r, cfg.Port)
	}
	s.mu.Lock()
	cfg.Running = s.server != nil
	cfg.LastError = s.err
	s.mu.Unlock()
	return cfg
}

func (s *Service) Update(ctx context.Context, in UpdateRequest) (Config, error) {
	if s.settings == nil {
		return Config{}, domain.Errf(domain.CodeNotImplement)
	}
	fnosURL, err := normalizeFnosURL(in.FnosURL, false)
	if err != nil {
		return Config{}, err
	}
	port, err := proxybase.NormalizeOptionalPort(in.Port.String())
	if err != nil {
		return Config{}, err
	}
	pathMaps := normalizeHostStrmRoots(in.PathMaps)
	if in.Enabled && port != "" {
		if fnosURL == "" {
			return Config{}, domain.Errorf(domain.CodeValidation, "启用飞牛反代并填写端口时，需要填写飞牛影视地址")
		}
		if err := s.checkPortConflict(port); err != nil {
			return Config{}, err
		}
	}
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyFnosEnabled:           strconv.FormatBool(in.Enabled),
		settings.KeyFnosName:              normalizeConfigName(in.Name),
		settings.KeyFnosURL:               fnosURL,
		settings.KeyFnosProxyPort:         port,
		settings.KeyFnosStrmPathMaps:      pathMaps,
		settings.KeyFnosDirectSTRMClients: proxybase.NormalizeClientKeywords(in.DirectSTRMClients),
	}); err != nil {
		return Config{}, err
	}
	if err := s.Sync(ctx); err != nil {
		return s.Snapshot(nil), err
	}
	return s.Snapshot(nil), nil
}

func (s *Service) checkPortConflict(port string) error {
	if s.settings == nil || port == "" {
		return nil
	}
	if s.portUsedByEmby != nil && s.portUsedByEmby(port) {
		return domain.Errorf(domain.CodeValidation, "反代端口与 Emby 反代端口冲突")
	}
	return nil
}

func (s *Service) Test(ctx context.Context) error {
	return s.TestConfig(ctx, s.configFromSettings())
}

func (s *Service) TestUpdate(ctx context.Context, in UpdateRequest) error {
	cfg, err := ConfigFromUpdate(in)
	if err != nil {
		return err
	}
	return s.TestConfig(ctx, cfg)
}

func (s *Service) TestConfig(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.FnosURL) == "" {
		return domain.Errorf(domain.CodeValidation, "请先填写飞牛影视地址")
	}
	testCtx, cancel := context.WithTimeout(ctx, proxybase.TestRequestTimeout)
	defer cancel()
	candidates := []string{
		cfg.FnosURL + "/System/Info/Public",
		cfg.FnosURL + "/System/Info",
		cfg.FnosURL + "/",
	}
	var lastErr error
	for _, testURL := range candidates {
		req, err := http.NewRequestWithContext(testCtx, http.MethodGet, testURL, nil)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "飞牛影视地址无效")
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 500 {
			return nil
		}
		lastErr = fnosTestHTTPError(resp.StatusCode)
	}
	if lastErr != nil {
		if ae, ok := lastErr.(*domain.AppError); ok {
			return ae
		}
		return fnosTestConnectError(lastErr)
	}
	return domain.Errorf(domain.CodeDriverError, "飞牛影视地址无法访问")
}

func ConfigFromUpdate(in UpdateRequest) (Config, error) {
	fnosURL, err := normalizeFnosURL(in.FnosURL, false)
	if err != nil {
		return Config{}, err
	}
	port, err := proxybase.NormalizeOptionalPort(in.Port.String())
	if err != nil {
		return Config{}, err
	}
	return Config{
		Enabled:           in.Enabled,
		Name:              normalizeConfigName(in.Name),
		FnosURL:           fnosURL,
		Port:              port,
		PathMaps:          normalizeHostStrmRoots(in.PathMaps),
		DirectSTRMClients: proxybase.NormalizeClientKeywords(in.DirectSTRMClients),
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	if err := s.Sync(ctx); err != nil {
		s.log.Warn("飞牛反代启动失败", "error", err)
	}
}

func (s *Service) Sync(ctx context.Context) error {
	cfg := s.configFromSettings()
	s.syncSourceCacheConfig(cfg)
	port, _ := strconv.Atoi(cfg.Port)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil && (port == 0 || !cfg.Enabled || s.port != port) {
		s.stopLocked(ctx)
	}
	s.err = ""
	if !cfg.Enabled || port == 0 {
		return nil
	}
	if cfg.FnosURL == "" {
		s.err = "启用反代时需要填写飞牛影视地址"
		return domain.Errorf(domain.CodeValidation, "%s", s.err)
	}
	if err := s.checkPortConflict(cfg.Port); err != nil {
		s.err = err.Error()
		return err
	}
	if s.server != nil && s.port == port {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		s.err = fmt.Sprintf("飞牛反代端口 %d 监听失败：%v", port, err)
		return domain.Errorf(domain.CodeDriverError, "%s", s.err)
	}
	s.server = srv
	s.port = port
	go func() {
		s.log.Info("飞牛反代已监听", "addr", srv.Addr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.err = err.Error()
			s.server = nil
			s.port = 0
			s.mu.Unlock()
			s.log.Error("飞牛反代服务异常退出", "error", err)
		}
	}()
	return nil
}

func (s *Service) Shutdown(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked(ctx)
}

func (s *Service) stopLocked(ctx context.Context) {
	if s.server == nil {
		return
	}
	srv := s.server
	s.server = nil
	s.port = 0
	stopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(stopCtx); err != nil {
		_ = srv.Close()
	}
}

func (s *Service) configFromSettings() Config {
	if s.settings == nil {
		return Config{}
	}
	return Config{
		Enabled:           s.settings.Bool(settings.KeyFnosEnabled),
		Name:              normalizeConfigName(s.settings.String(settings.KeyFnosName)),
		FnosURL:           strings.TrimRight(strings.TrimSpace(s.settings.String(settings.KeyFnosURL)), "/"),
		Port:              strings.TrimSpace(s.settings.String(settings.KeyFnosProxyPort)),
		PathMaps:          strings.TrimSpace(s.settings.String(settings.KeyFnosStrmPathMaps)),
		DirectSTRMClients: proxybase.NormalizeClientKeywords(s.settings.StringAllowEmpty(settings.KeyFnosDirectSTRMClients)),
	}
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	escapedPath := r.URL.EscapedPath()
	if proxybase.StrmPlayPathRE.MatchString(escapedPath) {
		s.serveSTRM(w, r)
		return
	}
	cfg := s.configFromSettings()
	if !cfg.Enabled || cfg.FnosURL == "" {
		http.Error(w, "飞牛反代未启用", http.StatusNotFound)
		return
	}
	fullPath := strings.TrimPrefix(r.URL.Path, "/")
	if isStreamRequest(fullPath, r.URL.RawQuery) {
		s.redirectSTRMStream(w, r, cfg, fullPath)
		return
	}
	if itemFilePathRE.MatchString(fullPath) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		// 部分客户端改走 Download/File 取流，需解析 STRM 避免飞牛返回 HTML。
		s.redirectItemFile(w, r, cfg, fullPath)
		return
	}
	if playbackInfoPathRE.MatchString(fullPath) && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		s.modifyPlaybackInfo(w, r, cfg, fullPath)
		return
	}
	if baseHTMLPlayerPathRE.MatchString(fullPath) && r.Method == http.MethodGet {
		s.modifyBaseHTMLPlayer(w, r, cfg, fullPath)
		return
	}
	s.proxyRequest(w, r, cfg, fullPath)
}

func isStreamRequest(fullPath, rawQuery string) bool {
	pathLower := strings.ToLower("/" + strings.TrimPrefix(fullPath, "/"))
	for _, skip := range []string{"/images/", "/additionalparts", "/specialfeatures", "/subtitles"} {
		if strings.Contains(pathLower, skip) {
			return false
		}
	}
	if strings.Contains(pathLower, "/stream.") || strings.Contains(pathLower, "/original.") ||
		strings.Contains(pathLower, "/master.m3u8") {
		return true
	}
	if (strings.Contains(pathLower, "/stream") || strings.HasSuffix(pathLower, "/original") || strings.Contains(pathLower, "/original?")) &&
		(strings.Contains(pathLower, "/videos/") || strings.Contains(strings.ToLower(rawQuery), "mediasourceid=")) {
		return true
	}
	return false
}

func (s *Service) serveSTRM(w http.ResponseWriter, r *http.Request) {
	if s.strm == nil || s.playback == nil {
		http.Error(w, "STRM playback is unavailable", http.StatusNotImplemented)
		return
	}
	m := proxybase.StrmPlayPathRE.FindStringSubmatch(r.URL.EscapedPath())
	if len(m) < 5 {
		http.NotFound(w, r)
		return
	}
	accountID, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || accountID <= 0 {
		http.Error(w, "invalid account id", http.StatusBadRequest)
		return
	}
	fileID, err := strm.DecodeFileKey(m[2])
	if err != nil {
		http.Error(w, "invalid file key", http.StatusBadRequest)
		return
	}
	ok, err := s.strm.MatchToken(r.Context(), m[3])
	if err != nil || !ok {
		http.Error(w, "permission denied", http.StatusForbidden)
		return
	}
	signature := ""
	if len(m) > 5 {
		signature = m[5]
	}
	if s.strm.SignatureEnabled() {
		if signature == "" {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		}
		unsignedPath := strings.TrimSuffix(r.URL.EscapedPath(), "/s/"+signature)
		if !s.strm.VerifySignature(unsignedPath, signature) {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		}
	}
	name, _ := url.PathUnescape(m[4])
	if err := s.servePlaybackHTTP(w, r, playback.Request{AccountID: accountID, FileID: fileID}, playback.Intent{FileName: name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Service) redirectSTRMStream(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	s.log.Debug("飞牛反代播放请求来源",
		"client", proxybase.EmbyClientName(r),
		"user_agent", r.UserAgent(),
	)
	mediaSourceID := queryValue(r, "mediasourceid")
	itemID := ""
	if m := videoStreamPathRE.FindStringSubmatch(fullPath); len(m) > 1 {
		itemID = m[1]
	}
	if itemID == "" {
		itemID = extractItemID(fullPath)
	}

	playURL, strmPath := s.resolvePlayURL(mediaSourceID, itemID, cfg)
	if playURL == "" && itemID != "" {
		// 缓存未命中时从 Item 详情重新定位 STRM。
		if path := s.fetchItemPath(r, cfg, itemID); isStrmPath(path) {
			strmPath = path
			if u := s.readStrmURL(path, cfg); u != "" {
				playURL = u
				s.rememberSource(mediaSourceID, itemID, path, u)
			}
		}
	}
	if playURL != "" {
		if proxybase.MatchesClientKeywords(r, cfg.DirectSTRMClients) {
			proxybase.ServeSTRMDescriptor(w, r, playURL)
			return
		}
		if s.serveLitePanPlayback(w, r, playURL) {
			return
		}
		w.Header().Set("Location", playURL)
		w.WriteHeader(http.StatusFound)
		return
	}
	if strmPath != "" {
		s.log.Warn("飞牛反代无法读取 strm，透传上游", "path", strmPath, "media_source_id", mediaSourceID, "item_id", itemID)
	}
	s.proxyRequest(w, r, cfg, fullPath)
}

func (s *Service) serveLitePanPlayback(w http.ResponseWriter, r *http.Request, playURL string) bool {
	if !isLitePanSTRMURL(playURL) {
		return false
	}
	accountID, fileID, ok := proxybase.ParseLitePanSTRMURL(playURL)
	if !ok {
		s.log.Warn("飞牛反代无法解析 LitePan STRM", "url", playURL)
		http.Error(w, "invalid litepan strm url", http.StatusBadGateway)
		return true
	}
	name := strmFileNameFromPlayURL(playURL)
	if err := s.servePlaybackHTTP(w, r, playback.Request{
		AccountID: accountID,
		FileID:    fileID,
	}, playback.Intent{FileName: name}); err != nil {
		s.log.Warn("飞牛反代解析 LitePan STRM 失败", "url", playURL, "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return true
	}
	return true
}

func (s *Service) resolvePlayURL(mediaSourceID, itemID string, cfg Config) (playURL, strmPath string) {
	src := s.lookupCached(mediaSourceID, itemID)
	if src == nil {
		return "", ""
	}
	strmPath = src.Path
	if src.URL != "" {
		return src.URL, strmPath
	}
	if url := s.readStrmURL(src.Path, cfg); url != "" {
		s.rememberSource(mediaSourceID, itemID, src.Path, url)
		return url, strmPath
	}
	return "", strmPath
}

func (s *Service) redirectItemFile(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	itemID := ""
	if m := itemFilePathRE.FindStringSubmatch(fullPath); len(m) > 1 {
		itemID = m[1]
	}
	playURL, strmPath := s.resolvePlayURL("", itemID, cfg)
	if playURL == "" && itemID != "" {
		// PlaybackInfo 未走过时，按 Item 详情 Path 读 strm
		if path := s.fetchItemPath(r, cfg, itemID); isStrmPath(path) {
			strmPath = path
			playURL = s.readStrmURL(path, cfg)
			if playURL != "" {
				s.rememberSource("", itemID, path, playURL)
			}
		}
	}
	if playURL != "" {
		if s.serveLitePanPlayback(w, r, playURL) {
			return
		}
		w.Header().Set("Location", playURL)
		w.WriteHeader(http.StatusFound)
		return
	}
	if strmPath != "" {
		s.log.Warn("飞牛反代 Item 文件请求无法读 strm，透传上游", "item_id", itemID, "path", strmPath)
	}
	s.proxyRequest(w, r, cfg, fullPath)
}

// fetchItemDetail 拉取用于重新定位 STRM 的 Item 详情，失败返回 nil。
func (s *Service) fetchItemDetail(r *http.Request, cfg Config, itemID string) map[string]any {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || cfg.FnosURL == "" {
		return nil
	}
	q := url.Values{}
	q.Set("Fields", "Path,MediaSources")
	if tok := queryValue(r, "api_key"); tok != "" {
		q.Set("api_key", tok)
	}
	target, err := targetURL(cfg, withEmbyAPIPrefix("Items/"+itemID), q.Encode())
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	copyRequestHeaders(req.Header, r.Header, true)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	body = maybeGunzipBody(resp, body)
	if looksLikeHTML(body) {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	return payload
}

func (s *Service) fetchItemPath(r *http.Request, cfg Config, itemID string) string {
	payload := s.fetchItemDetail(r, cfg, itemID)
	if payload == nil {
		return ""
	}
	if path := strings.TrimSpace(stringValue(payload, "Path")); path != "" {
		return path
	}
	for _, ms := range mediaSources(payload) {
		if path := strings.TrimSpace(stringValue(ms, "Path")); isStrmPath(path) {
			return path
		}
	}
	return ""
}

// modifyPlaybackInfo 缓存 STRM 媒体源、补齐播放字段，并强制从 /emby 路径取得 JSON。
func (s *Service) modifyPlaybackInfo(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	upstreamPath := withEmbyAPIPrefix(fullPath)
	itemID := ""
	if m := playbackInfoPathRE.FindStringSubmatch(fullPath); len(m) > 1 {
		itemID = m[1]
	}
	resp, body, err := s.requestUpstreamWithRetry(r, cfg, upstreamPath, true)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body = maybeGunzipBody(resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeUpstreamBody(w, resp, body)
		return
	}
	if looksLikeHTML(body) {
		s.log.Warn("飞牛反代 PlaybackInfo 收到 HTML，非 JSON", "path", upstreamPath, "client_path", fullPath)
		writeUpstreamBody(w, resp, body)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeUpstreamBody(w, resp, body)
		return
	}
	if itemID == "" {
		itemID = strings.TrimSpace(anyString(payload["ItemId"]))
	}

	changed := false
	for _, mediaSource := range mediaSources(payload) {
		// 补齐客户端要求非空的 MediaStream 字段。
		if normalizeEmbyMediaStreams(mediaSource) {
			changed = true
		}
		if s.rewriteStrmMediaSource(mediaSource, itemID, r, cfg) {
			changed = true
		}
	}
	if changed {
		if out, err := json.Marshal(payload); err == nil {
			body = out
		}
	}
	headers := responseHeaders(resp.Header)
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Set("Content-Length", strconv.Itoa(len(body)))
	headers.Del("Content-Encoding")
	writeHeaders(w, headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// rewriteStrmMediaSource 缓存并预热 STRM 播放地址，同时改写播放能力字段。
func (s *Service) rewriteStrmMediaSource(mediaSource map[string]any, itemID string, r *http.Request, cfg Config) bool {
	mediaSourceID := stringValue(mediaSource, "Id", "ID")
	rawPath := strings.TrimSpace(stringValue(mediaSource, "Path"))
	if !isStrmPath(rawPath) {
		return false
	}
	playURL := s.readStrmURL(rawPath, cfg)
	s.rememberSource(mediaSourceID, itemID, rawPath, playURL)
	s.prewarmPlayback(playURL, r.UserAgent())
	id := strutil.FirstNonEmpty(itemID, stripMediaSourcePrefix(mediaSourceID))
	mediaSource["SupportsDirectStream"] = true
	mediaSource["SupportsDirectPlay"] = true
	mediaSource["SupportsTranscoding"] = false
	mediaSource["DirectStreamUrl"] = proxiedVideoPath(r, id, mediaSourceID)
	mediaSource["Protocol"] = "Http"
	mediaSource["IsRemote"] = true
	delete(mediaSource, "TranscodingUrl")
	delete(mediaSource, "TranscodingSubProtocol")
	delete(mediaSource, "TranscodingContainer")
	return true
}

func proxiedVideoPath(r *http.Request, itemID, mediaSourceID string) string {
	q := r.URL.Query()
	q.Set("MediaSourceId", mediaSourceID)
	if q.Get("static") == "" {
		q.Set("static", "true")
	}
	return "/Videos/" + itemID + "/stream?" + q.Encode()
}

func strmFileNameFromPlayURL(playURL string) string {
	m := proxybase.StrmPlayPathRE.FindStringSubmatch(proxybase.LitePanPath(playURL))
	if len(m) < 5 {
		return ""
	}
	name, err := url.PathUnescape(m[4])
	if err != nil {
		return m[4]
	}
	return name
}

// withEmbyAPIPrefix 保证飞牛 API 走 /emby 前缀，避免 POST 落到 SPA HTML。
func withEmbyAPIPrefix(fullPath string) string {
	p := strings.TrimPrefix(strings.TrimSpace(fullPath), "/")
	if p == "" {
		return p
	}
	if strings.HasPrefix(strings.ToLower(p), "emby/") {
		return p
	}
	return "emby/" + p
}

func maybeGunzipBody(resp *http.Response, body []byte) []byte {
	if resp == nil || !strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		return body
	}
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer gr.Close()
	out, err := io.ReadAll(gr)
	if err != nil {
		return body
	}
	return out
}

func looksLikeHTML(body []byte) bool {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return false
	}
	lower := bytes.ToLower(trim)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) ||
		bytes.HasPrefix(lower, []byte("<html"))
}

func (s *Service) modifyBaseHTMLPlayer(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	resp, body, err := s.requestUpstream(r, cfg, fullPath, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	crossOriginGuard := []byte(";try{(function(){var s=Element.prototype.setAttribute;Element.prototype.setAttribute=function(n,v){if(this&&this.tagName&&/^(VIDEO|AUDIO)$/i.test(this.tagName)&&String(n).toLowerCase()==='crossorigin')return;return s.call(this,n,v)};try{Object.defineProperty(HTMLMediaElement.prototype,'crossOrigin',{get:function(){return null},set:function(){return null},configurable:true})}catch(e){}})()}catch(e){};")
	body = bytes.ReplaceAll(body, []byte(`mediaSource.IsRemote&&"DirectPlay"===playMethod?null:"anonymous"`), []byte("null"))
	body = htmlCrossOriginRE.ReplaceAll(body, []byte("null"))
	if !bytes.Contains(body, []byte("HTMLMediaElement.prototype,'crossOrigin'")) {
		body = append(crossOriginGuard, body...)
	}
	headers := responseHeaders(resp.Header)
	headers.Set("Content-Length", strconv.Itoa(len(body)))
	headers.Set("Cache-Control", "no-store")
	headers.Del("Content-Encoding")
	writeHeaders(w, headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func (s *Service) proxyRequest(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	targetValue, err := targetURL(cfg, fullPath, r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	target, err := url.Parse(targetValue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: s.client.Transport,
		Rewrite: func(req *httputil.ProxyRequest) {
			outURL := *target
			req.Out.URL = &outURL
			req.Out.Host = target.Host
			req.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", rewriteLocation(loc, cfg, r))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if req.Context().Err() != nil {
				return
			}
			s.log.Warn("飞牛反代请求失败", "path", req.URL.Path, "error", err)
			http.Error(w, "飞牛影视服务暂时无法访问", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// requestUpstreamWithRetry 仅对 PlaybackInfo 短暂断连和 400/403 额外重试一次。
func (s *Service) requestUpstreamWithRetry(r *http.Request, cfg Config, fullPath string, identity bool) (*http.Response, []byte, error) {
	var lastErr error
	for attempt := 1; attempt <= playbackInfoRetries; attempt++ {
		resp, body, err := s.requestUpstream(r, cfg, fullPath, identity)
		if err != nil {
			lastErr = err
			if attempt < playbackInfoRetries {
				time.Sleep(playbackInfoRetryGap)
			}
			continue
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			if attempt < playbackInfoRetries {
				time.Sleep(playbackInfoRetryGap)
				continue
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
		return resp, body, nil
	}
	if lastErr != nil {
		return nil, nil, lastErr
	}
	return nil, nil, errors.New("PlaybackInfo 重试失败")
}

func (s *Service) requestUpstream(r *http.Request, cfg Config, fullPath string, identity bool) (*http.Response, []byte, error) {
	target, err := targetURL(cfg, fullPath, r.URL.RawQuery)
	if err != nil {
		return nil, nil, err
	}
	var body io.Reader
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		buf, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(buf))
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		return nil, nil, err
	}
	copyRequestHeaders(req.Header, r.Header, identity)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, data, nil
}

// prewarmPlayback 在 PlaybackInfo 阶段后台预解析直链，缩短首次拉流等待。
func (s *Service) prewarmPlayback(playURL, ua string) {
	if s.playback == nil {
		return
	}
	accountID, fileID, ok := proxybase.ParseLitePanSTRMURL(playURL)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.playback.Resolve(ctx, accountID, fileID, ua, false, true)
	}()
}

func (s *Service) servePlaybackHTTP(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
	if s == nil || s.servePlayback == nil {
		return domain.Errf(domain.CodeNotImplement)
	}
	return s.servePlayback(w, r, req, intent)
}

func (s *Service) rememberSource(mediaSourceID, itemID, strmPath, playURL string) {
	now := time.Now()
	mediaSourceID = strings.TrimSpace(mediaSourceID)
	itemID = strings.TrimSpace(itemID)
	key := stripMediaSourcePrefix(mediaSourceID)
	if mediaSourceID == "" && itemID == "" {
		return
	}
	src := &cachedSource{
		MediaSourceID: mediaSourceID,
		ItemID:        itemID,
		Path:          strings.TrimSpace(strmPath),
		URL:           strings.TrimSpace(playURL),
		LastUsed:      now,
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.pruneExpiredSourceCacheLocked(now)
	if mediaSourceID != "" {
		s.removeSourceLocked(s.byMS[mediaSourceID])
	}
	if key != "" {
		s.removeSourceLocked(s.byMS[key])
	}
	if itemID != "" {
		s.removeSourceLocked(s.byItem[itemID])
	}
	s.cacheEntries[src] = struct{}{}
	if key != "" {
		s.byMS[key] = src
	}
	if mediaSourceID != "" {
		s.byMS[mediaSourceID] = src
	}
	if src.ItemID != "" {
		s.byItem[src.ItemID] = src
	}
	s.enforceSourceCacheLimitLocked()
}

func (s *Service) lookupCached(mediaSourceID, itemID string) *cachedSource {
	now := time.Now()
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.pruneExpiredSourceCacheLocked(now)
	var src *cachedSource
	if mediaSourceID != "" {
		src = s.byMS[mediaSourceID]
		if src == nil {
			src = s.byMS[stripMediaSourcePrefix(mediaSourceID)]
		}
	}
	if src == nil && itemID != "" {
		src = s.byItem[itemID]
	}
	if src == nil {
		return nil
	}
	src.LastUsed = now
	snapshot := *src
	return &snapshot
}

func (s *Service) syncSourceCacheConfig(cfg Config) {
	signature := strings.TrimRight(strings.TrimSpace(cfg.FnosURL), "/") + "\x00" + normalizeHostStrmRoots(cfg.PathMaps)
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if !s.cacheConfigInitialized {
		s.cacheConfig = signature
		s.cacheConfigInitialized = true
		return
	}
	if s.cacheConfig == signature {
		return
	}
	s.clearSourceCacheLocked()
	s.cacheConfig = signature
}

func (s *Service) clearSourceCacheLocked() {
	clear(s.byMS)
	clear(s.byItem)
	clear(s.cacheEntries)
}

func (s *Service) pruneExpiredSourceCacheLocked(now time.Time) {
	for src := range s.cacheEntries {
		if !src.LastUsed.Add(sourceCacheTTL).After(now) {
			s.removeSourceLocked(src)
		}
	}
}

func (s *Service) enforceSourceCacheLimitLocked() {
	for len(s.cacheEntries) > sourceCacheMaxEntries {
		var oldest *cachedSource
		for src := range s.cacheEntries {
			if oldest == nil || src.LastUsed.Before(oldest.LastUsed) {
				oldest = src
			}
		}
		if oldest == nil {
			return
		}
		s.removeSourceLocked(oldest)
	}
}

func (s *Service) removeSourceLocked(src *cachedSource) {
	if src == nil {
		return
	}
	if src.MediaSourceID != "" {
		if s.byMS[src.MediaSourceID] == src {
			delete(s.byMS, src.MediaSourceID)
		}
		key := stripMediaSourcePrefix(src.MediaSourceID)
		if key != "" && s.byMS[key] == src {
			delete(s.byMS, key)
		}
	}
	if src.ItemID != "" && s.byItem[src.ItemID] == src {
		delete(s.byItem, src.ItemID)
	}
	delete(s.cacheEntries, src)
}

func (s *Service) readStrmURL(rawPath string, cfg Config) string {
	candidates := strmPathCandidates(rawPath, s.resolvePathMaps(cfg.PathMaps))
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		for _, rawLine := range strings.Split(string(data), "\n") {
			line := proxybase.CleanWrappedURL(rawLine)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if url := resolveHTTPPlayURL(line); url != "" {
				return url
			}
			break
		}
	}
	return ""
}

func (s *Service) resolvePathMaps(raw string) [][2]string {
	strmDir := strings.TrimRight(strings.TrimSpace(s.strmDir), "/")
	roots := hostStrmRoots(raw)
	out := make([][2]string, 0, len(roots))
	for _, from := range roots {
		out = append(out, [2]string{from, strmDir})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if len(out[j][0]) > len(out[i][0]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func hostStrmRoots(raw string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(line), "\\", "/"), "/")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

func normalizeHostStrmRoots(raw string) string {
	return strings.Join(hostStrmRoots(raw), "\n")
}

func strmPathCandidates(rawPath string, maps [][2]string) []string {
	rawPath = strings.TrimSpace(strings.ReplaceAll(rawPath, "\\", "/"))
	if strings.HasPrefix(rawPath, "file://") {
		if u, err := url.Parse(rawPath); err == nil {
			rawPath = u.Path
		}
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	add(rawPath)
	for _, m := range maps {
		if strings.HasPrefix(rawPath, m[0]) {
			add(m[1] + strings.TrimPrefix(rawPath, m[0]))
		}
	}
	return out
}

func resolveHTTPPlayURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if isLitePanSTRMURL(value) {
		return value
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}

func isStrmPath(value string) bool {
	v := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")))
	if strings.HasPrefix(v, "file://") {
		if u, err := url.Parse(v); err == nil {
			v = strings.ToLower(u.Path)
		}
	}
	return strings.HasSuffix(v, ".strm")
}

func targetURL(cfg Config, fullPath, rawQuery string) (string, error) {
	baseStr := strings.TrimRight(strings.TrimSpace(cfg.FnosURL), "/")
	path := strings.TrimPrefix(strings.TrimSpace(fullPath), "/")
	if strings.HasSuffix(strings.ToLower(baseStr), "/emby") && strings.HasPrefix(strings.ToLower(path), "emby/") {
		path = path[len("emby/"):]
	}
	base, err := url.Parse(baseStr + "/")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(ref)
	target.RawQuery = rawQuery
	return target.String(), nil
}

func normalizeFnosURL(raw string, required bool) (string, error) {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		if required {
			return "", domain.Errorf(domain.CodeValidation, "请填写飞牛影视地址")
		}
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", domain.Errorf(domain.CodeValidation, "飞牛影视地址格式不正确，示例：http://192.168.1.10:8005")
	}
	return v, nil
}

// normalizeConfigName 归一化配置名称：空名回落默认显示名，避免界面出现空白名称。
func normalizeConfigName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "飞牛影视"
	}
	return name
}

func isLitePanSTRMURL(value string) bool {
	return strings.HasPrefix(strings.ToLower(proxybase.LitePanPath(value)), "/api/strm/play/")
}

func extractItemID(fullPath string) string {
	parts := strings.Split(strings.Trim(fullPath, "/"), "/")
	for i, p := range parts {
		if strings.EqualFold(p, "Videos") || strings.EqualFold(p, "Items") || strings.EqualFold(p, "Audio") {
			if i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return ""
}

func responseHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for k, values := range src {
		if _, skip := proxybase.HopByHopHeaderNames[strings.ToLower(k)]; skip {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
	return dst
}

func copyRequestHeaders(dst, src http.Header, identity bool) {
	for k, values := range src {
		if _, skip := proxybase.HopByHopHeaderNames[strings.ToLower(k)]; skip {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
	if identity {
		dst.Set("Accept-Encoding", "identity")
	}
}

func writeHeaders(w http.ResponseWriter, headers http.Header) {
	for k, values := range headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
}

func writeUpstreamBody(w http.ResponseWriter, resp *http.Response, body []byte) {
	writeHeaders(w, responseHeaders(resp.Header))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func rewriteLocation(location string, cfg Config, r *http.Request) string {
	fnosURL := strings.TrimRight(cfg.FnosURL, "/")
	if strings.HasPrefix(location, fnosURL) {
		return strings.TrimRight(proxybase.PublicBase(r, cfg.Port), "/") + strings.TrimPrefix(location, fnosURL)
	}
	return location
}

func queryValue(r *http.Request, key string) string {
	target := strings.ToLower(key)
	for k, values := range r.URL.Query() {
		if strings.ToLower(k) == target && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func stripMediaSourcePrefix(value string) string {
	return strings.TrimPrefix(value, "mediasource_")
}

func anyString(v any) string {
	switch got := v.(type) {
	case string:
		return got
	case json.Number:
		return got.String()
	case fmt.Stringer:
		return got.String()
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}

func stringValue(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
}

func mediaSources(m map[string]any) []map[string]any {
	if m == nil {
		return nil
	}
	raw, ok := m["MediaSources"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if ms, ok := item.(map[string]any); ok {
			out = append(out, ms)
		}
	}
	return out
}

// embyMediaStreamNonNullFields 是部分客户端要求存在且非 null 的字段。
var embyMediaStreamNonNullFields = []string{"Type", "Language", "DisplayLanguage", "Title", "DisplayTitle"}

// normalizeEmbyMediaStreams 补齐必填字符串字段并返回是否发生修改。
func normalizeEmbyMediaStreams(mediaSource map[string]any) bool {
	if mediaSource == nil {
		return false
	}
	raw, ok := mediaSource["MediaStreams"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, item := range raw {
		stream, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range embyMediaStreamNonNullFields {
			if v, exists := stream[field]; !exists || v == nil {
				stream[field] = ""
				changed = true
			}
		}
	}
	return changed
}

func fnosTestHTTPError(status int) *domain.AppError {
	switch status {
	case http.StatusNotFound:
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址不正确，请检查服务地址（默认端口 8005）")
	default:
		if status >= 500 {
			return domain.Errorf(domain.CodeDriverError, "飞牛影视服务异常，请稍后重试")
		}
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址无法访问")
	}
}

func fnosTestConnectError(err error) *domain.AppError {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址连接超时，请检查网络与服务是否在线")
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址无法连接，请检查地址和端口是否正确（默认 8005）")
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "cannot resolve"), strings.Contains(msg, "lookup"):
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址无法解析，请检查主机名或 IP")
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "timeout"):
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址连接超时，请检查网络与服务是否在线")
	default:
		return domain.Errorf(domain.CodeDriverError, "飞牛影视地址无法连接，请检查地址是否正确")
	}
}
