package driver

import (
	"context"
	"time"

	"litepan/internal/domain"
)

// AuthType 决定该驱动的认证刷新策略。
type AuthType string

const (
	AuthNone   AuthType = "none"
	AuthToken  AuthType = "token"
	AuthCookie AuthType = "cookie"
)

// Config 是驱动的静态元信息，决定公共层对它的处理方式。
type Config struct {
	Name        string   // 唯一标识，如 "localfs"/"123_open"
	DisplayName string   // 前端展示名
	Description string   // 前端驱动选择说明
	CardTags    []string // 驱动卡片展示标签；未声明则前端不显示胶囊
	SortOrder   int      // 驱动选择排序，0 表示使用默认靠后顺序
	AuthLabel   string   // 前端展示的认证方式标签
	CardColor   string   // 前端卡片色
	CardLogo    string   // 前端卡片 logo 路径，如 /logos/123.png
	DefaultRoot string   // 默认根目录 ID
	AuthType    AuthType // 认证类型
	// OAuthName 是需要代理续期的驱动在统一 OAuth 服务中的注册名。
	OAuthName string
	// 主动刷新调度参数（AuthToken 型驱动填写 TokenLifetime/RefreshAdvance）。
	TokenLifetime          time.Duration
	RefreshAdvance         time.Duration
	HealthCheckInterval    time.Duration // AuthCookie 型驱动填写
	ProvideHashes          []string      // 跨盘秒传：源盘可提供的指纹类型（sha1/md5）
	RapidUploadHashes      []string      // 跨盘秒传：目标盘支持的指纹秒传类型
	UploadConflictPolicies []string      // 跨盘秒传/上传：前端可选冲突策略
	// QRDevices 是扫码登录时可选设备来源；空表示扫码界面不提供切换。
	QRDevices []FieldOption
	// QRDeviceField 是 Addition 中保存设备来源的 JSON 字段名，与 QRDevices 配套。
	QRDeviceField string
	// InternalExperimental 为内部实验性驱动：默认不展示在前端驱动列表，需解锁开发模式后可见。
	InternalExperimental   bool
	SupportsAccountProfile bool
}

// Meta 是所有驱动必须实现的元信息与生命周期接口。
type Meta interface {
	Config() Config
	GetAddition() any // 配置结构指针，用于反射生成表单与 JSON 反序列化
	Init(ctx context.Context) error
	Drop(ctx context.Context) error
	Ping(ctx context.Context) error
}

// Lister 提供目录列举能力，是驱动的基础能力。
type Lister interface {
	ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error)
}

// FullListEntry 是清单模式（cur=0 全量列举）返回的远端文件条目。
type FullListEntry struct {
	FileID   string // 文件 ID
	ParentID string // 父目录 ID（pid）
	Name     string // 文件名
	Size     int64  // 字节大小
	Sha1     string // 文件 SHA-1（可能为空）
	PickCode string // 115 提取码（可能为空）
	MTime    int64  // 修改/上传时间（Unix 秒，可能为 0）
}

// FullListLister 可选：一次拉取 rootID 下全部文件（含所有子孙目录）。
// STRM 增强扫描用它替代逐目录递归，减少接口请求量。
type FullListLister interface {
	ListAllFiles(ctx context.Context, rootID string) ([]FullListEntry, error)
	// ResolveDirPath 返回目录的完整远端路径（以 / 分隔、不含末尾斜杠，根为 ""）。
	// 清单条目只带 pid，需要它把 pid 翻译成路径；实现方应尽量使用自己的内存缓存。
	ResolveDirPath(ctx context.Context, dirID string) (string, error)
}

// AccountProfileProvider 可选：返回账号昵称、会员与容量资料。
// 资料仅在存储管理页按天后台刷新，不参与文件访问链路。
type AccountProfileProvider interface {
	GetAccountProfile(ctx context.Context) (*domain.AccountProfile, error)
}

// InfoGetter 可选：单文件信息。
type InfoGetter interface {
	GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error)
}

// DownloadRequest 是驱动解析直链时的输入。
type DownloadRequest struct {
	FileID string
	UA     string
}

// Downloader 可选：解析下载/播放直链。
type Downloader interface {
	ResolveDownload(ctx context.Context, req DownloadRequest) (*domain.DownloadInfo, error)
}

type Deleter interface {
	DeleteFiles(ctx context.Context, fileIDs []string) error
}

type Mover interface {
	MoveFiles(ctx context.Context, fileIDs []string, targetParentID, sourceParentID string) error
}

type Copier interface {
	CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error
}

type Renamer interface {
	RenameFile(ctx context.Context, fileID, newName string) error
}

type FolderCreator interface {
	CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error)
}

// OAuthConsumer 经统一 OAuth 代理刷新令牌的驱动实现。
type OAuthConsumer interface {
	SetOAuthServer(baseURL string)
}

// AuthCredentialConsumer 运行前注入 token/cookie。
type AuthCredentialConsumer interface {
	SetAuthCredentials(creds domain.AuthCredentials)
}

// AuthPersistFunc 驱动自行刷新成功后回写 account_auth_states 的回调。
type AuthPersistFunc func(ctx context.Context, creds domain.AuthCredentials) error

// AuthPersistConsumer 由会在运行期更新令牌的驱动实现（如 OAuth 刷新）。
type AuthPersistConsumer interface {
	SetAuthPersister(fn AuthPersistFunc)
}

// ConnectionErrorExplainer 可选：自定义连通性错误文案。
type ConnectionErrorExplainer interface {
	ExplainConnectionError(technical string, saving bool) string
}

// RequestIntervalConsumer 可选：账号级 API 请求间隔。
type RequestIntervalConsumer interface {
	SetRequestIntervalGate(gate RequestIntervalGate)
}

// Driver 最小契约：元信息 + 列目录；其余能力走可选接口。
type Driver interface {
	Meta
	Lister
}
