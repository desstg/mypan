package baiduopen

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

type listResp struct {
	List []fileEntry `json:"list"`
}

type metasResp struct {
	List []fileEntry `json:"list"`
}

type createResp struct {
	FsID int64  `json:"fs_id"`
	Path string `json:"path"`
}

type fileEntry struct {
	FsID           json.Number       `json:"fs_id"`
	Path           string            `json:"path"`
	ServerFilename string            `json:"server_filename"`
	Filename       string            `json:"filename"`
	IsDir          int               `json:"isdir"`
	Size           json.Number       `json:"size"`
	ServerMTime    json.Number       `json:"server_mtime"`
	LocalMTime     json.Number       `json:"local_mtime"`
	ServerCTime    json.Number       `json:"server_ctime"`
	LocalCTime     json.Number       `json:"local_ctime"`
	MD5            string            `json:"md5"`
	Category       int               `json:"category"`
	DLink          string            `json:"dlink"`
	Thumbs         map[string]string `json:"thumbs"`
	DirEmpty       int               `json:"dir_empty"`
}

func (e fileEntry) itemID() string {
	if path := strings.TrimSpace(e.Path); path != "" {
		return path
	}
	if id := strings.TrimSpace(e.FsID.String()); id != "" {
		return id
	}
	return e.entryName()
}

func (e fileEntry) entryName() string {
	for _, s := range []string{e.ServerFilename, e.Filename} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	if p := strings.TrimRight(strings.TrimSpace(e.Path), "/"); p != "" {
		if idx := strings.LastIndex(p, "/"); idx >= 0 {
			return p[idx+1:]
		}
		return p
	}
	return "unknown"
}

func (e fileEntry) entrySize() int64 {
	if s := strings.TrimSpace(e.Size.String()); s != "" {
		if v, err := e.Size.Int64(); err == nil {
			return v
		}
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

func (e fileEntry) modTime() time.Time {
	for _, n := range []json.Number{e.ServerMTime, e.LocalMTime, e.ServerCTime, e.LocalCTime} {
		if s := strings.TrimSpace(n.String()); s != "" && s != "0" {
			if v, err := n.Int64(); err == nil && v > 0 {
				return time.Unix(v, 0)
			}
		}
	}
	return time.Time{}
}

func fileToItem(e fileEntry) domain.FileItem {
	item := domain.FileItem{
		ID:     e.itemID(),
		Name:   e.entryName(),
		Size:   e.entrySize(),
		IsDir:  e.IsDir == 1,
		IDKind: domain.IDPath,
	}
	if md5 := normalizeMD5(e.MD5); md5 != "" {
		item.Hash = map[domain.HashType]string{domain.HashMD5: md5}
	}
	if thumb := pickThumb(e.Thumbs); thumb != "" {
		item.Thumb = thumb
	}
	if ts := e.modTime(); !ts.IsZero() {
		item.ModTime = ts
	}
	return item
}

func normalizeMD5(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	if len(text) != 32 {
		return ""
	}
	for _, ch := range text {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return text
}

func pickThumb(thumbs map[string]string) string {
	for _, key := range []string{"url3", "url2", "url1", "icon"} {
		if v := strings.TrimSpace(thumbs[key]); v != "" {
			return v
		}
	}
	for _, v := range thumbs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
