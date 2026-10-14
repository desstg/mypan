package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/strmscrape"
)

// 海报墙「本地目录」注册项的增删查。
//
// 这些目录**不在任何 STRM 任务之下**（是别的工具产出的媒体库目录），所以不能靠
// 任务反查；用户注册一份，墙的下拉里就多一个可选项。详见 strmscrape/wall_local.go
// 与 settings/wallLocalDirs 的长注释。
//
// ⚠️ DELETE **只删注册项，不删磁盘** —— 名字里带 local-dirs 容易被理解成「管理这些
// 目录」，所以接口注释与前端文案都要说清这一点。

type wallLocalDirList struct {
	Dirs []settings.WallLocalDir `json:"dirs"`
}

func (h *Handler) listWallLocalDirs(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	dirs, err := h.strmScrape.ListWallLocalDirs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if dirs == nil {
		dirs = []settings.WallLocalDir{}
	}
	writeOK(w, wallLocalDirList{Dirs: dirs})
}

func (h *Handler) createWallLocalDir(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body struct {
		Root      string `json:"root"`
		Name      string `json:"name"`
		MediaKind string `json:"media_kind"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	dirs, err := h.strmScrape.CreateWallLocalDir(r.Context(), strmscrape.WallLocalDirInput{Root: body.Root, Name: body.Name, MediaKind: body.MediaKind})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, wallLocalDirList{Dirs: dirs})
}

func (h *Handler) updateWallLocalDir(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 id"))
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	dirs, err := h.strmScrape.UpdateWallLocalDir(r.Context(), id, body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, wallLocalDirList{Dirs: dirs})
}

func (h *Handler) deleteWallLocalDir(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 id"))
		return
	}
	dirs, err := h.strmScrape.DeleteWallLocalDir(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, wallLocalDirList{Dirs: dirs})
}
