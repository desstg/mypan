package api

import (
	"context"
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/fusemount"
)

type fuseMountDTO struct {
	ID          int64  `json:"id,omitempty"`
	Name        string `json:"name"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name,omitempty"`
	RootItemID  string `json:"root_item_id"`
	RootPath    string `json:"root_path"`
	MountPoint  string `json:"mount_point"`
	ReadOnly    bool   `json:"read_only"`
	AutoMount   bool   `json:"auto_mount"`
	UID         uint32 `json:"uid"`
	GID         uint32 `json:"gid"`
	DirMode     string `json:"dir_mode"`
	FileMode    string `json:"file_mode"`
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"`
	LastError   string `json:"last_error,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

type fuseConfigDTO struct {
	Enabled   bool    `json:"enabled"`
	MountRoot *string `json:"mount_root,omitempty"`
}

func (h *Handler) fuseStatus(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	writeOK(w, h.fuse.Status(r.Context()))
}

func (h *Handler) listFuseMounts(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	list, err := h.fuse.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	accNames := h.accountNameMap(r.Context())
	out := make([]fuseMountDTO, 0, len(list))
	for _, m := range list {
		out = append(out, toFuseMountDTO(m, accNames[m.AccountID]))
	}
	writeOK(w, out)
}

func (h *Handler) createFuseMount(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	var in fuseMountDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	m, err := fromFuseMountDTO(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	created, err := h.fuse.Create(r.Context(), m)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toFuseMountDTO(created, h.accountName(r.Context(), created.AccountID)))
}

func (h *Handler) updateFuseMount(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的挂载 ID"))
		return
	}
	var in fuseMountDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	m, err := fromFuseMountDTO(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	m.ID = id
	updated, err := h.fuse.Update(r.Context(), m)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, toFuseMountDTO(updated, h.accountName(r.Context(), updated.AccountID)))
}

func (h *Handler) deleteFuseMount(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的挂载 ID"))
		return
	}
	if err := h.fuse.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": true})
}

func (h *Handler) mountFuse(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的挂载 ID"))
		return
	}
	if err := h.fuse.Mount(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	m, _ := h.fuse.Get(r.Context(), id)
	writeOK(w, toFuseMountDTO(m, h.accountName(r.Context(), m.AccountID)))
}

func (h *Handler) unmountFuse(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "无效的挂载 ID"))
		return
	}
	if err := h.fuse.Unmount(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	m, _ := h.fuse.Get(r.Context(), id)
	writeOK(w, toFuseMountDTO(m, h.accountName(r.Context(), m.AccountID)))
}

func (h *Handler) updateFuseConfig(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.fuse != nil) {
		return
	}
	var in fuseConfigDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.fuse.SetConfig(r.Context(), in.Enabled, in.MountRoot); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, h.fuse.Status(r.Context()))
}

func toFuseMountDTO(m *domain.FuseMount, accountName string) fuseMountDTO {
	if m == nil {
		return fuseMountDTO{}
	}
	return fuseMountDTO{
		ID:          m.ID,
		Name:        m.Name,
		AccountID:   m.AccountID,
		AccountName: accountName,
		RootItemID:  m.RootItemID,
		RootPath:    m.RootPath,
		MountPoint:  m.MountPoint,
		ReadOnly:    m.ReadOnly,
		AutoMount:   m.AutoMount,
		UID:         m.UID,
		GID:         m.GID,
		DirMode:     fusemount.FormatMode(m.DirMode),
		FileMode:    fusemount.FormatMode(m.FileMode),
		Enabled:     m.Enabled,
		State:       m.State,
		LastError:   m.LastError,
		SortOrder:   m.SortOrder,
	}
}

func fromFuseMountDTO(in fuseMountDTO) (*domain.FuseMount, error) {
	dirMode, err := fusemount.ParseModeOctal(in.DirMode, 0o755)
	if err != nil {
		return nil, err
	}
	fileMode, err := fusemount.ParseModeOctal(in.FileMode, 0o644)
	if err != nil {
		return nil, err
	}
	mp := strings.TrimSpace(in.MountPoint)
	if mp == "" && strings.TrimSpace(in.Name) != "" {
		mp = fusemount.MountRoot + "/" + strings.TrimSpace(in.Name)
	}
	return &domain.FuseMount{
		Name:       strings.TrimSpace(in.Name),
		AccountID:  in.AccountID,
		RootItemID: strings.TrimSpace(in.RootItemID),
		RootPath:   strings.TrimSpace(in.RootPath),
		MountPoint: mp,
		ReadOnly:   in.ReadOnly || true,
		AutoMount:  in.AutoMount,
		UID:        in.UID,
		GID:        in.GID,
		DirMode:    dirMode,
		FileMode:   fileMode,
		Enabled:    in.Enabled || true,
		SortOrder:  in.SortOrder,
	}, nil
}

func (h *Handler) accountNameMap(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	if h.accountSvc == nil {
		return out
	}
	list, err := h.accountSvc.List(ctx)
	if err != nil {
		return out
	}
	for _, v := range list {
		if v.Account != nil {
			out[v.Account.ID] = v.Account.Name
		}
	}
	return out
}

func (h *Handler) accountName(ctx context.Context, id int64) string {
	return h.accountNameMap(ctx)[id]
}
