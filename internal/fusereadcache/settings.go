package fusereadcache

import (
	"context"
	"strconv"
	"strings"

	"litepan/internal/settings"
)

type Config struct {
	Enabled        bool
	MaxGB          int
	MaxBytes       int64
	RetentionDays  int
	EvictionPolicy string
}

func LoadConfig(ctx context.Context, svc *settings.Service) Config {
	if svc == nil {
		return Config{
			MaxGB:          DefaultMaxGB,
			MaxBytes:       int64(DefaultMaxGB) * 1024 * 1024 * 1024,
			RetentionDays:  DefaultRetention,
			EvictionPolicy: DefaultPolicy,
		}
	}
	maxGB := svc.Int(settings.KeyFuseReadCacheMaxGB)
	if maxGB < MinMaxGB {
		maxGB = DefaultMaxGB
	}
	if maxGB > MaxMaxGB {
		maxGB = MaxMaxGB
	}
	retention := svc.Int(settings.KeyFuseReadCacheRetentionDays)
	if retention < MinRetentionDays {
		retention = DefaultRetention
	}
	if retention > MaxRetentionDays {
		retention = MaxRetentionDays
	}
	policy := strings.TrimSpace(svc.String(settings.KeyFuseReadCacheEvictionPolicy))
	if policy != PolicyLargeFile {
		policy = PolicyLRU
	}
	return Config{
		Enabled:        svc.Bool(settings.KeyFuseReadCacheEnabled),
		MaxGB:          maxGB,
		MaxBytes:       int64(maxGB) * 1024 * 1024 * 1024,
		RetentionDays:  retention,
		EvictionPolicy: policy,
	}
}

func ConfigPatch(enabled bool, maxGB, retentionDays int, policy string) map[string]string {
	if policy != PolicyLargeFile {
		policy = PolicyLRU
	}
	if maxGB < MinMaxGB {
		maxGB = MinMaxGB
	}
	if maxGB > MaxMaxGB {
		maxGB = MaxMaxGB
	}
	if retentionDays < MinRetentionDays {
		retentionDays = MinRetentionDays
	}
	if retentionDays > MaxRetentionDays {
		retentionDays = MaxRetentionDays
	}
	enabledVal := "false"
	if enabled {
		enabledVal = "true"
	}
	return map[string]string{
		settings.KeyFuseReadCacheEnabled:        enabledVal,
		settings.KeyFuseReadCacheMaxGB:          strconv.Itoa(maxGB),
		settings.KeyFuseReadCacheRetentionDays:  strconv.Itoa(retentionDays),
		settings.KeyFuseReadCacheEvictionPolicy: policy,
	}
}
