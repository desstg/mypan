package dav

import (
	"net/url"
	"path"
	"strings"
)

type ParsedPath struct {
	AccountName string
	RelParts    []string
}

func ParseWebDAVPath(raw string) ParsedPath {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return ParsedPath{}
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	segments := strings.Split(strings.Trim(cleaned, "/"), "/")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		if u, err := url.PathUnescape(seg); err == nil {
			seg = u
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return ParsedPath{}
	}
	if len(out) == 1 {
		return ParsedPath{AccountName: out[0]}
	}
	return ParsedPath{AccountName: out[0], RelParts: out[1:]}
}

func isMacOSMetadataPath(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	name := parts[len(parts)-1]
	if name == ".DS_Store" || strings.HasPrefix(name, "._") {
		return true
	}
	ignored := map[string]struct{}{
		".TemporaryItems": {},
		".Trashes":        {},
		".Spotlight-V100": {},
		".fseventsd":      {},
	}
	for _, p := range parts {
		if _, ok := ignored[p]; ok {
			return true
		}
	}
	return false
}

// stripWebDAVStagingSuffix 去掉 WebDAV 原子上传临时后缀（如 file.img.~#0）。
func stripWebDAVStagingSuffix(name string) (canonical string, staging bool) {
	idx := strings.LastIndex(name, ".~#")
	if idx < 0 {
		return name, false
	}
	suffix := name[idx+3:]
	if suffix == "" {
		return name, false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return name, false
		}
	}
	return name[:idx], true
}

func isStagingMoveToCanonical(src, dst []string) bool {
	if len(src) == 0 || len(dst) == 0 || len(src) != len(dst) {
		return false
	}
	for i := 0; i < len(src)-1; i++ {
		if src[i] != dst[i] {
			return false
		}
	}
	canonical, staging := stripWebDAVStagingSuffix(src[len(src)-1])
	return staging && canonical == dst[len(dst)-1]
}
