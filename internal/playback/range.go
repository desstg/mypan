package playback

import (
	"fmt"
	"strconv"
	"strings"
)

func parseSingleRange(header string, size int64) (start, end int64, err error) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), "bytes=") {
		return 0, 0, fmt.Errorf("invalid range")
	}
	spec := strings.TrimSpace(header[6:])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, fmt.Errorf("multipart range not supported")
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range")
	}
	if parts[0] == "" {
		// suffix: bytes=-500
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, fmt.Errorf("invalid suffix range")
		}
		if size <= 0 {
			return 0, 0, fmt.Errorf("unknown size")
		}
		start = size - suffix
		if start < 0 {
			start = 0
		}
		return start, size - 1, nil
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("invalid range start")
	}
	if parts[1] == "" {
		if size <= 0 {
			return start, start, nil
		}
		return start, size - 1, nil
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, fmt.Errorf("invalid range end")
	}
	if size > 0 && end >= size {
		end = size - 1
	}
	return start, end, nil
}
