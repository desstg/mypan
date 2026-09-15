package playback

import (
	"fmt"
	"net/http"
	"strings"
)

func writeRedirect(w http.ResponseWriter, r *http.Request, res Resolved, intent Intent) {
	writeDynamicRedirectHeaders(w.Header())
	url := res.Link.URL
	rangeHdr := strings.TrimSpace(r.Header.Get("Range"))
	if intent.WebDAV && rangeHdr != "" && res.File.Size > 0 {
		start, end, err := parseSingleRange(rangeHdr, res.File.Size)
		if err == nil {
			w.Header().Set("Location", url)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, res.File.Size))
			w.WriteHeader(http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// 禁止缓存 302，避免过期直链
func writeDynamicRedirectHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
	header.Set("Referrer-Policy", "no-referrer")
}
