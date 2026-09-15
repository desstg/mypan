package domain

import (
	"net/http"
	"time"
)

type HashType string

const (
	HashSHA1 HashType = "sha1"
	HashMD5  HashType = "md5"
)

type IDKind uint8

const (
	IDStable IDKind = iota
	IDPath
)

type FileItem struct {
	ID      string
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
	Hash    map[HashType]string
	Thumb   string
	IDKind  IDKind
}

type DownloadMode uint8

const (
	DownloadRedirect DownloadMode = iota
	DownloadProxy
)

type UpstreamTransportPolicy uint8

const (
	UpstreamTransportDefault UpstreamTransportPolicy = iota
	UpstreamTransportForceHTTP2
)

type DownloadInfo struct {
	URL             string
	Headers         http.Header
	Mode            DownloadMode
	Expiration      time.Duration
	Concurrency     int
	ChunkSize       int64
	ForceProxy      bool
	TransportPolicy UpstreamTransportPolicy
	// LocalPath 非空时表示内容在本机文件，播放层直接读盘，无需上游 HTTP。
	LocalPath string

	Size     int64
	FileName string
}
