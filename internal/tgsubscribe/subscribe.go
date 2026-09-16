package tgsubscribe

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
)

// ————————————————————— 视图 —————————————————————

// ChannelView 是频道的对外形态。
type ChannelView struct {
	ID            int64  `json:"id"`
	ChatID        string `json:"chat_id"`
	Username      string `json:"username"`
	Title         string `json:"title"`
	Remark        string `json:"remark"`
	Level         int    `json:"level"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	LastError     string `json:"last_error"`
	LastMessageID int64  `json:"last_message_id"`
	LastPostAt    string `json:"last_post_at,omitempty"`
	MatchedCount  int64  `json:"matched_count"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// SubscriptionView 是订阅的对外形态。
type SubscriptionView struct {
	ID                int64        `json:"id"`
	TMDBID            string       `json:"tmdb_id"`
	MediaType         string       `json:"media_type"`
	Title             string       `json:"title"`
	OriginalTitle     string       `json:"original_title"`
	Year              int          `json:"year"`
	PosterPath        string       `json:"poster_path"`
	Overview          string       `json:"overview"`
	Aliases           []string     `json:"aliases"`
	AliasCount        int          `json:"alias_count"`
	Status            string       `json:"status"`
	TargetAccountID   int64        `json:"target_account_id"`
	TargetParentID    string       `json:"target_parent_id"`
	TargetDisplayPath string       `json:"target_display_path"`
	QualityProfileID  int64        `json:"quality_profile_id"`
	PushProvider      string       `json:"push_provider"`
	CollectWindowMin  int          `json:"collect_window_min"`
	UpgradeEnabled    bool         `json:"upgrade_enabled"`
	BestQualityScore  float64      `json:"best_quality_score"`
	MatchedCount      int64        `json:"matched_count"`
	PushedCount       int64        `json:"pushed_count"`
	LastMatchAt       string       `json:"last_match_at,omitempty"`
	LastPushAt        string       `json:"last_push_at,omitempty"`
	LastError         string       `json:"last_error"`
	CollectedEpisodes int          `json:"collected_episodes"`
	AiredEpisodes     int          `json:"aired_episodes"`
	TotalEpisodes     int          `json:"total_episodes"`
	Seasons           []SeasonInfo `json:"seasons,omitempty"`
	CreatedAt         string       `json:"created_at,omitempty"`
}

// EpisodeView 是已收集剧集的对外形态。
type EpisodeView struct {
	Season        int    `json:"season"`
	Episode       int    `json:"episode"`
	RecordID      int64  `json:"record_id"`
	OfflineTaskID string `json:"offline_task_id"`
	AccountID     int64  `json:"account_id"`
	FileID        string `json:"file_id"`
	TargetPath    string `json:"target_path"`
	DeliveredAt   string `json:"delivered_at,omitempty"`
}

// RecordView 是匹配记录的对外形态。
type RecordView struct {
	ID             int64   `json:"id"`
	ChannelID      int64   `json:"channel_id"`
	ChatTitle      string  `json:"chat_title"`
	MessageID      int64   `json:"message_id"`
	MessageDate    string  `json:"message_date,omitempty"`
	RawName        string  `json:"raw_name"`
	NameSource     string  `json:"name_source"`
	Magnet         string  `json:"magnet"`
	MagnetHash     string  `json:"magnet_hash"`
	SizeBytes      int64   `json:"size_bytes"`
	ParsedTitle    string  `json:"parsed_title"`
	ParsedYear     int     `json:"parsed_year"`
	Season         int     `json:"season"`
	Episode        int     `json:"episode"`
	EpisodeEnd     int     `json:"episode_end"`
	IsBatch        bool    `json:"is_batch"`
	Resolution     string  `json:"resolution"`
	VideoCodec     string  `json:"video_codec"`
	SourceTag      string  `json:"source_tag"`
	SubscriptionID int64   `json:"subscription_id"`
	SubTitle       string  `json:"subscription_title,omitempty"`
	MatchScore     float64 `json:"match_score"`
	QualityScore   float64 `json:"quality_score"`
	Status         string  `json:"status"`
	StatusLabel    string  `json:"status_label"`
	Reason         string  `json:"reason"`
	OfflineTaskID  string  `json:"offline_task_id"`
	AccountID      int64   `json:"account_id"`
	ProviderKind   string  `json:"provider_kind"`
	RetryCount     int     `json:"retry_count"`
	NextRetryAt    string  `json:"next_retry_at,omitempty"`
	CreatedAt      string  `json:"created_at,omitempty"`
}

// QualityProfileView 是画质方案的对外形态。
type QualityProfileView struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	IsDefault bool                   `json:"is_default"`
	Config    domain.TGQualityConfig `json:"config"`
}

// ConfigView 是配置弹窗要的全部配置（Token 打码）。
type ConfigView struct {
	Enabled          bool   `json:"enabled"`
	TokenSet         bool   `json:"token_set"`
	APPHost          string `json:"api_host"`
	ProxyEnabled     bool   `json:"proxy_enabled"`
	ProxyURL         string `json:"proxy_url"`
	ProxyUsername    string `json:"proxy_username"`
	ProxyPasswordSet bool   `json:"proxy_password_set"`
	AutoPush         bool   `json:"auto_push"`
	DefaultAccountID int64  `json:"default_account_id"`
	DefaultParentID  string `json:"default_parent_id"`
	DefaultPath      string `json:"default_display_path"`
	QualityProfileID int64  `json:"default_quality_profile_id"`
	CollectWindowMin int    `json:"collect_window_min"`
	MaxPushPerHour   int    `json:"max_push_per_hour"`
	Status           string `json:"status"`
	StatusMessage    string `json:"status_message"`
	BotName          string `json:"bot_name"`
}

// ConfigInput 是写配置的入参。Token / 密码留空表示「不改」。
type ConfigInput struct {
	Enabled          bool   `json:"enabled"`
	Token            string `json:"token"`
	APPHost          string `json:"api_host"`
	ProxyEnabled     bool   `json:"proxy_enabled"`
	ProxyURL         string `json:"proxy_url"`
	ProxyUsername    string `json:"proxy_username"`
	ProxyPassword    string `json:"proxy_password"`
	AutoPush         bool   `json:"auto_push"`
	DefaultAccountID int64  `json:"default_account_id"`
	DefaultParentID  string `json:"default_parent_id"`
	DefaultPath      string `json:"default_display_path"`
	QualityProfileID int64  `json:"default_quality_profile_id"`
	CollectWindowMin int    `json:"collect_window_min"`
	MaxPushPerHour   int    `json:"max_push_per_hour"`
}

// ————————————————————— 配置 —————————————————————

func (s *Service) ConfigView(ctx context.Context) ConfigView {
	view := ConfigView{
		Enabled:          s.settings.Bool(settings.KeyTGBotEnabled),
		TokenSet:         strings.TrimSpace(s.settings.String(settings.KeyTGBotToken)) != "",
		APPHost:          strings.TrimSpace(s.settings.String(settings.KeyTGBotAPIHost)),
		ProxyEnabled:     s.settings.Bool(settings.KeyTGBotProxyEnabled),
		ProxyURL:         strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotProxyURL)),
		ProxyUsername:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotProxyUsername)),
		ProxyPasswordSet: strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotProxyPassword)) != "",
		AutoPush:         s.settings.Bool(settings.KeyTGBotAutoPush),
		DefaultParentID:  strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotDefaultParentID)),
		DefaultPath:      strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotDefaultPath)),
		CollectWindowMin: s.settings.Int(settings.KeyTGBotCollectWindowMin),
		MaxPushPerHour:   s.maxPushPerHour(),
		Status:           strings.TrimSpace(s.settings.String(settings.KeyTGBotStatus)),
		StatusMessage:    strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotStatusMessage)),
		BotName:          strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyTGBotStatusMessage)),
	}
	if raw := strings.TrimSpace(s.settings.String(settings.KeyTGBotDefaultAccountID)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			view.DefaultAccountID = v
		}
	}
	view.QualityProfileID = s.defaultQualityProfileID()
	_ = ctx
	return view
}

// UpdateConfig 写配置。Token / 代理密码留空表示保持原值 ——
// 前端只会拿到「是否已设置」，不该被迫回传明文。
func (s *Service) UpdateConfig(ctx context.Context, in ConfigInput) error {
	patch := map[string]string{
		settings.KeyTGBotEnabled:          boolString(in.Enabled),
		settings.KeyTGBotAPIHost:          strings.TrimSpace(in.APPHost),
		settings.KeyTGBotProxyEnabled:     boolString(in.ProxyEnabled),
		settings.KeyTGBotProxyURL:         strings.TrimSpace(in.ProxyURL),
		settings.KeyTGBotProxyUsername:    strings.TrimSpace(in.ProxyUsername),
		settings.KeyTGBotAutoPush:         boolString(in.AutoPush),
		settings.KeyTGBotDefaultParentID:  strings.TrimSpace(in.DefaultParentID),
		settings.KeyTGBotDefaultPath:      strings.TrimSpace(in.DefaultPath),
		settings.KeyTGBotDefaultAccountID: strconv.FormatInt(in.DefaultAccountID, 10),
		settings.KeyTGBotProfileID:        strconv.FormatInt(in.QualityProfileID, 10),
		settings.KeyTGBotCollectWindowMin: strconv.Itoa(maxInt(in.CollectWindowMin, 0)),
		settings.KeyTGBotMaxPushPerHour:   strconv.Itoa(maxInt(in.MaxPushPerHour, 1)),
	}
	if token := strings.TrimSpace(in.Token); token != "" {
		patch[settings.KeyTGBotToken] = token
	}
	if pwd := strings.TrimSpace(in.ProxyPassword); pwd != "" {
		patch[settings.KeyTGBotProxyPassword] = pwd
	}
	if err := s.settings.Update(ctx, patch); err != nil {
		return err
	}
	// 配置变了必须重建客户端与匹配快照。
	s.mu.Lock()
	s.clientKey = ""
	s.client = nil
	s.mu.Unlock()
	s.InvalidateSnapshot()
	return nil
}

// TestBot 探活：getMe + 网络连通性。
func (s *Service) TestBot(ctx context.Context) (string, error) {
	client := s.clientFor()
	if client == nil || !client.Ready() {
		return "", domain.Errorf(domain.CodeValidation, "请先填写 Bot Token")
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return "", domain.Errorf(domain.CodeValidation, "连接 Telegram 失败：%s", describeError(err))
	}
	name := "@" + strings.TrimSpace(me.Username)
	_ = s.settings.UpdateSilent(ctx, map[string]string{
		settings.KeyTGBotStatus:        domain.TGChannelStatusOK,
		settings.KeyTGBotStatusMessage: "",
		settings.KeyTGBotBotName:       name,
	})
	return name, nil
}

// ————————————————————— 频道 —————————————————————

func (s *Service) ListChannels(ctx context.Context) ([]ChannelView, error) {
	rows, err := s.channels.List(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelView, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelView(row))
	}
	return out, nil
}

func channelView(c *domain.TGChannel) ChannelView {
	if c == nil {
		return ChannelView{}
	}
	return ChannelView{
		ID:            c.ID,
		ChatID:        c.ChatID,
		Username:      c.Username,
		Title:         c.Title,
		Remark:        c.Remark,
		Level:         c.Level,
		Enabled:       c.Enabled,
		Status:        c.Status,
		LastError:     c.LastError,
		LastMessageID: c.LastMessageID,
		LastPostAt:    formatTime(c.LastPostAt),
		MatchedCount:  c.MatchedCount,
		CreatedAt:     formatTime(c.CreatedAt),
	}
}

// ChannelProbe 是一次频道校验的结果。
type ChannelProbe struct {
	ChatID   string `json:"chat_id"`
	Username string `json:"username"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	BotName  string `json:"bot_name"`
	IsAdmin  bool   `json:"is_admin"`
}

// ProbeChannel 校验一个频道是否可用。
//
// 校验链：getChat 可见 → 类型是频道/超级群 → bot 是管理员。
// **必须把 @username 解析出来的数字 chat_id 固化下来**：频道改名后 @name 会失效，
// 只有数字 id 稳定。私有频道也只能用数字 id。
func (s *Service) ProbeChannel(ctx context.Context, input string) (*ChannelProbe, error) {
	chatID, err := NormalizeChannelInput(input)
	if err != nil {
		return nil, err
	}
	client := s.clientFor()
	if client == nil || !client.Ready() {
		return nil, domain.Errorf(domain.CodeValidation, "请先配置并保存 Bot Token")
	}

	chat, err := client.GetChat(ctx, chatID)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "读取频道「%s」失败：%s", chatID, describeError(err))
	}
	if chat.Type != "channel" && chat.Type != "supergroup" {
		return nil, domain.Errorf(domain.CodeValidation, "「%s」不是频道（类型为 %s）", chatID, chat.Type)
	}

	me, err := client.GetMe(ctx)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "读取 Bot 信息失败：%s", describeError(err))
	}

	probe := &ChannelProbe{
		ChatID:   strconv.FormatInt(chat.ID, 10),
		Username: chat.Username,
		Title:    chat.Title,
		Type:     chat.Type,
		BotName:  "@" + me.Username,
	}

	member, err := client.GetChatMember(ctx, probe.ChatID, me.ID)
	if err != nil {
		return probe, domain.Errorf(domain.CodeValidation,
			"Bot 不在该频道内，请先把 %s 拉进频道并设为管理员", probe.BotName)
	}
	if member.Status != "administrator" && member.Status != "creator" {
		return probe, domain.Errorf(domain.CodeValidation,
			"Bot 在频道内的身份是「%s」，必须是管理员才能收到频道消息", describeMemberStatus(member.Status))
	}
	probe.IsAdmin = true
	return probe, nil
}

// NormalizeChannelInput 把用户输入整理成 Bot API 能接受的 chat_id 形式。
//
// 支持三种输入：@channelname、https://t.me/xxx、-100xxxxxxxxxx。
func NormalizeChannelInput(input string) (string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", domain.Errorf(domain.CodeValidation, "请填写频道")
	}
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/", "https://telegram.me/", "telegram.me/"} {
		if strings.HasPrefix(lower, prefix) {
			raw = raw[len(prefix):]
			break
		}
	}
	// 去掉 /s/ 前缀（网页版预览链接）与查询串。
	if strings.HasPrefix(raw, "s/") {
		raw = raw[2:]
	}
	if idx := strings.IndexAny(raw, "?/#"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return "", domain.Errorf(domain.CodeValidation, "频道地址不完整")
	}
	if strings.HasPrefix(raw, "@") {
		return raw, nil
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return raw, nil
	}
	// 剩下的当作公开频道的用户名。
	return "@" + raw, nil
}

func describeMemberStatus(status string) string {
	switch status {
	case "member":
		return "普通成员"
	case "restricted":
		return "受限成员"
	case "left":
		return "已离开"
	case "kicked":
		return "已被移除"
	}
	return status
}

// ChannelInput 是新增/编辑频道的入参。
type ChannelInput struct {
	Chat    string `json:"chat"`
	Remark  string `json:"remark"`
	Level   int    `json:"level"`
	Enabled bool   `json:"enabled"`
}

// CreateChannel 校验并添加频道。
//
// 校验成功后才落库，并且存的是**解析后的数字 chat_id** —— 公开频道的 @name
// 在改名后会失效，存 @name 等于埋一个静默失效的坑。
func (s *Service) CreateChannel(ctx context.Context, in ChannelInput) (*ChannelView, error) {
	probe, err := s.ProbeChannel(ctx, in.Chat)
	if err != nil {
		return nil, err
	}
	channel := &domain.TGChannel{
		ChatID:   probe.ChatID,
		Username: probe.Username,
		Title:    probe.Title,
		Remark:   strings.TrimSpace(in.Remark),
		Level:    normalizeLevel(in.Level),
		Enabled:  in.Enabled,
		Status:   domain.TGChannelStatusOK,
	}
	id, err := s.channels.Create(ctx, channel)
	if err != nil {
		return nil, err
	}
	channel.ID = id
	view := channelView(channel)
	return &view, nil
}

func (s *Service) UpdateChannel(ctx context.Context, id int64, in ChannelInput) (*ChannelView, error) {
	channel, err := s.channels.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if remark := strings.TrimSpace(in.Remark); remark != "" || in.Remark == "" {
		channel.Remark = remark
	}
	channel.Level = normalizeLevel(in.Level)
	channel.Enabled = in.Enabled
	// 改了频道地址就重新校验一次，顺便把新的 chat_id 固化下来。
	if chat := strings.TrimSpace(in.Chat); chat != "" && chat != channel.ChatID && chat != "@"+channel.Username {
		probe, err := s.ProbeChannel(ctx, chat)
		if err != nil {
			return nil, err
		}
		channel.ChatID = probe.ChatID
		channel.Username = probe.Username
		channel.Title = probe.Title
		channel.Status = domain.TGChannelStatusOK
		channel.LastError = ""
	}
	if err := s.channels.Update(ctx, channel); err != nil {
		return nil, err
	}
	view := channelView(channel)
	return &view, nil
}

func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	return s.channels.Delete(ctx, id)
}

// TestChannel 重新校验已保存频道的可用性。
func (s *Service) TestChannel(ctx context.Context, id int64) (*ChannelProbe, error) {
	channel, err := s.channels.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	probe, probeErr := s.ProbeChannel(ctx, channel.ChatID)
	if probeErr != nil {
		_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusError, probeErr.Error())
		return nil, probeErr
	}
	_ = s.channels.MarkStatus(ctx, id, domain.TGChannelStatusOK, "")
	return probe, nil
}

func normalizeLevel(level int) int {
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

// ————————————————————— 订阅 —————————————————————

// SubscriptionInput 是新增订阅的入参。
//
// TMDB 字段由前端从搜索结果带过来，避免后端再打一次 TMDB 列表接口。
type SubscriptionInput struct {
	TMDBID            string `json:"tmdb_id"`
	MediaType         string `json:"media_type"`
	Title             string `json:"title"`
	OriginalTitle     string `json:"original_title"`
	Year              int    `json:"year"`
	PosterPath        string `json:"poster_path"`
	Overview          string `json:"overview"`
	QualityProfileID  int64  `json:"quality_profile_id"`
	TargetAccountID   int64  `json:"target_account_id"`
	TargetParentID    string `json:"target_parent_id"`
	TargetDisplayPath string `json:"target_display_path"`
	PushProvider      string `json:"push_provider"`
	CollectWindowMin  int    `json:"collect_window_min"`
	UpgradeEnabled    *bool  `json:"upgrade_enabled"`
}

func (s *Service) ListSubscriptions(ctx context.Context, status string) ([]SubscriptionView, error) {
	rows, err := s.subs.List(ctx, status)
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.subscriptionView(ctx, row, true))
	}
	return out, nil
}

func (s *Service) GetSubscription(ctx context.Context, id int64) (*SubscriptionView, error) {
	row, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := s.subscriptionView(ctx, row, true)
	return &view, nil
}

// GetSubscriptionByTMDB 供海报墙标注「已订阅」。
func (s *Service) GetSubscriptionByTMDB(ctx context.Context, tmdbID, mediaType string) (*SubscriptionView, error) {
	row, err := s.subs.GetByTMDB(ctx, strings.TrimSpace(tmdbID), strings.TrimSpace(mediaType))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	view := s.subscriptionView(ctx, row, false)
	return &view, nil
}

func (s *Service) subscriptionView(ctx context.Context, sub *domain.TGSubscription, withEpisodes bool) SubscriptionView {
	view := SubscriptionView{
		ID:                sub.ID,
		TMDBID:            sub.TMDBID,
		MediaType:         sub.MediaType,
		Title:             sub.Title,
		OriginalTitle:     sub.OriginalTitle,
		Year:              sub.Year,
		PosterPath:        sub.PosterPath,
		Overview:          sub.Overview,
		Aliases:           sub.Aliases,
		AliasCount:        len(sub.Aliases),
		Status:            sub.Status,
		TargetAccountID:   sub.TargetAccountID,
		TargetParentID:    sub.TargetParentID,
		TargetDisplayPath: sub.TargetDisplayPath,
		QualityProfileID:  sub.QualityProfileID,
		PushProvider:      sub.PushProvider,
		CollectWindowMin:  sub.CollectWindowMin,
		UpgradeEnabled:    sub.UpgradeEnabled,
		BestQualityScore:  sub.BestQualityScore,
		MatchedCount:      sub.MatchedCount,
		PushedCount:       sub.PushedCount,
		LastMatchAt:       formatTime(sub.LastMatchAt),
		LastPushAt:        formatTime(sub.LastPushAt),
		LastError:         sub.LastError,
		CreatedAt:         formatTime(sub.CreatedAt),
	}
	if seasons, ok := decodeSeasons(sub.Seasons); ok {
		view.Seasons = seasons
		total := 0
		for _, season := range seasons {
			if season.SeasonNumber > 0 {
				total += season.EpisodeCount
			}
		}
		view.TotalEpisodes = total
		if aired, ok := airedEpisodeTotal(sub.Seasons, time.Now()); ok {
			view.AiredEpisodes = aired
		}
	}
	if withEpisodes {
		if rows, err := s.episodes.ListBySubscription(ctx, sub.ID); err == nil {
			view.CollectedEpisodes = len(rows)
		}
	}
	return view
}

// CreateSubscription 建订阅，并把 TMDB 别名与季集固化下来。
func (s *Service) CreateSubscription(ctx context.Context, in SubscriptionInput) (*SubscriptionView, error) {
	tmdbID := strings.TrimSpace(in.TMDBID)
	if tmdbID == "" {
		return nil, domain.Errorf(domain.CodeValidation, "缺少 TMDB ID")
	}
	mediaType := strings.TrimSpace(in.MediaType)
	if mediaType != domain.TGMediaTypeMovie && mediaType != domain.TGMediaTypeTV {
		return nil, domain.Errorf(domain.CodeValidation, "类型必须是 movie 或 tv")
	}
	if existing, err := s.subs.GetByTMDB(ctx, tmdbID, mediaType); err == nil && existing != nil {
		return nil, domain.Errorf(domain.CodeValidation, "《%s》已经在订阅列表里了", existing.Title)
	}

	meta := s.FetchSubscriptionMeta(ctx, tmdbID, mediaType)
	now := time.Now()
	sub := &domain.TGSubscription{
		TMDBID:            tmdbID,
		MediaType:         mediaType,
		Title:             strings.TrimSpace(in.Title),
		OriginalTitle:     strings.TrimSpace(in.OriginalTitle),
		Year:              in.Year,
		PosterPath:        strings.TrimSpace(in.PosterPath),
		Overview:          strings.TrimSpace(in.Overview),
		Aliases:           meta.Aliases,
		AliasesSyncedAt:   now,
		Seasons:           meta.Seasons,
		SeasonsSyncedAt:   now,
		Status:            domain.TGSubStatusActive,
		TargetAccountID:   in.TargetAccountID,
		TargetParentID:    strings.TrimSpace(in.TargetParentID),
		TargetDisplayPath: strings.TrimSpace(in.TargetDisplayPath),
		QualityProfileID:  in.QualityProfileID,
		PushProvider:      normalizePushProvider(in.PushProvider),
		CollectWindowMin:  normalizeWindow(in.CollectWindowMin, s.settings.Int(settings.KeyTGBotCollectWindowMin)),
		UpgradeEnabled:    boolOrDefault(in.UpgradeEnabled, true),
	}
	if sub.Title == "" {
		sub.Title = sub.OriginalTitle
	}
	id, err := s.subs.Create(ctx, sub)
	if err != nil {
		return nil, err
	}
	sub.ID = id
	s.InvalidateSnapshot()
	view := s.subscriptionView(ctx, sub, false)
	return &view, nil
}

// UpdateSubscription 改订阅配置。TMDB 身份字段不可改（改身份等于换一部片，
// 应该删掉重订）。
func (s *Service) UpdateSubscription(ctx context.Context, id int64, in SubscriptionInput) (*SubscriptionView, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sub.QualityProfileID = in.QualityProfileID
	sub.TargetAccountID = in.TargetAccountID
	sub.TargetParentID = strings.TrimSpace(in.TargetParentID)
	sub.TargetDisplayPath = strings.TrimSpace(in.TargetDisplayPath)
	sub.PushProvider = normalizePushProvider(in.PushProvider)
	sub.CollectWindowMin = normalizeWindow(in.CollectWindowMin, sub.CollectWindowMin)
	if in.UpgradeEnabled != nil {
		sub.UpgradeEnabled = *in.UpgradeEnabled
	}
	if err := s.subs.Update(ctx, sub); err != nil {
		return nil, err
	}
	s.InvalidateSnapshot()
	view := s.subscriptionView(ctx, sub, true)
	return &view, nil
}

func (s *Service) DeleteSubscription(ctx context.Context, id int64) error {
	if err := s.subs.Delete(ctx, id); err != nil {
		return err
	}
	s.InvalidateSnapshot()
	return nil
}

// SetSubscriptionStatus 手动改状态：标记完成 / 暂停 / 恢复。
func (s *Service) SetSubscriptionStatus(ctx context.Context, id int64, status string) error {
	status = strings.TrimSpace(status)
	switch status {
	case domain.TGSubStatusActive, domain.TGSubStatusPaused, domain.TGSubStatusCompleted:
	default:
		return domain.Errorf(domain.CodeValidation, "未知的订阅状态：%s", status)
	}
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return err
	}
	sub.Status = status
	if status != domain.TGSubStatusActive {
		// 状态不再是 active 时窗口里的候选没有意义了，清掉避免下一轮又被捞出来。
		sub.PendingDeadlineAt = time.Time{}
	}
	if err := s.subs.Update(ctx, sub); err != nil {
		return err
	}
	s.InvalidateSnapshot()
	return nil
}

// ResetSubscription 清掉已收集进度，重新开始追。
func (s *Service) ResetSubscription(ctx context.Context, id int64) (int64, error) {
	sub, err := s.subs.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	n, err := s.episodes.DeleteBySubscription(ctx, id)
	if err != nil {
		return 0, err
	}
	sub.Status = domain.TGSubStatusActive
	sub.BestQualityScore = 0
	sub.PendingDeadlineAt = time.Time{}
	if err := s.subs.Update(ctx, sub); err != nil {
		return 0, err
	}
	s.InvalidateSnapshot()
	return n, nil
}

// ListEpisodes 返回订阅的已收集剧集。
func (s *Service) ListEpisodes(ctx context.Context, id int64) ([]EpisodeView, error) {
	rows, err := s.episodes.ListBySubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]EpisodeView, 0, len(rows))
	for _, row := range rows {
		out = append(out, EpisodeView{
			Season:        row.Season,
			Episode:       row.Episode,
			RecordID:      row.RecordID,
			OfflineTaskID: row.OfflineTaskID,
			AccountID:     row.AccountID,
			FileID:        row.FileID,
			TargetPath:    row.TargetPath,
			DeliveredAt:   formatTime(row.DeliveredAt),
		})
	}
	return out, nil
}

func normalizePushProvider(raw string) string {
	switch strings.TrimSpace(raw) {
	case domain.TGPushProviderNative, domain.TGPushProviderBuiltin:
		return strings.TrimSpace(raw)
	}
	return domain.TGPushProviderAuto
}

func normalizeWindow(value, fallback int) int {
	if value < 0 {
		return fallback
	}
	if value > 1440 {
		return 1440
	}
	return value
}

// ————————————————————— 画质方案 —————————————————————

func (s *Service) ListQualityProfiles(ctx context.Context) ([]QualityProfileView, error) {
	rows, err := s.quality.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]QualityProfileView, 0, len(rows))
	for _, row := range rows {
		cfg, ok := decodeQualityConfig(row.Config)
		if !ok {
			cfg = DefaultQualityConfig()
		}
		out = append(out, QualityProfileView{
			ID:        row.ID,
			Name:      row.Name,
			IsDefault: row.IsDefault,
			Config:    NormalizeQualityConfig(cfg),
		})
	}
	return out, nil
}

// QualityProfileInput 是新增/编辑画质方案的入参。
type QualityProfileInput struct {
	Name    string                 `json:"name"`
	Config  domain.TGQualityConfig `json:"config"`
	Default bool                   `json:"is_default"`
}

func (s *Service) CreateQualityProfile(ctx context.Context, in QualityProfileInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, domain.Errorf(domain.CodeValidation, "方案名称不能为空")
	}
	cfg := NormalizeQualityConfig(in.Config)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "画质方案格式不正确")
	}
	id, err := s.quality.Create(ctx, &domain.TGQualityProfile{
		Name:      name,
		IsDefault: in.Default,
		Config:    encoded,
	})
	if err != nil {
		return 0, err
	}
	if in.Default {
		if err := s.quality.SetDefault(ctx, id); err != nil {
			s.log.Warn("tg subscribe set default profile failed", "id", id, "err", err)
		}
	}
	return id, nil
}

func (s *Service) UpdateQualityProfile(ctx context.Context, id int64, in QualityProfileInput) error {
	profile, err := s.quality.Get(ctx, id)
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		profile.Name = name
	}
	encoded, err := json.Marshal(NormalizeQualityConfig(in.Config))
	if err != nil {
		return domain.Errorf(domain.CodeValidation, "画质方案格式不正确")
	}
	profile.Config = encoded
	if err := s.quality.Update(ctx, profile); err != nil {
		return err
	}
	if in.Default {
		return s.quality.SetDefault(ctx, id)
	}
	return nil
}

func (s *Service) DeleteQualityProfile(ctx context.Context, id int64) error {
	if id == 1 {
		return domain.Errorf(domain.CodeValidation, "默认方案不可删除")
	}
	return s.quality.Delete(ctx, id)
}

// ————————————————————— 匹配历史 —————————————————————

// RecordFilter 是匹配历史的查询条件。
type RecordFilter struct {
	Status         string
	SubscriptionID int64
	ChannelID      int64
	Keyword        string
	Limit          int
	Offset         int
}

func (s *Service) ListRecords(ctx context.Context, f RecordFilter) ([]RecordView, int, error) {
	rows, total, err := s.records.List(ctx, domain.TGMatchRecordFilter{
		Status:         f.Status,
		SubscriptionID: f.SubscriptionID,
		ChannelID:      f.ChannelID,
		Keyword:        f.Keyword,
		Limit:          f.Limit,
		Offset:         f.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	titles := s.subscriptionTitles(ctx)
	out := make([]RecordView, 0, len(rows))
	for _, row := range rows {
		out = append(out, recordView(row, titles[row.SubscriptionID]))
	}
	return out, total, nil
}

func (s *Service) GetRecord(ctx context.Context, id int64) (*RecordView, error) {
	row, err := s.records.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := recordView(row, "")
	if row.SubscriptionID > 0 {
		if sub, err := s.subs.Get(ctx, row.SubscriptionID); err == nil && sub != nil {
			view.SubTitle = sub.Title
		}
	}
	return &view, nil
}

func (s *Service) subscriptionTitles(ctx context.Context) map[int64]string {
	rows, err := s.subs.List(ctx, "")
	if err != nil {
		return nil
	}
	out := make(map[int64]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Title
	}
	return out
}

func recordView(rec *domain.TGMatchRecord, subTitle string) RecordView {
	return RecordView{
		ID:             rec.ID,
		ChannelID:      rec.ChannelID,
		ChatTitle:      rec.ChatTitle,
		MessageID:      rec.MessageID,
		MessageDate:    formatTime(rec.MessageDate),
		RawName:        rec.RawName,
		NameSource:     rec.NameSource,
		Magnet:         rec.Magnet,
		MagnetHash:     rec.MagnetHash,
		SizeBytes:      rec.SizeBytes,
		ParsedTitle:    rec.ParsedTitle,
		ParsedYear:     rec.ParsedYear,
		Season:         rec.Season,
		Episode:        rec.Episode,
		EpisodeEnd:     rec.EpisodeEnd,
		IsBatch:        rec.IsBatch,
		Resolution:     rec.Resolution,
		VideoCodec:     rec.VideoCodec,
		SourceTag:      rec.SourceTag,
		SubscriptionID: rec.SubscriptionID,
		SubTitle:       subTitle,
		MatchScore:     rec.MatchScore,
		QualityScore:   rec.QualityScore,
		Status:         rec.Status,
		StatusLabel:    statusLabel(rec.Status),
		Reason:         rec.Reason,
		OfflineTaskID:  rec.OfflineTaskID,
		AccountID:      rec.AccountID,
		ProviderKind:   rec.ProviderKind,
		RetryCount:     rec.RetryCount,
		NextRetryAt:    formatTime(rec.NextRetryAt),
		CreatedAt:      formatTime(rec.CreatedAt),
	}
}

// ManualPush 手动推送一条记录（待确认 / 未匹配 / 推送失败的兜底入口）。
//
// subscriptionID 非零时可以把记录改挂到用户手动指定的订阅上 —— 这正是
// ambiguous 记录的处理方式：系统不敢赌，让用户点一下。
func (s *Service) ManualPush(ctx context.Context, recordID, subscriptionID int64) (*RecordView, error) {
	rec, err := s.records.Get(ctx, recordID)
	if err != nil {
		return nil, err
	}
	targetSubID := subscriptionID
	if targetSubID <= 0 {
		targetSubID = rec.SubscriptionID
	}
	if targetSubID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "请先指定这条记录属于哪个订阅")
	}
	sub, err := s.subs.Get(ctx, targetSubID)
	if err != nil {
		return nil, err
	}
	if sub.Status == domain.TGSubStatusCompleted || sub.Status == domain.TGSubStatusPaused {
		return nil, domain.Errorf(domain.CodeValidation, "订阅「%s」当前是%s状态，请先恢复订阅", sub.Title, statusLabelText(sub.Status))
	}

	rec.SubscriptionID = sub.ID
	if err := s.pushRecord(ctx, sub, rec); err != nil {
		rec.Status = domain.TGRecordFailed
		rec.Reason = "手动推送失败：" + err.Error()
		rec.RetryCount++
		_ = s.records.Update(ctx, rec)
		return nil, err
	}
	view := recordView(rec, sub.Title)
	return &view, nil
}

// IgnoreRecord 人工忽略一条记录。
func (s *Service) IgnoreRecord(ctx context.Context, recordID int64) error {
	rec, err := s.records.Get(ctx, recordID)
	if err != nil {
		return err
	}
	rec.Status = domain.TGRecordIgnored
	rec.Reason = "已人工忽略"
	rec.NextRetryAt = time.Time{}
	return s.records.Update(ctx, rec)
}

// ClearRecords 清空历史。
func (s *Service) ClearRecords(ctx context.Context, before time.Time) (int64, error) {
	return s.records.ClearBefore(ctx, before)
}

// ————————————————————— 状态 —————————————————————

// Stats 汇总匹配历史各状态计数与运行时状态。
type Stats struct {
	Status  Status         `json:"status"`
	Records map[string]int `json:"records"`
}

func (s *Service) Stats(ctx context.Context) Stats {
	out := Stats{Status: s.Status(ctx)}
	if counts, err := s.records.CountByStatus(ctx); err == nil {
		out.Records = counts
	} else {
		out.Records = map[string]int{}
	}
	return out
}

// PreviewQuality 是匹配算法的调试器：粘一段发布名，看解析、画质判定与候选得分。
//
// 「画质规则」tab 的试跑框用它。它把整条决策链摊开给用户看 ——
// 没有这个，用户调画质规则只能靠猜。
func (s *Service) PreviewQuality(ctx context.Context, rawName string, profileID int64) (map[string]any, error) {
	rawName = strings.TrimSpace(rawName)
	if rawName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请填写要试跑的发布名")
	}

	rel := ParseReleaseName(rawName)
	cfg, usedProfile := s.previewConfig(ctx, profileID)
	verdict := Evaluate(rel, cfg)

	subs, err := s.subs.List(ctx, "")
	if err != nil {
		return nil, err
	}
	recalled := RecallCandidates(rel, subs)
	scored := make([]ScoredSubscription, 0, len(recalled))
	for _, sub := range recalled {
		scored = append(scored, ScoredSubscription{Subscription: sub, MatchScore: ScoreCandidate(rel, sub)})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	candidates := make([]map[string]any, 0, len(scored))
	for i, item := range scored {
		if i >= 10 {
			break
		}
		candidates = append(candidates, map[string]any{
			"subscription_id": item.Subscription.ID,
			"title":           item.Subscription.Title,
			"year":            item.Subscription.Year,
			"media_type":      item.Subscription.MediaType,
			"match_score":     round1(item.Score),
			"accepted":        item.Score >= matchAcceptThreshold,
			"reason":          item.Reason,
			"title_hit":       item.TitleHit,
			"title_source":    item.TitleSource,
		})
	}

	// 没有候选时也把「差一点」的原因给出来，否则用户不知道是片名没对上还是被年份筛掉。
	unmatchedHint := ""
	if len(scored) == 0 && len(subs) > 0 {
		best := 0.0
		for _, sub := range subs {
			score := ScoreCandidate(rel, sub)
			if score.Score > best {
				best = score.Score
				unmatchedHint = sub.Title + "：" + score.Reason
			}
		}
		if unmatchedHint == "" {
			unmatchedHint = "所有订阅的片名都不重合"
		}
	}

	return map[string]any{
		"parsed": map[string]any{
			"raw":              rel.Raw,
			"title_candidates": rel.TitleCandidates,
			"year":             derefIntAny(rel.Year),
			"season":           derefIntAny(rel.Season),
			"episode":          derefIntAny(rel.Episode),
			"episode_end":      derefIntAny(rel.EpisodeEnd),
			"is_batch":         rel.IsBatch,
			"resolution":       rel.Resolution,
			"video_codec":      rel.VideoCodec,
			"source":           rel.Source,
			"audio_codec":      rel.AudioCodec,
			"release_group":    rel.ReleaseGroup,
			"looks_like":       rel.LooksLikeRelease(),
		},
		"quality": map[string]any{
			"passed": verdict.Passed,
			"score":  round1(verdict.Score),
			"reason": verdict.Reason,
		},
		"quality_profile_id": usedProfile,
		"candidates":         candidates,
		"unmatched_hint":     unmatchedHint,
		"thresholds": map[string]any{
			"accept":    matchAcceptThreshold,
			"ambiguous": matchAmbiguousThreshold,
		},
	}, nil
}

func (s *Service) previewConfig(ctx context.Context, profileID int64) (domain.TGQualityConfig, int64) {
	if profileID <= 0 {
		profileID = s.defaultQualityProfileID()
	}
	if profileID > 0 {
		if profile, err := s.quality.Get(ctx, profileID); err == nil && profile != nil {
			if cfg, ok := decodeQualityConfig(profile.Config); ok {
				return NormalizeQualityConfig(cfg), profile.ID
			}
		}
	}
	if profile, err := s.quality.GetDefault(ctx); err == nil && profile != nil {
		if cfg, ok := decodeQualityConfig(profile.Config); ok {
			return NormalizeQualityConfig(cfg), profile.ID
		}
	}
	return DefaultQualityConfig(), 0
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func derefIntAny(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// ————————————————————— 工具 —————————————————————

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ProviderSummary 汇总可用性与降级提示，供前端在订阅详情里提示用户。
func (s *Service) ProviderSummary(ctx context.Context, accountID int64) string {
	if accountID <= 0 || s.offline == nil {
		return ""
	}
	caps, err := s.offline.Capabilities(ctx, accountID)
	if err != nil {
		return "无法探测网盘能力，将使用内置下载器"
	}
	if caps.SupportsURLs && containsFold(caps.URLSchemes, "magnet") {
		return offlinedownload.ProviderNative
	}
	if caps.BuiltinEnabled && containsFold(caps.BuiltinURLSchemes, "magnet") {
		return offlinedownload.ProviderBuiltin
	}
	return "unsupported"
}
