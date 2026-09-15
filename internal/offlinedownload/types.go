package offlinedownload

import "litepan/internal/driver"

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
	Supported              bool     `json:"supported"`
	SupportsURLs           bool     `json:"supports_urls"`
	SupportsBatchURLs      bool     `json:"supports_batch_urls"`
	SupportsTorrent        bool     `json:"supports_torrent"`
	URLSchemes             []string `json:"url_schemes"`
	RootTargetAllowed      bool     `json:"root_target_allowed"`
	RemoteDelete           bool     `json:"remote_delete"`
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
