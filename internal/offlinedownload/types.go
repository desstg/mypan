package offlinedownload

import (
	"strings"

	"litepan/internal/driver"
)

const (
	SourceURL     = "url"
	SourceTorrent = "bt"
)

const (
	ProviderNative  = "native"
	ProviderBuiltin = "builtin"
)

const (
	ExecutorURLHTTP   = "url_http"
	ExecutorURLMagnet = "url_magnet"
)

const (
	PhaseDownloading = "downloading"
	PhaseVerifying   = "verifying"
	PhaseHandoff     = "handoff"
	PhaseDone        = "done"
)

type MagnetDiagnostics struct {
	Stage                 string  `json:"stage,omitempty"`
	TrackerCount          int     `json:"tracker_count,omitempty"`
	DHTNodes              int     `json:"dht_nodes,omitempty"`
	DHTGoodNodes          int     `json:"dht_good_nodes,omitempty"`
	DHTOutstandingQueries int     `json:"dht_outstanding_queries,omitempty"`
	ActivePeers           int     `json:"active_peers,omitempty"`
	PendingPeers          int     `json:"pending_peers,omitempty"`
	TotalPeers            int     `json:"total_peers,omitempty"`
	ConnectedSeeders      int     `json:"connected_seeders,omitempty"`
	HalfOpenPeers         int     `json:"half_open_peers,omitempty"`
	MetadataReady         bool    `json:"metadata_ready,omitempty"`
	LastSampleAt          float64 `json:"last_sample_at,omitempty"`
}

type Capabilities struct {
	Supported         bool     `json:"supported"`
	SupportsURLs      bool     `json:"supports_urls"`
	SupportsBatchURLs bool     `json:"supports_batch_urls"`
	SupportsTorrent   bool     `json:"supports_torrent"`
	URLSchemes        []string `json:"url_schemes"`
	RootTargetAllowed bool     `json:"root_target_allowed"`
	RemoteDelete      bool     `json:"remote_delete"`
	// DriverType 是这个账号的驱动类型。公共层做「哪种 scheme 只有网页登录态能走」
	// 这类判断时要用它 —— 光看 scheme 白名单看不出「声明了但要走另一份凭据」。
	DriverType string `json:"driver_type"`
	// WebLoginReady 报告「需要网页登录态的那部分能力」现在能不能用。
	// 不需要附加凭据的驱动一律 true；123 要看账号配没配网页账号密码。
	WebLoginReady          bool     `json:"web_login_ready"`
	BuiltinEnabled         bool     `json:"builtin_enabled"`
	BuiltinSupportsURLs    bool     `json:"builtin_supports_urls"`
	BuiltinURLSchemes      []string `json:"builtin_url_schemes"`
	BuiltinSupportsTorrent bool     `json:"builtin_supports_torrent"`
}

type Task struct {
	TaskID            string             `json:"task_id"`
	AccountID         int64              `json:"account_id"`
	AccountName       string             `json:"account_name"`
	DriverType        string             `json:"driver_type"`
	ProviderKind      string             `json:"provider_kind,omitempty"`
	ExecutorType      string             `json:"executor_type,omitempty"`
	SourceKind        string             `json:"source_kind"`
	Source            string             `json:"source"`
	Name              string             `json:"name"`
	ProviderTaskID    string             `json:"provider_task_id,omitempty"`
	InfoHash          string             `json:"info_hash,omitempty"`
	TargetParentID    string             `json:"target_parent_id"`
	TargetDisplayPath string             `json:"target_display_path"`
	Status            string             `json:"status"`
	Phase             string             `json:"phase,omitempty"`
	Progress          int                `json:"progress"`
	Size              int64              `json:"size"`
	DownloadedBytes   int64              `json:"downloaded_bytes,omitempty"`
	SpeedBytes        float64            `json:"speed_bytes,omitempty"`
	LocalTempPath     string             `json:"local_temp_path,omitempty"`
	MagnetDiagnostics *MagnetDiagnostics `json:"magnet_diagnostics,omitempty"`
	FileID            string             `json:"file_id,omitempty"`
	Message           string             `json:"message"`
	Error             string             `json:"error,omitempty"`
	RemoteDelete      bool               `json:"remote_delete"`
	CreatedAt         float64            `json:"created_at"`
	UpdatedAt         float64            `json:"updated_at"`
}

type AddURLParams struct {
	AccountID         int64
	ProviderKind      string
	URLs              []string
	FileName          string
	TargetParentID    string
	TargetDisplayPath string
	// Source 标明这次提交是从哪一页发起的（''=文件管理那一侧，其余见
	// internal/searchtransfer 的 Source* 常量）。**只用来写转存记录**，
	// 不参与任何下载决策。转存记录是「后台搜索转存页」的功能，
	// 文件管理里手动建的离线任务不该混进去。
	Source string
	// RecordSink 由调用方注入：提交成功后把每个新建的任务交给它留档。
	// 为 nil 表示不留档（默认）。
	//
	// 用回调而不是让 offlinedownload 自己写转存记录：那张表是「搜索转存」页的
	// 概念，离线下载服务不该知道它 —— 否则以后加一个 RecordKind 都要改两个包。
	RecordSink func(tasks []Task)
}

type TorrentPreparation struct {
	PreparationID string                      `json:"preparation_id"`
	TorrentName   string                      `json:"torrent_name"`
	TotalSize     int64                       `json:"total_size"`
	Files         []driver.OfflineTorrentFile `json:"files"`
	ExpiresAt     float64                     `json:"expires_at"`
}

type AddTorrentParams struct {
	AccountID         int64
	PreparationID     string
	Wanted            []int
	TargetParentID    string
	TargetDisplayPath string
	SavePath          string
}

type BatchDeleteResult struct {
	DeletedTaskIDs []string          `json:"deleted_task_ids"`
	FailedTaskIDs  []string          `json:"failed_task_ids"`
	FailedMessages map[string]string `json:"failed_messages"`
}

// NativeSchemeUsable 报告「网盘原生离线通道」在**这个账号**上能不能用这个 scheme。
//
// 只看 URLSchemes 是不够的：有的驱动声明支持某个 scheme，而实现那条路要一份
// 额外的网页登录态（123 的磁力就是这样）。两个调用方（tgsubscribe 的推送决策、
// searchtransfer 的界面能力表）都靠这个函数，判据只此一份。
func (c Capabilities) NativeSchemeUsable(scheme string) bool {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	ok := false
	for _, s := range c.URLSchemes {
		if strings.EqualFold(strings.TrimSpace(s), scheme) {
			ok = true
			break
		}
	}
	if !ok || !c.SupportsURLs {
		return false
	}
	// 需要网页登录态、而这个账号没配凭据：声明了也用不了。
	if c.DriverType == "123_open" && scheme == "magnet" && !c.WebLoginReady {
		return false
	}
	return true
}

// SchemeDeclared 报告 URLSchemes 里有没有这个 scheme（**不看**凭据是否齐备）。
func (c Capabilities) SchemeDeclared(scheme string) bool {
	if !c.SupportsURLs {
		return false
	}
	for _, s := range c.URLSchemes {
		if strings.EqualFold(strings.TrimSpace(s), scheme) {
			return true
		}
	}
	return false
}

// BuiltinSchemeUsable 报告内置下载器能不能吃这个 scheme。
func (c Capabilities) BuiltinSchemeUsable(scheme string) bool {
	if !c.BuiltinEnabled {
		return false
	}
	for _, s := range c.BuiltinURLSchemes {
		if strings.EqualFold(strings.TrimSpace(s), scheme) {
			return true
		}
	}
	return false
}
