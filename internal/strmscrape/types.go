package strmscrape

const (
	WriteModeMissingOnly = "missing_only"
	WriteModeOverwrite   = "overwrite"

	ItemStatusOK    = "ok"
	ItemStatusMiss  = "miss"
	ItemStatusDoubt = "doubt"

	MediaTypeMovie = "movie"
	MediaTypeTV    = "tv"
)

// Item 表示一部作品（电影文件夹 / 剧集根目录），不是单个 .strm。
type Item struct {
	ID         string `json:"id"`
	RelDir     string `json:"rel_dir"`
	StrmName   string `json:"strm_name,omitempty"`
	Title      string `json:"title"`
	Year       *int   `json:"year,omitempty"`
	MediaType  string `json:"media_type"`
	Status     string `json:"status"`
	HasNFO     bool   `json:"has_nfo"`
	HasPoster  bool   `json:"has_poster"`
	HasPending bool   `json:"has_pending"`
	ManualDone bool   `json:"manual_done"`
	TMDBID     string `json:"tmdb_id,omitempty"`
	PosterURL  string `json:"poster_url,omitempty"`
	FolderName string `json:"folder_name,omitempty"`
	FileCount  int    `json:"file_count"`
	EpLocal    int    `json:"ep_local,omitempty"`
	EpTMDB     int    `json:"ep_tmdb,omitempty"`
	EpScraped  int    `json:"ep_scraped,omitempty"`
	TVState    string `json:"tv_state,omitempty"` // ended|updating
	AddedAt    string `json:"added_at,omitempty"`
}

type ItemListSort string

const (
	ItemListSortTitleAsc  ItemListSort = "title_asc"
	ItemListSortYearDesc  ItemListSort = "year_desc"
	ItemListSortYearAsc   ItemListSort = "year_asc"
	ItemListSortAddedAsc  ItemListSort = "added_asc"
	ItemListSortAddedDesc ItemListSort = "added_desc"
)

type ItemListQuery struct {
	Offset    int          `json:"offset"`
	Limit     int          `json:"limit"`
	Keyword   string       `json:"keyword,omitempty"`
	Status    string       `json:"status,omitempty"`
	MediaType string       `json:"media_type,omitempty"`
	TVState   string       `json:"tv_state,omitempty"`
	Sort      ItemListSort `json:"sort,omitempty"`
}

type ItemListStats struct {
	Total int `json:"total"`
	OK    int `json:"ok"`
	Miss  int `json:"miss"`
	Doubt int `json:"doubt"`
}

type ItemListResult struct {
	Items   []Item        `json:"items"`
	Total   int           `json:"total"`
	Offset  int           `json:"offset"`
	Limit   int           `json:"limit"`
	HasMore bool          `json:"has_more"`
	Stats   ItemListStats `json:"stats"`
}

type Progress struct {
	Running bool  `json:"running"`
	TaskID  int64 `json:"strm_task_id"`
	// SourceKey 是这一轮跑的是**哪个数据源**（`wallSource.ScopeKey()`）：
	// 任务是裸十进制、本地目录是 `local-<id>`。
	//
	// 存在的理由：Progress 是**单槽全局**，前端靠它判「这轮是不是我发起的」。
	// 只比 strm_task_id 的话，在本地目录上跑一次刮削会让**任务墙**也亮起「刮削中」。
	SourceKey     string `json:"source_key,omitempty"`
	Total         int    `json:"total"`
	Done          int    `json:"done"`
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	Message       string `json:"message"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	CurrentItemID string `json:"current_item_id"`
	ItemRevision  int    `json:"item_revision"`
	UpdatedItem   *Item  `json:"updated_item,omitempty"`
}

type Settings struct {
	WriteMode string `json:"write_mode"`
	// AIBackfill 是「刮削时要不要用 AI 补认规则认不出的那批」。
	//
	// ⚠️ 它是一个**设置**，不是请求参数：入口只有一颗「开始刮削」，没有第二颗按钮
	// 去承载这个意图；而这件事会花钱，得让用户在设置里显式勾。
	// 仍然**只对本地目录源生效**（任务源永不走 AI，见 aiEnabledFor）。
	AIBackfill bool `json:"ai_backfill"`

	TmdbAPIKey            string `json:"tmdb_api_key"`
	TmdbLanguage          string `json:"tmdb_language"`
	TmdbAPIHost           string `json:"tmdb_api_host"`
	TmdbImageHost         string `json:"tmdb_image_host"`
	TmdbRequestIntervalMS int    `json:"tmdb_request_interval_ms"`

	// ThePornDBKey 是**欧美片**刮削源（theporndb 官方 API）的密钥。
	//
	// 它为什么在这页而不是番号设置里：**刮削相关的设置都归「海报墙 → 设置」**。
	// 它与 `TmdbAPIKey` 是同一类东西（一个外部源要的凭据），所以并排。
	//
	// ⚠️ 回读时只回 `ThePornDBKeySet`（**绝不回密钥本身**，同 TMDB 那条规矩）。
	ThePornDBKey string `json:"theporndb_key"`
	// ThePornDBKeySet 只告诉界面「配没配」，供输入框的 placeholder 用。
	ThePornDBKeySet bool `json:"theporndb_key_set"`

	// FlareSolverrURL 是 FlareSolverr 的地址（破 Cloudflare 盾 / 年龄墙的第三段出口）。
	//
	// 它不是密钥（一般是内网地址），所以**原样回传**，不像 TMDB / theporndb 那样只回「配没配」。
	// 空 = 不启用；启用了才能补 `freejavbt`（CF 盾）与 `dmm`（年龄墙）。
	FlareSolverrURL string `json:"flaresolverr_url"`

	// —— AI 翻译（把非中文的简介/标题翻成中文）——
	//
	// TranslateEnabled 是总开关；TranslateFields 是翻哪几个（`summary` / `title`）；
	// TranslatePrompt 是用户自定义提示词（空 = 用内置那份）。
	//
	// ⚠️ AI 本身（地址/密钥/模型/开关）在「系统设置 → AI 整理」里配 ——
	// 这里只管「刮削时要不要用它翻」。
	TranslateEnabled bool     `json:"translate_enabled"`
	TranslateFields  []string `json:"translate_fields"`
	TranslatePrompt  string   `json:"translate_prompt"`
}

type RunRequest struct {
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先，表示「刮的是墙上挂的那个本地目录」。
	LocalDirID string `json:"local_dir_id,omitempty"`
	WriteMode  string `json:"write_mode,omitempty"`
	// ItemIDs 非空时**只刮这几部**（页头「批量选择」勾中的作品）。
	//
	// 元素是 `buildItem` 给的条目 id（`pathToItemID(relKey)` 的十六进制）。
	// 空 = 整根扫（老行为逐字不变）。
	ItemIDs []string `json:"item_ids,omitempty"`
}

type Scope struct {
	StrmTaskID   int64    `json:"strm_task_id"`
	ExcludedDirs []string `json:"excluded_dirs"`

	// 下面三个是**内部**字段（不进 JSON）：本地目录源没有 taskID，键与根都由它们给。
	// 前端只需要 strm_task_id 与 excluded_dirs 这两个既有字段。
	ScopeKey   string `json:"-"`
	Root       string `json:"-"`
	MediaKind  string `json:"-"`
	LocalDirID string `json:"-"`
}

type ScopeDirectory struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ModTime string `json:"mod_time,omitempty"`
}

type RematchRequest struct {
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先（挂上来的本地目录源，见 wall_source.go）。
	LocalDirID string `json:"local_dir_id,omitempty"`
	ItemID     string `json:"item_id"`
	TMDBID     string `json:"tmdb_id"`
	MediaType  string `json:"media_type"`
	Title      string `json:"title,omitempty"`
	Year       *int   `json:"year,omitempty"`
}

type MarkNormalRequest struct {
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先（挂上来的本地目录源，见 wall_source.go）。
	LocalDirID string `json:"local_dir_id,omitempty"`
	ItemID     string `json:"item_id"`
	MediaType  string `json:"media_type,omitempty"`
	ClearMatch bool   `json:"clear_match,omitempty"`
}

type RescrapeRequest struct {
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先（挂上来的本地目录源，见 wall_source.go）。
	LocalDirID string `json:"local_dir_id,omitempty"`
	ItemID     string `json:"item_id"`
}
