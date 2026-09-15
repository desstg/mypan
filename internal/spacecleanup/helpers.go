package spacecleanup

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var systemJunkNames = map[string]struct{}{
	".DS_Store":   {},
	"Thumbs.db":   {},
	"thumbs.db":   {},
	"desktop.ini": {},
	".directory":  {},
	".hidden":     {},
}

func isSystemJunk(name string) bool {
	if _, ok := systemJunkNames[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "._") && len(name) > 2
}

func isHexTaskDir(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, ch := range name {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}

func cleanPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "" && path != "." {
			out[path] = struct{}{}
		}
	}
	return out
}

func pathWithin(root, target string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	target = filepath.Clean(strings.TrimSpace(target))
	if root == "" || root == "." || target == "" || target == "." {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func directChildOf(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	return filepath.Dir(target) == root && pathWithin(root, target)
}

func pathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return pathWithin(a, b) || pathWithin(b, a)
}

type treeStats struct {
	bytes int64
	files int64
	dirs  int64
}

func inspectTree(path string) (treeStats, error) {
	var out treeStats
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			out.files++
			return nil
		}
		if entry.IsDir() {
			out.dirs++
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out.files++
		out.bytes += info.Size()
		return nil
	})
	return out, err
}

func itemID(kind, target string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + filepath.Clean(target)))
	return hex.EncodeToString(sum[:12])
}

func sortItems(items []planItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return categoryOrder(items[i].Category) < categoryOrder(items[j].Category)
		}
		if items[i].Risk != items[j].Risk {
			return riskOrder(items[i].Risk) < riskOrder(items[j].Risk)
		}
		return items[i].Path < items[j].Path
	})
}

func categoryOrder(category string) int {
	switch category {
	case CategoryStrm:
		return 0
	case CategoryTemp:
		return 1
	case CategoryLogs:
		return 2
	case CategoryCache:
		return 3
	case CategoryDatabase:
		return 4
	default:
		return 99
	}
}

func riskOrder(risk string) int {
	switch risk {
	case RiskSafe:
		return 0
	case RiskReview:
		return 1
	case RiskRebuild:
		return 2
	case RiskLocking:
		return 3
	default:
		return 99
	}
}
