package playback

import (
	"crypto/tls"
	"net/http"
	"time"

	"litepan/internal/domain"
)

func newUpstreamTransport(http2 bool) *http.Transport {
	tr := &http.Transport{
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: 120 * time.Second,
	}
	if !http2 {
		tr.ForceAttemptHTTP2 = false
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	} else {
		tr.ForceAttemptHTTP2 = true
	}
	return tr
}

func (s *Service) upstreamClient(link domain.DownloadInfo) *http.Client {
	if link.TransportPolicy == domain.UpstreamTransportForceHTTP2 {
		return s.clientH2
	}
	if benchHTTP2Enabled() {
		return s.clientH2
	}
	return s.clientHTTP1
}
