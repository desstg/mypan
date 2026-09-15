package strm

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type ReplaceTokenResult struct {
	Total   int `json:"total"`
	Matched int `json:"matched"`
	Updated int `json:"updated"`
}

func ReplaceTokenInFiles(strmDir, oldToken, newToken string, secret []byte) (ReplaceTokenResult, error) {
	var result ReplaceTokenResult
	oldToken = strings.TrimSpace(oldToken)
	newToken = strings.TrimSpace(newToken)
	if newToken == "" {
		return result, fmt.Errorf("new token required")
	}
	root := strings.TrimSpace(strmDir)
	if root == "" {
		root = "strm"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return result, err
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			return nil
		}
		result.Total++
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		if len(lines) == 0 {
			return nil
		}
		first := strings.TrimSpace(lines[0])
		if first == "" {
			return nil
		}
		replaced, matched := replaceTokenInLine(first, oldToken, newToken, secret)
		if !matched {
			return nil
		}
		result.Matched++
		if replaced != first {
			lines[0] = replaced
			out := strings.Join(lines, "\n")
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			if err := os.WriteFile(path, []byte(out), 0o644); err == nil {
				result.Updated++
			}
		}
		return nil
	})
	return result, err
}

func replaceTokenInLine(line, oldToken, newToken string, secret []byte) (string, bool) {
	raw := strings.TrimSpace(line)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" && !strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "/") {
			parsed, err = url.Parse("http://local" + raw)
		}
	}
	if err != nil || parsed == nil {
		return raw, false
	}

	path := parsed.Path
	tokenIdx := strings.Index(path, "/t/")
	if tokenIdx >= 0 {
		rest := path[tokenIdx+3:]
		end := strings.Index(rest, "/n/")
		if end < 0 {
			return raw, false
		}
		current := rest[:end]
		if oldToken != "" && current != oldToken {
			return raw, false
		}
		newPath := path[:tokenIdx+3] + newToken + rest[end:]
		if strings.Contains(newPath, "/s/") {
			newPath = stripSignature(newPath)
			if len(secret) > 0 {
				newPath += "/s/" + SignPath(newPath, secret)
			}
		}
		parsed.Path = newPath
		parsed.RawPath = ""
		return rebuildURL(parsed), true
	}
	return raw, false
}

func stripSignature(path string) string {
	idx := strings.Index(path, "/s/")
	if idx < 0 {
		return path
	}
	return path[:idx]
}

func rebuildURL(u *url.URL) string {
	if u.Host == "local" {
		out := u.Path
		if u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		return out
	}
	return u.String()
}
