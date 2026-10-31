package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/fusemount"
)

type localDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type localBrowseResult struct {
	Path     string          `json:"path"`
	Parent   *string         `json:"parent"`
	Dirs     []localDirEntry `json:"dirs"`
	Exists   bool            `json:"exists"`
	Writable bool            `json:"writable"`
}

// browseDefaultPath 返回本地目录浏览器默认落点：
// STRM 输出目录、数据目录、FUSE 挂载根优先，再回退到常见挂载位置。
func (h *Handler) browseDefaultPath() string {
	seen := map[string]struct{}{}
	var candidates []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		candidates = append(candidates, p)
	}
	add(h.strmDir)
	add(h.dataDir)
	add(fusemount.MountRoot)
	for _, p := range []string{"/data", "/mnt", "/media", "/"} {
		add(p)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return "/"
}

// isBrowsableAbsPath 判断一个前端传来的路径是不是「绝对路径」。
//
// 不能只看 `/` 开头：Windows 上 `filepath.IsAbs("/app")` 是 false（没有卷名），
// 而前端那排常用位置（`/app/strm` `/mnt` …）是容器里的写法。反过来
// `E:\icons` 在 Windows 上是绝对路径、在 Linux 上不是。两边都收，
// 交给 `filepath.Abs` 各自解析。
//
// 2026-10-31：原来只认 `/` 开头，于是 Windows 上（含发出去的
// `mypan-*-windows-amd64.exe`）这个目录选择器**一打开就报错**：
// `browseDefaultPath()` 在 Windows 上返回 `E:\...`，自己就被自己的校验拒了。
func isBrowsableAbsPath(p string) bool {
	return strings.HasPrefix(p, "/") || filepath.IsAbs(p)
}

func (h *Handler) browseLocalFS(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		raw = h.browseDefaultPath()
	}
	if !isBrowsableAbsPath(raw) {
		writeJSON(w, http.StatusOK, Resp{
			Success:   false,
			Message:   "请使用绝对路径（以 / 开头，或 Windows 上的 E:\\… 这种写法）",
			ErrorType: string(domain.CodeValidation),
		})
		return
	}

	target, err := filepath.Abs(raw)
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeValidation, err))
		return
	}
	target = filepath.Clean(target)

	parent := parentPath(target)
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, Resp{
				Success: false,
				Message: "目录不存在: " + target,
				Data: localBrowseResult{
					Path:   target,
					Parent: parent,
					Dirs:   []localDirEntry{},
					Exists: false,
				},
			})
			return
		}
		writeErr(w, domain.Wrap(domain.CodeDriverError, err))
		return
	}
	if !info.IsDir() {
		writeJSON(w, http.StatusOK, Resp{
			Success:   false,
			Message:   "该路径不是目录: " + target,
			ErrorType: string(domain.CodeValidation),
		})
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsPermission(err) {
			writeJSON(w, http.StatusOK, Resp{
				Success:   false,
				Message:   "无读取权限: " + target,
				ErrorType: string(domain.CodePermissionDenied),
			})
			return
		}
		writeErr(w, domain.Wrap(domain.CodeDriverError, err))
		return
	}

	dirs := make([]localDirEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, localDirEntry{
			Name: e.Name(),
			Path: filepath.Join(target, e.Name()),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	writeOK(w, localBrowseResult{
		Path:     target,
		Parent:   parent,
		Dirs:     dirs,
		Exists:   true,
		Writable: true,
	})
}

func parentPath(path string) *string {
	clean := filepath.Clean(path)
	if clean == "/" {
		return nil
	}
	p := filepath.Dir(clean)
	return &p
}
