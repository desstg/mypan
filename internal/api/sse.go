package api

import (
	"fmt"
	"net/http"
	"time"

	"litepan/internal/domain"
)

const defaultSSEPingInterval = 15 * time.Second

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, domain.Errorf(domain.CodeInternal, "不支持 SSE")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return &sseWriter{w: w, flusher: flusher}, nil
}

func (s *sseWriter) writeEvent(event, data string) {
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	s.flusher.Flush()
}

func streamSSEMessages(r *http.Request, s *sseWriter, eventName, initial string, ch <-chan string) {
	if initial != "" {
		s.writeEvent(eventName, initial)
	}
	streamSSELoop(r, s, eventName, ch)
}

func streamSSEByteMessages(r *http.Request, s *sseWriter, eventName string, initial []byte, ch <-chan []byte) {
	if len(initial) > 0 {
		s.writeEvent(eventName, string(initial))
	}
	streamSSELoop(r, s, eventName, ch)
}

func streamSSELoop[T ~string | ~[]byte](r *http.Request, s *sseWriter, eventName string, ch <-chan T) {
	ticker := time.NewTicker(defaultSSEPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-ch:
			if !ok {
				return
			}
			s.writeEvent(eventName, string(payload))
		case <-ticker.C:
			s.writeEvent("ping", "{}")
		}
	}
}
