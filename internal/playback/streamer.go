package playback

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
)

func (s *Service) serveStream(w http.ResponseWriter, r *http.Request, req Request, res Resolved, fileName, ua string, intent Intent) error {
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = strings.TrimSpace(res.File.Name)
	}
	if name == "" {
		name = strings.TrimSpace(res.Link.FileName)
	}
	if name == "" {
		name = "download"
	}

	if localPath := strings.TrimSpace(res.Link.LocalPath); localPath != "" {
		return s.serveLocalFile(w, r, res, name, localPath, intent)
	}

	lh := &linkHolder{
		svc:            s,
		link:           res.Link,
		accountID:      req.AccountID,
		fileID:         req.FileID,
		ua:             ua,
		playback:       intent.allowsPlaybackResolve(),
		skipRangeLimit: intent.SkipRangeLimit,
		refreshLeft:    2,
	}
	partSize := res.Link.ChunkSize
	if partSize <= 0 {
		partSize = defaultPartSize
	}
	size := res.File.Size
	if size <= 0 && res.Link.Size > 0 {
		size = res.Link.Size
	}
	if size <= 0 {
		if probed, err := s.probeSizeViaRange0(r.Context(), lh); err == nil && probed > 0 {
			size = probed
		}
	}

	modTime := res.File.ModTime
	etag := stableETag(res.File.ID, size, modTime)
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	inline := intent.Inline && isInlinePreviewType(ctype)
	disp := contentDisposition(name, inline)

	if r.Method == http.MethodHead {
		writeStreamHeaders(w, streamHeaders{
			contentType:        ctype,
			etag:               etag,
			contentDisposition: disp,
			contentLength:      size,
			acceptRanges:       true,
			modTime:            modTime,
		})
		w.WriteHeader(http.StatusOK)
		return nil
	}

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag && strings.TrimSpace(r.Header.Get("Range")) == "" {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return nil
	}

	rangeHdr := strings.TrimSpace(r.Header.Get("Range"))
	if rangeHdr != "" {
		if size > 0 {
			start, end, perr := parseSingleRange(rangeHdr, size)
			if perr != nil {
				if size > 0 {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				}
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return nil
			}
			writeStreamHeaders(w, streamHeaders{
				contentType:        ctype,
				etag:               etag,
				contentDisposition: disp,
				contentLength:      end - start + 1,
				contentRange:       fmt.Sprintf("bytes %d-%d/%d", start, end, size),
				acceptRanges:       true,
				modTime:            modTime,
			})
			w.WriteHeader(http.StatusPartialContent)
			return s.streamUpstreamBody(r.Context(), w, lh, start, end, partSize)
		}
		return s.passthrough(w, r, req, res, ua)
	}

	if size > 0 {
		writeStreamHeaders(w, streamHeaders{
			contentType:        ctype,
			etag:               etag,
			contentDisposition: disp,
			contentLength:      size,
			acceptRanges:       true,
			modTime:            modTime,
		})
		w.WriteHeader(http.StatusOK)
		return s.streamUpstreamBody(r.Context(), w, lh, 0, size-1, partSize)
	}

	seeker := s.newHTTPSeeker(r.Context(), req.AccountID, req.FileID, ua, res)
	defer seeker.Close()
	serveContent(w, r, seeker, res.File.ID, name, modTime, size)
	return nil
}

// SVG 可能含脚本，不 inline
func isInlinePreviewType(contentType string) bool {
	return strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		contentType == "application/vnd.apple.mpegurl" ||
		(strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml")
}
