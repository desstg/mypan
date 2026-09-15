package api

import (
	"bufio"
	"net"
	"net/http"
)

type commitWriter struct {
	http.ResponseWriter
	committed bool
	request   *http.Request
}

func (w *commitWriter) WriteHeader(code int) {
	if w.committed {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *commitWriter) Write(b []byte) (int, error) {
	if !w.committed {
		w.committed = true
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *commitWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *commitWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func trackResponseCommit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&commitWriter{ResponseWriter: w, request: r}, r)
	})
}

func responseCommitted(w http.ResponseWriter) bool {
	if cw, ok := w.(*commitWriter); ok {
		return cw.committed
	}
	return false
}
