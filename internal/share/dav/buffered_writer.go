package dav

import "net/http"

type bufferedResponseWriter struct {
	statusCode int
	body       []byte
	header     http.Header
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{}
}

func (w *bufferedResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}

func (w *bufferedResponseWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
}

func (w *bufferedResponseWriter) writeTo(rw http.ResponseWriter) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	dst := rw.Header()
	for k, vs := range w.header {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	rw.WriteHeader(w.statusCode)
	if len(w.body) == 0 {
		return 0, nil
	}
	return rw.Write(w.body)
}
