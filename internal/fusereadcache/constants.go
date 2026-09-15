package fusereadcache

const (
	SubdirName = "fuse_read_cache"

	BlockSize = 4 * 1024 * 1024

	MinMaxGB         = 1
	MaxMaxGB         = 500
	DefaultMaxGB     = 10
	MinRetentionDays = 1
	MaxRetentionDays = 90
	DefaultRetention = 7

	PolicyLRU       = "lru"
	PolicyLargeFile = "large_file"
	DefaultPolicy   = PolicyLRU
)
