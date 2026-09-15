package logx

type Entry struct {
	Timestamp  string         `json:"timestamp"`
	Level      int            `json:"level"`
	Module     string         `json:"module"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	AccountID  any            `json:"account_id,omitempty"`
	DriverName any            `json:"driver_name,omitempty"`
}

// QueryFilter 是日志检索条件。
type QueryFilter struct {
	Level     *int
	MinLevel  *int
	Module    string
	StartTime string
	EndTime   string
	Keyword   string
	Limit     int
	Offset    int
}

// Stats 是日志统计摘要。
type Stats struct {
	Total                      int            `json:"total"`
	ByLevel                    map[string]int `json:"by_level"`
	ByModule                   map[string]int `json:"by_module"`
	RecentErrors               int            `json:"recent_errors"`
	RecentErrorsTotal          int            `json:"recent_errors_total"`
	RecentUnacknowledgedErrors int            `json:"recent_unacknowledged_errors"`
	LastRecentErrorAt          string         `json:"last_recent_error_at,omitempty"`
	LastAcknowledgedErrorAt    string         `json:"last_acknowledged_error_at,omitempty"`
}
