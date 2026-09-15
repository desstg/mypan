package httpx

import (
	"io"
	"strings"
)

const DefaultReadLimit = 8 << 20

func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	return io.ReadAll(io.LimitReader(r, limit))
}

func Truncate(b []byte, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 300
	}
	s := strings.TrimSpace(string(b))
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
