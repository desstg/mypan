package embyproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

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
	maskedSecret              = "******"
	embyMediaSourceCacheTTL   = time.Hour
	embyMediaSourceCacheLimit = 512
)

type cachedMediaSource struct {
	itemID   string
	playURL  string
	lastUsed time.Time
}

var (
	videoStreamPathRE    = regexp.MustCompile(`(?i)^(?:/?emby)?/?Videos/([^/]+)/(stream|original)(?:\.\w+)?$`)
	playbackInfoPathRE   = regexp.MustCompile(`(?i)^(?:/?emby)?/?Items/([^/]+)/PlaybackInfo$`)
	baseHTMLPlayerPathRE = regexp.MustCompile(`(?i)^(?:/?emby)?/?web/modules/htmlvideoplayer/basehtmlplayer\.js$`)
	htmlCrossOriginRE    = regexp.MustCompile(`mediaSource\.IsRemote\s*&&\s*(?:"DirectPlay"\s*===\s*playMethod|playMethod\s*===\s*"DirectPlay")\s*\?\s*null\s*:\s*"anonymous"`)
)

type Service struct {
	settings *settings.Service
	playback *playback.Service
	strm     *strm.Service
	log      *slog.Logger
	client   *http.Client

	servePlayback func(http.ResponseWriter, *http.Request, playback.Request, playback.Intent) error

	mu       sync.Mutex
	runtimes map[string]*runtime

	mediaSourceMu    sync.Mutex
	mediaSourceCache map[string]*cachedMediaSource
}

type runtime struct {
	server *http.Server
	port   int
	err    string
}

type Options struct {
	Settings *settings.Service
	Playback *playback.Service
	Strm     *strm.Service
	Log      *slog.Logger
}

type Config struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	EmbyURL           string `json:"emby_url"`
	APIKey            string `json:"api_key"`
	Port              string `json:"proxy_port"`
	DirectSTRMClients string `json:"direct_strm_clients"`
	ProxyURL          string `json:"proxy_url"`
	Running           bool   `json:"running"`
	LastError         string `json:"last_error,omitempty"`
}

type storedConfig struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	EmbyURL           string `json:"emby_url"`
	APIKey            string `json:"api_key"`
	Port              string `json:"proxy_port"`
	DirectSTRMClients string `json:"direct_strm_clients,omitempty"`
}

type UpdateRequest struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	EmbyURL           string                   `json:"emby_url"`
	APIKey            string                   `json:"api_key"`
	Port              jsonvalue.FlexibleString `json:"proxy_port"`
	DirectSTRMClients string                   `json:"direct_strm_clients"`
}

type State struct {
	Enabled bool     `json:"enabled"`
	Items   []Config `json:"items"`
}

type RefreshResult struct {
	ConfigID    string `json:"config_id"`
	ConfigName  string `json:"config_name"`
	Mode        string `json:"mode"`
	TaskID      string `json:"task_id,omitempty"`
	LibraryID   string `json:"library_id,omitempty"`
	LibraryName string `json:"library_name,omitempty"`
}

type RefreshRequest struct {
	ConfigID  string `json:"config_id"`
	Mode      string `json:"mode"`
	LibraryID string `json:"library_id"`
}

type Library struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CollectionType string `json:"collection_type,omitempty"`
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
	return &Service{
		settings:         opts.Settings,
		playback:         opts.Playback,
		strm:             opts.Strm,
		log:              log,
		client:           client,
		runtimes:         map[string]*runtime{},
		mediaSourceCache: map[string]*cachedMediaSource{},
		servePlayback: func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
			if opts.Playback == nil {
				return domain.Errf(domain.CodeNotImplement)
			}
			return opts.Playback.ServeHTTP(w, r, req, intent)
		},
	}
}

func (s *Service) Snapshots(r *http.Request) []Config {
	configs := s.configsFromSettings()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range configs {
		configs[i].APIKey = maskSecret(configs[i].APIKey)
		if configs[i].Port != "" {
			configs[i].ProxyURL = proxybase.PublicBase(r, configs[i].Port)
		}
		if rt := s.runtimes[configs[i].ID]; rt != nil {
			configs[i].Running = rt.server != nil
			configs[i].LastError = rt.err
		}
	}
	return configs
}

func (s *Service) State(r *http.Request) State {
	items := s.Snapshots(r)
	return State{Enabled: s.enabled() && len(items) > 0, Items: items}
}

func (s *Service) Snapshot(r *http.Request) Config {
	configs := s.Snapshots(r)
	if len(configs) > 0 {
		return configs[0]
	}
	return Config{}
}

func (s *Service) Replace(ctx context.Context, enabled bool, inputs []UpdateRequest) (State, error) {
	if s.settings == nil {
		return State{}, domain.Errf(domain.CodeNotImplement)
	}
	stored := s.configsFromSettings()
	storedByID := make(map[string]Config, len(stored))
	for _, cfg := range stored {
		storedByID[cfg.ID] = cfg
	}
	configs := make([]Config, 0, len(inputs))
	seenIDs := make(map[string]struct{}, len(inputs))
	seenNames := make(map[string]struct{}, len(inputs))
	seenPorts := make(map[string]struct{}, len(inputs))
	for _, in := range inputs {
		cfg, err := ConfigFromUpdate(in)
		if err != nil {
			return State{}, err
		}
		if cfg.ID == "" {
			cfg.ID = uuid.NewString()
		}
		if _, ok := seenIDs[cfg.ID]; ok {
			return State{}, domain.Errorf(domain.CodeValidation, "Emby 配置重复")
		}
		seenIDs[cfg.ID] = struct{}{}
		nameKey := strings.ToLower(cfg.Name)
		if _, ok := seenNames[nameKey]; ok {
			return State{}, domain.Errorf(domain.CodeValidation, "Emby 配置名称不能重复")
		}
		seenNames[nameKey] = struct{}{}
		if old, ok := storedByID[cfg.ID]; ok && isStoredSecretInput(cfg.APIKey, old.APIKey) {
			cfg.APIKey = old.APIKey
		}
		if cfg.EmbyURL == "" || cfg.APIKey == "" {
			return State{}, domain.Errorf(domain.CodeValidation, "请填写 Emby 地址和 API Key")
		}
		if enabled && cfg.Port == "" {
			return State{}, domain.Errorf(domain.CodeValidation, "启用 Emby 反代前，请为所有配置填写反代端口")
		}
		if cfg.Port != "" {
			if _, ok := seenPorts[cfg.Port]; ok {
				return State{}, domain.Errorf(domain.CodeValidation, "多个 Emby 反代不能使用同一个端口")
			}
			seenPorts[cfg.Port] = struct{}{}
			if err := s.checkFnosPortConflict(cfg.Port); err != nil {
				return State{}, err
			}
		}
		configs = append(configs, cfg)
	}
	if len(configs) == 0 {
		enabled = false
	}
	raw, err := json.Marshal(storedConfigs(configs))
	if err != nil {
		return State{}, domain.Wrap(domain.CodeInternal, err)
	}
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyEmbyEnabled:        strconv.FormatBool(enabled),
		settings.KeyEmbyProxyInstances: string(raw),
	}); err != nil {
		return State{}, err
	}
	if err := s.Sync(ctx); err != nil {
		return s.State(nil), err
	}
	return s.State(nil), nil
}

func storedConfigs(configs []Config) []storedConfig {
	out := make([]storedConfig, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, storedConfig{
			ID: cfg.ID, Name: cfg.Name, EmbyURL: cfg.EmbyURL, APIKey: cfg.APIKey, Port: cfg.Port,
			DirectSTRMClients: cfg.DirectSTRMClients,
		})
	}
	return out
}

func (s *Service) checkFnosPortConflict(port string) error {
	if s.settings == nil || port == "" {
		return nil
	}
	if !s.settings.Bool(settings.KeyFnosEnabled) {
		return nil
	}
	fnosPort := strings.TrimSpace(s.settings.String(settings.KeyFnosProxyPort))
	if fnosPort != "" && fnosPort == port {
		return domain.Errorf(domain.CodeValidation, "反代端口与飞牛影视反代端口冲突")
	}
	return nil
}

func (s *Service) UsesPort(port string) bool {
	if !s.enabled() {
		return false
	}
	port = strings.TrimSpace(port)
	for _, cfg := range s.configsFromSettings() {
		if cfg.Port == port {
			return true
		}
	}
	return false
}

func (s *Service) Test(ctx context.Context) error {
	cfg, err := s.resolveConfig("")
	if err != nil {
		return err
	}
	return s.TestConfig(ctx, cfg)
}

func (s *Service) TestUpdate(ctx context.Context, in UpdateRequest) error {
	cfg, err := ConfigFromUpdate(in)
	if err != nil {
		return err
	}
	if old, findErr := s.resolveConfig(in.ID); findErr == nil && isStoredSecretInput(in.APIKey, old.APIKey) {
		cfg.APIKey = old.APIKey
	}
	return s.TestConfig(ctx, cfg)
}

func (s *Service) TestConfig(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.EmbyURL) == "" {
		return domain.Errorf(domain.CodeValidation, "请先填写 Emby 地址")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return domain.Errorf(domain.CodeValidation, "请先填写 Emby API Key")
	}
	testCtx, cancel := context.WithTimeout(ctx, proxybase.TestRequestTimeout)
	defer cancel()
	testURL := cfg.EmbyURL + "/System/Info?" + url.Values{"api_key": {cfg.APIKey}}.Encode()
	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, testURL, nil)
	if err != nil {
		return domain.Errorf(domain.CodeValidation, "Emby 地址无效")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return embyTestHTTPError(resp.StatusCode)
	}
	return nil
}

func (s *Service) ListLibraries(ctx context.Context, configIDs ...string) ([]Library, error) {
	configID := ""
	if len(configIDs) > 0 {
		configID = configIDs[0]
	}
	cfg, err := s.resolveConfig(configID)
	if err != nil {
		return nil, err
	}
	return s.listLibraries(ctx, cfg)
}

func (s *Service) listLibraries(ctx context.Context, cfg Config) ([]Library, error) {
	if strings.TrimSpace(cfg.EmbyURL) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请先填写 Emby 地址")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请先填写 Emby API Key")
	}
	base := strings.TrimRight(cfg.EmbyURL, "/")
	query := url.Values{"api_key": {cfg.APIKey}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/Library/SelectableMediaFolders?"+query, nil)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, embyTestHTTPError(resp.StatusCode)
	}
	var payload []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	out := make([]Library, 0, len(payload))
	for _, item := range payload {
		id := strings.TrimSpace(anyString(item["Id"]))
		name := strings.TrimSpace(anyString(item["Name"]))
		if id == "" || name == "" {
			continue
		}
		out = append(out, Library{
			ID:             id,
			Name:           name,
			CollectionType: strings.TrimSpace(anyString(item["CollectionType"])),
		})
	}
	return out, nil
}

func (s *Service) RefreshLibrary(ctx context.Context, req RefreshRequest) (RefreshResult, error) {
	cfg, err := s.resolveConfig(req.ConfigID)
	if err != nil {
		return RefreshResult{}, err
	}
	if strings.TrimSpace(cfg.EmbyURL) == "" {
		return RefreshResult{}, domain.Errorf(domain.CodeValidation, "请先填写 Emby 地址")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return RefreshResult{}, domain.Errorf(domain.CodeValidation, "请先填写 Emby API Key")
	}
	base := strings.TrimRight(cfg.EmbyURL, "/")
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = "global"
	}
	if mode == "library" {
		result, err := s.refreshLibraryByID(ctx, cfg, strings.TrimSpace(req.LibraryID))
		return withRefreshConfig(result, cfg), err
	}
	result, err := s.refreshAllLibraries(ctx, base, cfg.APIKey)
	return withRefreshConfig(result, cfg), err
}

func withRefreshConfig(result RefreshResult, cfg Config) RefreshResult {
	result.ConfigID = cfg.ID
	result.ConfigName = cfg.Name
	return result
}

func (s *Service) refreshAllLibraries(ctx context.Context, base, apiKey string) (RefreshResult, error) {
	query := url.Values{"api_key": {apiKey}}.Encode()
	taskID, err := s.findLibraryRefreshTask(ctx, base, query)
	if err == nil && taskID != "" {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ScheduledTasks/Running/"+taskID+"?"+query, nil)
		if reqErr != nil {
			return RefreshResult{}, domain.Wrap(domain.CodeInternal, reqErr)
		}
		resp, doErr := s.client.Do(req)
		if doErr != nil {
			return RefreshResult{}, embyTestConnectError(doErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 400 {
			return RefreshResult{Mode: "scheduled_task", TaskID: taskID}, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/Library/Refresh?"+query, nil)
	if err != nil {
		return RefreshResult{}, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return RefreshResult{}, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return RefreshResult{}, embyTestHTTPError(resp.StatusCode)
	}
	return RefreshResult{Mode: "global"}, nil
}

func (s *Service) refreshLibraryByID(ctx context.Context, cfg Config, libraryID string) (RefreshResult, error) {
	if libraryID == "" {
		return RefreshResult{}, domain.Errorf(domain.CodeValidation, "请选择 Emby 媒体库")
	}
	libraries, err := s.listLibraries(ctx, cfg)
	if err != nil {
		return RefreshResult{}, err
	}
	var selected *Library
	for i := range libraries {
		if libraries[i].ID == libraryID {
			selected = &libraries[i]
			break
		}
	}
	if selected == nil {
		return RefreshResult{}, domain.Errorf(domain.CodeValidation, "所选 Emby 媒体库不存在")
	}
	base := strings.TrimRight(cfg.EmbyURL, "/")
	query := url.Values{
		"Recursive":           {"true"},
		"ImageRefreshMode":    {"Default"},
		"MetadataRefreshMode": {"Default"},
		"ReplaceAllImages":    {"false"},
		"ReplaceAllMetadata":  {"false"},
		"api_key":             {cfg.APIKey},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/Items/"+url.PathEscape(libraryID)+"/Refresh?"+query, nil)
	if err != nil {
		return RefreshResult{}, domain.Wrap(domain.CodeInternal, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return RefreshResult{}, embyTestConnectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return RefreshResult{}, embyTestHTTPError(resp.StatusCode)
	}
	return RefreshResult{
		Mode:        "library",
		LibraryID:   selected.ID,
		LibraryName: selected.Name,
	}, nil
}

func (s *Service) findLibraryRefreshTask(ctx context.Context, base, query string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ScheduledTasks?"+query, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", domain.Errorf(domain.CodeDriverError, "读取 Emby 计划任务失败")
	}
	var tasks []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return "", err
	}
	for _, task := range tasks {
		name := strings.ToLower(strings.TrimSpace(anyString(task["Name"])))
		key := strings.TrimSpace(anyString(task["Key"]))
		if strings.Contains(name, "扫描媒体库") || strings.Contains(name, "refresh library") || strings.Contains(name, "scan media library") || key == "RefreshLibrary" {
			return strings.TrimSpace(anyString(task["Id"])), nil
		}
	}
	return "", nil
}

func ConfigFromUpdate(in UpdateRequest) (Config, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Config{}, domain.Errorf(domain.CodeValidation, "请输入 Emby 配置名称")
	}
	if len([]rune(name)) > 40 {
		return Config{}, domain.Errorf(domain.CodeValidation, "Emby 配置名称不能超过 40 个字符")
	}
	embyURL, err := normalizeEmbyURL(in.EmbyURL, false)
	if err != nil {
		return Config{}, err
	}
	port, err := proxybase.NormalizeOptionalPort(in.Port.String())
	if err != nil {
		return Config{}, err
	}
	return Config{
		ID:                strings.TrimSpace(in.ID),
		Name:              name,
		EmbyURL:           embyURL,
		APIKey:            strings.TrimSpace(in.APIKey),
		Port:              port,
		DirectSTRMClients: proxybase.NormalizeClientKeywords(in.DirectSTRMClients),
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	if err := s.Sync(ctx); err != nil {
		s.log.Warn("Emby 反代启动失败", "error", err)
	}
}

func (s *Service) Sync(ctx context.Context) error {
	configs := s.configsFromSettings()
	enabled := s.enabled() && len(configs) > 0
	wanted := make(map[string]Config, len(configs))
	if enabled {
		for _, cfg := range configs {
			wanted[cfg.ID] = cfg
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, rt := range s.runtimes {
		cfg, ok := wanted[id]
		port, _ := strconv.Atoi(cfg.Port)
		if !ok || port == 0 || rt.port != port {
			s.stopRuntimeLocked(ctx, id)
		}
	}
	var firstErr error
	for _, cfg := range configs {
		port, _ := strconv.Atoi(cfg.Port)
		if !enabled || port == 0 {
			continue
		}
		if rt := s.runtimes[cfg.ID]; rt != nil && rt.server != nil && rt.port == port {
			rt.err = ""
			continue
		}
		if err := s.startRuntimeLocked(cfg, port); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) Shutdown(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.runtimes {
		s.stopRuntimeLocked(ctx, id)
	}
}

func (s *Service) startRuntimeLocked(cfg Config, port int) error {
	rt := &runtime{port: port}
	s.runtimes[cfg.ID] = rt
	if cfg.EmbyURL == "" || cfg.APIKey == "" {
		rt.err = "启用反代时需要填写 Emby 地址和 API Key"
		return domain.Errorf(domain.CodeValidation, "%s", rt.err)
	}
	if err := s.checkFnosPortConflict(cfg.Port); err != nil {
		rt.err = err.Error()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.handleConfig(cfg.ID, w, r)
	})
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		rt.err = fmt.Sprintf("Emby 反代端口 %d 监听失败：%v", port, err)
		return domain.Errorf(domain.CodeDriverError, "%s", rt.err)
	}
	rt.server = srv
	go func(id, name string, active *runtime) {
		s.log.Info("Emby 反代已监听", "name", name, "addr", srv.Addr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			if s.runtimes[id] == active {
				active.err = err.Error()
				active.server = nil
				active.port = 0
			}
			s.mu.Unlock()
			s.log.Error("Emby 反代服务异常退出", "name", name, "error", err)
		}
	}(cfg.ID, cfg.Name, rt)
	return nil
}

func (s *Service) stopRuntimeLocked(ctx context.Context, id string) {
	rt := s.runtimes[id]
	if rt == nil {
		return
	}
	delete(s.runtimes, id)
	if rt.server == nil {
		return
	}
	srv := rt.server
	stopCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(stopCtx); err != nil {
		_ = srv.Close()
	}
}

func (s *Service) configsFromSettings() []Config {
	if s.settings == nil {
		return nil
	}
	var configs []Config
	if err := json.Unmarshal([]byte(s.settings.String(settings.KeyEmbyProxyInstances)), &configs); err != nil {
		s.log.Error("Emby 反代配置解析失败", "error", err)
		return nil
	}
	for i := range configs {
		configs[i].ID = strings.TrimSpace(configs[i].ID)
		configs[i].Name = strings.TrimSpace(configs[i].Name)
		configs[i].EmbyURL = strings.TrimRight(strings.TrimSpace(configs[i].EmbyURL), "/")
		configs[i].APIKey = strings.TrimSpace(configs[i].APIKey)
		configs[i].Port = strings.TrimSpace(configs[i].Port)
		configs[i].DirectSTRMClients = proxybase.NormalizeClientKeywords(configs[i].DirectSTRMClients)
	}
	return configs
}

func (s *Service) enabled() bool {
	return s.settings != nil && s.settings.Bool(settings.KeyEmbyEnabled)
}

func (s *Service) resolveConfig(id string) (Config, error) {
	id = strings.TrimSpace(id)
	configs := s.configsFromSettings()
	if id != "" {
		for _, cfg := range configs {
			if cfg.ID == id {
				return cfg, nil
			}
		}
		return Config{}, domain.Errorf(domain.CodeValidation, "所选 Emby 配置不存在")
	}
	if len(configs) > 0 {
		return configs[0], nil
	}
	return Config{}, domain.Errorf(domain.CodeValidation, "请先配置 Emby")
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.resolveConfig("")
	if err != nil {
		http.Error(w, "Emby proxy is not configured", http.StatusNotFound)
		return
	}
	s.handleWithConfig(cfg, w, r)
}

func (s *Service) handleConfig(configID string, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.resolveConfig(configID)
	if err != nil {
		http.Error(w, "Emby proxy is not configured", http.StatusNotFound)
		return
	}
	s.handleWithConfig(cfg, w, r)
}

func (s *Service) handleWithConfig(cfg Config, w http.ResponseWriter, r *http.Request) {
	if proxybase.StrmPlayPathRE.MatchString(r.URL.EscapedPath()) {
		s.serveSTRM(w, r)
		return
	}
	if !s.enabled() || cfg.EmbyURL == "" || cfg.APIKey == "" {
		http.Error(w, "Emby proxy is not enabled", http.StatusNotFound)
		return
	}
	fullPath := strings.TrimPrefix(r.URL.Path, "/")
	if videoStreamPathRE.MatchString(fullPath) {
		s.redirectSTRMStream(w, r, cfg, fullPath)
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
	if err := s.playbackServe(w, r, playback.Request{AccountID: accountID, FileID: fileID}, playback.Intent{FileName: name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Service) playbackServe(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
	if s.servePlayback == nil {
		return domain.Errf(domain.CodeNotImplement)
	}
	return s.servePlayback(w, r, req, intent)
}

func (s *Service) serveLitePanPlayback(w http.ResponseWriter, r *http.Request, litepanURL string) bool {
	accountID, fileID, ok := proxybase.ParseLitePanSTRMURL(litepanURL)
	if !ok {
		return false
	}
	if err := s.playbackServe(w, r, playback.Request{AccountID: accountID, FileID: fileID}, playback.Intent{}); err != nil {
		if isExpectedClientDisconnect(r.Context(), err) {
			s.log.Debug("Emby 反代播放请求已取消", "account_id", accountID, "file_id", fileID, "error", err)
			return true
		}
		s.log.Warn("Emby 反代解析 LitePan STRM 失败", "account_id", accountID, "file_id", fileID, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	return true
}

// isExpectedClientDisconnect 识别播放器探测、跳转 Range 或重建播放链路时主动取消的旧请求。
// 这类错误不代表解析或上游故障，不应记为 Warn，也不再尝试补写 500 响应。
func isExpectedClientDisconnect(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"broken pipe", "connection reset by peer", "use of closed network connection"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func embyMediaSourceCacheKey(cfg Config, mediaSourceID string) string {
	serverKey := strings.TrimSpace(cfg.ID)
	if serverKey == "" {
		serverKey = strings.TrimRight(strings.TrimSpace(cfg.EmbyURL), "/")
	}
	return serverKey + "\x00" + stripMediaSourcePrefix(strings.TrimSpace(mediaSourceID))
}

func (s *Service) rememberMediaSource(cfg Config, itemID, mediaSourceID, playURL string) {
	if strings.TrimSpace(mediaSourceID) == "" || !isLitePanSTRMURL(playURL) {
		return
	}
	now := time.Now()
	key := embyMediaSourceCacheKey(cfg, mediaSourceID)
	s.mediaSourceMu.Lock()
	defer s.mediaSourceMu.Unlock()
	for cacheKey, cached := range s.mediaSourceCache {
		if cached == nil || !cached.lastUsed.Add(embyMediaSourceCacheTTL).After(now) {
			delete(s.mediaSourceCache, cacheKey)
		}
	}
	if len(s.mediaSourceCache) >= embyMediaSourceCacheLimit {
		var oldestKey string
		var oldest time.Time
		for cacheKey, cached := range s.mediaSourceCache {
			if cached != nil && (oldestKey == "" || cached.lastUsed.Before(oldest)) {
				oldestKey = cacheKey
				oldest = cached.lastUsed
			}
		}
		if oldestKey != "" {
			delete(s.mediaSourceCache, oldestKey)
		}
	}
	s.mediaSourceCache[key] = &cachedMediaSource{
		itemID:   strings.TrimSpace(itemID),
		playURL:  strings.TrimSpace(playURL),
		lastUsed: now,
	}
}

func (s *Service) lookupMediaSource(cfg Config, itemID, mediaSourceID string) string {
	if strings.TrimSpace(mediaSourceID) == "" {
		return ""
	}
	now := time.Now()
	key := embyMediaSourceCacheKey(cfg, mediaSourceID)
	s.mediaSourceMu.Lock()
	defer s.mediaSourceMu.Unlock()
	cached := s.mediaSourceCache[key]
	if cached == nil {
		return ""
	}
	if !cached.lastUsed.Add(embyMediaSourceCacheTTL).After(now) {
		delete(s.mediaSourceCache, key)
		return ""
	}
	if cached.itemID != "" && strings.TrimSpace(itemID) != "" && cached.itemID != strings.TrimSpace(itemID) {
		return ""
	}
	cached.lastUsed = now
	return cached.playURL
}

func (s *Service) redirectSTRMStream(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	client := proxybase.EmbyClientName(r)
	if r.Method == http.MethodHead {
		s.proxyRequest(w, r, cfg, fullPath)
		return
	}
	mediaSourceID := queryValue(r, "mediasourceid")
	itemID := ""
	if m := videoStreamPathRE.FindStringSubmatch(fullPath); len(m) > 1 {
		itemID = m[1]
	}
	s.log.Debug("Emby 反代播放请求来源",
		"client", client,
		"user_agent", r.UserAgent(),
		"item_id", itemID,
		"media_source_id", mediaSourceID,
		"path", proxybase.LitePanPath(fullPath),
	)
	if mediaSourceID == "" {
		s.log.Debug("Emby 反代播放未携带媒体源，透传上游", "item_id", itemID)
		s.proxyRequest(w, r, cfg, fullPath)
		return
	}
	if cachedURL := s.lookupMediaSource(cfg, itemID, mediaSourceID); cachedURL != "" {
		accountID, fileID, parsed := proxybase.ParseLitePanSTRMURL(cachedURL)
		s.log.Debug("Emby 反代命中 PlaybackInfo 版本缓存",
			"item_id", itemID,
			"requested_media_source_id", mediaSourceID,
			"account_id", accountID,
			"file_id", fileID,
			"litepan_url_parsed", parsed,
		)
		if proxybase.MatchesClientKeywords(r, cfg.DirectSTRMClients) {
			proxybase.ServeSTRMDescriptor(w, r, cachedURL)
			return
		}
		if s.serveLitePanPlayback(w, r, cachedURL) {
			return
		}
	}
	item := s.fetchEmbyItem(r.Context(), cfg, itemID)
	if item == nil {
		item = s.fetchEmbyItem(r.Context(), cfg, stripMediaSourcePrefix(mediaSourceID))
	}
	if item == nil {
		s.proxyRequest(w, r, cfg, fullPath)
		return
	}
	target := stripMediaSourcePrefix(mediaSourceID)
	for _, mediaSource := range mediaSources(item) {
		current := stripMediaSourcePrefix(stringValue(mediaSource, "Id", "ID"))
		if current != "" && current != target {
			continue
		}
		if redirectURL := s.extractLitePanSTRM(mediaSource, r, cfg); redirectURL != "" {
			accountID, fileID, parsed := proxybase.ParseLitePanSTRMURL(redirectURL)
			s.log.Debug("Emby 反代命中多版本媒体源",
				"item_id", itemID,
				"requested_media_source_id", mediaSourceID,
				"matched_media_source_id", stringValue(mediaSource, "Id", "ID"),
				"account_id", accountID,
				"file_id", fileID,
				"litepan_url_parsed", parsed,
			)
			if proxybase.MatchesClientKeywords(r, cfg.DirectSTRMClients) {
				proxybase.ServeSTRMDescriptor(w, r, redirectURL)
				return
			}
			if s.serveLitePanPlayback(w, r, redirectURL) {
				return
			}
		}
	}
	itemPath := normalizeMediaURL(stringValue(item, "Path"), r, cfg)
	if isLitePanSTRMURL(itemPath) {
		accountID, fileID, parsed := proxybase.ParseLitePanSTRMURL(itemPath)
		s.log.Debug("Emby 反代多版本未命中，回退影片公共路径",
			"item_id", itemID,
			"requested_media_source_id", mediaSourceID,
			"account_id", accountID,
			"file_id", fileID,
			"litepan_url_parsed", parsed,
		)
		if proxybase.MatchesClientKeywords(r, cfg.DirectSTRMClients) {
			proxybase.ServeSTRMDescriptor(w, r, itemPath)
			return
		}
		if s.serveLitePanPlayback(w, r, itemPath) {
			return
		}
	}
	s.proxyRequest(w, r, cfg, fullPath)
}

func (s *Service) modifyPlaybackInfo(w http.ResponseWriter, r *http.Request, cfg Config, fullPath string) {
	resp, body, err := s.requestUpstream(r, cfg, fullPath, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeUpstreamBody(w, resp, body)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeUpstreamBody(w, resp, body)
		return
	}
	itemID := ""
	if m := playbackInfoPathRE.FindStringSubmatch(fullPath); len(m) > 1 {
		itemID = m[1]
	}
	item := s.fetchEmbyItem(r.Context(), cfg, itemID)
	changed := false
	for _, mediaSource := range mediaSources(payload) {
		mediaSourceID := stringValue(mediaSource, "Id", "ID")
		itemMediaSource := findMediaSourceByID(item, mediaSourceID)
		litepanURL := s.extractLitePanSTRM(mediaSource, r, cfg)
		resolvedFrom := "playback_info"
		if litepanURL == "" && itemMediaSource != nil {
			litepanURL = s.extractLitePanSTRM(itemMediaSource, r, cfg)
			resolvedFrom = "item_media_source"
		}
		itemPath := normalizeMediaURL(stringValue(item, "Path"), r, cfg)
		if litepanURL == "" && isLitePanSTRMURL(itemPath) {
			litepanURL = itemPath
			resolvedFrom = "item_path_fallback"
		}
		if litepanURL == "" {
			s.log.Debug("Emby 反代 PlaybackInfo 未找到 LitePan STRM",
				"item_id", itemID,
				"media_source_id", mediaSourceID,
			)
			continue
		}
		accountID, fileID, parsed := proxybase.ParseLitePanSTRMURL(litepanURL)
		s.log.Debug("Emby 反代 PlaybackInfo 版本映射",
			"item_id", itemID,
			"media_source_id", mediaSourceID,
			"resolved_from", resolvedFrom,
			"account_id", accountID,
			"file_id", fileID,
			"litepan_url_parsed", parsed,
		)
		s.rememberMediaSource(cfg, itemID, mediaSourceID, litepanURL)
		directPath := proxiedVideoPath(r, cfg, strutil.FirstNonEmpty(itemID, stripMediaSourcePrefix(mediaSourceID)), mediaSourceID)
		mediaSource["SupportsDirectPlay"] = true
		mediaSource["SupportsDirectStream"] = true
		mediaSource["SupportsTranscoding"] = false
		mediaSource["Protocol"] = "Http"
		mediaSource["DirectStreamUrl"] = directPath
		delete(mediaSource, "TranscodingUrl")
		delete(mediaSource, "TranscodingSubProtocol")
		delete(mediaSource, "TranscodingContainer")
		delete(mediaSource, "TranscodingLiveStartIndex")
		delete(mediaSource, "TrancodeLiveStartIndex")
		if info := s.inferLitePanMediaInfo(r.Context(), litepanURL, r.UserAgent()); info != nil {
			for k, v := range info {
				mediaSource[k] = v
			}
		}
		changed = true
	}
	if !changed {
		writeUpstreamBody(w, resp, body)
		return
	}
	out, _ := json.Marshal(payload)
	headers := responseHeaders(resp.Header)
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Set("Content-Length", strconv.Itoa(len(out)))
	headers.Del("Content-Encoding")
	writeHeaders(w, headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
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
	target, err := targetURL(cfg, fullPath, r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var body io.Reader
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		data, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusBadRequest)
			return
		}
		if len(data) > 0 {
			body = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyRequestHeaders(req.Header, r.Header, false)
	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	headers := responseHeaders(resp.Header)
	if loc := headers.Get("Location"); loc != "" {
		headers.Set("Location", rewriteLocation(loc, cfg, r))
	}
	writeHeaders(w, headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.CopyBuffer(w, resp.Body, make([]byte, 128*1024))
}

func (s *Service) requestUpstream(r *http.Request, cfg Config, fullPath string, identity bool) (*http.Response, []byte, error) {
	target, err := targetURL(cfg, fullPath, r.URL.RawQuery)
	if err != nil {
		return nil, nil, err
	}
	var body io.Reader
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		buf, _ := io.ReadAll(r.Body)
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

func (s *Service) fetchEmbyItem(ctx context.Context, cfg Config, itemID string) map[string]any {
	itemID = stripMediaSourcePrefix(itemID)
	if itemID == "" {
		return nil
	}
	params := url.Values{
		"Ids":       {itemID},
		"Limit":     {"1"},
		"Fields":    {"Path,MediaSources"},
		"Recursive": {"true"},
		"api_key":   {cfg.APIKey},
	}
	base := strings.TrimRight(cfg.EmbyURL, "/")
	candidates := []string{base + "/emby/Items", base + "/Items"}
	if strings.HasSuffix(strings.ToLower(base), "/emby") {
		candidates = []string{base + "/Items"}
	}
	for _, endpoint := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
		if err != nil {
			continue
		}
		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}
		var payload struct {
			Items []map[string]any `json:"Items"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err == nil && resp.StatusCode < 400 && len(payload.Items) > 0 {
			return payload.Items[0]
		}
	}
	return nil
}

func (s *Service) extractLitePanSTRM(mediaSource map[string]any, r *http.Request, cfg Config) string {
	for _, key := range []string{"Path", "DirectStreamUrl", "DirectStreamURL", "TranscodingUrl"} {
		value := normalizeMediaURL(stringValue(mediaSource, key), r, cfg)
		if isLitePanSTRMURL(value) {
			return value
		}
	}
	return ""
}

func (s *Service) inferLitePanMediaInfo(ctx context.Context, litepanURL, ua string) map[string]any {
	if s.playback == nil {
		return nil
	}
	accountID, fileID, ok := proxybase.ParseLitePanSTRMURL(litepanURL)
	if !ok {
		return nil
	}
	res, err := s.playback.Resolve(ctx, accountID, fileID, ua, false, true)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	name := strings.TrimSpace(res.File.Name)
	if name == "" {
		name = strings.TrimSpace(res.Link.FileName)
	}
	if name != "" {
		out["Name"] = name
		if ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), "."); ext != "" {
			out["Container"] = ext
		}
	}
	size := res.File.Size
	if size <= 0 {
		size = res.Link.Size
	}
	if size > 0 {
		out["Size"] = size
	}
	return out
}

func targetURL(cfg Config, fullPath, rawQuery string) (string, error) {
	baseStr := strings.TrimRight(strings.TrimSpace(cfg.EmbyURL), "/")
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

func normalizeEmbyURL(raw string, required bool) (string, error) {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		if required {
			return "", domain.Errorf(domain.CodeValidation, "请填写 Emby 地址")
		}
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", domain.Errorf(domain.CodeValidation, "Emby 地址格式不正确，示例：http://192.168.1.10:8096")
	}
	return v, nil
}

func proxiedVideoPath(r *http.Request, cfg Config, itemID, mediaSourceID string) string {
	q := r.URL.Query()
	q.Set("MediaSourceId", mediaSourceID)
	if q.Get("static") == "" {
		q.Set("static", "true")
	}
	if q.Get("api_key") == "" {
		q.Set("api_key", cfg.APIKey)
	}
	return "/Videos/" + itemID + "/stream?" + q.Encode()
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

func normalizeMediaURL(candidate string, r *http.Request, cfg Config) string {
	value := proxybase.CleanWrappedURL(candidate)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	if strings.HasPrefix(value, "/") {
		return strings.TrimRight(proxybase.PublicBase(r, cfg.Port), "/") + value
	}
	return value
}

func isLitePanSTRMURL(value string) bool {
	return strings.HasPrefix(strings.ToLower(proxybase.LitePanPath(value)), "/api/strm/play/")
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
	embyURL := strings.TrimRight(cfg.EmbyURL, "/")
	if strings.HasPrefix(location, embyURL) {
		return strings.TrimRight(proxybase.PublicBase(r, cfg.Port), "/") + strings.TrimPrefix(location, embyURL)
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

func findMediaSourceByID(item map[string]any, mediaSourceID string) map[string]any {
	target := stripMediaSourcePrefix(mediaSourceID)
	for _, mediaSource := range mediaSources(item) {
		current := stripMediaSourcePrefix(stringValue(mediaSource, "Id", "ID"))
		if current == "" || current == target {
			return mediaSource
		}
	}
	return nil
}

func maskSecret(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	n := len(v)
	if n <= 4 {
		if n <= 2 {
			return strings.Repeat("*", n)
		}
		return v[:1] + strings.Repeat("*", n-2) + v[n-1:]
	}
	if n <= 8 {
		return v[:2] + "****" + v[n-2:]
	}
	return v[:4] + "****" + v[n-4:]
}

func isStoredSecretInput(input, stored string) bool {
	input = strings.TrimSpace(input)
	stored = strings.TrimSpace(stored)
	if input == "" || stored == "" {
		return false
	}
	if input == stored || input == maskedSecret {
		return true
	}
	return input == maskSecret(stored)
}

func embyTestHTTPError(status int) *domain.AppError {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return domain.Errorf(domain.CodeDriverError, "Emby API Key 不正确")
	case http.StatusNotFound:
		return domain.Errorf(domain.CodeDriverError, "Emby 地址不正确，请检查服务地址")
	default:
		if status >= 500 {
			return domain.Errorf(domain.CodeDriverError, "Emby 服务异常，请稍后重试")
		}
		return domain.Errorf(domain.CodeDriverError, "Emby 地址无法访问")
	}
}

func embyTestConnectError(err error) *domain.AppError {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return domain.Errorf(domain.CodeDriverError, "Emby 地址连接超时，请检查网络与服务是否在线")
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return domain.Errorf(domain.CodeDriverError, "Emby 地址无法连接，请检查地址和端口是否正确")
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "cannot resolve"), strings.Contains(msg, "lookup"):
		return domain.Errorf(domain.CodeDriverError, "Emby 地址无法解析，请检查主机名或 IP")
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "timeout"):
		return domain.Errorf(domain.CodeDriverError, "Emby 地址连接超时，请检查网络与服务是否在线")
	default:
		return domain.Errorf(domain.CodeDriverError, "Emby 地址无法连接，请检查地址是否正确")
	}
}
