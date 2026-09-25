package jav

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/javdb"
	"litepan/internal/settings"
)

// Node 是一个 JAVDB API 镜像节点。
type Node struct {
	Name string `json:"name"`
	Base string `json:"base"`
}

// ConfigView 是「番号相关设置」要读的全部配置。
type ConfigView struct {
	// Enabled 控制「番号」分组要不要出现在订阅页的 Tab 栏里。
	Enabled bool `json:"enabled"`

	// —— 认证 ——
	//
	// 密码与 token **从不回传**，只报「配没配过」。回传了就会有一个必然发生的
	// 事故：前端把掩码当成用户改动回写，一保存就把字面量 ****** 写进库 ——
	// 与 settings/registry.go 的 secretSpec 那段警告是同一件事。
	Username     string `json:"username"`
	HasPassword  bool   `json:"has_password"`
	HasToken     bool   `json:"has_token"`
	LoginStatus  string `json:"login_status"`
	LoginMessage string `json:"login_message"`
	LastLoginAt  string `json:"last_login_at"`

	// —— 网络 ——
	APIBase    string `json:"api_base"`
	APINodes   []Node `json:"api_nodes"`
	JavbusBase string `json:"javbus_base"`
	UseProxy   bool   `json:"use_proxy"`
	// ProxyReady 是「用户自己配了代理并且开着」，仅供界面回显开关状态。
	ProxyReady bool `json:"proxy_ready"`
	// EffectiveProxy 是**实际会走的那条路**，用来消除一个必然会出现的困惑：
	// 用户没在 LitePan 里配代理，抓取却通了 —— 因为没配时会跟随系统环境变量
	// （HTTP_PROXY / HTTPS_PROXY）。不显示出来的话，他既不知道现在经过了什么，
	// 也不知道关掉环境变量会怎样。
	EffectiveProxy string `json:"effective_proxy"`
	MinIntervalMS  int    `json:"min_interval_ms"`
	RequestGapMS   int    `json:"request_gap_ms"`
	TimeoutSec     int    `json:"timeout_sec"`
	Retry          int    `json:"retry"`

	// —— 订阅调度（两组，互不相干）——
	SubCheckEnabled     bool     `json:"sub_check_enabled"`
	SubDailyTimes       []string `json:"sub_daily_times"`
	SubCheckIntervalMin int      `json:"sub_check_interval_min"`
	SubSyncEnabled      bool     `json:"sub_sync_enabled"`
	SubSyncTimes        []string `json:"sub_sync_times"`
	SubPushBatch        int      `json:"sub_push_batch"`
	SubConcurrency      int      `json:"sub_concurrency"`
	SubRetryEnabled     bool     `json:"sub_retry_enabled"`
	SubIntervalMinSec   int      `json:"sub_interval_min_sec"`
	SubIntervalMaxSec   int      `json:"sub_interval_max_sec"`
	SubTimeoutSec       int      `json:"sub_timeout_sec"`

	// SidecarEnabled 控制推送成功后要不要在资源所在目录写 `<番号>.json`
	// （元数据侧车，见 sidecar.go）。默认开。
	SidecarEnabled bool `json:"sidecar_enabled"`

	// —— 推送默认目标 ——
	DefaultAccountID    int64  `json:"default_account_id"`
	DefaultParentID     string `json:"default_parent_id"`
	DefaultDisplayPath  string `json:"default_display_path"`
	DefaultPushProvider string `json:"default_push_provider"`

	// —— 媒体库同步 ——
	LibrarySyncEnabled bool   `json:"library_sync_enabled"`
	LibraryCron        string `json:"library_cron"`
	LibraryNextRunAt   string `json:"library_next_run_at"`
	LibraryLastRunAt   string `json:"library_last_run_at"`
	LibrarySyncStatus  string `json:"library_sync_status"`
	LibrarySyncMessage string `json:"library_sync_message"`
}

// ConfigInput 是写配置的入参。
//
// 密码/token 用「空串 = 不修改」：它们从不回传，前端也就无从「原样回写」，
// 只能靠留空表达「保持原样」。
type ConfigInput struct {
	Enabled bool `json:"enabled"`

	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`

	APIBase       string `json:"api_base"`
	JavbusBase    string `json:"javbus_base"`
	UseProxy      bool   `json:"use_proxy"`
	MinIntervalMS *int   `json:"min_interval_ms"`
	RequestGapMS  *int   `json:"request_gap_ms"`
	TimeoutSec    *int   `json:"timeout_sec"`
	Retry         *int   `json:"retry"`

	SubCheckEnabled     *bool    `json:"sub_check_enabled"`
	SubDailyTimes       []string `json:"sub_daily_times"`
	SubCheckIntervalMin *int     `json:"sub_check_interval_min"`
	SubSyncEnabled      *bool    `json:"sub_sync_enabled"`
	SubSyncTimes        []string `json:"sub_sync_times"`
	SubPushBatch        *int     `json:"sub_push_batch"`
	SubConcurrency      *int     `json:"sub_concurrency"`
	SubRetryEnabled     *bool    `json:"sub_retry_enabled"`
	SubIntervalMinSec   *int     `json:"sub_interval_min_sec"`
	SubIntervalMaxSec   *int     `json:"sub_interval_max_sec"`
	SubTimeoutSec       *int     `json:"sub_timeout_sec"`

	SidecarEnabled *bool `json:"sidecar_enabled"`

	DefaultAccountID    *int64 `json:"default_account_id"`
	DefaultParentID     string `json:"default_parent_id"`
	DefaultDisplayPath  string `json:"default_display_path"`
	DefaultPushProvider string `json:"default_push_provider"`

	LibrarySyncEnabled *bool  `json:"library_sync_enabled"`
	LibraryCron        string `json:"library_cron"`
}

// ConnectionResult 是一次探活的结论。
type ConnectionResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
	// Detail 放「连上了谁」这类补充信息（媒体服务器的 ServerName、
	// JAVDB 的 token 有效期之类），没内容时为空。
	Detail string `json:"detail,omitempty"`
}

// TestConnectionResult 是 JAVDB + JAVBUS 的探活结论。
type TestConnectionResult struct {
	Javdb  ConnectionResult `json:"javdb"`
	Javbus ConnectionResult `json:"javbus"`
	// LoggedIn 表示这次探活是否顺带完成了登录（配了账号密码但没 token 时）。
	LoggedIn bool `json:"logged_in"`
}

// ————————————————————— 读 —————————————————————

// ConfigView 汇总配置。
func (s *Service) Config(ctx context.Context) (ConfigView, error) {
	if s.settings == nil {
		return ConfigView{}, errNotReady()
	}

	view := ConfigView{
		Enabled:     s.Enabled(),
		Username:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavUsername)),
		HasPassword: strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavPassword)) != "",
		HasToken:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavToken)) != "",
		LoginStatus: strings.TrimSpace(s.settings.String(settings.KeyJavLoginStatus)),
		LoginMessage: strings.TrimSpace(
			s.settings.StringAllowEmpty(settings.KeyJavLoginMessage)),
		LastLoginAt: s.settings.StringAllowEmpty(settings.KeyJavLastLoginAt),

		APIBase:        strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavAPIBase)),
		APINodes:       s.apiNodes(),
		JavbusBase:     strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavJavbusBase)),
		UseProxy:       s.settings.Bool(settings.KeyJavUseProxy),
		ProxyReady:     s.useProxy(),
		EffectiveProxy: s.effectiveProxyLabel(),
		MinIntervalMS:  s.settings.Int(settings.KeyJavMinIntervalMS),
		RequestGapMS:   s.settings.Int(settings.KeyJavRequestGapMS),
		TimeoutSec:     s.settings.Int(settings.KeyJavTimeoutSec),
		Retry:          s.settings.Int(settings.KeyJavRetry),

		SubCheckEnabled:     s.settings.Bool(settings.KeyJavSubCheckEnabled),
		SubDailyTimes:       s.stringList(settings.KeyJavSubDailyTimes),
		SubCheckIntervalMin: s.settings.Int(settings.KeyJavSubCheckIntervalMin),
		SubSyncEnabled:      s.settings.Bool(settings.KeyJavSubSyncEnabled),
		SubSyncTimes:        s.stringList(settings.KeyJavSubSyncTimes),
		SubPushBatch:        s.settings.Int(settings.KeyJavSubPushBatch),
		SubConcurrency:      s.settings.Int(settings.KeyJavSubConcurrency),
		SubRetryEnabled:     s.settings.Bool(settings.KeyJavSubRetryEnabled),
		SubIntervalMinSec:   s.settings.Int(settings.KeyJavSubIntervalMinSec),
		SubIntervalMaxSec:   s.settings.Int(settings.KeyJavSubIntervalMaxSec),
		SubTimeoutSec:       s.settings.Int(settings.KeyJavSubTimeoutSec),

		SidecarEnabled: s.sidecarEnabled(),

		DefaultParentID:     strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavDefaultParentID)),
		DefaultDisplayPath:  strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavDefaultPath)),
		DefaultPushProvider: strings.TrimSpace(s.settings.String(settings.KeyJavDefaultPushProvider)),

		LibrarySyncEnabled: s.settings.Bool(settings.KeyJavLibrarySyncEnabled),
		LibraryCron:        strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavLibraryCron)),
		LibraryLastRunAt:   s.settings.StringAllowEmpty(settings.KeyJavLibraryLastRunAt),
		LibrarySyncStatus:  strings.TrimSpace(s.settings.String(settings.KeyJavLibrarySyncStatus)),
		LibrarySyncMessage: strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavLibrarySyncMessage)),
	}

	if raw := strings.TrimSpace(s.settings.String(settings.KeyJavDefaultAccountID)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			view.DefaultAccountID = v
		}
	}

	view.LibraryNextRunAt = nextRunAt(view.LibraryCron)
	_ = ctx
	return view, nil
}

// apiNodes 解析设置的节点列表。解析不出来时回落到内置的三个官方镜像。
func (s *Service) apiNodes() []Node {
	raw := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavAPINodes))
	if raw != "" {
		var nodes []Node
		if err := json.Unmarshal([]byte(raw), &nodes); err == nil && len(nodes) > 0 {
			return nodes
		}
	}
	// 设置被清空或写坏时回落内置值：节点列表是「域名被墙时唯一的自救入口」，
	// 让它变成一个空下拉框等于把唯一的出路堵上。
	return defaultAPINodes()
}

// defaultAPINodes 是三个官方镜像，与源码 config.py 的 API_NODES 一致。
func defaultAPINodes() []Node {
	return []Node{
		{Name: "jdforrepam.com", Base: "https://jdforrepam.com/api"},
		{Name: "apidd.spthgb.com", Base: "https://apidd.spthgb.com/api"},
		{Name: "apidd.czssdgz.com", Base: "https://apidd.czssdgz.com/api"},
	}
}

// stringList 读一个 JSON 字符串数组设置。
func (s *Service) stringList(key string) []string {
	raw := strings.TrimSpace(s.settings.StringAllowEmpty(key))
	if raw == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// nextRunAt 用 cronspec 算出下次触发时间，算不出来返回空串。
//
// 返回空串而非报错：表达式写坏不该让整个配置页打不开，
// 前端把它显示成「该表达式在可预见的时间内不会触发」就够了。
func nextRunAt(expr string) string {
	spec, ok := parseCron(expr)
	if !ok {
		return ""
	}
	next, ok := spec.Next(time.Now())
	if !ok {
		return ""
	}
	return next.Format(time.RFC3339)
}

// ————————————————————— 写 —————————————————————

// UpdateConfig 写配置。
func (s *Service) UpdateConfig(ctx context.Context, in ConfigInput) error {
	if s.settings == nil {
		return errNotReady()
	}

	// 推送通道是 select 类型，空串不在可选值里，写进去会被设置层拒掉。
	// 空 = 「没指定」，回落成 auto 才是它该有的意思。
	pushProvider := strings.TrimSpace(in.DefaultPushProvider)
	if pushProvider == "" {
		pushProvider = "auto"
	}

	patch := map[string]string{
		settings.KeyJavEnabled:             boolString(in.Enabled),
		settings.KeyJavAPIBase:             strings.TrimSpace(in.APIBase),
		settings.KeyJavJavbusBase:          strings.TrimSpace(in.JavbusBase),
		settings.KeyJavUseProxy:            boolString(in.UseProxy),
		settings.KeyJavDefaultParentID:     strings.TrimSpace(in.DefaultParentID),
		settings.KeyJavDefaultPath:         strings.TrimSpace(in.DefaultDisplayPath),
		settings.KeyJavDefaultPushProvider: pushProvider,
		settings.KeyJavUsername:            strings.TrimSpace(in.Username),
	}

	// 凭据留空 = 不修改。空串写进去会静默清掉用户的 token，
	// 而界面上那个输入框本来就是空的（从不回传），用户根本没有「清空」的意图。
	if v := strings.TrimSpace(in.Password); v != "" {
		patch[settings.KeyJavPassword] = v
	}
	if v := strings.TrimSpace(in.Token); v != "" {
		patch[settings.KeyJavToken] = v
	}

	if in.MinIntervalMS != nil {
		patch[settings.KeyJavMinIntervalMS] = strconv.Itoa(*in.MinIntervalMS)
	}
	if in.RequestGapMS != nil {
		patch[settings.KeyJavRequestGapMS] = strconv.Itoa(*in.RequestGapMS)
	}
	if in.TimeoutSec != nil {
		patch[settings.KeyJavTimeoutSec] = strconv.Itoa(*in.TimeoutSec)
	}
	if in.Retry != nil {
		patch[settings.KeyJavRetry] = strconv.Itoa(*in.Retry)
	}

	if in.SubCheckEnabled != nil {
		patch[settings.KeyJavSubCheckEnabled] = boolString(*in.SubCheckEnabled)
	}
	if in.SubDailyTimes != nil {
		patch[settings.KeyJavSubDailyTimes] = jsonStringList(in.SubDailyTimes)
	}
	if in.SubCheckIntervalMin != nil {
		patch[settings.KeyJavSubCheckIntervalMin] = strconv.Itoa(*in.SubCheckIntervalMin)
	}
	if in.SubSyncEnabled != nil {
		patch[settings.KeyJavSubSyncEnabled] = boolString(*in.SubSyncEnabled)
	}
	if in.SubSyncTimes != nil {
		patch[settings.KeyJavSubSyncTimes] = jsonStringList(in.SubSyncTimes)
	}
	if in.SubPushBatch != nil {
		patch[settings.KeyJavSubPushBatch] = strconv.Itoa(*in.SubPushBatch)
	}
	if in.SubConcurrency != nil {
		patch[settings.KeyJavSubConcurrency] = strconv.Itoa(*in.SubConcurrency)
	}
	if in.SubRetryEnabled != nil {
		patch[settings.KeyJavSubRetryEnabled] = boolString(*in.SubRetryEnabled)
	}
	if in.SubIntervalMinSec != nil {
		patch[settings.KeyJavSubIntervalMinSec] = strconv.Itoa(*in.SubIntervalMinSec)
	}
	if in.SubIntervalMaxSec != nil {
		patch[settings.KeyJavSubIntervalMaxSec] = strconv.Itoa(*in.SubIntervalMaxSec)
	}
	if in.SubTimeoutSec != nil {
		patch[settings.KeyJavSubTimeoutSec] = strconv.Itoa(*in.SubTimeoutSec)
	}
	if in.SidecarEnabled != nil {
		patch[settings.KeyJavSidecarEnabled] = boolString(*in.SidecarEnabled)
	}

	if in.DefaultAccountID != nil {
		patch[settings.KeyJavDefaultAccountID] = strconv.FormatInt(*in.DefaultAccountID, 10)
	}

	if in.LibrarySyncEnabled != nil {
		patch[settings.KeyJavLibrarySyncEnabled] = boolString(*in.LibrarySyncEnabled)
	}
	if cron := strings.TrimSpace(in.LibraryCron); cron != "" {
		// 写入时严格校验：表达式写坏了就在保存这一刻告诉他，
		// 而不是等调度循环默默什么都不做、几天后才发现没同步过。
		if _, ok := parseCron(cron); !ok {
			return domain.Errorf(domain.CodeValidation,
				"定时刷新计划的 cron 表达式无效：%s（格式：分 时 日 月 星期）", cron)
		}
		patch[settings.KeyJavLibraryCron] = cron
	}

	if err := s.settings.Update(ctx, patch); err != nil {
		return err
	}
	s.clientMu.Lock()
	s.dbClient, s.dbKey = nil, ""
	s.busClient, s.busKey = nil, ""
	s.clientMu.Unlock()
	return nil
}

// ————————————————————— 登录与探活 —————————————————————

// Login 用账号密码换 token 并落库。
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	if s.settings == nil {
		return "", errNotReady()
	}
	if strings.TrimSpace(username) == "" {
		username = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavUsername))
	}
	if strings.TrimSpace(password) == "" {
		password = s.settings.StringAllowEmpty(settings.KeyJavPassword)
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return "", domain.Errorf(domain.CodeValidation, "请先填写 JAVDB 账号与密码")
	}

	client, err := s.javdbClient()
	if err != nil {
		return "", err
	}
	token, err := client.Login(ctx, username, password)
	if err != nil {
		// 登录失败原样带出上游的 message：那里写着「账号或密码错误」这类
		// 用户能直接行动的原因，包一层反而丢信息。
		s.markLogin(ctx, "error", err.Error())
		return "", upstreamErr(err)
	}

	// 登录成功顺带把账号密码一起存了 —— 用户在这一页填了它们，
	// 就是想让它长期生效，不该要求他再点一次保存。
	patch := map[string]string{
		settings.KeyJavToken:        token,
		settings.KeyJavUsername:     strings.TrimSpace(username),
		settings.KeyJavPassword:     password,
		settings.KeyJavLoginStatus:  "ok",
		settings.KeyJavLoginMessage: "",
		settings.KeyJavLastLoginAt:  time.Now().Format(time.RFC3339),
	}
	if err := s.settings.Update(ctx, patch); err != nil {
		return "", err
	}
	s.clientMu.Lock()
	s.dbClient, s.dbKey = nil, ""
	s.clientMu.Unlock()
	return token, nil
}

func (s *Service) markLogin(ctx context.Context, status, message string) {
	if s.settings == nil {
		return
	}
	_ = s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyJavLoginStatus:  status,
		settings.KeyJavLoginMessage: message,
	})
}

// TestConnection 同时探活 JAVDB 与 JAVBUS。
//
// 两边分开报结论：一个通一个不通是很常见的状态（比如 JAVDB 的节点被墙、
// JAVBUS 还活着），合并成一句「连接失败」会让人不知道该修哪一边。
func (s *Service) TestConnection(ctx context.Context) TestConnectionResult {
	var out TestConnectionResult
	s.probeJavdb(ctx, &out)
	s.probeJavbus(ctx, &out)
	return out
}

func (s *Service) probeJavdb(ctx context.Context, out *TestConnectionResult) {
	client, err := s.javdbClient()
	if err != nil {
		out.Javdb = ConnectionResult{Message: err.Error()}
		return
	}

	// 配了账号密码但还没 token 时顺手登录一次：这正是用户点「测试连接」
	// 最想确认的事，让他再去找「登录」按钮是多余的。
	if s.settings.StringAllowEmpty(settings.KeyJavToken) == "" &&
		strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavUsername)) != "" {
		start := time.Now()
		token, err := s.Login(ctx, "", "")
		latency := time.Since(start).Milliseconds()
		if err != nil {
			out.Javdb = ConnectionResult{Message: "登录失败：" + err.Error(), LatencyMS: latency}
			return
		}
		out.LoggedIn = true
		out.Javdb = ConnectionResult{OK: true, Message: "登录成功", LatencyMS: latency,
			Detail: "token " + maskSecret(token)}
		return
	}

	start := time.Now()
	// 探活用日榜：不需要 token、一次请求就回，正好验签名与节点。
	_, err = client.Hot(ctx, "daily", "0")
	latency := time.Since(start).Milliseconds()
	if err != nil {
		out.Javdb = ConnectionResult{Message: err.Error(), LatencyMS: latency}
		return
	}
	out.Javdb = ConnectionResult{OK: true, Message: "连通成功", LatencyMS: latency}
}

func (s *Service) probeJavbus(ctx context.Context, out *TestConnectionResult) {
	client, err := s.javbusClient()
	if err != nil {
		out.Javbus = ConnectionResult{Message: err.Error()}
		return
	}
	// 拿一个一定有页面的老番号探活。用真实番号而不是首页：
	// 首页能打开但详情页结构变了的情况是存在的，那时只有详情页能暴露问题。
	start := time.Now()
	msg, err := client.Test(ctx, "SSIS-001")
	latency := time.Since(start).Milliseconds()
	if err != nil {
		out.Javbus = ConnectionResult{Message: err.Error(), LatencyMS: latency}
		return
	}
	out.Javbus = ConnectionResult{OK: true, Message: msg, LatencyMS: latency}
}

// TestNodes 依次探活每个 API 节点，返回延迟。
func (s *Service) TestNodes(ctx context.Context) []ConnectionResult {
	nodes := s.apiNodes()
	results := make([]ConnectionResult, 0, len(nodes))
	for _, node := range nodes {
		start := time.Now()
		client, err := javdb.New(javdb.Options{
			APIBase:  node.Base,
			Token:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavToken)),
			Timeout:  s.timeout(),
			Retries:  1, // 测速场景只打一次：重试会把真实延迟盖掉
			ProxyURL: s.proxyURL(),
		})
		if err != nil {
			results = append(results, ConnectionResult{Message: err.Error()})
			continue
		}
		// 探活用日榜（同上）。
		_, err = client.Hot(ctx, "daily", "0")
		latency := time.Since(start).Milliseconds()
		if err != nil {
			results = append(results, ConnectionResult{Message: err.Error(), LatencyMS: latency})
			continue
		}
		results = append(results, ConnectionResult{OK: true, Message: "连通", LatencyMS: latency})
	}
	return results
}

// ————————————————————— 工具 —————————————————————

// maskSecret 把凭据打个码，只留头尾。日志与探活详情里用它，
// 免得 token 出现在界面上或日志里。
func maskSecret(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= 8 {
		return "******"
	}
	return v[:4] + "******" + v[len(v)-4:]
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// jsonStringList 把时间/类别列表序列化进设置。
//
// 排序并去重：同一组时间点每次保存得到同一个字符串，
// 前端靠比较前后串判断「有没有改动」，顺序不稳定会一直误报未保存。
func jsonStringList(values []string) string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		cleaned = append(cleaned, v)
	}
	sortStrings(cleaned)
	b, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// IsNotFound 报告错误是不是「没找到」。API 层据此决定 404 还是 500。
func IsNotFound(err error) bool {
	var ae *domain.AppError
	if errors.As(err, &ae) {
		return ae.Code == domain.CodeNotFound
	}
	return false
}
