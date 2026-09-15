package playback

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"time"
)

func serveContent(w http.ResponseWriter, r *http.Request, content io.ReadSeeker, fileID, name string, modTime time.Time, size int64) {
	etag := stableETag(fileID, size, modTime)

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag && r.Method != http.MethodHead {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("ETag", etag)
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	rangeHdr := r.Header.Get("Range")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if rangeHdr != "" && size > 0 {
		start, end, err := parseSingleRange(rangeHdr, size)
		if err == nil {
			if _, err := content.Seek(start, io.SeekStart); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			sendLen := end - start + 1
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(sendLen, 10))
			w.WriteHeader(http.StatusPartialContent)
			copyN(w, content, sendLen)
			return
		}
	}

	if _, err := content.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	if size > 0 {
		copyN(w, content, size)
	} else {
		_, _ = io.Copy(w, content)
	}
}

func copyN(w io.Writer, r io.Reader, n int64) {
	bufp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufp)
	buf := *bufp
	remain := n
	for remain > 0 {
		toRead := int64(len(buf))
		if toRead > remain {
			toRead = remain
		}
		nr, err := r.Read(buf[:toRead])
		if nr > 0 {
			nw, werr := w.Write(buf[:nr])
			if nw > 0 {
				remain -= int64(nw)
			}
			if werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
