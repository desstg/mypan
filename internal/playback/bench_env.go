package playback

import "os"

func benchHTTP2Enabled() bool {
	return os.Getenv("LITEPAN_BENCH_HTTP2") == "1"
}

func benchUpstreamHeaders() bool {
	return os.Getenv("LITEPAN_BENCH_UPSTREAM") == "1"
}

func benchForwardClientHeaders() bool {
	return os.Getenv("LITEPAN_BENCH_FORWARD") == "1"
}
