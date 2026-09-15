package strmscrape

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func relUnder(root, full string) string {
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	if absFull, err := filepath.Abs(full); err == nil {
		full = absFull
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return filepath.Base(full)
	}
	return rel
}

func isInside(root, full string) bool {
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	if absFull, err := filepath.Abs(full); err == nil {
		full = absFull
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathToItemID(rel string) string {
	sum := sha1.Sum([]byte(filepath.ToSlash(rel)))
	return hex.EncodeToString(sum[:8])
}

func pathEscape(p string) string {
	return url.QueryEscape(filepath.ToSlash(p))
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}
