package jav

import (
	"context"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/quality"
)

// SubscriptionView 是订阅的对外形态。
//
// 它比 domain.JavSubscription 多了一个合成的 Mode：界面上是三档
// （严格 / 洗版 / 预下载），而库里是正交的两列。合成只朝一个方向做 ——
// 写入仍然走 DownloadMode + PreDownload 两个独立字段，见 quality.SubscriptionMode。
type SubscriptionView struct {
	ID         int64  `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetURL  string `json:"target_url"`
	TargetName string `json:"target_name"`
	Status     string `json:"status"`

	// 卡片上那张封面：影片订阅给影片封面，演员订阅给头像，清单/在线没有。
	// 由服务端补齐 —— 订阅表里不存封面（它是影片的属性，不是订阅的属性），
	// 冗余存一份会在影片封面更新后对不上。
	Cover       string `json:"cover"`
	Thumb       string `json:"thumb"`
	Number      string `json:"number"`
	ReleaseDate string `json:"release_date"`

	Mode         string `json:"mode"`
	DownloadMode string `json:"download_mode"`
	PreDownload  bool   `json:"pre_download"`

	IncludeCommentLinks bool `json:"include_comment_links"`

	Qualities         []string `json:"qualities"`
	MinSizeMB         *int     `json:"min_size_mb"`
	MaxSizeMB         *int     `json:"max_size_mb"`
	MaxFileCount      *int     `json:"max_file_count"`
	ReleaseDateFrom   string   `json:"release_date_from"`
	ReleaseDateTo     string   `json:"release_date_to"`
	ExpiryDays        *int     `json:"expiry_days"`
	Categories        []string `json:"categories"`
	ExcludeCategories []string `json:"exclude_categories"`

	Enabled bool `json:"enabled"`

	TargetAccountID   int64  `json:"target_account_id"`
	TargetParentID    string `json:"target_parent_id"`
	TargetDisplayPath string `json:"target_display_path"`
	PushProvider      string `json:"push_provider"`
	SubfolderMode     string `json:"subfolder_mode"`

	LastCheckedAt string `json:"last_checked_at"`
	LastPushAt    string `json:"last_push_at"`
	CompletedAt   string `json:"completed_at"`
	LastError     string `json:"last_error"`
	// MatchedCount 是**影片**粒度的：符合订阅条件的影片有几部。卡片上的
	// 「检：N」用的就是它 —— 一部片挂五条磁链，这里仍然是 1。
	MatchedCount int `json:"matched_count"`
	// PushedCount 是**成功到网盘**的影片数（去重）。
	//
	// 与 MatchedCount 的分工：前者是「收进来多少部」，这个是「真正落到网盘
	// 多少部」。只数离线任务已完成的记录（status=pushed），刚提交还在下的不算。
	PushedCount int `json:"pushed_count"`
	// PendingCount 是已提交、还在网盘下载的影片数。
	//
	// 卡片上要跟 PushedCount 一起显示：中间那段窗口里只显示 PushedCount 的话
	// 就是「推：0」，用户刚点完推送看到这个只会以为没推成功。
	PendingCount int    `json:"pending_count"`
	CreatedAt    string `json:"created_at"`
}

// SubscriptionInput 是建/改订阅的入参。
//
// 与 quality.Input 几乎同形，但那一份是纯校验用的（不带 ID），
// 这一份带上 ID 供 PATCH 用。多一层映射换来的是：校验逻辑完全不需要知道
// 「这条订阅是从哪儿来的」，测试也不用构造领域实体。
type SubscriptionInput struct {
	ID         int64  `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetURL  string `json:"target_url"`
	TargetName string `json:"target_name"`

	DownloadMode string `json:"download_mode"`
	PreDownload  bool   `json:"pre_download"`

	IncludeCommentLinks bool `json:"include_comment_links"`

	Qualities         []string `json:"qualities"`
	MinSizeMB         *int     `json:"min_size_mb"`
	MaxSizeMB         *int     `json:"max_size_mb"`
	MaxFileCount      *int     `json:"max_file_count"`
	ReleaseDateFrom   string   `json:"release_date_from"`
	ReleaseDateTo     string   `json:"release_date_to"`
	ExpiryDays        *int     `json:"expiry_days"`
	Categories        []string `json:"categories"`
	ExcludeCategories []string `json:"exclude_categories"`

	Enabled bool `json:"enabled"`

	TargetAccountID   int64  `json:"target_account_id"`
	TargetParentID    string `json:"target_parent_id"`
	TargetDisplayPath string `json:"target_display_path"`
	PushProvider      string `json:"push_provider"`
	SubfolderMode     string `json:"subfolder_mode"`
}

// toQualityInput 转成纯校验层的入参。
func (in SubscriptionInput) toQualityInput() quality.Input {
	return quality.Input{
		TargetType: in.TargetType, TargetID: in.TargetID, TargetURL: in.TargetURL,
		TargetName:   in.TargetName,
		DownloadMode: in.DownloadMode, PreDownload: in.PreDownload,
		IncludeCommentLinks: in.IncludeCommentLinks,
		Qualities: in.Qualities, MinSizeMB: in.MinSizeMB, MaxSizeMB: in.MaxSizeMB,
		MaxFileCount: in.MaxFileCount, ReleaseDateFrom: in.ReleaseDateFrom,
		ReleaseDateTo: in.ReleaseDateTo, ExpiryDays: in.ExpiryDays,
		Categories: in.Categories, ExcludeCategories: in.ExcludeCategories,
		Enabled: in.Enabled, TargetAccountID: in.TargetAccountID,
		TargetParentID: in.TargetParentID, TargetDisplayPath: in.TargetDisplayPath,
		PushProvider: in.PushProvider, SubfolderMode: in.SubfolderMode,
	}
}

// CreateSubscription 建订阅。
func (s *Service) CreateSubscription(ctx context.Context, in SubscriptionInput) (*SubscriptionView, error) {
	payload, err := quality.ValidateSubscriptionPayload(in.toQualityInput(), true)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}

	// 同一个目标只能有一条订阅。先查一次给出友好文案 —— 光靠唯一索引的话，
	// 用户看到的是一句 SQLite 的 constraint failed。
	if existing, gerr := s.subs.GetByTarget(ctx, payload.TargetType, payload.TargetKey); gerr == nil && existing != nil {
		return nil, domain.Errorf(domain.CodeValidation, "已经订阅过 %s 了", existing.TargetName)
	}

	rec := viewToDomain(payload)
	id, err := s.subs.Create(ctx, rec)
	if err != nil {
		return nil, err
	}
	created, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := toSubscriptionView(created)
	return &view, nil
}

// UpdateSubscription 改订阅。
func (s *Service) UpdateSubscription(ctx context.Context, in SubscriptionInput) (*SubscriptionView, error) {
	if in.ID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "订阅 id 无效")
	}
	existing, err := s.subs.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	// 目标字段在编辑表单里是只读的，没回传时用已存的值补上 ——
	// 否则「只改一个画质条件」会因为「目标为空」被校验拒掉。
	if strings.TrimSpace(in.TargetType) == "" {
		in.TargetType = existing.TargetType
	}
	if strings.TrimSpace(in.TargetID) == "" {
		in.TargetID = existing.TargetID
	}
	if strings.TrimSpace(in.TargetURL) == "" {
		in.TargetURL = existing.TargetURL
	}
	if strings.TrimSpace(in.TargetName) == "" {
		in.TargetName = existing.TargetName
	}

	payload, err := quality.ValidateSubscriptionPayload(in.toQualityInput(), false)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}

	rec := viewToDomain(payload)
	rec.ID = in.ID
	rec.Status = existing.Status
	if err := s.subs.Update(ctx, rec); err != nil {
		return nil, err
	}
	updated, err := s.subs.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	view := toSubscriptionView(updated)
	return &view, nil
}

// viewToDomain 把校验后的入参转成领域模型。状态不在这里设 ——
// 新建时由 repo 的默认值给 active，更新时保留原值。
func viewToDomain(p *quality.Payload) *domain.JavSubscription {
	return &domain.JavSubscription{
		TargetType: p.TargetType, TargetID: p.TargetID, TargetURL: p.TargetURL,
		TargetKey: p.TargetKey, TargetName: p.TargetName,
		DownloadMode: p.DownloadMode, PreDownload: p.PreDownload,
		IncludeCommentLinks: p.IncludeCommentLinks,
		Qualities: p.Qualities,
		MinSizeMB: p.MinSizeMB, HasMinSize: p.HasMinSize,
		MaxSizeMB: p.MaxSizeMB, HasMaxSize: p.HasMaxSize,
		MaxFileCount: p.MaxFileCount, HasMaxFileCount: p.HasMaxFileCount,
		ReleaseDateFrom: p.ReleaseDateFrom, ReleaseDateTo: p.ReleaseDateTo,
		ExpiryDays: p.ExpiryDays, HasExpiryDays: p.HasExpiryDays,
		Categories: p.Categories, ExcludeCategories: p.ExcludeCategories,
		Enabled:           p.Enabled,
		TargetAccountID:   p.TargetAccountID,
		TargetParentID:    p.TargetParentID,
		TargetDisplayPath: p.TargetDisplayPath,
		PushProvider:      p.PushProvider,
		SubfolderMode:     p.SubfolderMode,
	}
}

// toSubscriptionView 转对外视图。
func toSubscriptionView(s *domain.JavSubscription) SubscriptionView {
	return SubscriptionView{
		ID: s.ID, TargetType: s.TargetType, TargetID: s.TargetID, TargetURL: s.TargetURL,
		TargetName: s.TargetName, Status: s.Status,
		Mode: s.Mode(), DownloadMode: s.DownloadMode, PreDownload: s.PreDownload,
		IncludeCommentLinks: s.IncludeCommentLinks,
		Qualities:         orEmptyStrings(s.Qualities),
		MinSizeMB:         intPtrIf(s.MinSizeMB, s.HasMinSize),
		MaxSizeMB:         intPtrIf(s.MaxSizeMB, s.HasMaxSize),
		MaxFileCount:      intPtrIf(s.MaxFileCount, s.HasMaxFileCount),
		ReleaseDateFrom:   s.ReleaseDateFrom,
		ReleaseDateTo:     s.ReleaseDateTo,
		ExpiryDays:        intPtrIf(s.ExpiryDays, s.HasExpiryDays),
		Categories:        orEmptyStrings(s.Categories),
		ExcludeCategories: orEmptyStrings(s.ExcludeCategories),
		Enabled:           s.Enabled,
		TargetAccountID:   s.TargetAccountID,
		TargetParentID:    s.TargetParentID,
		TargetDisplayPath: s.TargetDisplayPath,
		PushProvider:      s.PushProvider,
		SubfolderMode:     s.SubfolderMode,
		LastCheckedAt:     formatTS(s.LastCheckedAt),
		LastPushAt:        formatTS(s.LastPushAt),
		CompletedAt:       formatTS(s.CompletedAt),
		LastError:         s.LastError,
		MatchedCount:      s.MatchedCount,
		CreatedAt:         formatTS(s.CreatedAt),
	}
}

// ListSubscriptions 列订阅，并补齐卡片要用的封面等信息。
func (s *Service) ListSubscriptions(ctx context.Context, f domain.JavSubscriptionFilter) ([]SubscriptionView, int, error) {
	rows, total, err := s.subs.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]SubscriptionView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSubscriptionView(r))
	}
	s.enrichSubscriptionCovers(ctx, out)
	s.enrichSubscriptionPushedCounts(ctx, out)
	return out, total, nil
}

// enrichSubscriptionPushedCounts 批量补「已推多少部」。
//
// 与封面一样是一次查完，不逐条查：订阅页一屏几十张卡。
// 查不到就留 0 —— 这个数字缺失只影响卡片上的一行字，
// 不该因为它让整个订阅列表拉不出来。
func (s *Service) enrichSubscriptionPushedCounts(ctx context.Context, views []SubscriptionView) {
	if len(views) == 0 {
		return
	}
	ids := make([]int64, 0, len(views))
	for i := range views {
		if views[i].ID > 0 {
			ids = append(ids, views[i].ID)
		}
	}
	counts, err := s.records.CountPushedBySubscription(ctx, ids)
	if err != nil {
		s.logWarn("jav load pushed counts failed", "err", err)
		return
	}
	for i := range views {
		views[i].PushedCount = counts[views[i].ID].Pushed
		views[i].PendingCount = counts[views[i].ID].Pending
	}
}

// enrichSubscriptionCovers 批量补封面，避免逐条查库。
//
// 影片订阅取影片封面、演员订阅取头像 —— 两者分别批量取一次。
func (s *Service) enrichSubscriptionCovers(ctx context.Context, views []SubscriptionView) {
	movieIDs := make([]string, 0, len(views))
	actorIDs := make([]string, 0, len(views))
	for _, v := range views {
		switch v.TargetType {
		case domain.JavTargetMovie, domain.JavTargetOnline:
			if v.TargetID != "" {
				movieIDs = append(movieIDs, v.TargetID)
			}
		case domain.JavTargetActor:
			if v.TargetID != "" {
				actorIDs = append(actorIDs, v.TargetID)
			}
		}
	}

	movies := map[string]*domain.JavMovie{}
	if len(movieIDs) > 0 {
		if got, err := s.movies.GetMany(ctx, movieIDs); err == nil {
			for _, m := range got {
				movies[m.ID] = m
			}
		} else {
			s.logWarn("jav load subscription covers failed", "err", err)
		}
	}
	actors := map[string]*domain.JavActor{}
	if len(actorIDs) > 0 {
		if got, err := s.movies.ActorsByIDs(ctx, actorIDs); err == nil {
			actors = got
		} else {
			s.logWarn("jav load subscription avatars failed", "err", err)
		}
	}

	for i := range views {
		v := &views[i]
		switch v.TargetType {
		case domain.JavTargetMovie, domain.JavTargetOnline:
			if m := movies[v.TargetID]; m != nil {
				v.Cover = m.Cover()
				v.Thumb = m.ThumbURL
				v.Number = m.Number
				v.ReleaseDate = m.ReleaseDate
			}
		case domain.JavTargetActor:
			if a := actors[v.TargetID]; a != nil {
				// 演员没有横版封面，头像就放在同一个位置。
				v.Cover = a.AvatarURL
				v.Thumb = a.AvatarURL
			}
		}
	}
}

// CompletedMovieView 是「已完成」那一档里的一条：一部推送成功过的影片。
type CompletedMovieView struct {
	MovieCard
	SubscriptionID   int64  `json:"subscription_id"`
	SubscriptionName string `json:"subscription_name"`
	TargetType       string `json:"target_type"`
	PushedAt         string `json:"pushed_at"`
	Status           string `json:"sub_status"`
}

// CompletedMovies 列出推送成功过的影片。
//
// 「已完成」那一档要看的是「我到底拿到了哪些片」，而不是「哪些订阅跑完了」——
// 所以它的数据源是**推送成功的记录**，不是订阅状态。这样影片订阅、演员订阅、
// 清单订阅推成功的片子会自然地汇到一处，不需要为每种类型各写一套。
func (s *Service) CompletedMovies(ctx context.Context, page, pageSize int, sortKey string, desc bool) ([]CompletedMovieView, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 60
	}

	rows, total, err := s.attempts.ListSucceededMovies(ctx, pageSize, (page-1)*pageSize, sortKey, desc)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return []CompletedMovieView{}, total, nil
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.MovieID)
	}
	movies, err := s.movies.GetMany(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[string]*domain.JavMovie, len(movies))
	for _, m := range movies {
		byID[m.ID] = m
	}

	names := s.subscriptionNames(ctx)
	inLibrary, _ := s.libraryCodes(ctx)

	out := make([]CompletedMovieView, 0, len(rows))
	for _, r := range rows {
		m := byID[r.MovieID]
		if m == nil {
			// 影片被删了但推送记录还在：跳过而不是给一张空卡片。
			continue
		}
		_, hit := inLibrary[m.Number]
		// subscription_id=0 就是「手动推送」（详情页点的那颗磁链不挂订阅）。
		name := names[r.SubscriptionID]
		if r.SubscriptionID <= 0 {
			name = "手动推送"
		}
		out = append(out, CompletedMovieView{
			MovieCard:        toCard(m, hit && m.Number != ""),
			SubscriptionID:   r.SubscriptionID,
			SubscriptionName: name,
			TargetType:       r.TargetType,
			PushedAt:         formatTS(r.PushedAt),
			Status:           domain.JavSubStatusCompleted,
		})
	}
	return out, total, nil
}

func (s *Service) subscriptionNames(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	rows, _, err := s.subs.List(ctx, domain.JavSubscriptionFilter{Limit: 1000})
	if err != nil {
		return out
	}
	for _, r := range rows {
		out[r.ID] = r.TargetName
	}
	return out
}

// Subscription 取单条订阅。
func (s *Service) Subscription(ctx context.Context, id int64) (*SubscriptionView, error) {
	rec, err := s.subs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := toSubscriptionView(rec)
	return &view, nil
}

// DeleteSubscription 删订阅（连带清掉候选与投递尝试）。
func (s *Service) DeleteSubscription(ctx context.Context, id int64) error {
	return s.subs.Delete(ctx, id)
}

// SetSubscriptionStatus 改状态。
func (s *Service) SetSubscriptionStatus(ctx context.Context, id int64, status string) error {
	switch status {
	case domain.JavSubStatusActive, domain.JavSubStatusPaused, domain.JavSubStatusCompleted:
	default:
		return domain.Errorf(domain.CodeValidation, "未知的订阅状态：%s", status)
	}
	if _, err := s.subs.Get(ctx, id); err != nil {
		return err
	}
	return s.subs.SetStatus(ctx, id, status)
}

// ————————————————————— 黑名单 —————————————————————

// BlacklistEntryView 是黑名单条目的对外形态。
type BlacklistEntryView struct {
	ID         int64  `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Reason     string `json:"reason"`
	CreatedAt  string `json:"created_at"`
	// MoviesCount 是这条黑名单**加入时**记下来的影片数（快照）。
	// 卡片上显示「N 部影片」用；0 表示当时没取到（老条目或上游不通）。
	MoviesCount int `json:"movies_count"`
}

// BlacklistInput 是加黑名单的入参。
type BlacklistInput struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetURL  string `json:"target_url"`
	TargetName string `json:"target_name"`
	Reason     string `json:"reason"`
	// SubscriptionID 是「从哪条订阅点进来的」。有它才好在**落黑名单之前**把
	// 那一刻符合条件的影片算出来存成快照（见 blacklistSnapshot）。
	SubscriptionID int64 `json:"subscription_id"`
}

// ListBlacklist 列黑名单。
func (s *Service) ListBlacklist(ctx context.Context, targetType string) ([]BlacklistEntryView, error) {
	rows, err := s.blacklist.List(ctx, targetType)
	if err != nil {
		return nil, err
	}
	out := make([]BlacklistEntryView, 0, len(rows))
	for _, e := range rows {
		out = append(out, BlacklistEntryView{
			ID: e.ID, TargetType: e.TargetType, TargetID: e.TargetID,
			TargetName: e.TargetName, Reason: e.Reason, CreatedAt: formatTS(e.CreatedAt),
			MoviesCount: len(e.MovieIDs),
		})
	}
	return out, nil
}

// BlacklistMovies 取一条黑名单挡住的影片。
//
// 两种来源，按条目里有没有快照分：
//
//   - **有快照**（新加的条目）：读那份「加入那一刻符合条件的影片」——
//     只读库里存的，不现算。拉黑之后这些片在匹配上全部变成不合格，现算只会是空集
//     （见 blacklistSnapshot 的注释）。
//   - **没快照**（迁移之前加的老条目、或加入时上游不通）：退回按**目标**去列 ——
//     该演员/该清单名下的片。这跟快照不是同一个口径（快照还叠了当时的日期窗、
//     清晰度等条件），但它是这条黑名单**正在挡住**的那些片，对「点开看看挡了谁」
//     这个问题是对得上的；老条目要是只因为没快照就点不开，那才是真没法看。
func (s *Service) BlacklistMovies(ctx context.Context, id int64) ([]MovieCard, error) {
	entry, err := s.blacklist.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, domain.Errorf(domain.CodeNotFound, "黑名单条目不存在")
	}

	var movies []*domain.JavMovie
	if len(entry.MovieIDs) > 0 {
		// 影片可能已经被删/从未入库：GetMany 只返回还在的那些，其余静默跳过 ——
		// 快照是历史记录，缺几张不该让整档打不开。
		if movies, err = s.movies.GetMany(ctx, entry.MovieIDs); err != nil {
			return nil, err
		}
	} else {
		movies, err = s.targetMoviesOf(ctx, entry.TargetType, entry.TargetID)
		if err != nil {
			return nil, err
		}
	}

	// 按上映日期倒序，与演员/清单订阅的影片弹窗同一个次序。
	sort.SliceStable(movies, func(i, j int) bool {
		if movies[i].ReleaseDate != movies[j].ReleaseDate {
			return movies[i].ReleaseDate > movies[j].ReleaseDate
		}
		return movies[i].ID > movies[j].ID
	})
	inLibrary, _ := s.libraryCodes(ctx)
	out := make([]MovieCard, 0, len(movies))
	for _, m := range movies {
		_, hit := inLibrary[m.Number]
		out = append(out, toCard(m, hit && m.Number != ""))
	}
	return out, nil
}

// targetMoviesOf 按「目标类型 + 目标 id」列本地影片，不套任何订阅条件。
//
// 与 localTargetMovies 同源，区别只在于它要一个订阅对象、且会额外返回清单 id。
// 这里只按目标取片，所以不套那层。
func (s *Service) targetMoviesOf(ctx context.Context, targetType, targetID string) ([]*domain.JavMovie, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil, nil
	}
	switch targetType {
	case "movie", "online":
		mv, gerr := s.movies.Get(ctx, targetID)
		if gerr != nil {
			return nil, nil // 影片没入库过就是空的，不是错误
		}
		return []*domain.JavMovie{mv}, nil
	case "actor":
		movies, lerr := s.movies.ListMoviesByActor(ctx, targetID)
		if lerr != nil {
			return nil, lerr
		}
		return movies, nil
	case "list":
		ids, lerr := s.lists.ListIDs(ctx, targetID)
		if lerr != nil {
			return nil, lerr
		}
		movies, gerr := s.movies.GetMany(ctx, ids)
		if gerr != nil {
			return nil, gerr
		}
		return movies, nil
	}
	return nil, nil
}

// AddBlacklist 加一条黑名单。
func (s *Service) AddBlacklist(ctx context.Context, in BlacklistInput) (int64, error) {
	targetType := strings.ToLower(strings.TrimSpace(in.TargetType))
	if _, ok := map[string]struct{}{"movie": {}, "actor": {}, "list": {}}[targetType]; !ok {
		return 0, domain.Errorf(domain.CodeValidation, "黑名单类型只能是影片/演员/清单")
	}
	name := strings.TrimSpace(in.TargetName)
	if name == "" {
		return 0, domain.Errorf(domain.CodeValidation, "黑名单名称不能为空")
	}
	// 快照必须先算、再落库。顺序反了就是空集：MovieOK 读黑名单，这条一旦落了库，
	// 「它挡住了哪些片」当场全变成不合格。
	movieIDs := s.blacklistSnapshot(ctx, targetType, strings.TrimSpace(in.TargetID), in.SubscriptionID)

	return s.blacklist.Create(ctx, &domain.JavBlacklistEntry{
		TargetType: targetType,
		TargetID:   strings.TrimSpace(in.TargetID),
		TargetKey:  quality.CanonicalTargetKey(targetType, in.TargetID, in.TargetURL),
		TargetName: name,
		Reason:     strings.TrimSpace(in.Reason),
		MovieIDs:   movieIDs,
	})
}

// blacklistSnapshot 给黑名单条目配一份「加入了哪些影片」的快照。
//
// 影片级条目没有歧义：就是它自己。
// 演员/清单级条目则要**在落库之前**取那一刻该订阅符合条件的影片 —— 落库之后再算，
// MovieOK 会把这些片全判成不合格（它读黑名单），拿到的只会是空集，而「黑名单」
// 那一档点开卡片要看的正是它们。
//
// 取不到（上游不通、没有订阅 id）就留空：**快照是给人看的记录，不是拉黑的前置条件**，
// 不能因为它算不出来就不让用户拉黑。
func (s *Service) blacklistSnapshot(ctx context.Context, targetType, targetID string, subscriptionID int64) []string {
	if targetType == "movie" {
		if targetID == "" {
			return nil
		}
		return []string{targetID}
	}
	if subscriptionID <= 0 {
		return nil
	}
	views, err := s.SubscriptionMovies(ctx, subscriptionID)
	if err != nil {
		s.logWarn("jav blacklist snapshot failed", "sub", subscriptionID, "err", err)
		return nil
	}
	ids := make([]string, 0, len(views))
	for _, v := range views {
		if v.Eligible {
			ids = append(ids, v.MovieCard.ID)
		}
	}
	return ids
}

// DeleteBlacklist 删一条黑名单。
func (s *Service) DeleteBlacklist(ctx context.Context, id int64) error {
	return s.blacklist.Delete(ctx, id)
}

// ————————————————————— 工具 —————————————————————

// blacklistCriteria 一次性把黑名单读成三张键集合，供逐条候选比对。
//
// 在循环外读一次而不是每条候选查一次库：演员订阅一轮要判几百部影片，
// 每部一次 SQL 是纯粹的浪费，而且这些集合在一轮检查里不会变。
func (s *Service) blacklistCriteria(ctx context.Context) (movies, actors, lists map[string]struct{}) {
	movies, actors, lists = map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	keys, err := s.blacklist.Keys(ctx)
	if err != nil {
		s.logWarn("jav load blacklist failed", "err", err)
		return
	}
	for k := range keys["movie"] {
		movies[k] = struct{}{}
	}
	for k := range keys["actor"] {
		actors[k] = struct{}{}
	}
	for k := range keys["list"] {
		lists[k] = struct{}{}
	}
	return movies, actors, lists
}

// criteriaOf 把订阅转成纯匹配条件。
func criteriaOf(sub *domain.JavSubscription, bm, ba, bl map[string]struct{}) quality.Criteria {
	return quality.Criteria{
		TargetType: sub.TargetType,
		Qualities:  sub.Qualities,
		MinSizeMB:  sub.MinSizeMB, HasMinSize: sub.HasMinSize,
		MaxSizeMB: sub.MaxSizeMB, HasMaxSize: sub.HasMaxSize,
		MaxFileCount: sub.MaxFileCount, HasMaxFileCount: sub.HasMaxFileCount,
		ReleaseDateFrom: sub.ReleaseDateFrom, ReleaseDateTo: sub.ReleaseDateTo,
		Categories: sub.Categories, ExcludeCategories: sub.ExcludeCategories,
		BlacklistedMovies: bm, BlacklistedActors: ba, BlacklistedLists: bl,
	}
}

func orEmptyStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func intPtrIf(v int, has bool) *int {
	if !has {
		return nil
	}
	return &v
}

// formatTS 把时间格式化成前端直接显示的串。
//
// **转成本地时区再格式化**：库里存的是 UTC，前端拿到之后只做
// `.slice(0, 16).replace("T", " ")`（纯字符串截取，不做时区换算），
// 所以这里不转的话，界面上每个时间都差一个时区 —— 东八区看到的是早 8 小时。
func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(time.RFC3339)
}
