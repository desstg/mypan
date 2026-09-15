package playback

import (
	"time"

	"litepan/internal/domain"
)

const (
	defaultLinkTTL  = 5 * time.Minute
	defaultPartSize = 8 << 20
)

type Resolved struct {
	File domain.FileItem
	Link domain.DownloadInfo
	Mode domain.DownloadMode
}
