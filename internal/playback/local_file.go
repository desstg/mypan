package playback

import (
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

func (s *Service) serveLocalFile(w http.ResponseWriter, r *http.Request, res Resolved, fileName, localPath string, intent Intent) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size <= 0 && res.File.Size > 0 {
		size = res.File.Size
	}
	modTime := info.ModTime()
	if !res.File.ModTime.IsZero() {
		modTime = res.File.ModTime
	}

	name := strings.TrimSpace(fileName)
	if name == "" {
		name = info.Name()
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	inline := intent.Inline && isInlinePreviewType(ctype)
	w.Header().Set("Content-Disposition", contentDisposition(name, inline))

	serveContent(w, r, f, res.File.ID, name, modTime, size)
	return nil
}
