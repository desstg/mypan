package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
)

type addOfflineURLsReq struct {
	AccountID         int64    `json:"account_id"`
	ProviderKind      string   `json:"provider_kind"`
	URLs              []string `json:"urls"`
	FileName          string   `json:"file_name"`
	TargetParentID    string   `json:"target_parent_id"`
	TargetDisplayPath string   `json:"target_display_path"`
}

type addOfflineTorrentReq struct {
	AccountID         int64  `json:"account_id"`
	PreparationID     string `json:"preparation_id"`
	Wanted            []int  `json:"wanted"`
	TargetParentID    string `json:"target_parent_id"`
	TargetDisplayPath string `json:"target_display_path"`
	SavePath          string `json:"save_path"`
}

type batchDeleteOfflineTasksReq struct {
	TaskIDs []string `json:"task_ids"`
}

const maxOfflineTorrentSize = 16 << 20

func (h *Handler) offlineDownloadCapabilities(w http.ResponseWriter, r *http.Request) {
	accountID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	capabilities, err := h.offlineDownloads.Capabilities(r.Context(), accountID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取离线下载能力成功", Data: capabilities})
}

func (h *Handler) addOfflineURLs(w http.ResponseWriter, r *http.Request) {
	var req addOfflineURLsReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	tasks, err := h.offlineDownloads.AddURLs(r.Context(), offlinedownload.AddURLParams{
		AccountID:         req.AccountID,
		ProviderKind:      req.ProviderKind,
		URLs:              req.URLs,
		FileName:          req.FileName,
		TargetParentID:    req.TargetParentID,
		TargetDisplayPath: req.TargetDisplayPath,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "离线下载任务已提交", Data: tasks})
}

func (h *Handler) prepareOfflineTorrent(w http.ResponseWriter, r *http.Request) {
	if h.uploads == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOfflineTorrentSize+(1<<20))
	if err := r.ParseMultipartForm(maxOfflineTorrentSize + (1 << 20)); err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "解析 BT 种子失败，文件不能超过 16 MiB"))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	accountID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("account_id")), 10, 64)
	if err != nil || accountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请选择 .torrent 种子文件"))
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".torrent") {
		writeErr(w, domain.Errorf(domain.CodeValidation, "只支持 .torrent 种子文件"))
		return
	}
	tempPath, total, err := saveUploadTemp(h.uploads.TempDir(), file, header.Filename)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer os.Remove(tempPath)
	if total > maxOfflineTorrentSize {
		writeErr(w, domain.Errorf(domain.CodeValidation, "BT 种子文件不能超过 16 MiB"))
		return
	}
	result, err := h.offlineDownloads.PrepareTorrent(r.Context(), accountID, tempPath, header.Filename)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "BT 种子解析成功", Data: result})
}

func (h *Handler) addOfflineTorrent(w http.ResponseWriter, r *http.Request) {
	var req addOfflineTorrentReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.offlineDownloads.AddTorrent(r.Context(), offlinedownload.AddTorrentParams{
		AccountID:         req.AccountID,
		PreparationID:     req.PreparationID,
		Wanted:            req.Wanted,
		TargetParentID:    req.TargetParentID,
		TargetDisplayPath: req.TargetDisplayPath,
		SavePath:          req.SavePath,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "BT 离线下载任务已提交", Data: task})
}

func (h *Handler) listOfflineDownloadTasks(w http.ResponseWriter, r *http.Request) {
	var accountID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
			return
		}
		accountID = id
	}
	refresh := !strings.EqualFold(r.URL.Query().Get("refresh"), "false")
	tasks, err := h.offlineDownloads.List(r.Context(), accountID, refresh)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取离线下载任务成功", Data: tasks})
}

func (h *Handler) refreshOfflineDownloadTasks(w http.ResponseWriter, r *http.Request) {
	var accountID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
			return
		}
		accountID = id
	}
	if err := h.offlineDownloads.Refresh(r.Context(), accountID, true); err != nil {
		writeErr(w, err)
		return
	}
	tasks, err := h.offlineDownloads.List(r.Context(), accountID, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "离线下载任务已刷新", Data: tasks})
}

func (h *Handler) deleteOfflineDownloadTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
	if err := h.offlineDownloads.Delete(r.Context(), taskID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "离线下载任务已删除"})
}

func (h *Handler) batchDeleteOfflineDownloadTasks(w http.ResponseWriter, r *http.Request) {
	var req batchDeleteOfflineTasksReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result := h.offlineDownloads.BatchDelete(r.Context(), req.TaskIDs)
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "批量删除离线下载任务完成", Data: result})
}
