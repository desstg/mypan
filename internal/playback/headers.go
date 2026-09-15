package playback

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type streamHeaders struct {
	contentType        string
	etag               string
	contentDisposition string
	contentLength      int64
	contentRange       string
	acceptRanges       bool
	modTime            time.Time
}

func writeStreamHeaders(w http.ResponseWriter, h streamHeaders) {
	w.Header().Set("Content-Type", h.contentType)
	w.Header().Set("ETag", h.etag)
	if h.contentDisposition != "" {
		w.Header().Set("Content-Disposition", h.contentDisposition)
	}
	if h.acceptRanges {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if h.contentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(h.contentLength, 10))
	}
	if h.contentRange != "" {
		w.Header().Set("Content-Range", h.contentRange)
	}
	if !h.modTime.IsZero() {
		w.Header().Set("Last-Modified", h.modTime.UTC().Format(http.TimeFormat))
	}
}

func contentDisposition(name string, inline bool) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	return fmt.Sprintf(`%s; filename*=UTF-8''%s`, kind, url.PathEscape(name))
}

func stableETag(id string, size int64, mod time.Time) string {
	modPart := ""
	if !mod.IsZero() {
		modPart = fmt.Sprintf("-%d", mod.Unix())
	}
	return fmt.Sprintf(`"%s-%d%s"`, id, size, modPart)
}
