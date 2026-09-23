package jav

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/mediaserver"
	"litepan/internal/settings"
)

// 库同步的默认值，与源码 start_library_quality_backfill 一致。
const (
	defaultBackfillBatch = 20
	defaultBackfillPause = 300 * time.Millisecond
)

// ServerView 是媒体服务器的对外形态。
//
// APIKey **不在**这里 —— 它是只写字段。返回了的话前端会把掩码回写，
// 与 JAVDB 凭据是同一个坑。
type ServerView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Type       string `json:"type"`
	Enabled    bool   `json:"enabled"`
	HasAPIKey  bool   `json:"has_api_key"`
	LastSyncAt string `json:"last_sync_at"`
	LastStatus string `json:"last_status"`
	LastError  string `json:"last_error"`
	ItemCount  int    `json:"item_count"`
	CodeCount  int    `json:"code_count"`
}

// ServerInput 是增改媒体服务器的入参。APIKey 空串 = 不修改。
type ServerInput struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Type    string `json:"type"`
	Enabled *bool  `json:"enabled"`
}

// SyncStatus 是库同步的当前状态。
type SyncStatus struct {
	Running   bool   `json:"running"`
	Trigger   string `json:"trigger"`
	StartedAt string `json:"started_at"`
	Finished  string `json:"finished_at"`
	Message   string `json:"message"`
	// Servers 是每台服务器本轮的结果摘要。
	Servers []ServerSyncResult `json:"servers"`
}

// ServerSyncResult 是单台服务器一次同步的结果。
type ServerSyncResult struct {
	ServerID int64  `json:"server_id"`
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Items    int    `json:"items"`
	Codes    int    `json:"codes"`
	Message  string `json:"message"`
}

// mediaServerClient 按服务器配置构造客户端，URL/Key 变了就重建。
//
// 缓存而不是每次现构造：同步一个几千部的库要发几十次分页请求，
// 每次现构造会丢掉连接复用，把一轮同步从几秒拖成几十秒。
func (s *Service) mediaServerClient(srv *domain.JavMediaServer) (*mediaserver.Client, error) {
	key := srv.URL + "|" + srv.APIKey + "|" + srv.Type
	s.clientMu.Lock()
	if cached, ok := s.srvClients[srv.ID]; ok && cached.key == key {
		client := cached.client
		s.clientMu.Unlock()
		return client, nil
	}
	s.clientMu.Unlock()

	client, err := mediaserver.New(mediaserver.Options{
		URL:      srv.URL,
		APIKey:   srv.APIKey,
		Name:     srv.Name,
		Type:     srv.Type,
		Timeout:  s.timeout(),
		ProxyURL: s.proxyURL(),
	})
	if err != nil {
		return nil, err
	}

	s.clientMu.Lock()
	s.srvClients[srv.ID] = &serverClientRef{client: client, key: key}
	s.clientMu.Unlock()
	return client, nil
}

// serverClientRef 是缓存的客户端条目。
type serverClientRef struct {
	client *mediaserver.Client
	key    string
}

// ————————————————————— 服务器 CRUD —————————————————————

// ListServers 列媒体服务器。
func (s *Service) ListServers(ctx context.Context) ([]ServerView, error) {
	rows, err := s.servers.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ServerView, 0, len(rows))
	for _, srv := range rows {
		out = append(out, toServerView(srv))
	}
	return out, nil
}

func toServerView(srv *domain.JavMediaServer) ServerView {
	return ServerView{
		ID: srv.ID, Name: srv.Name, URL: srv.URL, Type: srv.Type, Enabled: srv.Enabled,
		HasAPIKey:  strings.TrimSpace(srv.APIKey) != "",
		LastSyncAt: formatTS(srv.LastSyncAt),
		LastStatus: srv.LastStatus,
		LastError:  srv.LastError,
		ItemCount:  srv.ItemCount,
		CodeCount:  srv.CodeCount,
	}
}

// AddServer 加一台媒体服务器。同地址会复用已有那一行。
func (s *Service) AddServer(ctx context.Context, in ServerInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	url := strings.TrimSpace(in.URL)
	apiKey := strings.TrimSpace(in.APIKey)
	if name == "" {
		return 0, domain.Errorf(domain.CodeValidation, "名称不能为空")
	}
	if url == "" {
		return 0, domain.Errorf(domain.CodeValidation, "地址不能为空")
	}
	if apiKey == "" {
		return 0, domain.Errorf(domain.CodeValidation, "API Key 不能为空")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Type))
	if kind != domain.JavServerTypeJellyfin {
		kind = domain.JavServerTypeEmby
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	id, err := s.servers.Create(ctx, &domain.JavMediaServer{
		Name: name, URL: url, APIKey: apiKey, Type: kind, Enabled: enabled,
	})
	if err != nil {
		return 0, err
	}
	s.invalidateServerClient(id)
	return id, nil
}

// UpdateServer 改一台媒体服务器。
func (s *Service) UpdateServer(ctx context.Context, id int64, in ServerInput) error {
	srv, err := s.servers.Get(ctx, id)
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		srv.Name = name
	}
	if url := strings.TrimSpace(in.URL); url != "" {
		srv.URL = url
	}
	if kind := strings.ToLower(strings.TrimSpace(in.Type)); kind == domain.JavServerTypeJellyfin || kind == domain.JavServerTypeEmby {
		srv.Type = kind
	}
	// API Key 留空 = 不修改：它从不回传，用户也就无从「原样回写」。
	if apiKey := strings.TrimSpace(in.APIKey); apiKey != "" {
		srv.APIKey = apiKey
	}
	if in.Enabled != nil {
		srv.Enabled = *in.Enabled
	}
	if err := s.servers.Update(ctx, srv); err != nil {
		return err
	}
	s.invalidateServerClient(id)
	return nil
}

// DeleteServer 删一台媒体服务器（连带清掉它的库内条目）。
func (s *Service) DeleteServer(ctx context.Context, id int64) error {
	if err := s.servers.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidateServerClient(id)
	return nil
}

func (s *Service) invalidateServerClient(id int64) {
	s.clientMu.Lock()
	delete(s.srvClients, id)
	s.clientMu.Unlock()
}

// TestServer 探活一台服务器。
func (s *Service) TestServer(ctx context.Context, id int64) ConnectionResult {
	srv, err := s.servers.Get(ctx, id)
	if err != nil {
		return ConnectionResult{Message: err.Error()}
	}
	client, err := s.mediaServerClient(srv)
	if err != nil {
		// 探活结果的 Message 直接显示给用户，带上真实的构造错误
		// （多半是地址写错了）比一句「连接失败」有用。
		return ConnectionResult{Message: upstreamErr(err).Error()}
	}
	start := time.Now()
	name, err := client.Ping(ctx)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return ConnectionResult{Message: err.Error(), LatencyMS: latency}
	}
	return ConnectionResult{OK: true, Message: "连通成功", Detail: name, LatencyMS: latency}
}

// ————————————————————— 同步 —————————————————————

// syncState 是一轮全量同步的进度快照。互斥量挂在 Service 上（见 service.go）。
type syncState struct {
	running   bool
	trigger   string
	startedAt time.Time
	finished  time.Time
	message   string
	servers   []ServerSyncResult
}

// SyncStatus 返回当前的同步状态。
func (s *Service) SyncStatus(ctx context.Context) SyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	out := SyncStatus{
		Running:   s.syncState.running,
		Trigger:   s.syncState.trigger,
		StartedAt: formatTS(s.syncState.startedAt),
		Finished:  formatTS(s.syncState.finished),
		Message:   s.syncState.message,
		Servers:   append([]ServerSyncResult{}, s.syncState.servers...),
	}
	return out
}

// runLibrarySync 同步全部启用的服务器。
//
// 已经在跑时直接返回 false（调用方据此给出「同步正在进行中」）。
func (s *Service) runLibrarySync(ctx context.Context, trigger string) bool {
	s.syncMu.Lock()
	if s.syncState.running {
		s.syncMu.Unlock()
		return false
	}
	s.syncState = syncState{running: true, trigger: trigger, startedAt: time.Now()}
	s.syncMu.Unlock()

	defer func() {
		s.syncMu.Lock()
		s.syncState.running = false
		s.syncState.finished = time.Now()
		servers := append([]ServerSyncResult{}, s.syncState.servers...)
		s.syncMu.Unlock()

		s.markLibrarySync(servers)
	}()

	rows, err := s.servers.List(ctx)
	if err != nil {
		s.setSyncMessage("读取服务器列表失败：" + err.Error())
		return true
	}

	results := make([]ServerSyncResult, 0, len(rows))
	for _, srv := range rows {
		if ctx.Err() != nil {
			break
		}
		if !srv.Enabled {
			continue
		}
		results = append(results, s.syncOneServer(ctx, srv))
	}

	s.syncMu.Lock()
	s.syncState.servers = results
	s.syncMu.Unlock()

	// 全量同步之后慢慢补画质。不在这里一次补完：那要逐条查 /Items/{id}，
	// 一个几千部的库会持续压着媒体服务器，所以是「空闲时慢慢补」。
	s.backfillQuality(ctx)
	return true
}

// SyncServer 单独同步一台。
func (s *Service) SyncServer(ctx context.Context, id int64) (ServerSyncResult, error) {
	srv, err := s.servers.Get(ctx, id)
	if err != nil {
		return ServerSyncResult{}, err
	}
	return s.syncOneServer(ctx, srv), nil
}

// syncOneServer 同步一台服务器。
//
// 与源码 sync.sync_library 的分歧点在这里：**不做 clear_library**。
// 源码每次全量同步先清库再重写，结果是质检回填过的 resolution 每次都被抹掉、
// 要靠后台循环慢慢补回来。这里改成「翻页 upsert 全部 + 删掉本轮没见到的」，
// 回填成果不会每次同步丢一次。
func (s *Service) syncOneServer(ctx context.Context, srv *domain.JavMediaServer) ServerSyncResult {
	res := ServerSyncResult{ServerID: srv.ID, Name: srv.Name}
	now := time.Now()

	client, err := s.mediaServerClient(srv)
	if err != nil {
		res.Message = err.Error()
		_ = s.servers.MarkSync(ctx, srv.ID, now, domain.JavServerStatusError, err.Error(), 0, 0)
		return res
	}

	items, err := client.FetchAllMovies(ctx)
	if err != nil {
		// 连不上时**不动**已有数据：清空的话，一次网络抖动就会让整个
		// 「已入库」标记全部失效，用户看到的是库突然空了。
		res.Message = err.Error()
		_ = s.servers.MarkSync(ctx, srv.ID, now, domain.JavServerStatusError, err.Error(), 0, 0)
		return res
	}

	codes := 0
	for _, item := range items {
		// 番号优先从 Name 提，提不到再从 Path 提 —— 库的命名习惯千差万别，
		// 有的把番号放在文件名里，有的放在目录名里。
		code := mediaserver.ExtractCode(item.Name)
		if code == "" {
			code = mediaserver.ExtractCode(item.Path)
		}
		if code != "" {
			codes++
		}
		if err := s.library.Upsert(ctx, &domain.JavLibraryItem{
			ServerID: srv.ID,
			ItemID:   item.ID,
			Code:     code,
			Title:    item.Name,
			Path:     item.Path,
			SyncedAt: now,
		}); err != nil {
			s.logWarn("jav upsert library item failed", "server", srv.ID, "item", item.ID, "err", err)
		}
	}

	// 删掉本轮没见到的：用户从媒体库里删了片，这里也要跟着去掉，
	// 否则「已入库」角标会一直亮着。
	if removed, err := s.library.DeleteMissing(ctx, srv.ID, now); err != nil {
		s.logWarn("jav delete missing library items failed", "server", srv.ID, "err", err)
	} else if removed > 0 {
		s.logInfo("jav removed stale library items", "server", srv.ID, "removed", removed)
	}

	res.OK = true
	res.Items = len(items)
	res.Codes = codes
	res.Message = "同步完成"
	_ = s.servers.MarkSync(ctx, srv.ID, now, domain.JavServerStatusOK, "", len(items), codes)
	return res
}

// backfillQuality 补一批条目的画质信息。
//
// 每批 20 部、条目之间停顿 0.3 秒，与源码一致：这一步要逐条查
// /Items/{id}，全量跑会持续压着媒体服务器。全部补齐后每轮耗时趋零。
func (s *Service) backfillQuality(ctx context.Context) {
	batch := s.settings.Int(settings.KeyJavLibraryBackfillN)
	if batch <= 0 {
		batch = defaultBackfillBatch
	}
	pending, err := s.library.ListPendingQuality(ctx, batch)
	if err != nil {
		s.logWarn("jav list pending quality failed", "err", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	pauseMS := s.settings.Int(settings.KeyJavLibraryBackfillMS)
	if pauseMS <= 0 {
		pauseMS = int(defaultBackfillPause / time.Millisecond)
	}

	// 按服务器缓存客户端，别在循环里反复构造。
	clients := make(map[int64]*mediaserver.Client)
	updated := 0
	for _, item := range pending {
		if ctx.Err() != nil {
			return
		}
		client, ok := clients[item.ServerID]
		if !ok {
			srv, serr := s.servers.Get(ctx, item.ServerID)
			if serr != nil {
				clients[item.ServerID] = nil
				continue
			}
			client, serr = s.mediaServerClient(srv)
			if serr != nil {
				client = nil
			}
			clients[item.ServerID] = client
		}
		if client == nil {
			continue
		}

		resolution, sizeBytes, merr := client.ItemMedia(ctx, item.ItemID)
		if merr != nil {
			s.logWarn("jav item media failed", "server", item.ServerID, "item", item.ItemID, "err", merr)
			continue
		}
		// 查得到就写，哪怕 resolution 是 0（普通画质）—— 0 也是**已知**，
		// 不写的话这条会被永远当成「还没质检」反复重查。
		if err := s.library.Upsert(ctx, &domain.JavLibraryItem{
			ServerID: item.ServerID, ItemID: item.ItemID, Code: item.Code, Title: item.Title,
			Path: item.Path, Resolution: resolution, HasQuality: true,
			SizeBytes: sizeBytes, SyncedAt: item.SyncedAt,
		}); err != nil {
			s.logWarn("jav backfill upsert failed", "item", item.ItemID, "err", err)
			continue
		}
		updated++

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(pauseMS) * time.Millisecond):
		}
	}
	if updated > 0 {
		s.logInfo("jav quality backfill done", "updated", updated)
	}
}

// setSyncMessage 记一条同步摘要。
func (s *Service) setSyncMessage(msg string) {
	s.syncMu.Lock()
	s.syncState.message = msg
	s.syncMu.Unlock()
}

// markLibrarySync 把本轮结果写进设置，供设置页显示。
func (s *Service) markLibrarySync(results []ServerSyncResult) {
	status := domain.JavServerStatusOK
	message := ""
	for _, r := range results {
		if !r.OK {
			status = domain.JavServerStatusError
			message = r.Name + "：" + r.Message
			break
		}
	}
	s.setSyncMessage(message)

	if s.settings == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyJavLibraryLastRunAt:   time.Now().Format(time.RFC3339),
		settings.KeyJavLibrarySyncStatus:  status,
		settings.KeyJavLibrarySyncMessage: message,
	})
}

// ————————————————————— 库查询 —————————————————————

// LibraryServerStatsView 是「媒体库影片数量」里的一格。
//
// 不能把 domain.JavLibraryStats 直接塞进响应：那个结构体**一个 json tag 都没有**，
// 序列化出去是 ServerID / ItemCount 这种大驼峰，而前端读的是 server_id /
// item_count —— 对不上不会报错，只会让那一格**整块空白**（这个坑踩过了）。
type LibraryServerStatsView struct {
	ServerID      int64  `json:"server_id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	URL           string `json:"url"`
	ItemCount     int    `json:"item_count"`
	CodeCount     int    `json:"code_count"`
	DistinctCodes int    `json:"distinct_codes"`
	LastSyncAt    string `json:"last_sync_at"`
	LastStatus    string `json:"last_status"`
	LastError     string `json:"last_error"`
}

// LibraryStats 返回各服务器的计数与全局去重番号数。
func (s *Service) LibraryStats(ctx context.Context) (map[string]any, error) {
	stats, totalCodes, totalItems, err := s.library.Stats(ctx)
	if err != nil {
		return nil, err
	}
	servers := make([]LibraryServerStatsView, 0, len(stats))
	withCode, withoutCode := 0, 0
	for _, st := range stats {
		withCode += st.CodeCount
		withoutCode += st.ItemCount - st.CodeCount
		servers = append(servers, LibraryServerStatsView{
			ServerID:      st.ServerID,
			Name:          st.Name,
			Type:          st.Type,
			URL:           st.URL,
			ItemCount:     st.ItemCount,
			CodeCount:     st.CodeCount,
			DistinctCodes: st.DistinctCodes,
			LastSyncAt:    formatTS(st.LastSyncAt),
			LastStatus:    st.LastStatus,
			LastError:     st.LastError,
		})
	}

	// 「已入库」的影片数：界面上下角标真的会标成「已入库」的那些。
	// 数不出来只影响一格显示，不该让整个统计接口失败。
	inLibrary, ierr := s.library.CountMoviesInLibrary(ctx)
	if ierr != nil {
		s.logWarn("jav count in-library movies failed", "err", ierr)
	}

	return map[string]any{
		"servers":              servers,
		"total_distinct_codes": totalCodes,
		"total_items":          totalItems,
		"movies_with_code":     withCode,
		"movies_without_code":  withoutCode,
		"in_library_movies":    inLibrary,
	}, nil
}

// LibraryItemView 是库内条目的对外形态。
//
// 这里**没有** api_key 字段 —— 不是靠约定不填，是结构体里根本没有。
// 源码在 library_stream_lookup 上专门写了「严禁注入模板」的警告。
type LibraryItemView struct {
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	ItemID     string `json:"item_id"`
	Code       string `json:"code"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Resolution int    `json:"resolution"`
	HasQuality bool   `json:"has_quality"`
	SizeBytes  int64  `json:"size_bytes"`
	SyncedAt   string `json:"synced_at"`
}

// LibraryLookup 按番号查库内条目。
func (s *Service) LibraryLookup(ctx context.Context, code string) ([]LibraryItemView, error) {
	items, err := s.library.Lookup(ctx, code)
	if err != nil {
		return nil, err
	}
	names := s.serverNames(ctx)
	out := make([]LibraryItemView, 0, len(items))
	for _, it := range items {
		out = append(out, LibraryItemView{
			ServerID: it.ServerID, ServerName: names[it.ServerID],
			ItemID: it.ItemID, Code: it.Code, Title: it.Title, Path: it.Path,
			Resolution: it.Resolution, HasQuality: it.HasQuality,
			SizeBytes: it.SizeBytes, SyncedAt: formatTS(it.SyncedAt),
		})
	}
	return out, nil
}

// LibraryItems 列某台服务器的库内条目。
func (s *Service) LibraryItems(ctx context.Context, serverID int64, limit, offset int) ([]LibraryItemView, int, error) {
	items, total, err := s.library.ListByServer(ctx, serverID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	names := s.serverNames(ctx)
	out := make([]LibraryItemView, 0, len(items))
	for _, it := range items {
		out = append(out, LibraryItemView{
			ServerID: it.ServerID, ServerName: names[it.ServerID],
			ItemID: it.ItemID, Code: it.Code, Title: it.Title, Path: it.Path,
			Resolution: it.Resolution, HasQuality: it.HasQuality,
			SizeBytes: it.SizeBytes, SyncedAt: formatTS(it.SyncedAt),
		})
	}
	return out, total, nil
}

func (s *Service) serverNames(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	rows, err := s.servers.List(ctx)
	if err != nil {
		return out
	}
	for _, srv := range rows {
		out[srv.ID] = srv.Name
	}
	return out
}

// StartLibrarySync 在后台起一轮全量同步，立刻返回是否**启动了**。
//
// 同步一个几千部的库要几十秒到几分钟，同步阻塞地等会让 HTTP 请求超时。
// 返回 false 表示已经有一轮在跑 —— 调用方据此给出「同步正在进行中」，
// 而不是把这次请求并进另一轮（那样用户拿到的是别人的结果）。
func (s *Service) StartLibrarySync(ctx context.Context, trigger string) bool {
	s.syncMu.Lock()
	if s.syncState.running {
		s.syncMu.Unlock()
		return false
	}
	s.syncMu.Unlock()

	// 用独立的 context：请求的 ctx 在响应返回后就取消了，
	// 拿它跑后台同步会让同步在第一台服务器上就被打断。
	go s.runLibrarySync(context.WithoutCancel(ctx), trigger)
	return true
}

// UpdateLibrarySchedule 保存定时刷新计划。
//
// cron 在这里做**写入时严格**的校验：写坏了当场拒绝，而不是等调度循环
// 默默什么都不做、几天后才发现从来没同步过。
func (s *Service) UpdateLibrarySchedule(ctx context.Context, enabled *bool, cron string) error {
	if s.settings == nil {
		return errNotReady()
	}
	patch := map[string]string{}
	if enabled != nil {
		patch[settings.KeyJavLibrarySyncEnabled] = boolString(*enabled)
	}
	if cron = strings.TrimSpace(cron); cron != "" {
		if _, ok := parseCron(cron); !ok {
			return domain.Errorf(domain.CodeValidation,
				"定时刷新计划的 cron 表达式无效：%s（格式：分 时 日 月 星期）", cron)
		}
		patch[settings.KeyJavLibraryCron] = cron
	}
	if len(patch) == 0 {
		return nil
	}
	return s.settings.Update(ctx, patch)
}
