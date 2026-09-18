package settings

import (
	"litepan/internal/announcement"
	"litepan/internal/domain"
)

// 全局设置：默认值在代码，DB 仅存用户改过的项。

// 设置键。oauth 复用 domain 常量，保证与驱动层读取一致。
const (
	KeyOAuthServerURL              = domain.SettingOAuthServerURL
	KeyCacheEnabled                = "cache_enabled"
	KeyCacheTTL                    = "cache_ttl"
	KeyCacheMaxItems               = "cache_max_items"
	KeyCacheMemoryLimitMB          = "cache_memory_limit_mb"
	KeyCachePersistenceEnabled     = "cache_persistence_enabled"
	KeyCachePersistenceIntervalMin = "cache_persistence_interval_minutes"
	KeyUploadTaskConcurrency       = "upload_task_concurrency"
	KeyBuiltinOfflineTempDir       = "builtin_offline_temp_dir"
	KeyBuiltinOfflineMaxSpeedMB    = "builtin_offline_max_speed_mb"
	KeyBuiltinOfflineBTPort        = "builtin_offline_bt_port"
	KeyWebDAVCacheEnabled          = "webdav_cache_enabled"
	KeyFuseReadCacheEnabled        = "fuse_read_cache_enabled"
	KeyFuseReadCacheMaxGB          = "fuse_read_cache_max_gb"
	KeyFuseReadCacheRetentionDays  = "fuse_read_cache_retention_days"
	KeyFuseReadCacheEvictionPolicy = "fuse_read_cache_eviction_policy"
	KeyAuthActiveRefresh           = "auth_active_refresh_enabled"
	KeyAccountShowProfile          = "account_show_profile"
	KeyAccountShowMembership       = "account_show_membership"
	KeyLogLevel                    = "log_level"
	KeyLogRetentionDays            = "log_retention_days"
	KeyLogErrorAckAt               = "log_error_ack_at"
	KeyAnnouncementReadVersion     = "announcement_read_version"
	KeyAnnouncementURL             = "announcement_url"

	// 全局出站代理。TMDB（目录整理 / STRM 刮削 / 分类整理）与 TG 频道抓取共用这一份 ——
	// 以前它们各有一套 mo_proxy_* / tg_bot_proxy_*，同一个代理要在两个页面各填一遍。
	// 裸名键，和 announcement_url / log_level 一样属于「其他设置」页。
	KeyProxyEnabled  = "proxy_enabled"
	KeyProxyURL      = "proxy_url"
	KeyProxyUsername = "proxy_username"
	// KeyProxyPassword 标 Sensitive：后端回传的是掩码，密码不出浏览器。
	// 代价是通用表单不能直接渲染它（见 SystemSettings.vue 的 PROXY_PASSWORD_KEY 特判）。
	KeyProxyPassword = "proxy_password"

	KeyEmbyEnabled               = "emby_enabled"
	KeyEmbyProxyInstances        = "emby_proxy_instances"
	KeyFnosEnabled               = "fnos_enabled"
	KeyFnosName                  = "fnos_name"
	KeyFnosURL                   = "fnos_url"
	KeyFnosProxyPort             = "fnos_proxy_port"
	KeyFnosStrmPathMaps          = "fnos_strm_path_maps"
	KeyFnosDirectSTRMClients     = "fnos_direct_strm_clients"
	KeyStrmToken                 = "strm_token"
	KeyStrmBaseURL               = "strm_base_url"
	KeyStrmSignatureEnabled      = "strm_signature_enabled"
	KeyStrmDefaultScanInterval   = "strm_default_scan_interval"
	KeyStrmDefaultExtensions     = "strm_default_extensions"
	KeyStrmISOFilenameEnabled    = "strm_iso_filename_enabled"
	KeyStrmMinFileSizeMB         = "strm_min_file_size_mb"
	KeyStrmConflictPolicy        = "strm_conflict_policy"
	KeyStrmTaskConcurrency       = "strm_task_concurrency"
	KeyStrmMetadataExtensions    = "strm_metadata_extensions"
	KeyStrmMetadataMaxSizeMB     = "strm_metadata_max_size_mb"
	KeyStrmMetadataParentEnabled = "strm_metadata_parent_enabled"
	KeyStrmMetadataSyncMode      = "strm_metadata_sync_mode"
	KeyStrmTool115TreeEnabled    = "strm_tool_115_tree_enabled"
	KeyLocalUploadEnabled        = "local_upload_enabled"
	KeyLocalUploadMappings       = "local_upload_mappings"
	KeyCoverExtractEnabled       = "cover_extract_enabled"
	KeyCoverExtractStyle         = "cover_extract_style"
	KeyQuarkTVEnabled            = "quark_tv_enabled"
	KeyQuarkTVPlayMode           = "quark_tv_play_mode"
	KeyQuarkTVClientListMode     = "quark_tv_client_list_mode"
	KeyQuarkTVProxyClients       = "quark_tv_proxy_clients"
	KeyStrmScrapeWriteMode       = "strm_scrape_write_mode"
	KeyStrmScrapeScopes          = "strm_scrape_scopes"

	KeyMOTmdbAPIKey            = "mo_tmdb_api_key"
	KeyMOTmdbLanguage          = "mo_tmdb_language"
	KeyMOTmdbAPIHost           = "mo_tmdb_api_host"
	KeyMOTmdbImageHost         = "mo_tmdb_image_host"
	KeyMOAPIRequestIntervalMS  = "mo_api_request_interval_ms"
	KeyMOTmdbRequestIntervalMS = "mo_tmdb_request_interval_ms"
	KeyMOFileExtensions        = "mo_file_extensions"
	KeyMOMetadataExtensions    = "mo_metadata_extensions"
	KeyMOMediaTagOrder         = "mo_media_tag_order"
	KeyMOAlignMediaTags        = "mo_align_media_tags"
	KeyMOMaxWorksPerRun        = "mo_max_works_per_run"
	KeyMOOverwriteExisting     = "mo_overwrite_existing"
	KeyAIOrganizeEnabled       = "ai_organize_enabled"
	KeyAIOrganizeInstances     = "ai_organize_instances"
	KeyAIOrganizeBaseURL       = "ai_organize_base_url"
	KeyAIOrganizeAPIKey        = "ai_organize_api_key"
	KeyAIOrganizeModel         = "ai_organize_model"
	KeyMOClassificationEnabled = "mo_classification_enabled"
	KeyMOClassificationConfig  = "mo_classification_config"

	// 番号匹配方案（与 TMDB 方案并行、互斥）。
	KeyMOJavRules         = "mo_jav_rules"
	KeyMOJavDeleteSmall   = "mo_jav_delete_small"
	KeyMOJavSmallFileMB   = "mo_jav_small_file_mb"
	KeyMOJavCleanEmpty    = "mo_jav_clean_empty_dirs"
	KeyMOJavMaxDirs       = "mo_jav_max_dirs"
	KeyMOJavDeleteTypes   = "mo_jav_delete_types"
	KeyMOJavDeleteExclude = "mo_jav_delete_exclude_types"

	// TG 影片订阅。这些项全部 Hidden —— 不希望在「系统设置」里出现第二个入口，
	// 只在该功能自己的配置弹窗里通过 /api/tg-subscribe/config 读写。
	//
	// 取消息方式已从 Bot API 长轮询改为抓 t.me 公开网页预览（见 internal/tgsubscribe/preview），
	// 所以 token / API 反代地址 / update offset 这几个键都不再存在；
	// 代理也已收敛成全局的 proxy_*，不再有 tg_bot_proxy_*。
	// 键名里的 tg_bot 是历史遗留，改名要动数据迁移，收益不抵风险。
	KeyTGBotEnabled          = "tg_bot_enabled"
	KeyTGBotAutoPush         = "tg_bot_auto_push"
	KeyTGBotDefaultAccountID = "tg_bot_default_account_id"
	KeyTGBotDefaultParentID  = "tg_bot_default_parent_id"
	KeyTGBotDefaultPath      = "tg_bot_default_display_path"
	KeyTGBotProfileID        = "tg_bot_default_quality_profile_id"
	KeyTGBotCollectWindowMin = "tg_bot_collect_window_min"
	KeyTGBotMaxPushPerHour   = "tg_bot_max_push_per_hour"
	KeyTGBotLastPollAt       = "tg_bot_last_poll_at"
	KeyTGBotStatus           = "tg_bot_status"
	KeyTGBotStatusMessage    = "tg_bot_status_message"

	// 网页预览抓取的调参项。前两个暴露到前端表单，后两个只在排障时改。
	KeyTGPreviewPollIntervalSec = "tg_preview_poll_interval_sec"
	KeyTGPreviewBackfillPages   = "tg_preview_backfill_pages"
	KeyTGPreviewRequestGapMs    = "tg_preview_request_gap_ms"
	KeyTGPreviewTimeoutSec      = "tg_preview_timeout_sec"

	// KeyTGRecommendedSeeded 记录「内置推荐频道已经入库过一次」。
	//
	// 一次性播种的**唯一**凭据：靠它才能做到「首次启动自动添加」，同时保证
	// 用户删掉某个推荐频道之后不会被下次启动塞回来。
	KeyTGRecommendedSeeded = "tg_recommended_seeded"

	// KeyTGRecommendedPending 是「播种时没加成功的推荐频道」的用户名列表（逗号分隔）。
	//
	// 有它才能把两件事分开：**没加成功**（该给用户一个补加入口）与
	// **用户自己删掉的**（不该再冒出来）。没有这个区分的话，用户删掉一个默认频道，
	// 界面上就会立刻多出一行「未添加 · 补加」，看起来像是删除没生效。
	KeyTGRecommendedPending = "tg_recommended_pending"
)

// Type 决定后台表单控件与校验方式。
type Type string

const (
	TypeString Type = "string"
	TypeInt    Type = "int"
	TypeBool   Type = "bool"
	TypeSelect Type = "select"
)

// Option 是 select 类型的可选项。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Spec 声明单个全局设置的元数据，驱动后台表单渲染与写入校验。
type Spec struct {
	Key         string
	Type        Type
	Category    string
	Label       string
	Description string
	Default     string // 默认值的规范字符串形式（与 configs 表存储一致）
	Unit        string
	Min, Max    *int     // 仅 TypeInt
	Options     []Option // 仅 TypeSelect
	Sensitive   bool
	Hidden      bool
	// normalize 对字符串值做规范化/兜底（如 OAuth 地址校验），nil 表示不处理。
	normalize func(string) string
}

// Category 是设置分组，用于后台分区展示。
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func intp(n int) *int { return &n }

// defaultSpecs 是全部全局设置的有序声明。新增全局设置只改这里。
func boolSpec(key, category, label, description, def string) Spec {
	return Spec{Key: key, Type: TypeBool, Category: category, Label: label, Description: description, Default: def}
}

func stringSpec(key, category, label, description, def string) Spec {
	return Spec{Key: key, Type: TypeString, Category: category, Label: label, Description: description, Default: def}
}

// secretSpec 是 stringSpec 的 Sensitive 版本：后端下发时把值掩成 ******。
//
// 用它的时候前端必须自己处理「留空表示不修改」—— 通用表单会把掩码当成用户改动，
// 一保存就把字面量 ****** 写进库。参考 SystemSettings.vue 里代理密码那一段。
func secretSpec(key, category, label, description string) Spec {
	return Spec{Key: key, Type: TypeString, Category: category, Label: label, Description: description, Sensitive: true}
}

func intSpec(key, category, label, description, def, unit string, min, max int) Spec {
	return Spec{Key: key, Type: TypeInt, Category: category, Label: label, Description: description, Default: def, Unit: unit, Min: intp(min), Max: intp(max)}
}

func selectSpec(key, category, label, description, def string, options []Option) Spec {
	return Spec{Key: key, Type: TypeSelect, Category: category, Label: label, Description: description, Default: def, Options: options}
}

func defaultSpecs() []Spec {
	return []Spec{
		boolSpec(KeyCacheEnabled, "performance", "启用元数据缓存", "关闭后所有目录列表都直连网盘，不走缓存。", "true"),
		intSpec(KeyCacheTTL, "performance", "全局缓存时间", "缓存过期时间", "30", "分钟", 0, 1440),
		intSpec(KeyCacheMaxItems, "performance", "缓存条目上限", "元数据缓存最多保留的条目数，超出按 LRU 淘汰。", "10000", "条", 1000, 1000000),
		intSpec(KeyCacheMemoryLimitMB, "performance", "缓存内存上限", "元数据缓存的字节软上限，接近上限触发分级淘汰。", "128", "MB", 64, 16384),
		boolSpec(KeyCachePersistenceEnabled, "performance", "启用缓存持久化", "定时将未过期元数据缓存写入磁盘，重启后恢复。", "true"),
		intSpec(KeyCachePersistenceIntervalMin, "performance", "持久化快照间隔", "缓存写入磁盘的间隔，修改后立即生效。", "10", "分钟", 1, 1440),
		intSpec(KeyUploadTaskConcurrency, "performance", "任务并发数", "上传、跨盘下载和内置下载三个队列各自使用该并发上限，队列之间不共享槽位；修改后立即生效。", "3", "个", 1, 5),
		stringSpec(KeyBuiltinOfflineTempDir, "performance", "内置离线临时目录", "内置下载器缓存文件所在的容器内路径；Docker 请先把宿主机目录映射进容器。修改后新任务立即使用。", "data/builtin_offline"),
		intSpec(KeyBuiltinOfflineMaxSpeedMB, "performance", "内置离线限速", "HTTP 与 Magnet 共用的全局下载限速；填 0 表示不限速。", "0", "MB/s", 0, 10240),
		intSpec(KeyBuiltinOfflineBTPort, "performance", "磁力下载端口", "用于磁力/BT 下载连接其他节点；Docker Bridge 网络需同时映射同一 TCP/UDP 端口，Host 网络无需映射。填 0 表示随机端口，修改后立即应用。", "42069", "", 0, 65535),
		boolSpec(KeyWebDAVCacheEnabled, "performance", "WebDAV 路径与 PROPFIND 缓存", "开启后缓存 WebDAV 路径解析与 PROPFIND 响应，减少客户端列目录时的网盘 API 调用。", "true"),
		boolSpec(KeyFuseReadCacheEnabled, "performance", "FUSE 读缓存", "开启后 FUSE 读取过的文件块会写入本地磁盘，与元数据缓存无关。在「文件共享 → 本地挂载」页配置。", "false"),
		intSpec(KeyFuseReadCacheMaxGB, "performance", "FUSE 读缓存容量上限", "磁盘块缓存最大占用，在「文件共享 → 本地挂载」页配置。", "10", "GB", 1, 500),
		intSpec(KeyFuseReadCacheRetentionDays, "performance", "FUSE 读缓存保留天数", "超过该天数的缓存块会被删除，在「文件共享 → 本地挂载」页配置。", "7", "天", 1, 90),
		selectSpec(KeyFuseReadCacheEvictionPolicy, "performance", "FUSE 读缓存淘汰策略", "容量满时的淘汰方式，在「文件共享 → 本地挂载」页配置。", "lru", []Option{
			{Value: "lru", Label: "最近最少使用（LRU）"},
			{Value: "large_file", Label: "大文件优先"},
		}),
		boolSpec(KeyAuthActiveRefresh, "system", "智能主动认证刷新", "后台按 token 有效期预刷新、Cookie 健康检查；关闭后仅保留被动刷新。", "true"),
		boolSpec(KeyAccountShowProfile, "account_display", "显示账号信息", "在网盘账号卡片第二行显示昵称、手机号或邮箱；关闭后显示创建时间。", "true"),
		boolSpec(KeyAccountShowMembership, "account_display", "显示会员标签", "在网盘账号名称后显示该网盘返回的 VIP 或 SVIP 标签。", "true"),
		selectSpec(KeyLogLevel, "system", "日志级别", "控制控制台与落盘日志的最低级别；认证调度、刷新结果等默认可在 Info 查看。", "info", []Option{
			{Value: "debug", Label: "Debug（调试）"},
			{Value: "info", Label: "Info（常规）"},
			{Value: "warn", Label: "Warn（警告）"},
			{Value: "error", Label: "Error（错误）"},
		}),
		intSpec(KeyLogRetentionDays, "system", "日志保留天数", "按天落盘日志的保留期。自动清理与日志页手动清理都会按该天数删除更早的旧日志。", "30", "天", 1, 365),
		{Key: KeyEmbyEnabled, Type: TypeBool, Default: "false", Hidden: true},
		{Key: KeyEmbyProxyInstances, Type: TypeString, Default: "[]", Sensitive: true, Hidden: true},
		boolSpec(KeyFnosEnabled, "fnos", "启用飞牛影视反代", "开启后且填写反代端口时，MyPan 会启动飞牛影视反代服务。", "false"),
		stringSpec(KeyFnosName, "fnos", "飞牛影视配置名称", "飞牛影视反代配置的名称，仅用于界面区分。", "飞牛影视"),
		stringSpec(KeyFnosURL, "fnos", "飞牛影视地址", "飞牛影视服务地址，默认端口 8005，例如 http://192.168.1.10:8005。", ""),
		stringSpec(KeyFnosProxyPort, "fnos", "反代端口", "可留空。填写并启用后，MyPan 会在该端口启动飞牛影视反代服务。", ""),
		stringSpec(KeyFnosStrmPathMaps, "fnos", "飞牛 STRM 目录", "填写 Docker 中映射到 /app/strm 的左边路径。例：/vol1/.../MyPanGO:/app/strm → 填 /vol1/.../MyPanGO。两边相同可留空。", ""),
		{Key: KeyFnosDirectSTRMClients, Type: TypeString, Default: "Infuse", Hidden: true},
		stringSpec(KeyStrmToken, "strm", "STRM 播放令牌", "STRM 播放路径鉴权令牌，请在系统设置「API 秘钥」中管理。", ""),
		stringSpec(KeyStrmBaseURL, "strm", "STRM 基础地址", "生成本地 .strm 时使用的站点基址（例如 https://example.com）。留空时使用当前服务监听地址。", ""),
		boolSpec(KeyStrmSignatureEnabled, "strm", "启用 STRM 路径签名", "开启后 /api/strm/play 路径必须携带有效签名。", "false"),
		intSpec(KeyStrmDefaultScanInterval, "strm", "STRM 默认扫描间隔", "新建任务未指定扫描间隔时使用。", "360", "分钟", 1, 1440),
		stringSpec(KeyStrmDefaultExtensions, "strm", "默认同步文件类型", "STRM 任务未单独指定扩展名时使用，英文分号分隔。", "mp4;mkv;avi;mov;wmv;flv;ts;m2ts;mpg;mpeg;webm;m4v;iso;rmvb;mp3;flac;aac;wav;m4a"),
		boolSpec(KeyStrmISOFilenameEnabled, "strm", "ISO 使用 .iso.strm 文件名", "开启后网盘 .iso 文件生成“文件名.iso.strm”，方便 Infuse 识别 ISO。关闭时保持现有“文件名.strm”命名。", "false"),
		intSpec(KeyStrmMinFileSizeMB, "strm", "小文件过滤", "忽略小于该大小的媒体文件，0 表示不过滤。", "0", "MB", 0, 10240),
		stringSpec(KeyStrmConflictPolicy, "strm", "同名冲突策略", "同目录同名不同后缀时保留哪一个：size_desc / size_asc / name_asc。", "size_desc"),
		intSpec(KeyStrmTaskConcurrency, "strm", "STRM 任务并发", "同时运行的 STRM 扫描任务上限。", "3", "", 1, 10),
		stringSpec(KeyStrmMetadataExtensions, "strm", "元数据扩展名", "任务开启同步元数据时使用的扩展名，英文分号分隔。", "srt;ass;ssa;sub;sup;idx;vtt;nfo;jpg;jpeg;png;webp;bmp;gif"),
		intSpec(KeyStrmMetadataMaxSizeMB, "strm", "元数据大小上限", "同步元数据时忽略超过该大小的文件。", "10", "MB", 1, 1024),
		boolSpec(KeyStrmMetadataParentEnabled, "strm", "父目录元数据同步", "子目录有影片时，也同步父目录下的海报、nfo 等元数据。", "true"),
		boolSpec(KeyStrmTool115TreeEnabled, "strm", "115 网盘 STRM 增强（目录树清单模式）", "开启后 115Open 账号的 STRM 任务改用全量清单 + 增量对账方式执行，减少逐目录递归请求；配了分支的任务维持原逻辑。", "false"),
		selectSpec(KeyStrmMetadataSyncMode, "strm", "元数据同步策略", "local_primary=保留本地并从云端补缺；cloud_primary=本地目录与云端保持一致；bidirectional=本地与云端互相补缺。", "local_primary", []Option{
			{Value: "cloud_primary", Label: "网盘元数据为主"},
			{Value: "local_primary", Label: "本地元数据补缺"},
			{Value: "bidirectional", Label: "本地与云端互补"},
		}),
		stringSpec(KeyStrmScrapeWriteMode, "strm", "STRM 刮削写入策略", "missing_only=仅补缺；overwrite=覆盖已有 nfo/海报。", "missing_only"),
		{Key: KeyStrmScrapeScopes, Type: TypeString, Default: "{}", Hidden: true},

		// 全局代理。顺序即卡片上的行序（Snapshot 复用声明顺序）。
		boolSpec(KeyProxyEnabled, "proxy", "启用代理",
			"开启后，TMDB（目录整理 / STRM 刮削 / 分类整理）与 TG 频道抓取都经此代理出站。其它功能（网盘 API、播放串流）不走代理。", "false"),
		stringSpec(KeyProxyURL, "proxy", "代理地址",
			"HTTP/HTTPS 代理地址，例如 http://127.0.0.1:7890。暂不支持 socks5 —— 请填代理软件提供的 HTTP 端口。", ""),
		stringSpec(KeyProxyUsername, "proxy", "代理用户名", "代理认证用户名，无认证可留空。", ""),
		secretSpec(KeyProxyPassword, "proxy", "代理密码", "代理认证密码，无认证可留空。"),

		stringSpec(KeyMOTmdbAPIKey, "media_organize", "TMDB API Key", "The Movie Database API 密钥。", ""),
		stringSpec(KeyMOTmdbLanguage, "media_organize", "TMDB 搜索语言", "TMDB 搜索与详情语言，例如 zh-CN。", "zh-CN"),
		stringSpec(KeyMOTmdbAPIHost, "media_organize", "TMDB API 主域名", "自建反代时填写主域名，程序自动补 /3。", "https://api.themoviedb.org"),
		stringSpec(KeyMOTmdbImageHost, "media_organize", "TMDB 图片主域名", "自建反代时填写主域名，程序自动补 /t/p。", "https://image.tmdb.org"),
		intSpec(KeyMOAPIRequestIntervalMS, "media_organize", "API 额外补偿间隔", "网盘 API 请求之间的额外等待时间。", "300", "毫秒", 50, 10000),
		intSpec(KeyMOTmdbRequestIntervalMS, "media_organize", "TMDB 请求间隔", "两次 TMDB API 请求之间的最小间隔。", "250", "毫秒", 100, 5000),
		stringSpec(KeyMOFileExtensions, "media_organize", "媒体文件扩展名", "参与整理的媒体扩展名，英文分号分隔。", "mkv;mp4;avi;ts;mov;wmv;iso;m2ts;rmvb;flv;m4v;webm"),
		stringSpec(KeyMOMetadataExtensions, "media_organize", "元数据文件扩展名", "随媒体一起整理的元数据扩展名，英文分号分隔。", "nfo;ass;ssa;srt;sub;idx;sup;vtt;jpg;jpeg;png;webp;bmp"),
		stringSpec(KeyMOMediaTagOrder, "media_organize", "媒体信息标签排序", "重命名时媒体标签的排列顺序，JSON 数组字符串。", `["screen_size","video_codec","audio_codec","audio_channels"]`),
		boolSpec(KeyMOAlignMediaTags, "media_organize", "强迫症模式", "同后缀文件保持媒体信息标签一致。", "false"),
		intSpec(KeyMOMaxWorksPerRun, "media_organize", "每次最多整理作品数", "单次执行最多处理的作品数，0 表示不限制。", "50", "", 0, 10000),
		boolSpec(KeyMOOverwriteExisting, "media_organize", "同名冲突时覆盖", "目标位置已有同名文件时覆盖，默认跳过。", "false"),
		{
			// 番号规则是结构化数据（三个规则表），走独立的 /jav-rules 端点读写，
			// 和 mo_classification_config 一样只在注册表里占个位。
			Key:     KeyMOJavRules,
			Type:    TypeString,
			Default: "{}",
			Hidden:  true,
		},
		boolSpec(KeyMOJavDeleteSmall, "media_organize", "番号·清理小文件", "番号匹配时删除小于阈值且非元数据的文件。默认关闭，开启后请务必先看预览。", "false"),
		intSpec(KeyMOJavSmallFileMB, "media_organize", "番号·小文件阈值", "小于该大小的文件会被删除，单位 MB。", "300", "MB", 1, 102400),
		boolSpec(KeyMOJavCleanEmpty, "media_organize", "番号·清理空目录", "番号整理后删除源目录里已空的子目录。只删空目录，不影响有内容的目录。", "true"),
		intSpec(KeyMOJavMaxDirs, "media_organize", "番号·扫描目录上限", "单次扫描最多遍历的目录数，防止超大目录树把任务卡死。", "500", "", 10, 100000),
		stringSpec(KeyMOJavDeleteTypes, "media_organize", "番号·可删除的文件类型", "只有这些扩展名在小文件清理时会被删（英文分号分隔）；留空表示不限类型。", ""),
		stringSpec(KeyMOJavDeleteExclude, "media_organize", "番号·不删除的文件类型", "这些扩展名永远不会被小文件清理删掉（英文分号分隔）；留空表示不排除任何类型。", "nfo;ass;ssa;srt;sub;idx;sup;vtt;jpg;jpeg;png;webp;bmp"),
		{
			Key:     KeyAIOrganizeEnabled,
			Type:    TypeBool,
			Default: "false",
			Hidden:  true,
		},
		{
			Key:       KeyAIOrganizeInstances,
			Type:      TypeString,
			Default:   "[]",
			Sensitive: true,
			Hidden:    true,
		},
		{
			Key:     KeyAIOrganizeBaseURL,
			Type:    TypeString,
			Default: "https://api.deepseek.com",
			Hidden:  true,
		},
		{
			Key:       KeyAIOrganizeAPIKey,
			Type:      TypeString,
			Default:   "",
			Sensitive: true,
			Hidden:    true,
		},
		{
			Key:     KeyAIOrganizeModel,
			Type:    TypeString,
			Default: "deepseek-chat",
			Hidden:  true,
		},
		{
			Key:     KeyMOClassificationEnabled,
			Type:    TypeBool,
			Default: "false",
			Hidden:  true,
		},
		{
			Key:     KeyMOClassificationConfig,
			Type:    TypeString,
			Default: "",
			Hidden:  true,
		},
		{
			Key:         KeyOAuthServerURL,
			Type:        TypeString,
			Category:    "system",
			Label:       "OAuth 代理服务地址",
			Description: "添加账号时「自动获取 Token」经此服务转发。留空或无效地址将回落默认值。本地调试可填 http://127.0.0.1:8000。",
			Default:     domain.DefaultOAuthServerURL,
			normalize:   domain.NormalizeOAuthServerURL,
		},
		{
			Key:     KeyLogErrorAckAt,
			Type:    TypeString,
			Default: "",
			Hidden:  true,
		},
		{
			Key:     KeyAnnouncementReadVersion,
			Type:    TypeString,
			Default: "",
			Hidden:  true,
		},
		stringSpec(KeyAnnouncementURL, "announcement", "公告地址",
			"后台公告 JSON 的地址。程序会定期拉取该文件并缓存，用于后台的更新通告与「关于」弹窗。留空则使用内置默认地址。",
			announcement.DefaultURL),
		{
			Key:     KeyLocalUploadEnabled,
			Type:    TypeBool,
			Default: "false",
			Hidden:  true,
		},
		{
			Key:     KeyLocalUploadMappings,
			Type:    TypeString,
			Default: "[]",
			Hidden:  true,
		},
		{
			Key:     KeyCoverExtractEnabled,
			Type:    TypeBool,
			Default: "false",
			Hidden:  true,
		},
		{
			// 海报默认样式（JSON）：shape/height/panel_color/opacity/text_color/packaged
			Key:     KeyCoverExtractStyle,
			Type:    TypeString,
			Default: "",
			Hidden:  true,
		},
		{
			Key:     KeyQuarkTVEnabled,
			Type:    TypeBool,
			Default: "false",
			Hidden:  true,
		},
		{
			Key:     KeyQuarkTVPlayMode,
			Type:    TypeString,
			Default: "adaptive",
			Hidden:  true,
		},
		{
			Key:     KeyQuarkTVClientListMode,
			Type:    TypeString,
			Default: "proxy_list",
			Hidden:  true,
		},
		{
			Key:     KeyQuarkTVProxyClients,
			Type:    TypeString,
			Default: "vidhub",
			Hidden:  true,
		},

		// TG 影片订阅：全部 Hidden，由功能自己的配置弹窗读写。
		// tg_bot_auto_push 默认 false 是刻意的安全设计 —— 用户先开「观察模式」跑一段，
		// 确认匹配历史里的判定符合预期，再打开自动推送。回填历史帖时也靠它兜底。
		{Key: KeyTGBotEnabled, Type: TypeBool, Default: "false", Hidden: true},
		{Key: KeyTGBotAutoPush, Type: TypeBool, Default: "false", Hidden: true},
		{Key: KeyTGBotDefaultAccountID, Type: TypeString, Default: "0", Hidden: true},
		{Key: KeyTGBotDefaultParentID, Type: TypeString, Default: "", Hidden: true},
		{Key: KeyTGBotDefaultPath, Type: TypeString, Default: "", Hidden: true},
		{Key: KeyTGBotProfileID, Type: TypeString, Default: "0", Hidden: true},
		{Key: KeyTGBotCollectWindowMin, Type: TypeInt, Default: "5", Min: intp(0), Max: intp(1440), Hidden: true},
		{Key: KeyTGBotMaxPushPerHour, Type: TypeInt, Default: "20", Min: intp(1), Max: intp(500), Hidden: true},
		{Key: KeyTGBotLastPollAt, Type: TypeString, Default: "", Hidden: true},
		{Key: KeyTGBotStatus, Type: TypeString, Default: "unknown", Hidden: true},
		{Key: KeyTGBotStatusMessage, Type: TypeString, Default: "", Hidden: true},

		// 网页预览抓取。t.me/s/ 不是给程序用的接口，没有公开配额，
		// 默认值刻意保守：10 分钟一轮、每请求间隔 2 秒。
		{Key: KeyTGPreviewPollIntervalSec, Type: TypeInt, Default: "600", Min: intp(30), Max: intp(86400), Hidden: true},
		{Key: KeyTGPreviewBackfillPages, Type: TypeInt, Default: "1", Min: intp(0), Max: intp(20), Hidden: true},
		{Key: KeyTGPreviewRequestGapMs, Type: TypeInt, Default: "2000", Min: intp(0), Max: intp(30000), Hidden: true},
		{Key: KeyTGPreviewTimeoutSec, Type: TypeInt, Default: "20", Min: intp(5), Max: intp(120), Hidden: true},
		// 一次性播种的标记与「没加成功的清单」，纯内部状态。
		{Key: KeyTGRecommendedSeeded, Type: TypeBool, Default: "false", Hidden: true},
		{Key: KeyTGRecommendedPending, Type: TypeString, Default: "", Hidden: true},
	}
}

// categories 返回有序分组定义；只保留当前实际用到的分组。
func categories() []Category {
	return []Category{
		{ID: "system", Label: "系统设置"},
		{ID: "account_display", Label: "网盘账号显示"},
		{ID: "announcement", Label: "后台公告"},
		{ID: "proxy", Label: "网络代理"},
		{ID: "performance", Label: "性能设置"},
		{ID: "strm", Label: "STRM 设置"},
		{ID: "media_organize", Label: "媒体整理设置"},
	}
}
